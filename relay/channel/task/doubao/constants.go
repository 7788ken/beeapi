package doubao

import (
	"fmt"
	"strconv"
	"strings"
)

var ModelList = []string{
	"doubao-seedance-1-0-pro-250528",
	"doubao-seedance-1-0-lite-t2v",
	"doubao-seedance-1-0-lite-i2v",
	"doubao-seedance-1-5-pro-251215",
	"doubao-seedance-2-0-260128",
	"doubao-seedance-2-0-fast-260128",
}

var ChannelName = "doubao-video"

type videoPriceKey struct {
	resolution string
	hasVideo   bool
}

// videoPriceTable 各模型族按「输出分辨率 × 是否带参考视频」的单价表（USD / 百万 token）。
// 计费只用表内单价之间的比值：ModelRatio 由管理员按 base 档（480p/720p、不带视频）配置，
// 其余档位换算成相对 base 的系数乘上去，所以表里数字的绝对值不直接进账，但必须同一口径。
//
// 数值取 BytePlus ModelArk 定价页（2026-09-22 更新）与上游 service-inference 报价单的划线价，
// 两者对 2.0 / 2.5 一致；fast、mini 上游按 BytePlus 限时折扣价（至 2026-10-07）报，此处照上游。
// 2.5 的 1080p 上游报 8.42/5.04 低于自家 720p，疑为标错，暂按 BytePlus 官方 11.7/7.0。
var videoPriceTable = map[string]map[videoPriceKey]float64{
	"seedance-2-0": {
		{resolution: "base", hasVideo: false}:  7.0,
		{resolution: "base", hasVideo: true}:   4.3,
		{resolution: "1080p", hasVideo: false}: 7.7,
		{resolution: "1080p", hasVideo: true}:  4.7,
		{resolution: "4k", hasVideo: false}:    4.0,
		{resolution: "4k", hasVideo: true}:     2.4,
	},
	"seedance-2-0-fast": {
		{resolution: "base", hasVideo: false}: 4.20,
		{resolution: "base", hasVideo: true}:  2.48,
	},
	"seedance-2-0-mini": {
		{resolution: "base", hasVideo: false}: 1.40,
		{resolution: "base", hasVideo: true}:  0.84,
	},
	"seedance-2-5": {
		{resolution: "base", hasVideo: false}:  10.7,
		{resolution: "base", hasVideo: true}:   6.4,
		{resolution: "1080p", hasVideo: false}: 11.7,
		{resolution: "1080p", hasVideo: true}:  7.0,
	},
}

// canonicalSeedanceModel 把各种 seedance 模型别名归一为"模型族"键（videoPriceTable 的键）：
// 去除 doubao-/dreamina- 前缀、末尾的 -max 线路后缀（max 与 hc/df 折扣不变），
// 以及末尾的 -<6位及以上数字> 版本号后缀。
//
//	doubao-seedance-2-0            -> seedance-2-0
//	doubao-seedance-2-0-260128     -> seedance-2-0
//	dreamina-seedance-2-0          -> seedance-2-0
//	doubao-seedance-2-0-fast-260128 -> seedance-2-0-fast
//	dreamina-seedance-2-0-260128-max      -> seedance-2-0
//	dreamina-seedance-2-0-fast-260128-max -> seedance-2-0-fast
//
// 未知/未配置的变体（如 filter-off）归一后不在 videoPriceTable 中，
// GetVideoInputRatio 返回 false，按基础价计费（保守，避免错误折扣）。
func canonicalSeedanceModel(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.TrimPrefix(s, "doubao-")
	s = strings.TrimPrefix(s, "dreamina-")
	s = strings.TrimSuffix(s, "-max") // 线路后缀先于版本号剥离：…-260128-max → …-260128 → 模型族
	if i := strings.LastIndex(s, "-"); i >= 0 {
		if suffix := s[i+1:]; len(suffix) >= 6 && isAllDigits(suffix) {
			s = s[:i]
		}
	}
	return s
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// GetVideoInputRatio 返回 base 档带参考视频相对不带视频的折扣系数（含视频单价 / 不含视频单价），
// 直接从 videoPriceTable 推导，避免两处维护不一致。未配置的模型族返回 false。
func GetVideoInputRatio(modelName string) (float64, bool) {
	prices, ok := videoPriceTable[canonicalSeedanceModel(modelName)]
	if !ok {
		return 0, false
	}
	base := prices[videoPriceKey{resolution: "base", hasVideo: false}]
	withVideo, ok := prices[videoPriceKey{resolution: "base", hasVideo: true}]
	if !ok || base <= 0 {
		return 0, false
	}
	return withVideo / base, true
}

func GetSeedanceBillingRatio(modelName, resolution string, hasVideo bool) (float64, bool) {
	prices, ok := videoPriceTable[canonicalSeedanceModel(modelName)]
	if !ok {
		return 0, false
	}
	base := prices[videoPriceKey{resolution: "base", hasVideo: false}]
	if base <= 0 {
		return 0, false
	}
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	switch resolution {
	case "", "480p", "720p":
		resolution = "base"
	}
	price, ok := prices[videoPriceKey{resolution: resolution, hasVideo: hasVideo}]
	if !ok {
		return 1, true
	}
	return price / base, true
}

// ===== Seedance 参数白名单（用于 ValidateRequestAndSetAction 前置校验） =====

// seedance 时长允许范围（火山方舟 t2v 硬限制）
const (
	seedanceDurationMin = 4
	seedanceDurationMax = 15
)

// seedanceExtraDurations 在标准 4~15s 之外，按模型额外放行的精确时长值。
//
// 用精确值白名单而非放宽上限：上游只多支持 30s 这一档，16~29s 仍会被拒，
// 放宽成区间只会让中间值透到上游换回 5xx，再触发 newapi 重试白烧配额。
//
// 键为客户调用时使用的模型名（小写）—— 时长校验在 ValidateRequestAndSetAction 里执行，
// 早于 relay_task.go 的 ModelMappedHelper，此处拿不到渠道映射后的上游名。
// 因此站点若对该模型做了重命名，这里要登记对外名。
var seedanceExtraDurations = map[string][]int{
	"dreamina-seedance-2-5-hc": {30},
}

// seedanceDurationMaxOverrides 按模型放宽的连续区间上限（覆盖默认 seedanceDurationMax）。
// 火山方舟 Seedance 2.5 官方时长范围为 4~30s；2.5-max 线路上游已确认接受该区间，
// 故对它放开整段 16~30s，而不是像 2-5-hc 那样只登记精确值 30。
// 键同 seedanceExtraDurations：客户调用时使用的模型名（小写）。
var seedanceDurationMaxOverrides = map[string]int{
	"dreamina-seedance-2-5-260628-max": 30,
}

// seedanceDurationRange 返回该模型接受的连续时长区间 [min, max]。
func seedanceDurationRange(modelName string) (int, int) {
	if max, ok := seedanceDurationMaxOverrides[normalizeSeedanceModelKey(modelName)]; ok {
		return seedanceDurationMin, max
	}
	return seedanceDurationMin, seedanceDurationMax
}

// normalizeSeedanceModelKey 归一模型名用于白名单查表。
// 只做大小写和空白处理，不复用 canonicalSeedanceModel 的族归并 ——
// 族归并会把同族里未开放 30s 的其他变体一并放行。
func normalizeSeedanceModelKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// seedanceAdaptiveDuration 视频编辑任务的时长哨兵：上游要求 duration=-1（配合 ratio=adaptive），
// 成片时长由模型按被编辑的原视频自适应决定。与 relaycommon.AdaptiveTaskDuration 同值。
const seedanceAdaptiveDuration = -1

// supportsSeedanceAdaptiveDuration 判断该模型是否支持 duration=-1（视频编辑 / 智能时长）。
// 火山方舟文档：Seedance 2.5 系列支持视频编辑与 -1 智能时长；2.0 及更早版本不支持
// （2.0 上传原片会被上游识别为编辑任务后以 TaskTypeConstraint 拒绝，或 -1 直接 400）。
// 按模型族归一（canonicalSeedanceModel）判断，dreamina-/doubao- 前缀、-max/-hc/-ep 线路后缀均可命中。
func supportsSeedanceAdaptiveDuration(modelName string) bool {
	return strings.HasPrefix(canonicalSeedanceModel(modelName), "seedance-2-5")
}

// isSeedanceDurationAllowed 判断该模型是否接受此时长：
// 落在该模型的连续区间（默认 4~15s，见 seedanceDurationMaxOverrides），命中 seedanceExtraDurations 里为该模型登记的额外值，
// 或为 -1 且模型支持自适应时长（视频编辑）。
func isSeedanceDurationAllowed(modelName string, sec int) bool {
	if min, max := seedanceDurationRange(modelName); sec >= min && sec <= max {
		return true
	}
	if sec == seedanceAdaptiveDuration {
		return supportsSeedanceAdaptiveDuration(modelName)
	}
	for _, extra := range seedanceExtraDurations[normalizeSeedanceModelKey(modelName)] {
		if sec == extra {
			return true
		}
	}
	return false
}

// seedanceDurationError 生成时长错误，带上该模型实际可用的取值，便于客户自查。
func seedanceDurationError(modelName string, sec int) error {
	if sec == seedanceAdaptiveDuration {
		return fmt.Errorf("duration=-1 (adaptive, video editing) is only supported on seedance 2.5 models; model %q does not support it", modelName)
	}
	extras := seedanceExtraDurations[normalizeSeedanceModelKey(modelName)]
	if supportsSeedanceAdaptiveDuration(modelName) {
		extras = append(append([]int{}, extras...), seedanceAdaptiveDuration)
	}
	min, max := seedanceDurationRange(modelName)
	if len(extras) == 0 {
		return fmt.Errorf("duration must be between %d and %d seconds, got %d",
			min, max, sec)
	}
	parts := make([]string, 0, len(extras))
	for _, e := range extras {
		parts = append(parts, strconv.Itoa(e))
	}
	return fmt.Errorf("duration must be between %d and %d seconds, or one of [%s] for model %q, got %d",
		min, max, strings.Join(parts, ", "), modelName, sec)
}

var validSeedanceRatios = map[string]struct{}{
	"16:9": {}, "9:16": {}, "1:1": {}, "4:3": {}, "3:4": {}, "21:9": {}, "adaptive": {},
}

var validSeedanceResolutions = map[string]struct{}{
	"480p": {}, "720p": {}, "1080p": {}, "4k": {},
}

// fastSeedanceModels 显式列出不支持 1080p 的 fast 变体。
// 用显式 map 而非 strings.Contains(name, "fast") 以避免误判未来命名变体。
var fastSeedanceModels = map[string]struct{}{
	"doubao-seedance-2-0-fast-260128": {},
}

// topLevelMustGoToMetadata 列出"放在请求体顶层不会被 adapter 消费"的字段。
// 命中即返回 400，提示客户挪到 metadata 对象，避免静默丢字段导致排查无门。
// 不包含 seconds/duration —— 这两个字段在 TaskSubmitReq 顶层有专门支持。
var topLevelMustGoToMetadata = []string{
	"resolution",
	"ratio",
	"generate_audio",
	"watermark",
	"size",
	"seed",
	"frames",
	"camera_fixed",
	"return_last_frame",
	"callback_url",
	"service_tier",
	"draft",
	"safety_identifier",
	"priority",
}

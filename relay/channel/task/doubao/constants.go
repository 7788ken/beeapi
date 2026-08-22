package doubao

import "strings"

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

var videoPriceTable = map[string]map[videoPriceKey]float64{
	"seedance-2-0": {
		{resolution: "base", hasVideo: false}:  46,
		{resolution: "base", hasVideo: true}:   28,
		{resolution: "1080p", hasVideo: false}: 51,
		{resolution: "1080p", hasVideo: true}:  31,
		{resolution: "4k", hasVideo: false}:    26,
		{resolution: "4k", hasVideo: true}:     16,
	},
	"seedance-2-0-fast": {
		{resolution: "base", hasVideo: false}: 37,
		{resolution: "base", hasVideo: true}:  22,
	},
}

// videoInputRatioMap 视频输入折扣比率（含视频单价 / 不含视频单价）。
// 管理员应将 ModelRatio 设置为"不含视频"的较高费率，
// 系统在检测到视频输入时自动乘以此折扣。
//
// 键使用"模型族"规范名（见 canonicalSeedanceModel），不耦合具体发布版本号，
// 这样 doubao-seedance-2-0 / doubao-seedance-2-0-260128 / dreamina-seedance-2-0
// 等各种对外名或映射名都能命中同一折扣。
var videoInputRatioMap = map[string]float64{
	"seedance-2-0":      28.0 / 46.0, // ~0.6087
	"seedance-2-0-fast": 22.0 / 37.0, // ~0.5946
}

// canonicalSeedanceModel 把各种 seedance 模型别名归一为"模型族"键：
// 去除 doubao-/dreamina- 前缀，以及末尾的 -<6位及以上数字> 版本号后缀。
//
//	doubao-seedance-2-0            -> seedance-2-0
//	doubao-seedance-2-0-260128     -> seedance-2-0
//	dreamina-seedance-2-0          -> seedance-2-0
//	doubao-seedance-2-0-fast-260128 -> seedance-2-0-fast
//
// 未知/未配置折扣的变体（如 filter-off、mini）归一后不在 videoInputRatioMap 中，
// GetVideoInputRatio 返回 false，按基础价计费（保守，避免错误折扣）。
func canonicalSeedanceModel(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.TrimPrefix(s, "doubao-")
	s = strings.TrimPrefix(s, "dreamina-")
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

func GetVideoInputRatio(modelName string) (float64, bool) {
	r, ok := videoInputRatioMap[canonicalSeedanceModel(modelName)]
	return r, ok
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

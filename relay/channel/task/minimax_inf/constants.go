package minimax_inf

import "strings"

// MiniMax 视频生成（service-inference 网关）渠道，渠道类型 61。
// 文档：https://console.service-inference.ai/docs/minimax
//
// 上游 BASE_URL 为 https://model.service-inference.ai，走该网关的 sd v1 线协议
// （与渠道类型 58 同族）：提交 POST /v1/video/generate、轮询 GET /v1/video/tasks/{task_id}，
// 响应统一包在 {"task":{...}} 信封里，视频地址取 task.outputs[0]；
// MiniMax 原生字段（resolution / usage / ratio 等）被网关收进 task.metadata。
//
// 注意：这与渠道类型 35（hailuo，MiniMax 官方 /v1/video_generation + base_resp 状态码）
// 是两套完全不同的协议，不可混用。
//
// ⚠ 上述文档在模型清单、分辨率档位、单价与「content 支持哪些元素」上均与网关实际
// 行为不符（详见 modelSpecs、resolutionBillingRatios 与下方素材常量的注释）。本文件的
// 能力边界一律以 GET /v1/models 与提交端点的 400 报错原文为准，不以文档为准。
//
// 尤其是文档的「参数说明」只列了 text 与 image_url 两种 content 元素，但同一份文档的
// 「输入素材计费」章节明确列了音频（免费）与视频（按输入时长计费），响应体
// task.metadata.usage 也带 input_seconds —— 计费章节才是能力的真实线索。
// 2026-09-12 零成本实测已确认 video_url / audio_url / first_frame / middle_frame /
// last_frame 全部被上游原生解析，且上游 new-api 官方仓库的 MiniMax-H3 实现
// （plugins/tasks/hailuo/plugin.js）给出的数量上限与实测完全一致。

const ChannelName = "minimax-inf-video"

// 网关开放的 MiniMax 视频模型，模型名固定为全小写。
//
// 实测依据（2026-09-06，GET /v1/models + 提交端点 400 报错原文）：
//
//	minimax-h3      supported resolutions: 480P, 768P, 2K；supported durations: 4s…15s
//	minimax-h3-max  supported resolutions: 480P, 768P；   supported durations: 5s…15s
//
// 上游文档「使用限制」写的是"模型 仅 minimax-h3"，那是举例而非穷举——
// /v1/models 明确返回上述两个模型，且 h3-max 实测能通过模型校验。
const (
	modelMinimaxH3    = "minimax-h3"
	modelMinimaxH3Max = "minimax-h3-max"
)

// supportedModels 客户名/映射名（小写）→ 上游规范名。
var supportedModels = map[string]string{
	modelMinimaxH3:    modelMinimaxH3,
	modelMinimaxH3Max: modelMinimaxH3Max,
}

var ModelList = []string{
	modelMinimaxH3,
	modelMinimaxH3Max,
}

const (
	// GenerateEndpointFmt 提交任务：POST {base}/v1/video/generate
	GenerateEndpointFmt = "%s/v1/video/generate"
	// QueryEndpointFmt 轮询任务：GET {base}/v1/video/tasks/{task_id}
	// 仅支持 GET；POST /v1/video/tasks 不存在，会返回 404。
	QueryEndpointFmt = "%s/v1/video/tasks/%s"
)

// 网关任务状态流转：pending（排队）→ processing（生成中）→ completed（完成）。
// 中间可能短暂出现 status 为空字符串的过渡态，按"未完成"继续轮询即可。
const (
	statusPending    = "pending"
	statusProcessing = "processing"
	statusCompleted  = "completed"
	statusFailed     = "failed"
)

// 分辨率档位（上游枚举，大小写敏感）。各模型支持哪几档见 modelSpecs。
const (
	resolution480P = "480P"
	resolution768P = "768P"
	resolution2K   = "2K"
)

const (
	ratioAdaptive = "adaptive"

	// defaultDuration 客户未指定时长时的默认值。取 5 是因为它同时落在
	// h3（4–15s）与 h3-max（5–15s）的合法区间内。
	defaultDuration = 5

	// defaultResolution 客户未指定分辨率时显式下发 768P，
	// 而不是省略字段让上游自选——省略会导致预扣费无法确定倍率。
	// 768P 是两个模型都支持、且计费基准档所在的那一档。
	defaultResolution = resolution768P
)

// validRatios 画面比例白名单。adaptive 仅在带视觉素材（参考图/参考视频/首尾帧）时可用，
// 文生视频必填且不能为 adaptive（场景相关校验见 adaptor.go）。
// 与上游 new-api 的 H3_RATIOS 一致。
var validRatios = map[string]struct{}{
	"16:9": {}, "4:3": {}, "1:1": {}, "3:4": {}, "9:16": {}, "21:9": {},
	ratioAdaptive: {},
}

// ============================
// content[] 元素类型与素材角色
// ============================

// content 元素类型。上游对不认识的 type 是「静默丢弃」而非报错——塞一个不存在的
// type 会让请求退化成纯文本并触发 t2va 的 ratio 校验，故本地必须自己把住白名单，
// 否则客户写错类型只会得到一条莫名其妙的 ratio 报错。
const (
	contentTypeText     = "text"
	contentTypeImageURL = "image_url"
	contentTypeVideoURL = "video_url"
	contentTypeAudioURL = "audio_url"
)

// 素材角色。分两个互斥的模式：
//
//	帧模式   first_frame / middle_frame / last_frame —— 用首尾（中）帧约束画面
//	参考模式 reference_image / reference_video / reference_audio —— 用素材驱动主体
//
// 上游报错原文："reference mode cannot be mixed with
// first_frame/middle_frame/last_frame; choose one"（错误码 2013，提交期即拒）。
// middle_frame 是文档完全没提、由该报错原文与上游 new-api 实现共同确认存在的角色。
const (
	roleFirstFrame     = "first_frame"
	roleMiddleFrame    = "middle_frame"
	roleLastFrame      = "last_frame"
	roleReferenceImage = "reference_image"
	roleReferenceVideo = "reference_video"
	roleReferenceAudio = "reference_audio"
)

// imageRoleOrder / videoRoleOrder / audioRoleOrder 各 content 类型允许的 role，
// 按固定顺序存放（不直接用 map 是为了错误文案可稳定断言，同 supportedResolutions）。
// role 可省略，省略时按该类型的默认角色处理，见 defaultRoleForType。
//
// 上游并不校验 role 与 type 是否匹配（image_url 配 role=reference_video 也能提交通过，
// 语义实际由 type 决定），本地收紧是为了让客户在本地就拿到准确报错，而不是让上游
// 生成出与预期不符的画面。
var (
	imageRoleOrder = []string{roleReferenceImage, roleFirstFrame, roleMiddleFrame, roleLastFrame}
	videoRoleOrder = []string{roleReferenceVideo}
	audioRoleOrder = []string{roleReferenceAudio}
)

// defaultRoleForType 客户省略 role 时的默认角色。
//
// ⚠ 图片默认按 reference_image，与上游 new-api 的 hailuo 插件不同（它把无 role 的图片
// 当 first_frame）。本站自 20260905R1 起线上就是 reference_image 语义，且本网关文档的
// 「示例：图生视频」也写的是 reference_image，改动会静默改变既有客户的生成效果，故保持。
func defaultRoleForType(typ string) string {
	switch typ {
	case contentTypeImageURL:
		return roleReferenceImage
	case contentTypeVideoURL:
		return roleReferenceVideo
	case contentTypeAudioURL:
		return roleReferenceAudio
	default:
		return ""
	}
}

// rolesForType 取某类型允许的 role 列表（固定顺序，仅用于校验与错误提示）。
func rolesForType(typ string) []string {
	switch typ {
	case contentTypeImageURL:
		return imageRoleOrder
	case contentTypeVideoURL:
		return videoRoleOrder
	case contentTypeAudioURL:
		return audioRoleOrder
	default:
		return nil
	}
}

// isAllowedRole 判定 role 是否属于该类型的白名单。
func isAllowedRole(typ, role string) bool {
	for _, r := range rolesForType(typ) {
		if r == role {
			return true
		}
	}
	return false
}

// 素材数量上限。2026-09-12 实测为提交期强校验（错误码 2013），报错原文：
//
//	at most 9 reference images allowed
//	at most 3 reference videos allowed
//	at most 3 reference audios allowed
//
// 与上游 new-api hailuo 插件的 H3_MAX_REFERENCE_IMAGES / _VIDEOS / _AUDIOS 完全一致，
// 两个模型（h3 与 h3-max）上限相同。
const (
	maxReferenceImages = 9
	maxReferenceVideos = 3
	maxReferenceAudios = 3

	// maxFrameImages 帧模式图片总数上限，对应上游插件的 H3_MAX_FRAME_IMAGES=2。
	// 实测 first_frame + middle_frame + last_frame 三帧同传会被上游拒掉。
	maxFrameImages = 2

	// maxInputVideoSeconds 输入视频时长上限，对应上游插件的 H3_MAX_INPUT_VIDEO_SECONDS=15。
	// 目前仅用于文档记录：提交时拿不到输入视频的真实时长，后续补输入素材计费时
	// 按此上限预留、任务完成后用 usage.input_seconds 冲正（上游插件即此做法）。
	maxInputVideoSeconds = 15
)

// 便捷字段路径下从 metadata 读的素材键名，全部对齐上游 new-api 的 hailuo 插件，
// 使客户在两个渠道类型（35 官方 / 61 本网关）上能用同一套入参写法。
// 参考视频额外支持顶层 videos[]（与 images[] 对称，见 relaycommon.TaskSubmitReq）；
// 参考音频因为 TaskSubmitReq 没有对应字段，只能走 metadata 或顶层 content[]。
const (
	metaFirstFrameImage = "first_frame_image"
	metaLastFrameImage  = "last_frame_image"
	metaReferenceVideo  = "reference_video"
	metaReferenceAudio  = "reference_audio"
)

// modelSpec 每个模型的能力边界，用于前置校验：非法组合直接 400，
// 避免透到上游换 4xx 白烧一次往返。
//
// 数据来源是上游提交端点的 400 报错原文与 GET /v1/models（2026-09-06 实测），
// 不是文档——文档漏了 h3-max 整个模型，也漏了 h3 的 480P 档。
//
// 素材能力（参考图/参考视频/参考音频/首尾帧）两个模型完全一致，故不按模型分档：
// 2026-09-12 实测 h3-max 同样接受 image_url（传 10 张图时上游回的是「at most 9
// reference images allowed」而不是「不支持图片输入」）。早先「h3-max 的 pricing 无
// image_input_count 单元 ⇒ 不接受图片」的推定已被证伪：计价单元不是能力信号。
type modelSpec struct {
	minDuration int
	maxDuration int
	resolutions map[string]struct{}

	// audioNeedsVisualCompanion 音频能否作为唯一参考素材（true = 不能，必须配图片或视频）。
	//
	// h3-max = true：2026-09-12 实测提交期即拒，报错原文
	//	"invalid params, audio cannot be the only reference; include at least one
	//	 reference image or video (2013)"
	// h3 = false：同一批探测里 h3 的音频素材解码失败得更早（ffprobe），语义校验没机会
	//	触发，因此无法证明该规则适用于 h3。按「不拿未证实的规则去收紧能力」的原则，
	//	h3 不在本地拦，交由上游判定——若上游同样拒，客户会拿到上游的准确报错原文。
	//	一旦用真实音频验证出 h3 也拒，把本字段改为 true 即可。
	audioNeedsVisualCompanion bool
}

var modelSpecs = map[string]modelSpec{
	modelMinimaxH3: {
		minDuration: 4,
		maxDuration: 15,
		// 上游报错原文：model -H3 does not support resolution 1K,
		// supported resolutions: 480P, 768P, 2K
		resolutions: map[string]struct{}{
			resolution480P: {}, resolution768P: {}, resolution2K: {},
		},
	},
	modelMinimaxH3Max: {
		// 上游报错原文：model -H3-Max does not support duration 4s,
		// supported durations: 5s, 6s, ..., 15s
		minDuration: 5,
		maxDuration: 15,
		// 上游报错原文：model -H3-Max does not support resolution 1K,
		// supported resolutions: 480P, 768P（无 2K）
		resolutions: map[string]struct{}{
			resolution480P: {}, resolution768P: {},
		},
		audioNeedsVisualCompanion: true,
	},
}

// getSpec 按已归一的规范模型名取能力边界。
func getSpec(canonicalModel string) (modelSpec, bool) {
	spec, ok := modelSpecs[canonicalModel]
	return spec, ok
}

// supportedResolutions 按固定顺序列出模型支持的分辨率档位，仅用于错误提示。
// spec.resolutions 是 map，直接遍历会因 Go 的随机遍历顺序导致
// 同一个错误每次文案不同，无法写稳定断言、也不利于客户比对。
func supportedResolutions(spec modelSpec) []string {
	order := []string{resolution480P, resolution768P, resolution2K}
	out := make([]string, 0, len(spec.resolutions))
	for _, r := range order {
		if _, ok := spec.resolutions[r]; ok {
			out = append(out, r)
		}
	}
	return out
}

// resolutionBillingRatios 分辨率计费倍率，按模型分档，基准档为 768P。
// 数据取自 GET /v1/models 的 pricing.units[metric=video_seconds].by.resolution.prices
// （2026-09-06 实测）：
//
//	minimax-h3      768P $0.0624/秒、2K   $0.1014/秒 → 2K   = 0.1014/0.0624 = 1.625
//	minimax-h3-max  768P $0.0624/秒、480P $0.039/秒  → 480P = 0.039/0.0624  = 0.625
//
// 两个模型的 768P 单价相同（$0.0624），所以基础价可共用同一配置口径：
// 管理员应把模型基础价配成「768P 每秒单价」，预扣费 = 基础价 × 秒数 × 本表倍率。
// 注意文档里的 $0.08/$0.13/$0.04 是实际值的固定 1.282 倍（= 50/39），不要用。
//
// 未登记 minimax-h3 的 480P：h3 实测支持 480P，但 /v1/models 里 h3 的
// prices 只给了 {768P, 2K}，缺 480P 条目。按 pricing 结构它会 fallback 到
// default price 0.1014（比 768P 还贵），疑似上游配置遗漏。已向上游提问，
// 未回复前不猜倍率，保持未登记（EstimateBilling 不额外乘）。
//
// 未实现：输入素材计费。文档的「输入素材计费」载明视频按输入时长计费（费率同输出
// 分辨率档）、图片前 5 张免费超出 $0.0312/张（/v1/models 实际值）、音频免费。
// 图片附加费是绝对金额而非倍率，无法用 OtherRatios 表达；输入视频时长在提交时
// 不可知（需等任务完成后读 usage.input_seconds）。上游 new-api 的 hailuo 插件做法是
// 提交时按 maxInputVideoSeconds 预留、完成后用实际值冲正（extractUsage 返回
// input_images / input_video_seconds 两个事实），本站待计费口径确定后照此补。
var resolutionBillingRatios = map[string]map[string]float64{
	modelMinimaxH3: {
		resolution768P: 1.0,
		resolution2K:   1.625,
	},
	modelMinimaxH3Max: {
		resolution768P: 1.0,
		resolution480P: 0.625,
	},
}

func getResolutionBillingRatio(canonicalModel, resolution string) (float64, bool) {
	m, ok := resolutionBillingRatios[canonicalModel]
	if !ok {
		return 0, false
	}
	r, ok := m[canonicalResolution(resolution)]
	return r, ok
}

// canonicalResolution 归一分辨率取值：大小写不敏感，映射为上游枚举 480P / 768P / 2K。
func canonicalResolution(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "480p":
		return resolution480P
	case "768p":
		return resolution768P
	case "2k":
		return resolution2K
	default:
		return strings.TrimSpace(v)
	}
}

// resolveUpstreamModel 把客户传入名或渠道映射名归一为上游规范模型名（全小写）。
// 严格白名单：上游只开放 supportedModels 里这几个模型，不认识的直接拒。
func resolveUpstreamModel(candidates ...string) (string, bool) {
	for _, c := range candidates {
		name := strings.ToLower(strings.TrimSpace(c))
		if name == "" {
			continue
		}
		if canonical, ok := supportedModels[name]; ok {
			return canonical, true
		}
	}
	return "", false
}

package operation_setting

import "strings"

// 报错触发模型移出（后台「监控告警」页可配）：
// 上游报「没有该模型」时同步核实并移出单个模型，避免整渠道被禁用。
var (
	ModelMissingRemovalEnabled         = true
	ModelMissingRemovalCooldownSeconds = 60  // 同渠道+模型触发防抖窗口（秒）
	ModelMissingRecheckIntervalSeconds = 600 // 移出后复核间隔（秒），复核发现模型回归自动加回
)

// 429 限流触发模型移出：复用「模型缺失」的移出/复核/加回基础设施，只有触发条件与核实口径不同。
// 限流时模型仍在上游列表，"模型是否存在"核实必然放行，故 429 路径不做该核实；但仍要求能拉到
// 上游模型列表——列表在此不作缺失证据，而是复核加回的依赖：拉不到 = 摘了加不回来。
// 语义上是「用复核周期当冷却窗口」：限流期间摘掉→下一轮复核模型仍在→加回→仍限流则再摘，往复。
var (
	// 默认关：新增行为，保持上线前语义不变，由管理员在后台显式开启。
	ModelRateLimitRemovalEnabled         = false
	ModelRateLimitRecheckIntervalSeconds = 180 // 429 移出后复核间隔（秒），比模型缺失短——限流通常分钟级自愈
)

// 权限 / 不支持类错误触发模型移出：同样复用「模型缺失」的移出/复核/加回基础设施。
// 典型场景：Azure 部署未开该模型、AWS Bedrock 该区域未开通该模型——上游返回 403 权限拒绝
// 或 400「模型不支持」，而渠道其余模型完全正常。改造前这类错误会命中
// AutomaticDisableKeywords（默认已含 Permission denied / Operation not allowed）从而停掉
// 整个渠道，一个渠道上几十个正常模型被连带切掉。
//
// 与缺失路径的关键差异：403 发生时模型仍在上游 /v1/models 清单里，所以本路径**跳过**
// 「模型是否存在」核实——走核实必然放行，模型永远摘不掉（429 路径当初踩过同一个坑）。
// 但仍保留「拉不到上游模型列表就不摘」这道前置：列表在此不作缺失证据，而是复核加回的
// 唯一依赖，拉不到 = 摘了加不回来（复核会一路顺延到 24h 保留上限）。
var (
	// 默认关：新增行为。关闭时 403/400 继续走 AutomaticDisableKeywords 停渠道判定，
	// 与上线前完全一致，由管理员在后台显式开启。
	ModelForbiddenRemovalEnabled         = false
	ModelForbiddenRecheckIntervalSeconds = 1800 // 权限变更远不如限流频繁，复核间隔取更长
)

// ModelForbiddenStatusCodeRanges 触发权限摘除的状态码（ranges 格式，默认 "400,403"）。
// 与关键词表是「与」关系：状态码命中且文案命中才判定为权限类，避免把普通 400 参数错误摘成模型问题。
var ModelForbiddenStatusCodeRanges = []StatusCodeRange{
	{Start: 400, End: 400},
	{Start: 403, End: 403},
}

func ModelForbiddenStatusCodesToString() string {
	return statusCodeRangesToString(ModelForbiddenStatusCodeRanges)
}

func ModelForbiddenStatusCodesFromString(s string) error {
	ranges, err := ParseHTTPStatusCodeRanges(s)
	if err != nil {
		return err
	}
	ModelForbiddenStatusCodeRanges = ranges
	return nil
}

func IsModelForbiddenStatusCode(code int) bool {
	return shouldMatchStatusCodeRanges(ModelForbiddenStatusCodeRanges, code)
}

// ModelForbiddenKeywords 权限拒绝 / 模型不支持的常见错误文案（匹配时两侧都转小写）。
// 与 AutomaticDisableKeywords 可能有重叠条目（Permission denied / Operation not allowed
// 默认就在停渠道表里）。按低烈度优先原则，命中本表走摘模型：relay 报错路径先调用
// tryHandleChannelModelMissing，接管成功即短路整渠道禁用；只有摘不掉时才回退停渠道判定。
// 不做自动数据迁移——各站点在停渠道表里自加了条目，无法自动判断归属。
var ModelForbiddenKeywords = []string{
	"Permission denied",
	"does not have access to model",
	"Operation not allowed",
	"model not supported",
	"unsupported model",
}

func ModelForbiddenKeywordsToString() string {
	return strings.Join(ModelForbiddenKeywords, "\n")
}

func ModelForbiddenKeywordsFromString(s string) {
	ModelForbiddenKeywords = []string{}
	for _, k := range strings.Split(s, "\n") {
		k = strings.TrimSpace(k)
		if k != "" {
			ModelForbiddenKeywords = append(ModelForbiddenKeywords, k)
		}
	}
}

// ── 摘除累计与升级 ──
// 安全阀：限流 / 权限两条路径的摘除依据都是「推断」而非「证据」——整渠道 RPM 打满或
// 凭据级无权时，每个模型都会报同样的错，不设上限会把渠道逐个模型掏空，且每次移出都触发
// 一次全量渠道缓存重建。摘到触顶恰恰是「限的/拒的是渠道不是模型」的信号。
var (
	// ModelRemovalMaxRemovedPerChannel 同渠道累计摘除上限。默认 5，与改造前硬编码值一致。
	ModelRemovalMaxRemovedPerChannel = 5
	// ModelRemovalCapAction 触顶后动作。默认 alert_only = 改造前行为（仅记日志 + 回退原禁用判定）。
	ModelRemovalCapAction = ModelRemovalCapActionAlertOnly
	// ModelMissingRemovalCapEnabled 缺失路径是否纳入上限。默认 false = 改造前行为。
	// 缺失路径摘除前有存在性核实兜底（上游列表里真的没有才摘），触顶意味着上游确实批量
	// 下线了 N 个模型，是事实而非误判；此时停整渠道会连带切掉仍可用的模型，与低烈度优先相反。
	ModelMissingRemovalCapEnabled = false
)

// ModelRemovalConsecutiveThreshold 触发「限流 / 权限」摘除前，同一 (渠道, 模型) 需要连续命中的
// 次数（默认 10，后台「渠道治理 ▸ 模型级摘除」可配，设 1 = 恢复「命中一次即摘」的旧行为）。
//
// 这两条路径摘除依据是推断而非证据：上游是账号池时，单条 429/400 常常只是池子里某个账号被
// 限流/无权、池子随即 failover 到下一个账号的瞬时信号，一次就摘会误伤（把可重试的瞬时错误变成
// 「无可用渠道」硬失败）。改为「连续 N 次」后：计数走共享 Redis 按 (渠道,模型) 跨节点聚合，中途
// 任一成功请求即清零，只有持续失败才累积到门槛后摘除。
//
// 缺失路径不适用——它有 /v1/models 存在性核实作硬证据，单次核实即可信，不叠加连击门槛。
var ModelRemovalConsecutiveThreshold = 10

const (
	ModelRemovalCapActionAlertOnly      = "alert_only"
	ModelRemovalCapActionDisableChannel = "disable_channel"
)

// ModelRemovalNotifyEnabled 模型级摘除是否通知管理员。
//
// 存在理由是「降烈度的代价要补回来」：停整渠道在渠道列表里是红色的、还会发通知，而摘掉
// 一个模型之后渠道状态仍是「启用」，运维界面上看不出任何异常——低烈度换来的是低可见度。
// 开启后每次摘除都发一封，内容含渠道名、模型名、触发原因与下次复核时间。
//
// 默认 false = 上线前行为：改造前**只有停用渠道会发通知**（service.DisableChannel 里受
// channel_health_setting.notify_on_degrade 约束），摘除路径从来不发。默认关即"一封都不多发"，
// 由管理员按渠道规模显式开启（大集群上摘除比停用频繁得多，默认开会造成邮件风暴）。
var ModelRemovalNotifyEnabled = false

// IsValidModelRemovalCapAction 白名单校验，未知取值直接报错而不静默回落，避免配置写错后
// 管理员以为已开启升级而实际没有生效。
func IsValidModelRemovalCapAction(action string) bool {
	return action == ModelRemovalCapActionAlertOnly || action == ModelRemovalCapActionDisableChannel
}

// ShouldDisableChannelOnRemovalCap 触顶是否升级为停用整渠道。
func ShouldDisableChannelOnRemovalCap() bool {
	return ModelRemovalCapAction == ModelRemovalCapActionDisableChannel
}

// ModelMissingKeywords 上游「没有该模型」的常见错误文案（小写匹配）。
// 识别偏宽松没关系：真正移出前必须经过上游模型列表核实，误报最多多一次 /v1/models 调用。
var ModelMissingKeywords = []string{
	"does not exist",
	"model not found",
	"no such model",
	"invalid model",
	"unknown model",
	"cannot find model",
	"模型不存在",
	"没有该模型",
	"不存在该模型",
	"无效的模型",
}

func ModelMissingKeywordsToString() string {
	return strings.Join(ModelMissingKeywords, "\n")
}

func ModelMissingKeywordsFromString(s string) {
	ModelMissingKeywords = []string{}
	for _, k := range strings.Split(s, "\n") {
		k = strings.TrimSpace(strings.ToLower(k))
		if k != "" {
			ModelMissingKeywords = append(ModelMissingKeywords, k)
		}
	}
}

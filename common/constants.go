package common

import (
	"crypto/tls"
	//"os"
	//"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var StartTime = time.Now().Unix() // unit: second
var Version = "v0.0.0"            // this hard coding will be replaced automatically when building, no need to manually change
var SystemName = "New API"
var Footer = ""
var Logo = ""
var TopUpLink = ""

var themeValue atomic.Value // stores string; safe for concurrent read/write

func init() {
	themeValue.Store("classic")
}

func GetTheme() string {
	return themeValue.Load().(string)
}

// SetTheme updates the frontend theme atomically.
// Only "default" and "classic" are accepted; other values are silently ignored.
func SetTheme(t string) {
	if t == "default" || t == "classic" {
		themeValue.Store(t)
	}
}

// ThemeAwarePath rewrites legacy /console/* paths to the default-theme
// equivalents when the active theme is "default".  For "classic" (or any
// other theme) the path is returned unchanged.  The function only touches
// known prefixes so it is safe to call with arbitrary suffixes and query
// strings.
func ThemeAwarePath(suffix string) string {
	if GetTheme() != "default" {
		return suffix
	}
	switch {
	case strings.HasPrefix(suffix, "/console/topup"):
		return strings.Replace(suffix, "/console/topup", "/wallet", 1)
	case strings.HasPrefix(suffix, "/console/log"):
		return strings.Replace(suffix, "/console/log", "/usage-logs", 1)
	case strings.HasPrefix(suffix, "/console/personal"):
		return strings.Replace(suffix, "/console/personal", "/profile", 1)
	}
	return suffix
}

// var ChatLink = ""
// var ChatLink2 = ""
var QuotaPerUnit = 500 * 1000.0 // $0.002 / 1K tokens
// 保留旧变量以兼容历史逻辑，实际展示由 general_setting.quota_display_type 控制
var DisplayInCurrencyEnabled = true
var DisplayTokenStatEnabled = true
var DrawingEnabled = true
var TaskEnabled = true
var DataExportEnabled = true
var DataExportInterval = 5         // unit: minute
var DataExportDefaultTime = "hour" // unit: minute
var DefaultCollapseSidebar = false // default value of collapse sidebar

// Any options with "Secret", "Token" in its key won't be return by GetOptions

var SessionSecret = uuid.New().String()
var CryptoSecret = uuid.New().String()
var SessionCookieSecure = false
var SessionCookieTrustedURLs []string

var OptionMap map[string]string
var OptionMapRWMutex sync.RWMutex

var ItemsPerPage = 10
var MaxRecentItems = 1000

var PasswordLoginEnabled = true
var PasswordRegisterEnabled = true
var EmailVerificationEnabled = false
var GitHubOAuthEnabled = false
var LinuxDOOAuthEnabled = false
var WeChatAuthEnabled = false
var TelegramOAuthEnabled = false
var TurnstileCheckEnabled = false
var RegisterEnabled = true

var EmailDomainRestrictionEnabled = false // 是否启用邮箱域名限制
var EmailAliasRestrictionEnabled = false  // 是否启用邮箱别名限制
var EmailDomainWhitelist = []string{
	"gmail.com",
	"163.com",
	"126.com",
	"qq.com",
	"outlook.com",
	"hotmail.com",
	"icloud.com",
	"yahoo.com",
	"foxmail.com",
}
var EmailLoginAuthServerList = []string{
	"smtp.sendcloud.net",
	"smtp.azurecomm.net",
}

var DebugEnabled bool
var MemoryCacheEnabled bool

var LogConsumeEnabled = true

var TLSInsecureSkipVerify bool
var InsecureTLSConfig = &tls.Config{InsecureSkipVerify: true}

var SMTPServer = ""
var SMTPPort = 587
var SMTPSSLEnabled = false
var SMTPForceAuthLogin = false
var SMTPAccount = ""
var SMTPFrom = ""
var SMTPToken = ""

var GitHubClientId = ""
var GitHubClientSecret = ""
var LinuxDOClientId = ""
var LinuxDOClientSecret = ""
var LinuxDOMinimumTrustLevel = 0

var WeChatServerAddress = ""
var WeChatServerToken = ""
var WeChatAccountQRCodeImageURL = ""

var TurnstileSiteKey = ""
var TurnstileSecretKey = ""

var TelegramBotToken = ""
var TelegramBotName = ""

var QuotaForNewUser = 0
var QuotaForInviter = 0
var QuotaForInvitee = 0

// AffiliateCommissionEnabled 推荐分成开关；开启后每日结算被推荐人余额扣费的分成给推荐人。
var AffiliateCommissionEnabled = false

// AffiliateCommissionRatio 推荐分成比例 (0-1)；例如 0.10 表示被推荐人余额扣费 10% 给推荐人。
var AffiliateCommissionRatio = 0.0

// RewardInviterOnEffectiveOnly 开启后，邀请奖励(QuotaForInviter)不再注册即发，
// 改为被推荐人成为「有效用户」(已登录 + 消费达标)后再发放，防止纯注册薅羊毛。
var RewardInviterOnEffectiveOnly = true

// EffectiveInviteeConsumeThreshold 有效用户的消费门槛(used_quota)；
// <=0 时自动取 QuotaForInvitee+1，即被推荐人消费必须超过我们赠送的额度。
var EffectiveInviteeConsumeThreshold = 0
var ChannelDisableThreshold = 5.0
var AutomaticDisableChannelEnabled = false
var AutomaticEnableChannelEnabled = false
var QuotaRemindThreshold = 500000
var PreConsumedQuota = 500

var RetryTimes = 0

//var RootUserEmail = ""

var IsMasterNode bool

// NodeName 节点名称，从 NODE_NAME 环境变量读取；
// 用于审计日志中标识节点身份，在容器/K8s 部署时比自动探测到的容器内网 IP 更具可读性。
var NodeName = ""

var requestInterval int
var RequestInterval time.Duration

var SyncFrequency int // unit is second

var BatchUpdateEnabled = false
var BatchUpdateInterval int

var RelayTimeout int // unit is second

var RelayMaxIdleConns int
var RelayMaxIdleConnsPerHost int
var RelayIdleConnTimeout int // 空闲连接在连接池中的最长保留时间（秒）；0 表示不限制

// 细粒度出站超时（秒），用于快速识别"节点无响应"以触发多 base_url 切换
var RelayDialTimeout int             // TCP 建连超时
var RelayTLSHandshakeTimeout int     // TLS 握手超时
var RelayStreamRespHeaderTimeout int // 流式请求响应头等待超时（非流式不限，靠 per-model timeout）

var GeminiSafetySetting string

// https://docs.cohere.com/docs/safety-modes Type; NONE/CONTEXTUAL/STRICT
var CohereSafetySetting string

const (
	RequestIdKey         = "X-Oneapi-Request-Id"
	UpstreamRequestIdKey = "X-Upstream-Request-Id"
)

const (
	RoleGuestUser  = 0
	RoleCommonUser = 1
	RoleAdminUser  = 10
	RoleRootUser   = 100
)

func IsValidateRole(role int) bool {
	return role == RoleGuestUser || role == RoleCommonUser || role == RoleAdminUser || role == RoleRootUser
}

var (
	FileUploadPermission    = RoleGuestUser
	FileDownloadPermission  = RoleGuestUser
	ImageUploadPermission   = RoleGuestUser
	ImageDownloadPermission = RoleGuestUser
)

// All duration's unit is seconds
// Shouldn't larger then RateLimitKeyExpirationDuration
var (
	GlobalApiRateLimitEnable   bool
	GlobalApiRateLimitNum      int
	GlobalApiRateLimitDuration int64

	GlobalWebRateLimitEnable   bool
	GlobalWebRateLimitNum      int
	GlobalWebRateLimitDuration int64

	CriticalRateLimitEnable   bool
	CriticalRateLimitNum            = 20
	CriticalRateLimitDuration int64 = 20 * 60

	UploadRateLimitNum            = 10
	UploadRateLimitDuration int64 = 60

	DownloadRateLimitNum            = 10
	DownloadRateLimitDuration int64 = 60

	// /api/data/runtime 专用限速：60 次/分钟 per IP，前端 60s 自动刷新留足 headroom
	RuntimeMetricsRateLimitNum            = 60
	RuntimeMetricsRateLimitDuration int64 = 60

	// Per-user search rate limit (applies after authentication, keyed by user ID)
	SearchRateLimitEnable         = true
	SearchRateLimitNum            = 10
	SearchRateLimitDuration int64 = 60

	// Per-user critical-operation rate limit (keyed by user ID, per scope)
	UserCriticalRateLimitEnable         = true
	UserCriticalRateLimitNum            = 5
	UserCriticalRateLimitDuration int64 = 60

	// 邮件发送全局限速：窗口内最多 Num 封，超出直接丢弃并记日志。
	// 防止验证码/渠道告警突发群发撞上 SMTP 服务商（如 Zoho）的频率上限导致整批被拒或封号。
	EmailSendRateLimitEnable         = true
	EmailSendRateLimitNum            = 30
	EmailSendRateLimitDuration int64 = 60
)

var RateLimitKeyExpirationDuration = 20 * time.Minute

const (
	UserStatusEnabled  = 1 // don't use 0, 0 is the default value!
	UserStatusDisabled = 2 // also don't use 0
)

const (
	TokenStatusEnabled   = 1 // don't use 0, 0 is the default value!
	TokenStatusDisabled  = 2 // also don't use 0
	TokenStatusExpired   = 3
	TokenStatusExhausted = 4
)

const (
	RedemptionCodeStatusEnabled  = 1 // don't use 0, 0 is the default value!
	RedemptionCodeStatusDisabled = 2 // also don't use 0
	RedemptionCodeStatusUsed     = 3 // also don't use 0
)

const (
	ChannelStatusUnknown          = 0
	ChannelStatusEnabled          = 1 // don't use 0, 0 is the default value!
	ChannelStatusManuallyDisabled = 2 // also don't use 0
	ChannelStatusAutoDisabled     = 3
)

const (
	TopUpStatusPending = "pending"
	TopUpStatusSuccess = "success"
	TopUpStatusFailed  = "failed"
	TopUpStatusExpired = "expired"
)

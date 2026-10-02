package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"gorm.io/gorm"
)

const (
	systemRoleLiftOptionKey      = "claude.system_role_lift_enabled"
	systemRoleLiftGrandfatherKey = "claude.system_role_lift_grandfathered"
)

type Option struct {
	Key   string `json:"key" gorm:"primaryKey"`
	Value string `json:"value"`
}

func AllOption() ([]*Option, error) {
	var options []*Option
	var err error
	err = DB.Find(&options).Error
	return options, err
}

func InitOptionMap() {
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)
	common.OptionMap["iq_test_setting.enabled"] = "false"
	common.OptionMap["iq_test_setting.interval_minutes"] = "60"
	common.OptionMap["iq_test_setting.concurrency"] = "4"
	common.OptionMap["iq_test_setting.disable_below_baseline"] = "false"
	common.OptionMap["iq_test_setting.priority_step"] = "10"
	common.OptionMap["iq_test_setting.questions_per_round"] = "8"
	common.OptionMap["iq_test_setting.per_question_timeout_seconds"] = "20"
	common.OptionMap["iq_test_setting.notify_on_action"] = "true"

	// 添加原有的系统配置
	common.OptionMap["FileUploadPermission"] = strconv.Itoa(common.FileUploadPermission)
	common.OptionMap["FileDownloadPermission"] = strconv.Itoa(common.FileDownloadPermission)
	common.OptionMap["ImageUploadPermission"] = strconv.Itoa(common.ImageUploadPermission)
	common.OptionMap["ImageDownloadPermission"] = strconv.Itoa(common.ImageDownloadPermission)
	common.OptionMap["PasswordLoginEnabled"] = strconv.FormatBool(common.PasswordLoginEnabled)
	common.OptionMap["PasswordRegisterEnabled"] = strconv.FormatBool(common.PasswordRegisterEnabled)
	common.OptionMap["EmailVerificationEnabled"] = strconv.FormatBool(common.EmailVerificationEnabled)
	common.OptionMap["GitHubOAuthEnabled"] = strconv.FormatBool(common.GitHubOAuthEnabled)
	common.OptionMap["LinuxDOOAuthEnabled"] = strconv.FormatBool(common.LinuxDOOAuthEnabled)
	common.OptionMap["TelegramOAuthEnabled"] = strconv.FormatBool(common.TelegramOAuthEnabled)
	common.OptionMap["WeChatAuthEnabled"] = strconv.FormatBool(common.WeChatAuthEnabled)
	common.OptionMap["TurnstileCheckEnabled"] = strconv.FormatBool(common.TurnstileCheckEnabled)
	common.OptionMap["RegisterEnabled"] = strconv.FormatBool(common.RegisterEnabled)
	common.OptionMap["AutomaticDisableChannelEnabled"] = strconv.FormatBool(common.AutomaticDisableChannelEnabled)
	common.OptionMap["AutomaticEnableChannelEnabled"] = strconv.FormatBool(common.AutomaticEnableChannelEnabled)
	common.OptionMap["LogConsumeEnabled"] = strconv.FormatBool(common.LogConsumeEnabled)
	common.OptionMap["DisplayInCurrencyEnabled"] = strconv.FormatBool(common.DisplayInCurrencyEnabled)
	common.OptionMap["DisplayTokenStatEnabled"] = strconv.FormatBool(common.DisplayTokenStatEnabled)
	common.OptionMap["DrawingEnabled"] = strconv.FormatBool(common.DrawingEnabled)
	common.OptionMap["TaskEnabled"] = strconv.FormatBool(common.TaskEnabled)
	common.OptionMap["DataExportEnabled"] = strconv.FormatBool(common.DataExportEnabled)
	common.OptionMap["ChannelDisableThreshold"] = strconv.FormatFloat(common.ChannelDisableThreshold, 'f', -1, 64)
	common.OptionMap["EmailDomainRestrictionEnabled"] = strconv.FormatBool(common.EmailDomainRestrictionEnabled)
	common.OptionMap["EmailAliasRestrictionEnabled"] = strconv.FormatBool(common.EmailAliasRestrictionEnabled)
	common.OptionMap["EmailDomainWhitelist"] = strings.Join(common.EmailDomainWhitelist, ",")
	common.OptionMap["SMTPServer"] = ""
	common.OptionMap["SMTPFrom"] = ""
	common.OptionMap["SMTPPort"] = strconv.Itoa(common.SMTPPort)
	common.OptionMap["SMTPAccount"] = ""
	common.OptionMap["SMTPToken"] = ""
	common.OptionMap["SMTPSSLEnabled"] = strconv.FormatBool(common.SMTPSSLEnabled)
	common.OptionMap["SMTPForceAuthLogin"] = strconv.FormatBool(common.SMTPForceAuthLogin)
	common.OptionMap["Notice"] = ""
	common.OptionMap["About"] = ""
	common.OptionMap["HomePageContent"] = ""
	common.OptionMap["Footer"] = common.Footer
	common.OptionMap["SystemName"] = common.SystemName
	common.OptionMap["Logo"] = common.Logo
	common.OptionMap["ServerAddress"] = ""
	common.OptionMap["WorkerUrl"] = system_setting.WorkerUrl
	common.OptionMap["WorkerValidKey"] = system_setting.WorkerValidKey
	common.OptionMap["WorkerAllowHttpImageRequestEnabled"] = strconv.FormatBool(system_setting.WorkerAllowHttpImageRequestEnabled)
	common.OptionMap["PayAddress"] = ""
	common.OptionMap["CustomCallbackAddress"] = ""
	common.OptionMap["EpayId"] = ""
	common.OptionMap["EpayKey"] = ""
	common.OptionMap["Price"] = strconv.FormatFloat(operation_setting.Price, 'f', -1, 64)
	common.OptionMap["USDExchangeRate"] = strconv.FormatFloat(operation_setting.USDExchangeRate, 'f', -1, 64)
	common.OptionMap["MinTopUp"] = strconv.Itoa(operation_setting.MinTopUp)
	common.OptionMap["StripeMinTopUp"] = strconv.Itoa(setting.StripeMinTopUp)
	common.OptionMap["StripeApiSecret"] = setting.StripeApiSecret
	common.OptionMap["StripeWebhookSecret"] = setting.StripeWebhookSecret
	common.OptionMap["StripePriceId"] = setting.StripePriceId
	common.OptionMap["StripeUnitPrice"] = strconv.FormatFloat(setting.StripeUnitPrice, 'f', -1, 64)
	common.OptionMap["StripePromotionCodesEnabled"] = strconv.FormatBool(setting.StripePromotionCodesEnabled)
	common.OptionMap["CreemApiKey"] = setting.CreemApiKey
	common.OptionMap["CreemProducts"] = setting.CreemProducts
	common.OptionMap["CreemTestMode"] = strconv.FormatBool(setting.CreemTestMode)
	common.OptionMap["CreemWebhookSecret"] = setting.CreemWebhookSecret
	common.OptionMap["WaffoEnabled"] = strconv.FormatBool(setting.WaffoEnabled)
	common.OptionMap["WaffoApiKey"] = setting.WaffoApiKey
	common.OptionMap["WaffoPrivateKey"] = setting.WaffoPrivateKey
	common.OptionMap["WaffoPublicCert"] = setting.WaffoPublicCert
	common.OptionMap["WaffoSandboxPublicCert"] = setting.WaffoSandboxPublicCert
	common.OptionMap["WaffoSandboxApiKey"] = setting.WaffoSandboxApiKey
	common.OptionMap["WaffoSandboxPrivateKey"] = setting.WaffoSandboxPrivateKey
	common.OptionMap["WaffoSandbox"] = strconv.FormatBool(setting.WaffoSandbox)
	common.OptionMap["WaffoMerchantId"] = setting.WaffoMerchantId
	common.OptionMap["WaffoNotifyUrl"] = setting.WaffoNotifyUrl
	common.OptionMap["WaffoReturnUrl"] = setting.WaffoReturnUrl
	common.OptionMap["WaffoSubscriptionReturnUrl"] = setting.WaffoSubscriptionReturnUrl
	common.OptionMap["WaffoCurrency"] = setting.WaffoCurrency
	common.OptionMap["WaffoUnitPrice"] = strconv.FormatFloat(setting.WaffoUnitPrice, 'f', -1, 64)
	common.OptionMap["WaffoMinTopUp"] = strconv.Itoa(setting.WaffoMinTopUp)
	common.OptionMap["WaffoPayMethods"] = setting.WaffoPayMethods2JsonString()
	common.OptionMap["WaffoAllowedGroups"] = setting.WaffoAllowedGroups
	common.OptionMap["SfpayEnabled"] = strconv.FormatBool(setting.AgouEnabled)
	common.OptionMap["SfpayBaseURL"] = setting.AgouBaseURL
	common.OptionMap["SfpayAppId"] = setting.AgouAppId
	common.OptionMap["SfpayAppSecret"] = setting.AgouAppSecret
	common.OptionMap["SfpayGroupCode"] = setting.AgouGroupCode
	common.OptionMap["SfpayNotifyUrl"] = setting.AgouNotifyUrl
	common.OptionMap["SfpayReturnUrl"] = setting.AgouReturnUrl
	common.OptionMap["SfpayUnitPrice"] = strconv.FormatFloat(setting.AgouUnitPrice, 'f', -1, 64)
	common.OptionMap["SfpayMinTopUp"] = strconv.Itoa(setting.AgouMinTopUp)
	common.OptionMap["SfpayMaxTopUp"] = strconv.Itoa(setting.AgouMaxTopUp)
	common.OptionMap["SfpayAllowedCallbackIPs"] = setting.AgouAllowedCallbackIPs
	common.OptionMap["SfpayAlipayPayType"] = setting.AgouAlipayPayType
	common.OptionMap["SfpayWechatEnabled"] = strconv.FormatBool(setting.AgouWechatEnabled)
	common.OptionMap["SfpayWechatPayType"] = setting.AgouWechatPayType
	common.OptionMap["SfpayAllowedGroups"] = setting.AgouAllowedGroups
	common.OptionMap["SfpayPayChannels"] = setting.AgouPayChannels2JsonString()
	common.OptionMap["SfpayLogo"] = setting.AgouLogo
	common.OptionMap["WaffoPancakeEnabled"] = strconv.FormatBool(setting.WaffoPancakeEnabled)
	common.OptionMap["WaffoPancakeSandbox"] = strconv.FormatBool(setting.WaffoPancakeSandbox)
	common.OptionMap["WaffoPancakeMerchantID"] = setting.WaffoPancakeMerchantID
	common.OptionMap["WaffoPancakePrivateKey"] = setting.WaffoPancakePrivateKey
	common.OptionMap["WaffoPancakeWebhookPublicKey"] = setting.WaffoPancakeWebhookPublicKey
	common.OptionMap["WaffoPancakeWebhookTestKey"] = setting.WaffoPancakeWebhookTestKey
	common.OptionMap["WaffoPancakeStoreID"] = setting.WaffoPancakeStoreID
	common.OptionMap["WaffoPancakeProductID"] = setting.WaffoPancakeProductID
	common.OptionMap["WaffoPancakeReturnURL"] = setting.WaffoPancakeReturnURL
	common.OptionMap["WaffoPancakeCurrency"] = setting.WaffoPancakeCurrency
	common.OptionMap["WaffoPancakeUnitPrice"] = strconv.FormatFloat(setting.WaffoPancakeUnitPrice, 'f', -1, 64)
	common.OptionMap["WaffoPancakeMinTopUp"] = strconv.Itoa(setting.WaffoPancakeMinTopUp)
	common.OptionMap["WaffoPancakeAllowedGroups"] = setting.WaffoPancakeAllowedGroups
	common.OptionMap["WaffoPancakePayChannels"] = setting.WaffoPancakePayChannels2JsonString()
	common.OptionMap["WaffoPancakeLogo"] = setting.WaffoPancakeLogo
	common.OptionMap["CryptomusEnabled"] = strconv.FormatBool(setting.CryptomusEnabled)
	common.OptionMap["CryptomusMerchantID"] = setting.CryptomusMerchantID
	common.OptionMap["CryptomusPaymentApiKey"] = setting.CryptomusPaymentApiKey
	common.OptionMap["CryptomusWebhookApiKey"] = setting.CryptomusWebhookApiKey
	common.OptionMap["CryptomusDefaultCurrency"] = setting.CryptomusDefaultCurrency
	common.OptionMap["CryptomusDefaultNetwork"] = setting.CryptomusDefaultNetwork
	common.OptionMap["CryptomusAllowedCurrencies"] = setting.CryptomusAllowedCurrencies
	common.OptionMap["CryptomusUnitPrice"] = strconv.FormatFloat(setting.CryptomusUnitPrice, 'f', -1, 64)
	common.OptionMap["CryptomusMinTopUp"] = strconv.Itoa(setting.CryptomusMinTopUp)
	common.OptionMap["CryptomusReturnURL"] = setting.CryptomusReturnURL
	common.OptionMap["CryptomusAllowedGroups"] = setting.CryptomusAllowedGroups
	common.OptionMap["CryptomusPayChannels"] = setting.CryptomusPayChannels2JsonString()
	common.OptionMap["CryptomusLogo"] = setting.CryptomusLogo
	common.OptionMap["BepusdtEnabled"] = strconv.FormatBool(setting.BepusdtEnabled)
	common.OptionMap["BepusdtBaseURL"] = setting.BepusdtBaseURL
	common.OptionMap["BepusdtApiToken"] = setting.BepusdtApiToken
	common.OptionMap["BepusdtCurrencies"] = setting.BepusdtCurrencies
	common.OptionMap["BepusdtLifetimeSec"] = strconv.Itoa(setting.BepusdtLifetimeSec)
	common.OptionMap["BepusdtUnitPrice"] = strconv.FormatFloat(setting.BepusdtUnitPrice, 'f', -1, 64)
	common.OptionMap["BepusdtMinTopUp"] = strconv.Itoa(setting.BepusdtMinTopUp)
	common.OptionMap["BepusdtReturnURL"] = setting.BepusdtReturnURL
	common.OptionMap["BepusdtAllowedGroups"] = setting.BepusdtAllowedGroups
	common.OptionMap["BepusdtPayChannels"] = setting.BepusdtPayChannels2JsonString()
	common.OptionMap["BepusdtLogo"] = setting.BepusdtLogo
	common.OptionMap["TopupGroupRatio"] = common.TopupGroupRatio2JSONString()
	common.OptionMap["Chats"] = setting.Chats2JsonString()
	common.OptionMap["AutoGroups"] = setting.AutoGroups2JsonString()
	common.OptionMap["DefaultUseAutoGroup"] = strconv.FormatBool(setting.DefaultUseAutoGroup)
	common.OptionMap["PayMethods"] = operation_setting.PayMethods2JsonString()
	common.OptionMap["GitHubClientId"] = ""
	common.OptionMap["GitHubClientSecret"] = ""
	common.OptionMap["TelegramBotToken"] = ""
	common.OptionMap["TelegramBotName"] = ""
	common.OptionMap["WeChatServerAddress"] = ""
	common.OptionMap["WeChatServerToken"] = ""
	common.OptionMap["WeChatAccountQRCodeImageURL"] = ""
	common.OptionMap["TurnstileSiteKey"] = ""
	common.OptionMap["TurnstileSecretKey"] = ""
	common.OptionMap["QuotaForNewUser"] = strconv.Itoa(common.QuotaForNewUser)
	common.OptionMap["QuotaForInviter"] = strconv.Itoa(common.QuotaForInviter)
	common.OptionMap["QuotaForInvitee"] = strconv.Itoa(common.QuotaForInvitee)
	common.OptionMap["AffiliateCommissionEnabled"] = strconv.FormatBool(common.AffiliateCommissionEnabled)
	common.OptionMap["AffiliateCommissionRatio"] = strconv.FormatFloat(common.AffiliateCommissionRatio, 'f', -1, 64)
	common.OptionMap["RewardInviterOnEffectiveOnly"] = strconv.FormatBool(common.RewardInviterOnEffectiveOnly)
	common.OptionMap["EffectiveInviteeConsumeThreshold"] = strconv.Itoa(common.EffectiveInviteeConsumeThreshold)
	common.OptionMap["QuotaRemindThreshold"] = strconv.Itoa(common.QuotaRemindThreshold)
	common.OptionMap["PreConsumedQuota"] = strconv.Itoa(common.PreConsumedQuota)
	common.OptionMap["ModelRequestRateLimitCount"] = strconv.Itoa(setting.ModelRequestRateLimitCount)
	common.OptionMap["ModelRequestRateLimitDurationMinutes"] = strconv.Itoa(setting.ModelRequestRateLimitDurationMinutes)
	common.OptionMap["ModelRequestRateLimitSuccessCount"] = strconv.Itoa(setting.ModelRequestRateLimitSuccessCount)
	common.OptionMap["ModelRequestRateLimitGroup"] = setting.ModelRequestRateLimitGroup2JSONString()
	common.OptionMap["ModelRatio"] = ratio_setting.ModelRatio2JSONString()
	common.OptionMap["ModelPrice"] = ratio_setting.ModelPrice2JSONString()
	common.OptionMap["CacheRatio"] = ratio_setting.CacheRatio2JSONString()
	common.OptionMap["CreateCacheRatio"] = ratio_setting.CreateCacheRatio2JSONString()
	common.OptionMap["GroupRatio"] = ratio_setting.GroupRatio2JSONString()
	common.OptionMap["GroupGroupRatio"] = ratio_setting.GroupGroupRatio2JSONString()
	common.OptionMap["UserUsableGroups"] = setting.UserUsableGroups2JSONString()
	common.OptionMap["CompletionRatio"] = ratio_setting.CompletionRatio2JSONString()
	common.OptionMap["ImageRatio"] = ratio_setting.ImageRatio2JSONString()
	common.OptionMap["AudioRatio"] = ratio_setting.AudioRatio2JSONString()
	common.OptionMap["AudioCompletionRatio"] = ratio_setting.AudioCompletionRatio2JSONString()
	common.OptionMap["TopUpLink"] = common.TopUpLink
	//common.OptionMap["ChatLink"] = common.ChatLink
	//common.OptionMap["ChatLink2"] = common.ChatLink2
	common.OptionMap["QuotaPerUnit"] = strconv.FormatFloat(common.QuotaPerUnit, 'f', -1, 64)
	common.OptionMap["RetryTimes"] = strconv.Itoa(common.RetryTimes)
	common.OptionMap["DataExportInterval"] = strconv.Itoa(common.DataExportInterval)
	common.OptionMap["DataExportDefaultTime"] = common.DataExportDefaultTime
	common.OptionMap["DefaultCollapseSidebar"] = strconv.FormatBool(common.DefaultCollapseSidebar)
	common.OptionMap["MjNotifyEnabled"] = strconv.FormatBool(setting.MjNotifyEnabled)
	common.OptionMap["MjAccountFilterEnabled"] = strconv.FormatBool(setting.MjAccountFilterEnabled)
	common.OptionMap["MjModeClearEnabled"] = strconv.FormatBool(setting.MjModeClearEnabled)
	common.OptionMap["MjForwardUrlEnabled"] = strconv.FormatBool(setting.MjForwardUrlEnabled)
	common.OptionMap["MjActionCheckSuccessEnabled"] = strconv.FormatBool(setting.MjActionCheckSuccessEnabled)
	common.OptionMap["CheckSensitiveEnabled"] = strconv.FormatBool(setting.CheckSensitiveEnabled)
	common.OptionMap["DemoSiteEnabled"] = strconv.FormatBool(operation_setting.DemoSiteEnabled)
	common.OptionMap["SelfUseModeEnabled"] = strconv.FormatBool(operation_setting.SelfUseModeEnabled)
	common.OptionMap["ModelRequestRateLimitEnabled"] = strconv.FormatBool(setting.ModelRequestRateLimitEnabled)
	common.OptionMap["CheckSensitiveOnPromptEnabled"] = strconv.FormatBool(setting.CheckSensitiveOnPromptEnabled)
	common.OptionMap["StopOnSensitiveEnabled"] = strconv.FormatBool(setting.StopOnSensitiveEnabled)
	common.OptionMap["SensitiveWords"] = setting.SensitiveWordsToString()
	common.OptionMap["SensitiveAsyncEnabled"] = strconv.FormatBool(setting.GetSensitiveAsyncEnabled())
	common.OptionMap["SensitiveDumpToFile"] = strconv.FormatBool(setting.GetSensitiveDumpToFile())
	common.OptionMap["SensitiveSampleRate"] = strconv.Itoa(setting.GetSensitiveSampleRate())
	common.OptionMap["SensitiveDumpRetentionDays"] = strconv.Itoa(setting.GetSensitiveDumpRetentionDays())
	common.OptionMap["SensitiveDumpDiskGuardPercent"] = strconv.Itoa(setting.GetSensitiveDumpDiskGuardPercent())
	common.OptionMap["StreamCacheQueueLength"] = strconv.Itoa(setting.StreamCacheQueueLength)
	common.OptionMap["AutomaticDisableKeywords"] = operation_setting.AutomaticDisableKeywordsToString()
	common.OptionMap["AutomaticDisableStatusCodes"] = operation_setting.AutomaticDisableStatusCodesToString()
	common.OptionMap["AutomaticRetryStatusCodes"] = operation_setting.AutomaticRetryStatusCodesToString()
	common.OptionMap["ModelMissingRemovalEnabled"] = strconv.FormatBool(operation_setting.ModelMissingRemovalEnabled)
	common.OptionMap["ModelMissingRemovalCooldownSeconds"] = strconv.Itoa(operation_setting.ModelMissingRemovalCooldownSeconds)
	common.OptionMap["ModelMissingRecheckIntervalSeconds"] = strconv.Itoa(operation_setting.ModelMissingRecheckIntervalSeconds)
	common.OptionMap["ModelMissingKeywords"] = operation_setting.ModelMissingKeywordsToString()
	common.OptionMap["ModelRateLimitRemovalEnabled"] = strconv.FormatBool(operation_setting.ModelRateLimitRemovalEnabled)
	common.OptionMap["ModelRateLimitRecheckIntervalSeconds"] = strconv.Itoa(operation_setting.ModelRateLimitRecheckIntervalSeconds)
	common.OptionMap["ModelForbiddenRemovalEnabled"] = strconv.FormatBool(operation_setting.ModelForbiddenRemovalEnabled)
	common.OptionMap["ModelForbiddenStatusCodes"] = operation_setting.ModelForbiddenStatusCodesToString()
	common.OptionMap["ModelForbiddenKeywords"] = operation_setting.ModelForbiddenKeywordsToString()
	common.OptionMap["ModelForbiddenRecheckIntervalSeconds"] = strconv.Itoa(operation_setting.ModelForbiddenRecheckIntervalSeconds)
	common.OptionMap["ModelRemovalMaxRemovedPerChannel"] = strconv.Itoa(operation_setting.ModelRemovalMaxRemovedPerChannel)
	common.OptionMap["ModelRemovalConsecutiveThreshold"] = strconv.Itoa(operation_setting.ModelRemovalConsecutiveThreshold)
	common.OptionMap["ModelRemovalCapAction"] = operation_setting.ModelRemovalCapAction
	common.OptionMap["ModelMissingRemovalCapEnabled"] = strconv.FormatBool(operation_setting.ModelMissingRemovalCapEnabled)
	common.OptionMap["ModelRemovalNotifyEnabled"] = strconv.FormatBool(operation_setting.ModelRemovalNotifyEnabled)
	common.OptionMap["ExposeRatioEnabled"] = strconv.FormatBool(ratio_setting.IsExposeRatioEnabled())
	// 对账 tab 上游账单数据源：balance 面板地址 + 只读服务令牌（Token 后缀在 GET /api/option/ 自动脱敏）
	common.OptionMap["ReconcileBalancePanelBaseURL"] = ""
	common.OptionMap["ReconcileBalancePanelToken"] = ""

	// 自动添加所有注册的模型配置
	modelConfigs := config.GlobalConfig.ExportAllConfigs()
	for k, v := range modelConfigs {
		common.OptionMap[k] = v
	}

	common.OptionMapRWMutex.Unlock()
	loadOptionsFromDatabase()
}

func loadOptionsFromDatabase() {
	options, _ := AllOption()
	for _, option := range options {
		err := updateOptionMap(option.Key, option.Value)
		if err != nil {
			common.SysLog("failed to update option map: " + err.Error())
		}
	}
	grandfatherSystemRoleLiftDefault()
}

// grandfatherSystemRoleLiftDefault keeps sites that were effectively on under
// the old default (no stored claude.system_role_lift_enabled) on after the
// code default flips to false. An explicit stored true or false is not
// rewritten. A fresh database with no option rows keeps the new default.
// The marker row makes this one-shot, so a later new site that saves other
// options does not get flipped on at the next restart.
func grandfatherSystemRoleLiftDefault() {
	if DB == nil || optionMapHas(systemRoleLiftGrandfatherKey) {
		return
	}

	var marker Option
	err := DB.Where(map[string]any{"key": systemRoleLiftGrandfatherKey}).Take(&marker).Error
	if err == nil {
		_ = updateOptionMap(systemRoleLiftGrandfatherKey, marker.Value)
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.SysError("system role lift grandfather lookup failed: " + err.Error())
		return
	}

	var rowCount int64
	if err := DB.Model(&Option{}).Count(&rowCount).Error; err != nil {
		common.SysError("system role lift grandfather count failed: " + err.Error())
		return
	}

	var stored Option
	storedErr := DB.Where(map[string]any{"key": systemRoleLiftOptionKey}).Take(&stored).Error
	if storedErr != nil && !errors.Is(storedErr, gorm.ErrRecordNotFound) {
		common.SysError("system role lift grandfather read failed: " + storedErr.Error())
		return
	}
	if rowCount > 0 && errors.Is(storedErr, gorm.ErrRecordNotFound) {
		if err := writeOption(systemRoleLiftOptionKey, "true"); err != nil {
			common.SysError("failed to preserve claude.system_role_lift_enabled: " + err.Error())
			return
		}
		common.SysLog("存量站点未保存 claude.system_role_lift_enabled，按旧默认保持开启")
	}
	if err := writeOption(systemRoleLiftGrandfatherKey, "true"); err != nil {
		common.SysError("failed to record system role lift grandfather: " + err.Error())
	}
}

func optionMapHas(key string) bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if common.OptionMap == nil {
		return false
	}
	_, ok := common.OptionMap[key]
	return ok
}

func writeOption(key, value string) error {
	var option Option
	err := DB.Where(map[string]any{"key": key}).Take(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		option = Option{Key: key, Value: value}
		if err := DB.Create(&option).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if option.Value != value {
		option.Value = value
		if err := DB.Save(&option).Error; err != nil {
			return err
		}
	}
	if err := updateOptionMap(key, value); err != nil {
		return err
	}
	broadcastOptionUpdate()
	return nil
}

func SyncOptions(ctx context.Context, frequency int) {
	backgroundtask.RunPeriodic(ctx, time.Duration(frequency)*time.Second, false, func() {
		common.SysLog("syncing options from database")
		loadOptionsFromDatabase()
	})
}

// optionUpdateChannel 是集群内配置变更的广播频道。
const optionUpdateChannel = "option_updated"

// broadcastOptionUpdate 通知集群其余节点立即重载配置。
// 失败只记日志：数据库已写入成功，各节点仍会由 option-sync 在 SyncFrequency 内追平。
func broadcastOptionUpdate() {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := common.RDB.Publish(ctx, optionUpdateChannel, "").Err(); err != nil {
		common.SysLog("failed to broadcast option update: " + err.Error())
	}
}

// SubscribeOptionUpdates 监听集群广播并立即重载配置，让配置变更秒级收敛到所有节点。
// 与 SyncOptions 的定时轮询互补：轮询兜住广播丢失（Redis 重启、网络抖动）的情况。
func SubscribeOptionUpdates(ctx context.Context) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	sub := common.RDB.Subscribe(ctx, optionUpdateChannel)
	defer sub.Close()

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			common.SysLog("syncing options from database (cluster broadcast)")
			loadOptionsFromDatabase()
		}
	}
}

func UpdateOption(key string, value string) error {
	// Save to database first
	option := Option{
		Key: key,
	}
	// https://gorm.io/docs/update.html#Save-All-Fields
	DB.FirstOrCreate(&option, Option{Key: key})
	option.Value = value
	// Save is a combination function.
	// If save value does not contain primary key, it will execute Create,
	// otherwise it will execute Update (with all fields).
	DB.Save(&option)
	// Update OptionMap
	if err := updateOptionMap(key, value); err != nil {
		return err
	}
	broadcastOptionUpdate()
	return nil
}

func updateOptionMap(key string, value string) (err error) {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	common.OptionMap[key] = value

	// 检查是否是模型配置 - 使用更规范的方式处理
	if handleConfigUpdate(key, value) {
		return nil // 已由配置系统处理
	}

	// 处理传统配置项...
	if strings.HasSuffix(key, "Permission") {
		intValue, _ := strconv.Atoi(value)
		switch key {
		case "FileUploadPermission":
			common.FileUploadPermission = intValue
		case "FileDownloadPermission":
			common.FileDownloadPermission = intValue
		case "ImageUploadPermission":
			common.ImageUploadPermission = intValue
		case "ImageDownloadPermission":
			common.ImageDownloadPermission = intValue
		}
	}
	if strings.HasSuffix(key, "Enabled") || key == "DefaultCollapseSidebar" || key == "DefaultUseAutoGroup" || key == "SMTPForceAuthLogin" || key == "SensitiveDumpToFile" {
		boolValue := value == "true"
		switch key {
		case "PasswordRegisterEnabled":
			common.PasswordRegisterEnabled = boolValue
		case "PasswordLoginEnabled":
			common.PasswordLoginEnabled = boolValue
		case "EmailVerificationEnabled":
			common.EmailVerificationEnabled = boolValue
		case "GitHubOAuthEnabled":
			common.GitHubOAuthEnabled = boolValue
		case "LinuxDOOAuthEnabled":
			common.LinuxDOOAuthEnabled = boolValue
		case "WeChatAuthEnabled":
			common.WeChatAuthEnabled = boolValue
		case "TelegramOAuthEnabled":
			common.TelegramOAuthEnabled = boolValue
		case "TurnstileCheckEnabled":
			common.TurnstileCheckEnabled = boolValue
		case "RegisterEnabled":
			common.RegisterEnabled = boolValue
		case "EmailDomainRestrictionEnabled":
			common.EmailDomainRestrictionEnabled = boolValue
		case "EmailAliasRestrictionEnabled":
			common.EmailAliasRestrictionEnabled = boolValue
		case "AutomaticDisableChannelEnabled":
			common.AutomaticDisableChannelEnabled = boolValue
		case "AutomaticEnableChannelEnabled":
			common.AutomaticEnableChannelEnabled = boolValue
		case "ModelMissingRemovalEnabled":
			operation_setting.ModelMissingRemovalEnabled = boolValue
		case "ModelRateLimitRemovalEnabled":
			operation_setting.ModelRateLimitRemovalEnabled = boolValue
		case "ModelForbiddenRemovalEnabled":
			operation_setting.ModelForbiddenRemovalEnabled = boolValue
		case "ModelMissingRemovalCapEnabled":
			operation_setting.ModelMissingRemovalCapEnabled = boolValue
		case "ModelRemovalNotifyEnabled":
			operation_setting.ModelRemovalNotifyEnabled = boolValue
		case "LogConsumeEnabled":
			common.LogConsumeEnabled = boolValue
		case "DisplayInCurrencyEnabled":
			// 兼容旧字段：同步到新配置 general_setting.quota_display_type（运行时生效）
			// true -> USD, false -> TOKENS
			newVal := "USD"
			if !boolValue {
				newVal = "TOKENS"
			}
			if cfg := config.GlobalConfig.Get("general_setting"); cfg != nil {
				_ = config.UpdateConfigFromMap(cfg, map[string]string{"quota_display_type": newVal})
			}
		case "DisplayTokenStatEnabled":
			common.DisplayTokenStatEnabled = boolValue
		case "DrawingEnabled":
			common.DrawingEnabled = boolValue
		case "TaskEnabled":
			common.TaskEnabled = boolValue
		case "DataExportEnabled":
			common.DataExportEnabled = boolValue
		case "DefaultCollapseSidebar":
			common.DefaultCollapseSidebar = boolValue
		case "MjNotifyEnabled":
			setting.MjNotifyEnabled = boolValue
		case "MjAccountFilterEnabled":
			setting.MjAccountFilterEnabled = boolValue
		case "MjModeClearEnabled":
			setting.MjModeClearEnabled = boolValue
		case "MjForwardUrlEnabled":
			setting.MjForwardUrlEnabled = boolValue
		case "MjActionCheckSuccessEnabled":
			setting.MjActionCheckSuccessEnabled = boolValue
		case "CheckSensitiveEnabled":
			setting.CheckSensitiveEnabled = boolValue
		case "DemoSiteEnabled":
			operation_setting.DemoSiteEnabled = boolValue
		case "SelfUseModeEnabled":
			operation_setting.SelfUseModeEnabled = boolValue
		case "CheckSensitiveOnPromptEnabled":
			setting.CheckSensitiveOnPromptEnabled = boolValue
		case "ModelRequestRateLimitEnabled":
			setting.ModelRequestRateLimitEnabled = boolValue
		case "StopOnSensitiveEnabled":
			setting.StopOnSensitiveEnabled = boolValue
		case "SensitiveAsyncEnabled":
			setting.SetSensitiveAsyncEnabled(boolValue)
		case "SensitiveDumpToFile":
			setting.SetSensitiveDumpToFile(boolValue)
		case "SMTPSSLEnabled":
			common.SMTPSSLEnabled = boolValue
		case "SMTPForceAuthLogin":
			common.SMTPForceAuthLogin = boolValue
		case "WorkerAllowHttpImageRequestEnabled":
			system_setting.WorkerAllowHttpImageRequestEnabled = boolValue
		case "DefaultUseAutoGroup":
			setting.DefaultUseAutoGroup = boolValue
		case "ExposeRatioEnabled":
			ratio_setting.SetExposeRatioEnabled(boolValue)
		}
	}
	switch key {
	case "EmailDomainWhitelist":
		common.EmailDomainWhitelist = strings.Split(value, ",")
	case "SensitiveSampleRate":
		if intValue, parseErr := strconv.Atoi(value); parseErr == nil {
			setting.SetSensitiveSampleRate(intValue)
		}
	case "SensitiveDumpRetentionDays":
		if intValue, parseErr := strconv.Atoi(value); parseErr == nil {
			setting.SetSensitiveDumpRetentionDays(intValue)
		}
	case "SensitiveDumpDiskGuardPercent":
		if intValue, parseErr := strconv.Atoi(value); parseErr == nil {
			setting.SetSensitiveDumpDiskGuardPercent(intValue)
		}
	case "SMTPServer":
		common.SMTPServer = value
	case "SMTPPort":
		intValue, _ := strconv.Atoi(value)
		common.SMTPPort = intValue
	case "SMTPAccount":
		common.SMTPAccount = value
	case "SMTPFrom":
		common.SMTPFrom = value
	case "SMTPToken":
		common.SMTPToken = value
	case "ServerAddress":
		system_setting.ServerAddress = value
	case "WorkerUrl":
		system_setting.WorkerUrl = value
	case "WorkerValidKey":
		system_setting.WorkerValidKey = value
	case "PayAddress":
		operation_setting.PayAddress = value
	case "Chats":
		err = setting.UpdateChatsByJsonString(value)
	case "AutoGroups":
		err = setting.UpdateAutoGroupsByJsonString(value)
	case "ContentBackupSetting":
		err = operation_setting.UpdateContentBackupSettingByJsonString(value)
	case "CustomCallbackAddress":
		operation_setting.CustomCallbackAddress = value
	case "EpayId":
		operation_setting.EpayId = value
	case "EpayKey":
		operation_setting.EpayKey = value
	case "Price":
		operation_setting.Price, _ = strconv.ParseFloat(value, 64)
	case "USDExchangeRate":
		operation_setting.USDExchangeRate, _ = strconv.ParseFloat(value, 64)
	case "MinTopUp":
		operation_setting.MinTopUp, _ = strconv.Atoi(value)
	case "StripeApiSecret":
		setting.StripeApiSecret = value
	case "StripeWebhookSecret":
		setting.StripeWebhookSecret = value
	case "StripePriceId":
		setting.StripePriceId = value
	case "StripeUnitPrice":
		setting.StripeUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "StripeMinTopUp":
		setting.StripeMinTopUp, _ = strconv.Atoi(value)
	case "StripePromotionCodesEnabled":
		setting.StripePromotionCodesEnabled = value == "true"
	case "CreemApiKey":
		setting.CreemApiKey = value
	case "CreemProducts":
		setting.CreemProducts = value
	case "CreemTestMode":
		setting.CreemTestMode = value == "true"
	case "CreemWebhookSecret":
		setting.CreemWebhookSecret = value
	case "WaffoEnabled":
		setting.WaffoEnabled = value == "true"
	case "WaffoApiKey":
		setting.WaffoApiKey = value
	case "WaffoPrivateKey":
		setting.WaffoPrivateKey = value
	case "WaffoPublicCert":
		setting.WaffoPublicCert = value
	case "WaffoSandboxPublicCert":
		setting.WaffoSandboxPublicCert = value
	case "WaffoSandboxApiKey":
		setting.WaffoSandboxApiKey = value
	case "WaffoSandboxPrivateKey":
		setting.WaffoSandboxPrivateKey = value
	case "WaffoSandbox":
		setting.WaffoSandbox = value == "true"
	case "WaffoMerchantId":
		setting.WaffoMerchantId = value
	case "WaffoNotifyUrl":
		setting.WaffoNotifyUrl = value
	case "WaffoReturnUrl":
		setting.WaffoReturnUrl = value
	case "WaffoSubscriptionReturnUrl":
		setting.WaffoSubscriptionReturnUrl = value
	case "WaffoCurrency":
		setting.WaffoCurrency = value
	case "WaffoUnitPrice":
		setting.WaffoUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "WaffoMinTopUp":
		setting.WaffoMinTopUp, _ = strconv.Atoi(value)
	case "WaffoAllowedGroups":
		setting.WaffoAllowedGroups = value
	case "SfpayEnabled":
		setting.AgouEnabled = value == "true"
	case "SfpayBaseURL":
		setting.AgouBaseURL = value
	case "SfpayAppId":
		setting.AgouAppId = value
	case "SfpayAppSecret":
		setting.AgouAppSecret = value
	case "SfpayGroupCode":
		setting.AgouGroupCode = value
	case "SfpayNotifyUrl":
		setting.AgouNotifyUrl = value
	case "SfpayReturnUrl":
		setting.AgouReturnUrl = value
	case "SfpayUnitPrice":
		setting.AgouUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "SfpayMinTopUp":
		setting.AgouMinTopUp, _ = strconv.Atoi(value)
	case "SfpayMaxTopUp":
		setting.AgouMaxTopUp, _ = strconv.Atoi(value)
	case "SfpayAllowedCallbackIPs":
		setting.AgouAllowedCallbackIPs = value
	case "SfpayAlipayPayType":
		setting.AgouAlipayPayType = value
	case "SfpayWechatEnabled":
		setting.AgouWechatEnabled = value == "true"
	case "SfpayWechatPayType":
		setting.AgouWechatPayType = value
	case "SfpayAllowedGroups":
		setting.AgouAllowedGroups = value
	case "WaffoPancakeEnabled":
		setting.WaffoPancakeEnabled = value == "true"
	case "WaffoPancakeSandbox":
		setting.WaffoPancakeSandbox = value == "true"
	case "WaffoPancakeMerchantID":
		setting.WaffoPancakeMerchantID = value
	case "WaffoPancakePrivateKey":
		setting.WaffoPancakePrivateKey = value
	case "WaffoPancakeWebhookPublicKey":
		setting.WaffoPancakeWebhookPublicKey = value
	case "WaffoPancakeWebhookTestKey":
		setting.WaffoPancakeWebhookTestKey = value
	case "WaffoPancakeStoreID":
		setting.WaffoPancakeStoreID = value
	case "WaffoPancakeProductID":
		setting.WaffoPancakeProductID = value
	case "WaffoPancakeReturnURL":
		setting.WaffoPancakeReturnURL = value
	case "WaffoPancakeCurrency":
		setting.WaffoPancakeCurrency = value
	case "WaffoPancakeUnitPrice":
		setting.WaffoPancakeUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "WaffoPancakeMinTopUp":
		setting.WaffoPancakeMinTopUp, _ = strconv.Atoi(value)
	case "WaffoPancakeAllowedGroups":
		setting.WaffoPancakeAllowedGroups = value
	case "CryptomusEnabled":
		setting.CryptomusEnabled = value == "true"
	case "CryptomusMerchantID":
		setting.CryptomusMerchantID = value
	case "CryptomusPaymentApiKey":
		setting.CryptomusPaymentApiKey = value
	case "CryptomusWebhookApiKey":
		setting.CryptomusWebhookApiKey = value
	case "CryptomusDefaultCurrency":
		setting.CryptomusDefaultCurrency = value
	case "CryptomusDefaultNetwork":
		setting.CryptomusDefaultNetwork = value
	case "CryptomusAllowedCurrencies":
		setting.CryptomusAllowedCurrencies = value
	case "CryptomusUnitPrice":
		setting.CryptomusUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "CryptomusMinTopUp":
		setting.CryptomusMinTopUp, _ = strconv.Atoi(value)
	case "CryptomusReturnURL":
		setting.CryptomusReturnURL = value
	case "CryptomusAllowedGroups":
		setting.CryptomusAllowedGroups = value
	case "BepusdtEnabled":
		setting.BepusdtEnabled = value == "true"
	case "BepusdtBaseURL":
		setting.BepusdtBaseURL = strings.TrimRight(strings.TrimSpace(value), "/")
	case "BepusdtApiToken":
		setting.BepusdtApiToken = strings.TrimSpace(value)
	case "BepusdtCurrencies":
		setting.BepusdtCurrencies = value
	case "BepusdtLifetimeSec":
		setting.BepusdtLifetimeSec, _ = strconv.Atoi(value)
	case "BepusdtUnitPrice":
		setting.BepusdtUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "BepusdtMinTopUp":
		setting.BepusdtMinTopUp, _ = strconv.Atoi(value)
	case "BepusdtReturnURL":
		setting.BepusdtReturnURL = value
	case "BepusdtAllowedGroups":
		setting.BepusdtAllowedGroups = value
	case "TopupGroupRatio":
		err = common.UpdateTopupGroupRatioByJSONString(value)
	case "GitHubClientId":
		common.GitHubClientId = value
	case "GitHubClientSecret":
		common.GitHubClientSecret = value
	case "LinuxDOClientId":
		common.LinuxDOClientId = value
	case "LinuxDOClientSecret":
		common.LinuxDOClientSecret = value
	case "LinuxDOMinimumTrustLevel":
		common.LinuxDOMinimumTrustLevel, _ = strconv.Atoi(value)
	case "Footer":
		common.Footer = value
	case "SystemName":
		common.SystemName = value
	case "Logo":
		common.Logo = value
	case "WeChatServerAddress":
		common.WeChatServerAddress = value
	case "WeChatServerToken":
		common.WeChatServerToken = value
	case "WeChatAccountQRCodeImageURL":
		common.WeChatAccountQRCodeImageURL = value
	case "TelegramBotToken":
		common.TelegramBotToken = value
	case "TelegramBotName":
		common.TelegramBotName = value
	case "TurnstileSiteKey":
		common.TurnstileSiteKey = value
	case "TurnstileSecretKey":
		common.TurnstileSecretKey = value
	case "QuotaForNewUser":
		common.QuotaForNewUser, _ = strconv.Atoi(value)
	case "QuotaForInviter":
		common.QuotaForInviter, _ = strconv.Atoi(value)
	case "QuotaForInvitee":
		common.QuotaForInvitee, _ = strconv.Atoi(value)
	case "AffiliateCommissionEnabled":
		common.AffiliateCommissionEnabled, _ = strconv.ParseBool(value)
	case "AffiliateCommissionRatio":
		if v, parseErr := strconv.ParseFloat(value, 64); parseErr == nil {
			if v < 0 {
				v = 0
			} else if v > 1 {
				v = 1
			}
			common.AffiliateCommissionRatio = v
		}
	case "RewardInviterOnEffectiveOnly":
		common.RewardInviterOnEffectiveOnly, _ = strconv.ParseBool(value)
	case "EffectiveInviteeConsumeThreshold":
		common.EffectiveInviteeConsumeThreshold, _ = strconv.Atoi(value)
	case "QuotaRemindThreshold":
		common.QuotaRemindThreshold, _ = strconv.Atoi(value)
	case "PreConsumedQuota":
		common.PreConsumedQuota, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitCount":
		setting.ModelRequestRateLimitCount, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitDurationMinutes":
		setting.ModelRequestRateLimitDurationMinutes, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitSuccessCount":
		setting.ModelRequestRateLimitSuccessCount, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitGroup":
		err = setting.UpdateModelRequestRateLimitGroupByJSONString(value)
	case "RetryTimes":
		common.RetryTimes, _ = strconv.Atoi(value)
	case "DataExportInterval":
		common.DataExportInterval, _ = strconv.Atoi(value)
	case "DataExportDefaultTime":
		common.DataExportDefaultTime = value
	case "ModelRatio":
		err = ratio_setting.UpdateModelRatioByJSONString(value)
	case "GroupRatio":
		err = ratio_setting.UpdateGroupRatioByJSONString(value)
	case "GroupGroupRatio":
		err = ratio_setting.UpdateGroupGroupRatioByJSONString(value)
	case "UserUsableGroups":
		err = setting.UpdateUserUsableGroupsByJSONString(value)
	case "CompletionRatio":
		err = ratio_setting.UpdateCompletionRatioByJSONString(value)
	case "ModelPrice":
		err = ratio_setting.UpdateModelPriceByJSONString(value)
	case "CacheRatio":
		err = ratio_setting.UpdateCacheRatioByJSONString(value)
	case "CreateCacheRatio":
		err = ratio_setting.UpdateCreateCacheRatioByJSONString(value)
	case "ImageRatio":
		err = ratio_setting.UpdateImageRatioByJSONString(value)
	case "AudioRatio":
		err = ratio_setting.UpdateAudioRatioByJSONString(value)
	case "AudioCompletionRatio":
		err = ratio_setting.UpdateAudioCompletionRatioByJSONString(value)
	case "TopUpLink":
		common.TopUpLink = value
	//case "ChatLink":
	//	common.ChatLink = value
	//case "ChatLink2":
	//	common.ChatLink2 = value
	case "ChannelDisableThreshold":
		common.ChannelDisableThreshold, _ = strconv.ParseFloat(value, 64)
	case "QuotaPerUnit":
		common.QuotaPerUnit, _ = strconv.ParseFloat(value, 64)
	case "SensitiveWords":
		setting.SensitiveWordsFromString(value)
	case "AutomaticDisableKeywords":
		operation_setting.AutomaticDisableKeywordsFromString(value)
	case "AutomaticDisableStatusCodes":
		err = operation_setting.AutomaticDisableStatusCodesFromString(value)
	case "AutomaticRetryStatusCodes":
		err = operation_setting.AutomaticRetryStatusCodesFromString(value)
	case "ModelMissingRemovalCooldownSeconds":
		if intValue, parseErr := strconv.Atoi(value); parseErr == nil && intValue >= 1 {
			operation_setting.ModelMissingRemovalCooldownSeconds = intValue
		}
	case "ModelMissingRecheckIntervalSeconds":
		if intValue, parseErr := strconv.Atoi(value); parseErr == nil && intValue >= 60 {
			operation_setting.ModelMissingRecheckIntervalSeconds = intValue
		}
	case "ModelMissingKeywords":
		operation_setting.ModelMissingKeywordsFromString(value)
	case "ModelRateLimitRecheckIntervalSeconds":
		if intValue, parseErr := strconv.Atoi(value); parseErr == nil && intValue >= 60 {
			operation_setting.ModelRateLimitRecheckIntervalSeconds = intValue
		}
	case "ModelForbiddenStatusCodes":
		err = operation_setting.ModelForbiddenStatusCodesFromString(value)
	case "ModelForbiddenKeywords":
		operation_setting.ModelForbiddenKeywordsFromString(value)
	case "ModelForbiddenRecheckIntervalSeconds":
		if intValue, parseErr := strconv.Atoi(value); parseErr == nil && intValue >= 60 {
			operation_setting.ModelForbiddenRecheckIntervalSeconds = intValue
		}
	case "ModelRemovalMaxRemovedPerChannel":
		if intValue, parseErr := strconv.Atoi(value); parseErr == nil && intValue >= 1 {
			operation_setting.ModelRemovalMaxRemovedPerChannel = intValue
		}
	case "ModelRemovalConsecutiveThreshold":
		// 连续 N 次才摘除的门槛；<1 无意义（会让门槛失效变成从不摘），拒写。
		if intValue, parseErr := strconv.Atoi(value); parseErr == nil && intValue >= 1 {
			operation_setting.ModelRemovalConsecutiveThreshold = intValue
		}
	case "ModelRemovalCapAction":
		// 枚举白名单校验：写错值直接报错，不静默回落成 alert_only——否则管理员以为已开启
		// 升级停渠道而实际没生效。
		if !operation_setting.IsValidModelRemovalCapAction(value) {
			err = fmt.Errorf("invalid ModelRemovalCapAction: %s (expected %s or %s)", value,
				operation_setting.ModelRemovalCapActionAlertOnly, operation_setting.ModelRemovalCapActionDisableChannel)
			break
		}
		operation_setting.ModelRemovalCapAction = value
	case "StreamCacheQueueLength":
		setting.StreamCacheQueueLength, _ = strconv.Atoi(value)
	case "PayMethods":
		err = operation_setting.UpdatePayMethodsByJsonString(value)
	case "WaffoPayMethods", "SfpayPayChannels", "CryptomusPayChannels", "WaffoPancakePayChannels", "BepusdtPayChannels":
		// 这些支付方式/渠道配置直接从 OptionMap 读（setting.GetWaffoPayMethods / GetXxxPayChannels），
		// 值已在本函数开头 common.OptionMap[key] = value 写入，无需额外同步内存变量。
	case "WaffoPancakeLogo":
		setting.WaffoPancakeLogo = value
	case "CryptomusLogo":
		setting.CryptomusLogo = value
	case "BepusdtLogo":
		setting.BepusdtLogo = value
	case "SfpayLogo":
		setting.AgouLogo = value
	}
	return err
}

// handleConfigUpdate 处理分层配置更新，返回是否已处理
func handleConfigUpdate(key, value string) bool {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return false // 不是分层配置
	}

	configName := parts[0]
	configKey := parts[1]

	// 获取配置对象
	cfg := config.GlobalConfig.Get(configName)
	if cfg == nil {
		return false // 未注册的配置
	}

	// 更新配置
	configMap := map[string]string{
		configKey: value,
	}
	config.UpdateConfigFromMap(cfg, configMap)

	// 特定配置的后处理
	if configName == "performance_setting" {
		performance_setting.UpdateAndSync()
	} else if configName == "tool_price_setting" {
		operation_setting.RebuildToolPriceIndex()
	} else if configName == "billing_setting" {
		InvalidatePricingCache()
		ratio_setting.InvalidateExposedDataCache()
	} else if configName == "theme" {
		system_setting.UpdateAndSyncTheme()
	} else if configName == "channel_health_setting" {
		operation_setting.NormalizeChannelHealthConfig()
	}

	return true // 已处理
}

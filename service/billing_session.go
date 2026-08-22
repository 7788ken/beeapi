package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billinglifecycle"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
type BillingSession struct {
	relayInfo        *relaycommon.RelayInfo
	funding          FundingSource
	trusted          bool // 信任旁路：未预扣、结算走批量聚合
	preConsumedQuota int  // 实际预扣额度（信任用户可能为 0）
	tokenConsumed    int  // 令牌额度实际扣减量
	extraReserved    int  // 发送前补充预扣的额度（订阅退款时需要单独回滚）
	fundingSettled   bool // funding.Settle 已成功，资金来源已提交
	settled          bool // Settle 全部完成（资金 + 令牌）
	refunded         bool // Refund 已调用
	mu               sync.Mutex
}

var reserveBillingRoot = billinglifecycle.ReserveRoot

// walletRecordId 返回钱包预扣凭据的幂等键。取 relay 的 RequestId，
// 缺失时返回空串（退化为无凭据模式，不阻断计费）。
func (s *BillingSession) walletRecordId() string {
	if s.relayInfo == nil {
		return ""
	}
	return s.relayInfo.RequestId
}

// Settle 根据实际消耗额度进行结算。
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return nil
	}
	if wallet, ok := s.funding.(*WalletFunding); ok && s.trusted {
		// 信任旁路：请求未做预扣、无凭据，结算即记债。走批量聚合合并同一
		// 用户的高频扣减，消除 users.quota 单行热点。
		if actualQuota > 0 {
			if err := model.TrustedSettleUserQuota(wallet.userId, actualQuota); err != nil {
				return err
			}
			// 限额令牌同样走批量聚合补扣 remain/used；无限令牌两字段本就不变。
			// 资金已提交，令牌补扣失败只记日志，不回滚结算（与非信任路径口径一致）。
			if !s.relayInfo.TokenUnlimited && !s.relayInfo.IsPlayground {
				if err := model.TrustedSettleTokenQuota(s.relayInfo.TokenId, actualQuota); err != nil {
					common.SysLog(fmt.Sprintf("error settling token quota after trusted funding settled (userId=%d, tokenId=%d, quota=%d): %s",
						s.relayInfo.UserId, s.relayInfo.TokenId, actualQuota, err.Error()))
				}
			}
		}
		wallet.consumed = actualQuota
		s.fundingSettled = true
		s.settled = true
		s.syncRelayInfo()
		return nil
	}
	delta := actualQuota - s.preConsumedQuota
	if delta == 0 {
		// 预扣恰好等于实际用量：钱不用动，但凭据必须关闭，
		// 否则清扫任务会误判为遗留预扣并重复退款。
		if _, ok := s.funding.(*WalletFunding); ok && !s.relayInfo.IsPlayground {
			if err := model.FinalizeUserTokenQuota(
				s.walletRecordId(),
				s.relayInfo.UserId,
				s.relayInfo.TokenId,
				0,
				model.WalletPreConsumeStatusSettled,
			); err != nil {
				return err
			}
		}
		s.settled = true
		return nil
	}
	if wallet, ok := s.funding.(*WalletFunding); ok && !s.relayInfo.IsPlayground {
		// 结算与关闭预扣凭据必须同一事务：否则清扫任务会把已结算的请求
		// 当成遗留预扣再退一次钱。
		if err := model.FinalizeUserTokenQuota(
			s.walletRecordId(),
			s.relayInfo.UserId,
			s.relayInfo.TokenId,
			delta,
			model.WalletPreConsumeStatusSettled,
		); err != nil {
			return err
		}
		wallet.consumed += delta
		s.tokenConsumed += delta
		s.fundingSettled = true
		s.settled = true
		s.syncRelayInfo()
		return nil
	}
	if subscription, ok := s.funding.(*SubscriptionFunding); ok && !s.relayInfo.IsPlayground {
		if err := model.AdjustSubscriptionTokenQuota(
			s.relayInfo.UserId,
			subscription.subscriptionId,
			s.relayInfo.TokenId,
			delta,
		); err != nil {
			return err
		}
		s.tokenConsumed += delta
		s.relayInfo.SubscriptionPostDelta += int64(delta)
		s.fundingSettled = true
		s.settled = true
		return nil
	}
	// Playground 不扣令牌，仅调整资金来源。
	if !s.fundingSettled {
		if err := s.funding.Settle(delta); err != nil {
			return err
		}
		s.fundingSettled = true
	}
	var tokenErr error
	if !s.relayInfo.IsPlayground {
		if delta > 0 {
			tokenErr = model.DecreaseTokenQuota(s.relayInfo.TokenId, delta)
		} else {
			tokenErr = model.IncreaseTokenQuota(s.relayInfo.TokenId, -delta)
		}
		if tokenErr != nil {
			// 资金来源已提交，令牌调整失败只能记录日志；标记 settled 防止 Refund 误退资金
			common.SysLog(fmt.Sprintf("error adjusting token quota after funding settled (userId=%d, tokenId=%d, delta=%d): %s",
				s.relayInfo.UserId, s.relayInfo.TokenId, delta, tokenErr.Error()))
		}
	}
	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(delta)
	}
	s.settled = true
	return tokenErr
}

// Refund 退还所有预扣费，幂等安全，异步执行。
func (s *BillingSession) Refund(c *gin.Context) {
	s.mu.Lock()
	if s.settled || s.refunded || !s.needsRefundLocked() {
		s.mu.Unlock()
		return
	}
	ticket, err := reserveBillingRoot("billing-refund")
	if err != nil {
		s.mu.Unlock()
		common.SysLog("failed to reserve billing refund: " + err.Error())
		return
	}
	submitted := false
	sessionLocked := true
	defer func() {
		recovered := recover()
		if !submitted {
			if releaseErr := ticket.Release(); releaseErr != nil {
				common.SysLog("failed to release unsubmitted billing refund: " + releaseErr.Error())
			}
			if sessionLocked {
				if !s.settled {
					s.refunded = false
				}
				s.mu.Unlock()
				sessionLocked = false
			} else {
				s.mu.Lock()
				if !s.settled {
					s.refunded = false
				}
				s.mu.Unlock()
			}
		}
		if recovered != nil {
			panic(recovered)
		}
	}()
	s.refunded = true
	s.mu.Unlock()
	sessionLocked = false

	logger.LogInfo(c, fmt.Sprintf("用户 %d 请求失败, 返还预扣费（token_quota=%s, funding=%s）",
		s.relayInfo.UserId,
		logger.FormatQuota(s.tokenConsumed),
		s.funding.Source(),
	))

	// 复制需要的值到闭包中
	tokenId := s.relayInfo.TokenId
	isPlayground := s.relayInfo.IsPlayground
	tokenConsumed := s.tokenConsumed
	extraReserved := s.extraReserved
	subscriptionId := s.relayInfo.SubscriptionId
	funding := s.funding
	walletRecordId := s.walletRecordId()

	if err := ticket.Submit(func(_ *billinglifecycle.Ticket) {
		if wallet, ok := funding.(*WalletFunding); ok && !isPlayground {
			// 退款与关闭凭据同一事务。失败时凭据仍停留在 reserved，
			// 由后台清扫任务在数据库恢复后完成退款——不再永久丢钱。
			if err := model.FinalizeUserTokenQuota(
				walletRecordId,
				wallet.userId,
				tokenId,
				-wallet.consumed,
				model.WalletPreConsumeStatusRefunded,
			); err != nil {
				common.SysLog(fmt.Sprintf(
					"error atomically refunding wallet and token quota (request=%s, userId=%d, quota=%d), reservation left for sweeper: %s",
					walletRecordId, wallet.userId, wallet.consumed, err.Error(),
				))
				s.mu.Lock()
				s.refunded = false
				s.mu.Unlock()
			}
			return
		}
		if _, ok := funding.(*SubscriptionFunding); ok && !isPlayground {
			if err := funding.Refund(); err != nil {
				common.SysLog("error atomically refunding subscription and token quota: " + err.Error())
				s.mu.Lock()
				s.refunded = false
				s.mu.Unlock()
			}
			return
		}
		// 1) 退还资金来源
		if err := funding.Refund(); err != nil {
			common.SysLog("error refunding billing source: " + err.Error())
		}
		if extraReserved > 0 && funding.Source() == BillingSourceSubscription && subscriptionId > 0 {
			if err := model.PostConsumeUserSubscriptionDelta(subscriptionId, -int64(extraReserved)); err != nil {
				common.SysLog("error refunding subscription extra reserved quota: " + err.Error())
			}
		}
		// 2) 退还令牌额度
		if tokenConsumed > 0 && !isPlayground {
			if err := model.IncreaseTokenQuota(tokenId, tokenConsumed); err != nil {
				common.SysLog("error refunding token quota: " + err.Error())
			}
		}
	}); err != nil {
		common.SysLog("failed to submit billing refund: " + err.Error())
		return
	}
	submitted = true
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	if s.settled || s.refunded || s.fundingSettled {
		// fundingSettled 时资金来源已提交结算，不能再退预扣费
		return false
	}
	if s.tokenConsumed > 0 {
		return true
	}
	// 订阅可能在 tokenConsumed=0 时仍预扣了额度
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.preConsumed > 0 {
		return true
	}
	return false
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	return s.preConsumedQuota
}

func (s *BillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.trusted {
		// 信任旁路不持有预扣，无需追加。
		return nil
	}
	if s.settled || s.refunded || targetQuota <= s.preConsumedQuota {
		return nil
	}

	delta := targetQuota - s.preConsumedQuota
	if delta <= 0 {
		return nil
	}

	if wallet, ok := s.funding.(*WalletFunding); ok && !s.relayInfo.IsPlayground {
		if err := model.ExtendUserTokenReservation(
			s.walletRecordId(),
			s.relayInfo.UserId,
			s.relayInfo.TokenId,
			delta,
		); err != nil {
			return err
		}
		wallet.consumed += delta
		s.preConsumedQuota += delta
		s.tokenConsumed += delta
		s.extraReserved += delta
		s.syncRelayInfo()
		return nil
	}
	if subscription, ok := s.funding.(*SubscriptionFunding); ok && !s.relayInfo.IsPlayground {
		if err := model.ReserveSubscriptionPreConsumeTokenQuota(
			subscription.requestId,
			s.relayInfo.UserId,
			subscription.subscriptionId,
			s.relayInfo.TokenId,
			delta,
		); err != nil {
			return err
		}
		s.preConsumedQuota += delta
		s.tokenConsumed += delta
		s.extraReserved += delta
		s.syncRelayInfo()
		return nil
	}

	if err := s.reserveFunding(delta); err != nil {
		return err
	}
	if err := s.reserveToken(delta); err != nil {
		s.rollbackFundingReserve(delta)
		return err
	}

	s.preConsumedQuota += delta
	s.tokenConsumed += delta
	s.extraReserved += delta
	s.syncRelayInfo()
	return nil
}

// ---------------------------------------------------------------------------
// PreConsume — 统一预扣费入口（含信任额度旁路）
// ---------------------------------------------------------------------------

// preConsume 执行预扣费。
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	effectiveQuota := quota

	if effectiveQuota > 0 {
		logger.LogInfo(c, fmt.Sprintf("用户 %d 需要预扣费 %s (funding=%s)", s.relayInfo.UserId, logger.FormatQuota(effectiveQuota), s.funding.Source()))
	}

	if effectiveQuota > 0 {
		if wallet, ok := s.funding.(*WalletFunding); ok && !s.relayInfo.IsPlayground {
			if err := model.ReserveUserTokenQuotaWithRecord(
				s.walletRecordId(),
				s.relayInfo.UserId,
				s.relayInfo.TokenId,
				effectiveQuota,
			); err != nil {
				errorCode := types.ErrorCodePreConsumeTokenQuotaFailed
				if errors.Is(err, model.ErrInsufficientUserQuota) {
					errorCode = types.ErrorCodeInsufficientUserQuota
				}
				return types.NewErrorWithStatusCode(err, errorCode, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
			}
			wallet.consumed = effectiveQuota
			s.tokenConsumed = effectiveQuota
			s.preConsumedQuota = effectiveQuota
			s.syncRelayInfo()
			return nil
		}
		if subscription, ok := s.funding.(*SubscriptionFunding); ok && !s.relayInfo.IsPlayground {
			if err := subscription.PreConsume(effectiveQuota); err != nil {
				return subscriptionPreConsumeError(err)
			}
			s.tokenConsumed = effectiveQuota
			s.preConsumedQuota = effectiveQuota
			s.syncRelayInfo()
			return nil
		}
		if err := PreConsumeTokenQuota(s.relayInfo, effectiveQuota); err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		s.tokenConsumed = effectiveQuota
	}

	if err := s.funding.PreConsume(effectiveQuota); err != nil {
		// 预扣费失败，回滚令牌额度
		if s.tokenConsumed > 0 && !s.relayInfo.IsPlayground {
			if rollbackErr := model.IncreaseTokenQuota(s.relayInfo.TokenId, s.tokenConsumed); rollbackErr != nil {
				common.SysLog(fmt.Sprintf("error rolling back token quota (userId=%d, tokenId=%d, amount=%d, fundingErr=%s): %s",
					s.relayInfo.UserId, s.relayInfo.TokenId, s.tokenConsumed, err.Error(), rollbackErr.Error()))
			}
			s.tokenConsumed = 0
		}
		return subscriptionPreConsumeError(err)
	}

	s.preConsumedQuota = effectiveQuota

	// ---- 同步 RelayInfo 兼容字段 ----
	s.syncRelayInfo()

	return nil
}

func subscriptionPreConsumeError(err error) *types.NewAPIError {
	// TODO: model 层应为所有额度不足分支提供统一哨兵错误。
	errMsg := err.Error()
	if errors.Is(err, model.ErrInsufficientTokenQuota) {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	if errors.Is(err, model.ErrNoEligibleSubscription) ||
		strings.Contains(errMsg, "no active subscription") ||
		strings.Contains(errMsg, "subscription quota insufficient") {
		return types.NewErrorWithStatusCode(fmt.Errorf("订阅额度不足或未配置订阅: %s", errMsg), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
}

func (s *BillingSession) reserveFunding(delta int) error {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := model.DecreaseUserQuota(funding.userId, delta); err != nil {
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		funding.consumed += delta
		return nil
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, int64(delta)); err != nil {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("订阅额度不足或未配置订阅: %s", err.Error()),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		return nil
	default:
		return types.NewError(fmt.Errorf("unsupported funding source: %s", s.funding.Source()), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
}

func (s *BillingSession) rollbackFundingReserve(delta int) {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := model.IncreaseUserQuota(funding.userId, delta); err != nil {
			common.SysLog("error rolling back wallet funding reserve: " + err.Error())
		} else {
			funding.consumed -= delta
		}
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, -int64(delta)); err != nil {
			common.SysLog("error rolling back subscription funding reserve: " + err.Error())
		}
	}
}

func (s *BillingSession) reserveToken(delta int) error {
	if delta <= 0 || s.relayInfo.IsPlayground {
		return nil
	}
	if err := PreConsumeTokenQuota(s.relayInfo, delta); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return nil
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed + int64(s.extraReserved)
		info.SubscriptionPostDelta = 0
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter + int64(s.extraReserved)
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
	}
}

// walletTrustThresholdQuota 返回钱包信任旁路阈值（quota 单位），0 = 关闭。
func walletTrustThresholdQuota() int {
	usd := operation_setting.GetQuotaSetting().WalletTrustQuotaUsd
	if usd <= 0 {
		return 0
	}
	return int(usd * common.QuotaPerUnit)
}

// ---------------------------------------------------------------------------
// NewBillingSession 工厂 — 根据计费偏好创建会话并处理回退
// ---------------------------------------------------------------------------

// NewBillingSession 根据用户计费偏好创建 BillingSession，处理无订阅时的钱包回退。
// 订阅额度耗尽一律返回 403（不回退钱包），耗尽后的分组降级由耗尽定时任务处理。
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)

	// 钱包路径需要先检查用户额度
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if userQuota <= 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("用户额度不足, 剩余额度: %s", logger.FormatQuota(userQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if userQuota-preConsumedQuota < 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("预扣费额度失败, 用户剩余额度: %s, 需要预扣费额度: %s", logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		relayInfo.UserQuota = userQuota

		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   &WalletFunding{userId: relayInfo.UserId},
		}
		if trustQuota := walletTrustThresholdQuota(); trustQuota > 0 &&
			!relayInfo.ForcePreConsume &&
			!relayInfo.IsPlayground &&
			userQuota-preConsumedQuota >= trustQuota {
			// 信任旁路：余额扣除本次预估后仍不低于阈值，且令牌无限额度
			// 或令牌剩余额度同样高于阈值（对齐上游 shouldTrust 语义），
			// 跳过预扣（不写 users 行、不落凭据），结算时走批量聚合。
			tokenTrusted := relayInfo.TokenUnlimited
			if !tokenTrusted {
				tokenTrusted = c.GetInt("token_quota") > trustQuota
			}
			if tokenTrusted {
				session.trusted = true
				session.syncRelayInfo()
				logger.LogInfo(c, fmt.Sprintf("用户 %d 余额 %s 高于信任阈值 %s 且令牌额度充足, 跳过预扣费",
					relayInfo.UserId, logger.FormatQuota(userQuota), logger.FormatQuota(trustQuota)))
				return session, nil
			}
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subConsume := int64(preConsumedQuota)
		if subConsume <= 0 {
			subConsume = 1
		}
		session := &BillingSession{
			relayInfo: relayInfo,
			funding: &SubscriptionFunding{
				requestId:     relayInfo.RequestId,
				userId:        relayInfo.UserId,
				tokenId:       relayInfo.TokenId,
				tokenRequired: !relayInfo.IsPlayground,
				modelName:     relayInfo.OriginModelName,
				amount:        subConsume,
				usingGroup:    relayInfo.UsingGroup,
			},
		}
		// 必须传 subConsume 而非 preConsumedQuota，保证 SubscriptionFunding.amount、
		// preConsume 参数和 FinalPreConsumedQuota 三者一致，避免订阅多扣费。
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	switch pref {
	case "subscription_only":
		// 严格按 group 匹配查询：仅当用户存在 BoundGroup == relayInfo.UsingGroup 的活跃订阅时才走订阅。
		sub, _, _, subCheckErr := model.GetEligibleActiveSubscription(relayInfo.UserId, relayInfo.UsingGroup)
		if subCheckErr != nil {
			return nil, types.NewError(subCheckErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if sub == nil {
			// subscription_only 找不到匹配 group 的订阅 → 直接报错，不悄悄走钱包。
			// 消息要可操作：API 调用方（非网页）只能从这条 message 得知被拒原因。
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("当前分组「%s」无生效订阅（订阅可能已到期）。请续费订阅，或在「我的订阅」页将消费模式切换为「优先钱包」或「仅用钱包」后重试", relayInfo.UsingGroup),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, err := tryWallet()
		if err != nil {
			if err.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				return trySubscription()
			}
			return nil, err
		}
		return session, nil
	case "subscription_first", "subscription_then_auto":
		fallthrough
	default:
		sub, _, hasExhausted, subCheckErr := model.GetEligibleActiveSubscription(relayInfo.UserId, relayInfo.UsingGroup)
		if subCheckErr != nil {
			return nil, types.NewError(subCheckErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if sub == nil {
			if hasExhausted {
				// 该分组无活跃订阅但存在耗尽订阅：分组套餐价只对订阅额度有效，
				// 不得回退钱包以该分组倍率扣费（token 显式绑定订阅分组时会走到这里）。
				if pref == "subscription_then_auto" {
					return nil, types.NewErrorWithStatusCode(
						fmt.Errorf("当前分组「%s」订阅额度已用完，已切换为按量计费。请使用未绑定分组的令牌调用，或等待额度重置/续费订阅", relayInfo.UsingGroup),
						types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
						types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
				}
				return nil, types.NewErrorWithStatusCode(
					fmt.Errorf("当前分组「%s」订阅额度已用完，调用已暂停（不会扣减钱包余额）。请续费订阅或等待额度重置", relayInfo.UsingGroup),
					types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
					types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
			}
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr != nil {
			if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				// 订阅额度耗尽时不再静默回退钱包：回退会沿用订阅分组倍率扣余额，
				// 计价口径与资金来源错配。耗尽的后续处理（降级 auto / 保持暂停）由
				// ProcessExhaustedSubscriptions 定时任务按用户偏好执行。
				if pref == "subscription_then_auto" {
					return nil, types.NewErrorWithStatusCode(
						fmt.Errorf("当前分组「%s」订阅额度已用完，系统将在约 1 分钟内自动切换为按量计费，请稍后重试", relayInfo.UsingGroup),
						types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
						types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
				}
				return nil, types.NewErrorWithStatusCode(
					fmt.Errorf("当前分组「%s」订阅额度已用完，调用已暂停（不会扣减钱包余额）。请续费订阅，或在「我的订阅」页将消费模式切换为「优先订阅（用完转按量）」后重试", relayInfo.UsingGroup),
					types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
					types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
			}
			return nil, apiErr
		}
		return session, nil
	}
}

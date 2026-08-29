package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	// Import oauth package to register providers via init()
	_ "github.com/QuantumNous/new-api/oauth"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetApiRouter(router *gin.Engine) {
	apiRouter := router.Group("/api")
	apiRouter.Use(middleware.RouteTag("api"))
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.BodyStorageCleanup()) // 清理请求体存储
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	anonymousRequestBodyLimit := middleware.AnonymousRequestBodyLimit()
	sessionCookieOriginGuard := middleware.SessionCookieOriginGuard()
	{
		apiRouter.GET("/setup", controller.GetSetup)
		apiRouter.POST("/setup", anonymousRequestBodyLimit, controller.PostSetup)
		apiRouter.GET("/status", controller.GetStatus)
		apiRouter.GET("/uptime/status", controller.GetUptimeKumaStatus)
		apiRouter.GET("/models", middleware.UserAuth(), controller.DashboardListModels)
		apiRouter.GET("/status/test", middleware.AdminAuth(), controller.TestStatus)
		apiRouter.GET("/notice", controller.GetNotice)
		apiRouter.GET("/user-agreement", controller.GetUserAgreement)
		apiRouter.GET("/privacy-policy", controller.GetPrivacyPolicy)
		apiRouter.GET("/about", controller.GetAbout)
		//apiRouter.GET("/midjourney", controller.GetMidjourney)
		apiRouter.GET("/home_page_content", controller.GetHomePageContent)
		apiRouter.GET("/pricing", middleware.TryUserAuth(), controller.GetPricing)
		// 价格变动展示与通知（Docs/price-change-notify-design.md）：用户侧匿名可访问（分组过滤同 pricing），管理侧 AdminAuth
		priceChangesRoute := apiRouter.Group("/price_changes")
		{
			priceChangesRoute.GET("", middleware.TryUserAuth(), controller.GetPriceChanges)
			priceChangesAdminRoute := priceChangesRoute.Group("")
			priceChangesAdminRoute.Use(middleware.AdminAuth())
			{
				priceChangesAdminRoute.GET("/pending", controller.GetPendingPriceChanges)
				priceChangesAdminRoute.POST("/publish", controller.PublishPriceChanges)
				priceChangesAdminRoute.GET("/batches", controller.GetPricePublishBatches)
				priceChangesAdminRoute.GET("/batches/:id", controller.GetPricePublishBatchDetail)
			}
		}
		perfMetricsRoute := apiRouter.Group("/perf-metrics")
		perfMetricsRoute.Use(middleware.TryUserAuth())
		{
			perfMetricsRoute.GET("/summary", controller.GetPerfMetricsSummary)
			perfMetricsRoute.GET("/groups", controller.GetPerfMetricsGroupUptime)
			perfMetricsRoute.GET("", controller.GetPerfMetrics)
		}
		apiRouter.GET("/rankings", controller.GetRankings)
		apiRouter.GET("/verification", middleware.EmailVerificationRateLimit(), middleware.TurnstileCheck(), controller.SendEmailVerification)
		apiRouter.GET("/reset_password", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.SendPasswordResetEmail)
		apiRouter.POST("/user/reset", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.ResetPassword)
		// OAuth routes - specific routes must come before :provider wildcard
		apiRouter.GET("/oauth/state", sessionCookieOriginGuard, middleware.CriticalRateLimit(), middleware.TryUserAuth(), controller.GenerateOAuthCode)
		apiRouter.POST("/oauth/email/bind", sessionCookieOriginGuard, middleware.CriticalRateLimit(), anonymousRequestBodyLimit, middleware.UserAuth(), controller.EmailBind)
		// Non-standard OAuth (WeChat, Telegram) - keep original routes
		apiRouter.GET("/oauth/wechat", sessionCookieOriginGuard, middleware.CriticalRateLimit(), controller.WeChatAuth)
		apiRouter.POST("/oauth/wechat/bind", sessionCookieOriginGuard, middleware.CriticalRateLimit(), anonymousRequestBodyLimit, middleware.UserAuth(), controller.WeChatBind)
		apiRouter.GET("/oauth/telegram/login", sessionCookieOriginGuard, middleware.CriticalRateLimit(), controller.TelegramLogin)
		apiRouter.GET("/oauth/telegram/bind", sessionCookieOriginGuard, middleware.CriticalRateLimit(), middleware.UserAuth(), controller.TelegramBind)
		// Standard OAuth providers (GitHub, Discord, OIDC, LinuxDO) - unified route
		apiRouter.GET("/oauth/:provider", sessionCookieOriginGuard, middleware.CriticalRateLimit(), middleware.TryUserAuth(), controller.HandleOAuth)
		apiRouter.GET("/ratio_config", middleware.CriticalRateLimit(), controller.GetRatioConfig)

		apiRouter.POST("/stripe/webhook", anonymousRequestBodyLimit, controller.StripeWebhook)
		apiRouter.POST("/creem/webhook", anonymousRequestBodyLimit, controller.CreemWebhook)
		apiRouter.POST("/waffo/webhook", anonymousRequestBodyLimit, controller.WaffoWebhook)
		apiRouter.POST("/waffo-pancake/webhook", anonymousRequestBodyLimit, controller.WaffoPancakeWebhook)
		apiRouter.POST("/cryptomus/webhook", anonymousRequestBodyLimit, controller.CryptomusWebhook)
		apiRouter.POST("/sfpay/notify", anonymousRequestBodyLimit, controller.AgouNotify)

		// Universal secure verification routes
		apiRouter.POST("/verify", middleware.UserAuth(), middleware.CriticalRateLimit(), controller.UniversalVerify)

		userRoute := apiRouter.Group("/user")
		{
			userRoute.POST("/register", sessionCookieOriginGuard, middleware.CriticalRateLimit(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), controller.Register)
			userRoute.POST("/login", sessionCookieOriginGuard, middleware.CriticalRateLimit(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), controller.Login)
			userRoute.POST("/login/2fa", sessionCookieOriginGuard, middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.Verify2FALogin)
			userRoute.POST("/refresh", sessionCookieOriginGuard, middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.RefreshDashboardSession)
			userRoute.POST("/passkey/login/begin", sessionCookieOriginGuard, middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.PasskeyLoginBegin)
			userRoute.POST("/passkey/login/finish", sessionCookieOriginGuard, middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.PasskeyLoginFinish)
			//userRoute.POST("/tokenlog", middleware.CriticalRateLimit(), controller.TokenLog)
			userRoute.POST("/logout", sessionCookieOriginGuard, middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.Logout)
			userRoute.POST("/epay/notify", anonymousRequestBodyLimit, controller.EpayNotify)
			userRoute.GET("/epay/notify", controller.EpayNotify)
			userRoute.GET("/groups", controller.GetUserGroups)

			selfRoute := userRoute.Group("/")
			selfRoute.Use(middleware.UserAuth())
			{
				selfRoute.GET("/self/groups", controller.GetUserGroups)
				selfRoute.GET("/self", controller.GetSelf)
				selfRoute.GET("/models", controller.GetUserModels)
				selfRoute.PUT("/self", controller.UpdateSelf)
				selfRoute.DELETE("/self", controller.DeleteSelf)
				selfRoute.GET("/token", middleware.UserCriticalRateLimit("access-token"), controller.GenerateAccessToken)
				selfRoute.GET("/passkey", controller.PasskeyStatus)
				selfRoute.POST("/passkey/register/begin", controller.PasskeyRegisterBegin)
				selfRoute.POST("/passkey/register/finish", controller.PasskeyRegisterFinish)
				selfRoute.POST("/passkey/verify/begin", controller.PasskeyVerifyBegin)
				selfRoute.POST("/passkey/verify/finish", controller.PasskeyVerifyFinish)
				selfRoute.DELETE("/passkey", controller.PasskeyDelete)
				selfRoute.GET("/aff", controller.GetAffCode)
				selfRoute.GET("/aff/users", controller.GetMyInvitees)
				selfRoute.GET("/topup/info", controller.GetTopUpInfo)
				selfRoute.GET("/topup/self", controller.GetUserTopUps)
				selfRoute.POST("/topup", middleware.CriticalRateLimit(), controller.TopUp)
				selfRoute.POST("/pay", middleware.CriticalRateLimit(), controller.RequestEpay)
				selfRoute.POST("/amount", controller.RequestAmount)
				selfRoute.POST("/stripe/pay", middleware.CriticalRateLimit(), controller.RequestStripePay)
				selfRoute.POST("/stripe/amount", controller.RequestStripeAmount)
				selfRoute.POST("/creem/pay", middleware.CriticalRateLimit(), controller.RequestCreemPay)
				selfRoute.POST("/waffo/amount", controller.RequestWaffoAmount)
				selfRoute.POST("/waffo/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPay)
				selfRoute.POST("/waffo-pancake/amount", controller.RequestWaffoPancakeAmount)
				selfRoute.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPancakePay)
				selfRoute.POST("/cryptomus/amount", controller.RequestCryptomusAmount)
				selfRoute.POST("/cryptomus/pay", middleware.CriticalRateLimit(), controller.RequestCryptomusPay)
				selfRoute.POST("/sfpay/amount", controller.RequestAgouAmount)
				selfRoute.POST("/sfpay/pay", middleware.CriticalRateLimit(), controller.RequestAgouPay)
				selfRoute.POST("/aff_transfer", middleware.UserCriticalRateLimit("aff-transfer"), controller.TransferAffQuota)
				selfRoute.PUT("/setting", controller.UpdateUserSetting)

				// 2FA routes
				selfRoute.GET("/2fa/status", controller.Get2FAStatus)
				selfRoute.POST("/2fa/setup", controller.Setup2FA)
				selfRoute.POST("/2fa/enable", controller.Enable2FA)
				selfRoute.POST("/2fa/disable", controller.Disable2FA)
				selfRoute.POST("/2fa/backup_codes", controller.RegenerateBackupCodes)

				// Check-in routes
				selfRoute.GET("/checkin", controller.GetCheckinStatus)
				selfRoute.POST("/checkin", middleware.TurnstileCheck(), controller.DoCheckin)

				// Custom OAuth bindings
				selfRoute.GET("/oauth/bindings", controller.GetUserOAuthBindings)
				selfRoute.DELETE("/oauth/bindings/:provider_id", controller.UnbindCustomOAuth)
			}

			adminRoute := userRoute.Group("/")
			adminRoute.Use(middleware.AdminAuth())
			{
				// 只读：有「管理用户」或「调整额度」任一权限即可 —— 给用户充值也得先看得到用户列表
				userReadRoute := adminRoute.Group("")
				userReadRoute.Use(middleware.RequireAdminPerm(model.AdminPermUserManage, model.AdminPermQuotaGrant))
				{
					userReadRoute.GET("/", controller.GetAllUsers)
					userReadRoute.GET("/pinned", controller.GetPinnedUsers)
					userReadRoute.GET("/search", controller.SearchUsers)
					userReadRoute.GET("/:id", controller.GetUser)
					// add_quota 走「调整额度」权限、其余动作走「管理用户」权限，
					// 两者在 controller.ManageUser 内部按 action 分别校验
					userReadRoute.POST("/manage", controller.ManageUser)
				}

				userManageRoute := adminRoute.Group("")
				userManageRoute.Use(middleware.RequireAdminPerm(model.AdminPermUserManage))
				{
					// 手动触发用户 RPM 重算（admin-only，CriticalRateLimit 防刷）
					userManageRoute.POST("/recompute_metrics", middleware.CriticalRateLimit(), controller.RecomputeUserMetrics)
					userManageRoute.GET("/topup", controller.GetAllTopUps)
					userManageRoute.POST("/topup/complete", controller.AdminCompleteTopUp)
					userManageRoute.GET("/:id/oauth/bindings", controller.GetUserOAuthBindingsByAdmin)
					userManageRoute.DELETE("/:id/oauth/bindings/:provider_id", controller.UnbindCustomOAuthByAdmin)
					userManageRoute.DELETE("/:id/bindings/:binding_type", controller.AdminClearUserBinding)
					userManageRoute.POST("/", controller.CreateUser)
					userManageRoute.PUT("/", controller.UpdateUser)
					userManageRoute.DELETE("/:id", controller.DeleteUser)
					userManageRoute.DELETE("/:id/reset_passkey", controller.AdminResetPasskey)

					// Admin 2FA routes
					userManageRoute.GET("/2fa/stats", controller.Admin2FAStats)
					userManageRoute.DELETE("/:id/2fa", controller.AdminDisable2FA)
				}

				// 管理员权限配置只有超级管理员能读写
				userRootRoute := adminRoute.Group("")
				userRootRoute.Use(middleware.RootAuth())
				{
					userRootRoute.PUT("/:id/admin_perms", controller.UpdateUserAdminPerms)
				}
			}
		}

		// Subscription billing (plans, purchase, admin management)
		subscriptionRoute := apiRouter.Group("/subscription")
		subscriptionRoute.Use(middleware.UserAuth())
		{
			subscriptionRoute.GET("/plans", controller.GetSubscriptionPlans)
			subscriptionRoute.GET("/self", controller.GetSubscriptionSelf)
			subscriptionRoute.PUT("/self/preference", controller.UpdateSubscriptionPreference)
			// 用户软删除自己已过期/已取消的订阅（is_hidden=true），不影响限购名额计数。
			subscriptionRoute.DELETE("/self/:id", controller.HideSelfSubscription)
			subscriptionRoute.POST("/epay/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestEpay)
			subscriptionRoute.POST("/stripe/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestStripePay)
			subscriptionRoute.POST("/creem/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestCreemPay)
			subscriptionRoute.POST("/balance/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestBalancePay)
		}
		subscriptionAdminRoute := apiRouter.Group("/subscription/admin")
		subscriptionAdminRoute.Use(middleware.AdminAuth())
		{
			subscriptionAdminRoute.GET("/plans", controller.AdminListSubscriptionPlans)
			subscriptionAdminRoute.POST("/plans", controller.AdminCreateSubscriptionPlan)
			subscriptionAdminRoute.PUT("/plans/:id", controller.AdminUpdateSubscriptionPlan)
			subscriptionAdminRoute.PATCH("/plans/:id", controller.AdminUpdateSubscriptionPlanStatus)
			subscriptionAdminRoute.POST("/bind", controller.AdminBindSubscription)
			subscriptionAdminRoute.POST("/plans/:id/subscriptions/reset", controller.AdminResetPlanSubscriptions)

			// User subscription management (admin)
			subscriptionAdminRoute.GET("/users/:id/subscriptions", controller.AdminListUserSubscriptions)
			subscriptionAdminRoute.POST("/users/:id/subscriptions", controller.AdminCreateUserSubscription)
			subscriptionAdminRoute.POST("/users/:id/subscriptions/reset", controller.AdminResetUserSubscriptionsByPlan)
			subscriptionAdminRoute.POST("/user_subscriptions/:id/invalidate", controller.AdminInvalidateUserSubscription)
			subscriptionAdminRoute.PATCH("/user_subscriptions/:id/expiry", controller.AdminUpdateUserSubscriptionExpiry)
			subscriptionAdminRoute.DELETE("/user_subscriptions/:id", controller.AdminDeleteUserSubscription)

			// All user subscriptions list + group budget (admin dashboard)
			subscriptionAdminRoute.GET("/user_subscriptions", controller.AdminListAllUserSubscriptions)
			subscriptionAdminRoute.GET("/group_budget", controller.AdminGetSubscriptionGroupBudget)
			subscriptionAdminRoute.GET("/bound_groups", controller.AdminListBoundGroups)
		}

		// Subscription payment callbacks (no auth)
		apiRouter.POST("/subscription/epay/notify", anonymousRequestBodyLimit, controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/notify", controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/return", controller.SubscriptionEpayReturn)
		apiRouter.POST("/subscription/epay/return", anonymousRequestBodyLimit, controller.SubscriptionEpayReturn)
		optionRoute := apiRouter.Group("/option")
		optionRoute.Use(middleware.RootAuth())
		{
			optionRoute.GET("/", controller.GetOptions)
			optionRoute.PUT("/", controller.UpdateOption)
			optionRoute.GET("/channel_affinity_cache", controller.GetChannelAffinityCacheStats)
			optionRoute.DELETE("/channel_affinity_cache", controller.ClearChannelAffinityCache)
			optionRoute.POST("/rest_model_ratio", controller.ResetModelRatio)
			optionRoute.POST("/migrate_console_setting", controller.MigrateConsoleSetting) // 用于迁移检测的旧键，下个版本会删除
		}

		// Custom OAuth provider management (root only)
		customOAuthRoute := apiRouter.Group("/custom-oauth-provider")
		customOAuthRoute.Use(middleware.RootAuth())
		{
			customOAuthRoute.POST("/discovery", controller.FetchCustomOAuthDiscovery)
			customOAuthRoute.GET("/", controller.GetCustomOAuthProviders)
			customOAuthRoute.GET("/:id", controller.GetCustomOAuthProvider)
			customOAuthRoute.POST("/", controller.CreateCustomOAuthProvider)
			customOAuthRoute.PUT("/:id", controller.UpdateCustomOAuthProvider)
			customOAuthRoute.DELETE("/:id", controller.DeleteCustomOAuthProvider)
		}
		performanceRoute := apiRouter.Group("/performance")
		performanceRoute.Use(middleware.RootAuth())
		{
			performanceRoute.GET("/stats", controller.GetPerformanceStats)
			performanceRoute.DELETE("/disk_cache", controller.ClearDiskCache)
			performanceRoute.POST("/reset_stats", controller.ResetPerformanceStats)
			performanceRoute.POST("/gc", controller.ForceGC)
			performanceRoute.GET("/logs", controller.GetLogFiles)
			performanceRoute.DELETE("/logs", controller.CleanupLogFiles)
		}
		ratioSyncRoute := apiRouter.Group("/ratio_sync")
		ratioSyncRoute.Use(middleware.RootAuth())
		{
			ratioSyncRoute.GET("/channels", controller.GetSyncableChannels)
			ratioSyncRoute.POST("/fetch", controller.FetchUpstreamRatios)
		}
		// 分站同步（Sub-Site Sync）— 详见 docs/2026-05-27-sub-site-sync-plan.md
		subSiteRoute := apiRouter.Group("/sub_site")
		subSiteRoute.Use(middleware.RootAuth())
		{
			subSiteRoute.GET("/list", controller.ListSubSites)
			subSiteRoute.POST("/upsert", controller.UpsertSubSite)
			subSiteRoute.DELETE("/:id", controller.DeleteSubSite)
			subSiteRoute.POST("/verify", controller.VerifySubSite)
			subSiteRoute.GET("/:id/groups", controller.GetSubSiteGroups)
			subSiteRoute.POST("/:id/create_channels", middleware.CriticalRateLimit(), controller.CreateSubSiteChannels)
		}
		channelRoute := apiRouter.Group("/channel")
		// 进渠道模块：有「查看渠道」或「新建/修改渠道」任一即可（只给 edit 不给 view 时也能用）
		channelRoute.Use(middleware.AdminAuth(), middleware.RequireAdminPerm(model.AdminPermChannelView, model.AdminPermChannelEdit))
		// 会改渠道配置的写操作再单独收一道「新建/修改渠道」（默认关）。
		// 只读 + 诊断类（列表/详情/统计/对账/测试/拉余额/重算/探测更新/外部测评）留在「查看渠道」，
		// 否则只读管理员连排障都做不了。
		channelEditPerm := middleware.RequireAdminPerm(model.AdminPermChannelEdit)
		{
			channelRoute.GET("/", controller.GetAllChannels)
			channelRoute.GET("/search", controller.SearchChannels)
			// 手动触发渠道质量评分重算（admin-only，CriticalRateLimit 防刷）。
			// 正常运行靠后台 5min tick；此接口主要用于调试/调整公式后立即看效果。
			channelRoute.POST("/recompute_metrics", middleware.CriticalRateLimit(), controller.RecomputeChannelMetrics)
			channelRoute.GET("/statistics", controller.GetChannelStatistics)
			channelRoute.GET("/statistics/trend", controller.GetChannelStatisticsTrend)
			channelRoute.GET("/statistics/top_users", controller.GetChannelTopUsers)
			// 对账视图：窗口内各渠道 × 模型的成功/失败/超时/费用精确聚合（上限 24h）。
			channelRoute.GET("/reconcile", controller.GetChannelReconcile)
			// 对账上游账单：从 balance 面板拉各上游账号昨/今实际消费（运行时配置启用）。
			channelRoute.GET("/reconcile/upstream_bill", controller.GetChannelReconcileUpstreamBill)
			// 列表「可用性」列：全渠道近 N 小时按小时桶的成功/失败计数（service 层 5min 缓存）
			channelRoute.GET("/uptime", controller.GetChannelUptime)
			channelRoute.GET("/models", controller.ChannelListModels)
			channelRoute.GET("/models_enabled", controller.EnabledListModels)
			channelRoute.GET("/:id", controller.GetChannel)
			channelRoute.GET("/:id/health/events", controller.GetChannelHealthEvents)
			channelRoute.POST("/:id/health/recover", channelEditPerm, controller.RecoverChannelHealth)
			channelRoute.POST("/:id/key", middleware.RootAuth(), middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.SecurityProofRequired("channel.key.read", []string{"2fa", "passkey"}), controller.GetChannelKey)
			channelRoute.GET("/test", controller.TestAllChannels)
			channelRoute.GET("/test/:id", controller.TestChannel)
			channelRoute.GET("/update_balance", controller.UpdateAllChannelsBalance)
			channelRoute.GET("/update_balance/:id", controller.UpdateChannelBalance)
			channelRoute.POST("/", channelEditPerm, controller.AddChannel)
			channelRoute.PUT("/", channelEditPerm, controller.UpdateChannel)
			channelRoute.DELETE("/disabled", channelEditPerm, controller.DeleteDisabledChannel)
			channelRoute.POST("/tag/disabled", channelEditPerm, controller.DisableTagChannels)
			channelRoute.POST("/tag/enabled", channelEditPerm, controller.EnableTagChannels)
			channelRoute.PUT("/tag", channelEditPerm, controller.EditTagChannels)
			channelRoute.DELETE("/:id", channelEditPerm, controller.DeleteChannel)
			channelRoute.POST("/batch", channelEditPerm, controller.DeleteChannelBatch)
			channelRoute.POST("/fix", channelEditPerm, controller.FixChannelsAbilities)
			channelRoute.GET("/fetch_models/:id", controller.FetchUpstreamModels)
			channelRoute.POST("/fetch_models", middleware.RootAuth(), controller.FetchModels)
			channelRoute.POST("/codex/oauth/start", channelEditPerm, controller.StartCodexOAuth)
			channelRoute.POST("/codex/oauth/complete", channelEditPerm, controller.CompleteCodexOAuth)
			channelRoute.POST("/:id/codex/oauth/start", channelEditPerm, controller.StartCodexOAuthForChannel)
			channelRoute.POST("/:id/codex/oauth/complete", channelEditPerm, controller.CompleteCodexOAuthForChannel)
			channelRoute.POST("/:id/codex/refresh", channelEditPerm, controller.RefreshCodexChannelCredential)
			channelRoute.GET("/:id/codex/usage", controller.GetCodexChannelUsage)
			channelRoute.POST("/ollama/pull", channelEditPerm, controller.OllamaPullModel)
			channelRoute.POST("/ollama/pull/stream", channelEditPerm, controller.OllamaPullModelStream)
			channelRoute.DELETE("/ollama/delete", channelEditPerm, controller.OllamaDeleteModel)
			channelRoute.GET("/ollama/version/:id", controller.OllamaVersion)
			channelRoute.POST("/batch/tag", channelEditPerm, controller.BatchSetChannelTag)
			channelRoute.GET("/tag/models", controller.GetTagModels)
			channelRoute.POST("/copy/:id", channelEditPerm, controller.CopyChannel)
			channelRoute.POST("/multi_key/manage", channelEditPerm, controller.ManageMultiKeys)
			channelRoute.POST("/upstream_updates/apply", channelEditPerm, controller.ApplyChannelUpstreamModelUpdates)
			channelRoute.POST("/upstream_updates/apply_all", channelEditPerm, controller.ApplyAllChannelUpstreamModelUpdates)
			channelRoute.POST("/upstream_updates/detect", controller.DetectChannelUpstreamModelUpdates)
			channelRoute.POST("/upstream_updates/detect_all", controller.DetectAllChannelUpstreamModelUpdates)
			// 外部测评（外部测评网关 /api/verify/claude SSE 透传），管理员可触发
			channelRoute.POST("/:id/verify", controller.VerifyChannel)
			channelRoute.GET("/:id/verify/reports", controller.ListChannelVerifyReports)
			// 质量分历史报表（列表 hover"查看更多"弹窗）
			channelRoute.GET("/:id/quality/history", controller.GetChannelQualityHistory)
			// 上游分组倍率变化监控（docs/2026-08-05-upstream-group-ratio-monitor.md）
			channelRoute.GET("/:id/ratio_changes", controller.GetChannelRatioChanges)
			channelRoute.POST("/ratio_monitor/run", middleware.CriticalRateLimit(), controller.RunChannelRatioMonitorNow)
			channelRoute.GET("/verify/report/:report_id", controller.GetChannelVerifyReport)
			channelRoute.POST("/verify/report/:report_id/cancel", controller.CancelChannelVerifyReport)
			// 软 RPM 限流规则（per user_id × channel_id），admin-only，CRUD
			channelRoute.GET("/:id/user_rpm", controller.ListChannelUserRpmRules)
			channelRoute.POST("/:id/user_rpm", channelEditPerm, controller.CreateChannelUserRpmRule)
			channelRoute.PUT("/:id/user_rpm/:rule_id", channelEditPerm, controller.UpdateChannelUserRpmRule)
			channelRoute.DELETE("/:id/user_rpm/:rule_id", channelEditPerm, controller.DeleteChannelUserRpmRule)
		}
		tokenRoute := apiRouter.Group("/token")
		tokenRoute.Use(middleware.UserAuth())
		{
			tokenRoute.GET("/", controller.GetAllTokens)
			tokenRoute.GET("/search", middleware.SearchRateLimit(), controller.SearchTokens)
			tokenRoute.GET("/:id", controller.GetToken)
			tokenRoute.POST("/:id/key", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKey)
			tokenRoute.POST("/", controller.AddToken)
			tokenRoute.PUT("/", controller.UpdateToken)
			tokenRoute.DELETE("/:id", controller.DeleteToken)
			tokenRoute.POST("/batch", controller.DeleteTokenBatch)
			tokenRoute.POST("/batch/keys", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKeysBatch)
		}

		usageRoute := apiRouter.Group("/usage")
		usageRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
		{
			tokenUsageRoute := usageRoute.Group("/token")
			tokenUsageRoute.Use(middleware.TokenAuthReadOnly())
			{
				tokenUsageRoute.GET("/", controller.GetTokenUsage)
			}
		}

		redemptionRoute := apiRouter.Group("/redemption")
		redemptionRoute.Use(middleware.AdminAuth())
		{
			redemptionRoute.GET("/", controller.GetAllRedemptions)
			redemptionRoute.GET("/search", controller.SearchRedemptions)
			redemptionRoute.GET("/:id", controller.GetRedemption)
			redemptionRoute.POST("/", controller.AddRedemption)
			redemptionRoute.PUT("/", controller.UpdateRedemption)
			redemptionRoute.DELETE("/invalid", controller.DeleteInvalidRedemption)
			redemptionRoute.DELETE("/:id", controller.DeleteRedemption)
		}
		logRoute := apiRouter.Group("/log")
		// 全站日志受「查看日志」权限约束；/self* 是用户看自己的日志，不受影响
		logViewPerm := middleware.RequireAdminPerm(model.AdminPermLogView)
		logRoute.GET("/", middleware.AdminAuth(), logViewPerm, controller.GetAllLogs)
		logRoute.GET("/export", middleware.AdminAuth(), logViewPerm, controller.ExportAllLogs)
		logRoute.DELETE("/", middleware.AdminAuth(), logViewPerm, controller.DeleteHistoryLogs)
		logRoute.GET("/stat", middleware.AdminAuth(), logViewPerm, controller.GetLogsStat)
		logRoute.GET("/self/stat", middleware.UserAuth(), controller.GetLogsSelfStat)
		logRoute.GET("/channel_affinity_usage_cache", middleware.AdminAuth(), logViewPerm, controller.GetChannelAffinityUsageCacheStats)
		logRoute.GET("/search", middleware.AdminAuth(), logViewPerm, controller.SearchAllLogs)
		logRoute.GET("/self", middleware.UserAuth(), controller.GetUserLogs)
		logRoute.GET("/self/export", middleware.UserAuth(), middleware.SearchRateLimit(), controller.ExportUserLogs)
		logRoute.GET("/self/search", middleware.UserAuth(), middleware.SearchRateLimit(), controller.SearchUserLogs)

		anomalyRoute := apiRouter.Group("/anomaly")
		anomalyRoute.GET("/", middleware.AdminAuth(), controller.GetAnomalyLogs)
		anomalyRoute.GET("/stats", middleware.AdminAuth(), controller.GetAnomalyStats)
		anomalyRoute.GET("/summary", middleware.AdminAuth(), controller.GetAnomalySummary)
		anomalyRoute.GET("/export", middleware.AdminAuth(), controller.GetAnomalyExport)

		dataRoute := apiRouter.Group("/data")
		dataRoute.GET("/", middleware.AdminAuth(), controller.GetAllQuotaDates)
		dataRoute.GET("/users", middleware.AdminAuth(), controller.GetQuotaDatesByUser)
		dataRoute.GET("/groups", middleware.AdminAuth(), controller.GetQuotaDatesByGroup)
		dataRoute.GET("/group_users", middleware.AdminAuth(), controller.GetQuotaDatesByGroupMembers)
		dataRoute.GET("/user_groups", middleware.AdminAuth(), controller.GetQuotaDatesByUserGroups)
		dataRoute.GET("/self", middleware.UserAuth(), controller.GetUserQuotaDates)

		// 运行时大盘：5min 窗口 top10（用户/分组/渠道/分组×渠道）。
		// 后台 60s 重算 → Redis；admin 列表读快照（不直接查 logs）。
		dataRoute.GET("/runtime", middleware.AdminAuth(), middleware.RuntimeMetricsRateLimit(), controller.GetRuntimeMetrics)
		dataRoute.POST("/runtime/recompute", middleware.AdminAuth(), middleware.CriticalRateLimit(), controller.RecomputeRuntimeMetrics)

		sensitiveWordRoute := apiRouter.Group("/sensitive_word")
		sensitiveWordRoute.Use(middleware.AdminAuth())
		{
			sensitiveWordRoute.GET("/", controller.GetAllSensitiveWords)
			sensitiveWordRoute.GET("/:id", controller.GetSensitiveWord)
			sensitiveWordRoute.POST("/", controller.AddSensitiveWord)
			sensitiveWordRoute.PUT("/:id", controller.UpdateSensitiveWord)
			sensitiveWordRoute.PUT("/:id/toggle", controller.ToggleSensitiveWord)
			sensitiveWordRoute.DELETE("/:id", controller.DeleteSensitiveWord)
		}

		sensitiveBlockRoute := apiRouter.Group("/sensitive_block")
		sensitiveBlockRoute.Use(middleware.AdminAuth())
		{
			sensitiveBlockRoute.GET("/", controller.GetAllSensitiveBlockLogs)
			sensitiveBlockRoute.GET("/stats", controller.GetSensitiveAuditStats)
			sensitiveBlockRoute.GET("/:id", controller.GetSensitiveBlockLog)
			sensitiveBlockRoute.GET("/:id/body", controller.GetSensitiveBlockBody)
			sensitiveBlockRoute.POST("/:id/toggle_token", controller.ToggleSensitiveBlockToken)
		}

		logRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
		{
			logRoute.GET("/token", middleware.TokenAuthReadOnly(), controller.GetLogByKey)
		}
		groupRoute := apiRouter.Group("/group")
		groupRoute.Use(middleware.AdminAuth())
		{
			groupRoute.GET("/", controller.GetGroups)
		}

		prefillGroupRoute := apiRouter.Group("/prefill_group")
		prefillGroupRoute.Use(middleware.AdminAuth())
		{
			prefillGroupRoute.GET("/", controller.GetPrefillGroups)
			prefillGroupRoute.POST("/", controller.CreatePrefillGroup)
			prefillGroupRoute.PUT("/", controller.UpdatePrefillGroup)
			prefillGroupRoute.DELETE("/:id", controller.DeletePrefillGroup)
		}

		mjRoute := apiRouter.Group("/mj")
		mjRoute.GET("/self", middleware.UserAuth(), controller.GetUserMidjourney)
		mjRoute.GET("/", middleware.AdminAuth(), controller.GetAllMidjourney)

		taskRoute := apiRouter.Group("/task")
		{
			taskRoute.GET("/self", middleware.UserAuth(), controller.GetUserTask)
			taskRoute.GET("/refund_reconciliation", middleware.AdminAuth(), controller.GetAsyncRefundReconciliation)
			taskRoute.GET("/", middleware.AdminAuth(), controller.GetAllTask)
		}

		vendorRoute := apiRouter.Group("/vendors")
		vendorRoute.Use(middleware.AdminAuth())
		{
			vendorRoute.GET("/", controller.GetAllVendors)
			vendorRoute.GET("/search", controller.SearchVendors)
			vendorRoute.GET("/:id", controller.GetVendorMeta)
			vendorRoute.POST("/", controller.CreateVendorMeta)
			vendorRoute.PUT("/", controller.UpdateVendorMeta)
			vendorRoute.DELETE("/:id", controller.DeleteVendorMeta)
		}

		modelsRoute := apiRouter.Group("/models")
		modelsRoute.Use(middleware.AdminAuth())
		{
			modelsRoute.GET("/sync_upstream/preview", controller.SyncUpstreamPreview)
			modelsRoute.POST("/sync_upstream", controller.SyncUpstreamModels)
			modelsRoute.GET("/missing", controller.GetMissingModels)
			modelsRoute.GET("/", controller.GetAllModelsMeta)
			modelsRoute.GET("/search", controller.SearchModelsMeta)
			modelsRoute.GET("/:id", controller.GetModelMeta)
			modelsRoute.POST("/", controller.CreateModelMeta)
			modelsRoute.PUT("/", controller.UpdateModelMeta)
			modelsRoute.DELETE("/:id", controller.DeleteModelMeta)
		}

		// Deployments (model deployment management)
		deploymentsRoute := apiRouter.Group("/deployments")
		deploymentsRoute.Use(middleware.AdminAuth())
		{
			deploymentsRoute.GET("/settings", controller.GetModelDeploymentSettings)
			deploymentsRoute.POST("/settings/test-connection", controller.TestIoNetConnection)
			deploymentsRoute.GET("/", controller.GetAllDeployments)
			deploymentsRoute.GET("/search", controller.SearchDeployments)
			deploymentsRoute.POST("/test-connection", controller.TestIoNetConnection)
			deploymentsRoute.GET("/hardware-types", controller.GetHardwareTypes)
			deploymentsRoute.GET("/locations", controller.GetLocations)
			deploymentsRoute.GET("/available-replicas", controller.GetAvailableReplicas)
			deploymentsRoute.POST("/price-estimation", controller.GetPriceEstimation)
			deploymentsRoute.GET("/check-name", controller.CheckClusterNameAvailability)
			deploymentsRoute.POST("/", controller.CreateDeployment)

			deploymentsRoute.GET("/:id", controller.GetDeployment)
			deploymentsRoute.GET("/:id/logs", controller.GetDeploymentLogs)
			deploymentsRoute.GET("/:id/containers", controller.ListDeploymentContainers)
			deploymentsRoute.GET("/:id/containers/:container_id", controller.GetContainerDetails)
			deploymentsRoute.PUT("/:id", controller.UpdateDeployment)
			deploymentsRoute.PUT("/:id/name", controller.UpdateDeploymentName)
			deploymentsRoute.POST("/:id/extend", controller.ExtendDeployment)
			deploymentsRoute.DELETE("/:id", controller.DeleteDeployment)
		}
	}
}

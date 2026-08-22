package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/pkg/billinglifecycle"
	"github.com/QuantumNous/new-api/pkg/httplifecycle"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/router"
	"github.com/QuantumNous/new-api/service"
	_ "github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	_ "net/http/pprof"
)

//go:embed web/default/dist
var buildFS embed.FS

//go:embed web/default/dist/index.html
var indexPage []byte

//go:embed web/classic/dist
var classicBuildFS embed.FS

//go:embed web/classic/dist/index.html
var classicIndexPage []byte

func sessionCookieOptions() sessions.Options {
	return sessions.Options{
		Path:     "/",
		MaxAge:   2592000, // 30 days
		HttpOnly: true,
		Secure:   common.SessionCookieSecure,
		SameSite: http.SameSiteStrictMode,
	}
}

func main() {
	startTime := time.Now()

	err := InitResources()
	if err != nil {
		common.FatalLog("failed to initialize resources: " + err.Error())
		return
	}
	schemaMigrationOnly, _ := schemaMigrationOnlyEnabled()
	if schemaMigrationOnly {
		common.SysLog("schema migration only completed")
		return
	}

	common.SysLog("New API " + common.Version + " started")

	// 价格变动发布与通知：首启写 baseline 快照 + 断点续发中断的邮件批次（master only）
	service.InitPriceNotify()
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog("running in debug mode")
	}

	if common.RedisEnabled {
		// for compatibility with old versions
		common.MemoryCacheEnabled = true
	} else if err := service.StartNotificationLimitCleanup(); err != nil {
		common.FatalLog("failed to start notification limit cleanup: " + err.Error())
		return
	}
	if common.MemoryCacheEnabled {
		common.SysLog("memory cache enabled")
		common.SysLog(fmt.Sprintf("sync frequency: %d seconds", common.SyncFrequency))

		// Add panic recovery and retry for InitChannelCache
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysLog(fmt.Sprintf("InitChannelCache panic: %v, retrying once", r))
					// Retry once
					_, _, fixErr := model.FixAbility()
					if fixErr != nil {
						common.FatalLog(fmt.Sprintf("InitChannelCache failed: %s", fixErr.Error()))
					}
				}
			}()
			model.InitChannelCache()
		}()

		if err := backgroundtask.Start("channel-cache-sync", func(ctx context.Context) {
			model.SyncChannelCache(ctx, common.SyncFrequency)
		}); err != nil {
			common.FatalLog("failed to start channel cache sync: " + err.Error())
			return
		}
	}

	// 热更新配置
	if err := backgroundtask.Start("option-sync", func(ctx context.Context) {
		model.SyncOptions(ctx, common.SyncFrequency)
	}); err != nil {
		common.FatalLog("failed to start option sync: " + err.Error())
		return
	}

	// 数据看板
	if err := model.StartQuotaDataUpdater(); err != nil {
		common.FatalLog("failed to initialize quota data updater: " + err.Error())
		return
	}

	if os.Getenv("CHANNEL_UPDATE_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_UPDATE_FREQUENCY"))
		if err != nil {
			common.FatalLog("failed to parse CHANNEL_UPDATE_FREQUENCY: " + err.Error())
		}
		if err := backgroundtask.Start("automatic-channel-balance-update", func(ctx context.Context) {
			controller.AutomaticallyUpdateChannels(ctx, frequency)
		}); err != nil {
			common.FatalLog("failed to start automatic channel balance update: " + err.Error())
			return
		}
	}

	if err := controller.AutomaticallyTestChannels(); err != nil {
		common.FatalLog("failed to start automatic channel tests: " + err.Error())
		return
	}

	// Codex credential auto-refresh check every 10 minutes, refresh when expires within 1 day
	if err := service.StartCodexCredentialAutoRefreshTask(); err != nil {
		common.FatalLog("failed to start Codex credential auto-refresh: " + err.Error())
		return
	}

	if os.Getenv("BATCH_UPDATE_ENABLED") == "true" {
		common.BatchUpdateEnabled = true
		common.SysLog("batch update enabled with interval " + strconv.Itoa(common.BatchUpdateInterval) + "s")
		if err := model.InitBatchUpdater(); err != nil {
			common.FatalLog("failed to initialize batch updater: " + err.Error())
			return
		}
	}

	// Subscription quota reset task (daily/weekly/monthly/custom)
	if err := service.StartSubscriptionQuotaResetTask(); err != nil {
		common.FatalLog("failed to start subscription quota reset task: " + err.Error())
		return
	}

	// Affiliate commission daily settlement (00:30 Beijing time)
	if err := service.StartAffiliateCommissionTask(); err != nil {
		common.FatalLog("failed to start affiliate commission task: " + err.Error())
		return
	}

	// 遗留预扣费清扫：退款失败（如数据库瞬时不可用）时把停留在
	// reserved 状态的预扣费退回用户余额。
	if err := service.StartWalletReservationSweeper(); err != nil {
		common.FatalLog("failed to start wallet reservation sweeper: " + err.Error())
		return
	}

	// 渠道质量评分 后台快照（每 5min；启动时立即跑一次）
	// 注意：渠道/用户 RPM 已改为 Redis 实时滑动窗口（最近 1 分钟），不再依赖此后台任务的 rpm_24h 字段。
	// 渠道质量评分仍走此任务（24h 综合分），保留。
	if err := service.StartChannelMetricsTask(); err != nil {
		common.FatalLog("failed to start channel metrics task: " + err.Error())
		return
	}
	if err := service.StartChannelPeakRpmTask(); err != nil {
		common.FatalLog("failed to start channel peak RPM task: " + err.Error())
		return
	}

	// 渠道外部测评（verify）自动定时调度（master only；docs/2026-06-18-channel-verify-auto-schedule-plan.md）
	if err := controller.StartChannelVerifyScheduleTask(); err != nil {
		common.FatalLog("failed to start channel verify schedule: " + err.Error())
		return
	}

	// 用户 RPM 后台快照：废弃，改为 Redis 实时桶。
	// service.StartUserMetricsTask()

	// 运行时大盘快照（每 60s；5min 窗口；写 Redis + 内存兜底）
	if err := service.StartRuntimeMetricsTask(); err != nil {
		common.FatalLog("failed to start runtime metrics task: " + err.Error())
		return
	}

	// Sensitive monitor 异步审计 worker（替换旧版同步阻断）
	if err := service.StartSensitiveAuditWorkers(0); err != nil {
		common.FatalLog(err.Error())
		return
	}
	// Sensitive monitor dump 清理 + 磁盘水位监控（每节点清理自己的本地持久卷）
	service.StartSensitiveDumpCleaner()

	// Wire task polling adaptor factory (breaks service -> relay import cycle)
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		a := relay.GetTaskAdaptor(platform)
		if a == nil {
			return nil
		}
		return a
	}

	// Channel upstream model update check task
	if err := controller.StartChannelUpstreamModelUpdateTask(); err != nil {
		common.FatalLog("failed to start channel upstream model update task: " + err.Error())
		return
	}

	// Upstream group ratio monitor task
	if err := controller.StartChannelRatioMonitorTask(); err != nil {
		common.FatalLog("failed to start channel ratio monitor task: " + err.Error())
		return
	}

	if common.IsMasterNode && constant.UpdateTask {
		if err := billinglifecycle.StartProducer("midjourney-task-polling", func(ctx context.Context, parent *billinglifecycle.Ticket) {
			controller.UpdateMidjourneyTaskBulk(billinglifecycle.ContextWithParent(ctx, parent))
		}); err != nil {
			common.FatalLog("failed to start midjourney task polling: " + err.Error())
			return
		}
		if err := billinglifecycle.StartProducer("task-polling", func(ctx context.Context, parent *billinglifecycle.Ticket) {
			controller.UpdateTaskBulk(billinglifecycle.ContextWithParent(ctx, parent))
		}); err != nil {
			common.FatalLog("failed to start task polling: " + err.Error())
			return
		}
	}

	var pprofServer *http.Server
	if os.Getenv("ENABLE_PPROF") == "true" {
		pprofServer = &http.Server{
			Addr:    "0.0.0.0:8005",
			Handler: http.DefaultServeMux,
		}
		go func() {
			if serveErr := pprofServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				common.SysError("pprof server failed: " + serveErr.Error())
			}
		}()
		if err := backgroundtask.Start("pprof-monitor", common.Monitor); err != nil {
			common.FatalLog("failed to start pprof monitor: " + err.Error())
			return
		}
		common.SysLog("pprof enabled")
	}

	err = common.StartPyroScope()
	if err != nil {
		common.SysError(fmt.Sprintf("start pyroscope error : %v", err))
	}

	// Initialize HTTP server
	server := gin.New()
	server.Use(httplifecycle.Middleware())
	server.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		common.SysLog(fmt.Sprintf("panic detected: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("Panic detected, error: %v. Please submit a issue here: https://github.com/Calcium-Ion/new-api", err),
				"type":    "new_api_panic",
			},
		})
	}))
	// This will cause SSE not to work!!!
	//server.Use(gzip.Gzip(gzip.DefaultCompression))
	server.Use(middleware.RequestId())
	server.Use(middleware.PoweredBy())
	server.Use(middleware.I18n())
	middleware.SetUpLogger(server)
	// Initialize session store
	store := cookie.NewStore([]byte(common.SessionSecret))
	store.Options(sessionCookieOptions())
	server.Use(sessions.Sessions("session", store))

	InjectUmamiAnalytics()
	InjectGoogleAnalytics()

	// 加载敏感词缓存（service 共享缓存：SensitiveCollector / 异步审计 worker / 冷备 SensitiveFilter 共用）
	service.LoadSensitiveWords()

	// 设置路由
	router.SetRouter(server, router.ThemeAssets{
		DefaultBuildFS:   buildFS,
		DefaultIndexPage: indexPage,
		ClassicBuildFS:   classicBuildFS,
		ClassicIndexPage: classicIndexPage,
	})
	var port = os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}

	// Log startup success message
	common.LogStartupSuccess(startTime, port)

	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: server,
	}
	keepAliveDrainCtx, stopKeepAliveDrain := context.WithCancel(context.Background())
	defer stopKeepAliveDrain()
	keepAliveDrainSignals := make(chan os.Signal, 1)
	signal.Notify(keepAliveDrainSignals, syscall.SIGUSR1)
	defer signal.Stop(keepAliveDrainSignals)
	go runKeepAliveDrainSignal(
		keepAliveDrainCtx,
		httpServer,
		keepAliveDrainSignals,
		func() {
			common.SysLog("SIGUSR1 received, disabling HTTP keep-alive for zero-downtime drain")
		},
	)
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	serveResult := make(chan error, 1)
	go func() {
		serveResult <- httpServer.ListenAndServe()
	}()

	var serveFailure error
	select {
	case <-signalCtx.Done():
		common.SysLog("shutdown signal received, draining HTTP admission")
	case serveErr := <-serveResult:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serveFailure = serveErr
			common.SysError("HTTP server stopped unexpectedly: " + serveErr.Error())
		}
	}

	shutdownErr := runGracefulShutdown(
		defaultShutdownConfig(),
		httpServer,
		pprofServer,
		productionShutdownHooks(),
	)
	if shutdownErr != nil {
		common.SysError("graceful shutdown completed with errors: " + shutdownErr.Error())
		common.FatalLog("server shutdown failed: " + errors.Join(shutdownErr, serveFailure).Error())
	}
	common.SysLog("graceful shutdown completed")
	if serveFailure != nil {
		common.FatalLog("failed to serve HTTP: " + serveFailure.Error())
	}
}

func InjectUmamiAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("UMAMI_WEBSITE_ID") != "" {
		umamiSiteID := os.Getenv("UMAMI_WEBSITE_ID")
		umamiScriptURL := os.Getenv("UMAMI_SCRIPT_URL")
		if umamiScriptURL == "" {
			umamiScriptURL = "https://analytics.umami.is/script.js"
		}
		analyticsInjectBuilder.WriteString("<script defer src=\"")
		analyticsInjectBuilder.WriteString(umamiScriptURL)
		analyticsInjectBuilder.WriteString("\" data-website-id=\"")
		analyticsInjectBuilder.WriteString(umamiSiteID)
		analyticsInjectBuilder.WriteString("\"></script>")
	}
	analyticsInjectBuilder.WriteString("<!--Umami QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--umami-->\n")
	indexPage = bytes.ReplaceAll(indexPage, placeholder, analyticsInject)
	classicIndexPage = bytes.ReplaceAll(classicIndexPage, placeholder, analyticsInject)
}

func InjectGoogleAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("GOOGLE_ANALYTICS_ID") != "" {
		gaID := os.Getenv("GOOGLE_ANALYTICS_ID")
		// Google Analytics 4 (gtag.js)
		analyticsInjectBuilder.WriteString("<script async src=\"https://www.googletagmanager.com/gtag/js?id=")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("\"></script>")
		analyticsInjectBuilder.WriteString("<script>")
		analyticsInjectBuilder.WriteString("window.dataLayer = window.dataLayer || [];")
		analyticsInjectBuilder.WriteString("function gtag(){dataLayer.push(arguments);}")
		analyticsInjectBuilder.WriteString("gtag('js', new Date());")
		analyticsInjectBuilder.WriteString("gtag('config', '")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("');")
		analyticsInjectBuilder.WriteString("</script>")
	}
	analyticsInjectBuilder.WriteString("<!--Google Analytics QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--Google Analytics-->\n")
	indexPage = bytes.ReplaceAll(indexPage, placeholder, analyticsInject)
	classicIndexPage = bytes.ReplaceAll(classicIndexPage, placeholder, analyticsInject)
}

func InitResources() error {
	// Initialize resources here if needed
	// This is a placeholder function for future resource initialization
	err := godotenv.Load(".env")
	if err != nil {
		if common.DebugEnabled {
			common.SysLog("No .env file found, using default environment variables. If needed, please create a .env file and set the relevant variables.")
		}
	}

	// 加载环境变量
	common.InitEnv()
	schemaMigrationOnly, err := schemaMigrationOnlyEnabled()
	if err != nil {
		return err
	}

	logger.SetupLogger()

	// Initialize model settings
	ratio_setting.InitRatioSettings()

	service.InitHttpClient()

	service.InitTokenEncoders()

	// Initialize SQL Database
	err = model.InitDB()
	if err != nil {
		common.FatalLog("failed to initialize database: " + err.Error())
		return err
	}
	if schemaMigrationOnly {
		return nil
	}

	model.CheckSetup()

	// Initialize options, should after model.InitDB()
	model.InitOptionMap()

	// 清理旧的磁盘缓存文件
	common.CleanupOldCacheFiles()

	// 初始化模型
	model.GetPricing()

	// Initialize SQL Database
	err = model.InitLogDB()
	if err != nil {
		return err
	}

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		return err
	}

	if err := perfmetrics.Init(); err != nil {
		return err
	}

	// 启动系统监控
	if err := common.StartSystemMonitor(); err != nil {
		return err
	}

	// Initialize i18n
	err = i18n.Init()
	if err != nil {
		common.SysError("failed to initialize i18n: " + err.Error())
		// Don't return error, i18n is not critical
	} else {
		common.SysLog("i18n initialized with languages: " + strings.Join(i18n.SupportedLanguages(), ", "))
	}
	// Register user language loader for lazy loading
	i18n.SetUserLangLoader(model.GetUserLanguage)

	// Load custom OAuth providers from database
	err = oauth.LoadCustomProviders()
	if err != nil {
		common.SysError("failed to load custom OAuth providers: " + err.Error())
		// Don't return error, custom OAuth is not critical
	}

	return nil
}

func schemaMigrationOnlyEnabled() (bool, error) {
	switch strings.TrimSpace(os.Getenv("SCHEMA_MIGRATION_ONLY")) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, errors.New("SCHEMA_MIGRATION_ONLY must be true or false")
	}
}

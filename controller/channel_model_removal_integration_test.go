package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 端到端覆盖「触发 → 移出 → 复核 → 加回」整条装配链路。判定零件的单测在
// channel_model_missing_removal_test.go；这里验证零件真的被接上了。
func setupModelRemovalTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false // 跳过全量缓存重建，测试只关心落库结果

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelHealthEvent{}))

	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// upstreamModelsServer 假上游：/v1/models 返回给定模型列表；models 为 nil 时返回 500（模拟拉不到）。
func upstreamModelsServer(t *testing.T, models []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if models == nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		items := make([]string, 0, len(models))
		for _, m := range models {
			items = append(items, fmt.Sprintf(`{"id":%q,"object":"model"}`, m))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"object":"list","data":[%s]}`, strings.Join(items, ","))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func seedRemovalTestChannel(t *testing.T, db *gorm.DB, baseURL string, models []string) *model.Channel {
	t.Helper()
	ch := &model.Channel{
		Type:    1, // OpenAI：fetch 走默认 {base}/v1/models 分支
		Name:    "removal-test",
		Key:     "sk-test",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
		Models:  strings.Join(models, ","),
		Group:   "default",
	}
	require.NoError(t, db.Create(ch).Error)
	return ch
}

func reloadChannel(t *testing.T, db *gorm.DB, id int) *model.Channel {
	t.Helper()
	var ch model.Channel
	require.NoError(t, db.First(&ch, "id = ?", id).Error)
	return &ch
}

func rateLimitError() *types.NewAPIError {
	return types.NewErrorWithStatusCode(fmt.Errorf("rate limit exceeded"), types.ErrorCodeBadResponseStatusCode, 429)
}

func TestRunModelRemoval_RateLimitRemovesModelStillListedUpstream(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	// 关键前提：上游仍列出该模型（限流不等于下线）。缺失路径会因此放弃，限流路径必须照摘。
	srv := upstreamModelsServer(t, []string{"gpt-5", "gpt-4"})
	ch := seedRemovalTestChannel(t, db, srv.URL, []string{"gpt-5", "gpt-4"})

	runModelMissingRemoval(context.Background(), ch.Id, ch.Name, "gpt-5",
		types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name}, rateLimitError(), modelRemovalReasonRateLimit)

	got := reloadChannel(t, db, ch.Id)
	require.Equal(t, "gpt-4", got.Models, "限流模型应被摘除")

	settings := got.GetOtherSettings()
	require.Equal(t, []string{"gpt-5"}, settings.ModelMissingRemovedModels, "应进入待复核清单")
	require.Greater(t, settings.ModelMissingRecheckAt, int64(0), "应排定复核时间")
}

func TestRunModelRemoval_MissingPathStillSkipsWhenModelListed(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	srv := upstreamModelsServer(t, []string{"gpt-5", "gpt-4"})
	ch := seedRemovalTestChannel(t, db, srv.URL, []string{"gpt-5", "gpt-4"})

	missingErr := types.NewErrorWithStatusCode(fmt.Errorf("model does not exist"), types.ErrorCodeBadResponseStatusCode, 404)
	runModelMissingRemoval(context.Background(), ch.Id, ch.Name, "gpt-5",
		types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name}, missingErr, modelRemovalReasonMissing)

	got := reloadChannel(t, db, ch.Id)
	require.Equal(t, "gpt-5,gpt-4", got.Models, "缺失路径遇到模型仍在上游必须不摘（改造前行为不得回归）")
}

func TestRunModelRemoval_RateLimitSkipsWhenUpstreamListUnreachable(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	// 拉不到模型列表 = 复核链路不通，摘了就永远加不回来，必须不摘
	srv := upstreamModelsServer(t, nil)
	ch := seedRemovalTestChannel(t, db, srv.URL, []string{"gpt-5", "gpt-4"})

	runModelMissingRemoval(context.Background(), ch.Id, ch.Name, "gpt-5",
		types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name}, rateLimitError(), modelRemovalReasonRateLimit)

	got := reloadChannel(t, db, ch.Id)
	require.Equal(t, "gpt-5,gpt-4", got.Models, "上游列表拉不到时不得移出")
	require.Empty(t, got.GetOtherSettings().ModelMissingRemovedModels)
}

func TestRunModelRemoval_RateLimitRespectsPerChannelCap(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	all := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7"}
	srv := upstreamModelsServer(t, all)
	ch := seedRemovalTestChannel(t, db, srv.URL, all)

	capValue := modelRemovalMaxRemovedPerChannel()
	// 整渠道限流：每个模型都 429。安全阀应在第 5 个之后停手，不把渠道掏空。
	for _, m := range all {
		runModelMissingRemoval(context.Background(), ch.Id, ch.Name, m,
			types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name}, rateLimitError(), modelRemovalReasonRateLimit)
	}

	got := reloadChannel(t, db, ch.Id)
	removed := got.GetOtherSettings().ModelMissingRemovedModels
	require.Len(t, removed, capValue, "限流移出数应被上限截断")
	require.Len(t, strings.Split(got.Models, ","), len(all)-capValue)
}

// ─────────── 权限 / 不支持路径（任务 B）───────────

// forbiddenError 403 权限拒绝。文案「Permission denied」同时也在默认停渠道关键词表里，
// 用它可以验证「摘模型优先于停渠道」以及触顶回退后原判定照旧生效。
func forbiddenError() *types.NewAPIError {
	return types.NewErrorWithStatusCode(fmt.Errorf("Permission denied for this deployment"), types.ErrorCodeBadResponseStatusCode, 403)
}

// unsupportedModelError 400 模型不支持。文案只在权限关键词表里、**不在**默认停渠道关键词表里，
// 因此渠道最终是否被停用完全由新增的触顶升级决定——用它才能把新行为与既有关键词判定隔离开。
func unsupportedModelError() *types.NewAPIError {
	return types.NewErrorWithStatusCode(fmt.Errorf("model not supported in this region"), types.ErrorCodeBadResponseStatusCode, 400)
}

// enableForbiddenRemoval 打开权限摘除开关，并在测试结束后还原。
func enableForbiddenRemoval(t *testing.T) {
	t.Helper()
	origin := operation_setting.ModelForbiddenRemovalEnabled
	t.Cleanup(func() { operation_setting.ModelForbiddenRemovalEnabled = origin })
	operation_setting.ModelForbiddenRemovalEnabled = true
}

// setRemovalCap 覆盖上限与触顶动作，并在测试结束后还原。
func setRemovalCap(t *testing.T, capValue int, action string) {
	t.Helper()
	originCap := operation_setting.ModelRemovalMaxRemovedPerChannel
	originAction := operation_setting.ModelRemovalCapAction
	originAutoDisable := common.AutomaticDisableChannelEnabled
	t.Cleanup(func() {
		operation_setting.ModelRemovalMaxRemovedPerChannel = originCap
		operation_setting.ModelRemovalCapAction = originAction
		common.AutomaticDisableChannelEnabled = originAutoDisable
	})
	operation_setting.ModelRemovalMaxRemovedPerChannel = capValue
	operation_setting.ModelRemovalCapAction = action
}

// 任务 B 的核心：403 发生时模型仍在上游清单里，权限路径必须照摘。
// 若误用了缺失路径的存在性核实，这里会因「模型仍在上游」被否决，模型永远摘不掉。
func TestRunModelRemoval_ForbiddenRemovesModelStillListedUpstream(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	srv := upstreamModelsServer(t, []string{"gpt-5", "gpt-4"})
	ch := seedRemovalTestChannel(t, db, srv.URL, []string{"gpt-5", "gpt-4"})

	runModelMissingRemoval(context.Background(), ch.Id, ch.Name, "gpt-5",
		types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name, AutoBan: true}, forbiddenError(), modelRemovalReasonForbidden)

	got := reloadChannel(t, db, ch.Id)
	require.Equal(t, "gpt-4", got.Models, "权限拒绝的模型应被摘除（不得因仍在上游清单被否决）")
	require.Equal(t, common.ChannelStatusEnabled, got.Status, "渠道其余模型正常，整渠道不得被停用")

	settings := got.GetOtherSettings()
	require.Equal(t, []string{"gpt-5"}, settings.ModelMissingRemovedModels, "应进入待复核清单")
	require.Greater(t, settings.ModelMissingRecheckAt, int64(0), "应排定复核时间")
}

// 「拉不到上游列表就不摘」这道前置在权限路径同样必须保留：
// 复核加回依赖同一个 /v1/models 接口，拉不到 = 摘得掉加不回来（一路顺延到 24h 上限）。
func TestRunModelRemoval_ForbiddenSkipsWhenUpstreamListUnreachable(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	srv := upstreamModelsServer(t, nil)
	ch := seedRemovalTestChannel(t, db, srv.URL, []string{"gpt-5", "gpt-4"})

	runModelMissingRemoval(context.Background(), ch.Id, ch.Name, "gpt-5",
		types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name}, forbiddenError(), modelRemovalReasonForbidden)

	got := reloadChannel(t, db, ch.Id)
	require.Equal(t, "gpt-5,gpt-4", got.Models, "上游列表拉不到时不得移出")
	require.Empty(t, got.GetOtherSettings().ModelMissingRemovedModels)
}

// 装配级：验证权限判定真的被接进 relay 报错路径的接管决策，而不只是单测里能跑通。
// processChannelError 的逻辑是 `handled := tryHandle(...); if !handled && ShouldDisableChannel(err) {停渠道}`，
// 所以 tryHandle 的返回值就是「摘模型」与「停渠道」的分水岭。
// 这里靠预热冷却窗口让 tryHandle 在判定后同步返回，不落异步任务，避免测试竞态。
func TestTryHandleChannelModelMissing_ForbiddenTakeoverSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originAutoDisable := common.AutomaticDisableChannelEnabled
	originForbidden := operation_setting.ModelForbiddenRemovalEnabled
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = originAutoDisable
		operation_setting.ModelForbiddenRemovalEnabled = originForbidden
	})
	common.AutomaticDisableChannelEnabled = true

	newCtx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("original_model", "gpt-5")
		return c
	}
	channelError := types.ChannelError{ChannelId: 987654, ChannelName: "forbidden-takeover", AutoBan: true}
	err := forbiddenError()

	// 前置事实：默认的停渠道关键词表本来就会把这个 403 判成「停整渠道」。
	// 这正是任务 B 要降烈度的对象，也是「开关关 = 零行为变更」的参照。
	require.True(t, service.ShouldDisableChannel(err), "403 权限拒绝原本命中停渠道判定")

	t.Run("开关关闭时不接管，403 继续走原有停渠道判定", func(t *testing.T) {
		operation_setting.ModelForbiddenRemovalEnabled = false
		require.False(t, tryHandleChannelModelMissing(newCtx(), channelError, err))
	})

	t.Run("开关开启时接管，短路整渠道禁用", func(t *testing.T) {
		operation_setting.ModelForbiddenRemovalEnabled = true
		// 预热冷却窗口：下一次调用在判定命中后走冷却分支同步返回 true，不提交异步任务
		require.True(t, modelMissingRemovalCooldownOk(channelError.ChannelId, "gpt-5"))
		require.True(t, tryHandleChannelModelMissing(newCtx(), channelError, err))
	})
}

// ─────────── 摘除累计触顶升级（任务 A）───────────

// runForbiddenRemovalOverCap 用 400「模型不支持」连打 4 个模型，上限设 2 → 必然触顶两次。
// 刻意选不在停渠道关键词表里的文案：渠道最终状态只由触顶动作决定。
func runForbiddenRemovalOverCap(t *testing.T, db *gorm.DB) *model.Channel {
	t.Helper()
	all := []string{"m1", "m2", "m3", "m4"}
	srv := upstreamModelsServer(t, all)
	ch := seedRemovalTestChannel(t, db, srv.URL, all)

	err := unsupportedModelError()
	require.False(t, service.ShouldDisableChannel(err),
		"前置事实：该文案不在停渠道关键词表里，渠道状态变化只能来自触顶升级")

	for _, m := range all {
		runModelMissingRemoval(context.Background(), ch.Id, ch.Name, m,
			types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name, AutoBan: true}, err, modelRemovalReasonForbidden)
	}
	return reloadChannel(t, db, ch.Id)
}

// 触顶 + alert_only（默认）：渠道不得被停用。这是「默认值 = 上线前行为」的守门测试。
func TestRunModelRemoval_CapAlertOnlyKeepsChannelEnabled(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	enableForbiddenRemoval(t)
	setRemovalCap(t, 2, operation_setting.ModelRemovalCapActionAlertOnly)
	common.AutomaticDisableChannelEnabled = true

	got := runForbiddenRemovalOverCap(t, db)
	require.Len(t, got.GetOtherSettings().ModelMissingRemovedModels, 2, "摘除数应被上限截断")
	require.Equal(t, common.ChannelStatusEnabled, got.Status, "alert_only 下触顶不得停用渠道")
}

// 触顶 + disable_channel：渠道被停用，且落库 reason 能看出是「摘除累计触顶升级」而非普通报错禁用。
func TestRunModelRemoval_CapDisableChannelEscalates(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	enableForbiddenRemoval(t)
	setRemovalCap(t, 2, operation_setting.ModelRemovalCapActionDisableChannel)
	common.AutomaticDisableChannelEnabled = true

	got := runForbiddenRemovalOverCap(t, db)
	require.Equal(t, common.ChannelStatusAutoDisabled, got.Status, "触顶应升级为停用整渠道")

	statusReason, _ := got.GetOtherInfo()["status_reason"].(string)
	require.Contains(t, statusReason, "摘除累计触顶升级", "停用原因须体现是升级而非普通错误禁用")
	require.Contains(t, statusReason, string(modelRemovalReasonForbidden), "停用原因须带上触发路径")
}

// disable_channel 但全局「自动禁用渠道」总开关关闭：不得越过总开关停渠道。
func TestRunModelRemoval_CapEscalationRespectsGlobalDisableSwitch(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	enableForbiddenRemoval(t)
	setRemovalCap(t, 2, operation_setting.ModelRemovalCapActionDisableChannel)
	common.AutomaticDisableChannelEnabled = false

	got := runForbiddenRemovalOverCap(t, db)
	require.Equal(t, common.ChannelStatusEnabled, got.Status, "总开关关闭时不得自动停渠道")
}

// alert_only 必须把处置原样交回改造前的判定链：触顶后错误文案若本就命中停渠道关键词表
// （默认的 Permission denied 就是），渠道照旧被停用。这一条锁住「默认 = 上线前行为」的另一半：
// alert_only 不是「什么都不做」，而是「回退到改造前那套判定」。
func TestRunModelRemoval_CapAlertOnlyStillFallsBackToKeywordDisable(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	enableForbiddenRemoval(t)
	setRemovalCap(t, 2, operation_setting.ModelRemovalCapActionAlertOnly)
	common.AutomaticDisableChannelEnabled = true

	all := []string{"m1", "m2", "m3", "m4"}
	srv := upstreamModelsServer(t, all)
	ch := seedRemovalTestChannel(t, db, srv.URL, all)

	err := forbiddenError()
	require.True(t, service.ShouldDisableChannel(err), "前置事实：该 403 文案本就命中停渠道关键词表")

	for _, m := range all {
		runModelMissingRemoval(context.Background(), ch.Id, ch.Name, m,
			types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name, AutoBan: true}, err, modelRemovalReasonForbidden)
	}

	got := reloadChannel(t, db, ch.Id)
	require.Len(t, got.GetOtherSettings().ModelMissingRemovedModels, 2, "摘除数仍应被上限截断")
	require.Equal(t, common.ChannelStatusAutoDisabled, got.Status, "触顶回退后原关键词判定照旧生效")

	statusReason, _ := got.GetOtherInfo()["status_reason"].(string)
	require.NotContains(t, statusReason, "摘除累计触顶升级", "alert_only 不得写入升级原因")
}

// 缺失路径默认不受上限约束（= 改造前行为）：上游批量下线模型是正常场景。
func TestRunModelRemoval_MissingPathNotCappedByDefault(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	setRemovalCap(t, 2, operation_setting.ModelRemovalCapActionDisableChannel)
	common.AutomaticDisableChannelEnabled = true
	originCapCountsMissing := operation_setting.ModelMissingRemovalCapEnabled
	t.Cleanup(func() { operation_setting.ModelMissingRemovalCapEnabled = originCapCountsMissing })
	operation_setting.ModelMissingRemovalCapEnabled = false

	local := []string{"gone1", "gone2", "gone3", "gone4", "kept"}
	// 上游只剩 kept：gone* 全部确实下线，缺失路径的核实会一路放行
	srv := upstreamModelsServer(t, []string{"kept"})
	ch := seedRemovalTestChannel(t, db, srv.URL, local)

	for _, m := range []string{"gone1", "gone2", "gone3", "gone4"} {
		runModelMissingRemoval(context.Background(), ch.Id, ch.Name, m,
			types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name, AutoBan: true},
			types.NewErrorWithStatusCode(fmt.Errorf("model does not exist"), types.ErrorCodeBadResponseStatusCode, 404),
			modelRemovalReasonMissing)
	}

	got := reloadChannel(t, db, ch.Id)
	require.Len(t, got.GetOtherSettings().ModelMissingRemovedModels, 4, "缺失路径默认不受上限约束")
	require.Equal(t, common.ChannelStatusEnabled, got.Status, "缺失路径默认也不触发触顶升级")
}

// 显式开启后缺失路径纳入上限，并按触顶动作处置。
func TestRunModelRemoval_MissingPathCappedWhenOptedIn(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	setRemovalCap(t, 2, operation_setting.ModelRemovalCapActionDisableChannel)
	common.AutomaticDisableChannelEnabled = true
	originCapCountsMissing := operation_setting.ModelMissingRemovalCapEnabled
	t.Cleanup(func() { operation_setting.ModelMissingRemovalCapEnabled = originCapCountsMissing })
	operation_setting.ModelMissingRemovalCapEnabled = true

	local := []string{"gone1", "gone2", "gone3", "gone4", "kept"}
	srv := upstreamModelsServer(t, []string{"kept"})
	ch := seedRemovalTestChannel(t, db, srv.URL, local)

	for _, m := range []string{"gone1", "gone2", "gone3", "gone4"} {
		runModelMissingRemoval(context.Background(), ch.Id, ch.Name, m,
			types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name, AutoBan: true},
			types.NewErrorWithStatusCode(fmt.Errorf("model does not exist"), types.ErrorCodeBadResponseStatusCode, 404),
			modelRemovalReasonMissing)
	}

	got := reloadChannel(t, db, ch.Id)
	require.Len(t, got.GetOtherSettings().ModelMissingRemovedModels, 2, "开启后缺失路径受上限约束")
	require.Equal(t, common.ChannelStatusAutoDisabled, got.Status, "开启后缺失路径触顶也升级停渠道")
}

func TestRunModelRemoval_RateLimitRemovedModelIsRestoredOnRecheck(t *testing.T) {
	db := setupModelRemovalTestDB(t)
	srv := upstreamModelsServer(t, []string{"gpt-5", "gpt-4"})
	ch := seedRemovalTestChannel(t, db, srv.URL, []string{"gpt-5", "gpt-4"})

	runModelMissingRemoval(context.Background(), ch.Id, ch.Name, "gpt-5",
		types.ChannelError{ChannelId: ch.Id, ChannelName: ch.Name}, rateLimitError(), modelRemovalReasonRateLimit)
	require.Equal(t, "gpt-4", reloadChannel(t, db, ch.Id).Models)

	// 复核：模型仍在上游 → 加回。这就是用户要的「限流期间摘掉、恢复后自动放回」闭环。
	recheckChannelModelMissingModels(context.Background(), reloadChannel(t, db, ch.Id))

	got := reloadChannel(t, db, ch.Id)
	require.ElementsMatch(t, []string{"gpt-4", "gpt-5"}, strings.Split(got.Models, ","), "复核应把限流模型加回")
	settings := got.GetOtherSettings()
	require.Empty(t, settings.ModelMissingRemovedModels, "加回后追踪清单应清空")
	require.Equal(t, int64(0), settings.ModelMissingRecheckAt, "无待复核项时应停止追踪")
}

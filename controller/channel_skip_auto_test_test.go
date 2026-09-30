package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 渠道 setting.skip_auto_test=true（「不参与定时测试和可用性测试」）时，三条系统自动测试
// （全量定时测试、恢复探活、降级探测）都不能向该渠道上游发请求；同批未勾选的渠道照常被测，
// 证明循环确实跑到了。单渠道手动测试不受影响。假上游按 Authorization 里的 key 区分渠道计数。
const (
	skipAutoTestExemptKey = "sk-skip-auto-test-exempt"
	skipAutoTestNormalKey = "sk-skip-auto-test-normal"
)

func TestSkipAutoTest_ExcludedFromAutomaticChannelTests(t *testing.T) {
	t.Run("recovery probe", func(t *testing.T) {
		db := setupSkipAutoTestEnv(t)
		upstream := newCountingUpstream(t)
		seedSkipAutoTestChannels(t, db, upstream.url, common.ChannelStatusAutoDisabled, 0)

		require.NoError(t, recoverAutoDisabledChannels())

		requireOnlyNormalChannelTested(t, upstream)
	})

	t.Run("degrade probe", func(t *testing.T) {
		db := setupSkipAutoTestEnv(t)
		upstream := newCountingUpstream(t)
		seedSkipAutoTestChannels(t, db, upstream.url, common.ChannelStatusEnabled, 2)

		probeDegradedChannels()

		requireOnlyNormalChannelTested(t, upstream)
	})

	t.Run("test all channels", func(t *testing.T) {
		db := setupSkipAutoTestEnv(t)
		upstream := newCountingUpstream(t)
		seedSkipAutoTestChannels(t, db, upstream.url, common.ChannelStatusEnabled, 0)

		require.NoError(t, testAllChannels(false))
		require.Eventually(t, func() bool {
			testAllChannelsLock.Lock()
			defer testAllChannelsLock.Unlock()
			return !testAllChannelsRunning
		}, 10*time.Second, 10*time.Millisecond, "全量测试后台任务未在超时内结束")

		requireOnlyNormalChannelTested(t, upstream)
	})

	// 对照组：同一个勾选了开关的渠道，手动单测照常打到上游，说明上面的 0 次是开关生效而不是渠道本身测不通。
	t.Run("manual single channel test unaffected", func(t *testing.T) {
		db := setupSkipAutoTestEnv(t)
		upstream := newCountingUpstream(t)
		seedSkipAutoTestChannels(t, db, upstream.url, common.ChannelStatusEnabled, 0)

		exempt := &model.Channel{}
		require.NoError(t, db.Where("name = ?", skipAutoTestExemptKey).First(exempt).Error)
		require.True(t, exempt.GetSetting().SkipAutoTest)

		result := testChannel(exempt, "", "", false)

		require.NoError(t, result.localErr)
		require.Equal(t, 1, upstream.count(skipAutoTestExemptKey))
	})
}

func requireOnlyNormalChannelTested(t *testing.T, upstream *countingUpstream) {
	t.Helper()
	require.GreaterOrEqual(t, upstream.count(skipAutoTestNormalKey), 1, "未勾选的渠道应被自动测试打到上游，否则循环没跑到，用例不成立")
	require.Zero(t, upstream.count(skipAutoTestExemptKey), "勾选「不参与定时测试和可用性测试」的渠道不应收到任何自动测试请求")
}

// countingUpstream 假 OpenAI 上游：按 Bearer key 计数，任何请求都回一个合法的 chat.completion。
type countingUpstream struct {
	url    string
	mu     sync.Mutex
	counts map[string]int
}

func (u *countingUpstream) count(key string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.counts[key]
}

func newCountingUpstream(t *testing.T) *countingUpstream {
	t.Helper()
	upstream := &countingUpstream{counts: make(map[string]int)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		upstream.mu.Lock()
		upstream.counts[key]++
		upstream.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-skip-auto-test","object":"chat.completion","created":1700000000,"model":"gpt-4o-mini",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	upstream.url = srv.URL
	return upstream
}

// seedSkipAutoTestChannels 建一对同状态的 OpenAI 渠道，都指向假上游：一个勾选 skip_auto_test，一个不勾。
func seedSkipAutoTestChannels(t *testing.T, db *gorm.DB, baseURL string, status int, degradeLevel int) {
	t.Helper()
	for _, spec := range []struct {
		key     string
		setting *string
	}{
		{key: skipAutoTestExemptKey, setting: common.GetPointer(`{"skip_auto_test":true}`)},
		{key: skipAutoTestNormalKey},
	} {
		autoBan := 1
		level := degradeLevel
		url := baseURL
		require.NoError(t, db.Create(&model.Channel{
			Type:         constant.ChannelTypeOpenAI,
			Name:         spec.key,
			Key:          spec.key,
			Status:       status,
			BaseURL:      &url,
			Models:       "gpt-4o-mini",
			Group:        "default",
			AutoBan:      &autoBan,
			DegradeLevel: &level,
			Setting:      spec.setting,
		}).Error)
	}
}

// setupSkipAutoTestEnv 准备 testChannel 真实发出上游请求所需的最小环境（独立内存 SQLite、1 号用户、
// 测试模型倍率、HTTP 客户端），并在用例结束时还原所有改动过的全局状态。
func setupSkipAutoTestEnv(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)

	prevDB, prevLogDB := model.DB, model.LOG_DB
	prevSQLite, prevMySQL, prevPostgreSQL := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
	prevRedis, prevMemoryCache := common.RedisEnabled, common.MemoryCacheEnabled
	prevInterval := common.RequestInterval
	health := operation_setting.GetChannelHealthConfig()
	prevHealth := *health
	prevModelRatio := ratio_setting.ModelRatio2JSONString()

	common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = true, false, false
	common.RedisEnabled = false       // 用户缓存、错误日志的用户设置都直接读库
	common.MemoryCacheEnabled = false // 恢复探活启用渠道直接落库，不依赖渠道缓存
	common.RequestInterval = 0        // 循环里每个渠道之间不 sleep
	model.InitColumnRefs()            // GetUserGroup 用 group 列引用，未初始化会拼出空列名
	service.InitHttpClient()

	// 降级探测只看 DegradeProbeEnabled；被动总开关保持关闭，探测结果不写健康度状态
	health.Enabled = false
	health.DegradeProbeEnabled = true
	health.DegradeProbeMinLevel = 1
	health.DegradeProbeCount = 1

	// 测试请求发往上游前要过 ModelPriceHelper，模型没配倍率会提前失败
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o-mini":0.075}`))

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}, &model.ChannelHealthEvent{}))
	model.DB, model.LOG_DB = db, db

	// testChannel 以 1 号用户身份构造测试请求
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "skip-auto-test-root",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)

	t.Cleanup(func() {
		model.DB, model.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = prevSQLite, prevMySQL, prevPostgreSQL
		common.RedisEnabled, common.MemoryCacheEnabled = prevRedis, prevMemoryCache
		common.RequestInterval = prevInterval
		*health = prevHealth
		_ = ratio_setting.UpdateModelRatioByJSONString(prevModelRatio)
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

package model

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupOptionBroadcastTest(t *testing.T) {
	t.Helper()
	redisURL := strings.TrimSpace(os.Getenv("OPTION_REDIS_TEST_URL"))
	if redisURL == "" {
		t.Skip("OPTION_REDIS_TEST_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	client := redis.NewClient(options)
	require.NoError(t, client.Ping(context.Background()).Err())

	previousDB := DB
	previousRedisClient := common.RDB
	previousRedisEnabled := common.RedisEnabled

	path := filepath.Join(t.TempDir(), "option-broadcast.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)

	DB = db
	common.RDB = client
	common.RedisEnabled = true

	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = map[string]string{"Notice": "old"}
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		require.NoError(t, client.Close())
		require.NoError(t, sqlDB.Close())
		DB = previousDB
		common.RDB = previousRedisClient
		common.RedisEnabled = previousRedisEnabled
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})
}

func readOptionFromMap(key string) string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return common.OptionMap[key]
}

// TestSubscribeOptionUpdatesReloadsOnBroadcast 复现多节点场景：
// 节点 A 写库并广播后，未处理该请求的节点 B 应立即重载，而不是等 SyncFrequency 轮询。
func TestSubscribeOptionUpdatesReloadsOnBroadcast(t *testing.T) {
	setupOptionBroadcastTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subscribed := make(chan struct{})
	go func() {
		// 订阅建立需要一次网络往返，先确认频道已就绪再发布，避免消息发早了丢失。
		sub := common.RDB.Subscribe(ctx, optionUpdateChannel)
		_, err := sub.Receive(ctx)
		require.NoError(t, err)
		require.NoError(t, sub.Close())
		close(subscribed)
	}()
	<-subscribed

	go SubscribeOptionUpdates(ctx)
	require.Eventually(t, func() bool {
		n, err := common.RDB.PubSubNumSub(ctx, optionUpdateChannel).Result()
		return err == nil && n[optionUpdateChannel] >= 1
	}, 5*time.Second, 20*time.Millisecond, "订阅未建立")

	// 模拟节点 A：写库成功，但本节点内存未被更新（跨节点时正是如此）。
	require.NoError(t, DB.Save(&Option{Key: "Notice", Value: "new"}).Error)
	require.Equal(t, "old", readOptionFromMap("Notice"), "广播前本地内存应仍是旧值")

	broadcastOptionUpdate()

	require.Eventually(t, func() bool {
		return readOptionFromMap("Notice") == "new"
	}, 5*time.Second, 20*time.Millisecond, "收到广播后应立即重载出新值")
}

// TestUpdateOptionBroadcasts 确认写入口真的接上了广播。
// 上一个测试直接调 broadcastOptionUpdate，即使 UpdateOption 忘了调用也会通过。
func TestUpdateOptionBroadcasts(t *testing.T) {
	setupOptionBroadcastTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := common.RDB.Subscribe(ctx, optionUpdateChannel)
	defer sub.Close()
	_, err := sub.Receive(ctx)
	require.NoError(t, err)

	require.NoError(t, UpdateOption("Notice", "broadcasted"))

	select {
	case msg := <-sub.Channel():
		require.Equal(t, optionUpdateChannel, msg.Channel)
	case <-time.After(5 * time.Second):
		t.Fatal("UpdateOption 未发出广播")
	}
}

// TestSubscribeOptionUpdatesStopsOnContextCancel 确认 graceful shutdown 时订阅能退出，不泄漏 goroutine。
func TestSubscribeOptionUpdatesStopsOnContextCancel(t *testing.T) {
	setupOptionBroadcastTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		SubscribeOptionUpdates(ctx)
		close(done)
	}()

	require.Eventually(t, func() bool {
		n, err := common.RDB.PubSubNumSub(ctx, optionUpdateChannel).Result()
		return err == nil && n[optionUpdateChannel] >= 1
	}, 5*time.Second, 20*time.Millisecond, "订阅未建立")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("context 取消后订阅未退出")
	}
}

// TestOptionBroadcastNoopWithoutRedis 单机部署（未配 Redis）时不得 panic，行为退回定时轮询。
func TestOptionBroadcastNoopWithoutRedis(t *testing.T) {
	previousRedisClient := common.RDB
	previousRedisEnabled := common.RedisEnabled
	t.Cleanup(func() {
		common.RDB = previousRedisClient
		common.RedisEnabled = previousRedisEnabled
	})

	common.RedisEnabled = false
	common.RDB = nil

	broadcastOptionUpdate()

	done := make(chan struct{})
	go func() {
		SubscribeOptionUpdates(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("未启用 Redis 时订阅应立即返回")
	}
}

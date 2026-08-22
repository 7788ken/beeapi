package model

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
)

var errRejectRedisCommand = errors.New("test redis command rejected")

type recordingRejectRedisHook struct {
	mu       sync.Mutex
	commands []string
	changed  chan struct{}
}

func (hook *recordingRejectRedisHook) record(commands ...redis.Cmder) {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	for _, command := range commands {
		hook.commands = append(hook.commands, strings.ToLower(command.Name()))
	}
	select {
	case hook.changed <- struct{}{}:
	default:
	}
}

func (hook *recordingRejectRedisHook) BeforeProcess(
	ctx context.Context,
	command redis.Cmder,
) (context.Context, error) {
	hook.record(command)
	return ctx, errRejectRedisCommand
}

func (*recordingRejectRedisHook) AfterProcess(context.Context, redis.Cmder) error {
	return nil
}

func (hook *recordingRejectRedisHook) BeforeProcessPipeline(
	ctx context.Context,
	commands []redis.Cmder,
) (context.Context, error) {
	hook.record(commands...)
	return ctx, errRejectRedisCommand
}

func (*recordingRejectRedisHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func (hook *recordingRejectRedisHook) saw(command string) bool {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	command = strings.ToLower(command)
	for _, recorded := range hook.commands {
		if recorded == command {
			return true
		}
	}
	return false
}

func (hook *recordingRejectRedisHook) count(command string) int {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	command = strings.ToLower(command)
	count := 0
	for _, recorded := range hook.commands {
		if recorded == command {
			count++
		}
	}
	return count
}

func installRecordingRejectRedis(t *testing.T) *recordingRejectRedisHook {
	t.Helper()
	previousRDB := common.RDB
	previousEnabled := common.RedisEnabled
	previousCommonKeyCol := commonKeyCol
	client := redis.NewClient(&redis.Options{
		Addr:       "127.0.0.1:1",
		MaxRetries: -1,
	})
	hook := &recordingRejectRedisHook{changed: make(chan struct{}, 1)}
	client.AddHook(hook)
	common.RDB = client
	common.RedisEnabled = true
	commonKeyCol = "`key`"
	t.Cleanup(func() {
		common.RDB = previousRDB
		common.RedisEnabled = previousEnabled
		commonKeyCol = previousCommonKeyCol
		_ = client.Close()
	})
	return hook
}

func waitForDeletedCacheBarrier(t *testing.T, hook *recordingRejectRedisHook) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		if hook.saw("hset") {
			t.Fatal("stale DB result attempted to refill Redis after durable delete")
		}
		if hook.count("del") >= 2 {
			return
		}
		select {
		case <-hook.changed:
		case <-ctx.Done():
			t.Fatalf(
				"cache barrier did not finish: del=%d hset=%t: %v",
				hook.count("del"),
				hook.saw("hset"),
				ctx.Err(),
			)
		}
	}
}

func TestDeletedUserLedgerBlocksStaleDBReadFromRefillingCache(t *testing.T) {
	truncateTables(t)
	user := seedBatchDeleteTestUser(t, "stale-cache")
	hook := installRecordingRejectRedis(t)
	callbackName := "test:delete-user-after-db-read"
	var deleteStarted atomic.Bool
	var deleteErr error
	if err := DB.Callback().Query().
		After("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table != "users" || !deleteStarted.CompareAndSwap(false, true) {
				return
			}
			deleteErr = DeleteUserById(user.Id)
		}); err != nil {
		t.Fatalf("register user DB-read delete barrier: %v", err)
	}
	t.Cleanup(func() {
		_ = DB.Callback().Query().Remove(callbackName)
	})

	stale, err := GetUserCache(user.Id)
	if err != nil {
		t.Fatalf("GetUserCache across delete barrier: %v", err)
	}
	if stale == nil || stale.Id != user.Id {
		t.Fatalf("GetUserCache stale DB result = %#v, want user %d", stale, user.Id)
	}
	if deleteErr != nil {
		t.Fatalf("delete user after DB read: %v", deleteErr)
	}
	waitForDeletedCacheBarrier(t, hook)
}

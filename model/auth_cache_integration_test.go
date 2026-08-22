package model

import (
	"context"
	"errors"
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

func setupAuthCacheIntegrationTest(t *testing.T) *redis.Client {
	t.Helper()
	redisURL := strings.TrimSpace(os.Getenv("AUTH_REDIS_TEST_URL"))
	if redisURL == "" {
		t.Skip("AUTH_REDIS_TEST_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	client := redis.NewClient(options)
	require.NoError(t, client.Ping(context.Background()).Err())

	previousDB := DB
	previousRedisClient := common.RDB
	previousRedisEnabled := common.RedisEnabled
	previousSyncFrequency := common.SyncFrequency
	previousSessionSecret := common.SessionSecret

	path := filepath.Join(t.TempDir(), "auth-cache.db")
	db, err := gorm.Open(
		sqlite.Open(path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&User{},
		&UserSession{},
		&BatchUpdateDeleteLedger{},
	))
	sqlDB, err := db.DB()
	require.NoError(t, err)

	DB = db
	common.RDB = client
	common.RedisEnabled = true
	common.SyncFrequency = 1
	common.SessionSecret = "auth-cache-test-session-secret"
	require.NoError(t, client.FlushDB(context.Background()).Err())

	t.Cleanup(func() {
		require.NoError(t, client.FlushDB(context.Background()).Err())
		require.NoError(t, client.Close())
		require.NoError(t, sqlDB.Close())
		DB = previousDB
		common.RDB = previousRedisClient
		common.RedisEnabled = previousRedisEnabled
		common.SyncFrequency = previousSyncFrequency
		common.SessionSecret = previousSessionSecret
	})
	return client
}

func createAuthCacheTestUser(t *testing.T, suffix string) *User {
	t.Helper()
	user := &User{
		Username:    "auth-cache-" + suffix,
		Password:    "password-hash",
		AffCode:     "auth-cache-" + suffix,
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		Group:       "default",
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func createAuthCacheTestSession(
	t *testing.T,
	userID int,
	sid string,
) *UserSession {
	t.Helper()
	now := time.Now().Unix()
	session := &UserSession{
		SID:             sid,
		UserID:          userID,
		Version:         1,
		UserAuthVersion: 1,
		Status:          UserSessionStatusActive,
		RefreshHash:     strings.Repeat("a", 64),
		LoginMethod:     "test",
		CreatedAt:       now,
		LastActiveAt:    now,
		ExpiresAt:       now + 3600,
	}
	require.NoError(t, CreateUserSession(session))
	return session
}

func TestUserAuthCacheFenceRejectsDelayedStaleSnapshot(t *testing.T) {
	client := setupAuthCacheIntegrationTest(t)
	user := createAuthCacheTestUser(t, "user-fence")
	require.NoError(t, updateUserCache(*user))

	cached, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	require.Equal(t, int64(1), cached.AuthVersion)

	require.NoError(t, SetUserAuthVersionFence(user.Id, 2))
	_, err = cacheGetUserBase(user.Id)
	require.ErrorIs(t, err, ErrUserAuthCachePending)
	_, err = GetUserCache(user.Id)
	require.ErrorIs(t, err, ErrUserAuthCachePending)
	require.ErrorIs(t, updateUserCache(*user), ErrUserAuthCachePending)

	require.NoError(t, DB.Model(&User{}).
		Where("id = ? AND auth_version = ?", user.Id, 1).
		Updates(map[string]interface{}{
			"auth_version": 2,
			"status":       common.UserStatusDisabled,
		}).Error)
	require.NoError(t, PublishUserAuthCache(user.Id))

	cached, err = cacheGetUserBase(user.Id)
	require.NoError(t, err)
	require.Equal(t, int64(2), cached.AuthVersion)
	require.Equal(t, common.UserStatusDisabled, cached.Status)
	publicCache, err := GetUserCache(user.Id)
	require.NoError(t, err)
	require.Equal(t, int64(2), publicCache.AuthVersion)
	require.Equal(t, common.UserStatusDisabled, publicCache.Status)
	require.ErrorIs(t, updateUserCache(*user), ErrUserAuthCachePending)
	require.NoError(t, updateUserStatusCache(user.Id, true))
	cached, err = cacheGetUserBase(user.Id)
	require.NoError(t, err)
	require.Equal(t, common.UserStatusDisabled, cached.Status)

	orphanFenceUserID := user.Id + 10_000
	require.NoError(t, client.Set(
		context.Background(),
		getUserAuthFenceKey(orphanFenceUserID),
		3,
		0,
	).Err())
	require.NoError(t, SetUserAuthVersionFence(orphanFenceUserID, 2))
	ttl, err := client.TTL(
		context.Background(),
		getUserAuthFenceKey(orphanFenceUserID),
	).Result()
	require.NoError(t, err)
	require.Positive(t, ttl)
}

func TestUserSessionTombstoneRejectsDelayedActiveSnapshot(t *testing.T) {
	setupAuthCacheIntegrationTest(t)
	user := createAuthCacheTestUser(t, "session-tombstone")
	session := createAuthCacheTestSession(t, user.Id, "session-tombstone")
	staleEntry := session.cacheEntry()
	staleDeadline := userSessionCacheDeadline()

	revoked, err := RevokeUserSession(user.Id, session.SID, "test_revoke")
	require.NoError(t, err)
	require.True(t, revoked)
	require.ErrorIs(
		t,
		writeUserSessionCache(staleEntry, staleDeadline),
		ErrUserSessionInactive,
	)

	_, err = GetUserSessionCached(session.SID)
	require.ErrorIs(t, err, ErrUserSessionInactive)

	require.NoError(t, DB.Model(&UserSession{}).
		Where("sid = ?", session.SID).
		Updates(map[string]interface{}{
			"status":         UserSessionStatusActive,
			"revoked_at":     0,
			"revoked_reason": "",
		}).Error)
	_, err = GetUserSessionCached(session.SID)
	require.ErrorIs(t, err, ErrUserSessionInactive)
}

func TestExpiredTombstoneCannotReceiveFreshStaleObservationTTL(t *testing.T) {
	client := setupAuthCacheIntegrationTest(t)
	user := createAuthCacheTestUser(t, "observation-window")
	session := createAuthCacheTestSession(t, user.Id, "observation-window")
	staleEntry := session.cacheEntry()
	staleDeadline := userSessionCacheDeadline()

	revoked, err := RevokeUserSession(user.Id, session.SID, "test_revoke")
	require.NoError(t, err)
	require.True(t, revoked)

	require.Eventually(t, func() bool {
		exists, existsErr := client.Exists(
			context.Background(),
			userSessionCacheKey(session.SID),
		).Result()
		return existsErr == nil && exists == 0
	}, 3*time.Second, 20*time.Millisecond)

	require.ErrorIs(
		t,
		writeUserSessionCache(staleEntry, staleDeadline),
		errUserSessionCacheObservationStale,
	)
}

func TestRevokeByRefreshHashAndBulkRevokePublishTombstones(t *testing.T) {
	setupAuthCacheIntegrationTest(t)
	user := createAuthCacheTestUser(t, "bulk-revoke")
	current := createAuthCacheTestSession(t, user.Id, "bulk-current")
	refreshLogout := createAuthCacheTestSession(t, user.Id, "refresh-logout")
	other := createAuthCacheTestSession(t, user.Id, "bulk-other")

	revoked, err := RevokeUserSessionByRefreshHash(
		refreshLogout.SID,
		refreshLogout.RefreshHash,
		"logout",
	)
	require.NoError(t, err)
	require.True(t, revoked)
	_, err = GetUserSessionCached(refreshLogout.SID)
	require.ErrorIs(t, err, ErrUserSessionInactive)

	affected, err := RevokeOtherUserSessions(
		user.Id,
		current.SID,
		"security_change",
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)
	_, err = GetUserSessionCached(other.SID)
	require.ErrorIs(t, err, ErrUserSessionInactive)
	active, err := GetUserSessionCached(current.SID)
	require.NoError(t, err)
	require.Equal(t, current.SID, active.SID)

	invalid, err := RevokeUserSessionByRefreshHash(
		current.SID,
		strings.Repeat("b", 64),
		"logout",
	)
	require.NoError(t, err)
	require.False(t, invalid)
	active, err = GetUserSessionCached(current.SID)
	require.NoError(t, err)
	require.Equal(t, current.SID, active.SID)
}

func TestGetUserSessionCachedPreservesDatabaseErrors(t *testing.T) {
	client := setupAuthCacheIntegrationTest(t)
	user := createAuthCacheTestUser(t, "database-error")
	session := createAuthCacheTestSession(t, user.Id, "database-error")
	require.NoError(t, client.Del(
		context.Background(),
		userSessionCacheKey(session.SID),
	).Err())

	temporaryErr := errors.New("temporary database failure")
	require.NoError(t, DB.Callback().Query().
		Before("gorm:query").
		Register("auth_cache_temporary_failure", func(tx *gorm.DB) {
			tx.AddError(temporaryErr)
		}))

	_, err := GetUserSessionCached(session.SID)
	require.ErrorIs(t, err, temporaryErr)
}

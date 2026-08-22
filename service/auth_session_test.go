package service

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAuthSessionTest(t *testing.T) (*gorm.DB, *model.User) {
	t.Helper()
	previousDB := model.DB
	previousSecret := common.SessionSecret
	previousRedisEnabled := common.RedisEnabled
	common.SessionSecret = "test-session-secret-with-sufficient-entropy"
	common.RedisEnabled = false

	path := filepath.Join(t.TempDir(), "auth-session.db")
	db, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(5000)"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	model.DB = db

	user := &model.User{
		Username:    "auth-session-user",
		Password:    "password-hash",
		AffCode:     "auth-session-user",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SessionSecret = previousSecret
		common.RedisEnabled = previousRedisEnabled
		require.NoError(t, sqlDB.Close())
	})
	return db, user
}

func TestLoginSessionCreateRefreshAndConcurrentRecovery(t *testing.T) {
	db, user := setupAuthSessionTest(t)
	bundle, err := CreateLoginSession(
		user.Id,
		"password",
		"127.0.0.1",
		"test-agent",
	)
	require.NoError(t, err)
	require.NotEmpty(t, bundle.RefreshToken)
	require.Equal(t, "Bearer", bundle.TokenType)
	require.Equal(t, bundle.Session.SID, strings.Split(bundle.RefreshToken, ".")[0])

	identity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	require.Equal(t, user.Id, identity.UserID)
	require.Equal(t, bundle.Session.SID, identity.SessionID)

	var stored model.UserSession
	require.NoError(t, db.First(&stored, "sid = ?", bundle.Session.SID).Error)
	require.Len(t, stored.RefreshHash, 64)
	require.NotContains(t, bundle.RefreshToken, stored.RefreshHash)
	require.NotContains(t, stored.RefreshHash, strings.Split(bundle.RefreshToken, ".")[1])

	refreshed, err := RefreshLoginSession(bundle.RefreshToken)
	require.NoError(t, err)
	require.NotEqual(t, bundle.RefreshToken, refreshed.RefreshToken)

	recovered, err := RefreshLoginSession(bundle.RefreshToken)
	require.NoError(t, err)
	require.Equal(t, refreshed.RefreshToken, recovered.RefreshToken)
	require.NotEqual(t, refreshed.AccessToken, recovered.AccessToken)
}

func TestValidateLoginSessionRejectsOldUserAuthVersion(t *testing.T) {
	_, user := setupAuthSessionTest(t)
	bundle, err := CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)
	identity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)

	session, cachedUser, err := ValidateLoginSession(identity)
	require.NoError(t, err)
	require.Equal(t, bundle.Session.SID, session.SID)
	require.Equal(t, int64(1), cachedUser.AuthVersion)

	next, err := model.BumpUserAuthVersion(user.Id)
	require.NoError(t, err)
	require.Equal(t, int64(2), next)
	_, _, err = ValidateLoginSession(identity)
	require.ErrorIs(t, err, ErrLoginSessionRevoked)
}

func TestLoginSessionRejectsExpiredAccountAtCreateValidateAndRefresh(t *testing.T) {
	db, user := setupAuthSessionTest(t)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).
		Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)
	_, err := CreateLoginSession(user.Id, "password", "", "")
	require.ErrorIs(t, err, ErrLoginSessionInvalid)

	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).
		Update("expires_at", int64(0)).Error)
	bundle, err := CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)
	identity, err := ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)

	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).
		Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)
	_, _, err = ValidateLoginSession(identity)
	require.ErrorIs(t, err, ErrLoginSessionRevoked)
	_, err = RefreshLoginSession(bundle.RefreshToken)
	require.ErrorIs(t, err, ErrLoginSessionRevoked)
}

func TestRefreshUnknownSecretDoesNotRevokeSession(t *testing.T) {
	db, user := setupAuthSessionTest(t)
	bundle, err := CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)

	_, err = RefreshLoginSession(bundle.Session.SID + "." + strings.Repeat("x", 64))
	require.ErrorIs(t, err, ErrRefreshTokenInvalid)

	var stored model.UserSession
	require.NoError(t, db.First(&stored, "sid = ?", bundle.Session.SID).Error)
	require.Equal(t, model.UserSessionStatusActive, stored.Status)
	require.Zero(t, stored.RevokedAt)
}

func TestRefreshPreservesTemporaryDatabaseErrors(t *testing.T) {
	db, user := setupAuthSessionTest(t)
	bundle, err := CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)

	temporaryErr := errors.New("temporary database failure")
	require.NoError(t, db.Callback().Query().Before("gorm:query").
		Register("auth_session_temporary_failure", func(tx *gorm.DB) {
			tx.AddError(temporaryErr)
		}))

	_, err = RefreshLoginSession(bundle.RefreshToken)
	require.ErrorIs(t, err, temporaryErr)
	require.NotErrorIs(t, err, ErrRefreshTokenInvalid)
}

func TestRefreshKnownPreviousSecretAfterGraceRevokesSession(t *testing.T) {
	db, user := setupAuthSessionTest(t)
	bundle, err := CreateLoginSession(user.Id, "password", "", "")
	require.NoError(t, err)
	refreshed, err := RefreshLoginSession(bundle.RefreshToken)
	require.NoError(t, err)

	_, oldSecret, ok := splitRefreshToken(bundle.RefreshToken)
	require.True(t, ok)
	_, err = model.RotateUserSessionRefresh(
		user.Id,
		bundle.Session.SID,
		hashRefreshSecret(oldSecret),
		hashRefreshSecret("unused-next-secret"),
		time.Now().Add(RefreshReplayWindow+time.Second).Unix(),
		RefreshReplayWindow,
	)
	require.ErrorIs(t, err, model.ErrUserSessionRefreshReuse)

	_, err = RefreshLoginSession(refreshed.RefreshToken)
	require.ErrorIs(t, err, ErrLoginSessionRevoked)
	var stored model.UserSession
	require.NoError(t, db.First(&stored, "sid = ?", bundle.Session.SID).Error)
	require.Equal(t, model.UserSessionStatusRevoked, stored.Status)
	require.Equal(t, "refresh_reuse", stored.RevokedReason)
}

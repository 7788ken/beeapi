package service

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAuthFlowTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared&_pragma=busy_timeout(5000)"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.AuthFlow{},
		&model.ExternalIdentityClaim{},
	))
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		sqlDB, closeErr := db.DB()
		require.NoError(t, closeErr)
		require.NoError(t, sqlDB.Close())
	})
	useAuthTestSessionSecret(t)
}

func TestAuthFlowPersistsOpaquePayloadAndConsumesOnce(t *testing.T) {
	setupAuthFlowTestDB(t)
	type payload struct {
		Invitation string `json:"invitation"`
	}
	token, created, err := CreateAuthFlow(AuthFlowSpec{
		Purpose: AuthFlowPurposeRegistration,
		UserID:  17,
		Payload: payload{Invitation: "AFF123"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.NotEqual(t, token, created.TokenHash)
	require.NotContains(t, created.Payload, token)

	var got payload
	consumed, err := ConsumeAuthFlow(token, AuthFlowPurposeRegistration, time.Now(), &got)
	require.NoError(t, err)
	require.NotNil(t, consumed.ConsumedAt)
	require.Equal(t, "AFF123", got.Invitation)

	_, err = ConsumeAuthFlow(token, AuthFlowPurposeRegistration, time.Now(), &got)
	require.ErrorIs(t, err, ErrAuthFlowConsumed)
}

func TestAuthFlowExpiryPurposeAndDatabaseErrorsAreDeterministic(t *testing.T) {
	setupAuthFlowTestDB(t)
	token, _, err := CreateAuthFlow(AuthFlowSpec{
		Purpose: AuthFlowPurposeTwoFA,
		UserID:  9,
		TTL:     time.Second,
	})
	require.NoError(t, err)

	_, err = ConsumeAuthFlow(token, AuthFlowPurposeOAuth, time.Now(), nil)
	require.ErrorIs(t, err, ErrAuthFlowInvalid)
	_, err = ConsumeAuthFlow(token, AuthFlowPurposeTwoFA, time.Now().Add(2*time.Second), nil)
	require.ErrorIs(t, err, ErrAuthFlowExpired)

	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	_, err = ConsumeAuthFlow(token, AuthFlowPurposeTwoFA, time.Now(), nil)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrAuthFlowInvalid))
	require.False(t, errors.Is(err, ErrAuthFlowExpired))
	require.False(t, errors.Is(err, ErrAuthFlowConsumed))
}

func TestAuthFlowConcurrentConsumeHasOneWinner(t *testing.T) {
	setupAuthFlowTestDB(t)
	token, _, err := CreateAuthFlow(AuthFlowSpec{
		Purpose: AuthFlowPurposePasskey,
		UserID:  23,
	})
	require.NoError(t, err)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, consumeErr := ConsumeAuthFlow(token, AuthFlowPurposePasskey, time.Now(), nil)
			errs <- consumeErr
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	winners, consumed := 0, 0
	for consumeErr := range errs {
		if consumeErr == nil {
			winners++
		} else if errors.Is(consumeErr, ErrAuthFlowConsumed) {
			consumed++
		} else {
			require.NoError(t, consumeErr)
		}
	}
	require.Equal(t, 1, winners)
	require.Equal(t, 1, consumed)
}

func TestPasskeyRegistrationFlowExactBindingExpiryAndConcurrentConsume(t *testing.T) {
	setupAuthFlowTestDB(t)
	token, flow, err := CreateAuthFlow(AuthFlowSpec{
		Purpose:  AuthFlowPurposeRegistration,
		Provider: "passkey",
		Intent:   "register",
		UserID:   41,
		Payload:  map[string]string{"challenge": "opaque-challenge"},
		TTL:      time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, 41, flow.UserID)
	require.Equal(t, "passkey", flow.Provider)
	require.Equal(t, "register", flow.Intent)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			consumed, consumeErr := ConsumeBoundAuthFlow(
				token,
				AuthFlowPurposeRegistration,
				"passkey",
				"register",
				41,
				time.Now(),
				nil,
			)
			if consumeErr == nil {
				require.Equal(t, 41, consumed.UserID)
				require.Equal(t, "passkey", consumed.Provider)
				require.Equal(t, "register", consumed.Intent)
			}
			errs <- consumeErr
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	winners := 0
	for consumeErr := range errs {
		if consumeErr == nil {
			winners++
			continue
		}
		require.ErrorIs(t, consumeErr, ErrAuthFlowConsumed)
	}
	require.Equal(t, 1, winners)

	expired, _, err := CreateAuthFlow(AuthFlowSpec{
		Purpose:  AuthFlowPurposeRegistration,
		Provider: "passkey",
		Intent:   "register",
		UserID:   41,
		TTL:      time.Second,
	})
	require.NoError(t, err)
	_, err = ConsumeBoundAuthFlow(
		expired,
		AuthFlowPurposeRegistration,
		"passkey",
		"register",
		41,
		time.Now().Add(2*time.Second),
		nil,
	)
	require.ErrorIs(t, err, ErrAuthFlowExpired)
}

func TestBoundRegistrationFlowRejectsWrongTargetWithoutConsuming(t *testing.T) {
	setupAuthFlowTestDB(t)
	token, _, err := CreateAuthFlow(AuthFlowSpec{
		Purpose:  AuthFlowPurposeRegistration,
		Provider: "passkey",
		Intent:   "register",
		UserID:   41,
	})
	require.NoError(t, err)

	_, err = ConsumeBoundAuthFlow(
		token,
		AuthFlowPurposeRegistration,
		"passkey",
		"register",
		42,
		time.Now(),
		nil,
	)
	require.ErrorIs(t, err, ErrAuthFlowInvalid)

	_, err = ConsumeBoundAuthFlow(
		token,
		AuthFlowPurposeRegistration,
		"passkey",
		"register",
		41,
		time.Now(),
		nil,
	)
	require.NoError(t, err)
}

func TestRegistrationFlowCreateDatabaseErrorIsNotClassifiedAsInvalid(t *testing.T) {
	setupAuthFlowTestDB(t)
	dbErr := errors.New("registration flow database unavailable")
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").
		Register("test:auth-flow-create-error", func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "auth_flows" {
				tx.AddError(dbErr)
			}
		}))
	t.Cleanup(func() {
		model.DB.Callback().Create().Remove("test:auth-flow-create-error")
	})

	_, _, err := CreateAuthFlow(AuthFlowSpec{
		Purpose:  AuthFlowPurposeRegistration,
		Provider: "password",
		Intent:   "register",
	})
	require.ErrorIs(t, err, dbErr)
	require.False(t, errors.Is(err, ErrAuthFlowInvalid))
}

func TestExternalIdentityClaimConflictsAndIdempotency(t *testing.T) {
	setupAuthFlowTestDB(t)
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentity(tx, "telegram", "subject-1", 1)
	}))
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentity(tx, "telegram", "subject-1", 1)
	}))
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentity(tx, "telegram", "subject-1", 2)
	})
	require.ErrorIs(t, err, ErrIdentityConflict)
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentity(tx, "telegram", "subject-2", 1)
	})
	require.ErrorIs(t, err, ErrIdentityConflict)
}

func TestExternalIdentityClaimConflictRollsBackCallerMutation(t *testing.T) {
	setupAuthFlowTestDB(t)
	first := &model.User{Username: "first", Status: 1, AffCode: "aff-first"}
	second := &model.User{Username: "second", Status: 1, AffCode: "aff-second"}
	require.NoError(t, model.DB.Create(first).Error)
	require.NoError(t, model.DB.Create(second).Error)
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error {
		return ClaimExternalIdentity(tx, "wechat", "openid-1", first.Id)
	}))

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", second.Id).
			Update("wechat_id", "openid-1").Error; err != nil {
			return err
		}
		return ClaimExternalIdentity(tx, "wechat", "openid-1", second.Id)
	})
	require.ErrorIs(t, err, ErrIdentityConflict)

	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, second.Id).Error)
	require.Empty(t, reloaded.WeChatId)
}

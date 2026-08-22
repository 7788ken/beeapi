package service

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AuthFlowPurposeLogin        = "login"
	AuthFlowPurposeTwoFA        = "login_2fa"
	AuthFlowPurposePasskey      = "login_passkey"
	AuthFlowPurposeOAuth        = "login_oauth"
	AuthFlowPurposeRegistration = "registration"

	AuthFlowTTL = 10 * time.Minute
)

var (
	ErrAuthFlowInvalid  = errors.New("auth flow is invalid")
	ErrAuthFlowExpired  = errors.New("auth flow is expired")
	ErrAuthFlowConsumed = errors.New("auth flow is already consumed")
	ErrIdentityConflict = errors.New("external identity belongs to another user")
)

type AuthFlowSpec struct {
	Purpose   string
	Provider  string
	Intent    string
	UserID    int
	SessionID string
	Payload   any
	TTL       time.Duration
}

func CreateAuthFlow(spec AuthFlowSpec) (string, *model.AuthFlow, error) {
	return CreateAuthFlowWithTx(model.DB, spec)
}

func CreateAuthFlowWithTx(tx *gorm.DB, spec AuthFlowSpec) (string, *model.AuthFlow, error) {
	if tx == nil {
		return "", nil, ErrAuthFlowInvalid
	}
	purpose := strings.TrimSpace(spec.Purpose)
	if purpose == "" {
		return "", nil, ErrAuthFlowInvalid
	}
	ttl := spec.TTL
	if ttl == 0 {
		ttl = AuthFlowTTL
	}
	if ttl < 0 {
		return "", nil, ErrAuthFlowInvalid
	}
	payload := ""
	if spec.Payload != nil {
		encoded, err := common.Marshal(spec.Payload)
		if err != nil {
			return "", nil, err
		}
		payload = string(encoded)
	}
	token, err := common.GenerateRandomCharsKey(64)
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UTC()
	flow := &model.AuthFlow{
		TokenHash: hashAuthFlowToken(token),
		Purpose:   purpose,
		Provider:  strings.TrimSpace(spec.Provider),
		Intent:    strings.TrimSpace(spec.Intent),
		UserID:    spec.UserID,
		SessionID: strings.TrimSpace(spec.SessionID),
		Payload:   payload,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := tx.Create(flow).Error; err != nil {
		return "", nil, err
	}
	return token, flow, nil
}

func ConsumeAuthFlow(rawToken, purpose string, now time.Time, payload any) (*model.AuthFlow, error) {
	var consumed *model.AuthFlow
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var consumeErr error
		consumed, consumeErr = ConsumeAuthFlowWithTx(tx, rawToken, purpose, now, payload)
		return consumeErr
	})
	return consumed, err
}

func ConsumeBoundAuthFlow(
	rawToken, purpose, provider, intent string,
	userID int,
	now time.Time,
	payload any,
) (*model.AuthFlow, error) {
	var consumed *model.AuthFlow
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var consumeErr error
		consumed, consumeErr = ConsumeBoundAuthFlowWithTx(
			tx, rawToken, purpose, provider, intent, userID, now, payload,
		)
		return consumeErr
	})
	return consumed, err
}

func ConsumeBoundAuthFlowWithTx(
	tx *gorm.DB,
	rawToken, purpose, provider, intent string,
	userID int,
	now time.Time,
	payload any,
) (*model.AuthFlow, error) {
	if tx == nil {
		return nil, ErrAuthFlowInvalid
	}
	rawToken = strings.TrimSpace(rawToken)
	purpose = strings.TrimSpace(purpose)
	provider = strings.TrimSpace(provider)
	intent = strings.TrimSpace(intent)
	if rawToken == "" || purpose == "" || provider == "" || intent == "" || userID <= 0 {
		return nil, ErrAuthFlowInvalid
	}
	now = now.UTC()
	var consumed model.AuthFlow
	tokenHash := hashAuthFlowToken(rawToken)
	result := tx.Model(&model.AuthFlow{}).
		Where(
			"token_hash = ? AND purpose = ? AND provider = ? AND intent = ? AND user_id = ? AND consumed_at IS NULL AND expires_at > ?",
			tokenHash, purpose, provider, intent, userID, now,
		).
		Update("consumed_at", now)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, classifyBoundAuthFlowConsumeError(
			tx, rawToken, purpose, provider, intent, userID, now,
		)
	}
	if err := tx.Where("token_hash = ?", tokenHash).First(&consumed).Error; err != nil {
		return nil, err
	}
	if payload != nil && consumed.Payload != "" {
		if err := common.UnmarshalJsonStr(consumed.Payload, payload); err != nil {
			return nil, err
		}
	}
	return &consumed, nil
}

func ConsumeAuthFlowWithTx(tx *gorm.DB, rawToken, purpose string, now time.Time, payload any) (*model.AuthFlow, error) {
	if tx == nil {
		return nil, ErrAuthFlowInvalid
	}
	rawToken = strings.TrimSpace(rawToken)
	purpose = strings.TrimSpace(purpose)
	if rawToken == "" || purpose == "" {
		return nil, ErrAuthFlowInvalid
	}
	now = now.UTC()
	var consumed model.AuthFlow
	result := tx.Model(&model.AuthFlow{}).
		Where("token_hash = ? AND purpose = ? AND consumed_at IS NULL AND expires_at > ?",
			hashAuthFlowToken(rawToken), purpose, now).
		Update("consumed_at", now)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, classifyAuthFlowConsumeError(tx, rawToken, purpose, now)
	}
	if err := tx.Where("token_hash = ?", hashAuthFlowToken(rawToken)).First(&consumed).Error; err != nil {
		return nil, err
	}
	if payload != nil && consumed.Payload != "" {
		if err := common.UnmarshalJsonStr(consumed.Payload, payload); err != nil {
			return nil, err
		}
	}
	return &consumed, nil
}

func classifyBoundAuthFlowConsumeError(
	tx *gorm.DB,
	rawToken, purpose, provider, intent string,
	userID int,
	now time.Time,
) error {
	var flow model.AuthFlow
	err := tx.Where("token_hash = ? AND purpose = ?", hashAuthFlowToken(rawToken), purpose).
		First(&flow).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrAuthFlowInvalid
	}
	if err != nil {
		return err
	}
	if flow.Provider != provider || flow.Intent != intent || flow.UserID != userID {
		return ErrAuthFlowInvalid
	}
	if flow.ConsumedAt != nil {
		return ErrAuthFlowConsumed
	}
	if !flow.ExpiresAt.After(now) {
		return ErrAuthFlowExpired
	}
	return ErrAuthFlowInvalid
}

func InspectAuthFlow(rawToken, purpose string, now time.Time, payload any) (*model.AuthFlow, error) {
	rawToken = strings.TrimSpace(rawToken)
	purpose = strings.TrimSpace(purpose)
	if rawToken == "" || purpose == "" {
		return nil, ErrAuthFlowInvalid
	}
	var flow model.AuthFlow
	err := model.DB.Where("token_hash = ? AND purpose = ?", hashAuthFlowToken(rawToken), purpose).
		First(&flow).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAuthFlowInvalid
	}
	if err != nil {
		return nil, err
	}
	if flow.ConsumedAt != nil {
		return nil, ErrAuthFlowConsumed
	}
	if !flow.ExpiresAt.After(now.UTC()) {
		return nil, ErrAuthFlowExpired
	}
	if payload != nil && flow.Payload != "" {
		if err := common.UnmarshalJsonStr(flow.Payload, payload); err != nil {
			return nil, err
		}
	}
	return &flow, nil
}

func classifyAuthFlowConsumeError(tx *gorm.DB, rawToken, purpose string, now time.Time) error {
	var flow model.AuthFlow
	err := tx.Where("token_hash = ? AND purpose = ?", hashAuthFlowToken(rawToken), purpose).
		First(&flow).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrAuthFlowInvalid
	}
	if err != nil {
		return err
	}
	if flow.ConsumedAt != nil {
		return ErrAuthFlowConsumed
	}
	if !flow.ExpiresAt.After(now) {
		return ErrAuthFlowExpired
	}
	return ErrAuthFlowInvalid
}

func ConsumeLoginAuthFlow(rawToken, purpose, loginMethod, ip, userAgent string) (*AuthBundle, error) {
	flow, err := ConsumeAuthFlow(rawToken, purpose, time.Now(), nil)
	if err != nil {
		return nil, err
	}
	if flow.UserID <= 0 {
		return nil, ErrAuthFlowInvalid
	}
	return CreateLoginSession(flow.UserID, loginMethod, ip, userAgent)
}

// ClaimExternalIdentity enforces one provider subject owner and one provider
// slot per user. Repeating the same claim is idempotent.
func ClaimExternalIdentity(tx *gorm.DB, provider, subject string, userID int) error {
	provider = strings.TrimSpace(provider)
	subject = strings.TrimSpace(subject)
	if tx == nil || provider == "" || subject == "" || userID <= 0 {
		return ErrAuthFlowInvalid
	}
	var claim model.ExternalIdentityClaim
	err := tx.Where("provider = ? AND subject = ?", provider, subject).First(&claim).Error
	if err == nil {
		if claim.UserID == userID {
			return nil
		}
		return ErrIdentityConflict
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	err = tx.Where("provider = ? AND user_id = ?", provider, userID).First(&claim).Error
	if err == nil {
		if claim.Subject == subject {
			return nil
		}
		return ErrIdentityConflict
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ExternalIdentityClaim{
		Provider: provider,
		Subject:  subject,
		UserID:   userID,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	// A concurrent insert won one of the unique keys. ON CONFLICT keeps the
	// PostgreSQL transaction usable, so ownership can be classified uniformly
	// on all supported databases.
	var concurrent model.ExternalIdentityClaim
	if err := tx.Where(
		"provider = ? AND (subject = ? OR user_id = ?)",
		provider, subject, userID,
	).First(&concurrent).Error; err != nil {
		return err
	}
	if concurrent.Subject == subject && concurrent.UserID == userID {
		return nil
	}
	return ErrIdentityConflict
}

func hashAuthFlowToken(token string) string {
	return common.GenerateHMACWithKey(authSigningKey("auth-flow"), token)
}

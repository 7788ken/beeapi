package service

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrLoginSessionInvalid = errors.New("login session is invalid")
	ErrLoginSessionRevoked = errors.New("login session is revoked")
	ErrRefreshTokenInvalid = errors.New("refresh token is invalid")
	ErrRefreshRace         = errors.New("refresh token was already rotated")
)

type LoginSessionView struct {
	SID          string `json:"sid"`
	Current      bool   `json:"current"`
	LoginMethod  string `json:"login_method"`
	IP           string `json:"ip"`
	UserAgent    string `json:"user_agent"`
	CreatedAt    int64  `json:"created_at"`
	LastActiveAt int64  `json:"last_active_at"`
	ExpiresAt    int64  `json:"expires_at"`
}

type AuthBundle struct {
	AccessToken     string           `json:"access_token"`
	TokenType       string           `json:"token_type"`
	AccessExpiresAt int64            `json:"access_expires_at"`
	Session         LoginSessionView `json:"session"`
	RefreshToken    string           `json:"-"`
}

func CreateLoginSession(userID int, loginMethod, ip, userAgent string) (*AuthBundle, error) {
	user, err := model.GetUserById(userID, false)
	if err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion <= 0 ||
		(user.ExpiresAt > 0 && user.ExpiresAt <= time.Now().Unix()) {
		return nil, ErrLoginSessionInvalid
	}

	refreshSecret, err := common.GenerateRandomCharsKey(64)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	session := &model.UserSession{
		SID:             uuid.NewString(),
		UserID:          userID,
		Version:         1,
		UserAuthVersion: user.AuthVersion,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     hashRefreshSecret(refreshSecret),
		LoginMethod:     truncateAuthMetadata(loginMethod, 32),
		IP:              truncateAuthMetadata(ip, 64),
		UserAgent:       truncateAuthMetadata(userAgent, 512),
		CreatedAt:       now,
		LastActiveAt:    now,
		ExpiresAt:       time.Unix(now, 0).Add(LoginSessionTTL).Unix(),
	}
	if session.LoginMethod == "" {
		session.LoginMethod = "unknown"
	}
	if err := model.CreateUserSession(session); err != nil {
		return nil, err
	}
	bundle, err := issueAuthBundle(session, session.SID+"."+refreshSecret)
	if err != nil {
		_, _ = model.RevokeUserSession(userID, session.SID, "token_issue_failed")
		return nil, err
	}
	return bundle, nil
}

func ValidateLoginSession(
	identity AuthIdentity,
) (*model.UserSession, *model.UserBase, error) {
	session, err := model.GetUserSessionCached(identity.SessionID)
	if err != nil {
		if errors.Is(err, model.ErrUserSessionInactive) {
			return nil, nil, ErrLoginSessionRevoked
		}
		return nil, nil, err
	}
	now := time.Now().Unix()
	if session.UserID != identity.UserID ||
		session.Status != model.UserSessionStatusActive ||
		session.RevokedAt != 0 ||
		session.ExpiresAt <= now ||
		session.Version != identity.SessionVersion ||
		session.UserAuthVersion != identity.UserAuthVersion {
		return nil, nil, ErrLoginSessionRevoked
	}
	user, err := model.GetUserCache(identity.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user.Status != common.UserStatusEnabled ||
		(user.ExpiresAt > 0 && user.ExpiresAt <= now) ||
		user.AuthVersion != identity.UserAuthVersion {
		return nil, nil, ErrLoginSessionRevoked
	}
	return session, user, nil
}

func RefreshLoginSession(rawRefreshToken string) (*AuthBundle, error) {
	sid, secret, ok := splitRefreshToken(rawRefreshToken)
	if !ok {
		return nil, ErrRefreshTokenInvalid
	}
	session, err := model.GetUserSessionCached(sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRefreshTokenInvalid
		}
		if errors.Is(err, model.ErrUserSessionInactive) {
			return nil, ErrLoginSessionRevoked
		}
		return nil, err
	}
	if session.Status != model.UserSessionStatusActive ||
		session.RevokedAt != 0 || session.ExpiresAt <= time.Now().Unix() {
		return nil, ErrLoginSessionRevoked
	}

	userCache, err := model.GetUserCache(session.UserID)
	if err != nil {
		return nil, err
	}
	user, err := model.GetUserById(session.UserID, false)
	if err != nil {
		return nil, err
	}
	if userCache.Status != common.UserStatusEnabled ||
		(userCache.ExpiresAt > 0 && userCache.ExpiresAt <= time.Now().Unix()) ||
		userCache.AuthVersion != session.UserAuthVersion ||
		user.Status != common.UserStatusEnabled ||
		(user.ExpiresAt > 0 && user.ExpiresAt <= time.Now().Unix()) ||
		user.AuthVersion != session.UserAuthVersion {
		_, _ = model.RevokeUserSession(session.UserID, session.SID, "user_security_changed")
		return nil, ErrLoginSessionRevoked
	}

	nextSecret := deriveNextRefreshSecret(sid, secret)
	rotated, err := model.RotateUserSessionRefresh(
		session.UserID,
		sid,
		hashRefreshSecret(secret),
		hashRefreshSecret(nextSecret),
		time.Now().Unix(),
		RefreshReplayWindow,
	)
	if err != nil {
		if errors.Is(err, model.ErrUserSessionRefreshRace) && rotated != nil &&
			hashRefreshSecret(nextSecret) == rotated.RefreshHash {
			return issueAuthBundle(rotated, sid+"."+nextSecret)
		}
		if errors.Is(err, model.ErrUserSessionRefreshReuse) {
			return nil, ErrLoginSessionRevoked
		}
		if errors.Is(err, model.ErrUserSessionRefreshInvalid) {
			return nil, ErrRefreshTokenInvalid
		}
		if errors.Is(err, model.ErrUserSessionRefreshRace) {
			return nil, ErrRefreshRace
		}
		return nil, err
	}
	return issueAuthBundle(rotated, sid+"."+nextSecret)
}

func RevokeByRefreshToken(rawRefreshToken, reason string) error {
	sid, secret, ok := splitRefreshToken(rawRefreshToken)
	if !ok {
		return nil
	}
	_, err := model.RevokeUserSessionByRefreshHash(
		sid,
		hashRefreshSecret(secret),
		reason,
	)
	return err
}

func issueAuthBundle(session *model.UserSession, rawRefreshToken string) (*AuthBundle, error) {
	accessToken, accessExpiresAt, err := IssueAccessToken(AuthIdentity{
		UserID:          session.UserID,
		SessionID:       session.SID,
		UserAuthVersion: session.UserAuthVersion,
		SessionVersion:  session.Version,
	})
	if err != nil {
		return nil, err
	}
	return &AuthBundle{
		AccessToken:     accessToken,
		TokenType:       "Bearer",
		AccessExpiresAt: accessExpiresAt,
		Session: LoginSessionView{
			SID:          session.SID,
			Current:      true,
			LoginMethod:  session.LoginMethod,
			IP:           session.IP,
			UserAgent:    session.UserAgent,
			CreatedAt:    session.CreatedAt,
			LastActiveAt: session.LastActiveAt,
			ExpiresAt:    session.ExpiresAt,
		},
		RefreshToken: rawRefreshToken,
	}, nil
}

func splitRefreshToken(raw string) (string, string, bool) {
	sid, secret, ok := strings.Cut(strings.TrimSpace(raw), ".")
	if !ok || sid == "" || secret == "" || strings.Contains(secret, ".") {
		return "", "", false
	}
	if _, err := uuid.Parse(sid); err != nil {
		return "", "", false
	}
	return sid, secret, true
}

func hashRefreshSecret(secret string) string {
	return common.GenerateHMACWithKey(authSigningKey("refresh"), secret)
}

func deriveNextRefreshSecret(sid, currentSecret string) string {
	return common.GenerateHMACWithKey(
		authSigningKey("refresh-rotate"),
		sid+"."+currentSecret,
	)
}

func truncateAuthMetadata(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max]
}

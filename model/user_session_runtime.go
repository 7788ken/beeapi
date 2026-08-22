package model

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	userSessionCacheSchema     = 1
	userSessionRevokeBatchSize = 500
)

var (
	ErrUserSessionInvalid               = errors.New("user session is invalid")
	ErrUserSessionInactive              = errors.New("user session is inactive")
	ErrUserSessionRefreshInvalid        = errors.New("user session refresh token is invalid")
	ErrUserSessionRefreshRace           = errors.New("user session refresh is already in progress")
	ErrUserSessionRefreshReuse          = errors.New("user session refresh token was reused")
	errUserSessionCacheObservationStale = errors.New("user session cache observation is stale")
)

func (session *UserSession) AfterFind(_ *gorm.DB) error {
	session.PreviousRefreshHash = strings.TrimSpace(session.PreviousRefreshHash)
	return nil
}

type userSessionCacheEntry struct {
	SID             string
	UserID          int
	Version         int64
	UserAuthVersion int64
	Status          string
	LoginMethod     string
	IP              string
	UserAgent       string
	CreatedAt       int64
	LastActiveAt    int64
	ExpiresAt       int64
	RevokedAt       int64
	RevokedReason   string
	CacheSchema     int
}

func (session *UserSession) cacheEntry() *userSessionCacheEntry {
	return &userSessionCacheEntry{
		SID:             session.SID,
		UserID:          session.UserID,
		Version:         session.Version,
		UserAuthVersion: session.UserAuthVersion,
		Status:          session.Status,
		LoginMethod:     session.LoginMethod,
		IP:              session.IP,
		UserAgent:       session.UserAgent,
		CreatedAt:       session.CreatedAt,
		LastActiveAt:    session.LastActiveAt,
		ExpiresAt:       session.ExpiresAt,
		RevokedAt:       session.RevokedAt,
		RevokedReason:   session.RevokedReason,
		CacheSchema:     userSessionCacheSchema,
	}
}

func (entry *userSessionCacheEntry) session() *UserSession {
	return &UserSession{
		SID:             entry.SID,
		UserID:          entry.UserID,
		Version:         entry.Version,
		UserAuthVersion: entry.UserAuthVersion,
		Status:          entry.Status,
		LoginMethod:     entry.LoginMethod,
		IP:              entry.IP,
		UserAgent:       entry.UserAgent,
		CreatedAt:       entry.CreatedAt,
		LastActiveAt:    entry.LastActiveAt,
		ExpiresAt:       entry.ExpiresAt,
		RevokedAt:       entry.RevokedAt,
		RevokedReason:   entry.RevokedReason,
	}
}

func userSessionCacheKey(sid string) string {
	digest := common.GenerateHMACWithKey(
		[]byte("user-session-cache-v1:"+common.SessionSecret),
		sid,
	)
	return "auth:session:" + digest
}

func userSessionCacheDeadline() time.Time {
	return time.Now().Add(time.Duration(userCacheTTLSeconds()) * time.Second)
}

func CreateUserSession(session *UserSession) error {
	now := time.Now().Unix()
	if session == nil || session.SID == "" || session.UserID <= 0 ||
		session.UserAuthVersion <= 0 || session.RefreshHash == "" ||
		session.ExpiresAt <= now {
		return ErrUserSessionInvalid
	}
	if session.Version <= 0 {
		session.Version = 1
	}
	if session.Status == "" {
		session.Status = UserSessionStatusActive
	}
	if session.Status != UserSessionStatusActive || session.RevokedAt != 0 {
		return ErrUserSessionInvalid
	}
	if session.CreatedAt == 0 {
		session.CreatedAt = now
	}
	if session.LastActiveAt == 0 {
		session.LastActiveAt = now
	}
	cacheDeadline := userSessionCacheDeadline()
	if err := DB.Create(session).Error; err != nil {
		return err
	}
	if err := writeUserSessionCache(session.cacheEntry(), cacheDeadline); err != nil {
		if errors.Is(err, errUserSessionCacheObservationStale) {
			return confirmUserSessionActiveSnapshot(session)
		}
		if errors.Is(err, ErrUserSessionInactive) {
			return err
		}
		common.SysLog("failed to populate newly created user session cache: " + err.Error())
	}
	return nil
}

func GetUserSessionBySID(sid string) (*UserSession, error) {
	if sid == "" {
		return nil, ErrUserSessionInvalid
	}
	var session UserSession
	if err := DB.Where("sid = ?", sid).First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// GetUserSessionCached falls back to the database on a cache miss or Redis
// read failure. A revoking/revoked tombstone is authoritative for denial and
// never falls back to a potentially stale active row.
func GetUserSessionCached(sid string) (*UserSession, error) {
	if sid == "" {
		return nil, ErrUserSessionInvalid
	}
	if common.RedisEnabled {
		entry, err := getUserSessionCache(sid)
		if err == nil {
			return entry.session(), nil
		}
		if errors.Is(err, ErrUserSessionInactive) {
			return nil, err
		}
	}

	cacheDeadline := userSessionCacheDeadline()
	session, err := GetUserSessionBySID(sid)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if session.Status != UserSessionStatusActive ||
		session.RevokedAt != 0 ||
		session.ExpiresAt <= now {
		if common.RedisEnabled {
			entry := session.cacheEntry()
			entry.Status = UserSessionStatusRevoked
			_ = writeUserSessionCache(entry, time.Time{})
		}
		return nil, ErrUserSessionInactive
	}
	if common.RedisEnabled {
		if err := writeUserSessionCache(session.cacheEntry(), cacheDeadline); err != nil {
			if errors.Is(err, errUserSessionCacheObservationStale) {
				if confirmErr := confirmUserSessionActiveSnapshot(session); confirmErr != nil {
					return nil, confirmErr
				}
				return session, nil
			}
			if errors.Is(err, ErrUserSessionInactive) {
				return nil, err
			}
			common.SysLog("failed to synchronously populate user session cache: " + err.Error())
		}
	}
	return session, nil
}

func getUserSessionCache(sid string) (*userSessionCacheEntry, error) {
	var entry userSessionCacheEntry
	if err := common.RedisHGetObj(userSessionCacheKey(sid), &entry); err != nil {
		return nil, err
	}
	if entry.CacheSchema != userSessionCacheSchema ||
		entry.SID != sid ||
		entry.UserID <= 0 ||
		entry.Version <= 0 ||
		entry.UserAuthVersion <= 0 {
		return nil, fmt.Errorf("user session cache schema is stale")
	}
	if entry.Status != UserSessionStatusActive ||
		entry.RevokedAt != 0 ||
		entry.ExpiresAt <= time.Now().Unix() {
		return nil, ErrUserSessionInactive
	}
	return &entry, nil
}

// writeUserSessionCache writes an active snapshot only for the unspent
// observation window captured before its database read/mutation. This prevents
// a delayed active write from starting a fresh TTL after a revoke tombstone
// expires. Deny states start their own bounded TTL at publication time.
func writeUserSessionCache(entry *userSessionCacheEntry, cacheDeadline time.Time) error {
	if entry == nil || !common.RedisEnabled {
		return nil
	}
	now := time.Now()
	sessionExpiresAt := time.Unix(entry.ExpiresAt, 0)
	sessionTTL := sessionExpiresAt.Sub(now)
	var redisExpiration int64
	if entry.Status == UserSessionStatusActive {
		if cacheDeadline.IsZero() {
			return ErrUserSessionInvalid
		}
		if cacheDeadline.Sub(now) <= 0 {
			return errUserSessionCacheObservationStale
		}
		if sessionTTL <= 0 {
			return ErrUserSessionInactive
		}
		cacheExpiresAt := cacheDeadline
		if sessionExpiresAt.Before(cacheExpiresAt) {
			cacheExpiresAt = sessionExpiresAt
		}
		if cacheExpiresAt.Sub(now) < time.Millisecond {
			return errUserSessionCacheObservationStale
		}
		redisExpiration = cacheExpiresAt.UnixMilli()
	} else {
		ttl := min(
			sessionTTL,
			time.Duration(userCacheTTLSeconds())*time.Second,
		)
		if ttl <= 0 {
			ttl = time.Second
		}
		redisExpiration = max(ttl.Milliseconds(), 1)
	}
	entry.CacheSchema = userSessionCacheSchema

	const script = `
local current_status = redis.call('HGET', KEYS[1], 'Status')
local current_version = tonumber(redis.call('HGET', KEYS[1], 'Version') or '0')
if ARGV[5] == 'active' and (current_status == 'revoking' or current_status == 'revoked') then
  return 0
end
if current_version > tonumber(ARGV[3]) then
  return 0
end
redis.call('HSET', KEYS[1],
  'SID', ARGV[1], 'UserID', ARGV[2], 'Version', ARGV[3],
  'UserAuthVersion', ARGV[4], 'Status', ARGV[5],
  'LoginMethod', ARGV[6], 'IP', ARGV[7], 'UserAgent', ARGV[8],
  'CreatedAt', ARGV[9], 'LastActiveAt', ARGV[10], 'ExpiresAt', ARGV[11],
  'RevokedAt', ARGV[12], 'RevokedReason', ARGV[13], 'CacheSchema', ARGV[14])
if ARGV[5] == 'active' then
  redis.call('PEXPIREAT', KEYS[1], ARGV[15])
else
  redis.call('PEXPIRE', KEYS[1], ARGV[15])
end
return 1`
	result, err := common.RDB.Eval(
		context.Background(),
		script,
		[]string{userSessionCacheKey(entry.SID)},
		entry.SID,
		entry.UserID,
		entry.Version,
		entry.UserAuthVersion,
		entry.Status,
		entry.LoginMethod,
		entry.IP,
		entry.UserAgent,
		entry.CreatedAt,
		entry.LastActiveAt,
		entry.ExpiresAt,
		entry.RevokedAt,
		entry.RevokedReason,
		entry.CacheSchema,
		redisExpiration,
	).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrUserSessionInactive
	}
	if entry.Status == UserSessionStatusActive {
		completedAt := time.Now()
		if !completedAt.Before(cacheDeadline) {
			return errUserSessionCacheObservationStale
		}
		if !completedAt.Before(sessionExpiresAt) {
			return ErrUserSessionInactive
		}
	}
	return nil
}

func confirmUserSessionActiveSnapshot(session *UserSession) error {
	if session == nil ||
		session.SID == "" ||
		session.UserID <= 0 ||
		session.Version <= 0 ||
		session.UserAuthVersion <= 0 {
		return ErrUserSessionInvalid
	}
	var count int64
	err := DB.Model(&UserSession{}).
		Where(
			"sid = ? AND user_id = ? AND status = ? AND revoked_at = ? AND expires_at > ? AND version = ? AND user_auth_version = ?",
			session.SID,
			session.UserID,
			UserSessionStatusActive,
			0,
			time.Now().Unix(),
			session.Version,
			session.UserAuthVersion,
		).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrUserSessionInactive
	}
	return nil
}

func writeUserSessionDenyFence(
	session *UserSession,
	status string,
	now int64,
	reason string,
) error {
	if !common.RedisEnabled {
		return nil
	}
	entry := session.cacheEntry()
	entry.Status = status
	entry.RevokedAt = now
	entry.RevokedReason = reason
	return writeUserSessionCache(entry, time.Time{})
}

// RotateUserSessionRefresh uses a compare-and-swap update so every supported
// database has one winner. A recognized previous digest is recoverable only
// inside grace; after grace it revokes the token family. Unknown secrets never
// revoke the session.
func RotateUserSessionRefresh(userID int, sid, presentedHash, nextHash string, now int64, grace time.Duration) (*UserSession, error) {
	if userID <= 0 || sid == "" || presentedHash == "" || nextHash == "" ||
		hmac.Equal([]byte(presentedHash), []byte(nextHash)) {
		return nil, ErrUserSessionInvalid
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	graceSeconds := int64(grace / time.Second)
	if graceSeconds < 0 {
		return nil, ErrUserSessionInvalid
	}

	for range 3 {
		cacheDeadline := userSessionCacheDeadline()
		var session UserSession
		if err := DB.Where("sid = ? AND user_id = ?", sid, userID).First(&session).Error; err != nil {
			return nil, err
		}
		if session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now {
			return nil, ErrUserSessionInactive
		}

		if hmac.Equal([]byte(session.RefreshHash), []byte(presentedHash)) {
			result := DB.Model(&UserSession{}).
				Where(
					"sid = ? AND user_id = ? AND status = ? AND revoked_at = ? AND expires_at > ? AND refresh_hash = ?",
					sid,
					userID,
					UserSessionStatusActive,
					0,
					now,
					presentedHash,
				).
				Updates(map[string]interface{}{
					"previous_refresh_hash": session.RefreshHash,
					"previous_valid_until":  now + graceSeconds,
					"refresh_hash":          nextHash,
					"last_active_at":        now,
				})
			if result.Error != nil {
				return nil, result.Error
			}
			if result.RowsAffected == 0 {
				continue
			}
			session.PreviousRefreshHash = session.RefreshHash
			session.PreviousValidUntil = now + graceSeconds
			session.RefreshHash = nextHash
			session.LastActiveAt = now
			if err := writeUserSessionCache(session.cacheEntry(), cacheDeadline); err != nil {
				if errors.Is(err, errUserSessionCacheObservationStale) {
					if confirmErr := confirmUserSessionActiveSnapshot(&session); confirmErr != nil {
						return nil, confirmErr
					}
				} else if errors.Is(err, ErrUserSessionInactive) {
					return nil, err
				} else {
					common.SysLog("failed to update rotated user session cache: " + err.Error())
				}
			}
			return &session, nil
		}

		if session.PreviousRefreshHash == "" ||
			!hmac.Equal([]byte(session.PreviousRefreshHash), []byte(presentedHash)) {
			return nil, ErrUserSessionRefreshInvalid
		}
		if now <= session.PreviousValidUntil {
			return &session, ErrUserSessionRefreshRace
		}

		if err := writeUserSessionDenyFence(
			&session,
			UserSessionStatusRevoking,
			now,
			"refresh_reuse",
		); err != nil {
			return nil, err
		}
		result := DB.Model(&UserSession{}).
			Where(
				"sid = ? AND user_id = ? AND status = ? AND revoked_at = ? AND expires_at > ?",
				sid,
				userID,
				UserSessionStatusActive,
				0,
				now,
			).
			Updates(map[string]interface{}{
				"status":         UserSessionStatusRevoked,
				"revoked_at":     now,
				"revoked_reason": "refresh_reuse",
			})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			return nil, ErrUserSessionInactive
		}
		session.Status = UserSessionStatusRevoked
		session.RevokedAt = now
		session.RevokedReason = "refresh_reuse"
		if err := writeUserSessionCache(session.cacheEntry(), time.Time{}); err != nil {
			common.SysLog("failed to cache refresh-reuse session revoke: " + err.Error())
		}
		return &session, ErrUserSessionRefreshReuse
	}
	return nil, ErrUserSessionRefreshInvalid
}

func RevokeUserSession(userID int, sid, reason string) (bool, error) {
	if userID <= 0 || sid == "" {
		return false, ErrUserSessionInvalid
	}
	now := time.Now().Unix()
	var candidate UserSession
	if err := DB.Where("sid = ? AND user_id = ?", sid, userID).
		First(&candidate).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if candidate.Status != UserSessionStatusActive ||
		candidate.RevokedAt != 0 ||
		candidate.ExpiresAt <= now {
		return false, nil
	}
	if err := writeUserSessionDenyFence(
		&candidate,
		UserSessionStatusRevoking,
		now,
		reason,
	); err != nil {
		return false, err
	}

	var revoked bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current UserSession
		if err := withForUpdate(tx).
			Where("sid = ? AND user_id = ?", sid, userID).
			First(&current).Error; err != nil {
			return err
		}
		if current.Status != UserSessionStatusActive ||
			current.RevokedAt != 0 ||
			current.ExpiresAt <= now {
			return nil
		}
		result := tx.Model(&UserSession{}).
			Where("sid = ? AND status = ?", sid, UserSessionStatusActive).
			Updates(map[string]interface{}{
				"status":         UserSessionStatusRevoked,
				"revoked_at":     now,
				"revoked_reason": reason,
			})
		if result.Error != nil {
			return result.Error
		}
		revoked = result.RowsAffected == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	if revoked {
		candidate.Status = UserSessionStatusRevoked
		candidate.RevokedAt = now
		candidate.RevokedReason = reason
		if err := writeUserSessionCache(candidate.cacheEntry(), time.Time{}); err != nil {
			common.SysLog("failed to finalize user session revoke tombstone: " + err.Error())
		}
	}
	return revoked, nil
}

// RevokeUserSessionByRefreshHash authenticates logout with possession of the
// current refresh digest. The previous digest is accepted only inside the
// refresh race window.
func RevokeUserSessionByRefreshHash(
	sid string,
	presentedHash string,
	reason string,
) (bool, error) {
	if sid == "" || presentedHash == "" {
		return false, ErrUserSessionInvalid
	}
	now := time.Now().Unix()
	var session UserSession
	var revoked bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := withForUpdate(tx).
			Where("sid = ?", sid).
			First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if session.Status != UserSessionStatusActive ||
			session.RevokedAt != 0 ||
			session.ExpiresAt <= now {
			return nil
		}
		validCurrent := hmac.Equal(
			[]byte(session.RefreshHash),
			[]byte(presentedHash),
		)
		validPrevious := session.PreviousRefreshHash != "" &&
			now <= session.PreviousValidUntil &&
			hmac.Equal(
				[]byte(session.PreviousRefreshHash),
				[]byte(presentedHash),
			)
		if !validCurrent && !validPrevious {
			return nil
		}
		if err := writeUserSessionDenyFence(
			&session,
			UserSessionStatusRevoking,
			now,
			reason,
		); err != nil {
			return err
		}
		result := tx.Model(&UserSession{}).
			Where("sid = ? AND status = ?", sid, UserSessionStatusActive).
			Updates(map[string]interface{}{
				"status":         UserSessionStatusRevoked,
				"revoked_at":     now,
				"revoked_reason": reason,
			})
		if result.Error != nil {
			return result.Error
		}
		revoked = result.RowsAffected == 1
		if revoked {
			session.Status = UserSessionStatusRevoked
			session.RevokedAt = now
			session.RevokedReason = reason
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if revoked {
		if err := writeUserSessionCache(session.cacheEntry(), time.Time{}); err != nil {
			common.SysLog(
				"failed to finalize refresh-authenticated session revoke tombstone: " +
					err.Error(),
			)
		}
	}
	return revoked, nil
}

func RevokeOtherUserSessions(
	userID int,
	currentSID string,
	reason string,
) (int64, error) {
	return revokeUserSessions(userID, currentSID, reason)
}

func RevokeAllUserSessions(userID int, reason string) (int64, error) {
	return revokeUserSessions(userID, "", reason)
}

func revokeUserSessions(
	userID int,
	excludedSID string,
	reason string,
) (int64, error) {
	if userID <= 0 {
		return 0, ErrUserSessionInvalid
	}
	now := time.Now().Unix()
	var totalAffected int64
	for {
		query := DB.Where(
			"user_id = ? AND status = ? AND expires_at > ?",
			userID,
			UserSessionStatusActive,
			now,
		)
		if excludedSID != "" {
			query = query.Where("sid <> ?", excludedSID)
		}
		var candidates []UserSession
		if err := query.
			Order("sid").
			Limit(userSessionRevokeBatchSize).
			Find(&candidates).Error; err != nil {
			return totalAffected, err
		}
		if len(candidates) == 0 {
			return totalAffected, nil
		}
		for i := range candidates {
			if err := writeUserSessionDenyFence(
				&candidates[i],
				UserSessionStatusRevoking,
				now,
				reason,
			); err != nil {
				return totalAffected, err
			}
		}

		sids := make([]string, 0, len(candidates))
		for i := range candidates {
			sids = append(sids, candidates[i].SID)
		}
		var affected int64
		var revoked []UserSession
		err := DB.Transaction(func(tx *gorm.DB) error {
			if err := withForUpdate(tx).
				Where("sid IN ? AND status = ?", sids, UserSessionStatusActive).
				Find(&revoked).Error; err != nil {
				return err
			}
			if len(revoked) == 0 {
				return nil
			}
			lockedSIDs := make([]string, 0, len(revoked))
			for i := range revoked {
				lockedSIDs = append(lockedSIDs, revoked[i].SID)
			}
			result := tx.Model(&UserSession{}).
				Where("sid IN ? AND status = ?", lockedSIDs, UserSessionStatusActive).
				Updates(map[string]interface{}{
					"status":         UserSessionStatusRevoked,
					"revoked_at":     now,
					"revoked_reason": reason,
				})
			affected = result.RowsAffected
			return result.Error
		})
		if err != nil {
			return totalAffected, err
		}
		totalAffected += affected
		for i := range revoked {
			revoked[i].Status = UserSessionStatusRevoked
			revoked[i].RevokedAt = now
			revoked[i].RevokedReason = reason
			if err := writeUserSessionCache(
				revoked[i].cacheEntry(),
				time.Time{},
			); err != nil {
				common.SysLog(
					"failed to finalize bulk user session revoke tombstone: " +
						err.Error(),
				)
			}
		}
	}
}

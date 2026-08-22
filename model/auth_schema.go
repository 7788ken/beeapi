package model

import "time"

const (
	UserSessionStatusActive   = "active"
	UserSessionStatusRevoking = "revoking"
	UserSessionStatusRevoked  = "revoked"
)

// UserSession is the durable control-plane record for a dashboard login
// session. Opaque refresh secrets are never persisted; RefreshHash and
// PreviousRefreshHash store keyed digests.
//
// Dashboard authentication creates and validates these rows for access-token
// authorization, refresh rotation, and revocation.
type UserSession struct {
	SID                 string `json:"sid" gorm:"column:sid;type:varchar(64);primaryKey"`
	UserID              int    `json:"user_id" gorm:"column:user_id;not null;index:idx_user_sessions_user_status_expiry,priority:1;index:idx_user_sessions_user_created,priority:1"`
	Version             int64  `json:"version" gorm:"type:bigint;not null;default:1"`
	UserAuthVersion     int64  `json:"user_auth_version" gorm:"type:bigint;not null"`
	Status              string `json:"status" gorm:"type:varchar(16);not null;index:idx_user_sessions_user_status_expiry,priority:2;index:idx_user_sessions_status_revoked,priority:1"`
	RefreshHash         string `json:"-" gorm:"type:char(64);not null"`
	PreviousRefreshHash string `json:"-" gorm:"type:varchar(64)"`
	PreviousValidUntil  int64  `json:"-" gorm:"type:bigint;not null;default:0"`
	LoginMethod         string `json:"login_method" gorm:"type:varchar(32);not null"`
	IP                  string `json:"ip" gorm:"type:varchar(64)"`
	UserAgent           string `json:"user_agent" gorm:"type:text"`
	CreatedAt           int64  `json:"created_at" gorm:"autoCreateTime;column:created_at;index:idx_user_sessions_user_created,priority:2"`
	LastActiveAt        int64  `json:"last_active_at" gorm:"type:bigint;not null;column:last_active_at"`
	ExpiresAt           int64  `json:"expires_at" gorm:"type:bigint;not null;column:expires_at;index:idx_user_sessions_user_status_expiry,priority:3;index:idx_user_sessions_expires_at"`
	RevokedAt           int64  `json:"revoked_at,omitempty" gorm:"type:bigint;not null;default:0;column:revoked_at;index:idx_user_sessions_status_revoked,priority:2"`
	RevokedReason       string `json:"revoked_reason,omitempty" gorm:"type:varchar(64);column:revoked_reason"`
}

func (UserSession) TableName() string {
	return "user_sessions"
}

// AuthFlow stores one-time state for authentication ceremonies. TokenHash is
// a keyed digest; the opaque browser token is never stored.
type AuthFlow struct {
	ID         int64      `json:"id" gorm:"column:id;primaryKey"`
	TokenHash  string     `json:"-" gorm:"column:token_hash;type:char(64);not null;uniqueIndex"`
	Purpose    string     `json:"purpose" gorm:"column:purpose;type:varchar(32);not null;index:idx_auth_flow_purpose_expiry,priority:1"`
	Provider   string     `json:"provider,omitempty" gorm:"column:provider;type:varchar(64)"`
	Intent     string     `json:"intent,omitempty" gorm:"column:intent;type:varchar(16)"`
	UserID     int        `json:"user_id,omitempty" gorm:"column:user_id;index"`
	SessionID  string     `json:"session_id,omitempty" gorm:"column:session_id;type:varchar(64);index"`
	Payload    string     `json:"-" gorm:"column:payload;type:text"`
	CreatedAt  time.Time  `json:"created_at" gorm:"column:created_at"`
	ExpiresAt  time.Time  `json:"expires_at" gorm:"column:expires_at;not null;index:idx_auth_flow_purpose_expiry,priority:2"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty" gorm:"column:consumed_at;index"`
}

func (AuthFlow) TableName() string {
	return "auth_flows"
}

// ExternalIdentityClaim is the future single-owner mapping for identities
// issued by external providers. The subject and each user's provider slot are
// independently unique.
type ExternalIdentityClaim struct {
	ID        int64     `json:"id" gorm:"column:id;primaryKey"`
	Provider  string    `json:"provider" gorm:"column:provider;type:varchar(32);not null;uniqueIndex:idx_external_identity_subject,priority:1;uniqueIndex:idx_external_identity_user,priority:1"`
	Subject   string    `json:"subject" gorm:"column:subject;type:varchar(256);not null;uniqueIndex:idx_external_identity_subject,priority:2"`
	UserID    int       `json:"user_id" gorm:"column:user_id;not null;index;uniqueIndex:idx_external_identity_user,priority:2"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at"`
}

func (ExternalIdentityClaim) TableName() string {
	return "external_identity_claims"
}

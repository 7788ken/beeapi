package dto

import (
	"time"

	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

// ContentBackupSessionSearchRequest carries the raw session value in the body
// only; the server hashes it and never logs or echoes it (design doc 7.2).
type ContentBackupSessionSearchRequest struct {
	UserID        int        `json:"user_id"`
	SessionValue  string     `json:"session_value"`
	SessionSource *string    `json:"session_source,omitempty"`
	From          *time.Time `json:"from,omitempty"`
	To            *time.Time `json:"to,omitempty"`
	Cursor        string     `json:"cursor,omitempty"`
	PageSize      int        `json:"page_size,omitempty"`
}

// ContentBackupRetryRequest is capped at 100 explicit job ids (design doc 7.2).
type ContentBackupRetryRequest struct {
	JobIDs []string `json:"job_ids"`
}

// ContentBackupConfigUpdateRequest carries the whole blob plus the CAS guard.
// Credentials never travel here: FTPS username/password are daemon env only.
type ContentBackupConfigUpdateRequest struct {
	Config          contentbackup.Config `json:"config"`
	ExpectedVersion int64                `json:"expected_version"`
}

// ContentBackupChannelBackupRequest flips only the backup boolean.
type ContentBackupChannelBackupRequest struct {
	ChannelIDs []int `json:"channel_ids"`
	Enabled    bool  `json:"enabled"`
}

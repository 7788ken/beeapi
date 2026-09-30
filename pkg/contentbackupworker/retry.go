package contentbackupworker

import (
	"context"
	"errors"
	"math"
	"net"
	"os"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// Design doc 6.2 / 9.2 backoff ladder: 1m, 5m, 30m, 2h, 6h, then the hard 6h ceiling for
// every later attempt. attempt starts at 1 and jitter is clamped to [-0.2, 0.2].
var uploadRetryLadder = []time.Duration{
	time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
}

const (
	uploadRetryCeiling  = 6 * time.Hour
	uploadRetryJitter   = 0.2
	uploadAttemptLimit  = 16
	uploadMessageLimit  = 512
	uploadErrorCodeSize = 64
)

// RetryDelay is the frozen design doc 9.2 contract: the attempt-th backoff of the
// 1m/5m/30m/2h/6h ladder scaled by jitter, never above 6h. Attempts past the ladder keep
// the last rung, so attempt 16 still yields a bounded delay before MarkFailed ends the round.
func RetryDelay(attempt int, jitter float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	base := uploadRetryLadder[len(uploadRetryLadder)-1]
	if attempt <= len(uploadRetryLadder) {
		base = uploadRetryLadder[attempt-1]
	}
	if jitter > uploadRetryJitter {
		jitter = uploadRetryJitter
	}
	if jitter < -uploadRetryJitter {
		jitter = -uploadRetryJitter
	}
	delay := time.Duration(math.Round(float64(base) * (1 + jitter)))
	if delay < time.Second {
		delay = time.Second
	}
	if delay > uploadRetryCeiling {
		return uploadRetryCeiling
	}
	return delay
}

// last_error_code values written by the upload state machine. The three unrecoverable local
// codes come from model so RetryFailed keeps refusing to re-queue them (design doc 6.2).
const (
	UploadCodeNetwork            = "network"
	UploadCodeTimeout            = "timeout"
	UploadCodeRemoteHashMismatch = "remote_hash_mismatch"
	UploadCodeRemoteMissing      = "remote_missing"
	UploadCodeUnknown            = "unknown"

	UploadCodeAuth              = "auth"
	UploadCodeCertificate       = "certificate"
	UploadCodeInvalidPin        = "invalid_pin"
	UploadCodePermission        = "permission"
	UploadCodeRemoteUnsupported = "remote_unsupported"

	UploadCodeTargetConfig     = "target_config"
	UploadCodeRemoteConflict   = "remote_conflict"
	UploadCodeInvalidJob       = "invalid_job"
	UploadCodeTargetMismatch   = "target_mismatch"
	UploadCodeRetriesExhausted = "retries_exhausted"
	UploadCodeCancelled        = "cancelled"
	UploadCodeLeaseLost        = "lease_lost"
)

// UploadErrorClass is the design doc 6.2 state machine row an error selects.
type UploadErrorClass int

const (
	UploadClassRetryable UploadErrorClass = iota + 1
	UploadClassTargetUnusable
	UploadClassLocalUnrecoverable
	UploadClassRemoteConflict
	UploadClassJobUnusable
	UploadClassAborted
	UploadClassLeaseLost
)

func (c UploadErrorClass) String() string {
	switch c {
	case UploadClassRetryable:
		return "retryable"
	case UploadClassTargetUnusable:
		return "target_unusable"
	case UploadClassLocalUnrecoverable:
		return "local_unrecoverable"
	case UploadClassRemoteConflict:
		return "remote_conflict"
	case UploadClassJobUnusable:
		return "job_unusable"
	case UploadClassAborted:
		return "aborted"
	case UploadClassLeaseLost:
		return "lease_lost"
	}
	return "none"
}

// UploadErrorInfo is the classification verdict: which terminal write to make, whether the
// attempt may be retried, and whether the node must stop opening connections to the target.
type UploadErrorInfo struct {
	Class       UploadErrorClass
	Code        string
	Retryable   bool
	PauseTarget bool
}

var (
	errUploadLocalMissing      = errors.New("content backup upload: local backup file is missing")
	errUploadLocalHash         = errors.New("content backup upload: local backup does not match compressed_sha256")
	errUploadTargetMismatch    = errors.New("content backup upload: job target_id does not match the configured target")
	errUploadTargetMissing     = errors.New("content backup upload: no upload target is configured")
	errUploadRemoteUnavailable = errors.New("content backup upload: no remote store is available for the target")
)

func uploadRetryable(code string) UploadErrorInfo {
	return UploadErrorInfo{Class: UploadClassRetryable, Code: code, Retryable: true}
}

// A target level failure both fails the job and pauses new upload connections to that
// target (design doc 6.2 row 3); the pause is lifted once the connection probe passes.
func uploadTargetUnusable(code string) UploadErrorInfo {
	return UploadErrorInfo{Class: UploadClassTargetUnusable, Code: code, PauseTarget: true}
}

func uploadTerminal(class UploadErrorClass, code string) UploadErrorInfo {
	return UploadErrorInfo{Class: class, Code: code}
}

// ClassifyUploadError maps transport and local failures onto the state machine. Unknown
// errors stay retryable so a new failure mode costs attempts instead of silently failing a
// job whose local backup is still intact.
func ClassifyUploadError(err error) UploadErrorInfo {
	if err == nil {
		return UploadErrorInfo{}
	}
	switch {
	case errors.Is(err, model.ErrLeaseLost):
		return uploadTerminal(UploadClassLeaseLost, UploadCodeLeaseLost)
	case errors.Is(err, errUploadLocalMissing), errors.Is(err, os.ErrNotExist):
		return uploadTerminal(UploadClassLocalUnrecoverable, model.ContentBackupErrorLocalMissing)
	case errors.Is(err, errUploadLocalHash):
		return uploadTerminal(UploadClassLocalUnrecoverable, model.ContentBackupErrorHashError)
	case errors.Is(err, errUploadTargetMismatch), errors.Is(err, errUploadTargetMissing):
		return uploadTerminal(UploadClassJobUnusable, UploadCodeTargetMismatch)
	case errors.Is(err, context.Canceled):
		return uploadTerminal(UploadClassAborted, UploadCodeCancelled)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrFTPSTimeout):
		return uploadRetryable(UploadCodeTimeout)
	case errors.Is(err, ErrFTPSAuth):
		return uploadTargetUnusable(UploadCodeAuth)
	case errors.Is(err, ErrFTPSCertificate):
		return uploadTargetUnusable(UploadCodeCertificate)
	case errors.Is(err, ErrFTPSInvalidPin):
		return uploadTargetUnusable(UploadCodeInvalidPin)
	case errors.Is(err, ErrFTPSConfig):
		return uploadTargetUnusable(UploadCodeTargetConfig)
	case errors.Is(err, ErrFTPSPermission):
		return uploadTargetUnusable(UploadCodePermission)
	case errors.Is(err, ErrFTPSUnsupported):
		return uploadTargetUnusable(UploadCodeRemoteUnsupported)
	case errors.Is(err, ErrFTPSConflict):
		return uploadTerminal(UploadClassRemoteConflict, UploadCodeRemoteConflict)
	case errors.Is(err, ErrFTPSInvalidJob), errors.Is(err, errUploadRemoteUnavailable):
		return uploadTerminal(UploadClassJobUnusable, UploadCodeInvalidJob)
	case errors.Is(err, ErrFTPSLocalSource):
		return uploadTerminal(UploadClassLocalUnrecoverable, model.ContentBackupErrorHashError)
	case errors.Is(err, ErrFTPSHashMismatch):
		return uploadRetryable(UploadCodeRemoteHashMismatch)
	case errors.Is(err, ErrFTPSMissing):
		return uploadRetryable(UploadCodeRemoteMissing)
	case errors.Is(err, ErrFTPSNetwork):
		return uploadRetryable(UploadCodeNetwork)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return uploadRetryable(UploadCodeTimeout)
		}
		return uploadRetryable(UploadCodeNetwork)
	}
	return uploadRetryable(UploadCodeUnknown)
}

func uploadErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > uploadMessageLimit {
		message = message[:uploadMessageLimit]
	}
	return message
}

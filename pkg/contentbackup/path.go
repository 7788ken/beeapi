package contentbackup

import (
	"regexp"
	"time"

	"github.com/google/uuid"
)

const RemoteFileExtension = ".json.gz"

// 目录按北京时间生成而 DB 存 UTC：跨午夜请求停在开始那天，不按完成日期搬目录。
var remotePathLocation = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*3600)
}()

var (
	siteLabelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	jobIDPattern     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	shardPattern     = regexp.MustCompile(`^[0-9a-f]{2}$`)
	// LeaseTokenPattern is every lease token a temp name may carry; it keeps the token a
	// single safe path component.
	LeaseTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
)

func NewJobID() string {
	return uuid.NewString()
}

// RemotePath is the immutable archive key for a new job:
// /{site}/{beijing-date}/{jobID[0:2]}/{jobID}.json.gz.
// userID and sessionKey stay validated because callers still pass them, and the
// envelope keeps both for lookup. They are not path segments: a missing session
// used to dump every request for one user into a single nosession directory, and
// each upload then listed that directory. A path already stored on a job row is
// never recomputed, so files uploaded before this layout stay where they are.
func RemotePath(site string, userID int, sessionKey, jobID string, started time.Time) (string, error) {
	if reason := siteLabelProblem(site); reason != "" {
		return "", &ValidationError{Field: "site_id", Reason: reason, Errs: []error{ErrInvalidSiteLabel}}
	}
	if userID < 0 {
		return "", &ValidationError{Field: "user_id", Reason: "must not be negative", Errs: []error{ErrInvalidUserID}}
	}
	if reason := sessionKeyProblem(sessionKey); reason != "" {
		return "", &ValidationError{Field: "session_key", Reason: reason, Errs: []error{ErrInvalidSessionKey}}
	}
	if reason := jobIDProblem(jobID); reason != "" {
		return "", &ValidationError{Field: "job_id", Reason: reason, Errs: []error{ErrInvalidJobID}}
	}
	if started.IsZero() {
		return "", &ValidationError{Field: "request_started_at", Reason: "must not be zero", Errs: []error{ErrInvalidStartTime}}
	}
	return "/" + site + "/" + started.In(remotePathLocation).Format("2006-01-02") +
		"/" + jobID[:2] + "/" + jobID + RemoteFileExtension, nil
}

// IncomingDir holds in-flight temp objects for one job-id shard. A sweep lists
// only this directory, which contains the live uploads in that shard, never the
// archive directory those uploads will be renamed into.
func IncomingDir(site, jobID string) (string, error) {
	if reason := jobIDProblem(jobID); reason != "" {
		return "", &ValidationError{Field: "job_id", Reason: reason, Errs: []error{ErrInvalidJobID}}
	}
	return IncomingShardDir(site, jobID[:2])
}

// IncomingShardDir is one of the 256 incoming shards, named by the first two hex
// characters of the job ids it holds ("00".."ff").
func IncomingShardDir(site, shard string) (string, error) {
	if reason := siteLabelProblem(site); reason != "" {
		return "", &ValidationError{Field: "site_id", Reason: reason, Errs: []error{ErrInvalidSiteLabel}}
	}
	if !shardPattern.MatchString(shard) {
		return "", &ValidationError{Field: "shard", Reason: "must be two lowercase hex characters", Errs: []error{ErrInvalidJobID}}
	}
	return "/" + site + "/.incoming/" + shard, nil
}

// TempRemotePath is the logical temp object for one lease. suffix includes the dot.
func TempRemotePath(site, jobID, leaseToken, suffix string) (string, error) {
	dir, err := IncomingDir(site, jobID)
	if err != nil {
		return "", err
	}
	return dir + "/" + jobID + "." + leaseToken + suffix, nil
}

// ParseTempName reverses TempRemotePath's file name. ok is false for any name this code
// could not have written, so a listing never turns a foreign file into a delete target.
func ParseTempName(name, suffix string) (jobID, leaseToken string, ok bool) {
	const jobIDLen = 36
	if len(name) <= jobIDLen+1+len(suffix) || name[jobIDLen] != '.' || name[len(name)-len(suffix):] != suffix {
		return "", "", false
	}
	jobID = name[:jobIDLen]
	leaseToken = name[jobIDLen+1 : len(name)-len(suffix)]
	if jobIDProblem(jobID) != "" || !LeaseTokenPattern.MatchString(leaseToken) {
		return "", "", false
	}
	return jobID, leaseToken, true
}

func (m Metadata) RemotePath() (string, error) {
	return RemotePath(m.SiteID, m.UserID, m.SessionKey(), m.JobID, m.RequestStartedAt)
}

func ValidateSiteLabel(site string) error {
	if reason := siteLabelProblem(site); reason != "" {
		return &ValidationError{Field: "site_id", Reason: reason, Errs: []error{ErrInvalidSiteLabel}}
	}
	return nil
}

func ValidateSessionKey(sessionKey string) error {
	if reason := sessionKeyProblem(sessionKey); reason != "" {
		return &ValidationError{Field: "session_key", Reason: reason, Errs: []error{ErrInvalidSessionKey}}
	}
	return nil
}

func ValidateJobID(jobID string) error {
	if reason := jobIDProblem(jobID); reason != "" {
		return &ValidationError{Field: "job_id", Reason: reason, Errs: []error{ErrInvalidJobID}}
	}
	return nil
}

func ValidateFrameSHA256(sum string) error {
	if !isLowerHex64(sum) {
		return &ValidationError{
			Field:  "frame_sha256",
			Reason: "must be 64 lowercase hex characters",
			Errs:   []error{ErrInvalidFrameSHA256},
		}
	}
	return nil
}

func siteLabelProblem(site string) string {
	if site == "" {
		return "must not be empty"
	}
	if !siteLabelPattern.MatchString(site) {
		return "must match [a-z0-9][a-z0-9_-]{0,31}"
	}
	return ""
}

func sessionKeyProblem(sessionKey string) string {
	if sessionKey == "" {
		return "must not be empty"
	}
	if sessionKey == NoSessionKey {
		return ""
	}
	if !isLowerHex64(sessionKey) {
		return "must be " + NoSessionKey + " or 64 lowercase hex characters"
	}
	return ""
}

// jobIDProblem only accepts the server generated lowercase UUID form, so no client value can reach a path.
func jobIDProblem(jobID string) string {
	if jobID == "" {
		return "must not be empty"
	}
	if !jobIDPattern.MatchString(jobID) {
		return "must be a lowercase uuid"
	}
	return ""
}

func isLowerHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

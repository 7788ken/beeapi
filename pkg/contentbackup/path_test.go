package contentbackup

import (
	"errors"
	"path"
	"strings"
	"testing"
	"time"
)

func TestContentBackupRemotePath(t *testing.T) {
	started := time.Date(2026, 9, 15, 4, 34, 56, 789000000, time.UTC)
	got, err := RemotePath("ai", 10086, NoSessionKey, "00000000-0000-4000-8000-000000000001", started)
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	want := "/ai/2026-09-15/00/00000000-0000-4000-8000-000000000001.json.gz"
	if got != want {
		t.Fatalf("RemotePath = %q, want %q", got, want)
	}

	sessionKey := SessionKey(SessionSourceUser, "u-10086")
	got, err = RemotePath("us1", 7, sessionKey, "11111111-2222-4333-8444-555555555555", started)
	if err != nil {
		t.Fatalf("RemotePath with session: %v", err)
	}
	want = "/us1/2026-09-15/11/11111111-2222-4333-8444-555555555555.json.gz"
	if got != want {
		t.Fatalf("RemotePath = %q, want %q", got, want)
	}
	if strings.Contains(got, sessionKey) || strings.Contains(got, "/u-7/") {
		t.Fatalf("user and session stay out of the archive path, got %q", got)
	}
	if strings.Count(got, "/") != 4 {
		t.Fatalf("remote path must have exactly four separators, got %q", got)
	}
}

func TestContentBackupRemotePathUsesBeijingDate(t *testing.T) {
	jobID := "00000000-0000-4000-8000-000000000001"
	dayOf := func(started time.Time) string {
		t.Helper()
		path, err := RemotePath("ai", 1, NoSessionKey, jobID, started)
		if err != nil {
			t.Fatalf("RemotePath: %v", err)
		}
		return strings.Split(path, "/")[2]
	}
	tests := []struct {
		name    string
		started time.Time
		want    string
	}{
		{"utc morning stays on the same beijing day", time.Date(2026, 9, 15, 4, 34, 56, 0, time.UTC), "2026-09-15"},
		{"utc 15:59:59 is still 23:59:59 in beijing", time.Date(2026, 9, 15, 15, 59, 59, 0, time.UTC), "2026-09-15"},
		{"utc 16:00 rolls into the next beijing day", time.Date(2026, 9, 15, 16, 0, 0, 0, time.UTC), "2026-09-16"},
		{"same instant in another zone yields the same day", time.Date(2026, 9, 15, 11, 0, 0, 0, time.FixedZone("EST", -5*3600)), "2026-09-16"},
		{"year boundary in beijing", time.Date(2026, 12, 31, 16, 30, 0, 0, time.UTC), "2027-01-01"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dayOf(test.started); got != test.want {
				t.Fatalf("date segment = %q, want %q", got, test.want)
			}
		})
	}
}

func TestContentBackupRemotePathKeepsMidnightRequestOnStartDay(t *testing.T) {
	jobID := "00000000-0000-4000-8000-000000000001"
	started := time.Date(2026, 9, 15, 15, 50, 0, 0, time.UTC)
	finished := started.Add(20 * time.Minute)
	startPath, err := RemotePath("ai", 1, NoSessionKey, jobID, started)
	if err != nil {
		t.Fatalf("RemotePath(started): %v", err)
	}
	finishPath, err := RemotePath("ai", 1, NoSessionKey, jobID, finished)
	if err != nil {
		t.Fatalf("RemotePath(finished): %v", err)
	}
	if !strings.Contains(startPath, "/2026-09-15/") {
		t.Fatalf("a request starting at 23:50 beijing must stay on 2026-09-15, got %q", startPath)
	}
	if !strings.Contains(finishPath, "/2026-09-16/") {
		t.Fatalf("the same request finishing after midnight would move to 2026-09-16, got %q", finishPath)
	}
	if startPath == finishPath {
		t.Fatal("the date must come from the start time argument, callers must pass request start")
	}
}

func TestContentBackupRemotePathIsDeterministic(t *testing.T) {
	started := time.Date(2026, 9, 15, 4, 34, 56, 789000000, time.UTC)
	first, err := RemotePath("ai", 10086, NoSessionKey, "00000000-0000-4000-8000-000000000001", started)
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	for i := 0; i < 3; i++ {
		again, err := RemotePath("ai", 10086, NoSessionKey, "00000000-0000-4000-8000-000000000001", started)
		if err != nil {
			t.Fatalf("RemotePath: %v", err)
		}
		if again != first {
			t.Fatalf("remote path must be immutable once created: %q != %q", again, first)
		}
	}
}

func TestContentBackupRemotePathShardsByJobID(t *testing.T) {
	started := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	sessionKey := SessionKey(SessionSourcePromptCacheKey, "shared-cache-key")
	jobID := "00000000-0000-4000-8000-000000000001"
	first, err := RemotePath("ai", 10086, sessionKey, jobID, started)
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	second, err := RemotePath("ai", 20001, NoSessionKey, jobID, started)
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	if first != second {
		t.Fatalf("user and session must not change an already-identified job path: %q / %q", first, second)
	}
	if strings.Contains(first, "nosession") || strings.Contains(first, "/u-") {
		t.Fatalf("archive path must be sharded by job id, got %q", first)
	}
	third, err := RemotePath("ai", 10086, sessionKey, "00000000-0000-4000-8000-000000000002", started)
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	if third == first {
		t.Fatal("distinct jobs must produce distinct file names")
	}
	if path.Dir(third) != path.Dir(first) {
		t.Fatalf("jobs sharing a two-hex prefix share a shard directory: %q / %q", first, third)
	}
	otherShard, err := RemotePath("ai", 10086, sessionKey, "ab000000-0000-4000-8000-000000000001", started)
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	if path.Dir(otherShard) == path.Dir(first) {
		t.Fatalf("different job-id prefixes must land in different shards: %q / %q", first, otherShard)
	}
	other, err := RemotePath("us1", 10086, sessionKey, jobID, started)
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	if other == first {
		t.Fatal("sites must not share a remote directory")
	}
}

func TestContentBackupIncomingDirStaysOffTheArchivePath(t *testing.T) {
	jobID := "00000000-0000-4000-8000-000000000001"
	dir, err := IncomingDir("ai", jobID)
	if err != nil {
		t.Fatalf("IncomingDir: %v", err)
	}
	if dir != "/ai/.incoming/00" {
		t.Fatalf("IncomingDir = %q", dir)
	}
	temp, err := TempRemotePath("ai", jobID, "0123456789abcdef0123456789abcdef", ".uploading")
	if err != nil {
		t.Fatalf("TempRemotePath: %v", err)
	}
	if temp != "/ai/.incoming/00/"+jobID+".0123456789abcdef0123456789abcdef.uploading" {
		t.Fatalf("TempRemotePath = %q", temp)
	}
	archive, err := RemotePath("ai", 1, NoSessionKey, jobID, time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	if strings.HasPrefix(temp, archive) || strings.Contains(archive, "/.incoming/") {
		t.Fatalf("temp %q must not sit inside archive %q", temp, archive)
	}
}

func TestContentBackupIncomingShardDir(t *testing.T) {
	for _, shard := range []string{"00", "09", "ab", "ff"} {
		dir, err := IncomingShardDir("ai", shard)
		if err != nil {
			t.Fatalf("IncomingShardDir(%q): %v", shard, err)
		}
		if dir != "/ai/.incoming/"+shard {
			t.Fatalf("IncomingShardDir(%q) = %q", shard, dir)
		}
	}
	// The reaper lists exactly the directory uploads write their temps into.
	jobID := "ab000000-0000-4000-8000-000000000001"
	byJob, err := IncomingDir("ai", jobID)
	if err != nil {
		t.Fatalf("IncomingDir: %v", err)
	}
	byShard, err := IncomingShardDir("ai", jobID[:2])
	if err != nil {
		t.Fatalf("IncomingShardDir: %v", err)
	}
	temp, err := TempRemotePath("ai", jobID, "0123456789abcdef0123456789abcdef", ".uploading")
	if err != nil {
		t.Fatalf("TempRemotePath: %v", err)
	}
	if byJob != byShard || path.Dir(temp) != byShard {
		t.Fatalf("job dir %q, shard dir %q and temp %q must agree", byJob, byShard, temp)
	}

	for _, shard := range []string{"", "a", "abc", "AB", "aB", "g0", "..", "./", "a/", "/a", " a", "a\n", "ab\n"} {
		dir, err := IncomingShardDir("ai", shard)
		if err == nil || dir != "" {
			t.Fatalf("IncomingShardDir(%q) = %q, %v, want an error", shard, dir, err)
		}
		var validation *ValidationError
		if !errors.Is(err, ErrInvalidJobID) || !errors.As(err, &validation) || validation.Field != "shard" {
			t.Fatalf("IncomingShardDir(%q) error = %v, want a shard ValidationError", shard, err)
		}
	}
	for _, site := range []string{"", "AI", "../ai", "a/b"} {
		if _, err := IncomingShardDir(site, "00"); !errors.Is(err, ErrInvalidSiteLabel) {
			t.Fatalf("IncomingShardDir(site %q) error = %v, want %v", site, err, ErrInvalidSiteLabel)
		}
	}
}

func TestContentBackupParseTempNameRoundTripsTempRemotePath(t *testing.T) {
	const suffix = ".uploading"
	jobID := "ab000000-0000-4000-8000-000000000001"
	tokens := []string{
		"0123456789abcdef0123456789abcdef",
		"abcdefgh",
		strings.Repeat("Z", 128),
		"A-b_C-d_",
		strings.Repeat("-", 8),
	}
	for _, token := range tokens {
		temp, err := TempRemotePath("ai", jobID, token, suffix)
		if err != nil {
			t.Fatalf("TempRemotePath(token %q): %v", token, err)
		}
		gotJob, gotToken, ok := ParseTempName(path.Base(temp), suffix)
		if !ok || gotJob != jobID || gotToken != token {
			t.Fatalf("ParseTempName(%q) = %q, %q, %v", path.Base(temp), gotJob, gotToken, ok)
		}
		again, err := TempRemotePath("ai", gotJob, gotToken, suffix)
		if err != nil || again != temp {
			t.Fatalf("rebuilt temp = %q, %v, want %q", again, err, temp)
		}
	}
}

func TestContentBackupParseTempNameRejectsForeignNames(t *testing.T) {
	const suffix = ".uploading"
	jobID := "ab000000-0000-4000-8000-000000000001"
	token := "0123456789abcdef0123456789abcdef"
	tests := []struct {
		name string
		file string
	}{
		{"wrong suffix", jobID + "." + token + ".partial"},
		{"no suffix", jobID + "." + token},
		{"suffix not at the end", jobID + "." + token + suffix + ".bak"},
		{"suffix in another case", jobID + "." + token + ".UPLOADING"},
		{"archive name", jobID + RemoteFileExtension},
		{"uppercase job id", strings.ToUpper(jobID) + "." + token + suffix},
		{"job id with non hex", "zz000000-0000-4000-8000-000000000001." + token + suffix},
		{"job id without dashes", strings.ReplaceAll(jobID, "-", "0") + "." + token + suffix},
		{"short job id", jobID[:35] + "." + token + suffix},
		{"no dot after job id", jobID + "_" + token + suffix},
		{"token with bad chars", jobID + ".0123456789abcdef!" + suffix},
		{"token with space", jobID + ".01234567 89abcdef" + suffix},
		{"token with slash", jobID + ".01234567/89abcdef" + suffix},
		{"traversal token", jobID + "./../../../etc/passwd" + suffix},
		{"extra segment", jobID + "." + token + "." + token + suffix},
		{"empty segment", jobID + ".." + token + suffix},
		{"token too short", jobID + ".abcdefg" + suffix},
		{"token too long", jobID + "." + strings.Repeat("a", 129) + suffix},
		{"empty token", jobID + "." + suffix},
		{"job id and suffix only", jobID + suffix},
		{"suffix only", suffix},
		{"empty", ""},
		{"full path instead of a name", "/ai/.incoming/ab/" + jobID + "." + token + suffix},
		{"leading dot", "." + jobID + "." + token + suffix},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotJob, gotToken, ok := ParseTempName(test.file, suffix)
			if ok || gotJob != "" || gotToken != "" {
				t.Fatalf("ParseTempName(%q) = %q, %q, %v, want a rejection", test.file, gotJob, gotToken, ok)
			}
		})
	}
}

func TestContentBackupRemotePathRejectsTraversal(t *testing.T) {
	started := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	sessionKey := SessionKey(SessionSourceUser, "u-1")
	jobID := "00000000-0000-4000-8000-000000000001"
	tests := []struct {
		name       string
		site       string
		userID     int
		sessionKey string
		jobID      string
		started    time.Time
		wantErr    error
	}{
		{name: "empty site", site: "", userID: 1, sessionKey: NoSessionKey, jobID: jobID, started: started, wantErr: ErrInvalidSiteLabel},
		{name: "uppercase site", site: "AI", userID: 1, sessionKey: NoSessionKey, jobID: jobID, started: started, wantErr: ErrInvalidSiteLabel},
		{name: "site traversal", site: "../us1", userID: 1, sessionKey: NoSessionKey, jobID: jobID, started: started, wantErr: ErrInvalidSiteLabel},
		{name: "site with slash", site: "a/b", userID: 1, sessionKey: NoSessionKey, jobID: jobID, started: started, wantErr: ErrInvalidSiteLabel},
		{name: "site with dot", site: "a.b", userID: 1, sessionKey: NoSessionKey, jobID: jobID, started: started, wantErr: ErrInvalidSiteLabel},
		{name: "site starting with dash", site: "-ai", userID: 1, sessionKey: NoSessionKey, jobID: jobID, started: started, wantErr: ErrInvalidSiteLabel},
		{name: "site starting with underscore", site: "_ai", userID: 1, sessionKey: NoSessionKey, jobID: jobID, started: started, wantErr: ErrInvalidSiteLabel},
		{name: "site of 33 chars", site: strings.Repeat("a", 33), userID: 1, sessionKey: NoSessionKey, jobID: jobID, started: started, wantErr: ErrInvalidSiteLabel},
		{name: "negative user id", site: "ai", userID: -1, sessionKey: NoSessionKey, jobID: jobID, started: started, wantErr: ErrInvalidUserID},
		{name: "empty session key", site: "ai", userID: 1, sessionKey: "", jobID: jobID, started: started, wantErr: ErrInvalidSessionKey},
		{name: "session key traversal", site: "ai", userID: 1, sessionKey: "..", jobID: jobID, started: started, wantErr: ErrInvalidSessionKey},
		{name: "session key slash", site: "ai", userID: 1, sessionKey: "a/b", jobID: jobID, started: started, wantErr: ErrInvalidSessionKey},
		{name: "uppercase session key", site: "ai", userID: 1, sessionKey: strings.ToUpper(sessionKey), jobID: jobID, started: started, wantErr: ErrInvalidSessionKey},
		{name: "session key of 63 chars", site: "ai", userID: 1, sessionKey: strings.Repeat("a", 63), jobID: jobID, started: started, wantErr: ErrInvalidSessionKey},
		{name: "session key of 65 chars", site: "ai", userID: 1, sessionKey: strings.Repeat("a", 65), jobID: jobID, started: started, wantErr: ErrInvalidSessionKey},
		{name: "raw session value as key", site: "ai", userID: 1, sessionKey: "  Cache-Key/1  ", jobID: jobID, started: started, wantErr: ErrInvalidSessionKey},
		{name: "empty job id", site: "ai", userID: 1, sessionKey: sessionKey, jobID: "", started: started, wantErr: ErrInvalidJobID},
		{name: "job id traversal", site: "ai", userID: 1, sessionKey: sessionKey, jobID: "../../etc/passwd", started: started, wantErr: ErrInvalidJobID},
		{name: "job id with slash", site: "ai", userID: 1, sessionKey: sessionKey, jobID: "00000000-0000-4000-8000-00000000000/x", started: started, wantErr: ErrInvalidJobID},
		{name: "job id dot", site: "ai", userID: 1, sessionKey: sessionKey, jobID: ".", started: started, wantErr: ErrInvalidJobID},
		{name: "job id dot suffix", site: "ai", userID: 1, sessionKey: sessionKey, jobID: jobID + ".gz", started: started, wantErr: ErrInvalidJobID},
		{name: "job id without dashes", site: "ai", userID: 1, sessionKey: sessionKey, jobID: "000000000000400080000000000000001", started: started, wantErr: ErrInvalidJobID},
		{name: "uppercase job id", site: "ai", userID: 1, sessionKey: sessionKey, jobID: strings.ToUpper("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"), started: started, wantErr: ErrInvalidJobID},
		{name: "job id with space", site: "ai", userID: 1, sessionKey: sessionKey, jobID: jobID + " ", started: started, wantErr: ErrInvalidJobID},
		{name: "zero start time", site: "ai", userID: 1, sessionKey: sessionKey, jobID: jobID, started: time.Time{}, wantErr: ErrInvalidStartTime},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, err := RemotePath(test.site, test.userID, test.sessionKey, test.jobID, test.started)
			if err == nil {
				t.Fatalf("RemotePath(%q, %d, %q, %q) = %q, want an error", test.site, test.userID, test.sessionKey, test.jobID, path)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if path != "" {
				t.Fatalf("a rejected path must be empty, got %q", path)
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error %v must be a *ValidationError", err)
			}
			if validation.Field == "" || validation.Reason == "" {
				t.Fatalf("error %v must name the offending field and reason", err)
			}
		})
	}
}

func TestContentBackupRemotePathAcceptsBoundaryValues(t *testing.T) {
	started := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	jobID := "00000000-0000-4000-8000-000000000001"
	sessionKey := SessionKey(SessionSourceUser, "u-1")
	tests := []struct {
		name       string
		site       string
		userID     int
		sessionKey string
	}{
		{"single char site", "a", 0, sessionKey},
		{"32 char site", strings.Repeat("a", 32), 0, sessionKey},
		{"site with dash and underscore", "us1-a_b", 0, sessionKey},
		{"user id zero", "ai", 0, sessionKey},
		{"nosession directory", "ai", 1, NoSessionKey},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, err := RemotePath(test.site, test.userID, test.sessionKey, jobID, started)
			if err != nil {
				t.Fatalf("RemotePath: %v", err)
			}
			want := "/" + test.site + "/2026-09-15/" + jobID[:2] + "/" + jobID + ".json.gz"
			if path != want {
				t.Fatalf("RemotePath = %q, want %q", path, want)
			}
		})
	}
}

func TestContentBackupMetadataRemotePath(t *testing.T) {
	meta := validMetadata()
	got, err := meta.RemotePath()
	if err != nil {
		t.Fatalf("Metadata.RemotePath: %v", err)
	}
	want := "/ai/2026-09-15/" + meta.JobID[:2] + "/" + meta.JobID + ".json.gz"
	if got != want {
		t.Fatalf("Metadata.RemotePath = %q, want %q", got, want)
	}
	source := SessionSourceMetadataUserID
	value := "claude-user-1"
	meta.SessionSource = &source
	meta.SessionValue = &value
	meta.SessionMissingReason = nil
	got, err = meta.RemotePath()
	if err != nil {
		t.Fatalf("Metadata.RemotePath: %v", err)
	}
	if got != want {
		t.Fatalf("a session must not move the archive path: %q, want %q", got, want)
	}

	broken := meta
	broken.JobID = "not-a-uuid"
	if _, err := broken.RemotePath(); !errors.Is(err, ErrInvalidJobID) {
		t.Fatalf("Metadata.RemotePath error = %v, want %v", err, ErrInvalidJobID)
	}
}

func TestContentBackupJobIDGeneration(t *testing.T) {
	seen := make(map[string]bool, 8)
	for i := 0; i < 8; i++ {
		jobID := NewJobID()
		if err := ValidateJobID(jobID); err != nil {
			t.Fatalf("NewJobID produced %q which ValidateJobID rejects: %v", jobID, err)
		}
		if len(jobID) != 36 {
			t.Fatalf("job id %q must be a dashed uuid", jobID)
		}
		if jobID != strings.ToLower(jobID) {
			t.Fatalf("job id %q must be lowercase so case insensitive remotes cannot collide", jobID)
		}
		if seen[jobID] {
			t.Fatalf("job id %q repeated", jobID)
		}
		seen[jobID] = true
	}
}

func TestContentBackupPathValidators(t *testing.T) {
	if err := ValidateSiteLabel("ai"); err != nil {
		t.Fatalf("ValidateSiteLabel(ai) = %v", err)
	}
	if err := ValidateSiteLabel("AI"); !errors.Is(err, ErrInvalidSiteLabel) {
		t.Fatalf("ValidateSiteLabel(AI) = %v, want %v", err, ErrInvalidSiteLabel)
	}
	if err := ValidateSessionKey(NoSessionKey); err != nil {
		t.Fatalf("ValidateSessionKey(nosession) = %v", err)
	}
	if err := ValidateSessionKey(SessionKey(SessionSourceUser, "u-1")); err != nil {
		t.Fatalf("ValidateSessionKey(hash) = %v", err)
	}
	if err := ValidateSessionKey("noSession"); !errors.Is(err, ErrInvalidSessionKey) {
		t.Fatalf("ValidateSessionKey(noSession) = %v, want %v", err, ErrInvalidSessionKey)
	}
	if err := ValidateJobID(NewJobID()); err != nil {
		t.Fatalf("ValidateJobID(NewJobID()) = %v", err)
	}
	if err := ValidateJobID("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"); err != nil {
		t.Fatalf("a lowercase uuid with letters must be accepted: %v", err)
	}
	if err := ValidateJobID(".."); !errors.Is(err, ErrInvalidJobID) {
		t.Fatalf("ValidateJobID(..) = %v, want %v", err, ErrInvalidJobID)
	}
}

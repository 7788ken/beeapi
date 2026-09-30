package contentbackupworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

const cbTestLeaseToken = "0123456789abcdef0123456789abcdef"

func cbTestContent(size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i*7 + 13)
	}
	return b
}

func cbTestJobAndLease(t *testing.T, content []byte, token string) (model.ContentBackupJob, model.ContentBackupLease) {
	t.Helper()
	jobID := contentbackup.NewJobID()
	started := time.Date(2026, 9, 15, 4, 34, 56, 0, time.UTC)
	remote, err := contentbackup.RemotePath("sitea", 10086, contentbackup.NoSessionKey, jobID, started)
	if err != nil {
		t.Fatalf("remote path: %v", err)
	}
	sum := sha256.Sum256(content)
	job := model.ContentBackupJob{
		SiteID:           "sitea",
		JobID:            jobID,
		StorageNodeID:    "node-a",
		TargetID:         "target-a",
		RemotePath:       remote,
		CompressedSHA256: hex.EncodeToString(sum[:]),
		CompressedBytes:  int64(len(content)),
		CreatedAt:        started.Unix(),
		Status:           model.ContentBackupStatusProcessing,
	}
	lease := model.ContentBackupLease{
		SiteID:        "sitea",
		JobID:         jobID,
		StorageNodeID: "node-a",
		Owner:         "owner-a",
		Token:         token,
		Generation:    1,
		Until:         time.Now().Add(3 * time.Minute),
	}
	return job, lease
}

func cbNewStore(t *testing.T, srv *fakeFTPS, pin string, mutate func(*FTPSTarget)) *FTPSRemoteStore {
	t.Helper()
	target := FTPSTarget{
		TargetID:         "target-a",
		SiteID:           "sitea",
		Host:             "127.0.0.1",
		Port:             srv.port,
		Username:         "cbuser",
		Password:         "cbpass",
		CertSHA256:       pin,
		ConnectTimeout:   3 * time.Second,
		OperationTimeout: 10 * time.Second,
	}
	if mutate != nil {
		mutate(&target)
	}
	store, err := NewFTPSRemoteStore(target)
	if err != nil {
		t.Fatalf("NewFTPSRemoteStore: %v", err)
	}
	return store
}

func cbWaitForEvent(t *testing.T, srv *fakeFTPS, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if srv.hasEvent(substr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event %q not seen within %s (events: %v)", substr, timeout, srv.eventSnapshot())
}

func TestContentBackupFTPSConstructorValidation(t *testing.T) {
	validPin := strings.Repeat("ab", 32)
	valid := FTPSTarget{
		TargetID: "target-a", SiteID: "sitea", Host: "127.0.0.1", Port: 21,
		Username: "u", Password: "p", CertSHA256: validPin,
	}
	if _, err := NewFTPSRemoteStore(valid); err != nil {
		t.Fatalf("valid target must be accepted, got %v", err)
	}

	badPins := []string{"", "abc", strings.Repeat("AB", 32), strings.Repeat("z9", 32), strings.Repeat("ab", 31)}
	for _, pin := range badPins {
		cfg := valid
		cfg.CertSHA256 = pin
		_, err := NewFTPSRemoteStore(cfg)
		if !errors.Is(err, ErrFTPSInvalidPin) {
			t.Fatalf("pin %q: want ErrFTPSInvalidPin, got %v", pin, err)
		}
	}

	badTargets := []struct {
		name   string
		mutate func(*FTPSTarget)
	}{
		{"empty host", func(c *FTPSTarget) { c.Host = "" }},
		{"zero port", func(c *FTPSTarget) { c.Port = 0 }},
		{"port too large", func(c *FTPSTarget) { c.Port = 65536 }},
		{"empty username", func(c *FTPSTarget) { c.Username = "" }},
		{"empty target id", func(c *FTPSTarget) { c.TargetID = "" }},
		{"empty site id", func(c *FTPSTarget) { c.SiteID = "" }},
		{"bad site label", func(c *FTPSTarget) { c.SiteID = "Site A!" }},
	}
	for _, tc := range badTargets {
		cfg := valid
		tc.mutate(&cfg)
		if _, err := NewFTPSRemoteStore(cfg); err == nil {
			t.Fatalf("%s: want error, got nil", tc.name)
		}
	}
}

func TestContentBackupFTPSHappyPathAtomicCompletion(t *testing.T) {
	content := cbTestContent(64 * 1024)
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("PutVerified: %v", err)
	}

	got, ok := srv.fileSnapshot(job.RemotePath)
	if !ok || !bytes.Equal(got, content) {
		t.Fatalf("final file must exist with exact content (ok=%v equal=%v)", ok, bytes.Equal(got, content))
	}
	if paths := srv.listPaths(); len(paths) != 1 || paths[0] != job.RemotePath {
		t.Fatalf("only the final file may remain, got %v", paths)
	}
	tempPath, err := contentbackup.TempRemotePath(job.SiteID, job.JobID, cbTestLeaseToken, ".uploading")
	if err != nil {
		t.Fatalf("temp path: %v", err)
	}
	if strings.Contains(tempPath, path.Dir(job.RemotePath)) {
		t.Fatalf("temp %q must not sit in the archive directory %q", tempPath, path.Dir(job.RemotePath))
	}
	if !srv.hasEvent("STOR-COMPLETED " + tempPath) {
		t.Fatalf("STOR must target the job+lease-token temp name, events: %v", srv.eventSnapshot())
	}
	if !srv.hasEvent("RETR-COMPLETED " + tempPath) {
		t.Fatalf("verification RETR of the temp file is required, events: %v", srv.eventSnapshot())
	}
	if !srv.hasEvent("RNFR "+tempPath) || !srv.hasEvent("RNTO "+job.RemotePath) {
		t.Fatalf("RNFR/RNTO to the final name is required, events: %v", srv.eventSnapshot())
	}
	// Archive is /site/date/shard (3) and incoming repeats /site then adds .incoming/shard (3).
	if n := srv.countEvents("MKD /"); n != 6 {
		t.Fatalf("want 6 MKD segments, got %d: %v", n, srv.eventSnapshot())
	}
	if !srv.hasEvent("EPSV") {
		t.Fatalf("expected EPSV to be attempted, events: %v", srv.eventSnapshot())
	}
	// 首次认领不可能有上一次租约的临时文件，所以不列目录：在 FTPS 上一次 MLSD 是
	// 一整条数据连接加一次 TLS 握手。
	if srv.hasEvent("MLSD") {
		t.Fatalf("a first attempt must not list the incoming shard, events: %v", srv.eventSnapshot())
	}
	// 会话回池，不再每个文件 QUIT 一次；QUIT 只在 store 关闭时发生。
	if srv.hasEvent("QUIT") {
		t.Fatalf("a pooled session must not QUIT after each upload, events: %v", srv.eventSnapshot())
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	cbWaitForEvent(t, srv, "QUIT", 3*time.Second)
}

// 连接池的证据：第二个文件不再重付 TCP + AUTH TLS + USER/PASS + FEAT/TYPE/PBSZ/PROT。
func TestContentBackupFTPSReusesLoggedInSession(t *testing.T) {
	content := cbTestContent(2048)
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)

	for i := 0; i < 3; i++ {
		job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
		if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
			t.Fatalf("PutVerified %d: %v", i, err)
		}
	}
	if n := srv.countEvents("USER cbuser"); n != 1 {
		t.Fatalf("three uploads must share one logged-in control connection, got %d logins: %v", n, srv.eventSnapshot())
	}
	if n := srv.countEvents("AUTH TLS"); n != 1 {
		t.Fatalf("control TLS must be negotiated once, got %d: %v", n, srv.eventSnapshot())
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	cbWaitForEvent(t, srv, "QUIT", 3*time.Second)
}

func TestContentBackupFTPSDirCacheAndExistingDirConfirm(t *testing.T) {
	content := cbTestContent(4096)
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)

	job1, lease1 := cbTestJobAndLease(t, content, cbTestLeaseToken)
	if err := store.PutVerified(context.Background(), job1, lease1, bytes.NewReader(content)); err != nil {
		t.Fatalf("first PutVerified: %v", err)
	}
	mkdAfterFirst := srv.countEvents("MKD /")

	job2, lease2 := cbTestJobAndLease(t, content, cbTestLeaseToken)
	// Same archive directory and incoming shard, so a second upload has nothing new to create.
	job2.JobID = job1.JobID[:2] + job2.JobID[2:]
	job2.RemotePath = path.Dir(job1.RemotePath) + "/" + job2.JobID + contentbackup.RemoteFileExtension
	lease2.JobID = job2.JobID
	if err := store.PutVerified(context.Background(), job2, lease2, bytes.NewReader(content)); err != nil {
		t.Fatalf("second PutVerified: %v", err)
	}
	if n := srv.countEvents("MKD /"); n != mkdAfterFirst {
		t.Fatalf("cached directory must skip MKD, before=%d after=%d", mkdAfterFirst, n)
	}

	// A fresh store has a cold cache: MKD answers 550 "already exists" and the
	// adapter must confirm with CWD instead of trusting the 550 blindly.
	cold := cbNewStore(t, srv, srv.pin, nil)
	job3, lease3 := cbTestJobAndLease(t, content, cbTestLeaseToken)
	cwdBefore := srv.countEvents("CWD /sitea")
	if err := cold.PutVerified(context.Background(), job3, lease3, bytes.NewReader(content)); err != nil {
		t.Fatalf("cold-cache PutVerified: %v", err)
	}
	if srv.countEvents("CWD /sitea") <= cwdBefore {
		t.Fatalf("MKD 550 must be confirmed with CWD, events: %v", srv.eventSnapshot())
	}
}

func TestContentBackupFTPSMKD550DeniedIsNotTreatedAsExists(t *testing.T) {
	content := cbTestContent(2048)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.mkdDenied = true })
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSPermission) {
		t.Fatalf("want ErrFTPSPermission, got %v", err)
	}
	if srv.hasEvent("STOR") || len(srv.listPaths()) != 0 {
		t.Fatalf("nothing may be stored when the directory is unusable: %v", srv.eventSnapshot())
	}
}

func TestContentBackupFTPSWrongPinRefusedNoPlaintextFallback(t *testing.T) {
	content := cbTestContent(2048)
	srv := cbStartFTPS(t, nil)
	_, otherPin := cbGenerateCert(t, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	store := cbNewStore(t, srv, otherPin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSCertificate) {
		t.Fatalf("want ErrFTPSCertificate, got %v", err)
	}
	cbWaitForEvent(t, srv, "CONTROL-HANDSHAKE-FAILED", 3*time.Second)
	if srv.hasEvent("USER ") || srv.hasEvent("STOR") {
		t.Fatalf("client must never continue after pin failure, events: %v", srv.eventSnapshot())
	}
	if len(srv.listPaths()) != 0 {
		t.Fatalf("no file may exist: %v", srv.listPaths())
	}
}

func TestContentBackupFTPSExpiredCertRefusedDespiteCorrectPin(t *testing.T) {
	content := cbTestContent(2048)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) {
		cert, _ := cbGenerateCert(t, time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour))
		cfg.cert = cert
	})
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSCertificate) {
		t.Fatalf("want ErrFTPSCertificate, got %v", err)
	}
	if !strings.Contains(err.Error(), "validity") && !strings.Contains(err.Error(), "expired") {
		t.Logf("note: error text does not mention validity window: %v", err)
	}
	cbWaitForEvent(t, srv, "CONTROL-HANDSHAKE-FAILED", 3*time.Second)
	if srv.hasEvent("USER ") {
		t.Fatalf("expired certificate must be rejected even with a matching pin: %v", srv.eventSnapshot())
	}
}

func TestContentBackupFTPSAuthFailure(t *testing.T) {
	content := cbTestContent(2048)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.refuseAuth = true })
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSAuth) {
		t.Fatalf("want ErrFTPSAuth, got %v", err)
	}
	if srv.hasEvent("STOR") || len(srv.listPaths()) != 0 {
		t.Fatalf("nothing may be stored after auth failure: %v", srv.eventSnapshot())
	}
}

func TestContentBackupFTPSProtRefusedRejectsConnection(t *testing.T) {
	content := cbTestContent(2048)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.refuseProt = true })
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSCertificate) {
		t.Fatalf("refusing PROT P must abort the connection with ErrFTPSCertificate, got %v", err)
	}
	if srv.hasEvent("STOR") || len(srv.listPaths()) != 0 {
		t.Fatalf("no data may move without data-channel protection: %v", srv.eventSnapshot())
	}
}

func TestContentBackupFTPSPlaintextDataChannelRefused(t *testing.T) {
	content := cbTestContent(8192)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.plaintextData = true })
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if err == nil {
		t.Fatal("plaintext data channel must fail the upload")
	}
	if errors.Is(err, ErrFTPSConflict) || errors.Is(err, ErrFTPSHashMismatch) {
		t.Fatalf("unexpected classification %v", err)
	}
	cbWaitForEvent(t, srv, "DATA-TLS-CLIENTHELLO-DISCARDED", 3*time.Second)
	if srv.hasEvent("DATA-PLAINTEXT-BODY") {
		t.Fatal("client must never send body bytes over a plaintext data channel")
	}
	if len(srv.listPaths()) != 0 {
		t.Fatalf("server must not have stored anything: %v", srv.listPaths())
	}
}

func TestContentBackupFTPSRetrDigestMismatchNoRename(t *testing.T) {
	content := cbTestContent(16 * 1024)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.corruptAfterSTOR = true })
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSHashMismatch) {
		t.Fatalf("want ErrFTPSHashMismatch, got %v", err)
	}
	if srv.hasEvent("RNTO") || srv.hasEvent("RENAMED") {
		t.Fatalf("RNTO must not run after a digest mismatch, events: %v", srv.eventSnapshot())
	}
	if len(srv.listPaths()) != 0 {
		t.Fatalf("corrupted temp file must be deleted, remaining: %v", srv.listPaths())
	}
}

func TestContentBackupFTPSFinalSameDigestIdempotentSuccess(t *testing.T) {
	content := cbTestContent(16 * 1024)
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
	srv.seedFile(job.RemotePath, content)

	counted := &cbCountingReader{r: bytes.NewReader(content)}
	if err := store.PutVerified(context.Background(), job, lease, counted); err != nil {
		t.Fatalf("same digest must be an idempotent success, got %v", err)
	}
	if srv.hasEvent("STOR") || srv.hasEvent("RNTO") || srv.hasEvent("RENAMED") {
		t.Fatalf("existing file with matching digest must not be rewritten: %v", srv.eventSnapshot())
	}
	if got, ok := srv.fileSnapshot(job.RemotePath); !ok || !bytes.Equal(got, content) {
		t.Fatal("final file must stay untouched")
	}
	if counted.n != 0 {
		t.Fatalf("local source must not be consumed, read %d bytes", counted.n)
	}
}

func TestContentBackupFTPSFinalDifferentDigestConflict(t *testing.T) {
	content := cbTestContent(16 * 1024)
	other := cbTestContent(16*1024 + 3)
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
	srv.seedFile(job.RemotePath, other)

	counted := &cbCountingReader{r: bytes.NewReader(content)}
	err := store.PutVerified(context.Background(), job, lease, counted)
	if !errors.Is(err, ErrFTPSConflict) {
		t.Fatalf("want ErrFTPSConflict, got %v", err)
	}
	got, ok := srv.fileSnapshot(job.RemotePath)
	if !ok || !bytes.Equal(got, other) {
		t.Fatal("conflicting remote file must never be overwritten")
	}
	if srv.hasEvent("STOR") || srv.hasEvent("RNTO") {
		t.Fatalf("no upload or rename may happen on conflict: %v", srv.eventSnapshot())
	}
	if counted.n != 0 {
		t.Fatalf("local source must be preserved unconsumed, read %d bytes", counted.n)
	}
}

func TestContentBackupFTPSRenameRefusedExistingSameDigest(t *testing.T) {
	content := cbTestContent(8192)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) {
		cfg.rnToConflict = func(s *fakeFTPS, from, to string) bool {
			data, _ := s.fileSnapshot(from)
			s.seedFile(to, data)
			return true
		}
	})
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("refused RNTO with matching final content must resolve to idempotent success, got %v", err)
	}
	if !srv.hasEvent("RNTO-REFUSED-RACE ") {
		t.Fatalf("the race hook must have refused the rename: %v", srv.eventSnapshot())
	}
	got, ok := srv.fileSnapshot(job.RemotePath)
	if !ok || !bytes.Equal(got, content) {
		t.Fatal("final file must hold the verified content")
	}
	if paths := srv.listPaths(); len(paths) != 1 {
		t.Fatalf("temp file must be deleted after the race resolves, got %v", paths)
	}
}

func TestContentBackupFTPSRenameRefusedExistingDifferentDigest(t *testing.T) {
	content := cbTestContent(8192)
	other := cbTestContent(9000)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) {
		cfg.rnToConflict = func(s *fakeFTPS, from, to string) bool {
			s.seedFile(to, other)
			return true
		}
	})
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSConflict) {
		t.Fatalf("want ErrFTPSConflict, got %v", err)
	}
	got, ok := srv.fileSnapshot(job.RemotePath)
	if !ok || !bytes.Equal(got, other) {
		t.Fatal("the racing final file must not be overwritten")
	}
	if paths := srv.listPaths(); len(paths) != 1 {
		t.Fatalf("our temp file must be cleaned up, got %v", paths)
	}
}

func TestContentBackupFTPSUnsupportedRenameStopsProtocol(t *testing.T) {
	content := cbTestContent(8192)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.rejectRename = true })
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
	if !errors.Is(err, ErrFTPSUnsupported) {
		t.Fatalf("want ErrFTPSUnsupported when rename semantics are missing, got %v", err)
	}
	if _, ok := srv.fileSnapshot(job.RemotePath); ok {
		t.Fatal("no final file may be claimed without rename support")
	}
	if len(srv.listPaths()) != 0 {
		t.Fatalf("temp file must be cleaned up, got %v", srv.listPaths())
	}
}

func TestContentBackupFTPSCancelClosesAllConnections(t *testing.T) {
	content := cbTestContent(8192)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.hangPhase = "stor" })
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	start := time.Now()
	go func() { errCh <- store.PutVerified(ctx, job, lease, bytes.NewReader(content)) }()

	cbWaitForEvent(t, srv, "HANG-STOR", 5*time.Second)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PutVerified did not return after cancellation")
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("cancellation took too long: %s", elapsed)
	}
	select {
	case <-srv.controlClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("control connection was not closed after cancellation")
	}
	select {
	case <-srv.dataClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("data connection was not closed after cancellation")
	}
}

func TestContentBackupFTPSTimeouts(t *testing.T) {
	content := cbTestContent(4096)
	cases := []struct {
		name string
		hang string
	}{
		{"greeting hang", "greeting"},
		{"auth tls hang", "auth-tls"},
		{"login hang", "login"},
		{"stor hang", "stor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.hangPhase = tc.hang })
			store := cbNewStore(t, srv, srv.pin, func(target *FTPSTarget) {
				target.OperationTimeout = 700 * time.Millisecond
				target.ConnectTimeout = 500 * time.Millisecond
			})
			job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
			start := time.Now()
			err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
			elapsed := time.Since(start)
			if !errors.Is(err, ErrFTPSTimeout) {
				t.Fatalf("want ErrFTPSTimeout, got %v", err)
			}
			if elapsed > 5*time.Second {
				t.Fatalf("timeout not enforced promptly: %s", elapsed)
			}
		})
	}
}

func TestContentBackupFTPSStaleTempSweepScope(t *testing.T) {
	content := cbTestContent(4096)
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
	otherJob, otherLease := cbTestJobAndLease(t, content, "ffffffffffffffffffffffffffffffff")

	incoming, err := contentbackup.IncomingDir(job.SiteID, job.JobID)
	if err != nil {
		t.Fatalf("incoming dir: %v", err)
	}
	staleTemp := incoming + "/" + job.JobID + ".deadbeefdeadbeefdeadbeefdeadbeef.uploading"
	otherTemp := incoming + "/" + otherJob.JobID + "." + otherLease.Token + ".uploading"
	archiveDir := path.Dir(job.RemotePath)
	archiveStale := archiveDir + "/" + job.JobID + ".deadbeefdeadbeefdeadbeefdeadbeef.uploading"
	unrelated := archiveDir + "/unrelated.json.gz"
	srv.seedFile(staleTemp, []byte("stale"))
	srv.seedFile(otherTemp, []byte("other job temp"))
	srv.seedFile(archiveStale, []byte("left in the archive directory"))
	srv.seedFile(unrelated, []byte("unrelated content"))

	// 上一次租约留下的临时文件只可能出现在重试上，所以清扫也只在重试时做。
	lease.Generation = 2
	if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("PutVerified: %v", err)
	}
	if _, ok := srv.fileSnapshot(staleTemp); ok {
		t.Fatal("stale temp of the same job (expired lease token) must be deleted")
	}
	if _, ok := srv.fileSnapshot(archiveStale); !ok {
		t.Fatal("a leftover temp in the archive directory must stay there; that directory is no longer listed")
	}
	if _, ok := srv.fileSnapshot(otherTemp); !ok {
		t.Fatal("temp files of other jobs must never be touched")
	}
	if got, ok := srv.fileSnapshot(unrelated); !ok || string(got) != "unrelated content" {
		t.Fatal("unrelated remote files must never be touched")
	}
	if got, ok := srv.fileSnapshot(job.RemotePath); !ok || !bytes.Equal(got, content) {
		t.Fatal("final file must be uploaded correctly")
	}
}

func TestContentBackupFTPSExpiredWorkerRace(t *testing.T) {
	content := cbTestContent(16 * 1024)

	t.Run("stale worker temp is cleaned by the current worker", func(t *testing.T) {
		srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.hangPhase = "retr" })
		stale := cbNewStore(t, srv, srv.pin, nil)
		job, staleLease := cbTestJobAndLease(t, content, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- stale.PutVerified(ctx, job, staleLease, bytes.NewReader(content)) }()
		cbWaitForEvent(t, srv, "HANG-RETR", 5*time.Second)
		cancel()
		select {
		case err := <-errCh:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("stale worker want context.Canceled, got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("stale worker did not stop after cancellation")
		}
		staleTemp, pathErr := contentbackup.TempRemotePath(job.SiteID, job.JobID, staleLease.Token, ".uploading")
		if pathErr != nil {
			t.Fatalf("temp path: %v", pathErr)
		}
		if _, ok := srv.fileSnapshot(staleTemp); !ok {
			t.Fatalf("test setup broken: stale temp %s should remain on the server", staleTemp)
		}

		srv.setHang("")
		current := cbNewStore(t, srv, srv.pin, nil)
		_, currentLease := cbTestJobAndLease(t, content, cbTestLeaseToken)
		currentLease.JobID = job.JobID
		currentLease.SiteID = job.SiteID
		// 接手一个租约过期的任务，必然是第二代及以后。
		currentLease.Generation = staleLease.Generation + 1
		job2 := job
		if err := current.PutVerified(context.Background(), job2, currentLease, bytes.NewReader(content)); err != nil {
			t.Fatalf("current worker PutVerified: %v", err)
		}
		if _, ok := srv.fileSnapshot(staleTemp); ok {
			t.Fatal("current worker must delete the stale lease temp of the same job")
		}
		if got, ok := srv.fileSnapshot(job.RemotePath); !ok || !bytes.Equal(got, content) {
			t.Fatal("final file must hold the correct content")
		}
	})

	t.Run("concurrent workers with different tokens converge", func(t *testing.T) {
		srv := cbStartFTPS(t, nil)
		job, leaseA := cbTestJobAndLease(t, content, cbTestLeaseToken)
		_, leaseB := cbTestJobAndLease(t, content, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
		leaseB.JobID = job.JobID
		leaseB.SiteID = job.SiteID
		storeA := cbNewStore(t, srv, srv.pin, nil)
		storeB := cbNewStore(t, srv, srv.pin, nil)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs[0] = storeA.PutVerified(context.Background(), job, leaseA, bytes.NewReader(content))
		}()
		go func() {
			defer wg.Done()
			errs[1] = storeB.PutVerified(context.Background(), job, leaseB, bytes.NewReader(content))
		}()
		wg.Wait()

		if errs[0] != nil && errs[1] != nil {
			t.Fatalf("identical-content race must let at least one worker succeed: %v / %v", errs[0], errs[1])
		}
		for i, err := range errs {
			if errors.Is(err, ErrFTPSConflict) || errors.Is(err, ErrFTPSHashMismatch) {
				t.Fatalf("worker %d must not see a conflict for identical content: %v", i, err)
			}
		}
		if got, ok := srv.fileSnapshot(job.RemotePath); !ok || !bytes.Equal(got, content) {
			t.Fatal("final file must exist with the exact content")
		}
		if paths := srv.listPaths(); len(paths) != 1 || paths[0] != job.RemotePath {
			t.Fatalf("no temp files may survive the race, got %v", paths)
		}
	})
}

func TestContentBackupFTPSOpenLifecycle(t *testing.T) {
	content := cbTestContent(32 * 1024)

	t.Run("read all and close closes data and control", func(t *testing.T) {
		srv := cbStartFTPS(t, nil)
		store := cbNewStore(t, srv, srv.pin, nil)
		job, _ := cbTestJobAndLease(t, content, cbTestLeaseToken)
		srv.seedFile(job.RemotePath, content)

		rc, err := store.Open(context.Background(), job)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Fatal("downloaded bytes differ")
		}
		if err := rc.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		select {
		case <-srv.controlClosed:
		case <-time.After(3 * time.Second):
			t.Fatal("Close must terminate the control connection")
		}
		if err := rc.Close(); err != nil {
			t.Fatalf("Close must be idempotent, got %v", err)
		}
	})

	t.Run("missing remote file", func(t *testing.T) {
		srv := cbStartFTPS(t, nil)
		store := cbNewStore(t, srv, srv.pin, nil)
		job, _ := cbTestJobAndLease(t, content, cbTestLeaseToken)
		_, err := store.Open(context.Background(), job)
		if !errors.Is(err, ErrFTPSMissing) {
			t.Fatalf("want ErrFTPSMissing, got %v", err)
		}
	})

	t.Run("cancel during streaming kills the connection", func(t *testing.T) {
		big := cbTestContent(1024 * 1024)
		srv := cbStartFTPS(t, nil)
		store := cbNewStore(t, srv, srv.pin, nil)
		job, _ := cbTestJobAndLease(t, big, cbTestLeaseToken)
		srv.seedFile(job.RemotePath, big)

		ctx, cancel := context.WithCancel(context.Background())
		rc, err := store.Open(ctx, job)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		head := make([]byte, 16)
		if _, err := io.ReadFull(rc, head); err != nil {
			t.Fatalf("first read: %v", err)
		}
		cancel()
		deadline := time.Now().Add(3 * time.Second)
		var readErr error
		for time.Now().Before(deadline) {
			if _, readErr = io.ReadAll(rc); readErr != nil {
				break
			}
		}
		if readErr == nil {
			t.Fatal("reads must fail after the context is cancelled")
		}
		rc.Close()
		select {
		case <-srv.controlClosed:
		case <-time.After(3 * time.Second):
			t.Fatal("control connection must die on cancellation")
		}
	})
}

func TestContentBackupFTPSJobBindingChecks(t *testing.T) {
	content := cbTestContent(1024)
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)

	cases := []struct {
		name        string
		openInvalid bool
		mutate      func(*model.ContentBackupJob, *model.ContentBackupLease)
	}{
		{"target mismatch", true, func(j *model.ContentBackupJob, l *model.ContentBackupLease) { j.TargetID = "other-target" }},
		{"site prefix mismatch", true, func(j *model.ContentBackupJob, l *model.ContentBackupLease) {
			j.RemotePath = strings.Replace(j.RemotePath, "/sitea/", "/siteb/", 1)
		}},
		{"basename mismatch", true, func(j *model.ContentBackupJob, l *model.ContentBackupLease) {
			j.RemotePath = path.Join(path.Dir(j.RemotePath), "someone-elses.json.gz")
		}},
		{"non canonical path", true, func(j *model.ContentBackupJob, l *model.ContentBackupLease) {
			j.RemotePath = "/sitea//2026-09-15/" + path.Base(j.RemotePath)
		}},
		{"bad compressed sha", true, func(j *model.ContentBackupJob, l *model.ContentBackupLease) { j.CompressedSHA256 = "xyz" }},
		{"lease job mismatch", false, func(j *model.ContentBackupJob, l *model.ContentBackupLease) { l.JobID = contentbackup.NewJobID() }},
		{"lease token path traversal", false, func(j *model.ContentBackupJob, l *model.ContentBackupLease) { l.Token = "../evil" }},
	}
	for _, tc := range cases {
		job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
		tc.mutate(&job, &lease)
		err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content))
		if !errors.Is(err, ErrFTPSInvalidJob) {
			t.Fatalf("%s: want ErrFTPSInvalidJob, got %v", tc.name, err)
		}
		if tc.openInvalid {
			if _, err := store.Open(context.Background(), job); !errors.Is(err, ErrFTPSInvalidJob) {
				t.Fatalf("%s: Open must enforce the same binding, got %v", tc.name, err)
			}
		}
	}
	if n := len(srv.eventSnapshot()); n != 0 {
		t.Fatalf("binding failures must be rejected before any connection, saw %d events: %v", n, srv.eventSnapshot())
	}
}

func TestContentBackupFTPSCancelledContextRejectedUpfront(t *testing.T) {
	content := cbTestContent(1024)
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := store.PutVerified(ctx, job, lease, bytes.NewReader(content))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if n := len(srv.eventSnapshot()); n != 0 {
		t.Fatalf("no connection may be attempted, saw events %v", srv.eventSnapshot())
	}
}

func TestContentBackupFTPSPassiveFallback(t *testing.T) {
	content := cbTestContent(8192)
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.refuseEPSV = true })
	store := cbNewStore(t, srv, srv.pin, nil)
	job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)

	if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
		t.Fatalf("PASV fallback must still complete the upload, got %v", err)
	}
	if !srv.hasEvent("EPSV") || !srv.hasEvent("PASV") {
		t.Fatalf("expected EPSV attempt then PASV fallback: %v", srv.eventSnapshot())
	}
	got, ok := srv.fileSnapshot(job.RemotePath)
	if !ok || !bytes.Equal(got, content) {
		t.Fatal("final file must be correct under PASV")
	}
}

func TestContentBackupFTPSBandwidthLimiter(t *testing.T) {
	limiter := NewBandwidthLimiter(2000)
	if limiter == nil {
		t.Fatal("positive rate must build a limiter")
	}
	if NewBandwidthLimiter(0) != nil || NewBandwidthLimiter(-5) != nil {
		t.Fatal("non-positive rate must disable limiting (nil)")
	}
	var nilLimiter *BandwidthLimiter
	if err := nilLimiter.Wait(context.Background(), 1<<20); err != nil {
		t.Fatalf("nil limiter must be a no-op, got %v", err)
	}

	ctx := context.Background()
	start := time.Now()
	if err := limiter.Wait(ctx, 1500); err != nil {
		t.Fatalf("first wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("burst budget must make the first wait immediate, took %s", elapsed)
	}
	start = time.Now()
	if err := limiter.Wait(ctx, 1500); err != nil {
		t.Fatalf("second wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Fatalf("second wait must be paced (~500ms at 2000 B/s), took %s", elapsed)
	}

	cancelCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := limiter.Wait(cancelCtx, 1<<20); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait must respect context, got %v", err)
	}

	t.Run("stor and retr share one limiter", func(t *testing.T) {
		content := cbTestContent(40 * 1024)
		srv := cbStartFTPS(t, nil)
		store := cbNewStore(t, srv, srv.pin, func(target *FTPSTarget) {
			target.Limiter = NewBandwidthLimiter(40000)
		})
		job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
		start := time.Now()
		if err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content)); err != nil {
			t.Fatalf("PutVerified: %v", err)
		}
		// 40 KiB up + 40 KiB down through one 40000 B/s budget with a 1s burst
		// must take at least ~1s; keep the floor loose to avoid flakes.
		if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
			t.Fatalf("STOR+RETR must share the byte limiter, took only %s", elapsed)
		}
	})
}

func TestContentBackupFTPSLocalSourceFailures(t *testing.T) {
	content := cbTestContent(16 * 1024)

	t.Run("short source", func(t *testing.T) {
		srv := cbStartFTPS(t, nil)
		store := cbNewStore(t, srv, srv.pin, nil)
		job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
		err := store.PutVerified(context.Background(), job, lease, bytes.NewReader(content[:len(content)/2]))
		if !errors.Is(err, ErrFTPSLocalSource) {
			t.Fatalf("want ErrFTPSLocalSource, got %v", err)
		}
		if _, ok := srv.fileSnapshot(job.RemotePath); ok {
			t.Fatal("no final file may be created from a short source")
		}
		if len(srv.listPaths()) != 0 {
			t.Fatalf("temp file must be cleaned up, got %v", srv.listPaths())
		}
	})

	t.Run("failing source", func(t *testing.T) {
		srv := cbStartFTPS(t, nil)
		store := cbNewStore(t, srv, srv.pin, nil)
		job, lease := cbTestJobAndLease(t, content, cbTestLeaseToken)
		err := store.PutVerified(context.Background(), job, lease, &cbFailingReader{})
		if !errors.Is(err, ErrFTPSLocalSource) {
			t.Fatalf("want ErrFTPSLocalSource, got %v", err)
		}
		if _, ok := srv.fileSnapshot(job.RemotePath); ok {
			t.Fatal("no final file may be created from a failing source")
		}
	})
}

// cbJobInShard is a fresh job id whose incoming shard is shard.
func cbJobInShard(shard string) string {
	return shard + contentbackup.NewJobID()[2:]
}

func cbIncomingTempPath(t *testing.T, jobID, token string) string {
	t.Helper()
	p, err := contentbackup.TempRemotePath("sitea", jobID, token, ftpsTempSuffix)
	if err != nil {
		t.Fatalf("temp path: %v", err)
	}
	return p
}

// cbForeignIncomingNames sit in an incoming shard but are nothing PutVerified could have
// written, so a listing must never hand them to the reaper as delete targets.
func cbForeignIncomingNames(jobID string) []string {
	return []string{
		"notes.txt",
		jobID + contentbackup.RemoteFileExtension,
		jobID + "." + cbTestLeaseToken + ".partial",
		jobID + ".short" + ftpsTempSuffix,
		jobID + ".bad token!" + ftpsTempSuffix,
		strings.ToUpper(jobID) + "." + cbTestLeaseToken + ftpsTempSuffix,
	}
}

var cbInvalidShards = []string{"", "a", "abc", "AB", "aB", "g0", "..", "a/", "/a", " a"}

func cbInvalidIncomingTemps() []IncomingTemp {
	jobID := cbJobInShard("ab")
	return []IncomingTemp{
		{JobID: jobID, Token: "../../../../etc/passwd"},
		{JobID: jobID, Token: "abcdefgh/ijklmnop"},
		{JobID: jobID, Token: "abcdefgh.uploading"},
		{JobID: jobID, Token: "short"},
		{JobID: jobID, Token: ""},
		{JobID: "", Token: cbTestLeaseToken},
		{JobID: strings.ToUpper(jobID), Token: cbTestLeaseToken},
		{JobID: "../" + jobID[3:], Token: cbTestLeaseToken},
	}
}

// cbTempsByKey indexes a listing by "jobID.token" so it compares as a set.
func cbTempsByKey(t *testing.T, temps []IncomingTemp) map[string]IncomingTemp {
	t.Helper()
	out := make(map[string]IncomingTemp, len(temps))
	for _, temp := range temps {
		key := temp.JobID + "." + temp.Token
		if _, dup := out[key]; dup {
			t.Fatalf("temp %s listed twice", key)
		}
		out[key] = temp
	}
	return out
}

func cbSortedKeys(temps map[string]IncomingTemp) []string {
	keys := make([]string, 0, len(temps))
	for key := range temps {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestContentBackupFTPSListIncomingKeepsOnlyOwnTempsOfTheShard(t *testing.T) {
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)
	jobA, jobB, jobC := cbJobInShard("ab"), cbJobInShard("ab"), cbJobInShard("cd")
	tokenOld := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shardDir := path.Dir(cbIncomingTempPath(t, jobA, cbTestLeaseToken))
	want := map[string]time.Time{
		jobA + "." + cbTestLeaseToken: time.Date(2026, 9, 20, 1, 2, 3, 0, time.UTC),
		jobA + "." + tokenOld:         time.Date(2026, 9, 21, 4, 5, 6, 0, time.UTC),
		jobB + "." + cbTestLeaseToken: time.Date(2026, 9, 22, 7, 8, 9, 0, time.UTC),
	}
	for key, mtime := range want {
		srv.seedFileAt(shardDir+"/"+key+ftpsTempSuffix, []byte("temp"), mtime)
	}
	for _, name := range cbForeignIncomingNames(jobA) {
		srv.seedFile(shardDir+"/"+name, []byte("foreign"))
	}
	srv.seedDir(cbIncomingTempPath(t, jobB, tokenOld)) // a directory named like a temp
	srv.seedFile(cbIncomingTempPath(t, jobC, cbTestLeaseToken), []byte("other shard"))
	archive := "/sitea/2026-09-15/ab/" + jobA + contentbackup.RemoteFileExtension
	srv.seedFile(archive, []byte("archive"))
	srv.seedFile(path.Dir(archive)+"/"+jobA+".deadbeefdeadbeef"+ftpsTempSuffix, []byte("old layout temp"))

	temps, err := store.ListIncoming(context.Background(), "ab")
	if err != nil {
		t.Fatalf("ListIncoming: %v", err)
	}
	got := cbTempsByKey(t, temps)
	if len(got) != len(want) {
		t.Fatalf("want exactly the %d temps of shard ab, got %v", len(want), cbSortedKeys(got))
	}
	for key, mtime := range want {
		temp, ok := got[key]
		if !ok {
			t.Fatalf("temp %s missing from %v", key, cbSortedKeys(got))
		}
		if !temp.ModTime.Equal(mtime) {
			t.Fatalf("temp %s ModTime = %v, want the listed %v", key, temp.ModTime, mtime)
		}
	}
	if !srv.hasEvent("MLSD " + shardDir) {
		t.Fatalf("the shard directory must be listed, events: %v", srv.eventSnapshot())
	}
	if srv.hasEvent("MLSD /sitea/2026-09-15") {
		t.Fatalf("the archive directory must never be listed, events: %v", srv.eventSnapshot())
	}

	other, err := store.ListIncoming(context.Background(), "cd")
	if err != nil {
		t.Fatalf("ListIncoming cd: %v", err)
	}
	if keys := cbSortedKeys(cbTempsByKey(t, other)); len(keys) != 1 || keys[0] != jobC+"."+cbTestLeaseToken {
		t.Fatalf("shard cd must list only its own temp, got %v", keys)
	}
	if n := srv.countEvents("USER cbuser"); n != 1 {
		t.Fatalf("listings must reuse one pooled session, got %d logins", n)
	}
}

// A server without MLSD (vsftpd) answers LIST with local wall-clock times and no year. The
// reaper must not age temps by those, so they come back undated.
func TestContentBackupFTPSListIncomingWithoutMLSDReportsNoTime(t *testing.T) {
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.listOnly = true })
	store := cbNewStore(t, srv, srv.pin, nil)
	job := cbJobInShard("ab")
	temp := cbIncomingTempPath(t, job, cbTestLeaseToken)
	srv.seedFileAt(temp, []byte("temp"), time.Now().Add(-48*time.Hour))

	temps, err := store.ListIncoming(context.Background(), "ab")
	if err != nil {
		t.Fatalf("ListIncoming: %v", err)
	}
	if len(temps) != 1 || temps[0].JobID != job || temps[0].Token != cbTestLeaseToken {
		t.Fatalf("want the one temp listed through LIST, got %v", temps)
	}
	if !temps[0].ModTime.IsZero() {
		t.Fatalf("a LIST time surfaced as %v, want no time", temps[0].ModTime)
	}
	if !srv.hasEvent("LIST " + path.Dir(temp)) {
		t.Fatal("the listing must have gone through LIST")
	}
}

func TestContentBackupFTPSListIncomingMissingShardIsEmpty(t *testing.T) {
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)

	// Nothing was ever uploaded: not even /sitea/.incoming exists.
	temps, err := store.ListIncoming(context.Background(), "00")
	if err != nil || len(temps) != 0 {
		t.Fatalf("a never-created shard must list as empty, got %v, %v", temps, err)
	}
	srv.seedFile(cbIncomingTempPath(t, cbJobInShard("ab"), cbTestLeaseToken), []byte("sibling shard"))
	temps, err = store.ListIncoming(context.Background(), "ef")
	if err != nil || len(temps) != 0 {
		t.Fatalf("a shard missing next to an existing one must list as empty, got %v, %v", temps, err)
	}
	if !srv.hasEvent("MLSD-MISSING /sitea/.incoming/00") || !srv.hasEvent("MLSD-MISSING /sitea/.incoming/ef") {
		t.Fatalf("both listings must have been answered with 550, events: %v", srv.eventSnapshot())
	}
	// The 550 is an answer, not a broken session.
	if n := srv.countEvents("USER cbuser"); n != 1 {
		t.Fatalf("a 550 listing must return its session to the pool, got %d logins", n)
	}
}

func TestContentBackupFTPSRemoveIncomingDeletesOnlyThatTemp(t *testing.T) {
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)
	jobID, siblingJob := cbJobInShard("ab"), cbJobInShard("ab")
	target := cbIncomingTempPath(t, jobID, cbTestLeaseToken)
	keep := []string{
		cbIncomingTempPath(t, jobID, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		cbIncomingTempPath(t, siblingJob, cbTestLeaseToken),
		"/sitea/2026-09-15/ab/" + jobID + contentbackup.RemoteFileExtension,
	}
	srv.seedFile(target, []byte("dead temp"))
	for _, p := range keep {
		srv.seedFile(p, []byte("keep"))
	}

	if err := store.RemoveIncoming(context.Background(), IncomingTemp{JobID: jobID, Token: cbTestLeaseToken}); err != nil {
		t.Fatalf("RemoveIncoming: %v", err)
	}
	if _, ok := srv.fileSnapshot(target); ok {
		t.Fatal("the temp must be deleted")
	}
	for _, p := range keep {
		if _, ok := srv.fileSnapshot(p); !ok {
			t.Fatalf("%s must not be touched", p)
		}
	}
	if n := srv.countEvents("DELETED "); n != 1 || !srv.hasEvent("DELETED "+target) {
		t.Fatalf("exactly the rebuilt temp path may be deleted, events: %v", srv.eventSnapshot())
	}

	// Already gone, and a shard that was never created: the fake answers both DELEs with 550.
	if err := store.RemoveIncoming(context.Background(), IncomingTemp{JobID: jobID, Token: cbTestLeaseToken}); err != nil {
		t.Fatalf("removing an already-deleted temp must not be an error, got %v", err)
	}
	if err := store.RemoveIncoming(context.Background(), IncomingTemp{JobID: cbJobInShard("ef"), Token: cbTestLeaseToken}); err != nil {
		t.Fatalf("removing a temp from a never-created shard must not be an error, got %v", err)
	}
	if n := srv.countEvents("DELE "); n != 3 {
		t.Fatalf("each remove must reach the server once, got %d DELE: %v", n, srv.eventSnapshot())
	}
	if n := srv.countEvents("USER cbuser"); n != 1 {
		t.Fatalf("removes must reuse one pooled session, got %d logins", n)
	}
}

func TestContentBackupFTPSIncomingRejectsInvalidInputBeforeConnecting(t *testing.T) {
	srv := cbStartFTPS(t, nil)
	store := cbNewStore(t, srv, srv.pin, nil)
	for _, shard := range cbInvalidShards {
		if temps, err := store.ListIncoming(context.Background(), shard); !errors.Is(err, ErrFTPSInvalidJob) || temps != nil {
			t.Fatalf("shard %q: want ErrFTPSInvalidJob, got %v, %v", shard, temps, err)
		}
	}
	for _, temp := range cbInvalidIncomingTemps() {
		if err := store.RemoveIncoming(context.Background(), temp); !errors.Is(err, ErrFTPSInvalidJob) {
			t.Fatalf("temp %+v: want ErrFTPSInvalidJob, got %v", temp, err)
		}
	}
	if n := len(srv.eventSnapshot()); n != 0 {
		t.Fatalf("invalid input must be rejected before any connection, saw %d events: %v", n, srv.eventSnapshot())
	}
}

func TestContentBackupFTPSListIncomingCancelAbortsTheListing(t *testing.T) {
	srv := cbStartFTPS(t, func(cfg *fakeFTPSCfg) { cfg.hangPhase = "mlsd" })
	store := cbNewStore(t, srv, srv.pin, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := store.ListIncoming(ctx, "ab")
		errCh <- err
	}()
	cbWaitForEvent(t, srv, "HANG-MLSD", 5*time.Second)
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListIncoming did not return after cancellation")
	}
	select {
	case <-srv.controlClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("the control connection must be closed after cancellation")
	}
}

type cbCountingReader struct {
	r io.Reader
	n int64
}

func (c *cbCountingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type cbFailingReader struct {
	sent bool
}

func (r *cbFailingReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.ErrUnexpectedEOF
	}
	r.sent = true
	copy(p, "partial")
	return 7, io.ErrUnexpectedEOF
}

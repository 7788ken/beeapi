package contentbackupworker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

func t03Spool(t *testing.T, mutate func(*contentbackup.Config)) (*SpoolManager, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "spool")
	cfg := t03Config()
	if mutate != nil {
		mutate(&cfg)
	}
	spool, err := NewSpoolManager(root, cfg)
	if err != nil {
		t.Fatalf("NewSpoolManager: %v", err)
	}
	return spool, root
}

func TestContentBackupSpoolDirectoryPermissions(t *testing.T) {
	spool, root := t03Spool(t, nil)
	for _, dir := range []string{root, spool.BodiesDir(), spool.PartsDir(), spool.QuarantineDir()} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if perm := info.Mode().Perm(); perm != 0700 {
			t.Fatalf("dir %s perm = %o, want 0700", dir, perm)
		}
	}
}

func TestContentBackupSpoolReserveReleaseCommit(t *testing.T) {
	spool, _ := t03Spool(t, nil)
	if err := spool.Reserve(1000); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	used, reserved := spool.Snapshot()
	if used != 0 || reserved != 1000 {
		t.Fatalf("after reserve used=%d reserved=%d, want 0/1000", used, reserved)
	}
	spool.Commit(1000, 400)
	used, reserved = spool.Snapshot()
	if used != 400 || reserved != 0 {
		t.Fatalf("after commit used=%d reserved=%d, want 400/0", used, reserved)
	}
	if err := spool.Reserve(500); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	spool.Release(500)
	used, reserved = spool.Snapshot()
	if used != 400 || reserved != 0 {
		t.Fatalf("after release used=%d reserved=%d, want 400/0", used, reserved)
	}
}

func TestContentBackupSpoolQuotaRefused(t *testing.T) {
	spool, _ := t03Spool(t, func(c *contentbackup.Config) { c.MaxSpoolMB = 1 })
	err := spool.Reserve(2 * 1024 * 1024)
	if !errors.Is(err, ErrSpoolQuota) {
		t.Fatalf("err = %v, want ErrSpoolQuota when the estimate exceeds the budget", err)
	}
	if _, reserved := spool.Snapshot(); reserved != 0 {
		t.Fatalf("a refused reservation must not be held, reserved = %d", reserved)
	}
}

func TestContentBackupSpoolStopWatermarkRefused(t *testing.T) {
	spool, _ := t03Spool(t, func(c *contentbackup.Config) {
		c.MaxSpoolMB = 1
		c.SpoolStopPercent = 90
	})
	// 950000 bytes is under the 1 MiB hard budget but over the 90% stop watermark.
	if err := spool.Reserve(950000); !errors.Is(err, ErrSpoolQuota) {
		t.Fatalf("err = %v, want ErrSpoolQuota at the stop watermark", err)
	}
}

func TestContentBackupSpoolFreeBytesRefused(t *testing.T) {
	spool, _ := t03Spool(t, nil)
	spool.statfsFn = func(string) (volumeStats, error) {
		return volumeStats{FreeBytes: 1024, TotalBytes: 100 << 30, FreeInodes: 1 << 20, TotalInodes: 1 << 24}, nil
	}
	if err := spool.Reserve(4096); !errors.Is(err, ErrSpoolNoSpace) {
		t.Fatalf("err = %v, want ErrSpoolNoSpace when free bytes are below the floor", err)
	}
}

func TestContentBackupSpoolInodeRefused(t *testing.T) {
	spool, _ := t03Spool(t, func(c *contentbackup.Config) { c.InodeAlertPercent = 90 })
	spool.statfsFn = func(string) (volumeStats, error) {
		return volumeStats{FreeBytes: 1 << 40, TotalBytes: 1 << 40, FreeInodes: 5, TotalInodes: 100}, nil
	}
	if err := spool.Reserve(1024); !errors.Is(err, ErrSpoolNoSpace) {
		t.Fatalf("err = %v, want ErrSpoolNoSpace when inodes reach the alert threshold", err)
	}
}

func TestContentBackupSpoolScanCorrectsUsageAndSkipsSymlinks(t *testing.T) {
	spool, _ := t03Spool(t, nil)
	jobA := contentbackup.NewJobID()
	jobB := contentbackup.NewJobID()
	completePath, _ := spool.JobPath(jobA)
	partPath, _ := spool.PartPath(jobB)
	if err := os.WriteFile(completePath, make([]byte, 500), 0600); err != nil {
		t.Fatalf("write complete: %v", err)
	}
	if err := os.WriteFile(partPath, make([]byte, 300), 0600); err != nil {
		t.Fatalf("write part: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.bin")
	if err := os.WriteFile(outside, make([]byte, 9999), 0600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	link := filepath.Join(spool.BodiesDir(), contentbackup.NewJobID()+contentbackup.RemoteFileExtension)
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	result, err := spool.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.CompleteFiles) != 1 || result.CompleteFiles[0].JobID != jobA {
		t.Fatalf("complete files = %+v, want only job %s", result.CompleteFiles, jobA)
	}
	if len(result.PartFiles) != 1 || result.PartFiles[0].JobID != jobB {
		t.Fatalf("part files = %+v, want only job %s", result.PartFiles, jobB)
	}
	if result.TotalBytes != 800 {
		t.Fatalf("total bytes = %d, want 800 (the symlink target must never be followed)", result.TotalBytes)
	}
	used, reserved := spool.Snapshot()
	if used != 800 || reserved != 0 {
		t.Fatalf("after scan used=%d reserved=%d, want 800/0", used, reserved)
	}
}

func TestContentBackupSpoolPathRejectsTraversalAndNonUUID(t *testing.T) {
	spool, _ := t03Spool(t, nil)
	for _, bad := range []string{"", "not-a-uuid", "../../etc/passwd", "UPPERCASE-UUID"} {
		if _, err := spool.JobPath(bad); err == nil {
			t.Fatalf("JobPath(%q) must be rejected", bad)
		}
		if _, err := spool.PartPath(bad); err == nil {
			t.Fatalf("PartPath(%q) must be rejected", bad)
		}
	}
	good := contentbackup.NewJobID()
	jobPath, err := spool.JobPath(good)
	if err != nil {
		t.Fatalf("JobPath(valid): %v", err)
	}
	if filepath.Dir(jobPath) != spool.BodiesDir() {
		t.Fatalf("job path %q escaped the bodies dir", jobPath)
	}
}

func TestContentBackupWorstCaseSpoolBytes(t *testing.T) {
	meta := t03Meta(contentbackup.NewJobID())
	meta.Request = contentbackup.BodyMeta{CapturedBytes: 3, ObservedBytes: 3}
	meta.Response = contentbackup.BodyMeta{CapturedBytes: 3, ObservedBytes: 3}
	// raw 6 bytes -> base64 (6+2)/3*4 = 8, plus 64 KiB metadata ceiling plus gzip slack.
	want := int64(8) + int64(contentbackup.MaxMetadataBytes) + 4096
	if got := WorstCaseSpoolBytes(meta); got != want {
		t.Fatalf("WorstCaseSpoolBytes = %d, want %d", got, want)
	}
	// A full 8 MiB per side must reserve at least the 4/3 base64 expansion.
	meta.Request = contentbackup.BodyMeta{CapturedBytes: contentbackup.MaxBodyBytesPerSide, ObservedBytes: contentbackup.MaxBodyBytesPerSide}
	meta.Response = contentbackup.BodyMeta{CapturedBytes: contentbackup.MaxBodyBytesPerSide, ObservedBytes: contentbackup.MaxBodyBytesPerSide}
	got := WorstCaseSpoolBytes(meta)
	if got < 2*contentbackup.MaxBodyBytesPerSide*4/3 {
		t.Fatalf("WorstCaseSpoolBytes = %d, must cover the 4/3 base64 expansion of both sides", got)
	}
}

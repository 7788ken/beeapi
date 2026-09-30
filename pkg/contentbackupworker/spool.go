package contentbackupworker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

const (
	spoolBodiesSubdir     = "bodies"
	spoolPartsSubdir      = "parts"
	spoolQuarantineSubdir = "quarantine"
	spoolPartSuffix       = ".part"

	spoolDirPerm  = 0700
	spoolFilePerm = 0600
)

var (
	// ErrSpoolQuota means the logical spool budget or its stop watermark is exhausted;
	// persisted files are never auto-evicted to make room (design doc 4.4).
	ErrSpoolQuota = errors.New("content backup spool budget is exhausted")
	// ErrSpoolNoSpace means the host volume lacks the free bytes or inodes the safety
	// floor requires, so a new handoff is refused with 507 (design doc 7.3).
	ErrSpoolNoSpace = errors.New("content backup spool volume lacks safe free space")
	// ErrSpoolPathEscapes means a resolved path or symlink would leave the spool root.
	ErrSpoolPathEscapes = errors.New("content backup spool path escapes the spool root")
)

type volumeStats struct {
	FreeBytes   int64
	TotalBytes  int64
	FreeInodes  int64
	TotalInodes int64
}

// SpoolFile is one regular file discovered by the startup scan.
type SpoolFile struct {
	Path  string
	JobID string
	Size  int64
	// ModTime 让周期对账能区分"正在写"和"早已中断"的 .part；没有它就只能靠
	// "启动时没有在途写入"这个一次性前提，挂上 ticker 后会抢走活动文件。
	ModTime time.Time
}

// ScanResult is the corrected on-disk picture Scan rebuilds from the files themselves.
type ScanResult struct {
	CompleteFiles   []SpoolFile
	PartFiles       []SpoolFile
	QuarantineFiles []SpoolFile
	TotalBytes      int64
}

// SpoolManager is this process's accountant for the node's spool volume. All
// reserve/release/commit mutations are serialised so the process never over-commits the
// budget, and Scan corrects the counters from disk. During a blue-green switch both slots
// run one on the same volume: file names are job ids and rows are claimed by DB CAS, so
// the two never write the same file, and each reconcile pass re-reads the real usage.
type SpoolManager struct {
	root          string
	bodiesDir     string
	partsDir      string
	quarantineDir string

	mu       sync.Mutex
	used     int64
	reserved int64

	limit          int64
	stopPercent    int64
	minFreeBytes   int64
	minFreePercent int64
	inodeAlertPct  int64

	statfsFn func(path string) (volumeStats, error)
}

// NewSpoolManager creates the spool tree with the section 5.2 permissions: the body
// directory is 0700 and files are 0600, so the business process never mounts it and only
// shares the socket.
func NewSpoolManager(root string, cfg contentbackup.Config) (*SpoolManager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("content backup spool root must not be empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve content backup spool root: %w", err)
	}
	s := &SpoolManager{
		root:          absRoot,
		bodiesDir:     filepath.Join(absRoot, spoolBodiesSubdir),
		partsDir:      filepath.Join(absRoot, spoolPartsSubdir),
		quarantineDir: filepath.Join(absRoot, spoolQuarantineSubdir),
		statfsFn:      defaultDiskStats,
	}
	s.ApplyBudget(cfg)
	for _, dir := range []string{s.root, s.bodiesDir, s.partsDir, s.quarantineDir} {
		if err := os.MkdirAll(dir, spoolDirPerm); err != nil {
			return nil, fmt.Errorf("create content backup spool dir %s: %w", dir, err)
		}
		if err := os.Chmod(dir, spoolDirPerm); err != nil {
			return nil, fmt.Errorf("chmod content backup spool dir %s: %w", dir, err)
		}
	}
	return s, nil
}

// ApplyBudget makes the admission budget follow the live config. These values used to be
// read once at construction: raising max_spool_mb from 2GiB to 16GiB made the heartbeat
// report 16GiB at once while Reserve kept refusing at the old 2GiB watermark until a
// restart. Shrinking the budget only stops new admissions; persisted files stay put.
func (s *SpoolManager) ApplyBudget(cfg contentbackup.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limit = int64(cfg.MaxSpoolMB) * 1024 * 1024
	s.stopPercent = int64(cfg.SpoolStopPercent)
	s.minFreeBytes = cfg.MinFreeBytes
	s.minFreePercent = int64(cfg.MinFreePercent)
	s.inodeAlertPct = int64(cfg.InodeAlertPercent)
}

// Limit is the byte budget Reserve actually enforces; the heartbeat reports this value.
func (s *SpoolManager) Limit() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limit
}

func (s *SpoolManager) Root() string          { return s.root }
func (s *SpoolManager) BodiesDir() string     { return s.bodiesDir }
func (s *SpoolManager) PartsDir() string      { return s.partsDir }
func (s *SpoolManager) QuarantineDir() string { return s.quarantineDir }

// JobPath resolves the durable {job_id}.json.gz location, rejecting any job id that is not
// a server generated UUID so no client value can steer a path out of the spool root.
func (s *SpoolManager) JobPath(jobID string) (string, error) {
	if err := contentbackup.ValidateJobID(jobID); err != nil {
		return "", err
	}
	return s.withinRoot(s.bodiesDir, jobID+contentbackup.RemoteFileExtension)
}

func (s *SpoolManager) PartPath(jobID string) (string, error) {
	if err := contentbackup.ValidateJobID(jobID); err != nil {
		return "", err
	}
	return s.withinRoot(s.partsDir, jobID+contentbackup.RemoteFileExtension+spoolPartSuffix)
}

func (s *SpoolManager) withinRoot(dir, name string) (string, error) {
	if strings.ContainsRune(name, '/') || name == "." || name == ".." || strings.Contains(name, "..") {
		return "", fmt.Errorf("%w: %q", ErrSpoolPathEscapes, name)
	}
	full := filepath.Join(dir, name)
	rel, err := filepath.Rel(s.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrSpoolPathEscapes, full)
	}
	return full, nil
}

// Reserve pessimistically holds the worst case JSON/base64/gzip bytes for one handoff. It
// never blocks: the caller turns a refusal into 507 rather than queueing.
func (s *SpoolManager) Reserve(estimate int64) error {
	if estimate < 0 {
		return errors.New("content backup spool reservation must not be negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	projected := s.used + s.reserved + estimate
	if projected > s.limit {
		return fmt.Errorf("%w: projected %d bytes exceeds the %d byte budget", ErrSpoolQuota, projected, s.limit)
	}
	if s.stopPercent > 0 && s.limit > 0 && projected*100 >= s.limit*s.stopPercent {
		return fmt.Errorf("%w: projected %d bytes reaches the %d%% stop watermark", ErrSpoolQuota, projected, s.stopPercent)
	}

	stats, err := s.statfsFn(s.root)
	if err != nil {
		return fmt.Errorf("stat content backup spool volume: %w", err)
	}
	minFree := s.minFreeBytes
	if s.minFreePercent > 0 && stats.TotalBytes > 0 {
		if pct := stats.TotalBytes * s.minFreePercent / 100; pct > minFree {
			minFree = pct
		}
	}
	if stats.FreeBytes-estimate < minFree {
		return fmt.Errorf("%w: free %d bytes minus %d reservation is below the %d byte floor",
			ErrSpoolNoSpace, stats.FreeBytes, estimate, minFree)
	}
	if stats.TotalInodes > 0 && s.inodeAlertPct > 0 {
		usedInodes := stats.TotalInodes - stats.FreeInodes + 1
		if usedInodes*100 >= stats.TotalInodes*s.inodeAlertPct {
			return fmt.Errorf("%w: inode usage %d/%d reaches the %d%% alert threshold",
				ErrSpoolNoSpace, usedInodes, stats.TotalInodes, s.inodeAlertPct)
		}
	}

	s.reserved += estimate
	return nil
}

// Release drops a reservation that never produced a persisted file.
func (s *SpoolManager) Release(estimate int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserved -= estimate
	if s.reserved < 0 {
		s.reserved = 0
	}
}

// Commit converts a reservation into real usage once the final file size is known.
func (s *SpoolManager) Commit(estimate, actual int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserved -= estimate
	if s.reserved < 0 {
		s.reserved = 0
	}
	if actual > 0 {
		s.used += actual
	}
}

func (s *SpoolManager) Snapshot() (used, reserved int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used, s.reserved
}

// Scan walks the whole spool once, corrects the used counter from the real file sizes and
// returns the complete/partial/quarantined inventory the recovery pass consumes. Symlinks
// and anything escaping the root are skipped, never followed.
func (s *SpoolManager) Scan() (ScanResult, error) {
	var result ScanResult
	var total int64
	dirs := []struct {
		path string
		into *[]SpoolFile
	}{
		{s.bodiesDir, &result.CompleteFiles},
		{s.partsDir, &result.PartFiles},
		{s.quarantineDir, &result.QuarantineFiles},
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir.path)
		if err != nil {
			return ScanResult{}, fmt.Errorf("read content backup spool dir %s: %w", dir.path, err)
		}
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			// A symlink is never a backup we own; skip it instead of following it out of the root.
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
			name := entry.Name()
			jobID, ok := spoolJobIDFromName(name)
			if !ok {
				continue
			}
			*dir.into = append(*dir.into, SpoolFile{
				Path:    filepath.Join(dir.path, name),
				JobID:   jobID,
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
			total += info.Size()
		}
	}
	sort.Slice(result.CompleteFiles, func(i, j int) bool { return result.CompleteFiles[i].JobID < result.CompleteFiles[j].JobID })
	sort.Slice(result.PartFiles, func(i, j int) bool { return result.PartFiles[i].JobID < result.PartFiles[j].JobID })

	s.mu.Lock()
	s.used = total
	s.reserved = 0
	s.mu.Unlock()

	result.TotalBytes = total
	return result, nil
}

func spoolJobIDFromName(name string) (string, bool) {
	switch {
	case strings.HasSuffix(name, contentbackup.RemoteFileExtension+spoolPartSuffix):
		return strings.TrimSuffix(name, contentbackup.RemoteFileExtension+spoolPartSuffix), true
	case strings.HasSuffix(name, contentbackup.RemoteFileExtension):
		return strings.TrimSuffix(name, contentbackup.RemoteFileExtension), true
	}
	return "", false
}

// WorstCaseSpoolBytes estimates the largest on-disk footprint a capture can produce: the
// base64 expansion (about 4/3) of both bodies plus the envelope structure and gzip slack,
// assuming gzip gives no benefit so the reservation is never optimistic (design doc 4.5).
func WorstCaseSpoolBytes(meta contentbackup.Metadata) int64 {
	raw := meta.Request.CapturedBytes + meta.Response.CapturedBytes
	if raw < 0 {
		raw = 0
	}
	base64 := (raw + 2) / 3 * 4
	return base64 + int64(contentbackup.MaxMetadataBytes) + 4096
}

func defaultDiskStats(path string) (volumeStats, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return volumeStats{}, err
	}
	blockSize := int64(st.Bsize)
	return volumeStats{
		FreeBytes:   int64(st.Bavail) * blockSize,
		TotalBytes:  int64(st.Blocks) * blockSize,
		FreeInodes:  int64(st.Ffree),
		TotalInodes: int64(st.Files),
	}, nil
}

// fsyncDir flushes the directory entry so a rename survives a crash; without it the renamed
// file can vanish after a power loss even though its own data was synced.
func fsyncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

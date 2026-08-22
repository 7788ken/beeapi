package service

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
)

const (
	sensitiveDumpCleanInterval = 6 * time.Hour
	sensitiveDumpDeleteLease   = 5 * time.Minute
	legacySensitiveDumpBatch   = 200
)

var sensitiveDumpCleanerProcessID = common.GetUUID()

var (
	markLegacySensitiveDumpsCleaned = model.MarkSensitiveDumpsCleaned
	hasDurableSensitiveDump         = model.HasSensitiveAuditJobForDumpPath
	removeLegacySensitiveDump       = os.Remove
)

type SensitiveDumpCleaner struct {
	mu        sync.Mutex
	state     sensitiveLifecycleState
	interval  time.Duration
	clean     func()
	cancel    context.CancelFunc
	stopped   chan struct{}
	startGate <-chan struct{}
}

func NewSensitiveDumpCleaner(interval time.Duration, clean func()) *SensitiveDumpCleaner {
	return &SensitiveDumpCleaner{
		state:    sensitiveLifecycleNew,
		interval: interval,
		clean:    clean,
		stopped:  make(chan struct{}),
	}
}

var defaultSensitiveDumpCleaner = NewSensitiveDumpCleaner(sensitiveDumpCleanInterval, runSensitiveDumpCleanOnce)

// StartSensitiveDumpCleaner 启动 dump 文件清理 + 磁盘水位监控守护协程。
//
//   - 每 6 小时扫一次根目录，删 mtime 早于 SensitiveDumpRetentionDays 的 .json.gz；
//     删除后批量更新 sensitive_block_logs.dump_exists=false（用 dump_path 反查）。
//   - 每轮顺便检查根目录可用磁盘：可用空间百分比低于 SensitiveDumpDiskGuardPercent 时
//     自动把 SensitiveDumpToFile 关掉并告警，防止把磁盘灌爆。
//   - 每个实例只清理自己 NODE_NAME 对应的本地持久卷；共享 DB 不代表共享文件系统。
func StartSensitiveDumpCleaner() {
	if err := defaultSensitiveDumpCleaner.Start(); err != nil {
		common.SysError("[sensitive] dump cleaner start failed: " + err.Error())
		return
	}
	common.SysLog("[sensitive] dump cleaner started interval=" + sensitiveDumpCleanInterval.String())
}

func StopSensitiveDumpCleaner(ctx context.Context) error {
	return defaultSensitiveDumpCleaner.Stop(ctx)
}

func (c *SensitiveDumpCleaner) Start() error {
	if c == nil {
		return errors.New("sensitive dump cleaner is nil")
	}
	if c.interval <= 0 {
		return errors.New("sensitive dump cleaner interval must be positive")
	}
	if c.clean == nil {
		return errors.New("sensitive dump cleaner function is nil")
	}

	c.mu.Lock()
	switch c.state {
	case sensitiveLifecycleRunning:
		c.mu.Unlock()
		return nil
	case sensitiveLifecycleStopping, sensitiveLifecycleStopped:
		c.mu.Unlock()
		return errors.New("sensitive dump cleaner cannot be restarted")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.state = sensitiveLifecycleRunning
	c.mu.Unlock()

	go c.run(runCtx)
	return nil
}

func (c *SensitiveDumpCleaner) run(ctx context.Context) {
	defer func() {
		c.mu.Lock()
		c.state = sensitiveLifecycleStopped
		close(c.stopped)
		c.mu.Unlock()
	}()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	if c.startGate != nil {
		select {
		case <-ctx.Done():
			return
		case <-c.startGate:
		}
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	c.runRound()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			c.runRound()
		}
	}
}

func (c *SensitiveDumpCleaner) runRound() {
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError("[sensitive] dump cleaner panic recovered")
		}
	}()
	c.clean()
}

func (c *SensitiveDumpCleaner) Stop(ctx context.Context) error {
	if c == nil {
		return errors.New("sensitive dump cleaner is nil")
	}
	if ctx == nil {
		return errors.New("sensitive dump cleaner stop context is nil")
	}

	c.mu.Lock()
	switch c.state {
	case sensitiveLifecycleNew:
		c.state = sensitiveLifecycleStopped
		close(c.stopped)
		c.mu.Unlock()
		return nil
	case sensitiveLifecycleRunning:
		c.state = sensitiveLifecycleStopping
		c.cancel()
	case sensitiveLifecycleStopping:
	case sensitiveLifecycleStopped:
		c.mu.Unlock()
		return nil
	}
	stopped := c.stopped
	c.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-stopped:
		return nil
	}
}

func runSensitiveDumpCleanOnce() {
	defer func() {
		if r := recover(); r != nil {
			common.SysError("[sensitive] dump cleaner panic recovered")
		}
	}()

	root := SensitiveDumpRoot()
	guardSensitiveDumpDisk(root)

	retentionDays := setting.GetSensitiveDumpRetentionDays()
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	deleted, scanned, errs := cleanDurableSensitiveDumps(cutoff)
	legacyDeleted, legacyScanned, legacyErrors := walkAndCleanDumps(root, cutoff)
	deleted += legacyDeleted
	scanned += legacyScanned
	errs += legacyErrors
	common.SysLog("[sensitive] dump clean tick scanned=" + strconv.Itoa(scanned) +
		" deleted=" + strconv.Itoa(deleted) + " errors=" + strconv.Itoa(errs))
}

func cleanDurableSensitiveDumps(cutoff time.Time) (deleted, scanned, errs int) {
	nodeID := strings.TrimSpace(common.NodeName)
	if nodeID == "" {
		common.SysError("[sensitive] durable dump cleanup skipped: NODE_NAME is empty")
		return 0, 0, 1
	}
	for {
		now := time.Now().Unix()
		jobs, err := model.ListDeletableSensitiveAuditJobs(nodeID, cutoff.Unix(), now, 100)
		if err != nil {
			common.SysError("[sensitive] list durable dumps failed node=" + nodeID + " err=" + err.Error())
			return deleted, scanned, errs + 1
		}
		if len(jobs) == 0 {
			return
		}
		claimedAny := false
		for _, job := range jobs {
			scanned++
			leaseToken := common.GetUUID()
			claimed, err := model.TryClaimSensitiveAuditDump(
				job.JobID,
				nodeID,
				sensitiveDumpCleanerProcessID,
				leaseToken,
				cutoff.Unix(),
				now,
				now+int64(sensitiveDumpDeleteLease/time.Second),
			)
			if err != nil {
				errs++
				common.SysError("[sensitive] claim durable dump failed job_id=" + job.JobID + " err=" + err.Error())
				continue
			}
			if !claimed {
				continue
			}
			claimedAny = true
			if err := os.Remove(job.DumpPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs++
				common.SysError("[sensitive] durable dump 删除失败 job_id=" + job.JobID + " path=" + job.DumpPath + " err=" + err.Error())
				continue
			}
			if err := model.CompleteSensitiveAuditDumpDeletion(job.JobID, leaseToken, time.Now().Unix()); err != nil {
				errs++
				common.SysError("[sensitive] finalize durable dump deletion failed job_id=" + job.JobID + " err=" + err.Error())
				continue
			}
			deleted++
		}
		if !claimedAny {
			return
		}
	}
}

// walkAndCleanDumps 遍历 root，找出 mtime 早于 cutoff 的 legacy .json.gz 文件。
// 每批先把关联日志标记为 dump_exists=false；只有 DB 更新成功后才删除该批文件。
// durable job 管理的 dump 由 cleanDurableSensitiveDumps 处理，这里始终跳过。
func walkAndCleanDumps(root string, cutoff time.Time) (deleted, scanned, errs int) {
	if root == "" {
		return
	}
	if _, err := os.Stat(root); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			errs++
			common.SysError("[sensitive] dump root stat err=" + err.Error())
		}
		return
	}
	stalePaths := make([]string, 0, 64)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			errs++
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// 只处理 dump 文件，跳过其它（含写盘失败留下的 .tmp）
		if filepath.Ext(path) != ".gz" {
			return nil
		}
		scanned++
		info, statErr := d.Info()
		if statErr != nil {
			errs++
			return nil
		}
		if info.ModTime().After(cutoff) {
			return nil
		}
		durable, lookupErr := hasDurableSensitiveDump(path)
		if lookupErr != nil {
			errs++
			common.SysError("[sensitive] durable dump lookup failed path=" + path + " err=" + lookupErr.Error())
			return nil
		}
		if durable {
			return nil
		}
		stalePaths = append(stalePaths, path)
		return nil
	})

	for start := 0; start < len(stalePaths); start += legacySensitiveDumpBatch {
		end := start + legacySensitiveDumpBatch
		if end > len(stalePaths) {
			end = len(stalePaths)
		}
		batch := stalePaths[start:end]
		if err := markLegacySensitiveDumpsCleaned(batch); err != nil {
			errs++
			common.SysError("[sensitive] legacy dump DB-first 标记失败，保留该批文件 err=" + err.Error())
			continue
		}
		for _, path := range batch {
			if rmErr := removeLegacySensitiveDump(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				errs++
				common.SysError("[sensitive] legacy dump 删除失败 path=" + path + " err=" + rmErr.Error())
				continue
			}
			deleted++
		}
	}
	return
}

// guardSensitiveDumpDisk 检查 root 所在分区的可用空间，低于阈值时关闭 dump 落盘开关并告警。
func guardSensitiveDumpDisk(root string) {
	threshold := setting.GetSensitiveDumpDiskGuardPercent()
	if threshold <= 0 || threshold >= 100 {
		return
	}
	target := root
	for target != "" {
		if _, err := os.Stat(target); err == nil {
			break
		}
		parent := filepath.Dir(target)
		if parent == target {
			return
		}
		target = parent
	}
	if target == "" {
		return
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(target, &stat); err != nil {
		common.SysError("[sensitive] dump 磁盘 statfs 失败 target=" + target + " err=" + err.Error())
		return
	}
	if stat.Blocks == 0 {
		return
	}
	freePct := int(float64(stat.Bavail) / float64(stat.Blocks) * 100)
	if freePct >= threshold {
		return
	}
	if !setting.GetSensitiveDumpToFile() {
		return
	}
	setting.SetSensitiveDumpToFile(false)
	common.SysError("[sensitive] dump 磁盘可用空间不足 (" + strconv.Itoa(freePct) +
		"% < " + strconv.Itoa(threshold) + "%)，已自动关闭 SensitiveDumpToFile；本次审计将丢弃直至磁盘恢复后人工开启")
}

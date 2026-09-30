package contentbackupworker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

// 进程内运行时：一个 spool 写入器 + 按站点标签惰性构建的上传/清理/读取/对账。
// 站点标签来自配置而非环境变量，所以"先启动、后在页面填标签"必须无需重启即可工作。

type t20ConfigSource struct {
	mu  sync.Mutex
	cfg contentbackup.Config
}

func (s *t20ConfigSource) get() contentbackup.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *t20ConfigSource) set(mutate func(*contentbackup.Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutate(&s.cfg)
}

func t20WaitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestContentBackupRuntimeWritesCapturesAndBuildsSiteWorkersLazily(t *testing.T) {
	db := t03OpenDB(t)
	source := &t20ConfigSource{cfg: t03Config()}
	source.set(func(c *contentbackup.Config) { c.SiteLabel = "" }) // 尚未配置站点标签
	spoolDir := filepath.Join(t.TempDir(), "spool")

	rt, err := Start(context.Background(), RuntimeConfig{
		SpoolDir:          spoolDir,
		DB:                db,
		Config:            source.get,
		SuperviseInterval: 30 * time.Millisecond,
		Logf:              func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = rt.Shutdown(context.Background()) }()

	if rt.StorageNodeID() == "" {
		t.Fatal("node id must be derived from the spool volume")
	}
	if again, _ := LoadOrCreateNodeID(spoolDir); again != rt.StorageNodeID() {
		t.Fatalf("node id %q must be persisted in the spool, file says %q", rt.StorageNodeID(), again)
	}
	if rt.Reader() != nil {
		t.Fatal("without a site label there is nothing to read from; Reader must be nil, not a guess")
	}

	// 采集落盘不依赖站点 worker：元数据自带 site_id，写入器按它入库。
	var released atomic.Int32
	jobID := contentbackup.NewJobID()
	capture := t03Capture(t03Meta(jobID), []byte("req"), []byte("resp"))
	capture.Meta.StorageNodeID = rt.StorageNodeID()
	capture.Release = func() { released.Add(1) }
	if !rt.Enqueue(capture) {
		t.Fatal("Enqueue must accept while the queue has room")
	}
	store := model.NewContentBackupStore(db, t03SiteID)
	t20WaitFor(t, 3*time.Second, func() bool {
		_, err := store.GetJob(context.Background(), jobID)
		return err == nil
	}, "capture was not written and registered by the spool worker")
	if released.Load() != 1 {
		t.Fatalf("Release must run exactly once after the write, ran %d", released.Load())
	}
	finalPath, _ := rt.Spool().JobPath(jobID)
	if !t03FileExists(t, finalPath) {
		t.Fatal("spool file missing")
	}
	if c := rt.Counters(); c.Queued != 1 || c.Written != 1 || c.Registered != 1 || c.Rejected != 0 {
		t.Fatalf("counters = %+v", c)
	}

	// 运维在页面填了站点标签：无需重启，worker 与读取器随之出现，心跳带着节点身份入库。
	source.set(func(c *contentbackup.Config) { c.SiteLabel = t03SiteID })
	t20WaitFor(t, 3*time.Second, func() bool { return rt.Reader() != nil }, "site workers were not built after the label appeared")
	t20WaitFor(t, 3*time.Second, func() bool {
		nodes, err := store.ListNodeStatus(context.Background())
		if err != nil || len(nodes) != 1 {
			return false
		}
		n := nodes[0]
		return n.StorageNodeID == rt.StorageNodeID() && n.ProcessID == rt.ProcessID() && n.FTPSCredentialsSet && n.PendingCount+n.ProcessingCount == 1
	}, "heartbeat did not publish the node row with credentials and the pending count")

	// 标签被清空：worker 拆掉，读取器消失；已落盘文件与任务行原样保留。
	source.set(func(c *contentbackup.Config) { c.SiteLabel = "" })
	t20WaitFor(t, 3*time.Second, func() bool { return rt.Reader() == nil }, "site workers were not torn down after the label was cleared")
	if !t03FileExists(t, finalPath) {
		t.Fatal("clearing the label must not delete anything")
	}

	if err := rt.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if rt.Enqueue(t03Capture(t03Meta(contentbackup.NewJobID()), []byte("a"), []byte("b"))) {
		t.Fatal("Enqueue after Shutdown must refuse")
	}
}

func TestContentBackupRuntimeFullQueueRejectsWithoutBlocking(t *testing.T) {
	db := t03OpenDB(t)
	source := &t20ConfigSource{cfg: t03Config()}
	rt, err := Start(context.Background(), RuntimeConfig{
		SpoolDir:          filepath.Join(t.TempDir(), "spool"),
		DB:                db,
		Config:            source.get,
		QueueDepth:        1,
		SuperviseInterval: time.Hour,
		Logf:              func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 用一个阻塞的 gzip 钩子把唯一的 worker 卡住，再塞满长度为 1 的队列。
	block := make(chan struct{})
	rt.writer.hookGzipWrite = func(w io.Writer, c *contentbackup.Capture, sha string) error {
		<-block
		return contentbackup.WriteEnvelope(w, c.Meta, c.RequestChunks, c.ResponseChunks, sha)
	}
	var released atomic.Int32
	mk := func() *contentbackup.Capture {
		c := t03Capture(t03Meta(contentbackup.NewJobID()), []byte("a"), []byte("b"))
		c.Release = func() { released.Add(1) }
		return c
	}
	if !rt.Enqueue(mk()) { // worker 取走并卡住
		t.Fatal("first enqueue")
	}
	time.Sleep(50 * time.Millisecond)
	if !rt.Enqueue(mk()) { // 填满队列
		t.Fatal("second enqueue")
	}
	start := time.Now()
	if rt.Enqueue(mk()) {
		t.Fatal("a full queue must reject, never block the relay")
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("rejection took too long: Enqueue must be non-blocking")
	}
	if rt.Counters().Rejected != 1 {
		t.Fatalf("counters = %+v", rt.Counters())
	}
	close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown must drain the two queued captures: %v", err)
	}
	if released.Load() != 2 {
		t.Fatalf("both accepted captures must be released exactly once, got %d (rejected one is the caller's)", released.Load())
	}
}

// 后台把 max_spool_mb 调大以后，准入判断必须跟着变，不能等重启。以前构造期读一次：
// 心跳立刻报新上限，写入却还按旧上限拒收，ai 站因此多丢了一个多小时的备份。
func TestContentBackupRuntimeAppliesSpoolBudgetWithoutRestart(t *testing.T) {
	db := t03OpenDB(t)
	source := &t20ConfigSource{cfg: t03Config()}
	source.set(func(c *contentbackup.Config) { c.MaxSpoolMB = 64 })
	rt, err := Start(context.Background(), RuntimeConfig{
		SpoolDir:          filepath.Join(t.TempDir(), "spool"),
		DB:                db,
		Config:            source.get,
		SuperviseInterval: 30 * time.Millisecond,
		Logf:              func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = rt.Shutdown(context.Background()) }()

	if got := rt.Spool().Limit(); got != 64<<20 {
		t.Fatalf("initial admission limit = %d, want %d", got, int64(64<<20))
	}
	source.set(func(c *contentbackup.Config) { c.MaxSpoolMB = 512 })
	t20WaitFor(t, 3*time.Second, func() bool { return rt.Spool().Limit() == 512<<20 },
		"a raised max_spool_mb must reach the admission check without a restart")
}

// 保留期到了的归档索引行要在清理循环里被删掉；没到期的留着。以前保留期没人读，
// 任务表按每天几十万行增长，ai 站按当前速率约 39 天写满磁盘。
func TestContentBackupRuntimeExpiresOldIndexRowsInCleanupLoop(t *testing.T) {
	db := t03OpenDB(t)
	source := &t20ConfigSource{cfg: t03Config()}
	source.set(func(c *contentbackup.Config) {
		c.SiteLabel = "" // 先不建站点 worker，拿到节点号、造好数据再放行
		c.IndexRetentionDays = 30
	})
	rt, err := Start(context.Background(), RuntimeConfig{
		SpoolDir:          filepath.Join(t.TempDir(), "spool"),
		DB:                db,
		Config:            source.get,
		SuperviseInterval: 30 * time.Millisecond,
		Logf:              func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = rt.Shutdown(context.Background()) }()

	seed := func(jobID string, uploadedAt time.Time) {
		t.Helper()
		row := model.ContentBackupJob{
			SiteID:        t03SiteID,
			JobID:         jobID,
			StorageNodeID: rt.StorageNodeID(),
			CreatedAt:     uploadedAt.Add(-time.Minute).Unix(),
			Status:        model.ContentBackupStatusUploaded,
			CleanupState:  model.ContentBackupCleanupDone,
			UploadedAt:    uploadedAt.Unix(),
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed %s: %v", jobID, err)
		}
	}
	expired := contentbackup.NewJobID()
	fresh := contentbackup.NewJobID()
	seed(expired, time.Now().AddDate(0, 0, -40))
	seed(fresh, time.Now().AddDate(0, 0, -1))

	source.set(func(c *contentbackup.Config) { c.SiteLabel = t03SiteID })
	store := model.NewContentBackupStore(db, t03SiteID)
	t20WaitFor(t, 3*time.Second, func() bool {
		_, err := store.GetJob(context.Background(), expired)
		return err != nil
	}, "an index row past index_retention_days must be expired by the cleanup loop")
	if _, err := store.GetJob(context.Background(), fresh); err != nil {
		t.Fatalf("a row inside the retention window must stay: %v", err)
	}
}

// 一拍只清一批（100 个）、30 秒一拍，本地释放的上限就是每秒 3.3 个。上传提速到每秒 10 个
// 以后，待清理的文件越堆越多，spool 一天内会再次顶到停止水位。积压满批时必须当拍接着清。
func TestContentBackupRuntimeDrainsCleanupBacklogWithinOneTick(t *testing.T) {
	db := t03OpenDB(t)
	source := &t20ConfigSource{cfg: t03Config()}
	source.set(func(c *contentbackup.Config) { c.SiteLabel = "" })
	rt, err := Start(context.Background(), RuntimeConfig{
		SpoolDir:          filepath.Join(t.TempDir(), "spool"),
		DB:                db,
		Config:            source.get,
		SuperviseInterval: 30 * time.Millisecond,
		Logf:              func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = rt.Shutdown(context.Background()) }()

	const backlog = 250 // 超过默认一批 100 的两倍
	uploadedAt := time.Now().Add(-time.Minute)
	for i := 0; i < backlog; i++ {
		jobID := contentbackup.NewJobID()
		path, err := rt.Spool().JobPath(jobID)
		if err != nil {
			t.Fatalf("JobPath: %v", err)
		}
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatalf("seed spool file: %v", err)
		}
		row := model.ContentBackupJob{
			SiteID:        t03SiteID,
			JobID:         jobID,
			StorageNodeID: rt.StorageNodeID(),
			CreatedAt:     uploadedAt.Add(-time.Minute).Unix(),
			Status:        model.ContentBackupStatusUploaded,
			CleanupState:  model.ContentBackupCleanupPending,
			UploadedAt:    uploadedAt.Unix(),
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}

	source.set(func(c *contentbackup.Config) { c.SiteLabel = t03SiteID })
	store := model.NewContentBackupStore(db, t03SiteID)
	t20WaitFor(t, 3*time.Second, func() bool {
		counts, err := store.CountJobs(context.Background(), rt.StorageNodeID())
		return err == nil && counts.CleanupPendingCount == 0
	}, "a cleanup backlog larger than one batch must be drained within one tick, not one batch per 30s")
	entries, err := os.ReadDir(rt.Spool().BodiesDir())
	if err != nil {
		t.Fatalf("read bodies dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("%d spool files left behind after cleanup", len(entries))
	}
}

// 上传提交只插一行增量，今日统计由清理循环合并；积压超过一批时同一拍内也要合并完，
// 否则状态栏的"今日上传"会一直落后。
func TestContentBackupRuntimeFoldsUploadDeltasIntoDailyStats(t *testing.T) {
	db := t03OpenDB(t)
	source := &t20ConfigSource{cfg: t03Config()}
	source.set(func(c *contentbackup.Config) { c.SiteLabel = "" })
	rt, err := Start(context.Background(), RuntimeConfig{
		SpoolDir:          filepath.Join(t.TempDir(), "spool"),
		DB:                db,
		Config:            source.get,
		SuperviseInterval: 30 * time.Millisecond,
		Logf:              func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = rt.Shutdown(context.Background()) }()

	const deltas = 2*contentBackupStatFoldBatch + 50
	day := model.ContentBackupStatDay(time.Now().Unix())
	rows := make([]model.ContentBackupStatDelta, 0, deltas)
	for i := 0; i < deltas; i++ {
		rows = append(rows, model.ContentBackupStatDelta{
			SiteID:        t03SiteID,
			StorageNodeID: rt.StorageNodeID(),
			StatDate:      day,
			UploadedBytes: 3,
			CreatedAt:     time.Now().Unix(),
		})
	}
	if err := db.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatalf("seed deltas: %v", err)
	}

	source.set(func(c *contentbackup.Config) { c.SiteLabel = t03SiteID })
	store := model.NewContentBackupStore(db, t03SiteID)
	t20WaitFor(t, 3*time.Second, func() bool {
		stat, err := store.GetDailyStats(context.Background(), day, rt.StorageNodeID())
		return err == nil && stat.UploadedCount == deltas
	}, "every upload delta must reach the daily stats, more than one batch within one tick")
	stat, err := store.GetDailyStats(context.Background(), day, rt.StorageNodeID())
	if err != nil || stat.UploadedBytes != 3*deltas {
		t.Fatalf("daily stat = %+v (%v), want %d bytes", stat, err, 3*deltas)
	}
	var left int64
	if err := db.Model(&model.ContentBackupStatDelta{}).Count(&left).Error; err != nil || left != 0 {
		t.Fatalf("deltas left = %d (%v), want 0", left, err)
	}
}

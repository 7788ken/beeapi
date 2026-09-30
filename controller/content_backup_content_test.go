package controller

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	contentbackupworker "github.com/QuantumNous/new-api/pkg/contentbackupworker"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 读取路径（预览 / 下载 / 测试连接）自 2026-09-18 起直接调用进程内 Reader。这里测的是
// 网关这一层的装配：授权之外的参数校验、状态码翻译、下载头延迟到首字节、半途失败断连。
// Reader 建在一个假远端之上，凭据来自配置——不再有 socket、daemon 或环境变量。

const (
	cbSite   = "sitea"
	cbNode   = "node-a"
	cbTarget = "target-a"
)

func setupContentBackupContentTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	previous := model.DB
	model.DB = db
	require.NoError(t, db.AutoMigrate(model.ContentBackupModels()...))

	t.Cleanup(func() {
		model.DB = previous
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

type cbFakeRemote struct {
	objects map[string][]byte
	openErr error
	puts    int
	removes int
}

func (r *cbFakeRemote) PutVerified(_ context.Context, job model.ContentBackupJob, _ model.ContentBackupLease, src io.Reader) error {
	data, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	if r.objects == nil {
		r.objects = map[string][]byte{}
	}
	r.objects[job.RemotePath] = data
	r.puts++
	return nil
}

func (r *cbFakeRemote) Open(_ context.Context, job model.ContentBackupJob) (io.ReadCloser, error) {
	if r.openErr != nil {
		return nil, r.openErr
	}
	data, ok := r.objects[job.RemotePath]
	if !ok {
		return nil, fmt.Errorf("content backup sftp: open %s: %w", job.RemotePath, contentbackupworker.ErrFTPSMissing)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (r *cbFakeRemote) Remove(_ context.Context, job model.ContentBackupJob) error {
	delete(r.objects, job.RemotePath)
	r.removes++
	return nil
}

func (r *cbFakeRemote) ListIncoming(context.Context, string) ([]contentbackupworker.IncomingTemp, error) {
	return nil, nil
}

func (r *cbFakeRemote) RemoveIncoming(context.Context, contentbackupworker.IncomingTemp) error {
	return nil
}

// cbSeedUploaded 造一条 uploaded 记录和摘要一致的 gzip 信封，放进假远端。
func cbSeedUploaded(t *testing.T, db *gorm.DB, remote *cbFakeRemote, body string) (string, []byte) {
	t.Helper()
	jobID := contentbackup.NewJobID()
	missing := contentbackup.SessionMissingReasonAbsent
	meta := contentbackup.Metadata{
		Version: contentbackup.MetadataVersion, SiteID: cbSite, StorageNodeID: cbNode, JobID: jobID,
		RequestID: "req-" + jobID, TargetID: cbTarget, ConfigVersion: 1,
		RequestStartedAt: time.Date(2026, 9, 15, 4, 34, 56, 0, time.UTC),
		UserID:           10086, TokenID: 42, ChannelID: 12, ChannelName: "example", Model: "m",
		Endpoint: "/v1/chat/completions", TerminalReason: "ok", ChannelType: 1, HTTPStatus: 200,
		SessionMissingReason: &missing,
	}
	request := []byte(`{"prompt":` + fmt.Sprintf("%q", body) + `}`)
	response := []byte(`{"completion":` + fmt.Sprintf("%q", body) + `}`)
	meta.Request = contentbackup.BodyMeta{ContentType: "application/json", CapturedBytes: int64(len(request)), ObservedBytes: int64(len(request)), Complete: true}
	meta.Response = contentbackup.BodyMeta{ContentType: "application/json", CapturedBytes: int64(len(response)), ObservedBytes: int64(len(response)), Complete: true}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	frameSHA := fmt.Sprintf("%x", sha256.Sum256(append(append([]byte(nil), request...), response...)))
	require.NoError(t, contentbackup.WriteEnvelope(gz, meta, [][]byte{request}, [][]byte{response}, frameSHA))
	require.NoError(t, gz.Close())
	raw := buf.Bytes()
	remotePath, err := meta.RemotePath()
	require.NoError(t, err)
	sum := sha256.Sum256(raw)

	store := model.NewContentBackupStore(db, cbSite)
	job := model.ContentBackupJob{
		SiteID: cbSite, JobID: jobID, RequestID: meta.RequestID, UserID: meta.UserID, StorageNodeID: cbNode,
		TargetID: cbTarget, RemotePath: remotePath, FrameSHA256: frameSHA,
		CompressedSHA256: hex.EncodeToString(sum[:]), CompressedBytes: int64(len(raw)),
		CreatedAt: meta.RequestStartedAt.Unix(), Status: model.ContentBackupStatusPending,
	}
	require.NoError(t, store.EnsurePending(context.Background(), job))
	now := time.Now().UTC()
	lease, ok, err := store.Claim(context.Background(), jobID, cbNode, "test", now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, store.MarkUploaded(context.Background(), lease, now))
	if remote.objects == nil {
		remote.objects = map[string][]byte{}
	}
	remote.objects[remotePath] = raw
	return jobID, raw
}

// cbInstallReader 把一个建在假远端上的 Reader 注入 service 层，并发布带站点标签的配置。
func cbInstallReader(t *testing.T, db *gorm.DB, remote *cbFakeRemote) {
	t.Helper()
	cfg := contentbackup.DefaultConfig()
	cfg.SiteLabel = cbSite
	cfg.TargetID = cbTarget
	cfg.RemoteProtocol = contentbackup.RemoteProtocolSFTP
	cfg.SFTPHost = "backup.example.com"
	cfg.SFTPHostKeySHA256 = strings.Repeat("ab", 32)
	cfg.RemoteUsername, cfg.RemotePassword = "u", "p"
	require.NoError(t, operation_setting.SetContentBackupConfig(cfg))
	t.Cleanup(func() { _ = operation_setting.SetContentBackupConfig(contentbackup.DefaultConfig()) })

	reader, err := contentbackupworker.NewReader(contentbackupworker.ReaderConfig{
		SiteID:        cbSite,
		StorageNodeID: cbNode,
		Store:         model.NewContentBackupStore(db, cbSite),
		RemoteFactory: func(string) (contentbackupworker.RemoteStore, error) { return remote, nil },
		Timeout:       5 * time.Second,
	})
	require.NoError(t, err)
	restore := service.SetContentBackupReaderProvider(func() *contentbackupworker.Reader { return reader })
	t.Cleanup(restore)
}

func cbCallHandler(t *testing.T, handler gin.HandlerFunc, method, path string, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, nil)
	c.Params = params
	handler(c)
	return rec
}

func TestContentBackupPreviewReturnsBoundedBodiesWithFlags(t *testing.T) {
	db := setupContentBackupContentTestDB(t)
	remote := &cbFakeRemote{}
	jobID, _ := cbSeedUploaded(t, db, remote, "hello preview")
	cbInstallReader(t, db, remote)

	rec := cbCallHandler(t, ContentBackupPreview, http.MethodGet, "/api/content_backup/jobs/"+jobID+"/preview", gin.Params{{Key: "job_id", Value: jobID}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	data := contentBackupJobDataOf(t, rec.Body.String())
	require.Equal(t, jobID, data["job_id"])
	request := data["request"].(map[string]any)
	require.Equal(t, false, request["truncated"], "截断标记必须随正文一起给出")
	require.Equal(t, true, request["complete"])
	require.NotEmpty(t, request["body"])
}

func TestContentBackupPreviewRejectsMalformedJobIDBeforeReading(t *testing.T) {
	db := setupContentBackupContentTestDB(t)
	remote := &cbFakeRemote{}
	cbInstallReader(t, db, remote)
	rec := cbCallHandler(t, ContentBackupPreview, http.MethodGet, "/api/content_backup/jobs/../preview", gin.Params{{Key: "job_id", Value: "../etc/passwd"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestContentBackupPreviewWithoutConfiguredSiteIsUnavailableNot500(t *testing.T) {
	setupContentBackupContentTestDB(t)
	restore := service.SetContentBackupReaderProvider(func() *contentbackupworker.Reader { return nil })
	t.Cleanup(restore)
	rec := cbCallHandler(t, ContentBackupPreview, http.MethodGet, "/x", gin.Params{{Key: "job_id", Value: contentbackup.NewJobID()}})
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, "未配置站点是已知状态，不能兜底成 500 掀掉整个后台: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "site label")
}

func TestContentBackupPreviewRelaysKnownReadStatuses(t *testing.T) {
	db := setupContentBackupContentTestDB(t)
	remote := &cbFakeRemote{}
	jobID, _ := cbSeedUploaded(t, db, remote, "x")
	for path := range remote.objects {
		delete(remote.objects, path)
	}
	cbInstallReader(t, db, remote)
	rec := cbCallHandler(t, ContentBackupPreview, http.MethodGet, "/x", gin.Params{{Key: "job_id", Value: jobID}})
	require.Equal(t, http.StatusConflict, rec.Code, "远端缺失必须是 409 remote_missing: %s", rec.Body.String())

	unknown := contentbackup.NewJobID()
	rec = cbCallHandler(t, ContentBackupPreview, http.MethodGet, "/x", gin.Params{{Key: "job_id", Value: unknown}})
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

func TestContentBackupDownloadStreamsVerifiedBytesWithHeaders(t *testing.T) {
	db := setupContentBackupContentTestDB(t)
	remote := &cbFakeRemote{}
	jobID, raw := cbSeedUploaded(t, db, remote, "download me")
	cbInstallReader(t, db, remote)

	rec := cbCallHandler(t, ContentBackupDownload, http.MethodGet, "/x", gin.Params{{Key: "job_id", Value: jobID}})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/gzip", rec.Header().Get("Content-Type"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Header().Get("Content-Disposition"), jobID+".json.gz")
	require.Equal(t, raw, rec.Body.Bytes(), "下载必须是原始 gzip 字节")
}

// 读取在一个字节都没产出时失败：必须发出真实状态码，而不是 200 + gzip 头 + 空 body。
func TestContentBackupDownloadNeverClaimsSuccessWhenRemoteIsMissing(t *testing.T) {
	db := setupContentBackupContentTestDB(t)
	remote := &cbFakeRemote{}
	jobID, _ := cbSeedUploaded(t, db, remote, "gone")
	for path := range remote.objects {
		delete(remote.objects, path)
	}
	cbInstallReader(t, db, remote)

	rec := cbCallHandler(t, ContentBackupDownload, http.MethodGet, "/x", gin.Params{{Key: "job_id", Value: jobID}})
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Empty(t, rec.Header().Get("Content-Disposition"), "失败时不得带下载头，浏览器会存下一个空 .gz")
	require.NotEqual(t, "application/gzip", rec.Header().Get("Content-Type"))
}

// 运维"保存站点标签 → 立刻点测试连接"会落在运行时构建 worker 的轮询间隙里（2026-09-18 ai 实发）。
// 标签已配置时必须等一小会儿而不是立刻报"未配置"。
func TestContentBackupReaderWaitsForSiteWorkersAfterLabelIsSaved(t *testing.T) {
	db := setupContentBackupContentTestDB(t)
	remote := &cbFakeRemote{}
	cbInstallReader(t, db, remote) // 发布了带 site_label 的配置，并注入一个可用 Reader
	real := service.ContentBackupReader()
	require.NotNil(t, real)

	var calls atomic.Int32
	restore := service.SetContentBackupReaderProvider(func() *contentbackupworker.Reader {
		if calls.Add(1) <= 3 { // 前几次轮询 worker 还没建好
			return nil
		}
		return real
	})
	t.Cleanup(restore)

	start := time.Now()
	rec := cbCallHandler(t, ContentBackupTestConnection, http.MethodPost, "/api/content_backup/test_connection", nil)
	require.Equal(t, http.StatusOK, rec.Code, "标签已配置时应等待 worker 出现，而不是报未配置: %s", rec.Body.String())
	require.GreaterOrEqual(t, calls.Load(), int32(4))
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestContentBackupTestConnectionRunsSixStagesInProcess(t *testing.T) {
	db := setupContentBackupContentTestDB(t)
	remote := &cbFakeRemote{}
	cbInstallReader(t, db, remote)

	rec := cbCallHandler(t, ContentBackupTestConnection, http.MethodPost, "/api/content_backup/test_connection", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data := contentBackupJobDataOf(t, rec.Body.String())
	stages := data["stages"].([]any)
	require.Len(t, stages, 6)
	for _, raw := range stages {
		stage := raw.(map[string]any)
		require.Equal(t, true, stage["ok"], "stage %v failed: %v", stage["name"], stage["message"])
	}
	require.Equal(t, cbTarget, data["target"])
	require.NotEmpty(t, data["executed_at"])
	require.Equal(t, 1, remote.puts, "探针必须真的写过一个合成文件")
	require.Equal(t, 1, remote.removes, "探针必须删掉自己的合成文件")
	require.Empty(t, remote.objects, "远端不得残留探针文件")
}

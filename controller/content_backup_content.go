package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	contentbackupworker "github.com/QuantumNous/new-api/pkg/contentbackupworker"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// 读取路径（预览 / 下载 / 测试连接）自 2026-09-18 起直接调用进程内的 Reader：解压、
// 哈希校验、读取预算都在 Reader 里执行，这里只做授权（路由层 RootAuth）、参数校验
// 与状态码翻译。

var errContentBackupReaderUnavailable = &contentbackupworker.ReadError{
	Code: contentbackupworker.ReadCodeTargetUnavailable,
	Err:  errors.New("content backup is not configured on this node yet (set the site label first)"),
}

// contentBackupReader 取进程内 Reader。站点 worker 是在配置里出现站点标签后由运行时按
// 轮询周期（5 秒）惰性构建的：运维"保存 → 立刻点测试连接"很容易落在这几秒里。只要标签
// 已经配置，就多等一小会儿再下"未配置"的结论，否则是一个假阴性。
func contentBackupReader(ctx context.Context) (*contentbackupworker.Reader, error) {
	deadline := time.Now().Add(contentBackupReaderWait)
	for {
		if reader := service.ContentBackupReader(); reader != nil {
			return reader, nil
		}
		if service.ContentBackupSiteID() == "" || time.Now().After(deadline) {
			return nil, errContentBackupReaderUnavailable
		}
		select {
		case <-ctx.Done():
			return nil, errContentBackupReaderUnavailable
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// contentBackupReaderWait 覆盖运行时一个完整的轮询周期再加余量。
var contentBackupReaderWait = 8 * time.Second

// ContentBackupPreview serves GET /api/content_backup/jobs/:job_id/preview
// (Root only): at most preview_bytes_per_side per side with the truncation and
// completeness flags always attached (design doc 7.2).
func ContentBackupPreview(c *gin.Context) {
	ctx, cancel := contentBackupRemoteCtx(c, 1)
	defer cancel()

	jobID := c.Param("job_id")
	if err := contentbackup.ValidateJobID(jobID); err != nil {
		contentBackupError(c, err, http.StatusBadRequest)
		return
	}
	reader, err := contentBackupReader(ctx)
	if err != nil {
		contentBackupReadError(c, err)
		return
	}
	// 每侧上限只由配置决定，调用方无法加码——否则一个查询参数就能把整份归档拉回内存。
	budget := operation_setting.GetContentBackupConfig().PreviewBytesPerSide
	if budget <= 0 {
		budget = 256 * 1024
	}
	result, err := reader.Preview(ctx, jobID, budget)
	if err != nil {
		contentBackupReadError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": result})
}

// ContentBackupDownload serves GET /api/content_backup/jobs/:job_id/download
// (Root only): streams the verified gzip with a safe fixed filename and
// Cache-Control: no-store (design doc 7.2).
func ContentBackupDownload(c *gin.Context) {
	jobID := c.Param("job_id")
	ctx, cancel := contentBackupRemoteCtx(c, 1)
	defer cancel()

	if err := contentbackup.ValidateJobID(jobID); err != nil {
		contentBackupError(c, err, http.StatusBadRequest)
		return
	}
	reader, err := contentBackupReader(ctx)
	if err != nil {
		contentBackupReadError(c, err)
		return
	}
	// 响应头推迟到第一个字节才写：读取可能在一个字节都没产出时就失败，那时必须还能
	// 发出真实状态码，而不是"200 + gzip 头 + 空 body"——浏览器会存下一个带 .gz 名字
	// 的空文件，运营以为归档已经到手了。
	sink := &contentBackupDeferredSink{c: c, jobID: jobID}
	if err := reader.Download(ctx, jobID, sink); err != nil {
		if !sink.started {
			contentBackupReadError(c, err)
			return
		}
		// 头已经出去了（典型是流末尾的哈希不符）。正常返回会给出一个格式完整的响应，
		// 客户端把半截归档当成完整文件存盘；这里必须让连接断在半途。
		common.SysError("content backup download " + jobID + ": " + err.Error())
		contentBackupAbortStream(c)
	}
}

type contentBackupDeferredSink struct {
	c       *gin.Context
	jobID   string
	started bool
}

func (s *contentBackupDeferredSink) Write(p []byte) (int, error) {
	if !s.started {
		s.started = true
		s.c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", s.jobID+".json.gz"))
		s.c.Header("Content-Type", "application/gzip")
		s.c.Header("Cache-Control", "no-store")
		s.c.Status(http.StatusOK)
	}
	return s.c.Writer.Write(p)
}

// contentBackupAbortStream 在正文写到一半失败时断开连接。此时状态行已经发出去了，
// 正常返回会给出一个格式完整的响应，客户端把半截归档当成完整文件存盘——取证场景里
// 最危险的假成功。断在半途才是真话。
func contentBackupAbortStream(c *gin.Context) {
	c.Abort()
	hijacker, ok := c.Writer.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	_ = conn.Close()
}

// ContentBackupTestConnection serves POST /api/content_backup/test_connection
// (Root only): staged probe results against the saved target only (design doc 7.2).
func ContentBackupTestConnection(c *gin.Context) {
	ctx, cancel := contentBackupRemoteCtx(c, 3)
	defer cancel()

	reader, err := contentBackupReader(ctx)
	if err != nil {
		contentBackupReadError(c, err)
		return
	}
	cfg := operation_setting.GetContentBackupConfig()
	result, err := reader.Probe(ctx, cfg.TargetID, cfg.RemoteHost())
	if err != nil && len(result.Stages) == 0 {
		contentBackupError(c, err, http.StatusBadRequest)
		return
	}
	// 阶段里带着失败也仍是 200：探测确实执行过了，逐阶段结论就是它的输出。
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"target":        result.Target,
			"stages":        result.Stages,
			"executed_node": service.ContentBackupStorageNodeID(),
			"executed_at":   time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// contentBackupReadError 把读取失败翻译成状态码。这里禁止把已知状态兜底成 500：
// 前端在 queryCache.onError 里对任何 500 直接跳 /500 错误页，运营点一次
// 「加载预览」就会丢掉整个后台管理页面，连失败原因都看不到（设计文档 9.3）。
func contentBackupReadError(c *gin.Context, err error) {
	var readErr *contentbackupworker.ReadError
	if errors.As(err, &readErr) {
		contentBackupError(c, err, contentbackupworker.ReadErrorStatus(err))
		return
	}
	contentBackupError(c, err, http.StatusInternalServerError)
}

var _ io.Writer = (*contentBackupDeferredSink)(nil)

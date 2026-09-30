package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// contentBackupStore 按站点身份构造：site_id 取配置里的 site_label（Root 在设置页填写，
// 整站共享同一个库所以所有节点一致），绝不接受请求参数，所有查询的站点过滤都在 store
// 内部完成（设计文档 7.2）。未配置时 site_id 为空，查询自然为空集，而不是猜一个。
func contentBackupStore() (*model.ContentBackupStore, error) {
	if model.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	return model.NewContentBackupStore(model.DB, operation_setting.GetContentBackupConfig().SiteLabel), nil
}

func contentBackupError(c *gin.Context, err error, status int) {
	c.JSON(status, gin.H{"success": false, "message": err.Error()})
}

// ContentBackupStatus serves GET /api/content_backup/status: the daily counters
// and queue summary for the status bar (design doc 7.2 / 9.2).
func ContentBackupStatus(c *gin.Context) {
	store, err := contentBackupStore()
	if err != nil {
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	ctx, cancel := contentBackupCtx(c)
	defer cancel()

	counts, err := store.CountJobs(ctx, "")
	if err != nil {
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	cfg := operation_setting.GetContentBackupConfig()
	today := model.ContentBackupStatDay(time.Now().Unix())
	stats, err := store.GetDailyStats(ctx, today, "")
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"enabled":               cfg.Enabled,
			"upload_paused":         cfg.UploadPaused,
			"configured":            cfg.TargetID != "" && cfg.RemoteHost() != "",
			"pending_count":         counts.PendingCount,
			"processing_count":      counts.ProcessingCount,
			"failed_count":          counts.FailedCount,
			"cleanup_pending_count": counts.CleanupPendingCount,
			"cleanup_pending_bytes": counts.CleanupPendingBytes,
			"oldest_pending_at":     contentBackupRFC3339(counts.OldestPendingAt),
			"today_uploaded_count":  stats.UploadedCount,
			"today_uploaded_bytes":  stats.UploadedBytes,
			"sampled_at":            time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// ContentBackupNodes serves GET /api/content_backup/nodes: per-node snapshots
// with online/offline derived from last_seen_at (never persisted).
func ContentBackupNodes(c *gin.Context) {
	store, err := contentBackupStore()
	if err != nil {
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	ctx, cancel := contentBackupCtx(c)
	defer cancel()

	nodes, err := store.ListNodeStatus(ctx)
	if err != nil {
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	offlineAfter := time.Duration(operation_setting.GetContentBackupConfig().NodeOfflineSeconds) * time.Second
	now := time.Now().Unix()
	items := make([]gin.H, 0, len(nodes))
	for _, node := range nodes {
		lastSeen := contentBackupRFC3339(node.LastSeenAt)
		state := "online"
		if node.LastSeenAt <= 0 || now-node.LastSeenAt > int64(offlineAfter.Seconds()) {
			state = "offline"
		}
		items = append(items, gin.H{
			"site_id":                 node.SiteID,
			"storage_node_id":         node.StorageNodeID,
			"process_id":              node.ProcessID,
			"config_version":          node.ConfigVersion,
			"applied_config_version":  node.AppliedConfigVersion,
			"last_seen_at":            lastSeen,
			"sampled_at":              contentBackupRFC3339(node.SampledAt),
			"online":                  state == "online",
			"spool_bytes":             node.SpoolBytes,
			"spool_limit_bytes":       node.SpoolLimitBytes,
			"disk_total_bytes":        node.DiskTotalBytes,
			"free_bytes":              node.FreeBytes,
			"inode_total":             node.InodeTotal,
			"free_inodes":             node.FreeInodes,
			"pending_count":           node.PendingCount,
			"processing_count":        node.ProcessingCount,
			"failed_count":            node.FailedCount,
			"oldest_pending_at":       contentBackupRFC3339(node.OldestPendingAt),
			"cleanup_pending_count":   node.CleanupPendingCount,
			"cleanup_pending_bytes":   node.CleanupPendingBytes,
			"orphan_count":            node.OrphanCount,
			"incomplete_spool_count":  node.IncompleteSpoolCount,
			"handoff_rejected_count":  node.HandoffRejectedCount,
			"handoff_unknown_count":   node.HandoffUnknownCount,
			"upload_bytes_per_second": node.UploadBytesPerSecond,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"items": items}})
}

// ContentBackupJobs serves GET /api/content_backup/jobs with cursor pagination.
func ContentBackupJobs(c *gin.Context) {
	store, err := contentBackupStore()
	if err != nil {
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	ctx, cancel := contentBackupCtx(c)
	defer cancel()

	filter := model.ContentBackupJobFilter{
		View:          c.Query("view"),
		Status:        c.Query("status"),
		CleanupState:  c.Query("cleanup_state"),
		RequestID:     c.Query("request_id"),
		StorageNodeID: c.Query("storage_node_id"),
		SessionSource: c.Query("session_source"),
		Cursor:        c.Query("cursor"),
	}
	if v := c.Query("user_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			filter.UserID = &id
		}
	}
	if v := c.Query("channel_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			filter.ChannelID = &id
		}
	}
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.From = &t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.To = &t
		}
	}
	if v := c.Query("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.PageSize = n
		}
	}
	page, err := store.ListJobs(ctx, filter)
	if err != nil {
		contentBackupError(c, err, http.StatusBadRequest)
		return
	}
	items := make([]gin.H, 0, len(page.Items))
	for _, job := range page.Items {
		items = append(items, contentBackupJobDTO(job))
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":       items,
			"next_cursor": page.NextCursor,
			"has_more":    page.HasMore,
		},
	})
}

// ContentBackupSearchSession serves POST /api/content_backup/jobs/search-session:
// the raw session value travels in the body only, hashed server-side (design doc 7.2).
func ContentBackupSearchSession(c *gin.Context) {
	store, err := contentBackupStore()
	if err != nil {
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	var req dto.ContentBackupSessionSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		contentBackupError(c, err, http.StatusBadRequest)
		return
	}
	if req.UserID == 0 || strings.TrimSpace(req.SessionValue) == "" {
		contentBackupError(c, errors.New("user_id and session_value are required"), http.StatusBadRequest)
		return
	}
	ctx, cancel := contentBackupCtx(c)
	defer cancel()

	sources := contentbackup.SessionSources()
	if req.SessionSource != nil && *req.SessionSource != "" {
		sources = []string{*req.SessionSource}
	}
	var combined []model.ContentBackupJob
	nextCursor := ""
	for _, source := range sources {
		filter := model.ContentBackupJobFilter{
			View:          model.ContentBackupViewArchive,
			UserID:        &req.UserID,
			SessionSource: source,
			SessionHash:   contentbackup.SessionKey(source, req.SessionValue),
			Cursor:        req.Cursor,
			PageSize:      req.PageSize,
		}
		if req.From != nil {
			filter.From = req.From
		}
		if req.To != nil {
			filter.To = req.To
		}
		page, err := store.ListJobs(ctx, filter)
		if err != nil {
			continue
		}
		combined = append(combined, page.Items...)
		if page.NextCursor != nil {
			nextCursor = *page.NextCursor
		}
	}
	items := make([]gin.H, 0, len(combined))
	for _, job := range combined {
		items = append(items, contentBackupJobDTO(job))
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":    items,
			"has_more": nextCursor != "",
		},
	})
}

// ContentBackupJobDetail serves GET /api/content_backup/jobs/:job_id (metadata only).
func ContentBackupJobDetail(c *gin.Context) {
	store, err := contentBackupStore()
	if err != nil {
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	ctx, cancel := contentBackupCtx(c)
	defer cancel()

	job, err := store.GetJob(ctx, c.Param("job_id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			contentBackupError(c, errors.New("no archive record for this request"), http.StatusNotFound)
			return
		}
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": contentBackupJobDTO(job)})
}

// ContentBackupRetry serves POST /api/content_backup/jobs/retry: at most 100
// explicit job ids, each returning queued/skipped/failed with a reason.
func ContentBackupRetry(c *gin.Context) {
	store, err := contentBackupStore()
	if err != nil {
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	var req dto.ContentBackupRetryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		contentBackupError(c, err, http.StatusBadRequest)
		return
	}
	if len(req.JobIDs) == 0 || len(req.JobIDs) > 100 {
		contentBackupError(c, errors.New("job_ids must contain between 1 and 100 entries"), http.StatusBadRequest)
		return
	}
	ctx, cancel := contentBackupCtx(c)
	defer cancel()

	results := make([]gin.H, 0, len(req.JobIDs))
	now := time.Now()
	for _, jobID := range req.JobIDs {
		result, err := store.RetryFailed(ctx, jobID, now)
		if err != nil {
			results = append(results, gin.H{"job_id": jobID, "result": "failed", "reason": err.Error()})
			continue
		}
		results = append(results, gin.H{"job_id": result.JobID, "result": result.Result, "reason": result.Reason})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"results": results}})
}

// ContentBackupGetConfig serves GET /api/content_backup/config (Root only). The
// password never leaves the server: the config is redacted and a boolean says
// whether a password is stored, so the form can show "set" without echoing it.
func ContentBackupGetConfig(c *gin.Context) {
	cfg := operation_setting.GetContentBackupConfig()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"config":                 cfg.Redacted(),
			"remote_password_set":    cfg.RemotePassword != "",
			"remote_credentials_set": cfg.RemoteCredentialsSet(),
		},
	})
}

// ContentBackupPutConfig serves PUT /api/content_backup/config (Root only):
// whole-blob save with expected_version CAS (design doc 7.1).
func ContentBackupPutConfig(c *gin.Context) {
	var req dto.ContentBackupConfigUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		contentBackupError(c, err, http.StatusBadRequest)
		return
	}
	if req.ExpectedVersion < 1 {
		contentBackupError(c, errors.New("expected_version is required"), http.StatusBadRequest)
		return
	}
	if model.DB == nil {
		contentBackupError(c, errors.New("database is not initialized"), http.StatusInternalServerError)
		return
	}
	saved, err := model.SaveContentBackupConfig(c.Request.Context(), model.DB, req.Config, req.ExpectedVersion)
	if err != nil {
		if errors.Is(err, model.ErrContentBackupConfigConflict) {
			contentBackupError(c, err, http.StatusConflict)
			return
		}
		if errors.Is(err, operation_setting.ErrContentBackupConfigInvalid) || errors.Is(err, contentbackup.ErrInvalidConfig) {
			contentBackupError(c, err, http.StatusBadRequest)
			return
		}
		contentBackupError(c, err, http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"config": saved.Redacted(), "remote_password_set": saved.RemotePassword != "", "remote_credentials_set": saved.RemoteCredentialsSet()}})
}

// ContentBackupUpdateChannels serves PUT /api/content_backup/channels/backup:
// flips only the backup boolean of selected channels without clobbering the
// rest of their settings (design doc 7.2).
func ContentBackupUpdateChannels(c *gin.Context) {
	var req dto.ContentBackupChannelBackupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		contentBackupError(c, err, http.StatusBadRequest)
		return
	}
	if len(req.ChannelIDs) == 0 || len(req.ChannelIDs) > 100 {
		contentBackupError(c, errors.New("channel_ids must contain between 1 and 100 entries"), http.StatusBadRequest)
		return
	}
	if model.DB == nil {
		contentBackupError(c, errors.New("database is not initialized"), http.StatusInternalServerError)
		return
	}
	updated := 0
	for _, id := range req.ChannelIDs {
		// Read-modify-write inside one transaction so other settings survive.
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// 只取 setting 一列，扫进带 *string 字段的结构体：Pluck 要求目标是切片，
			// 直接传 *string 会让 GORM 走 Scan-without-Next，每个渠道都报错，
			// 整个批量开关恒定返回 "no channel was updated"；而元素类型用 string
			// 又会在存量渠道 setting 为 NULL 时扫描失败。Take 顺带给出
			// ErrRecordNotFound，渠道不存在就不会被算进 updated。
			var row struct{ Setting *string }
			if err := tx.Model(&model.Channel{}).Select("setting").Where("id = ?", id).Take(&row).Error; err != nil {
				return err
			}
			setting := row.Setting
			channelSettings := dto.ChannelSettings{}
			if setting != nil && *setting != "" {
				if err := common.Unmarshal([]byte(*setting), &channelSettings); err != nil {
					return err
				}
			}
			channelSettings.ContentBackupEnabled = req.Enabled
			blob, err := common.Marshal(channelSettings)
			if err != nil {
				return err
			}
			value := string(blob)
			return tx.Model(&model.Channel{}).Where("id = ?", id).Update("setting", value).Error
		})
		if err == nil {
			updated++
		}
	}
	if updated == 0 {
		contentBackupError(c, errors.New("no channel was updated"), http.StatusInternalServerError)
		return
	}
	model.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"updated": updated}})
}

// contentBackupJobDTO maps the row to the public whitelist (design doc 9.2):
// no local_path, no lease internals, no raw session value.
func contentBackupJobDTO(job model.ContentBackupJob) gin.H {
	return gin.H{
		"site_id":             job.SiteID,
		"job_id":              job.JobID,
		"request_id":          job.RequestID,
		"user_id":             job.UserID,
		"token_id":            job.TokenID,
		"channel_id":          job.ChannelID,
		"channel_name":        job.ChannelName,
		"channel_type":        job.ChannelType,
		"model":               job.Model,
		"endpoint":            job.Endpoint,
		"session_source":      job.SessionSource,
		"session_hash":        job.SessionHash,
		"session_hint":        job.SessionHint,
		"upstream_request_id": job.UpstreamRequestID,
		"created_at":          contentBackupRFC3339(job.CreatedAt),
		"storage_node_id":     job.StorageNodeID,
		"target_id":           job.TargetID,
		"config_version":      job.ConfigVersion,
		"remote_path":         job.RemotePath,
		"frame_sha256":        job.FrameSHA256,
		"compressed_sha256":   job.CompressedSHA256,
		"compressed_bytes":    job.CompressedBytes,
		"stream":              job.Stream,
		"http_status":         job.HTTPStatus,
		"terminal_reason":     job.TerminalReason,
		// content_type 与 observed_bytes 必须随两侧一起给出：客查判断"正文是不是
		// 被截断"靠的就是 captured 与 observed 的差值，缺了 observed 前端只能显示
		// 恒定的 "-"，属于设计文档 9.3 禁止的假值。
		"request_content_type":    job.RequestContentType,
		"request_truncated":       job.RequestTruncated,
		"request_complete":        job.RequestComplete,
		"request_captured_bytes":  job.RequestCapturedBytes,
		"request_observed_bytes":  job.RequestObservedBytes,
		"response_content_type":   job.ResponseContentType,
		"response_truncated":      job.ResponseTruncated,
		"response_complete":       job.ResponseComplete,
		"response_captured_bytes": job.ResponseCapturedBytes,
		"response_observed_bytes": job.ResponseObservedBytes,
		"status":                  job.Status,
		"attempts":                job.Attempts,
		"retry_round":             job.RetryRound,
		"total_attempts":          job.TotalAttempts,
		"last_error_code":         job.LastErrorCode,
		"last_error_message":      job.LastErrorMessage,
		"available_at":            contentBackupRFC3339(job.AvailableAt),
		"updated_at":              contentBackupRFC3339(job.UpdatedAt),
		"uploaded_at":             contentBackupRFC3339(job.UploadedAt),
		"cleanup_state":           job.CleanupState,
		"cleanup_attempts":        job.CleanupAttempts,
		"cleanup_available_at":    contentBackupRFC3339(job.CleanupAvailableAt),
		"cleaned_at":              contentBackupRFC3339(job.CleanedAt),
	}
}

func contentBackupRFC3339(unixSeconds int64) string {
	if unixSeconds <= 0 {
		return ""
	}
	return time.Unix(unixSeconds, 0).UTC().Format(time.RFC3339)
}

func contentBackupCtx(c *gin.Context) (context.Context, context.CancelFunc) {
	timeout := time.Duration(operation_setting.GetContentBackupConfig().DaemonDBTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return context.WithTimeout(c.Request.Context(), timeout)
}

// contentBackupRemoteCtx bounds the handlers that go to the remote target (preview,
// download, probe). The DB budget above (3s) is far too short for them: one SSH handshake
// to the real target measured ~3s by itself, and the probe dials three times (write, read,
// delete). read_timeout_seconds is the per-operation budget the Reader also applies; the
// probe gets three of them because it is three remote operations in a row.
func contentBackupRemoteCtx(c *gin.Context, operations int) (context.Context, context.CancelFunc) {
	per := time.Duration(operation_setting.GetContentBackupConfig().ReadTimeoutSeconds) * time.Second
	if per <= 0 {
		per = 30 * time.Second
	}
	if operations < 1 {
		operations = 1
	}
	return context.WithTimeout(c.Request.Context(), per*time.Duration(operations))
}

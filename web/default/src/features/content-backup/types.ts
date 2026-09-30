/**
 * DTO 对应 T09 拟新增的 dto/content_backup.go（尚未实现）。字段 JSON 名逐字取自
 * model/content_backup_{job,node,stats}.go 的 json tag（T02 已冻结）与文档 9.2。
 * T09 走白名单映射，最终可能只返回子集，因此除主键外一律可选，组件对缺失值安全渲染。
 * lease_owner/lease_token/lease_until/lease_generation 与 local_path 属于内部执行字段，
 * 这里刻意不声明，UI 也不会展示。
 * 时间字段声明为 UTC RFC3339 字符串（文档 9.2）；若后端仍返回 int64 Unix 秒，
 * 由 api.ts 调用 status.ts 的归一化统一转换，组件只消费字符串。
 */

export const CONTENT_BACKUP_STATUS = [
  'pending',
  'processing',
  'failed',
  'uploaded',
] as const
export type ContentBackupJobStatus = (typeof CONTENT_BACKUP_STATUS)[number]

export const CONTENT_BACKUP_CLEANUP_STATE = [
  'not_applicable',
  'pending',
  'done',
] as const
export type ContentBackupCleanupState =
  (typeof CONTENT_BACKUP_CLEANUP_STATE)[number]

export const CONTENT_BACKUP_RETRY_RESULT = ['queued', 'skipped', 'failed'] as const
export type ContentBackupRetryOutcome =
  (typeof CONTENT_BACKUP_RETRY_RESULT)[number]

export const CONTENT_BACKUP_VIEW = ['archive', 'queue'] as const
export type ContentBackupView = (typeof CONTENT_BACKUP_VIEW)[number]

export const CONTENT_BACKUP_TERMINAL_REASON = [
  'complete',
  'client_disconnect',
  'error_response',
  'unknown',
  'validation_error',
  'upstream_error',
  'retry_exhausted',
] as const
export type ContentBackupTerminalReason =
  (typeof CONTENT_BACKUP_TERMINAL_REASON)[number]

export const CONTENT_BACKUP_SESSION_SOURCE = [
  'user',
  'metadata.user_id',
  'prompt_cache_key',
] as const
export type ContentBackupSessionSource =
  (typeof CONTENT_BACKUP_SESSION_SOURCE)[number]

/** 这三种失败必须线下恢复本地文件后才能重新入队（文档 6.2 末行） */
export const CONTENT_BACKUP_UNRECOVERABLE_ERROR_CODE = [
  'local_missing',
  'hash_error',
  'incomplete_spool',
] as const

export const CONTENT_BACKUP_PERM_VIEW = 'content_backup.view'
export const CONTENT_BACKUP_PERM_MANAGE = 'content_backup.manage'

export const CONTENT_BACKUP_DEFAULT_PAGE_SIZE = 20
export const CONTENT_BACKUP_MAX_PAGE_SIZE = 100
/** POST /jobs/retry 一批最多 100 个明确 job ID（文档 7.2） */
export const CONTENT_BACKUP_MAX_RETRY_BATCH = 100
/** PUT /channels/backup 一批最多 100 个渠道 ID（后端 controller 硬校验） */
export const CONTENT_BACKUP_MAX_CHANNEL_BULK = 100
/** 普通列表一次最多查 31 天（文档 3.2） */
export const CONTENT_BACKUP_MAX_RANGE_DAYS = 31
/** 每侧预览最多 256 KiB（文档 7.2） */
export const CONTENT_BACKUP_PREVIEW_BYTES_PER_SIDE = 256 * 1024
/** 节点离线判定：45 秒无心跳，后端不落库，只能由前端按 last_seen_at 计算 */
export const CONTENT_BACKUP_NODE_OFFLINE_SECONDS = 45
/** spool 80% 告警、90% 停止新交接（文档 7.3） */
export const CONTENT_BACKUP_SPOOL_ALERT_PERCENT = 80
export const CONTENT_BACKUP_SPOOL_STOP_PERCENT = 90
export const CONTENT_BACKUP_INODE_ALERT_PERCENT = 90

export interface ContentBackupResponse<T> {
  success: boolean
  message?: string
  data?: T
}

export interface ContentBackupJob {
  job_id: string
  site_id?: string
  request_id?: string
  user_id?: number
  token_id?: number
  channel_id?: number
  channel_name?: string
  channel_type?: number
  model?: string
  endpoint?: string
  session_source?: string
  session_hash?: string
  /** 脱敏截短提示，不是原始会话值 */
  session_hint?: string
  upstream_request_id?: string
  created_at?: string
  storage_node_id?: string
  target_id?: string
  config_version?: number
  remote_path?: string
  frame_sha256?: string
  compressed_sha256?: string
  compressed_bytes?: number
  stream?: boolean
  http_status?: number
  terminal_reason?: string
  request_content_type?: string
  request_captured_bytes?: number
  request_observed_bytes?: number
  request_truncated?: boolean
  request_complete?: boolean
  response_content_type?: string
  response_captured_bytes?: number
  response_observed_bytes?: number
  response_truncated?: boolean
  response_complete?: boolean
  /** 取值见 CONTENT_BACKUP_STATUS；未知值原样显示，不做穷举渲染 */
  status?: string
  attempts?: number
  retry_round?: number
  total_attempts?: number
  last_error_code?: string
  last_error_message?: string
  available_at?: string
  updated_at?: string
  uploaded_at?: string
  /** 取值见 CONTENT_BACKUP_CLEANUP_STATE；与 status 相互独立 */
  cleanup_state?: string
  cleanup_attempts?: number
  cleanup_available_at?: string
  cleanup_error?: string
  cleaned_at?: string
}

export interface ContentBackupJobPage {
  items: ContentBackupJob[]
  /** 不透明游标，前端不解析，原样回传 */
  next_cursor?: string | null
  has_more?: boolean
}

export interface ContentBackupJobsQuery {
  view: ContentBackupView
  status?: string
  cleanup_state?: string
  request_id?: string
  user_id?: number
  channel_id?: number
  storage_node_id?: string
  /** UTC RFC3339 */
  from?: string
  to?: string
  cursor?: string | null
  page_size?: number
}

export interface ContentBackupSessionSearchPayload {
  user_id?: number
  /** 原始会话值只走请求体，绝不进 URL / localStorage */
  session_value: string
  session_source?: string
  from?: string
  to?: string
  cursor?: string | null
  page_size?: number
}

/**
 * 三视图与状态条共享的页面状态筛选器。
 * 只存在于页面状态：URL 与 localStorage 都不写入会话值或正文（文档 3.2）。
 * 文本项保留字符串形态，构建查询时再解析成数字。
 */
export type ContentBackupTab = 'archives' | 'queue' | 'nodes'

export interface ContentBackupListFilter {
  requestId?: string
  userId?: string
  channelId?: string
  storageNodeId?: string
  status?: string
  cleanupState?: string
  /** UTC RFC3339 */
  from?: string
  to?: string
}

export interface ContentBackupStatusSummary {
  pending_count?: number
  processing_count?: number
  failed_count?: number
  uploaded_count?: number
  /** 已上传但仍占本地空间，与「已释放」不是一回事（文档 6.4） */
  cleanup_pending_count?: number
  cleanup_pending_bytes?: number
  oldest_pending_at?: string
  /** 今日口径是北京时间（Asia/Shanghai），与归档目录日期同源 */
  today_uploaded_count?: number
  today_uploaded_bytes?: number
  sampled_at?: string
  /**
   * 以下字段文档 9.2 未冻结，T09 可能不返回；缺失时状态条显示「未知」，
   * 不用 0/false 冒充已知状态。
   */
  enabled?: boolean
  upload_paused?: boolean
  configured?: boolean
  target_id?: string
  config_version?: number
  node_offline_seconds?: number
  content_retention_days?: number
  index_retention_days?: number
  capture_rejected_count?: number
}

export interface ContentBackupNodeSnapshot {
  site_id?: string
  storage_node_id?: string
  process_id?: string
  config_version?: number
  applied_config_version?: number
  last_seen_at?: string
  sampled_at?: string
  spool_bytes?: number
  spool_limit_bytes?: number
  disk_total_bytes?: number
  free_bytes?: number
  inode_total?: number
  free_inodes?: number
  pending_count?: number
  processing_count?: number
  failed_count?: number
  oldest_pending_at?: string
  cleanup_pending_count?: number
  cleanup_pending_bytes?: number
  orphan_count?: number
  incomplete_spool_count?: number
  handoff_rejected_count?: number
  handoff_unknown_count?: number
  upload_bytes_per_second?: number
}

export interface ContentBackupRetryResult {
  job_id: string
  /** queued / skipped / failed，见 CONTENT_BACKUP_RETRY_RESULT */
  result?: string
  /** skipped 时形如 not_retryable:<last_error_code> */
  reason?: string
}

/**
 * 预览正文的形态假设：与 pkg/contentbackup.EnvelopeBody（T01 已冻结）一致。
 * T10 尚未实现，若最终字段名不同，只需改这里与 api.ts 的归一化。
 */
export interface ContentBackupPreviewSide {
  content_type?: string
  /** base64 表示原始字节；也可能是 utf-8 文本 */
  encoding?: string
  body?: string
  captured_bytes?: number
  observed_bytes?: number
  truncated?: boolean
  complete?: boolean
}

export interface ContentBackupPreview {
  job_id?: string
  request?: ContentBackupPreviewSide
  response?: ContentBackupPreviewSide
}

/**
 * 与 pkg/contentbackup/config.go 的 Config 结构体逐字段对应（T12 时点冻结）。
 * 全部字段后端都会返回（GET /config 整包下发），无可选字段。
 * 2026-09-18 起备份管道跑在 beeapi 进程内：站点标签与远端用户名/密码都在这里由 Root 配置。
 * GET 永不回显密码（remote_password 恒为 ''），配合 ContentBackupConfigData.remote_password_set；
 * PUT 时密码留空表示沿用已存。
 */
export type ContentBackupRemoteProtocol = 'ftps' | 'sftp'

export interface ContentBackupConfig {
  version: number
  enabled: boolean
  upload_paused: boolean
  /** 站点身份：所有查询按它过滤，也是远端第一层目录；设一次，不要改 */
  site_label: string
  target_id: string
  remote_username: string
  /** GET 时恒为 ''；PUT 时 '' = 沿用已存密码 */
  remote_password: string
  ftps_host: string
  ftps_port: number
  cert_sha256: string
  /** 后端把历史配置里的 '' 当作 'ftps'；表单层统一先规范化再渲染 */
  remote_protocol: ContentBackupRemoteProtocol | ''
  sftp_host: string
  sftp_port: number
  /** SSH 主机公钥 SHA-256 的 64 位小写 hex（ssh-keygen -lf 打印的是同一段字节的 base64） */
  sftp_host_key_sha256: string
  /** 逻辑树 "/{site}/..." 挂在哪个物理目录下；'' = 账号登录目录（SFTP 通常不 chroot，"/" 是真根不可写） */
  sftp_base_dir: string

  max_body_bytes: number
  capture_memory_mb: number
  max_inflight_captures: number
  handoff_workers: number
  spool_workers: number
  upload_workers: number
  read_workers: number
  max_spool_mb: number

  handoff_attempt_timeout_seconds: number
  handoff_deadline_seconds: number
  handoff_max_attempts: number

  daemon_db_max_open: number
  daemon_db_max_idle: number
  daemon_sqlite_db_max_open: number
  daemon_db_timeout_seconds: number
  config_reload_seconds: number

  upload_bandwidth_mib: number
  read_bandwidth_mib: number
  upload_timeout_seconds: number
  ftps_connect_timeout_seconds: number
  lease_seconds: number
  lease_renew_seconds: number
  max_upload_attempts: number

  read_timeout_seconds: number
  preview_bytes_per_side: number
  max_decompress_bytes: number
  read_budget_mb: number

  reconcile_interval_seconds: number
  heartbeat_interval_seconds: number
  node_offline_seconds: number
  queue_refresh_seconds: number
  node_stats_refresh_seconds: number

  spool_alert_percent: number
  spool_stop_percent: number
  inode_alert_percent: number
  inode_recover_percent: number
  min_free_bytes: number
  min_free_percent: number

  oldest_pending_alert_minutes: number
  cleanup_pending_alert_minutes: number
  alert_dedup_minutes: number

  notify_oldest_pending: boolean
  notify_cleanup_pending: boolean
  notify_spool_high: boolean
  notify_inode_high: boolean
  notify_failed: boolean
  notify_handoff_rejected: boolean
  notify_node_offline: boolean

  content_retention_days: number
  index_retention_days: number
  stats_retention_days: number
}

export interface ContentBackupConfigData {
  config: ContentBackupConfig
  /** 库里是否已存密码（永不回显真实值） */
  remote_password_set: boolean
  /** 用户名与密码是否都已配置 */
  remote_credentials_set: boolean
}

export interface ContentBackupConfigSaveData {
  config: ContentBackupConfig
  remote_password_set: boolean
  remote_credentials_set: boolean
}

/** 探针只针对服务器已保存的 target，请求体不带任何参数（文档 7.2） */
export interface ContentBackupProbeStage {
  name: string
  ok: boolean
  message?: string
}

export interface ContentBackupProbeResult {
  target: string
  stages: ContentBackupProbeStage[]
  executed_node?: string
  /** UTC RFC3339 */
  executed_at?: string
}

export interface ContentBackupChannelBackupPayload {
  channel_ids: number[]
  enabled: boolean
}

export interface ContentBackupChannelBackupResult {
  updated: number
}

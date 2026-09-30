import type { ContentBackupConfig, ContentBackupRemoteProtocol } from './types'

/** 表单里 remote_protocol 只有两个合法值；'' 只存在于后端下发的历史配置里 */
export type ContentBackupConfigFormShape = Omit<
  ContentBackupConfig,
  'version' | 'remote_protocol'
> & {
  remote_protocol: ContentBackupRemoteProtocol
}

/**
 * 与 pkg/contentbackup/config.go DefaultConfig() 逐字段对应（不含 version）。
 * 用作表单占位、reset 补洞、以及 zodResolver 入参补齐——RHF 在字段未挂载时会把值
 * 从提交对象里拿掉（切协议、折叠「高级设置」都会卸掉 Controller），Zod 4 的
 * z.string()/z.number() 对 undefined 直接报 "expected string, received undefined"。
 */
export const DEFAULT_FORM_VALUES: ContentBackupConfigFormShape = {
  enabled: false,
  upload_paused: false,
  site_label: '',
  target_id: '',
  remote_username: '',
  remote_password: '',
  ftps_host: '',
  ftps_port: 21,
  cert_sha256: '',
  remote_protocol: 'ftps',
  sftp_host: '',
  sftp_port: 22,
  sftp_host_key_sha256: '',
  sftp_base_dir: '',

  max_body_bytes: 8 * 1024 * 1024,
  capture_memory_mb: 64,
  max_inflight_captures: 128,
  handoff_workers: 1,
  spool_workers: 1,
  upload_workers: 8,
  read_workers: 1,
  max_spool_mb: 2048,

  handoff_attempt_timeout_seconds: 30,
  handoff_deadline_seconds: 60,
  handoff_max_attempts: 2,

  daemon_db_max_open: 4,
  daemon_db_max_idle: 2,
  daemon_sqlite_db_max_open: 1,
  daemon_db_timeout_seconds: 3,
  config_reload_seconds: 5,

  upload_bandwidth_mib: 32,
  read_bandwidth_mib: 1,
  upload_timeout_seconds: 120,
  ftps_connect_timeout_seconds: 10,
  lease_seconds: 180,
  lease_renew_seconds: 30,
  max_upload_attempts: 16,

  read_timeout_seconds: 30,
  preview_bytes_per_side: 256 * 1024,
  max_decompress_bytes: 24 * 1024 * 1024,
  read_budget_mb: 128,

  reconcile_interval_seconds: 30,
  heartbeat_interval_seconds: 15,
  node_offline_seconds: 45,
  queue_refresh_seconds: 10,
  node_stats_refresh_seconds: 30,

  spool_alert_percent: 80,
  spool_stop_percent: 90,
  inode_alert_percent: 90,
  inode_recover_percent: 80,
  min_free_bytes: 1024 * 1024 * 1024,
  min_free_percent: 10,

  oldest_pending_alert_minutes: 15,
  cleanup_pending_alert_minutes: 30,
  alert_dedup_minutes: 30,

  notify_oldest_pending: true,
  notify_cleanup_pending: true,
  notify_spool_high: true,
  notify_inode_high: true,
  notify_failed: true,
  notify_handoff_rejected: true,
  notify_node_offline: true,

  content_retention_days: 30,
  index_retention_days: 30,
  stats_retention_days: 90,
}

/** "/" 或 "/a/b"：绝对、无尾斜杠、无空段与 ./.. 段（与 pkg/contentbackup ValidateRemoteBaseDir 一致）；'' 表示账号登录目录 */
const REMOTE_BASE_DIR_PATTERN = /^\/$|^(\/(?!\.\.?(\/|$))[^/]+)+$/

export function isValidRemoteBaseDir(value: string): boolean {
  return value === '' || REMOTE_BASE_DIR_PATTERN.test(value)
}

function definedEntries<T extends object>(value: T): Partial<T> {
  return Object.fromEntries(
    Object.entries(value).filter(([, entry]) => entry !== undefined)
  ) as Partial<T>
}

/**
 * 把 RHF / 接口给的残缺对象补成整包。显式 undefined 当「没给」处理，避免
 * `{ ...defaults, ftps_host: undefined }` 把默认空串盖掉后再被 Zod 拒掉。
 */
type FormValueInput = Partial<
  Omit<ContentBackupConfigFormShape, 'remote_protocol'>
> & {
  remote_protocol?: ContentBackupRemoteProtocol | ''
}

export function mergeFormValues(values: FormValueInput): ContentBackupConfigFormShape {
  const clean = definedEntries(values)
  return {
    ...DEFAULT_FORM_VALUES,
    ...clean,
    remote_protocol: clean.remote_protocol === 'sftp' ? 'sftp' : 'ftps',
  }
}

/**
 * 后端把 remote_protocol 的 '' 视为 ftps（早于该字段的历史配置）；表单必须先规范化，
 * 否则 z.enum 会把一份合法的历史配置判成无效，页面连保存按钮都点不了。
 * 其它字段原样透传：整包保存时另一协议的值也要一起回去，不能因为没显示就丢掉。
 * 接口缺键（旧行、部分解码）用 DEFAULT_FORM_VALUES 补，reset 后每个 Controller 都有字符串/数字。
 */
export function normalizeConfigForForm(
  config: Omit<ContentBackupConfig, 'version'>
): ContentBackupConfigFormShape {
  return mergeFormValues({
    ...config,
    remote_protocol: config.remote_protocol === 'sftp' ? 'sftp' : 'ftps',
  })
}

const HOST_PATTERN = /^[A-Za-z0-9._-]{1,253}$/
const HEX64_PATTERN = /^[a-f0-9]{64}$/

function asString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/**
 * 未选协议的主机 / pin / 根目录若不合法就清空再提交：它们此刻不显示、也不会被用到，而后端对
 * 两种协议的格式都会校验，一个看不见的坏值会让保存以 400 失败（更早的版本干脆在前端静默
 * 拦住，保存按钮点了没反应）。合法的值原样保留，方便切回。
 */
export function sanitizeInactiveProtocolFields<
  T extends Pick<
    ContentBackupConfig,
    | 'remote_protocol'
    | 'ftps_host'
    | 'cert_sha256'
    | 'sftp_host'
    | 'sftp_host_key_sha256'
    | 'sftp_base_dir'
  >,
>(values: T): T {
  const hostOk = (value: string) => value === '' || HOST_PATTERN.test(value)
  const pinOk = (value: string) => value === '' || HEX64_PATTERN.test(value)
  if (values.remote_protocol === 'sftp') {
    const ftpsHost = asString(values.ftps_host)
    const cert = asString(values.cert_sha256)
    return {
      ...values,
      ftps_host: hostOk(ftpsHost) ? ftpsHost : '',
      cert_sha256: pinOk(cert) ? cert : '',
    }
  }
  const sftpHost = asString(values.sftp_host)
  const sftpPin = asString(values.sftp_host_key_sha256)
  const baseDir = asString(values.sftp_base_dir)
  return {
    ...values,
    sftp_host: hostOk(sftpHost) ? sftpHost : '',
    sftp_host_key_sha256: pinOk(sftpPin) ? sftpPin : '',
    sftp_base_dir: isValidRemoteBaseDir(baseDir) ? baseDir : '',
  }
}

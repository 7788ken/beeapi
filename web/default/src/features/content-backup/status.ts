import dayjs from '@/lib/dayjs'
import { ROLE } from '@/lib/roles'
import {
  CONTENT_BACKUP_INODE_ALERT_PERCENT,
  CONTENT_BACKUP_MAX_PAGE_SIZE,
  CONTENT_BACKUP_MAX_RANGE_DAYS,
  CONTENT_BACKUP_MAX_RETRY_BATCH,
  CONTENT_BACKUP_NODE_OFFLINE_SECONDS,
  CONTENT_BACKUP_PERM_MANAGE,
  CONTENT_BACKUP_PERM_VIEW,
  CONTENT_BACKUP_SPOOL_ALERT_PERCENT,
  CONTENT_BACKUP_SPOOL_STOP_PERCENT,
  CONTENT_BACKUP_UNRECOVERABLE_ERROR_CODE,
  type ContentBackupJob,
  type ContentBackupNodeSnapshot,
  type ContentBackupPreviewSide,
  type ContentBackupStatusSummary,
} from './types'

export type BadgeTone = 'default' | 'secondary' | 'destructive' | 'outline'

/**
 * 后端可能输出 RFC3339 字符串（文档 9.2）也可能仍是 int64 Unix 秒（T02 表结构），
 * 这里统一收敛成 RFC3339 字符串；0/空/非法一律当成「没有该时间」，不显示 1970。
 */
export function normalizeTimestamp(value: unknown): string | undefined {
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || value <= 0) return undefined
    return new Date(value * 1000).toISOString()
  }
  if (typeof value !== 'string') return undefined
  const text = value.trim()
  if (text === '') return undefined
  if (/^\d+$/.test(text)) return normalizeTimestamp(Number(text))
  if (Number.isNaN(Date.parse(text))) return undefined
  return text
}

export function normalizeJobTimes(job: ContentBackupJob): ContentBackupJob {
  return {
    ...job,
    created_at: normalizeTimestamp(job.created_at),
    available_at: normalizeTimestamp(job.available_at),
    updated_at: normalizeTimestamp(job.updated_at),
    uploaded_at: normalizeTimestamp(job.uploaded_at),
    cleanup_available_at: normalizeTimestamp(job.cleanup_available_at),
    cleaned_at: normalizeTimestamp(job.cleaned_at),
  }
}

export function normalizeNodeTimes(
  node: ContentBackupNodeSnapshot
): ContentBackupNodeSnapshot {
  return {
    ...node,
    last_seen_at: normalizeTimestamp(node.last_seen_at),
    sampled_at: normalizeTimestamp(node.sampled_at),
    oldest_pending_at: normalizeTimestamp(node.oldest_pending_at),
  }
}

export function normalizeStatusTimes(
  summary: ContentBackupStatusSummary
): ContentBackupStatusSummary {
  return {
    ...summary,
    oldest_pending_at: normalizeTimestamp(summary.oldest_pending_at),
    sampled_at: normalizeTimestamp(summary.sampled_at),
  }
}

/** 只返回 i18n key（项目约定 key 即英文原文），由组件用 t() 渲染 */
export function statusLabel(status?: string): string {
  switch (status) {
    case 'pending':
      return 'Pending upload'
    case 'processing':
      return 'Uploading'
    case 'failed':
      return 'Upload failed'
    case 'uploaded':
      return 'Uploaded'
    default:
      break
  }
  if (status) return status
  return 'Unknown'
}

/**
 * 「上传成功」与「本地空间已释放」是两个独立状态（文档 6.4）：
 * 只有 cleanup_state=done 才可以说空间已释放，其它组合一律不这么说。
 */
export function cleanupLabel(status?: string, cleanupState?: string): string {
  if (status === 'uploaded') {
    if (cleanupState === 'done') return 'Local space reclaimed'
    if (cleanupState === 'pending') return 'Local cleanup pending'
    return 'Local cleanup state unknown'
  }
  if (status === 'pending' || status === 'processing' || status === 'failed') {
    return 'Not uploaded'
  }
  return 'Upload state unknown'
}

export function statusTone(status?: string): BadgeTone {
  switch (status) {
    case 'uploaded':
      return 'default'
    case 'failed':
      return 'destructive'
    case 'processing':
      return 'secondary'
    default:
      return 'outline'
  }
}

export function cleanupTone(status?: string, cleanupState?: string): BadgeTone {
  if (status !== 'uploaded') return 'outline'
  if (cleanupState === 'done') return 'secondary'
  if (cleanupState === 'pending') return 'destructive'
  return 'outline'
}

export function terminalReasonLabel(reason?: string): string {
  switch (reason) {
    case 'complete':
      return 'Terminal complete'
    case 'client_disconnect':
      return 'Client disconnected'
    case 'error_response':
      return 'Upstream error response'
    case 'validation_error':
      return 'Validation error'
    case 'upstream_error':
      return 'Upstream error'
    case 'retry_exhausted':
      return 'Retries exhausted'
    case 'unknown':
      return 'Unknown'
    default:
      break
  }
  if (reason) return reason
  return 'Unknown'
}

export function retryOutcomeLabel(result?: string): string {
  switch (result) {
    case 'queued':
      return 'Queued'
    case 'skipped':
      return 'Skipped'
    case 'failed':
      return 'Failed'
    default:
      break
  }
  if (result) return result
  return 'Unknown'
}

export function retryOutcomeTone(result?: string): BadgeTone {
  switch (result) {
    case 'queued':
      return 'default'
    case 'skipped':
      return 'secondary'
    default:
      return 'destructive'
  }
}

export function retryBlockedLabel(errorCode?: string): string | undefined {
  const codes: readonly string[] = CONTENT_BACKUP_UNRECOVERABLE_ERROR_CODE
  if (errorCode && codes.includes(errorCode)) {
    return 'Needs offline recovery before it can be queued'
  }
  return undefined
}

/** 只有 failed 能重新入队；processing/uploaded 会被后端 skipped */
export function isRetryableStatus(status?: string): boolean {
  return status === 'failed'
}

export interface RetryReason {
  /** not_retryable:<code> 里的 code */
  code?: string
  /** 不可重试时给运营的 i18n key */
  blockedLabel?: string
  raw?: string
}

export function parseRetryReason(reason?: string): RetryReason {
  if (!reason) return {}
  if (!reason.startsWith('not_retryable:')) return { raw: reason }
  const code = reason.slice('not_retryable:'.length)
  return { code, blockedLabel: retryBlockedLabel(code), raw: reason }
}

/** 与后端 contentBackupQueueRank 一致：failed 0、processing 1、其余 2 */
function queueRank(status?: string): number {
  if (status === 'failed') return 0
  if (status === 'processing') return 1
  return 2
}

function sortTime(item: ContentBackupJob): number {
  const parsed = item.created_at ? Date.parse(item.created_at) : Number.NaN
  if (Number.isNaN(parsed)) return Number.MAX_SAFE_INTEGER
  return parsed
}

/** 后端已按失败优先排序，这里再排一次，避免 T09 漏掉排序键时列表语义走样 */
export function sortQueueJobs(
  items: readonly ContentBackupJob[]
): ContentBackupJob[] {
  return [...items].sort((a, b) => {
    const rank = queueRank(a.status) - queueRank(b.status)
    if (rank !== 0) return rank
    const time = sortTime(a) - sortTime(b)
    if (time !== 0) return time
    return (a.job_id ?? '').localeCompare(b.job_id ?? '')
  })
}

export function integrityLabel(job: {
  request_truncated?: boolean
  response_truncated?: boolean
  request_complete?: boolean
  response_complete?: boolean
}): string {
  if (job.request_truncated === true || job.response_truncated === true) {
    return 'Truncated'
  }
  if (job.request_complete === false || job.response_complete === false) {
    return 'Incomplete'
  }
  if (job.request_complete === true || job.response_complete === true) {
    return 'Complete'
  }
  return 'Unknown'
}

export function integrityTone(label: string): BadgeTone {
  if (label === 'Complete') return 'secondary'
  if (label === 'Truncated' || label === 'Incomplete') return 'destructive'
  return 'outline'
}

export function clampRetryBatch(jobIds: readonly string[]): {
  accepted: string[]
  rejected: string[]
} {
  const accepted: string[] = []
  const rejected: string[] = []
  const seen = new Set<string>()
  for (const jobId of jobIds) {
    const trimmed = jobId.trim()
    if (trimmed === '' || seen.has(trimmed)) continue
    seen.add(trimmed)
    if (accepted.length < CONTENT_BACKUP_MAX_RETRY_BATCH) accepted.push(trimmed)
    else rejected.push(trimmed)
  }
  return { accepted, rejected }
}

export function clampPageSize(pageSize?: number): number {
  if (!pageSize || pageSize <= 0) return 20
  return Math.min(pageSize, CONTENT_BACKUP_MAX_PAGE_SIZE)
}

/** 普通列表一次最多 31 天；精确 request_id 查询由后端放宽到完整索引保留期 */
export function isWithinMaxRange(from?: string, to?: string): boolean {
  if (!from || !to) return true
  const start = Date.parse(from)
  const end = Date.parse(to)
  if (Number.isNaN(start) || Number.isNaN(end)) return true
  const diff = end - start
  if (diff < 0) return false
  return diff <= CONTENT_BACKUP_MAX_RANGE_DAYS * 24 * 60 * 60 * 1000
}

export interface ContentBackupPerms {
  view: boolean
  manage: boolean
}

/**
 * 复用 lib/admin-perms.ts 的语义（点分权限串 + Root 恒有 + 新权限默认不给存量管理员），
 * 但 content_backup.* 尚未登记进 ADMIN_PERM，所以在 feature 内解析，等 T12 收编。
 */
export function resolveContentBackupPerms(
  role: number | undefined,
  flags: Record<string, unknown> | undefined,
  permList: string[] | undefined
): ContentBackupPerms {
  const level = role ?? 0
  if (level < ROLE.ADMIN) return { view: false, manage: false }
  if (level >= ROLE.SUPER_ADMIN) return { view: true, manage: true }
  const manage =
    flags?.content_backup_manage === true ||
    (permList?.includes(CONTENT_BACKUP_PERM_MANAGE) ?? false)
  const view =
    manage ||
    flags?.content_backup_view === true ||
    (permList?.includes(CONTENT_BACKUP_PERM_VIEW) ?? false)
  return { view, manage }
}

export type NodeHeartbeatState = 'online' | 'offline' | 'unknown'

export interface NodeHeartbeat {
  state: NodeHeartbeatState
  secondsSince: number | undefined
}

/** 节点离线（45 秒无心跳）后端不落库，只能按 last_seen_at 现算 */
export function nodeHeartbeat(
  lastSeenAt?: string,
  nowMs: number = Date.now(),
  thresholdSeconds: number = CONTENT_BACKUP_NODE_OFFLINE_SECONDS
): NodeHeartbeat {
  if (!lastSeenAt) return { state: 'unknown', secondsSince: undefined }
  const parsed = Date.parse(lastSeenAt)
  if (Number.isNaN(parsed)) return { state: 'unknown', secondsSince: undefined }
  const secondsSince = Math.max(0, Math.floor((nowMs - parsed) / 1000))
  return {
    state: secondsSince > thresholdSeconds ? 'offline' : 'online',
    secondsSince,
  }
}

export function hasConfigDrift(node: {
  config_version?: number
  applied_config_version?: number
}): boolean {
  if (typeof node.config_version !== 'number') return false
  if (typeof node.applied_config_version !== 'number') return false
  return node.config_version !== node.applied_config_version
}

export function usagePercent(used?: number, total?: number): number | undefined {
  if (typeof used !== 'number' || typeof total !== 'number') return undefined
  if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0) {
    return undefined
  }
  return Math.min(100, Math.max(0, Math.round((used / total) * 1000) / 10))
}

export function spoolPercent(node: ContentBackupNodeSnapshot): number | undefined {
  return usagePercent(node.spool_bytes, node.spool_limit_bytes)
}

export function isSpoolWarning(node: ContentBackupNodeSnapshot): boolean {
  const percent = spoolPercent(node)
  if (percent === undefined) return false
  return percent >= CONTENT_BACKUP_SPOOL_ALERT_PERCENT
}

export function isSpoolCritical(node: ContentBackupNodeSnapshot): boolean {
  const percent = spoolPercent(node)
  if (percent === undefined) return false
  return percent >= CONTENT_BACKUP_SPOOL_STOP_PERCENT
}

export function isInodePressure(node: ContentBackupNodeSnapshot): boolean {
  if (typeof node.inode_total !== 'number' || node.inode_total <= 0) {
    return false
  }
  if (typeof node.free_inodes !== 'number') return false
  const usedPercent =
    ((node.inode_total - node.free_inodes) / node.inode_total) * 100
  return usedPercent >= CONTENT_BACKUP_INODE_ALERT_PERCENT
}

export function backlogSeconds(
  oldestPendingAt?: string,
  nowMs: number = Date.now()
): number | undefined {
  if (!oldestPendingAt) return undefined
  const parsed = Date.parse(oldestPendingAt)
  if (Number.isNaN(parsed)) return undefined
  return Math.max(0, Math.floor((nowMs - parsed) / 1000))
}

/** 没有节点上报该字段时返回 undefined，不用 0 冒充「没有拒收」 */
export function totalHandoffRejected(
  nodes: readonly ContentBackupNodeSnapshot[]
): number | undefined {
  let total: number | undefined
  for (const node of nodes) {
    if (typeof node.handoff_rejected_count !== 'number') continue
    total = (total ?? 0) + node.handoff_rejected_count
  }
  return total
}

export interface BeijingDayRange {
  /** UTC RFC3339，北京时间当天 00:00:00 */
  from: string
  /** UTC RFC3339，北京时间当天 23:59:59 */
  to: string
  /** 北京时间日期，与日统计键和归档目录日期同源 */
  dateLabel: string
}

/** 「今日」统计口径是北京时间（Asia/Shanghai），不是 UTC */
export function beijingDayRange(nowMs: number = Date.now()): BeijingDayRange {
  const shifted = new Date(nowMs + 8 * 60 * 60 * 1000)
  const dateLabel = shifted.toISOString().slice(0, 10)
  const startUtc = Date.parse(`${dateLabel}T00:00:00Z`) - 8 * 60 * 60 * 1000
  const endUtc = Date.parse(`${dateLabel}T23:59:59Z`) - 8 * 60 * 60 * 1000
  return {
    from: new Date(startUtc).toISOString(),
    to: new Date(endUtc).toISOString(),
    dateLabel,
  }
}

export function formatTimestamp(value?: string): string {
  if (!value) return '-'
  const parsed = dayjs(value)
  if (!parsed.isValid()) return value
  return parsed.format('YYYY-MM-DD HH:mm:ss')
}

export function formatRelativeTime(value?: string): string {
  if (!value) return '-'
  const parsed = dayjs(value)
  if (!parsed.isValid()) return value
  return parsed.fromNow()
}

export function formatBytes(bytes?: number | null): string {
  if (bytes === undefined || bytes === null) return '-'
  if (!Number.isFinite(bytes) || bytes < 0) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  if (unit === 0) return `${value} ${units[unit]}`
  return `${value.toFixed(1).replace(/\.0$/, '')} ${units[unit]}`
}

export function formatRate(bytesPerSecond?: number | null): string {
  if (bytesPerSecond === undefined || bytesPerSecond === null) return '-'
  return `${formatBytes(bytesPerSecond)}/s`
}

export interface HumanizedDuration {
  value: number
  unitKey: 'seconds' | 'minutes' | 'hours' | 'days'
}

export function humanizeSeconds(seconds: number): HumanizedDuration {
  if (!Number.isFinite(seconds) || seconds <= 0) {
    return { value: 0, unitKey: 'seconds' }
  }
  if (seconds < 60) return { value: Math.floor(seconds), unitKey: 'seconds' }
  if (seconds < 3600) {
    return { value: Math.floor(seconds / 60), unitKey: 'minutes' }
  }
  if (seconds < 86400) return { value: Math.floor(seconds / 3600), unitKey: 'hours' }
  return { value: Math.floor(seconds / 86400), unitKey: 'days' }
}

export interface EmptyStateContext {
  canView: boolean
  isError: boolean
  itemCount: number
  /** undefined 表示后端没返回该字段，不猜测 */
  configured?: boolean
  enabled?: boolean
  uploadPaused?: boolean
  nodeOffline?: boolean
}

/** 区分未配置/未开启/无匹配/无权限/接口错误/暂停/节点离线七种空白原因（文档 3.2） */
export function emptyStateLabel(ctx: EmptyStateContext): string | undefined {
  if (!ctx.canView) return 'No permission to view content backups'
  if (ctx.itemCount > 0) return undefined
  if (ctx.isError) return 'Content backup request failed'
  if (ctx.configured === false) return 'Content backup is not configured'
  if (ctx.enabled === false) return 'Content capture is disabled'
  if (ctx.uploadPaused === true) return 'Uploads are paused'
  if (ctx.nodeOffline === true) return 'Storage node is offline'
  return 'No matching backup records'
}

/**
 * 空表的第二行文案。表里 0 行有两种完全不同的含义：真的一条都没有，
 * 和「这次没拿到数据」。后者写上「尚无存储节点上报心跳。」「没有待上传的任务。」
 * 就是替没拿到的数据下结论 —— 设计文档 9.3 禁止的假 0。
 *
 * 返回空串而不是 undefined：TableEmpty 对 undefined 会回落到
 * 「未找到记录，请调整筛选条件。」，那同样是在断言一个我们没查到的 0。
 * 此时真实原因由上方的标题和错误提示承担，这一行宁可不说话。
 */
export function emptyStateDescription(
  ctx: EmptyStateContext,
  noDataLabel: string
): string {
  if (!ctx.canView || ctx.isError) return ''
  return noDataLabel
}

export type PreviewRender =
  | { kind: 'empty'; contentType: string; bytes: number }
  | {
      kind: 'binary'
      contentType: string
      bytes: number
      truncated: boolean
      complete: boolean
    }
  | {
      kind: 'json'
      contentType: string
      text: string
      truncated: boolean
      complete: boolean
    }
  | {
      kind: 'sse'
      contentType: string
      text: string
      events: string[]
      truncated: boolean
      complete: boolean
    }
  | {
      kind: 'text'
      contentType: string
      text: string
      truncated: boolean
      complete: boolean
    }

export function isTextualContentType(contentType?: string): boolean {
  const value = (contentType ?? '').toLowerCase()
  if (value === '') return false
  if (value.startsWith('text/')) return true
  return (
    value.includes('json') ||
    value.includes('xml') ||
    value.includes('javascript') ||
    value.includes('x-www-form-urlencoded') ||
    value.includes('csv')
  )
}

function looksLikeBase64(body: string): boolean {
  const compact = body.replace(/\s/g, '')
  if (compact.length === 0 || compact.length % 4 !== 0) return false
  return /^[A-Za-z0-9+/]+={0,2}$/.test(compact)
}

function decodePreviewBytes(
  side: ContentBackupPreviewSide
): Uint8Array | undefined {
  const body = side.body
  if (typeof body !== 'string' || body === '') return undefined
  const encoding = (side.encoding ?? '').toLowerCase()
  const asBase64 =
    encoding === 'base64' || (encoding === '' && looksLikeBase64(body))
  if (asBase64) {
    try {
      const binary = atob(body.replace(/\s/g, ''))
      const bytes = new Uint8Array(binary.length)
      for (let index = 0; index < binary.length; index += 1) {
        bytes[index] = binary.charCodeAt(index)
      }
      return bytes
    } catch {
      if (encoding === 'base64') return undefined
    }
  }
  return new TextEncoder().encode(body)
}

/**
 * 客户正文永远当纯文本处理：这里只产出字符串，组件用 React 默认转义渲染，
 * HTML/Markdown 一律不解析，避免客户内容变成 XSS 载体。
 */
export function renderPreviewSide(
  side?: ContentBackupPreviewSide
): PreviewRender {
  const contentType = side?.content_type ?? ''
  if (!side) return { kind: 'empty', contentType, bytes: 0 }
  const truncated = side.truncated === true
  const complete = side.complete === true
  const bytes = decodePreviewBytes(side)
  if (!bytes || bytes.length === 0) {
    return { kind: 'empty', contentType, bytes: side.captured_bytes ?? 0 }
  }
  if (!isTextualContentType(contentType)) {
    return {
      kind: 'binary',
      contentType,
      bytes: side.captured_bytes ?? bytes.length,
      truncated,
      complete,
    }
  }
  const text = new TextDecoder('utf-8', { fatal: false }).decode(bytes)
  if (contentType.toLowerCase().includes('event-stream')) {
    const events = text
      .split(/\r?\n\r?\n/)
      .map((event) => event.trim())
      .filter((event) => event !== '')
    return { kind: 'sse', contentType, text, events, truncated, complete }
  }
  // 截断的 JSON 不承诺可解析，只有完整且未截断才格式化
  if (contentType.toLowerCase().includes('json') && complete && !truncated) {
    try {
      const formatted = JSON.stringify(JSON.parse(text), null, 2)
      return { kind: 'json', contentType, text: formatted, truncated, complete }
    } catch {
      return { kind: 'text', contentType, text, truncated, complete }
    }
  }
  return { kind: 'text', contentType, text, truncated, complete }
}

/**
 * 原始会话值不进 URL、不进 localStorage，也不进 React Query 缓存键；
 * 这里只做缓存键区分用的非加密指纹。
 */
export function sessionFingerprint(value: string): string {
  if (!value) return ''
  let hash = 0x811c9dc5
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index)
    hash = Math.imul(hash, 0x01000193) >>> 0
  }
  return `s${hash.toString(16).padStart(8, '0')}-${value.length}`
}

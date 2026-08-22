/**
 * Utility functions for usage logs feature
 */
import {
  getAllLogs,
  getUserLogs,
  getAllMidjourneyLogs,
  getUserMidjourneyLogs,
  getAllTaskLogs,
  getUserTaskLogs,
} from '../api'
import {
  LOG_TYPES,
  DISPLAYABLE_LOG_TYPES,
  TIMING_LOG_TYPES,
} from '../constants'
import type {
  GetLogsParams,
  GetLogsResponse,
  FetchLogsConfig,
  GetMidjourneyLogsParams,
  GetTaskLogsParams,
} from '../types'

// ============================================================================
// Type Checkers & Utilities
// ============================================================================

/**
 * Check if log type is displayable (has detailed info)
 */
export function isDisplayableLogType(type: number): boolean {
  return (DISPLAYABLE_LOG_TYPES as readonly number[]).includes(type)
}

/**
 * Check if log type shows timing info
 */
export function isTimingLogType(type: number): boolean {
  return (TIMING_LOG_TYPES as readonly number[]).includes(type)
}

/**
 * Get log type configuration by type number
 */
export function getLogTypeConfig(type: number) {
  return LOG_TYPES.find((t) => t.value === type) || LOG_TYPES[0]
}

/**
 * Check if log uses per-call billing
 */
export function isPerCallBilling(modelPrice?: number): boolean {
  return (modelPrice ?? 0) > 0
}

/**
 * Get default time range (today 00:00:00 to now + 1 hour)
 */
export function getDefaultTimeRange(): { start: Date; end: Date } {
  const now = new Date()
  const start = new Date(now)
  start.setHours(0, 0, 0, 0)
  const end = new Date(now.getTime() + 3600 * 1000) // +1 hour

  return { start, end }
}

/**
 * Convert milliseconds timestamp to seconds for API
 */
function timestampToSeconds(ms: number): number {
  return Math.floor(ms / 1000)
}

/**
 * 容错解析 channel：剥掉外层引号并转 Number，兜底 0（= 后端"不过滤"）
 *
 * URL 不同入口可能给出 '80' / 80 / '"80"'（TanStack 字面量字符串经 JSON.stringify
 * 包了一层引号但因故未被还原回纯字符串等情况），统一归一化为整数
 */
function normalizeChannelId(raw: unknown): number {
  if (raw === undefined || raw === null || raw === '') return 0
  const stripped = String(raw).replace(/^"|"$/g, '').trim()
  const n = Number(stripped)
  return Number.isFinite(n) ? n : 0
}

/**
 * 读侧 trim：URL 里的字符串过滤值可能带前后空格（form encoding 的 `+` 解码而来，
 * 或老链接残留），后端 exact match 会因此失败。读取转 API 参数前统一 trim
 */
function trimText(raw: unknown): string {
  return String(raw ?? '').trim()
}

/**
 * Build query parameters from filters
 */
export function buildQueryParams(
  params: Record<string, unknown>
): URLSearchParams {
  const queryParams = new URLSearchParams()

  Object.entries(params).forEach(([key, value]) => {
    // Keep 0 as a valid value, only filter out undefined, null, and empty string
    if (value !== undefined && value !== null && value !== '') {
      queryParams.append(key, String(value))
    }
  })

  return queryParams
}

/**
 * Build time range parameters with default values
 * Shared logic for all log types
 */
function buildTimeRangeParams(
  searchParams: Record<string, unknown>,
  useMilliseconds: boolean
): { start_timestamp?: number; end_timestamp?: number } {
  const hasTimeParams = searchParams.startTime ?? searchParams.endTime
  const defaultTimeRange = !hasTimeParams ? getDefaultTimeRange() : null

  const convertTimestamp = (timestamp: number) =>
    useMilliseconds ? timestamp : timestampToSeconds(timestamp)

  const getTimestamp = (paramTime?: unknown, defaultTime?: Date) => {
    const time = (paramTime as number) || defaultTime?.getTime()
    return time ? convertTimestamp(time) : undefined
  }

  return {
    start_timestamp: getTimestamp(
      searchParams.startTime,
      defaultTimeRange?.start
    ),
    end_timestamp: getTimestamp(searchParams.endTime, defaultTimeRange?.end),
  }
}

/**
 * Build base parameters with time range (for drawing and task logs)
 * @param useMilliseconds - Whether to use millisecond timestamps (true for drawing logs, false for task logs)
 */
export function buildBaseParams(config: {
  page: number
  pageSize: number
  searchParams: Record<string, unknown>
  useMilliseconds?: boolean
}): {
  p: number
  page_size: number
  channel_id?: string
  start_timestamp?: number
  end_timestamp?: number
} {
  const { page, pageSize, searchParams, useMilliseconds = false } = config

  return {
    p: page,
    page_size: pageSize,
    ...(searchParams.channel
      ? {
          channel_id: String(normalizeChannelId(searchParams.channel) || ''),
        }
      : {}),
    ...buildTimeRangeParams(searchParams, useMilliseconds),
  }
}

/**
 * Build API params from search params and column filters (for common logs)
 */
export function buildApiParams(config: {
  page: number
  pageSize: number
  searchParams: Record<string, unknown>
  columnFilters?: Array<{ id: string; value: unknown }>
  isAdmin: boolean
}): GetLogsParams {
  const { page, pageSize, searchParams, columnFilters = [], isAdmin } = config

  // Helper to process type parameter (single value from array)
  const processType = (value: unknown) => {
    if (Array.isArray(value) && value.length === 1) {
      return Number(value[0])
    }
    return undefined
  }

  // Build base params from search params
  const model = trimText(searchParams.model)
  const token = trimText(searchParams.token)
  const group = trimText(searchParams.group)
  const username = trimText(searchParams.username)
  const requestId = trimText(searchParams.requestId)
  const params: GetLogsParams = {
    p: page,
    page_size: pageSize,
    ...(searchParams.type ? { type: processType(searchParams.type) } : {}),
    ...(model && { model_name: model }),
    ...(token && { token_name: token }),
    ...(group && { group }),
    ...(isAdmin && searchParams.channel
      ? { channel: normalizeChannelId(searchParams.channel) }
      : {}),
    ...(isAdmin && username && { username }),
    ...(requestId && { request_id: requestId }),
    ...buildTimeRangeParams(searchParams, false),
  }

  // Override with column filters if present
  if (columnFilters.length > 0) {
    columnFilters.forEach(({ id, value }) => {
      if (value === undefined || value === null || value === '') return
      const trimmed = typeof value === 'string' ? value.trim() : value
      if (trimmed === '') return

      switch (id) {
        case 'type':
          params.type = processType(value)
          break
        case 'model_name':
          params.model_name = String(trimmed)
          break
        case 'token_name':
          params.token_name = String(trimmed)
          break
        case 'group':
          params.group = String(trimmed)
          break
        case 'channel':
          if (isAdmin) params.channel = normalizeChannelId(value)
          break
        case 'username':
          if (isAdmin) params.username = String(trimmed)
          break
      }
    })
  }

  return params
}

// ============================================================================
// Data Fetching
// ============================================================================

/**
 * Fetch logs based on category type
 */
export async function fetchLogsByCategory(
  config: FetchLogsConfig
): Promise<GetLogsResponse> {
  const { logCategory, isAdmin, page, pageSize, searchParams, columnFilters } =
    config

  if (logCategory === 'common') {
    const params = buildApiParams({
      page,
      pageSize,
      searchParams,
      columnFilters,
      isAdmin,
    })
    return isAdmin ? await getAllLogs(params) : await getUserLogs(params)
  }

  // For drawing and task logs
  const baseParams = buildBaseParams({
    page,
    pageSize,
    searchParams,
    useMilliseconds: logCategory === 'drawing',
  })

  const filterText = trimText(searchParams.filter)
  const paramsWithFilter = {
    ...baseParams,
    ...(logCategory === 'drawing' && filterText ? { mj_id: filterText } : {}),
    ...(logCategory === 'task' && filterText ? { task_id: filterText } : {}),
  }

  if (logCategory === 'drawing') {
    return isAdmin
      ? await getAllMidjourneyLogs(paramsWithFilter as GetMidjourneyLogsParams)
      : await getUserMidjourneyLogs(paramsWithFilter as GetMidjourneyLogsParams)
  }

  // task logs
  return isAdmin
    ? await getAllTaskLogs(paramsWithFilter as GetTaskLogsParams)
    : await getUserTaskLogs(paramsWithFilter as GetTaskLogsParams)
}

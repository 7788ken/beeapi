/**
 * Utility functions for usage logs filters
 */
import { LOG_CATEGORY_LABELS } from '../constants'
import type {
  LogCategory,
  LogFilters,
  CommonLogFilters,
  DrawingLogFilters,
  TaskLogFilters,
} from '../types'

// ============================================================================
// Filter Building Functions
// ============================================================================

/**
 * 输入框值常带前后空格（粘贴或手误），URL 编码后空格→`+`，后端 exact match 失败。
 * Apply 入口统一 trim，空串视为未填写
 */
function trimStr(v: string | undefined): string | undefined {
  if (typeof v !== 'string') return v
  const t = v.trim()
  return t === '' ? undefined : t
}

/**
 * Build search params from filters based on log category
 */
export function buildSearchParams(
  filters: LogFilters,
  logCategory: LogCategory
): Record<string, unknown> {
  const channel = trimStr(filters.channel)
  const baseParams: Record<string, unknown> = {
    ...(filters.startTime && { startTime: filters.startTime.getTime() }),
    ...(filters.endTime && { endTime: filters.endTime.getTime() }),
    ...(channel && { channel }),
  }

  switch (logCategory) {
    case 'common': {
      const commonFilters = filters as CommonLogFilters
      const model = trimStr(commonFilters.model)
      const token = trimStr(commonFilters.token)
      const group = trimStr(commonFilters.group)
      const username = trimStr(commonFilters.username)
      const requestId = trimStr(commonFilters.requestId)
      return {
        ...baseParams,
        ...(model && { model }),
        ...(token && { token }),
        ...(group && { group }),
        ...(username && { username }),
        ...(requestId && { requestId }),
      }
    }
    case 'drawing': {
      const drawingFilters = filters as DrawingLogFilters
      const mjId = trimStr(drawingFilters.mjId)
      return {
        ...baseParams,
        ...(mjId && { filter: mjId }),
      }
    }
    case 'task': {
      const taskFilters = filters as TaskLogFilters
      const taskId = trimStr(taskFilters.taskId)
      return {
        ...baseParams,
        ...(taskId && { filter: taskId }),
      }
    }
    default:
      return baseParams
  }
}

/**
 * Get log category display name
 */
export function getLogCategoryLabel(category: LogCategory): string {
  return LOG_CATEGORY_LABELS[category]
}

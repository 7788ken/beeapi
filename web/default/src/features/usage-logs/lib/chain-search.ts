/**
 * 点开重试链路时的日志筛选。
 * 同一次请求的失败和成功共用 request_id，列表默认最新在前，
 * 这里改成从早到晚，第一条就是发起记录。
 * 重试可能跨几十分钟，时间窗再往前留一段，避免把首条裁掉。
 * 渠道和类型不带上：首条往往是别的渠道上的错误日志。
 */
export const CHAIN_LOOKBACK_MS = 3 * 60 * 60 * 1000

export type ChainLogSearchInput = {
  requestId: string
  createdAtSec?: number
  startTime?: number
  endTime?: number
  pageSize?: number
  model?: string
  token?: string
  group?: string
  username?: string
}

export type ChainLogSearch = {
  page: 1
  pageSize?: number
  requestId: string
  order: 'asc'
  startTime: number
  endTime: number
  model?: string
  token?: string
  group?: string
  username?: string
}

function present(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  return trimmed ? trimmed : undefined
}

export function buildChainLogSearch(input: ChainLogSearchInput): ChainLogSearch {
  const clickedMs =
    input.createdAtSec && input.createdAtSec > 0
      ? input.createdAtSec * 1000
      : Date.now()
  const lookbackStart = clickedMs - CHAIN_LOOKBACK_MS
  const startTime =
    input.startTime != null
      ? Math.min(input.startTime, lookbackStart)
      : lookbackStart
  const endTime =
    input.endTime != null ? Math.max(input.endTime, clickedMs) : clickedMs
  const model = present(input.model)
  const token = present(input.token)
  const group = present(input.group)
  const username = present(input.username)

  return {
    page: 1,
    ...(input.pageSize ? { pageSize: input.pageSize } : {}),
    requestId: input.requestId.trim(),
    order: 'asc',
    startTime,
    endTime,
    ...(model ? { model } : {}),
    ...(token ? { token } : {}),
    ...(group ? { group } : {}),
    ...(username ? { username } : {}),
  }
}

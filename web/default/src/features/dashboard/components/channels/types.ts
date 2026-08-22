// 渠道统计类型 — 与 controller/channel_statistics.go 中的 JSON 结构一致。

export type ChannelStatsSortBy =
  | 'quota'
  | 'call_count'
  | 'current_rpm'
  | 'peak_rpm'

export interface ChannelStatsItem {
  channel_id: number
  channel_name: string
  group: string
  status: number
  models: string[]
  quota: number
  call_count: number
  current_rpm: number
  peak_rpm: number
  peak_rpm_at: number
}

export interface ChannelStatsGroup {
  model_type: string
  total_quota: number
  total_calls: number
  channels: ChannelStatsItem[]
}

export interface ChannelStatsResp {
  groups: ChannelStatsGroup[]
  generated_at: number
  window_seconds: number
  sort_by: ChannelStatsSortBy
  top_n: number
}

export interface ChannelTrendBucket {
  channel_id: number
  bucket_start: number
  quota: number
  call_count: number
}

export interface ChannelTrendSeries {
  channel_id: number
  channel_name: string
  points: ChannelTrendBucket[]
}

export interface ChannelTrendResp {
  series: ChannelTrendSeries[]
  bucket_seconds: number
  start_ts: number
  end_ts: number
}

export type TrendMetric = 'quota' | 'call_count'

// 渠道卡片展开的 Top 用户排行 — 与 controller GetChannelTopUsers 的 JSON 一致。
export type TopUsersSortBy = 'quota' | 'rpm'

export interface ChannelTopUserItem {
  user_id: number
  username: string
  quota: number
  call_count: number
  rpm: number
  tokens: number
  last_seen: number
}

export interface ChannelTopUsersResp {
  channel_id: number
  channel_name: string
  window_seconds: number
  sort_by: TopUsersSortBy
  generated_at: number
  users: ChannelTopUserItem[]
}

export interface ChannelStatsFilters {
  rangeSeconds: number
  sortBy: ChannelStatsSortBy
  topN: number
  modelType: string // empty = all
}

// rangeSeconds 的特殊值，前端在调接口前换算成"自本地午夜到现在"的秒数。
export const RANGE_TODAY_SENTINEL = -1

export const RANGE_OPTIONS = [
  { value: 60, labelKey: 'Last 1 minute' },
  { value: RANGE_TODAY_SENTINEL, labelKey: 'Today' },
  { value: 24 * 3600, labelKey: 'Last 24 hours' },
] as const

// 把 filter 里可能的"今天"哨兵转成真实秒数；其它值原样返回。
// tz_offset_sec 默认取浏览器：-new Date().getTimezoneOffset()*60。
export function resolveRangeSeconds(rangeSeconds: number): number {
  if (rangeSeconds !== RANGE_TODAY_SENTINEL) return rangeSeconds
  const nowSec = Math.floor(Date.now() / 1000)
  const tzOffsetSec = -new Date().getTimezoneOffset() * 60
  // 当前本地时刻到今日 00:00:00 的秒数；下限 60，避免午夜刚过零点时落到 0 触发后端 min。
  return Math.max(60, (nowSec + tzOffsetSec) % 86400)
}

export const SORT_OPTIONS: { value: ChannelStatsSortBy; labelKey: string }[] = [
  { value: 'quota', labelKey: 'Consumption' },
  { value: 'call_count', labelKey: 'Call count' },
  { value: 'current_rpm', labelKey: 'Current RPM' },
  { value: 'peak_rpm', labelKey: 'Peak RPM' },
]

export const MODEL_TYPE_LABEL: Record<string, string> = {
  claude: 'Claude',
  codex: 'Codex',
  openai: 'OpenAI',
  gemini: 'Gemini',
  image: 'Image',
  video: 'Video',
  audio: 'Audio',
  embedding: 'Embedding',
  qwen: 'Qwen',
  deepseek: 'DeepSeek',
  moonshot: 'Moonshot',
  other: 'Other',
}

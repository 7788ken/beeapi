// 运行时大盘类型 — 与 service/runtime_metrics.go 中的 RuntimeSnapshot 一致。

export interface TopUserItem {
  user_id: number
  username: string
  rpm: number
  cost: number
  balance: number
  top_model: string
}

export interface TopGroupItem {
  group: string
  rpm: number
  cost: number
  user_count: number
  channel_used: number
}

export interface TopChannelItem {
  channel_id: number
  channel_name: string
  group: string
  rpm: number
  cost: number
  success_count: number
  error_count: number
  balance: number
}

export interface TopGroupChannelItem {
  group: string
  channel_id: number
  channel_name: string
  rpm: number
  cost: number
}

export interface RuntimeSnapshot {
  generated_at: number
  window_seconds: number
  elapsed_ms: number

  top_users_by_rpm: TopUserItem[]
  top_users_by_cost: TopUserItem[]
  top_users_by_balance: TopUserItem[]

  top_groups_by_rpm: TopGroupItem[]
  top_groups_by_cost: TopGroupItem[]

  top_channels_by_rpm: TopChannelItem[]
  top_channels_by_cost: TopChannelItem[]
  top_channels_by_balance: TopChannelItem[]

  top_group_channels_by_rpm: TopGroupChannelItem[]
  top_group_channels_by_cost: TopGroupChannelItem[]
}

export type RuntimeSortMode = 'rpm' | 'cost' | 'balance'

// 渠道对账类型 — 与 controller/channel_reconcile.go 的 JSON 结构一致。

export interface ChannelReconcileModelItem {
  model_name: string
  quota: number
  success_count: number
  error_count: number
  timeout_count: number
}

export interface ChannelReconcileItem {
  channel_id: number
  channel_name: string
  status: number
  quota: number
  success_count: number
  error_count: number
  timeout_count: number
  models: ChannelReconcileModelItem[]
}

export interface ChannelReconcileTotal {
  quota: number
  success_count: number
  error_count: number
  timeout_count: number
}

export interface ChannelReconcileResp {
  channels: ChannelReconcileItem[]
  total: ChannelReconcileTotal
  start_ts: number
  end_ts: number
  generated_at: number
}

// 上游账单 — 与 controller/channel_reconcile_bill.go 的 JSON 结构一致。
// 数据来自 balance 面板 /api/balance/reconciliation，金额为 USD（已含各账号充值比例换算）。
export interface UpstreamBillAccount {
  name: string
  platform: string
  success: boolean
  yesterday: number
  today: number
  yesterday_estimated: boolean
  error?: string
}

// 站点内按令牌绑定：渠道对应的上游令牌当日扣费（USD，含充值比例）。
export interface UpstreamChannelBill {
  keyname: string
  amount: number
  account: string
  shared: number
  via?: 'key' | 'name'
}

export interface UpstreamBillResp {
  configured: boolean
  day?: 'today' | 'yesterday'
  day_date?: string
  as_of?: string
  timezone?: string
  yesterday_date?: string
  today_date?: string
  accounts?: UpstreamBillAccount[]
  channel_bills?: Record<string, UpstreamChannelBill>
  detail_failed?: string[]
  total_yesterday: number
  total_today: number
  generated_at: number
}

export function formatUsd(usd: number): string {
  return `$${usd.toFixed(2)}`
}

export function localDateStr(offsetDays = 0): string {
  const d = new Date()
  d.setDate(d.getDate() + offsetDays)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

// 本地日期 → [当日本地零点, min(次日零点, now)]；午夜刚过时兜底 60s，满足后端 start < end。
export function dayRangeOf(date: string): { startTs: number; endTs: number } {
  const startTs = Math.floor(new Date(`${date}T00:00:00`).getTime() / 1000)
  const nowTs = Math.floor(Date.now() / 1000)
  const endTs = Math.min(startTs + 86400, Math.max(nowTs, startTs + 60))
  return { startTs, endTs }
}

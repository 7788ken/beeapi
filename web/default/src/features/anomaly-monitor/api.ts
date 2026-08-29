import { api } from '@/lib/api'

export interface AnomalyLog {
  id: number
  user_id: number
  username: string
  token_name: string
  model_name: string
  channel_id: number
  quota: number
  prompt_tokens: number
  completion_tokens: number
  created_at: number
  request_id: string
  anomaly_type: string
  anomaly_detail: string
  other: string
}

export interface AnomalyStats {
  client_disconnect: number
  high_cost: number
  channel_demote: number
  channel_disable: number
  channel_upgrade: number
  total_count: number
  total_quota: number
}

export interface AnomalyQuery {
  type?: string
  p?: number
  page_size?: number
  start_timestamp?: number
  end_timestamp?: number
  threshold?: number
  username?: string
  model_name?: string
  channel_id?: number
}

export async function fetchAnomalyLogs(params: AnomalyQuery) {
  const res = await api.get<{ success: boolean; data: AnomalyLog[]; total: number; page_quota_sum: number }>('/api/anomaly/', { params })
  return res.data
}

export async function fetchAnomalyStats(params: { start_timestamp?: number; end_timestamp?: number }) {
  const res = await api.get<{ success: boolean; data: AnomalyStats }>('/api/anomaly/stats', { params })
  return res.data
}

export interface AnomalySummary {
  total_count: number
  total_quota: number
}

export async function fetchAnomalySummary(params: AnomalyQuery) {
  const res = await api.get<{ success: boolean; data: AnomalySummary }>('/api/anomaly/summary', { params })
  return res.data
}

export function getExportUrl(params: AnomalyQuery): string {
  const searchParams = new URLSearchParams()
  if (params.type) searchParams.set('type', params.type)
  if (params.start_timestamp) searchParams.set('start_timestamp', String(params.start_timestamp))
  if (params.end_timestamp) searchParams.set('end_timestamp', String(params.end_timestamp))
  if (params.username) searchParams.set('username', params.username)
  if (params.model_name) searchParams.set('model_name', params.model_name)
  if (params.channel_id) searchParams.set('channel_id', String(params.channel_id))
  return `/api/anomaly/export?${searchParams.toString()}`
}

export interface ChannelOption {
  id: number
  name: string
}

export async function fetchChannelOptions(): Promise<ChannelOption[]> {
  try {
    const res = await api.get<{ success: boolean; data: { items: Array<{ id: number; name: string }> } }>('/api/channel/', {
      params: { p: 1, page_size: 200 },
      // 渠道下拉只是筛选便利项；管理员没有「查看渠道」权限时会 403，静默降级成空列表
      skipErrorHandler: true,
    } as Parameters<typeof api.get>[1])
    if (res.data.success && Array.isArray(res.data.data?.items)) {
      return res.data.data.items.map(ch => ({ id: ch.id, name: ch.name }))
    }
  } catch {}
  return []
}

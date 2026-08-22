import { api } from '@/lib/api'
import type { ChannelReconcileResp, UpstreamBillResp } from './types'

interface ApiResp<T> {
  success: boolean
  message?: string
  data?: T
}

export async function fetchChannelReconcile(params: {
  startTs: number
  endTs: number
}): Promise<ApiResp<ChannelReconcileResp>> {
  const res = await api.get('/api/channel/reconcile', {
    params: { start_ts: params.startTs, end_ts: params.endTs },
  })
  return res.data
}

// 上游账单（balance 面板数据源）：未配置时返回 configured=false；
// 配置后查询失败要以错误暴露（抛出让 React Query 进入 isError），不静默隐藏。
export async function fetchChannelReconcileUpstreamBill(
  day: 'today' | 'yesterday'
): Promise<UpstreamBillResp> {
  const res = await api.get('/api/channel/reconcile/upstream_bill', {
    params: { day },
  })
  const body = res.data as ApiResp<UpstreamBillResp>
  if (!body.success || !body.data) {
    throw new Error(body.message || 'upstream bill query failed')
  }
  return body.data
}

import type { AxiosRequestConfig } from 'axios'
import { api } from '@/lib/api'
import type { ApiResponse } from '@/features/subscriptions/types'
import type { GroupUptimeResponse, SelfGroupsResponse } from './types'

// 侧栏「可用分组」入口在每个控制台页面都会拉这两份数据：失败不弹全局 toast，
// 业务失败（HTTP 200 + success:false）抛错进查询的 error 态，由页面自己如实展示
const silentConfig = {
  skipErrorHandler: true,
  skipBusinessError: true,
} as AxiosRequestConfig

async function getData<T>(
  url: string,
  params?: Record<string, unknown>
): Promise<T> {
  const res = await api.get(url, { ...silentConfig, params })
  const body = res.data as ApiResponse<T>
  if (!body?.success) throw new Error(body?.message || `GET ${url} failed`)
  // Go 的 nil map 会编码成 null，语义就是空
  return (body.data ?? {}) as T
}

export function getSelfGroups(): Promise<SelfGroupsResponse> {
  return getData('/api/user/self/groups')
}

export function getGroupUptime(hours = 24): Promise<GroupUptimeResponse> {
  return getData('/api/perf-metrics/groups', { hours })
}

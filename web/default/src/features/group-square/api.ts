import { api } from '@/lib/api'
import type { ApiResponse } from '@/features/subscriptions/types'
import type { GroupUptimeResponse, SelfGroupsResponse } from './types'

export async function getSelfGroups(): Promise<
  ApiResponse<SelfGroupsResponse>
> {
  const res = await api.get('/api/user/self/groups')
  return res.data
}

export async function getGroupUptime(
  hours = 24
): Promise<ApiResponse<GroupUptimeResponse>> {
  const res = await api.get('/api/perf-metrics/groups', { params: { hours } })
  return res.data
}

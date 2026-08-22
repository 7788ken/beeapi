import { api } from '@/lib/api'

// 用户级 RPM 限流规则（每条 = 一个 (user_id, channel_id) 配额）
export interface UserRpmRule {
  id: number
  user_id: number
  channel_id: number
  base_rpm: number
  jitter_pct: number
  min_delay_ms: number
  max_delay_ms: number
  enabled: boolean
  created_at: number
  updated_at: number
}

export interface ListUserRpmResponse {
  success: boolean
  message?: string
  data?: {
    items: UserRpmRule[]
    total: number
  }
}

export interface MutateUserRpmRequest {
  user_id?: number
  base_rpm?: number
  jitter_pct?: number
  min_delay_ms?: number
  max_delay_ms?: number
  enabled?: boolean
}

interface SimpleResponse<T> {
  success: boolean
  message?: string
  data?: T
}

export async function listUserRpmRules(channelId: number): Promise<ListUserRpmResponse> {
  const res = await api.get(`/api/channel/${channelId}/user_rpm`)
  return res.data
}

export async function createUserRpmRule(
  channelId: number,
  body: MutateUserRpmRequest
): Promise<SimpleResponse<UserRpmRule>> {
  const res = await api.post(`/api/channel/${channelId}/user_rpm`, body)
  return res.data
}

export async function updateUserRpmRule(
  channelId: number,
  ruleId: number,
  body: MutateUserRpmRequest
): Promise<SimpleResponse<{ id: number }>> {
  const res = await api.put(`/api/channel/${channelId}/user_rpm/${ruleId}`, body)
  return res.data
}

export async function deleteUserRpmRule(
  channelId: number,
  ruleId: number
): Promise<SimpleResponse<{ id: number }>> {
  const res = await api.delete(`/api/channel/${channelId}/user_rpm/${ruleId}`)
  return res.data
}

export const userRpmQueryKey = (channelId: number) =>
  ['channel', channelId, 'user_rpm'] as const

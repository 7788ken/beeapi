import { api } from '@/lib/api'
import type {
  ApiResponse,
  PlanRecord,
  PlanPayload,
  UserSubscriptionRecord,
  UserSubscription,
  CreateUserSubscriptionRequest,
  ResetUserSubscriptionsRequest,
  ResetPlanSubscriptionsRequest,
  SubscriptionResetResult,
  SubscriptionPayResponse,
  SubscriptionPayRequest,
  SelfSubscriptionData,
} from './types'

// ============================================================================
// Admin Plan Management
// ============================================================================

export async function getAdminPlans(): Promise<ApiResponse<PlanRecord[]>> {
  const res = await api.get('/api/subscription/admin/plans')
  return res.data
}

export async function createPlan(
  data: PlanPayload
): Promise<ApiResponse<PlanRecord>> {
  const res = await api.post('/api/subscription/admin/plans', data)
  return res.data
}

export async function updatePlan(
  id: number,
  data: PlanPayload
): Promise<ApiResponse<PlanRecord>> {
  const res = await api.put(`/api/subscription/admin/plans/${id}`, data)
  return res.data
}

export async function patchPlanStatus(
  id: number,
  enabled: boolean
): Promise<ApiResponse> {
  const res = await api.patch(`/api/subscription/admin/plans/${id}`, {
    enabled,
  })
  return res.data
}

// ============================================================================
// Admin User Subscription Management
// ============================================================================

export async function getUserSubscriptions(
  userId: number
): Promise<ApiResponse<UserSubscriptionRecord[]>> {
  const res = await api.get(
    `/api/subscription/admin/users/${userId}/subscriptions`
  )
  return res.data
}

export async function createUserSubscription(
  userId: number,
  data: CreateUserSubscriptionRequest
): Promise<ApiResponse<{ message?: string }>> {
  const res = await api.post(
    `/api/subscription/admin/users/${userId}/subscriptions`,
    data
  )
  return res.data
}

export async function invalidateUserSubscription(
  subId: number
): Promise<ApiResponse<{ message?: string }>> {
  const res = await api.post(
    `/api/subscription/admin/user_subscriptions/${subId}/invalidate`
  )
  return res.data
}

export async function updateUserSubscriptionExpiry(
  subId: number,
  endTime: number
): Promise<ApiResponse<{ message?: string }>> {
  const res = await api.patch(
    `/api/subscription/admin/user_subscriptions/${subId}/expiry`,
    { end_time: endTime }
  )
  return res.data
}

export async function deleteUserSubscription(
  subId: number
): Promise<ApiResponse> {
  const res = await api.delete(
    `/api/subscription/admin/user_subscriptions/${subId}`
  )
  return res.data
}

export async function resetUserSubscriptionsByPlan(
  userId: number,
  data: ResetUserSubscriptionsRequest
): Promise<ApiResponse<SubscriptionResetResult>> {
  const res = await api.post(
    `/api/subscription/admin/users/${userId}/subscriptions/reset`,
    data
  )
  return res.data
}

export async function resetPlanSubscriptions(
  planId: number,
  data: ResetPlanSubscriptionsRequest
): Promise<ApiResponse<SubscriptionResetResult>> {
  const res = await api.post(
    `/api/subscription/admin/plans/${planId}/subscriptions/reset`,
    data
  )
  return res.data
}

// ============================================================================
// Admin Global User Subscription List + Group Budget
// ============================================================================

export interface AdminUserSubscriptionRow extends UserSubscription {
  username?: string
  plan_title?: string
  plan_bound_group?: string
}

export interface ListUserSubscriptionsResp {
  items: AdminUserSubscriptionRow[]
  total: number
  page: number
  page_size: number
}

export async function listAllUserSubscriptions(params: {
  status?: string
  username?: string
  bound_group?: string
  page?: number
  page_size?: number
}): Promise<ApiResponse<ListUserSubscriptionsResp>> {
  const res = await api.get('/api/subscription/admin/user_subscriptions', {
    params,
  })
  return res.data
}

export async function listBoundGroups(): Promise<ApiResponse<string[]>> {
  const res = await api.get('/api/subscription/admin/bound_groups')
  return res.data
}

export interface GroupBudgetRow {
  bound_group: string
  active_count: number
  daily_quota: number
  daily_price_usd: number
  currency: string
}

export async function getSubscriptionGroupBudget(): Promise<
  ApiResponse<GroupBudgetRow[]>
> {
  const res = await api.get('/api/subscription/admin/group_budget')
  return res.data
}

// ============================================================================
// User-facing Subscription Payment
// ============================================================================

export async function paySubscriptionStripe(
  data: SubscriptionPayRequest
): Promise<SubscriptionPayResponse> {
  const res = await api.post('/api/subscription/stripe/pay', data)
  return res.data
}

export async function paySubscriptionCreem(
  data: SubscriptionPayRequest
): Promise<SubscriptionPayResponse> {
  const res = await api.post('/api/subscription/creem/pay', data)
  return res.data
}

export async function paySubscriptionEpay(
  data: SubscriptionPayRequest & { payment_method: string }
): Promise<SubscriptionPayResponse & { url?: string }> {
  const res = await api.post('/api/subscription/epay/pay', data)
  return {
    ...res.data,
    url: res.data.url || (res as unknown as { url?: string }).url,
  }
}

export async function paySubscriptionByBalance(
  data: SubscriptionPayRequest
): Promise<ApiResponse<{ trade_no?: string }>> {
  const res = await api.post('/api/subscription/balance/pay', data)
  return res.data
}

// ============================================================================
// User Self Subscriptions
// ============================================================================

export async function getSelfSubscriptions(): Promise<
  ApiResponse<UserSubscriptionRecord[]>
> {
  const res = await api.get('/api/subscription/self')
  return res.data
}

export async function getSelfSubscriptionFull(): Promise<
  ApiResponse<SelfSubscriptionData>
> {
  const res = await api.get('/api/subscription/self')
  return res.data
}

// 用户软删除自己已过期/已取消的订阅（is_hidden=true，不占用限购名额）。
export async function hideSelfSubscription(
  subId: number
): Promise<ApiResponse<{ id: number; is_hidden: boolean }>> {
  const res = await api.delete(`/api/subscription/self/${subId}`)
  return res.data
}

export async function getPublicPlans(): Promise<ApiResponse<PlanRecord[]>> {
  const res = await api.get('/api/subscription/plans')
  return res.data
}

export async function updateBillingPreference(
  preference: string
): Promise<ApiResponse<{ billing_preference?: string }>> {
  const res = await api.put('/api/subscription/self/preference', {
    billing_preference: preference,
  })
  return res.data
}

export async function getGroups(): Promise<ApiResponse<string[]>> {
  const res = await api.get('/api/group')
  return res.data
}

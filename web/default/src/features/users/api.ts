import { api } from '@/lib/api'
import type {
  User,
  GetUsersParams,
  GetUsersResponse,
  SearchUsersParams,
  UserFormData,
  ManageUserAction,
  ManageUserQuotaPayload,
  ApiResponse,
} from './types'

// ============================================================================
// User Management APIs
// ============================================================================

/**
 * Get paginated users list, with optional backend-driven ORDER BY.
 * 后端有白名单：id / created_at / last_login_at / used_quota / request_count / rpm_24h / quota。
 */
export async function getUsers(
  params: GetUsersParams = {}
): Promise<GetUsersResponse> {
  const { p = 1, page_size = 10, order_by, order } = params
  const qs = new URLSearchParams()
  qs.set('p', String(p))
  qs.set('page_size', String(page_size))
  if (order_by) qs.set('order_by', order_by)
  if (order) qs.set('order', order)
  const res = await api.get(`/api/user/?${qs.toString()}`)
  return res.data
}

/**
 * 手动触发用户 RPM 重算（需「管理用户」权限，CriticalRateLimit）。
 * 调用方按 best-effort 处理；只有「调整额度」权限的管理员会 403，
 * 所以跳过全局错误 toast，别为一个刷新副作用弹红条。
 */
export async function recomputeUserMetrics(): Promise<ApiResponse> {
  const res = await api.post(`/api/user/recompute_metrics`, undefined, {
    skipErrorHandler: true,
  } as Parameters<typeof api.post>[2])
  return res.data
}

/**
 * Search users by keyword or group
 */
export async function searchUsers(
  params: SearchUsersParams
): Promise<GetUsersResponse> {
  const { keyword = '', group = '', p = 1, page_size = 10 } = params
  const res = await api.get(
    `/api/user/search?keyword=${keyword}&group=${group}&p=${p}&page_size=${page_size}`
  )
  return res.data
}

/**
 * Get single user by ID
 */
export async function getUser(id: number): Promise<ApiResponse<User>> {
  const res = await api.get(`/api/user/${id}`)
  return res.data
}

/**
 * Get pinned users (admin only)
 */
export async function getPinnedUsers(): Promise<ApiResponse<User[]>> {
  const res = await api.get('/api/user/pinned')
  return res.data
}

/**
 * Create a new user
 */
export async function createUser(
  data: UserFormData
): Promise<ApiResponse<User>> {
  const res = await api.post('/api/user/', data)
  return res.data
}

/**
 * Update an existing user
 */
export async function updateUser(
  data: UserFormData & { id: number }
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.put('/api/user/', data)
  return res.data
}

/**
 * Delete a single user (hard delete)
 */
export async function deleteUser(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/`)
  return res.data
}

/**
 * Manage user (promote, demote, enable, disable, delete)
 */
export async function manageUser(
  id: number,
  action: ManageUserAction
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.post('/api/user/manage', { id, action })
  return res.data
}

/**
 * Adjust user quota atomically (add/subtract/override)
 */
export async function adjustUserQuota(
  payload: ManageUserQuotaPayload
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.post('/api/user/manage', payload)
  return res.data
}

/**
 * Update an admin's fine-grained permissions (super admin only)
 */
export async function updateUserAdminPerms(
  id: number,
  adminPerms: string[]
): Promise<ApiResponse<string[]>> {
  const res = await api.put(`/api/user/${id}/admin_perms`, {
    admin_perms: adminPerms,
  })
  return res.data
}

/**
 * Reset user's Passkey registration
 */
export async function resetUserPasskey(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/reset_passkey`)
  return res.data
}

/**
 * Reset user's Two-Factor Authentication setup
 */
export async function resetUserTwoFA(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/2fa`)
  return res.data
}

/**
 * Get all available groups
 */
export async function getGroups(): Promise<ApiResponse<string[]>> {
  const res = await api.get('/api/group/')
  return res.data
}

// ============================================================================
// Admin Binding Management APIs
// ============================================================================

export interface OAuthBinding {
  provider_id: string
  provider_name: string
  user_id?: number
  external_id?: string
}

/**
 * Get user's custom OAuth bindings (admin)
 */
export async function getUserOAuthBindings(
  userId: number
): Promise<ApiResponse<OAuthBinding[]>> {
  const res = await api.get(`/api/user/${userId}/oauth/bindings`)
  return res.data
}

/**
 * Clear a user's built-in binding (admin)
 */
export async function adminClearUserBinding(
  userId: number,
  bindingType: string
): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${userId}/bindings/${bindingType}`)
  return res.data
}

/**
 * Unbind custom OAuth for a user (admin)
 */
export async function adminUnbindCustomOAuth(
  userId: number,
  providerId: string
): Promise<ApiResponse> {
  const res = await api.delete(
    `/api/user/${userId}/oauth/bindings/${providerId}`
  )
  return res.data
}

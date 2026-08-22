import { api } from '@/lib/api'
import type { ApiResponse } from '@/features/subscriptions/types'
import type {
  OptionEntry,
  SensitiveAuditStats,
  SensitiveBlockLog,
  SensitiveWord,
} from './types'

/**
 * Backend pagination shape (see common/page_info.go):
 *   { page, page_size, total, items: [...] }
 */
interface PageEnvelope<T> {
  page: number
  page_size: number
  total: number
  items: T[]
}

// ============================================================================
// Words
// ============================================================================

export interface WordsQuery {
  page: number
  pageSize: number
  keyword?: string
}

export async function listWords(
  q: WordsQuery
): Promise<ApiResponse<PageEnvelope<SensitiveWord>>> {
  const params = new URLSearchParams()
  params.set('p', String(q.page))
  params.set('page_size', String(q.pageSize))
  if (q.keyword) params.set('keyword', q.keyword)
  const res = await api.get(`/api/sensitive_word/?${params.toString()}`)
  return res.data
}

/**
 * Matches backend `sensitiveWordPayload` in controller/sensitive.go.
 * `pattern` is the keyword; `enabled` and `action` go through pointer optionals
 * on the backend so omitting them keeps current/default values.
 */
export interface WordPayload {
  pattern: string
  description?: string
  is_regex: boolean
  action: number
  enabled: boolean
}

export async function createWord(
  payload: WordPayload
): Promise<ApiResponse<SensitiveWord>> {
  const res = await api.post('/api/sensitive_word/', payload)
  return res.data
}

export async function updateWord(
  id: number,
  payload: WordPayload
): Promise<ApiResponse<SensitiveWord>> {
  const res = await api.put(`/api/sensitive_word/${id}`, payload)
  return res.data
}

export async function deleteWord(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/sensitive_word/${id}`)
  return res.data
}

export async function toggleWord(
  id: number
): Promise<ApiResponse<SensitiveWord>> {
  // Backend handler is empty-body PUT /:id/toggle and inverts `enabled`.
  const res = await api.put(`/api/sensitive_word/${id}/toggle`)
  return res.data
}

// ============================================================================
// Block records
// ============================================================================

export interface BlocksQuery {
  page: number
  pageSize: number
}

export async function listBlocks(
  q: BlocksQuery
): Promise<ApiResponse<PageEnvelope<SensitiveBlockLog>>> {
  const params = new URLSearchParams()
  params.set('p', String(q.page))
  params.set('page_size', String(q.pageSize))
  const res = await api.get(`/api/sensitive_block/?${params.toString()}`)
  return res.data
}

export async function getBlockBody(
  id: number
): Promise<ApiResponse<{ body: string }>> {
  const res = await api.get(`/api/sensitive_block/${id}/body`)
  return res.data
}

/**
 * POST /api/sensitive_block/:id/toggle_token  body: { disabled: boolean }
 * Returns: { token_disabled: boolean }
 */
export async function toggleBlockToken(
  id: number,
  disabled: boolean
): Promise<ApiResponse<{ token_disabled: boolean }>> {
  const res = await api.post(`/api/sensitive_block/${id}/toggle_token`, {
    disabled,
  })
  return res.data
}

// ============================================================================
// Stats + Options
// ============================================================================

export async function getStats(): Promise<ApiResponse<SensitiveAuditStats>> {
  const res = await api.get('/api/sensitive_block/stats')
  return res.data
}

export async function getOptions(): Promise<ApiResponse<OptionEntry[]>> {
  const res = await api.get('/api/option/')
  return res.data
}

export async function setOption(
  key: string,
  value: string | boolean | number
): Promise<ApiResponse> {
  const res = await api.put('/api/option/', { key, value })
  return res.data
}

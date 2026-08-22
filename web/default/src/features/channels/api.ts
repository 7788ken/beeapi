import type { AxiosRequestConfig } from 'axios'
import { api, getDashboardAuthHeaders } from '@/lib/api'
import { getGroups as getUserGroups } from '@/features/users/api'
import type {
  AddChannelRequest,
  BatchDeleteParams,
  BatchSetTagParams,
  Channel,
  ChannelBalanceResponse,
  ChannelTestResponse,
  CopyChannelParams,
  CopyChannelResponse,
  FetchModelsResponse,
  GetChannelResponse,
  GetChannelsParams,
  GetChannelsResponse,
  MultiKeyManageParams,
  MultiKeyStatusResponse,
  SearchChannelsParams,
  SearchChannelsResponse,
  TagOperationParams,
  ListVerifyReportsResponse,
  GetVerifyReportResponse,
  VerifySSEEvent,
} from './types'

// Extended API config types
interface ExtendedApiConfig extends AxiosRequestConfig {
  skipBusinessError?: boolean
  disableDuplicate?: boolean
}

export type CodexOAuthStartResponse = {
  success: boolean
  message?: string
  data?: {
    authorize_url?: string
  }
}

export type CodexOAuthCompleteResponse = {
  success: boolean
  message?: string
  data?: {
    key?: string
    account_id?: string
    email?: string
    expires_at?: string
    last_refresh?: string
  }
}

export type CodexUsageResponse = {
  success: boolean
  message?: string
  upstream_status?: number
  data?: Record<string, unknown>
}

export type CodexCredentialRefreshResponse = {
  success: boolean
  message?: string
  data?: {
    expires_at?: string
    last_refresh?: string
    account_id?: string
    email?: string
    channel_id?: number
    channel_type?: number
    channel_name?: string
  }
}

// ============================================================================
// Base Channel CRUD Operations
// ============================================================================

/**
 * Get paginated list of channels
 */
export async function getChannels(
  params: GetChannelsParams = {}
): Promise<GetChannelsResponse> {
  const res = await api.get('/api/channel', { params })
  return res.data
}

/**
 * Search channels with filters
 */
export async function searchChannels(
  params: SearchChannelsParams
): Promise<SearchChannelsResponse> {
  const res = await api.get('/api/channel/search', { params })
  return res.data
}

/**
 * Get single channel by ID
 */
export async function getChannel(id: number): Promise<GetChannelResponse> {
  const res = await api.get(`/api/channel/${id}`)
  return res.data
}

/**
 * Create new channel(s)
 * Supports single, batch, and multi-key modes
 */
export async function createChannel(
  data: AddChannelRequest
): Promise<{ success: boolean; message?: string }> {
  const res = await api.post('/api/channel', data)
  return res.data
}

/**
 * Update existing channel
 */
export async function updateChannel(
  id: number,
  data: Partial<Channel>
): Promise<{ success: boolean; message?: string; data?: Channel }> {
  const res = await api.put('/api/channel/', { id, ...data })
  return res.data
}

/**
 * Delete single channel
 */
export async function deleteChannel(
  id: number
): Promise<{ success: boolean; message?: string }> {
  const res = await api.delete(`/api/channel/${id}`)
  return res.data
}

/**
 * Batch delete channels
 */
export async function batchDeleteChannels(
  data: BatchDeleteParams
): Promise<{ success: boolean; message?: string; data?: number }> {
  const res = await api.post('/api/channel/batch', data)
  return res.data
}

/**
 * Batch set tag for channels
 */
export async function batchSetChannelTag(
  data: BatchSetTagParams
): Promise<{ success: boolean; message?: string; data?: number }> {
  const res = await api.post('/api/channel/batch/tag', data)
  return res.data
}

// ============================================================================
// Channel Operations
// ============================================================================

/**
 * Test channel connectivity
 */
export async function testChannel(
  id: number,
  params?: { model?: string; endpoint_type?: string; stream?: boolean }
): Promise<ChannelTestResponse> {
  const res = await api.get(`/api/channel/test/${id}`, { params })
  return res.data
}

/**
 * Update channel balance
 */
export async function updateChannelBalance(
  id: number
): Promise<ChannelBalanceResponse> {
  const res = await api.get(`/api/channel/update_balance/${id}`)
  return res.data
}

/**
 * Fetch available models from upstream provider
 */
export async function fetchUpstreamModels(
  id: number
): Promise<FetchModelsResponse> {
  const res = await api.get(`/api/channel/fetch_models/${id}`)
  return res.data
}

/**
 * Copy/clone a channel
 */
export async function copyChannel(
  id: number,
  params: CopyChannelParams = {}
): Promise<CopyChannelResponse> {
  const res = await api.post(`/api/channel/copy/${id}`, null, { params })
  return res.data
}

/**
 * Fix channel abilities
 */
export async function fixChannelAbilities(): Promise<{
  success: boolean
  message?: string
  data?: { success: number; fails: number }
}> {
  const res = await api.post('/api/channel/fix')
  return res.data
}

/**
 * Delete all disabled channels
 */
export async function deleteDisabledChannels(): Promise<{
  success: boolean
  message?: string
  data?: number
}> {
  const res = await api.delete('/api/channel/disabled')
  return res.data
}

/**
 * Get channel key (requires 2FA verification)
 */
export async function getChannelKey(
  id: number,
  proof?: string
): Promise<{ success: boolean; message?: string; data?: { key: string } }> {
  const res = await api.post(
    `/api/channel/${id}/key`,
    {},
    proof ? { headers: { 'X-Security-Proof': proof } } : undefined
  )
  return res.data
}

// ============================================================================
// Codex Channel Operations
// ============================================================================

export async function startCodexOAuth(): Promise<CodexOAuthStartResponse> {
  const config: ExtendedApiConfig = { skipBusinessError: true }
  const res = await api.post('/api/channel/codex/oauth/start', {}, config)
  return res.data
}

export async function completeCodexOAuth(
  input: string
): Promise<CodexOAuthCompleteResponse> {
  const config: ExtendedApiConfig = { skipBusinessError: true }
  const res = await api.post(
    '/api/channel/codex/oauth/complete',
    { input },
    config
  )
  return res.data
}

export async function refreshCodexCredential(
  channelId: number
): Promise<CodexCredentialRefreshResponse> {
  const config: ExtendedApiConfig = { skipBusinessError: true }
  const res = await api.post(
    `/api/channel/${channelId}/codex/refresh`,
    {},
    config
  )
  return res.data
}

export async function getCodexUsage(
  channelId: number
): Promise<CodexUsageResponse> {
  const config: ExtendedApiConfig = {
    skipBusinessError: true,
    disableDuplicate: true,
  }
  const res = await api.get(`/api/channel/${channelId}/codex/usage`, config)
  return res.data
}

// ============================================================================
// Multi-Key Management
// ============================================================================

/**
 * Manage multi-key channel operations
 */
export async function manageMultiKeys(
  params: MultiKeyManageParams
): Promise<MultiKeyStatusResponse | { success: boolean; message?: string }> {
  const res = await api.post('/api/channel/multi_key/manage', params)
  return res.data
}

/**
 * Get key status for multi-key channel
 */
export async function getMultiKeyStatus(
  channelId: number,
  page = 1,
  pageSize = 50,
  status?: number
): Promise<MultiKeyStatusResponse> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'get_key_status',
    page,
    page_size: pageSize,
    status,
  }) as Promise<MultiKeyStatusResponse>
}

/**
 * Enable a specific key in multi-key channel
 */
export async function enableMultiKey(
  channelId: number,
  keyIndex: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'enable_key',
    key_index: keyIndex,
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Disable a specific key in multi-key channel
 */
export async function disableMultiKey(
  channelId: number,
  keyIndex: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'disable_key',
    key_index: keyIndex,
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Delete a specific key in multi-key channel
 */
export async function deleteMultiKey(
  channelId: number,
  keyIndex: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'delete_key',
    key_index: keyIndex,
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Enable all keys in multi-key channel
 */
export async function enableAllMultiKeys(
  channelId: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'enable_all_keys',
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Disable all keys in multi-key channel
 */
export async function disableAllMultiKeys(
  channelId: number
): Promise<{ success: boolean; message?: string }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'disable_all_keys',
  }) as Promise<{ success: boolean; message?: string }>
}

/**
 * Delete all disabled keys in multi-key channel
 */
export async function deleteDisabledMultiKeys(
  channelId: number
): Promise<{ success: boolean; message?: string; data?: number }> {
  return manageMultiKeys({
    channel_id: channelId,
    action: 'delete_disabled_keys',
  }) as Promise<{ success: boolean; message?: string; data?: number }>
}

// ============================================================================
// Tag Operations
// ============================================================================

/**
 * Enable all channels with a specific tag
 */
export async function enableTagChannels(
  tag: string
): Promise<{ success: boolean; message?: string }> {
  const res = await api.post('/api/channel/tag/enabled', { tag })
  return res.data
}

/**
 * Disable all channels with a specific tag
 */
export async function disableTagChannels(
  tag: string
): Promise<{ success: boolean; message?: string }> {
  const res = await api.post('/api/channel/tag/disabled', { tag })
  return res.data
}

/**
 * Edit all channels with a specific tag
 */
export async function editTagChannels(
  params: TagOperationParams
): Promise<{ success: boolean; message?: string }> {
  const res = await api.put('/api/channel/tag', params)
  return res.data
}

/**
 * Get models for a specific tag
 */
export async function getTagModels(
  tag: string
): Promise<{ success: boolean; message?: string; data?: string }> {
  const res = await api.get('/api/channel/tag/models', { params: { tag } })
  return res.data
}

// ============================================================================
// Utility Functions
// ============================================================================

/**
 * Fetch models from a custom endpoint (for testing before creating channel)
 */
export async function fetchModels(data: {
  base_url: string
  type: number
  key?: string
  channel_id?: number
  advanced_custom?: string
  header_override?: string
  proxy?: string
}): Promise<FetchModelsResponse> {
  const res = await api.post('/api/channel/fetch_models', data)
  return res.data
}

/**
 * Delete an Ollama model from a channel
 */
export async function deleteOllamaModel(params: {
  channel_id: number
  model_name: string
}): Promise<{ success: boolean; message?: string }> {
  const res = await api.delete('/api/channel/ollama/delete', { data: params })
  return res.data
}

/**
 * Test all enabled channels
 */
export async function testAllChannels(): Promise<{
  success: boolean
  message?: string
}> {
  const res = await api.get('/api/channel/test')
  return res.data
}

/**
 * Update balance for all enabled channels
 */
export async function updateAllChannelsBalance(): Promise<{
  success: boolean
  message?: string
}> {
  const res = await api.get('/api/channel/update_balance')
  return res.data
}

/**
 * Get all available models
 */
export async function getAllModels(): Promise<{
  success: boolean
  message?: string
  data?: Array<{ id: string; [key: string]: unknown }>
}> {
  const res = await api.get('/api/channel/models')
  return res.data
}

/**
 * Get all enabled models
 */
export async function getEnabledModels(): Promise<{
  success: boolean
  message?: string
  data?: string[]
}> {
  const res = await api.get('/api/channel/models_enabled')
  return res.data
}

// ============================================================================
// Ollama Utilities
// ============================================================================

/**
 * Check Ollama version for a given channel
 */
export async function getOllamaVersion(
  channelId: number
): Promise<{ success: boolean; message?: string; data?: { version: string } }> {
  const res = await api.get(`/api/channel/ollama/version/${channelId}`)
  return res.data
}

// ============================================================================
// Group Management
// ============================================================================

/**
 * Get all available groups (re-exported from users API for convenience)
 */
export const getGroups = getUserGroups

// ============================================================================
// Channel Health (passive auto-degrade)
// ============================================================================

export type ChannelHealthSnapshot = {
  degrade_level: number
  original_priority: number
  original_weight: number
  current_priority: number
  current_weight: number
  last_demote_at: number
  last_upgrade_at: number
  last_demote_reason: string
  last_disabled_at: number
  permanent_disabled: number
  rebounce_count: number
  status: number
}

export type ChannelHealthEvent = {
  id: number
  channel_id: number
  event_type: 'demote' | 'upgrade' | 'disable' | 'enable' | 'snapshot'
  from_level: number
  to_level: number
  reason: string
  operator: string
  created_at: number
}

export type GetChannelHealthResponse = {
  success: boolean
  message?: string
  data: {
    snapshot: ChannelHealthSnapshot
    events: ChannelHealthEvent[]
  }
}

export async function getChannelHealth(
  id: number,
  params?: { days?: number; limit?: number }
): Promise<GetChannelHealthResponse> {
  const res = await api.get(`/api/channel/${id}/health/events`, { params })
  return res.data
}

export async function recoverChannelHealth(id: number): Promise<{
  success: boolean
  message?: string
  data?: { id: number; degrade_level: number; priority: number; weight: number }
}> {
  const res = await api.post(`/api/channel/${id}/health/recover`)
  return res.data
}

// ============================================================================
// Prefill Groups (Model Groups)
// ============================================================================

/**
 * Get prefill groups for quick model selection
 */
export async function getPrefillGroups(
  type: 'model' | 'group' = 'model'
): Promise<{
  success: boolean
  message?: string
  data?: Array<{ id: number; name: string; items: string | string[] }>
}> {
  const res = await api.get('/api/prefill_group', { params: { type } })
  return res.data
}

// ============================================================================
// External Verify (测评网关 /api/verify/claude SSE)
// ============================================================================

/**
 * 触发对某渠道的外部测评。后端透传测评网关 SSE 流。
 * onEvent 每收到一个 SSE 事件就回调一次。
 * 返回 abort 函数：调用即取消请求（前端关闭 Modal 用）。
 */
export function verifyChannel(
  id: number,
  model: string,
  onEvent: (evt: VerifySSEEvent) => void,
  onError: (err: Error) => void,
  onDone: () => void
): () => void {
  const controller = new AbortController()
  ;(async () => {
    try {
      const res = await fetch(`/api/channel/${id}/verify`, {
        method: 'POST',
        headers: {
          ...(await getDashboardAuthHeaders()),
          Accept: 'text/event-stream',
        },
        credentials: 'include',
        signal: controller.signal,
        body: JSON.stringify({ model }),
      })
      if (!res.ok) {
        const text = await res.text().catch(() => '')
        throw new Error(`HTTP ${res.status}: ${text || res.statusText}`)
      }
      if (!res.body) throw new Error('no response body')
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop() ?? ''
        for (const line of lines) {
          const trimmed = line.trim()
          if (!trimmed.startsWith('data:')) continue
          const jsonStr = trimmed.slice(5).trim()
          if (!jsonStr || jsonStr[0] !== '{') continue
          try {
            const evt = JSON.parse(jsonStr) as VerifySSEEvent
            onEvent(evt)
          } catch {
            // ignore malformed event
          }
        }
      }
      onDone()
    } catch (err) {
      if ((err as Error).name === 'AbortError') {
        onDone()
        return
      }
      onError(err as Error)
    }
  })()
  return () => controller.abort()
}

export async function listVerifyReports(
  channelId: number,
  page = 1,
  pageSize = 20
): Promise<ListVerifyReportsResponse> {
  const res = await api.get(`/api/channel/${channelId}/verify/reports`, {
    params: { page, page_size: pageSize },
  })
  return res.data
}

export async function getVerifyReport(
  reportId: number
): Promise<GetVerifyReportResponse> {
  const res = await api.get(`/api/channel/verify/report/${reportId}`)
  return res.data
}

// ── 质量历史报表（列表质量分 hover"查看更多"弹窗）──

export interface QualityHistoryBucket {
  bucket_start: number
  success_cnt: number
  error_cnt: number
  avg_use_time: number // 秒，仅成功请求
}

export interface QualityErrorModelRow {
  model_name: string
  error_cnt: number
}

export interface QualityHistoryData {
  bucket_seconds: number
  buckets: QualityHistoryBucket[] | null
  error_codes: Record<string, number>
  error_codes_sampled: boolean
  error_models: QualityErrorModelRow[] | null
  totals: {
    success_cnt: number
    error_cnt: number
    avg_use_time_ms: number
    avg_frt_ms: number
    frt_samples: number
  }
}

export interface QualityHistoryResponse {
  success: boolean
  message?: string
  data?: QualityHistoryData
}

export async function getChannelQualityHistory(
  channelId: number,
  startTimestamp: number,
  endTimestamp: number
): Promise<QualityHistoryResponse> {
  const res = await api.get(`/api/channel/${channelId}/quality/history`, {
    params: {
      start_timestamp: startTimestamp,
      end_timestamp: endTimestamp,
      tz_offset_sec: -new Date().getTimezoneOffset() * 60,
    },
  })
  return res.data
}

// ── 可用性条形图（列表「可用性」列）──
// 一次拿到全部渠道近 N 小时的每小时成功/失败计数，后端进程内缓存 5 分钟。

export interface ChannelUptimePoint {
  ts: number // 桶起始 unix 秒
  success_count: number
  error_count: number
  success_rate: number // 0-100
}

export interface ChannelUptimeResponse {
  success: boolean
  message?: string
  data?: Record<string, ChannelUptimePoint[]>
}

export async function getChannelUptime(
  hours = 24
): Promise<ChannelUptimeResponse> {
  const res = await api.get('/api/channel/uptime', {
    params: {
      hours,
      tz_offset_sec: -new Date().getTimezoneOffset() * 60,
    },
  })
  return res.data
}

/**
 * 终止一个 running 的测评。后端会把 ctx 取消并把 DB 写成 cancelled。
 * 已经结束的 report（success/failed/cancelled）也会返回 success（幂等）。
 */
export async function cancelVerifyReport(reportId: number): Promise<{
  success: boolean
  message?: string
  data?: {
    cancelled_inflight?: boolean
    already_done?: boolean
    status?: string
  }
}> {
  const res = await api.post(`/api/channel/verify/report/${reportId}/cancel`)
  return res.data
}

// ── 上游分组倍率变更明细（列表倍率角标点击弹窗）──
// docs/2026-08-05-upstream-group-ratio-monitor.md

export interface ChannelRatioChange {
  group_name: string
  ratio_kind: string // group | resolved | effective | api_rate | peak
  old_value: number
  new_value: number
  direction: number // 1=涨 -1=降
  batch_at: number
}

/** 当前分组倍率基线（弹窗首要展示"此刻的倍率"） */
export interface ChannelRatioBaseline {
  group_name: string
  ratio_kind: string
  ratio: number
  updated_at: number
}

export interface ChannelRatioChangesResponse {
  success: boolean
  message?: string
  data?: ChannelRatioChange[] | null
  current?: ChannelRatioBaseline[] | null
}

export async function getChannelRatioChanges(
  channelId: number,
  days = 7
): Promise<ChannelRatioChangesResponse> {
  const res = await api.get(`/api/channel/${channelId}/ratio_changes`, {
    params: { days },
  })
  return res.data
}

// ============================================================================
// Sub-Site Sync proxies — 详见 docs/2026-05-27-sub-site-sync-plan.md
// 复用 system-settings 已落地的 endpoint，避免重复定义 fetch 逻辑。
// ============================================================================
export {
  createSubSiteChannels,
  getSubSiteGroups,
  listSubSites,
} from '@/features/system-settings/api'
export type {
  SubSite,
  SubSiteCreateChannelsRequest,
  SubSiteCreateChannelsResponse,
  SubSiteCreateResult,
  SubSiteGroup,
  SubSiteGroupsResponse,
} from '@/features/system-settings/types'

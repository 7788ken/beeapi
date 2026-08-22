import { api } from '@/lib/api'
import type {
  DeleteLogsResponse,
  FetchUpstreamRatiosRequest,
  SubSite,
  SubSiteCreateChannelsRequest,
  SubSiteCreateChannelsResponse,
  SubSiteGroupsResponse,
  SubSiteListResponse,
  SubSiteUpsertRequest,
  SubSiteUpsertResponse,
  SubSiteVerifyRequest,
  SubSiteVerifyResponse,
  SystemOptionsResponse,
  UpdateOptionRequest,
  UpdateOptionResponse,
  UpstreamChannelsResponse,
  UpstreamRatiosResponse,
} from './types'

export async function getSystemOptions() {
  const res = await api.get<SystemOptionsResponse>('/api/option/')
  return res.data
}

export async function updateSystemOption(request: UpdateOptionRequest) {
  const res = await api.put<UpdateOptionResponse>('/api/option/', request)
  return res.data
}

export async function deleteLogsBefore(targetTimestamp: number) {
  const res = await api.delete<DeleteLogsResponse>('/api/log/', {
    params: { target_timestamp: targetTimestamp },
  })
  return res.data
}

export async function resetModelRatios() {
  const res = await api.post<UpdateOptionResponse>(
    '/api/option/rest_model_ratio'
  )
  return res.data
}

export async function getUpstreamChannels() {
  const res = await api.get<UpstreamChannelsResponse>(
    '/api/ratio_sync/channels'
  )
  return res.data
}

export async function fetchUpstreamRatios(request: FetchUpstreamRatiosRequest) {
  const res = await api.post<UpstreamRatiosResponse>(
    '/api/ratio_sync/fetch',
    request
  )
  return res.data
}

// ============================================================================
// Sub-Site Sync (分站同步)
// docs/2026-05-27-sub-site-sync-plan.md
// ============================================================================

export async function listSubSites(): Promise<SubSite[]> {
  const res = await api.get<SubSiteListResponse>('/api/sub_site/list')
  return res.data.data ?? []
}

export async function upsertSubSite(payload: SubSiteUpsertRequest) {
  const res = await api.post<SubSiteUpsertResponse>(
    '/api/sub_site/upsert',
    payload
  )
  return res.data
}

export async function deleteSubSite(id: number) {
  const res = await api.delete<{ success: boolean; message?: string }>(
    `/api/sub_site/${id}`
  )
  return res.data
}

export async function verifySubSite(payload: SubSiteVerifyRequest) {
  const res = await api.post<SubSiteVerifyResponse>(
    '/api/sub_site/verify',
    payload
  )
  return res.data
}

export async function getSubSiteGroups(id: number, refresh = false) {
  const res = await api.get<SubSiteGroupsResponse>(`/api/sub_site/${id}/groups`, {
    params: refresh ? { refresh: 1 } : undefined,
  })
  return res.data
}

export async function createSubSiteChannels(
  id: number,
  payload: SubSiteCreateChannelsRequest
) {
  const res = await api.post<SubSiteCreateChannelsResponse>(
    `/api/sub_site/${id}/create_channels`,
    payload
  )
  return res.data
}

import { api } from '@/lib/api'
import type {
  ChannelStatsFilters,
  ChannelStatsResp,
  ChannelTopUsersResp,
  ChannelTrendResp,
  TopUsersSortBy,
} from './types'

interface ApiResp<T> {
  success: boolean
  message?: string
  data?: T
}

export async function fetchChannelStatistics(
  filters: ChannelStatsFilters
): Promise<ApiResp<ChannelStatsResp>> {
  const res = await api.get('/api/channel/statistics', {
    params: {
      range_seconds: filters.rangeSeconds,
      sort_by: filters.sortBy,
      top_n: filters.topN,
      model_type: filters.modelType || undefined,
    },
  })
  return res.data
}

export async function fetchChannelTopUsers(params: {
  channelId: number
  rangeSeconds: number
  sortBy: TopUsersSortBy
  limit?: number
}): Promise<ApiResp<ChannelTopUsersResp>> {
  const res = await api.get('/api/channel/statistics/top_users', {
    params: {
      channel_id: params.channelId,
      range_seconds: params.rangeSeconds,
      sort_by: params.sortBy,
      limit: params.limit,
    },
  })
  return res.data
}

export async function fetchChannelTrend(params: {
  channelIds: number[]
  rangeSeconds: number
  bucketSeconds?: number
  tzOffsetSec?: number
}): Promise<ApiResp<ChannelTrendResp>> {
  const res = await api.get('/api/channel/statistics/trend', {
    params: {
      channel_ids: params.channelIds.join(','),
      range_seconds: params.rangeSeconds,
      bucket_seconds: params.bucketSeconds,
      tz_offset_sec: params.tzOffsetSec,
    },
  })
  return res.data
}

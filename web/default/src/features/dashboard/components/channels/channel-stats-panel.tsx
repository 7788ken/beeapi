// 渠道统计主面板 — 顶部 FilterBar，下方 Cards 网格 + Trend 图。
// 卡片点击 = 展开/收起该渠道的 Top 用户面板（全局单开）；
// 趋势曲线选中（最多 8 条）降级为行尾图标按钮。

import { useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchChannelStatistics } from './api'
import { ChannelStatsCards } from './channel-stats-cards'
import { ChannelStatsTrend } from './channel-stats-trend'
import { FilterBar } from './filter-bar'
import {
  resolveRangeSeconds,
  type ChannelStatsFilters,
  type ChannelStatsItem,
  type ChannelStatsResp,
} from './types'

const QUERY_KEY = ['dashboard', 'channel-stats'] as const
const MAX_SELECTED = 8

function defaultFilters(): ChannelStatsFilters {
  return {
    rangeSeconds: 24 * 3600,
    sortBy: 'quota',
    topN: 10,
    modelType: '',
  }
}

export function ChannelStatsPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [filters, setFilters] = useState<ChannelStatsFilters>(defaultFilters)
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set())
  // 当前展开 Top 用户面板的渠道（单选，再点收起）。
  const [expandedChannelId, setExpandedChannelId] = useState<number | null>(null)

  // queryKey 用 filters 原值（含"今天"=-1 哨兵），避免 resolveRangeSeconds 每秒 +1
  // 导致 queryKey 漂移 → 无限 refetch。effective 秒数延后到 queryFn 真正发请求时算。
  const query = useQuery<ChannelStatsResp | undefined>({
    queryKey: [...QUERY_KEY, filters],
    queryFn: () =>
      fetchChannelStatistics({
        ...filters,
        rangeSeconds: resolveRangeSeconds(filters.rangeSeconds),
      }).then((r) => r.data),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  })

  const groups = useMemo(() => query.data?.groups ?? [], [query.data?.groups])
  const availableTypes = useMemo(() => groups.map((g) => g.model_type), [groups])

  // channel_id -> channel_name 缓存，趋势图用
  const nameMap = useMemo(() => {
    const out: Record<number, string> = {}
    for (const g of groups) {
      for (const c of g.channels) {
        out[c.channel_id] = c.channel_name
      }
    }
    return out
  }, [groups])

  const handleToggleTrend = (item: ChannelStatsItem) => {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      if (next.has(item.channel_id)) {
        next.delete(item.channel_id)
      } else {
        if (next.size >= MAX_SELECTED) return prev
        next.add(item.channel_id)
      }
      return next
    })
  }

  const handleToggleExpand = (item: ChannelStatsItem) => {
    setExpandedChannelId((prev) =>
      prev === item.channel_id ? null : item.channel_id
    )
  }

  const handleRefresh = () => {
    void queryClient.invalidateQueries({ queryKey: QUERY_KEY })
    void queryClient.invalidateQueries({ queryKey: ['dashboard', 'channel-trend'] })
  }

  // 默认选中每个 type 的 #1 渠道，保证趋势图首屏有内容
  const effectiveIds = useMemo(() => {
    if (selectedIds.size > 0) return Array.from(selectedIds)
    const auto: number[] = []
    for (const g of groups) {
      const top = g.channels[0]
      if (top) auto.push(top.channel_id)
      if (auto.length >= MAX_SELECTED) break
    }
    return auto
  }, [selectedIds, groups])

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <FilterBar
          filters={filters}
          onChange={setFilters}
          onRefresh={handleRefresh}
          refreshing={query.isFetching}
          availableTypes={availableTypes}
        />
        {selectedIds.size > 0 && (
          <button
            type='button'
            className='text-muted-foreground hover:text-foreground text-xs underline'
            onClick={() => setSelectedIds(new Set())}
          >
            {t('Clear selection')}
          </button>
        )}
      </div>

      <ChannelStatsCards
        groups={groups}
        sortBy={filters.sortBy}
        loading={query.isPending}
        onToggleTrend={handleToggleTrend}
        selectedIds={selectedIds}
        expandedId={expandedChannelId}
        onToggleExpand={handleToggleExpand}
      />

      <ChannelStatsTrend
        channelIds={effectiveIds}
        channelNames={nameMap}
        rangeSeconds={filters.rangeSeconds}
      />
    </div>
  )
}

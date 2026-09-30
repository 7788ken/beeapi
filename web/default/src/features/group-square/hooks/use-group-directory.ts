import { useMemo } from 'react'
import type { UptimeDayPoint } from '@/components/uptime-sparkline'
import { usePriceChanges } from '@/features/price-changes/hooks'
import { OTHER_PLATFORM, PLATFORMS, classifyGroup } from '../lib/classify'
import {
  buildGroupStates,
  countAvailable,
  latestChangeAt,
  type GroupState,
} from '../lib/directory'
import { toUptimeSeries, useGroupUptimeQuery } from './use-group-uptime'
import { useSelfGroupsQuery } from './use-self-groups'

export interface GroupEntry extends GroupState {
  platform: string
  platformRank: number
  series: UptimeDayPoint[]
}

const PLATFORM_ORDER = [...PLATFORMS.map((p) => p.key), OTHER_PLATFORM.key]
const ROUTING_PLATFORM = 'auto'

/**
 * 可用分组页：在 buildGroupStates 之上补分类与迷你图数据。
 * 时间基准取数据的拉取时刻（可用判定、迷你图补槽、变动窗口三处一致），渲染期不读系统时钟。
 */
export function useGroupDirectory() {
  const groupsQuery = useSelfGroupsQuery()
  const uptimeQuery = useGroupUptimeQuery()
  const { data: priceChanges } = usePriceChanges()

  const groups = groupsQuery.data
  const uptime = uptimeQuery.data
  const nowMs = uptimeQuery.dataUpdatedAt || groupsQuery.dataUpdatedAt

  const entries = useMemo<GroupEntry[]>(() => {
    if (!groups) return []
    const series = toUptimeSeries(uptime, Math.floor(nowMs / 1000))
    return buildGroupStates(groups, uptime, priceChanges, nowMs).map((g) => {
      const platform = classifyGroup(g.name, g.desc, ROUTING_PLATFORM)
      return {
        ...g,
        platform,
        platformRank: PLATFORM_ORDER.indexOf(platform),
        series: series[g.name] ?? [],
      }
    })
  }, [groups, uptime, priceChanges, nowMs])

  return {
    entries,
    isLoading: groupsQuery.isLoading,
    isError: groupsQuery.isError,
    /** 可用率数据已到手：此前「只看可用」无从判断，开关置灰、不显示可用数 */
    availabilityReady: uptime !== undefined,
    availabilityPending: uptimeQuery.isPending,
    availableCount: uptime ? countAvailable(entries) : undefined,
    latestChangeAt: latestChangeAt(entries),
  }
}

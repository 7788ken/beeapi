import { useMemo } from 'react'
import { useNotificationStore } from '@/stores/notification-store'
import { usePriceChanges } from '@/features/price-changes/hooks'
import {
  buildGroupStates,
  countAvailable,
  latestChangeAt,
} from '../lib/directory'
import { useGroupUptimeQuery } from './use-group-uptime'
import { useSelfGroupsQuery } from './use-self-groups'

/**
 * 侧栏「可用分组」入口：可用数量 + 是否有还没看过的分组变动（进过一次页面即算看过）。
 * 刻意不经过 useGroupDirectory：那边要分类（引 @lobehub/icons 品牌图标）和迷你图数据，
 * 侧栏常驻在每个控制台页面，只拿两个数，不把图标库带进常驻包。
 */
export function useGroupNavState() {
  const groupsQuery = useSelfGroupsQuery()
  const uptimeQuery = useGroupUptimeQuery()
  const { data: priceChanges } = usePriceChanges()
  const seenAt = useNotificationStore((s) => s.groupChangesSeenAt)

  const groups = groupsQuery.data
  const uptime = uptimeQuery.data
  const nowMs = uptimeQuery.dataUpdatedAt || groupsQuery.dataUpdatedAt

  const states = useMemo(
    () => (groups ? buildGroupStates(groups, uptime, priceChanges, nowMs) : []),
    [groups, uptime, priceChanges, nowMs]
  )

  return {
    availableCount: uptime ? countAvailable(states) : undefined,
    hasUnseenChanges: latestChangeAt(states) > seenAt,
  }
}

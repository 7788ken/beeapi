import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { UptimeDayPoint } from '@/components/uptime-sparkline'
import { fillHourlyUptimeSlots, formatHourBucketLabel } from '@/lib/uptime-slots'
import { getGroupUptime } from '../api'

export const GROUP_UPTIME_HOURS = 24

/**
 * 分组可用率 mini 图数据源（GET /api/perf-metrics/groups），分组广场与密钥分组选择器共用。
 *
 * 返回 Record<分组, 24 个小时槽位>；后端只返回有日志的小时，这里补齐成连续 24 槽、
 * 缺失槽灰显。补槽偏移固定传 0：该接口匿名可访问，后端按 UTC 整点对齐，传浏览器偏移会全部错位。
 *
 * 口径与渠道页「可用性」列不同：真实流量按发生时刻的归属计入（含现已禁用渠道的历史），
 * 启用态测活只计入当前启用的分组，禁用期探活不计入（详见后端 service/group_uptime.go）；
 * 分组一个启用渠道都没有时，后端直接返回整窗口 0% 的实心序列（红），
 * 与「有渠道但没人用」的灰色空槽区分开。
 */
export function useGroupUptimeSeries(enabled = true) {
  const { data } = useQuery({
    queryKey: ['perf-metrics', 'groups', GROUP_UPTIME_HOURS] as const,
    queryFn: async () => {
      const res = await getGroupUptime(GROUP_UPTIME_HOURS)
      return res?.data ?? {}
    },
    staleTime: 60_000,
    enabled,
  })

  return useMemo<Record<string, UptimeDayPoint[]>>(() => {
    if (!data) return {}
    const out: Record<string, UptimeDayPoint[]> = {}
    for (const [group, buckets] of Object.entries(data)) {
      const byTs = new Map<number, UptimeDayPoint>()
      for (const b of buckets) {
        byTs.set(b.ts, {
          date: formatHourBucketLabel(b.ts),
          uptime_pct: b.success_rate,
          incidents: b.success_rate < 100 ? 1 : 0,
          outage_minutes: 0,
        })
      }
      out[group] = fillHourlyUptimeSlots(byTs, GROUP_UPTIME_HOURS, 0)
    }
    return out
  }, [data])
}

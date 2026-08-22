import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { UptimeDayPoint } from '@/components/uptime-sparkline'
import {
  browserTzOffsetSec,
  fillHourlyUptimeSlots,
  formatHourBucketLabel,
} from '@/lib/uptime-slots'
import { getChannelUptime } from '../api'
import { channelsQueryKeys } from '../lib'

const UPTIME_STALE_MS = 5 * 60 * 1000 // 与后端进程内缓存 TTL 对齐

/**
 * 渠道列表「可用性」列的数据源：整表拉一次 /api/channel/uptime，按 channel id 分发到行。
 * 返回 Map<channelId, UptimeDayPoint[]>，每个渠道都补齐成 hours 个小时槽位，无日志的小时灰显；
 * 窗口内完全没有日志的渠道不会出现在 Map 里（行内显示空态）。
 */
export function useChannelUptime(hours = 24) {
  const { data } = useQuery({
    queryKey: [...channelsQueryKeys.all, 'uptime', hours],
    queryFn: () => getChannelUptime(hours),
    staleTime: UPTIME_STALE_MS,
    refetchOnWindowFocus: false,
  })

  return useMemo(() => {
    const map = new Map<number, UptimeDayPoint[]>()
    const raw = data?.data
    if (!raw) return map
    // getChannelUptime 按浏览器偏移请求，槽位必须用同一偏移对齐。
    const tzOffsetSec = browserTzOffsetSec()
    for (const [channelId, points] of Object.entries(raw)) {
      const byTs = new Map<number, UptimeDayPoint>()
      for (const p of points) {
        byTs.set(p.ts, {
          date: formatHourBucketLabel(p.ts),
          uptime_pct: p.success_rate,
          incidents: p.error_count,
          outage_minutes: 0,
        })
      }
      map.set(
        Number(channelId),
        fillHourlyUptimeSlots(byTs, hours, tzOffsetSec)
      )
    }
    return map
  }, [data, hours])
}

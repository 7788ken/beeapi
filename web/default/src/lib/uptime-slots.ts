import type { UptimeDayPoint } from '@/components/uptime-sparkline'

const HOUR_SEC = 3600

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

/** 桶起始时间（unix 秒）→ 本地可读标签 "MM-DD HH:00"。后端已按本地时区偏移对齐整点。 */
export function formatHourBucketLabel(ts: number): string {
  const d = new Date(ts * 1000)
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:00`
}

/** 浏览器相对 UTC 的偏移秒数，与请求里发给后端的 tz_offset_sec 同口径。 */
export function browserTzOffsetSec(): number {
  return -new Date().getTimezoneOffset() * 60
}

/**
 * 把后端返回的稀疏小时桶补齐成连续 hours 个槽位，缺失的槽位标记 no_data 灰显。
 *
 * 后端只返回有日志的小时，稀疏序列会让「近 3 小时才有量」和「24 小时满量」画出同样长度的图，
 * 也无法区分「这一小时没请求」和「这一小时全挂」。
 *
 * tzOffsetSec 必须与该接口实际使用的对齐口径一致，否则槽位落不到后端桶上、整条曲线全灰：
 * 渠道可用性传浏览器偏移，分组广场（匿名可访问、后端固定按 UTC 对齐）传 0。
 */
export function fillHourlyUptimeSlots(
  byTs: Map<number, UptimeDayPoint>,
  hours: number,
  tzOffsetSec: number
): UptimeDayPoint[] {
  const nowSec = Math.floor(Date.now() / 1000)
  const shifted = nowSec + tzOffsetSec
  const endTs = shifted - (((shifted % HOUR_SEC) + HOUR_SEC) % HOUR_SEC) - tzOffsetSec

  const slots: UptimeDayPoint[] = []
  for (let i = hours - 1; i >= 0; i--) {
    const ts = endTs - i * HOUR_SEC
    const hit = byTs.get(ts)
    slots.push(
      hit ?? {
        date: formatHourBucketLabel(ts),
        uptime_pct: 0,
        incidents: 0,
        outage_minutes: 0,
        no_data: true,
      }
    )
  }
  return slots
}

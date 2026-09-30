// channels 子面板共享的小格式化件 — channel-stats-cards 与 channel-top-users 共用，
// runtime-panel 也复用这里的 RankBadge，避免就地复制漂移
// （runtime-panel 的 fmtRpm 仍是本地副本，不在此收敛范围）。

import { cn } from '@/lib/utils'
import { dashRankTone } from '../overview/dash-emphasis'

export function fmtRpm(rpm: number): string {
  if (!rpm || rpm < 0) return '0'
  if (rpm >= 100) return rpm.toFixed(0)
  if (rpm >= 10) return rpm.toFixed(1)
  return rpm.toFixed(2)
}

export function fmtCount(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return String(n)
}

export function RankBadge({ rank }: { rank: number }) {
  return (
    <span
      className={cn(
        'inline-flex h-5 w-5 shrink-0 items-center justify-center rounded text-[10px] font-semibold tabular-nums',
        dashRankTone(rank)
      )}
    >
      {rank}
    </span>
  )
}

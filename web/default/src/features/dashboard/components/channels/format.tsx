// channels 子面板共享的小格式化件 — channel-stats-cards 与 channel-top-users 共用，
// 避免就地复制漂移（runtime-panel 仍有历史副本，不在此收敛范围）。

import { cn } from '@/lib/utils'

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
  const color =
    rank === 1
      ? 'bg-amber-500/15 text-amber-700 dark:text-amber-400'
      : rank === 2
        ? 'bg-slate-400/15 text-slate-700 dark:text-slate-300'
        : rank === 3
          ? 'bg-orange-500/15 text-orange-700 dark:text-orange-400'
          : 'bg-muted text-muted-foreground'
  return (
    <span
      className={cn(
        'inline-flex h-5 w-5 shrink-0 items-center justify-center rounded text-[10px] font-semibold tabular-nums',
        color
      )}
    >
      {rank}
    </span>
  )
}

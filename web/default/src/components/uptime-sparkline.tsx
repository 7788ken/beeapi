import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

// ---------------------------------------------------------------------------
// Uptime sparkline (shared)
// ---------------------------------------------------------------------------
//
// Compact availability visualisation: a row of small coloured bars where:
//   - Bar colour reflects per-bucket uptime (green / amber / red)
//   - Bar height reflects severity (the worse the bucket, the shorter the bar)
//   - Hovering a bar reveals the exact bucket label and uptime
//   - The trailing number is the LATEST bucket (last hour), not the window
//     average — it answers "is it usable right now", which is what a user
//     picking a group actually needs. No sampling in that bucket => "—".
//
// Used by model details (per-group performance), group square cards and the
// admin channel table.

export type UptimeDayPoint = {
  date: string
  uptime_pct: number
  incidents: number
  outage_minutes: number
  // 该槽位没有任何采样：柱子灰显；若它是最新槽位，尾部数字显示 emptyLabel。
  // 用于区分「这一小时没有请求」和「这一小时全失败」。
  no_data?: boolean
}

type SparklineSize = 'sm' | 'md'

type UptimeSparklineProps = {
  series: UptimeDayPoint[]
  size?: SparklineSize
  /** 展示尾部数字（最新一个桶的可用率）。 */
  showLatest?: boolean
  emptyLabel?: string
  className?: string
}

// 可用率分档：>=70 绿、>=50 橙、其余红。柱子与尾部数字共用同一套阈值，
// 避免出现「柱子全红、数字标绿」的割裂观感。
export function colourFor(uptime: number): string {
  if (uptime >= 70) return 'bg-emerald-500'
  if (uptime >= 50) return 'bg-amber-500'
  return 'bg-rose-500'
}

export function heightFor(uptime: number): string {
  if (uptime >= 70) return 'h-full'
  if (uptime >= 50) return 'h-[72%]'
  return 'h-[40%]'
}

export function overallTextColour(pct: number): string {
  if (pct >= 70) return 'text-emerald-600 dark:text-emerald-400'
  if (pct >= 50) return 'text-amber-600 dark:text-amber-400'
  return 'text-rose-600 dark:text-rose-400'
}

export function UptimeSparkline(props: UptimeSparklineProps) {
  const { t } = useTranslation()
  const size = props.size ?? 'md'
  const showLatest = props.showLatest ?? true
  const noDataLabel = props.emptyLabel ?? '—'

  if (props.series.length === 0) {
    return (
      <span className={cn('text-muted-foreground text-xs', props.className)}>
        {noDataLabel}
      </span>
    )
  }

  // 尾部数字只看最新一个桶（近一小时），不做全窗口平均：平均会让一个早已恢复的
  // 分组长时间背着旧故障的分数，也会把刚挂掉的分组稀释成看起来还好。
  // 该桶无采样（no_data）时显示 emptyLabel，不用旧数据冒充当前状态。
  const latest = props.series[props.series.length - 1]
  const latestPct = latest.no_data ? null : latest.uptime_pct
  const latestHint = `${latest.date} · ${t('Last 1 hour')}`

  const containerHeight = size === 'sm' ? 'h-3.5' : 'h-5'
  const barWidth = size === 'sm' ? 'w-[3px]' : 'w-1'
  const gap = size === 'sm' ? 'gap-px' : 'gap-[2px]'

  return (
    <div className={cn('flex items-center gap-2', props.className)}>
      <div
        className={cn('flex items-end', containerHeight, gap)}
        role='img'
        aria-label={`uptime ${latestPct === null ? noDataLabel : `${latestPct.toFixed(2)}%`}`}
      >
        {props.series.map((day) => (
          <Tooltip key={day.date}>
            <TooltipTrigger asChild>
              <div
                className={cn(
                  'rounded-[1px] transition-opacity hover:opacity-80',
                  barWidth,
                  containerHeight,
                  'flex items-end'
                )}
              >
                <div
                  className={cn(
                    'w-full rounded-[1px]',
                    day.no_data
                      ? 'bg-muted-foreground/25 h-[18%]'
                      : cn(colourFor(day.uptime_pct), heightFor(day.uptime_pct))
                  )}
                  aria-hidden
                />
              </div>
            </TooltipTrigger>
            <TooltipContent side='top' className='font-mono text-xs'>
              <div className='font-medium'>{day.date}</div>
              {day.no_data ? (
                <div className='text-muted-foreground'>{noDataLabel}</div>
              ) : (
                <>
                  <div>{day.uptime_pct.toFixed(2)}%</div>
                  {day.outage_minutes > 0 && (
                    <div className='text-muted-foreground'>
                      {day.outage_minutes} min outage
                    </div>
                  )}
                </>
              )}
            </TooltipContent>
          </Tooltip>
        ))}
      </div>
      {showLatest &&
        (latestPct === null ? (
          <span
            className='text-muted-foreground font-mono text-sm tabular-nums'
            title={latestHint}
          >
            {noDataLabel}
          </span>
        ) : (
          <span
            className={cn(
              'font-mono text-sm font-semibold tabular-nums',
              overallTextColour(latestPct)
            )}
            title={latestHint}
          >
            {latestPct.toFixed(1)}%
          </span>
        ))}
    </div>
  )
}

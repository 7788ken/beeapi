import { useMemo } from 'react'
import { Activity, AlertCircle, CheckCircle2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import {
  UptimeSparkline,
  type UptimeDayPoint,
} from '@/components/uptime-sparkline'
import { formatUptimePct } from '@/features/performance-metrics/lib/format'
import { aggregateUptime } from '../lib/mock-stats'

// The sparkline itself now lives in `@/components/uptime-sparkline` so the
// group square and the admin channel table can reuse it. Re-exported here to
// keep existing pricing imports working.
export { UptimeSparkline }

// ---------------------------------------------------------------------------
// Uptime status row — sparkline + summary text + status icon
// ---------------------------------------------------------------------------

export function UptimeStatusRow(props: {
  series: UptimeDayPoint[]
  className?: string
}) {
  const { t } = useTranslation()
  const summary = useMemo(() => aggregateUptime(props.series), [props.series])
  const status = useMemo(() => {
    if (summary.uptime_pct >= 99.9) return 'operational'
    if (summary.uptime_pct >= 99.0) return 'minor'
    if (summary.uptime_pct >= 95.0) return 'degraded'
    return 'major'
  }, [summary.uptime_pct])

  const StatusIcon =
    status === 'operational'
      ? CheckCircle2
      : status === 'minor'
        ? Activity
        : AlertCircle

  const statusColour =
    status === 'operational'
      ? 'text-emerald-600 dark:text-emerald-400'
      : status === 'minor'
        ? 'text-emerald-600 dark:text-emerald-400'
        : status === 'degraded'
          ? 'text-amber-600 dark:text-amber-400'
          : 'text-rose-600 dark:text-rose-400'

  const statusLabel =
    status === 'operational'
      ? t('All systems operational')
      : status === 'minor'
        ? t('Minor blips in the last 30 days')
        : status === 'degraded'
          ? t('Degraded performance recently')
          : t('Significant outages detected')

  return (
    <div
      className={cn(
        'border-border/60 bg-muted/30 flex flex-wrap items-center gap-3 rounded-lg border px-3 py-2 sm:gap-4 sm:px-4',
        props.className
      )}
    >
      <div className='flex items-center gap-2'>
        <StatusIcon className={cn('size-4 shrink-0', statusColour)} />
        <span className='text-sm font-medium'>{t('Last 30 days uptime')}</span>
      </div>

      <UptimeSparkline series={props.series} className='ml-auto' />

      <div className='flex items-center gap-3 text-xs'>
        <span className={cn('font-medium', statusColour)}>{statusLabel}</span>
        {summary.incidents > 0 && (
          <span className='text-muted-foreground'>
            {summary.incidents}{' '}
            {summary.incidents === 1 ? t('incident') : t('incidents')}
          </span>
        )}
        {summary.outage_minutes > 0 && (
          <span className='text-muted-foreground'>
            {summary.outage_minutes} {t('min downtime')}
          </span>
        )}
        <span className='text-muted-foreground hidden sm:inline'>
          {formatUptimePct(summary.uptime_pct)} {t('overall')}
        </span>
      </div>
    </div>
  )
}

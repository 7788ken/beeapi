import { useMemo } from 'react'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { formatCompactNumber, formatNumber, formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'
import {
  formatLatency,
  formatThroughput,
  formatUptimePct,
} from '@/features/performance-metrics/lib/format'
import { dashSecondaryBtn } from './dash-style'
import { useMonthUsage } from './month-usage-chart'
import { usePerformanceSummary } from './use-performance-summary'

const SEGMENTS = 10

/** 指标卡内边距，比大面板（p-6 md:p-8）小一档，保持层级 */
const CARD_PAD = 'p-5 md:p-6'

export function SummaryCards(props: { showPerformance: boolean }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const performance = usePerformanceSummary(props.showPerformance)
  const { points, todayQuota, usageQuery } = useMonthUsage()

  const summary = useMemo(() => {
    const remainQuota = Number(user?.quota ?? 0)
    const usedQuota = Number(user?.used_quota ?? 0)
    const requestCount = Number(user?.request_count ?? 0)
    const monthQuota = points.reduce((sum, point) => sum + point.quota, 0)
    const monthTokens = points.reduce((sum, point) => sum + point.tokens, 0)
    const monthRequests = points.reduce((sum, point) => sum + point.count, 0)
    const today = points[points.length - 1]
    return {
      remainQuota,
      remainDisplay: formatQuota(remainQuota),
      usedDisplay: formatQuota(usedQuota),
      requestCountDisplay: formatNumber(requestCount),
      monthCostDisplay: formatQuota(monthQuota),
      monthTokensDisplay: formatCompactNumber(monthTokens),
      monthRequestsDisplay: formatNumber(monthRequests),
      todayTokensDisplay: formatCompactNumber(today?.tokens ?? 0),
      known: user != null,
      costs: points.map((point) => point.cost),
      counts: points.map((point) => point.count),
      tokens: points.map((point) => point.tokens),
    }
  }, [user, points])

  const levelLabel =
    performance.level === 'normal'
      ? t('Operating normally')
      : performance.level === 'attention'
        ? t('Needs attention')
        : performance.level === 'failing'
          ? t('Calls are failing')
          : t('No sample')

  return (
    <div className='grid grid-cols-1 gap-6 md:grid-cols-2 xl:grid-cols-4'>
      <BalanceCard
        remainQuota={summary.remainQuota}
        remainDisplay={summary.remainDisplay}
        todayQuota={todayQuota}
        todayDisplay={formatQuota(todayQuota)}
        known={summary.known}
        loading={!summary.known || usageQuery.isLoading}
      />
      <StatCard
        label={t("This month's cost")}
        value={usageQuery.isLoading ? '—' : summary.monthCostDisplay}
        sparkline={summary.costs}
        stroke='var(--primary)'
        footerLabel={t('Cumulative')}
        footerValue={summary.usedDisplay}
      />
      <StatCard
        label={t("This month's requests")}
        value={usageQuery.isLoading ? '—' : summary.monthRequestsDisplay}
        sparkline={summary.counts}
        stroke='var(--foreground)'
        footerLabel={t('Cumulative')}
        footerValue={summary.requestCountDisplay}
      />
      {props.showPerformance ? (
        <StatCard
          label={t('Success rate')}
          value={
            performance.loading
              ? '—'
              : performance.hasData
                ? formatUptimePct(performance.summary.successRate)
                : '—'
          }
          sparkline={[]}
          stroke='#7a73ff'
          footerLabel={performance.hasData ? levelLabel : t('No sample')}
          footerValue={
            performance.hasData
              ? `${formatLatency(performance.summary.avgLatencyMs)} · ${formatThroughput(performance.summary.avgTps)}`
              : ''
          }
        />
      ) : (
        <StatCard
          label={t("This month's tokens")}
          value={usageQuery.isLoading ? '—' : summary.monthTokensDisplay}
          sparkline={summary.tokens}
          stroke='#7a73ff'
          footerLabel={t("Today's tokens")}
          footerValue={summary.todayTokensDisplay}
        />
      )}
    </div>
  )
}

function BalanceCard(props: {
  remainQuota: number
  remainDisplay: string
  todayQuota: number
  todayDisplay: string
  known: boolean
  loading: boolean
}) {
  const { t } = useTranslation()
  const remain = Math.max(props.remainQuota, 0)
  const today = Math.max(props.todayQuota, 0)
  const total = remain + today
  const ratio = total > 0 ? remain / total : remain > 0 ? 1 : 0
  const percent = Math.round(ratio * 100)
  const usedUp = props.known && remain <= 0
  const low = !usedUp && percent < 25
  const filled = usedUp ? 0 : Math.max(1, Math.round(ratio * SEGMENTS))
  const status = usedUp
    ? t('Balance is used up. New requests will fail until you top up.')
    : low
      ? t('HP is running low')
      : t('Operating normally')

  return (
    <article
      className={cn(
        'bg-primary relative flex min-w-0 flex-col justify-between gap-4 overflow-hidden rounded-xl text-white shadow-[0_2px_5px_rgba(99,91,255,0.4),inset_0_1px_0_rgba(255,255,255,0.2)]',
        CARD_PAD
      )}
    >
      <svg
        className='pointer-events-none absolute -end-8 -top-10 h-40 w-40'
        viewBox='0 0 160 160'
        aria-hidden='true'
      >
        <circle cx='108' cy='52' r='58' fill='white' fillOpacity='0.08' />
        <circle cx='124' cy='36' r='32' fill='white' fillOpacity='0.12' />
        <circle cx='78' cy='18' r='16' fill='white' fillOpacity='0.1' />
      </svg>
      <svg
        className='pointer-events-none absolute -start-6 -bottom-8 h-24 w-24'
        viewBox='0 0 96 96'
        aria-hidden='true'
      >
        <circle cx='28' cy='70' r='36' fill='white' fillOpacity='0.08' />
      </svg>

      <div className='relative min-w-0'>
        <div className='flex items-start justify-between gap-3'>
          <p className='truncate text-sm text-white'>{t('Current Balance')}</p>
          <span className='shrink-0 rounded-lg bg-white/20 px-2 py-0.5 text-xs text-white'>
            {status}
          </span>
        </div>
        <p className='mt-1 truncate text-2xl font-semibold text-white tabular-nums'>
          {props.remainDisplay}
        </p>
        <div
          className='mt-3 flex gap-1'
          role='progressbar'
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={props.loading ? undefined : percent}
          aria-label={status}
        >
          {Array.from({ length: SEGMENTS }, (_, index) => (
            <span
              key={index}
              className={cn(
                'h-2 flex-1 rounded-sm',
                index < filled ? 'bg-white' : 'bg-white/25'
              )}
            />
          ))}
        </div>
      </div>

      <div className='relative flex items-center justify-between gap-3 border-t border-white/20 pt-3 text-sm'>
        <span className='min-w-0 truncate text-white'>
          {t('Used today')} {props.loading ? '—' : props.todayDisplay}
        </span>
        <Link
          to='/wallet'
          className={cn(
            dashSecondaryBtn,
            'h-8 shrink-0 bg-white px-3 text-[#0a2540] dark:bg-white dark:text-[#0a2540]'
          )}
        >
          {t('Recharge')}
        </Link>
      </div>
    </article>
  )
}

function StatCard(props: {
  label: string
  value: string
  sparkline: number[]
  stroke: string
  footerLabel: string
  footerValue: string
}) {
  // 没有任何非零样本时不画迷你图，避免出现一条毫无信息量的水平线
  const hasTrend =
    props.sparkline.length > 1 && props.sparkline.some((value) => value > 0)

  return (
    <article
      className={cn(
        'border-border bg-card text-card-foreground flex min-w-0 flex-col justify-between gap-4 rounded-xl border shadow-[0_2px_4px_rgba(0,0,0,0.04),0_8px_16px_rgba(0,0,0,0.08)] dark:shadow-[0_2px_4px_rgba(0,0,0,0.32),0_8px_16px_rgba(0,0,0,0.38)]',
        CARD_PAD
      )}
    >
      <div className='flex min-w-0 items-start justify-between gap-3'>
        <div className='min-w-0'>
          <p className='text-muted-foreground flex items-center gap-1.5 text-sm'>
            <span
              className='inline-block size-1.5 shrink-0 rounded-sm'
              style={{ backgroundColor: props.stroke }}
              aria-hidden='true'
            />
            <span className='truncate'>{props.label}</span>
          </p>
          <p className='mt-1 truncate text-2xl font-semibold tabular-nums'>
            {props.value}
          </p>
        </div>
        {hasTrend ? (
          <Sparkline values={props.sparkline} stroke={props.stroke} />
        ) : null}
      </div>
      <div className='border-border/70 flex items-center justify-between gap-3 border-t pt-3 text-sm'>
        <span className='text-muted-foreground min-w-0 truncate'>
          {props.footerLabel}
        </span>
        {props.footerValue ? (
          <span className='text-foreground shrink-0 font-medium tabular-nums'>
            {props.footerValue}
          </span>
        ) : null}
      </div>
    </article>
  )
}

function Sparkline(props: { values: number[]; stroke: string }) {
  const width = 120
  const height = 48
  const padX = 2
  const series = props.values.length > 1 ? props.values : [0, 0]
  const max = Math.max(...series, 0)
  const span = max || 1
  const innerW = width - padX * 2
  const step = innerW / (series.length - 1)
  const mid = height / 2
  const amplitude = height * 0.38
  const coords = series.map((value, index) => ({
    x: padX + index * step,
    y: mid - (Math.max(value, 0) / span) * amplitude,
  }))
  const line = smoothTrend(coords)
  const area = `${line} L ${coords[coords.length - 1].x} ${mid} L ${coords[0].x} ${mid} Z`

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      className='h-10 w-20 shrink-0 self-center sm:h-12 sm:w-28'
      aria-hidden='true'
    >
      <path d={area} fill={props.stroke} fillOpacity='0.16' stroke='none' />
      <path
        d={line}
        fill='none'
        stroke={props.stroke}
        strokeWidth='1.75'
        strokeLinejoin='round'
        strokeLinecap='round'
      />
    </svg>
  )
}

function smoothTrend(points: Array<{ x: number; y: number }>) {
  const [first] = points
  let path = `M ${first.x.toFixed(1)} ${first.y.toFixed(1)}`
  for (let index = 0; index < points.length - 1; index += 1) {
    const previous = points[index === 0 ? index : index - 1]
    const current = points[index]
    const next = points[index + 1]
    const after = points[index + 2] ?? next
    const controlStartX = current.x + (next.x - previous.x) / 6
    const controlStartY = current.y + (next.y - previous.y) / 6
    const controlEndX = next.x - (after.x - current.x) / 6
    const controlEndY = next.y - (after.y - current.y) / 6
    path += ` C ${controlStartX.toFixed(1)} ${controlStartY.toFixed(1)}, ${controlEndX.toFixed(1)} ${controlEndY.toFixed(1)}, ${next.x.toFixed(1)} ${next.y.toFixed(1)}`
  }
  return path
}

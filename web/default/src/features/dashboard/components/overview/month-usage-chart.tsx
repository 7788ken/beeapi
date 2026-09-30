import { useMemo } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { VChart } from '@visactor/react-vchart'
import { useTranslation } from 'react-i18next'
import {
  formatCompactNumber,
  formatNumber,
  formatQuota,
  quotaUnitsToDollars,
} from '@/lib/format'
import { VCHART_OPTION } from '@/lib/vchart'
import { getUserQuotaDates } from '@/features/dashboard/api'
import type { QuotaDataItem } from '@/features/dashboard/types'
import { dashSecondaryBtn, DashSkeleton } from './dash-style'

type DayPoint = {
  date: string
  tokens: number
  cost: number
  quota: number
  count: number
}

function monthBounds(now = new Date()) {
  const start = new Date(now.getFullYear(), now.getMonth(), 1)
  const end = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate(),
    23,
    59,
    59
  )
  return {
    startSec: Math.floor(start.getTime() / 1000),
    endSec: Math.floor(end.getTime() / 1000),
    start,
    end,
  }
}

function dayKey(date: Date) {
  return (
    date.getFullYear() * 10000 + (date.getMonth() + 1) * 100 + date.getDate()
  )
}

function labelOf(date: Date) {
  return `${date.getMonth() + 1}/${date.getDate()}`
}

function bucketDays(rows: QuotaDataItem[], start: Date, end: Date): DayPoint[] {
  const totals = new Map<
    number,
    { tokens: number; quota: number; count: number }
  >()
  for (const row of rows) {
    const stamp = Number(row.created_at)
    if (!Number.isFinite(stamp) || stamp <= 0) continue
    const ms = stamp < 1e12 ? stamp * 1000 : stamp
    const when = new Date(ms)
    const key = dayKey(when)
    const current = totals.get(key) ?? { tokens: 0, quota: 0, count: 0 }
    current.tokens += Number(row.token_used) || 0
    current.quota += Number(row.quota) || 0
    current.count += Number(row.count) || 0
    totals.set(key, current)
  }

  const points: DayPoint[] = []
  const cursor = new Date(
    start.getFullYear(),
    start.getMonth(),
    start.getDate()
  )
  const last = new Date(end.getFullYear(), end.getMonth(), end.getDate())
  while (cursor <= last) {
    const found = totals.get(dayKey(cursor))
    const quota = found?.quota ?? 0
    points.push({
      date: labelOf(cursor),
      tokens: found?.tokens ?? 0,
      quota,
      count: found?.count ?? 0,
      cost: quotaUnitsToDollars(quota),
    })
    cursor.setDate(cursor.getDate() + 1)
  }
  return points
}

const axisLabel = { style: { fill: 'rgba(255,255,255,0.72)', fontSize: 11 } }
const axisLine = { style: { stroke: 'rgba(255,255,255,0.22)' } }

// 卡片两种主题下都是深底，悬停件不能用 VChart 默认浅色主题的浅灰：
// 提示框用深藏蓝底白字（费用线的白点在白底提示框里会看不见）
const tooltipStyle = {
  panel: {
    backgroundColor: '#06182c',
    border: { color: 'rgba(255,255,255,0.16)', width: 1, radius: 8 },
    shadow: { x: 0, y: 8, blur: 24, spread: 0, color: 'rgba(0,0,0,0.35)' },
  },
  titleLabel: { fontColor: '#ffffff', fontWeight: 600 },
  keyLabel: { fontColor: 'rgba(255,255,255,0.72)' },
  valueLabel: { fontColor: '#ffffff', fontWeight: 600 },
}

export function useMonthUsage() {
  const bounds = useMemo(() => monthBounds(), [])
  const usageQuery = useQuery({
    queryKey: ['overview', 'month-usage', bounds.startSec, bounds.endSec],
    queryFn: () =>
      getUserQuotaDates(
        {
          start_timestamp: bounds.startSec,
          end_timestamp: bounds.endSec,
        },
        false
      ),
    staleTime: 60 * 1000,
  })
  const points = useMemo(
    () => bucketDays(usageQuery.data?.data ?? [], bounds.start, bounds.end),
    [usageQuery.data, bounds.start, bounds.end]
  )
  const today = points[points.length - 1]
  return { bounds, usageQuery, points, todayQuota: today?.quota ?? 0 }
}

export function MonthUsageChart() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { bounds, usageQuery, points } = useMonthUsage()
  const hasUsage = points.some((point) => point.tokens > 0 || point.quota > 0)
  const tokenLabel = t('Consumed tokens')
  const costLabel = t('Cost')

  const spec = useMemo(() => {
    const tooltipTitle = {
      value: (datum: Record<string, unknown>) => String(datum.date ?? ''),
    }
    const tooltipContent = [
      {
        key: (datum: Record<string, unknown>) => String(datum.series ?? ''),
        value: (datum: Record<string, unknown>) => {
          if (datum.series === costLabel)
            return formatQuota(Number(datum.quota ?? 0))
          return formatNumber(Number(datum.value ?? 0))
        },
      },
    ]
    return {
      type: 'common',
      background: 'transparent',
      padding: { top: 8, right: 8, bottom: 4, left: 8 },
      color: ['#80e9ff', '#ffffff'],
      data: [
        {
          id: 'tokens',
          values: points.map((point) => ({
            date: point.date,
            value: point.tokens,
            series: tokenLabel,
          })),
        },
        {
          id: 'cost',
          values: points.map((point) => ({
            date: point.date,
            value: point.cost,
            quota: point.quota,
            series: costLabel,
          })),
        },
      ],
      series: [
        {
          type: 'line',
          id: 'tokens',
          dataId: 'tokens',
          xField: 'date',
          yField: 'value',
          name: tokenLabel,
          point: { visible: true, style: { size: 5, fill: '#80e9ff' } },
          line: { style: { stroke: '#80e9ff', lineWidth: 2 } },
        },
        {
          type: 'line',
          id: 'cost',
          dataId: 'cost',
          xField: 'date',
          yField: 'value',
          name: costLabel,
          point: { visible: true, style: { size: 5, fill: '#ffffff' } },
          line: { style: { stroke: '#ffffff', lineWidth: 2 } },
        },
      ],
      axes: [
        {
          orient: 'left',
          seriesId: ['tokens'],
          label: {
            ...axisLabel,
            formatMethod: (value: string | string[]) =>
              formatCompactNumber(Number(value)),
          },
          domainLine: axisLine,
          grid: { visible: true, style: { stroke: 'rgba(255,255,255,0.12)' } },
        },
        {
          orient: 'right',
          seriesId: ['cost'],
          label: {
            ...axisLabel,
            formatMethod: (value: string | string[]) =>
              Number(value).toFixed(2),
          },
          domainLine: axisLine,
          grid: { visible: false },
        },
        {
          orient: 'bottom',
          type: 'band',
          label: axisLabel,
          domainLine: axisLine,
        },
      ],
      legends: {
        visible: true,
        orient: 'top',
        item: {
          // 悬停底用白色低透明度；默认主题的浅灰底会把白字吞掉
          background: {
            style: { cornerRadius: 4 },
            state: {
              selectedHover: { fill: '#ffffff', fillOpacity: 0.12 },
              unSelectedHover: { fill: '#ffffff', fillOpacity: 0.08 },
            },
          },
          label: {
            style: { fill: '#ffffff', fontSize: 12 },
            state: { unSelected: { fill: 'rgba(255,255,255,0.45)' } },
          },
        },
      },
      // 悬停整列（dimension）和悬停单点（mark）用同一套标题与内容，行名是系列名而不是日期
      tooltip: {
        style: tooltipStyle,
        mark: { title: tooltipTitle, content: tooltipContent },
        dimension: { title: tooltipTitle, content: tooltipContent },
      },
      // 类目轴的准星是整列色块：默认填充是浅色主题的网格灰，这里改成白色低透明度
      crosshair: {
        xField: {
          visible: true,
          line: {
            type: 'rect',
            style: { fill: '#ffffff', fillOpacity: 0.08, opacity: 1 },
          },
        },
      },
    }
  }, [points, tokenLabel, costLabel])

  return (
    <section className='dark:border-border dark:bg-card min-w-0 rounded-xl border border-[#0a2540] bg-[#0a2540] p-6 text-white shadow-[0_2px_4px_rgba(0,0,0,0.04),0_8px_16px_rgba(0,0,0,0.08)] md:p-8'>
      <div className='mb-6 flex flex-wrap items-start justify-between gap-4'>
        <div>
          <h3 className='text-xl font-semibold text-white md:text-2xl'>
            {t("This month's usage")}
          </h3>
          <p className='mt-2 max-w-prose text-base text-white'>
            {hasUsage
              ? t('Consumed tokens and cost for each day of this month.')
              : t('No usage this month yet.')}
          </p>
        </div>
        {usageQuery.isError ? (
          <button
            type='button'
            className={dashSecondaryBtn}
            onClick={() => {
              void queryClient.invalidateQueries({
                queryKey: [
                  'overview',
                  'month-usage',
                  bounds.startSec,
                  bounds.endSec,
                ],
              })
            }}
          >
            {t('Retry')}
          </button>
        ) : null}
      </div>
      {usageQuery.isLoading ? (
        <DashSkeleton className='h-64 w-full bg-white/10 lg:h-72 xl:h-80' />
      ) : usageQuery.isError ? (
        <p className='text-base text-white'>
          {t("Could not load this month's usage.")}
        </p>
      ) : (
        <div className='h-64 lg:h-72 xl:h-80'>
          <VChart spec={spec} options={VCHART_OPTION} />
        </div>
      )}
    </section>
  )
}

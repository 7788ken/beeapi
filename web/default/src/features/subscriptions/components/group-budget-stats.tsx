import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { VChart } from '@visactor/react-vchart'
import { Loader2, Users, Wallet } from 'lucide-react'
import { getRollingDateRange, formatChartTime } from '@/lib/time'
import { VCHART_OPTION } from '@/lib/vchart'
import { useTheme } from '@/context/theme-provider'
import { Skeleton } from '@/components/ui/skeleton'
import { renderQuotaCompat } from '@/features/dashboard/lib'
import { getUserQuotaDataByGroups } from '@/features/dashboard/api'
import { getSubscriptionGroupBudget } from '../api'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

const RANGE_OPTIONS: { days: number; label: string }[] = [
  { days: 7, label: '近 7 天' },
  { days: 14, label: '近 14 天' },
  { days: 30, label: '近 30 天' },
]

function dayKey(ts: number) {
  return formatChartTime(ts, 'day')
}

interface ChartPoint {
  Time: string
  Group: string
  Quota: number
}

export function GroupBudgetStats() {
  const { resolvedTheme } = useTheme()
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)
  const [days, setDays] = useState(7)

  useEffect(() => {
    const updateTheme = async () => {
      setThemeReady(false)
      if (!themeManagerPromise) {
        themeManagerPromise = import('@visactor/vchart').then(
          (m) => m.ThemeManager
        )
      }
      const ThemeManager = await themeManagerPromise
      themeManagerRef.current = ThemeManager
      ThemeManager.setCurrentTheme(resolvedTheme === 'dark' ? 'dark' : 'light')
      setThemeReady(true)
    }
    updateTheme()
  }, [resolvedTheme])

  // 1. 拉取每个 BoundGroup 的每日预算
  const { data: budgetData, isLoading: budgetLoading } = useQuery({
    queryKey: ['subscription-group-budget'],
    queryFn: async () => (await getSubscriptionGroupBudget()).data ?? [],
    staleTime: 60_000,
  })

  // 2. 拉取每个 group 的实际消耗（仅 subscription 计费源）
  const timeRange = useMemo(() => {
    const { start, end } = getRollingDateRange(days)
    return {
      start_timestamp: Math.floor(start.getTime() / 1000),
      end_timestamp: Math.floor(end.getTime() / 1000),
    }
  }, [days])

  const { data: usageData, isLoading: usageLoading } = useQuery({
    queryKey: ['subscription-group-usage', timeRange],
    queryFn: async () => {
      const res = await getUserQuotaDataByGroups({
        ...timeRange,
        billing_source: 'subscription',
      })
      return res.success ? res.data : []
    },
    staleTime: 60_000,
  })

  const isLoading = budgetLoading || usageLoading

  // 仅展示有预算的 group（即有 active 订阅的）
  const budgetMap = useMemo(() => {
    const m = new Map<
      string,
      {
        active_count: number
        daily_quota: number
        daily_price_usd: number
        currency: string
      }
    >()
    ;(budgetData ?? []).forEach((b) => {
      m.set(b.bound_group, {
        active_count: b.active_count,
        daily_quota: b.daily_quota,
        daily_price_usd: b.daily_price_usd,
        currency: b.currency || 'USD',
      })
    })
    return m
  }, [budgetData])

  const trackedGroups = useMemo(
    () =>
      Array.from(budgetMap.keys()).sort(
        (a, b) =>
          (budgetMap.get(b)?.daily_quota ?? 0) -
          (budgetMap.get(a)?.daily_quota ?? 0)
      ),
    [budgetMap]
  )

  // 按 group + day 聚合实际消耗
  const trendValues: ChartPoint[] = useMemo(() => {
    const trackedSet = new Set(trackedGroups)
    const buckets = new Map<string, number>() // key=`${group}|${dayLabel}`
    const allTimes = new Set<string>()
    ;(usageData ?? []).forEach((item) => {
      if (!item.group || !trackedSet.has(item.group)) return
      const tk = dayKey(Number(item.created_at))
      allTimes.add(tk)
      const k = `${item.group}|${tk}`
      buckets.set(k, (buckets.get(k) ?? 0) + (Number(item.quota) || 0))
    })
    const sortedTimes = Array.from(allTimes).sort()
    const out: ChartPoint[] = []
    sortedTimes.forEach((tk) => {
      trackedGroups.forEach((g) => {
        out.push({
          Time: tk,
          Group: g,
          Quota: buckets.get(`${g}|${tk}`) ?? 0,
        })
      })
    })
    return out
  }, [usageData, trackedGroups])

  // 每个 group 的实际近 N 天总消耗
  const actualTotals = useMemo(() => {
    const m = new Map<string, number>()
    trendValues.forEach((v) => {
      m.set(v.Group, (m.get(v.Group) ?? 0) + v.Quota)
    })
    return m
  }, [trendValues])

  // 折线图 spec
  const chartSpec = useMemo(() => {
    return {
      type: 'line',
      data: [{ id: 'usage', values: trendValues }],
      xField: 'Time',
      yField: 'Quota',
      seriesField: 'Group',
      stack: false,
      title: {
        visible: true,
        text: '订阅分组每日消耗趋势',
      },
      legends: { visible: true, selectMode: 'multiple' },
      axes: [
        { orient: 'bottom', type: 'band' },
        {
          orient: 'left',
          type: 'linear',
          label: {
            formatMethod: (v: number) => renderQuotaCompat(v, 1),
          },
        },
      ],
      point: { visible: true, size: 4 },
      tooltip: {
        mark: {
          content: [
            {
              key: (d: Record<string, unknown>) => d?.Group,
              value: (d: Record<string, unknown>) =>
                renderQuotaCompat(Number(d?.Quota) || 0, 2),
            },
          ],
        },
      },
      background: { fill: 'transparent' },
      animation: true,
    }
  }, [trendValues])

  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center gap-1.5 sm:gap-2'>
        <div className='flex shrink-0 items-center gap-1.5 rounded-md border p-0.5'>
          {RANGE_OPTIONS.map((opt) => (
            <button
              key={opt.days}
              type='button'
              onClick={() => setDays(opt.days)}
              className={`rounded-[5px] px-2.5 py-1 text-xs font-medium transition-colors ${
                days === opt.days
                  ? 'bg-primary text-primary-foreground shadow-sm'
                  : 'text-muted-foreground hover:bg-muted hover:text-foreground'
              }`}
            >
              {opt.label}
            </button>
          ))}
        </div>
        <span className='text-muted-foreground text-xs'>
          仅统计 status=active 的订阅，按 plan.bound_group 聚合
        </span>
        {isLoading ? (
          <Loader2 className='text-muted-foreground size-3.5 animate-spin' />
        ) : null}
      </div>

      {/* 卡片：每个 BoundGroup 的预算 vs 实际 */}
      <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
        {budgetLoading
          ? Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className='h-32 w-full' />
            ))
          : trackedGroups.length === 0
            ? (
                <div className='text-muted-foreground col-span-full rounded-lg border p-6 text-center text-sm'>
                  当前没有 active 订阅绑定到任何分组
                </div>
              )
            : trackedGroups.map((g) => {
                const b = budgetMap.get(g)!
                const actual = actualTotals.get(g) ?? 0
                const dailyAvg = days > 0 ? actual / days : 0
                const ratio =
                  b.daily_quota > 0 ? (dailyAvg / b.daily_quota) * 100 : 0
                const tone =
                  ratio >= 100
                    ? 'text-rose-600 dark:text-rose-400'
                    : ratio >= 75
                      ? 'text-amber-600 dark:text-amber-400'
                      : 'text-emerald-600 dark:text-emerald-400'
                return (
                  <div
                    key={g}
                    className='space-y-2 rounded-lg border p-4'
                  >
                    <div className='flex items-center gap-2'>
                      <div className='truncate text-sm font-semibold'>{g}</div>
                    </div>
                    <div className='text-muted-foreground flex items-center gap-3 text-xs'>
                      <div className='flex items-center gap-1'>
                        <Users className='size-3.5' />
                        {b.active_count} 份
                      </div>
                      <div className='flex items-center gap-1'>
                        <Wallet className='size-3.5' />
                        {b.currency} {b.daily_price_usd.toFixed(2)}/天
                      </div>
                    </div>
                    <div className='grid grid-cols-2 gap-2 pt-1 text-xs'>
                      <div>
                        <div className='text-muted-foreground'>每日预算</div>
                        <div className='font-semibold tabular-nums'>
                          {renderQuotaCompat(b.daily_quota, 2)}
                        </div>
                      </div>
                      <div>
                        <div className='text-muted-foreground'>
                          {days} 天日均
                        </div>
                        <div className={`font-semibold tabular-nums ${tone}`}>
                          {renderQuotaCompat(dailyAvg, 2)}
                        </div>
                      </div>
                    </div>
                    <div className='bg-muted h-1.5 w-full overflow-hidden rounded-full'>
                      <div
                        className={`h-full rounded-full transition-all ${
                          ratio >= 100
                            ? 'bg-rose-500'
                            : ratio >= 75
                              ? 'bg-amber-500'
                              : 'bg-emerald-500'
                        }`}
                        style={{ width: `${Math.min(ratio, 100).toFixed(1)}%` }}
                      />
                    </div>
                    <div className='text-muted-foreground text-xs'>
                      使用率 {ratio.toFixed(0)}%
                    </div>
                  </div>
                )
              })}
      </div>

      {/* 折线图：每日实际消耗趋势 */}
      <div className='overflow-hidden rounded-lg border'>
        <div className='border-b px-3 py-2 sm:px-5 sm:py-3'>
          <div className='text-sm font-semibold'>订阅分组每日消耗趋势</div>
          <div className='text-muted-foreground text-xs'>
            横轴：日期 · 纵轴：实际消耗（仅 billing_source=subscription）
          </div>
        </div>
        <div className='h-[340px] p-1.5 sm:h-96 sm:p-2'>
          {isLoading ? (
            <Skeleton className='h-full w-full' />
          ) : trendValues.length === 0 ? (
            <div className='text-muted-foreground flex h-full items-center justify-center text-sm'>
              暂无消耗数据
            </div>
          ) : (
            themeReady && (
              <VChart
                key={`group-budget-${days}-${resolvedTheme}`}
                spec={{
                  ...chartSpec,
                  theme: resolvedTheme === 'dark' ? 'dark' : 'light',
                  background: 'transparent',
                }}
                option={VCHART_OPTION}
              />
            )
          )}
        </div>
      </div>
    </div>
  )
}

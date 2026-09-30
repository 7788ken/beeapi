import { useEffect, useMemo, useState, useRef, useCallback } from 'react'
import { useQuery } from '@tanstack/react-query'
import { VChart } from '@visactor/react-vchart'
import { Users, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getRollingDateRange, type TimeGranularity } from '@/lib/time'
import { cn } from '@/lib/utils'
import { VCHART_OPTION } from '@/lib/vchart'
import { useTheme } from '@/context/theme-provider'
import { surfaceClass } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { getUserQuotaDataByUsers } from '@/features/dashboard/api'
import {
  TIME_GRANULARITY_OPTIONS,
  TIME_RANGE_PRESETS,
} from '@/features/dashboard/constants'
import {
  getDefaultDays,
  getSavedGranularity,
  saveGranularity,
  processUserChartData,
} from '@/features/dashboard/lib'
import type { ProcessedUserChartData } from '@/features/dashboard/types'
import { dashSegItem, dashSegTrack } from '../overview/dash-emphasis'
import { UserGroupsDialog } from './user-groups-dialog'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

const USER_CHARTS: {
  value: string
  labelKey: string
  specKey: keyof ProcessedUserChartData
}[] = [
  {
    value: 'rank',
    labelKey: 'User Consumption Ranking',
    specKey: 'spec_user_rank',
  },
  {
    value: 'trend',
    labelKey: 'User Consumption Trend',
    specKey: 'spec_user_trend',
  },
]

const TOP_USER_LIMIT_OPTIONS = [5, 10, 20, 50]

function getClickedUsername(event: unknown) {
  const datum = (event as { datum?: unknown })?.datum
  const row = Array.isArray(datum) ? datum[0] : datum
  if (!row || typeof row !== 'object') return ''
  const value =
    (row as Record<string, unknown>).username ??
    (row as Record<string, unknown>).User
  return typeof value === 'string' ? value : ''
}

export function UserCharts() {
  const { t } = useTranslation()
  const { resolvedTheme } = useTheme()
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)

  const [timeGranularity, setTimeGranularity] = useState<TimeGranularity>(() =>
    getSavedGranularity()
  )
  const [selectedRange, setSelectedRange] = useState<number>(() =>
    getDefaultDays(timeGranularity)
  )
  const [topUserLimit, setTopUserLimit] = useState(10)
  const [selectedUser, setSelectedUser] = useState('')
  const [groupsDialogOpen, setGroupsDialogOpen] = useState(false)
  const [timeRange, setTimeRange] = useState(() => {
    const days = getDefaultDays(timeGranularity)
    const { start, end } = getRollingDateRange(days)
    return {
      start_timestamp: Math.floor(start.getTime() / 1000),
      end_timestamp: Math.floor(end.getTime() / 1000),
    }
  })

  const handleRangeChange = useCallback((days: number) => {
    setSelectedRange(days)
    const { start, end } = getRollingDateRange(days)
    setTimeRange({
      start_timestamp: Math.floor(start.getTime() / 1000),
      end_timestamp: Math.floor(end.getTime() / 1000),
    })
  }, [])

  const handleGranularityChange = useCallback(
    (g: TimeGranularity) => {
      setTimeGranularity(g)
      saveGranularity(g)
      const days = getDefaultDays(g)
      if (days !== selectedRange) {
        handleRangeChange(days)
      }
    },
    [selectedRange, handleRangeChange]
  )

  const handleUserBarClick = useCallback((event: unknown) => {
    const username = getClickedUsername(event)
    if (!username) return
    setSelectedUser(username)
    setGroupsDialogOpen(true)
  }, [])

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

  const { data: userData, isLoading } = useQuery({
    queryKey: ['dashboard', 'user-quota', timeRange],
    queryFn: () => getUserQuotaDataByUsers(timeRange),
    select: (res) => (res.success ? res.data : []),
    staleTime: 60_000,
  })

  const chartData = useMemo(
    () =>
      processUserChartData(
        isLoading ? [] : (userData ?? []),
        timeGranularity,
        t,
        topUserLimit
      ),
    [userData, isLoading, timeGranularity, t, topUserLimit]
  )

  const isEmpty = !isLoading && (userData?.length ?? 0) === 0

  return (
    <div className='space-y-6'>
      <div className='flex items-center gap-2 overflow-x-auto pb-1 sm:gap-3'>
        <div className={dashSegTrack}>
          {TIME_RANGE_PRESETS.map((preset) => (
            <button
              key={preset.days}
              type='button'
              onClick={() => handleRangeChange(preset.days)}
              aria-pressed={selectedRange === preset.days}
              className={dashSegItem(selectedRange === preset.days)}
            >
              {t(preset.label)}
            </button>
          ))}
        </div>

        <div className={dashSegTrack}>
          {TIME_GRANULARITY_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              type='button'
              onClick={() =>
                handleGranularityChange(opt.value as TimeGranularity)
              }
              aria-pressed={timeGranularity === opt.value}
              className={dashSegItem(timeGranularity === opt.value)}
            >
              {t(opt.label)}
            </button>
          ))}
        </div>

        <div className={dashSegTrack}>
          <span className='text-muted-foreground px-2 text-xs font-medium'>
            {t('Top Users')}
          </span>
          {TOP_USER_LIMIT_OPTIONS.map((limit) => (
            <button
              key={limit}
              type='button'
              onClick={() => setTopUserLimit(limit)}
              aria-pressed={topUserLimit === limit}
              className={dashSegItem(topUserLimit === limit)}
            >
              {t('Top {{count}}', { count: limit })}
            </button>
          ))}
        </div>

        {isLoading && (
          <Loader2 className='text-muted-foreground size-4 animate-spin' />
        )}
      </div>

      <div className='grid gap-6'>
        {USER_CHARTS.map((chart) => {
          const spec = chartData[chart.specKey]
          const chartSpec =
            chart.value === 'rank' && spec
              ? {
                  ...spec,
                  bar: {
                    ...(spec.bar ?? {}),
                    style: {
                      ...(spec.bar?.style ?? {}),
                      cursor: 'pointer',
                    },
                  },
                }
              : spec

          return (
            <div
              key={chart.value}
              className={cn(surfaceClass, 'overflow-hidden')}
            >
              <div className='flex w-full items-center gap-2 border-b px-3 py-2 sm:px-5 sm:py-3'>
                <Users className='text-muted-foreground/60 size-4' />
                <div>
                  <div className='text-sm font-semibold'>
                    {t(chart.labelKey)}
                  </div>
                  {chart.value === 'rank' ? (
                    <div className='text-muted-foreground text-xs'>
                      {t('Click a bar to view group breakdown')}
                    </div>
                  ) : null}
                </div>
              </div>

              <div
                className={cn(
                  'p-1.5 sm:p-2',
                  // 空态不需要撑满图表高度
                  isEmpty ? 'h-40' : 'h-[300px] sm:h-96'
                )}
              >
                {isLoading ? (
                  <Skeleton className='h-full w-full' />
                ) : isEmpty ? (
                  <div className='text-muted-foreground flex h-full items-center justify-center text-sm'>
                    {t('No data in selected range')}
                  </div>
                ) : (
                  themeReady &&
                  spec && (
                    <VChart
                      key={`user-${chart.value}-${topUserLimit}-${resolvedTheme}`}
                      spec={{
                        ...chartSpec,
                        theme: resolvedTheme === 'dark' ? 'dark' : 'light',
                        background: 'transparent',
                      }}
                      option={VCHART_OPTION}
                      onClick={
                        chart.value === 'rank' ? handleUserBarClick : undefined
                      }
                    />
                  )
                )}
              </div>
            </div>
          )
        })}
      </div>

      <UserGroupsDialog
        open={groupsDialogOpen}
        onOpenChange={setGroupsDialogOpen}
        username={selectedUser}
        startTimestamp={timeRange.start_timestamp}
        endTimestamp={timeRange.end_timestamp}
      />
    </div>
  )
}

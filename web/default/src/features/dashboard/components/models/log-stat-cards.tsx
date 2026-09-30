import { useEffect, useState } from 'react'
import { useAuthStore } from '@/stores/auth-store'
import { formatNumber, formatQuota } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import { cn } from '@/lib/utils'
import { surfaceClass } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { getUserQuotaDates } from '@/features/dashboard/api'
import { useModelStatCardsConfig } from '@/features/dashboard/hooks/use-dashboard-config'
import {
  buildQueryParams,
  calculateDashboardStats,
  getDefaultDays,
} from '@/features/dashboard/lib'
import type {
  QuotaDataItem,
  DashboardFilters,
} from '@/features/dashboard/types'
import { dashHeroCard } from '../overview/dash-emphasis'
import { DashSkeleton } from '../overview/dash-style'

interface LogStatCardsProps {
  filters?: DashboardFilters
  onDataUpdate?: (data: QuotaDataItem[], loading: boolean) => void
}

export function LogStatCards(props: LogStatCardsProps) {
  const statCardsConfig = useModelStatCardsConfig()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = !!(user?.role && user.role >= 10)
  const [stats, setStats] = useState<{
    totalQuota: number
    totalCount: number
    totalTokens: number
  } | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)

  const [timeRangeMinutes, setTimeRangeMinutes] = useState(0)

  const { filters, onDataUpdate } = props

  useEffect(() => {
    const abortController = new AbortController()
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)

    setError(false)
    onDataUpdate?.([], true)

    const timeRange = computeTimeRange(
      getDefaultDays(filters?.time_granularity),
      filters?.start_timestamp,
      filters?.end_timestamp
    )
    const timeDiff = (timeRange.end_timestamp - timeRange.start_timestamp) / 60
    setTimeRangeMinutes(timeDiff)

    getUserQuotaDates(buildQueryParams(timeRange, filters), isAdmin)
      .then((res) => {
        if (abortController.signal.aborted) return
        const data = res?.data || []
        setStats(calculateDashboardStats(data))
        onDataUpdate?.(data, false)
      })
      .catch(() => {
        if (abortController.signal.aborted) return
        setStats(null)
        setError(true)
        onDataUpdate?.([], false)
      })
      .finally(() => {
        if (!abortController.signal.aborted) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
    }
  }, [filters, isAdmin, onDataUpdate])

  const adaptedStats = {
    rpm: stats?.totalCount ?? 0,
    quota: stats?.totalQuota ?? 0,
    tpm: stats?.totalTokens ?? 0,
  }

  const items = statCardsConfig.map((config) => ({
    key: config.key,
    title: config.title,
    value:
      config.key === 'quota'
        ? formatQuota(config.getValue(adaptedStats, timeRangeMinutes))
        : formatNumber(config.getValue(adaptedStats, timeRangeMinutes)),
    desc: config.description,
    icon: config.icon,
  }))

  return (
    <div className='grid grid-cols-2 gap-6 md:grid-cols-3 xl:grid-cols-5'>
      {items.map((it, idx) => {
        const Icon = it.icon
        const spanLast = idx === items.length - 1 && items.length % 2 !== 0
        const hero = it.key === 'count'
        const valueTone = error ? 'text-muted-foreground' : 'text-foreground'
        const descTone = error
          ? 'text-muted-foreground/40'
          : 'text-muted-foreground/60'
        const placeholder = hero ? (
          <>
            <DashSkeleton className='h-7 w-20 bg-white/20' />
            <DashSkeleton className='h-3.5 w-28 bg-white/20' />
          </>
        ) : (
          <>
            <Skeleton className='h-7 w-20' />
            <Skeleton className='h-3.5 w-28' />
          </>
        )
        return (
          <article
            key={it.title}
            className={cn(
              hero ? dashHeroCard : surfaceClass,
              'flex min-w-0 flex-col justify-between gap-3 p-5',
              spanLast && 'col-span-2 md:col-span-1'
            )}
          >
            <div className='flex items-center gap-2'>
              <Icon
                className={cn(
                  'size-3.5 shrink-0',
                  hero ? 'text-primary-foreground' : 'text-muted-foreground/60'
                )}
              />
              <span
                className={cn(
                  'truncate text-xs font-medium tracking-wider uppercase',
                  hero ? 'text-primary-foreground' : 'text-muted-foreground'
                )}
              >
                {it.title}
              </span>
            </div>

            {loading ? (
              <div className='space-y-1.5'>{placeholder}</div>
            ) : (
              <div className='min-w-0'>
                <div
                  className={cn(
                    'truncate font-mono text-2xl font-bold tracking-tight tabular-nums',
                    hero ? 'text-primary-foreground' : valueTone
                  )}
                >
                  {error ? '--' : it.value}
                </div>
                <div
                  className={cn(
                    'mt-1 truncate text-xs',
                    hero ? 'text-primary-foreground' : descTone
                  )}
                >
                  {it.desc}
                </div>
              </div>
            )}
          </article>
        )
      })}
    </div>
  )
}

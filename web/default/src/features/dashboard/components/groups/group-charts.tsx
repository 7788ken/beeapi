import { Fragment, useEffect, useMemo, useState, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { VChart } from '@visactor/react-vchart'
import { ChevronDown, ChevronRight, Layers, Loader2 } from 'lucide-react'
import { getRollingDateRange } from '@/lib/time'
import { VCHART_OPTION } from '@/lib/vchart'
import { useTheme } from '@/context/theme-provider'
import { Skeleton } from '@/components/ui/skeleton'
import {
  getGroupTopUsers,
  getUserQuotaDataByGroups,
} from '@/features/dashboard/api'
import {
  processUserChartData,
  renderQuotaCompat,
} from '@/features/dashboard/lib'
import type {
  GroupDashboardFilters,
  QuotaDataItem,
} from '@/features/dashboard/types'

const GROUP_MEMBER_TOP_N = 10

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

const BILLING_SOURCE_HINT: Record<string, string> = {
  '': '全部计费源',
  subscription: '订阅抵扣',
  wallet: '余额扣费',
}

interface GroupChartsProps {
  filters: GroupDashboardFilters
}

interface GroupRankRow {
  group: string
  count: number
  quota: number
  tokenUsed: number
  ratio: number
}

interface GroupMembersRowProps {
  group: string
  startTimestamp: number
  endTimestamp: number
  billingSource?: string
}

function GroupMembersRow(props: GroupMembersRowProps) {
  const { data: members, isLoading } = useQuery({
    queryKey: [
      'dashboard',
      'group-top-users',
      props.group,
      props.startTimestamp,
      props.endTimestamp,
      props.billingSource ?? '',
    ],
    queryFn: () =>
      getGroupTopUsers({
        group: props.group,
        start_timestamp: props.startTimestamp,
        end_timestamp: props.endTimestamp,
        billing_source: props.billingSource || undefined,
        limit: GROUP_MEMBER_TOP_N,
      }),
    select: (res) => (res.success ? res.data : []),
    staleTime: 60_000,
  })

  const maxMemberQuota = useMemo(() => {
    if (!members || members.length === 0) return 0
    return Math.max(...members.map((m) => Number(m.quota) || 0))
  }, [members])

  return (
    <tr className='border-l-4 border-l-primary/40 border-t bg-muted/50'>
      <td className='px-3 py-2 sm:px-5' colSpan={6}>
        {isLoading ? (
          <div className='space-y-1'>
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className='h-5 w-full' />
            ))}
          </div>
        ) : !members || members.length === 0 ? (
          <div className='text-muted-foreground py-2 text-xs'>
            暂无成员消费数据
          </div>
        ) : (
          <table className='w-full text-xs'>
            <thead className='text-muted-foreground'>
              <tr className='text-left'>
                <th className='w-10 py-1 pl-1 pr-2 text-center'>#</th>
                <th className='py-1 pr-2'>用户</th>
                <th className='hidden w-32 py-1 pr-2 sm:table-cell'>占比</th>
                <th className='py-1 pr-2 text-right'>调用次数</th>
                <th className='hidden py-1 pr-2 text-right sm:table-cell'>
                  Tokens
                </th>
                <th className='py-1 pr-2 text-right'>消耗</th>
              </tr>
            </thead>
            <tbody>
              {members.map((m, i) => {
                const memberQuota = Number(m.quota) || 0
                const memberRatio =
                  maxMemberQuota > 0 ? memberQuota / maxMemberQuota : 0
                return (
                  <tr
                    key={`${m.user_id ?? 0}-${m.username ?? ''}`}
                    className='border-t border-border/40'
                  >
                    <td className='py-1 pl-1 pr-2 text-center text-muted-foreground'>
                      {i + 1}
                    </td>
                    <td className='py-1 pr-2'>{m.username || '(未知)'}</td>
                    <td className='hidden py-1 pr-2 sm:table-cell'>
                      <div className='bg-background/60 h-1.5 w-full overflow-hidden rounded-full'>
                        <div
                          className='bg-primary/70 h-full rounded-full transition-all'
                          style={{
                            width: `${Math.max(memberRatio * 100, 2).toFixed(1)}%`,
                          }}
                        />
                      </div>
                    </td>
                    <td className='py-1 pr-2 text-right tabular-nums'>
                      {formatInt(Number(m.count) || 0)}
                    </td>
                    <td className='hidden py-1 pr-2 text-right tabular-nums sm:table-cell'>
                      {formatInt(Number(m.token_used) || 0)}
                    </td>
                    <td className='py-1 pr-2 text-right font-semibold tabular-nums'>
                      {renderQuotaCompat(memberQuota, 2)}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </td>
    </tr>
  )
}

function toTimeRange(filters: GroupDashboardFilters) {
  const start = filters.start_timestamp ?? getRollingDateRange(7).start
  const end = filters.end_timestamp ?? getRollingDateRange(7).end
  return {
    start_timestamp: Math.floor(start.getTime() / 1000),
    end_timestamp: Math.floor(end.getTime() / 1000),
  }
}

const formatInt = (n: number) =>
  Intl.NumberFormat(undefined, { maximumFractionDigits: 0 }).format(n)

export function GroupCharts(props: GroupChartsProps) {
  const { resolvedTheme } = useTheme()
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)

  const { filters } = props
  const timeRange = useMemo(() => toTimeRange(filters), [filters])
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())

  const toggleGroupExpansion = (group: string) => {
    setExpandedGroups((prev) => {
      const next = new Set(prev)
      if (next.has(group)) {
        next.delete(group)
      } else {
        next.add(group)
      }
      return next
    })
  }

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

  const queryParams = useMemo(
    () => ({
      ...timeRange,
      ...(filters.username ? { username: filters.username } : {}),
      ...(filters.billing_source
        ? { billing_source: filters.billing_source }
        : {}),
    }),
    [timeRange, filters.username, filters.billing_source]
  )

  const { data: rawData, isLoading } = useQuery({
    queryKey: ['dashboard', 'group-quota', queryParams],
    queryFn: () => getUserQuotaDataByGroups(queryParams),
    select: (res) => (res.success ? res.data : []),
    staleTime: 60_000,
  })

  const filteredData: QuotaDataItem[] = useMemo(
    () => (rawData ?? []).filter((item) => item.group && item.group !== ''),
    [rawData]
  )

  const topN = filters.top_groups ?? 10

  const rankRows: GroupRankRow[] = useMemo(() => {
    const totals = new Map<
      string,
      { count: number; quota: number; tokenUsed: number }
    >()
    filteredData.forEach((item) => {
      const key = item.group ?? ''
      const cur = totals.get(key) ?? { count: 0, quota: 0, tokenUsed: 0 }
      cur.count += Number(item.count) || 0
      cur.quota += Number(item.quota) || 0
      cur.tokenUsed += Number(item.token_used) || 0
      totals.set(key, cur)
    })
    const sorted = Array.from(totals.entries())
      .map(([group, v]) => ({ group, ...v }))
      .sort((a, b) => b.quota - a.quota)
      .slice(0, topN)
    const maxQuota = sorted[0]?.quota || 1
    return sorted.map((row) => ({
      ...row,
      ratio: maxQuota > 0 ? row.quota / maxQuota : 0,
    }))
  }, [filteredData, topN])

  // 趋势图复用 processUserChartData（把 group 映射成 username）
  const trendRemapped: QuotaDataItem[] = useMemo(
    () =>
      filteredData.map((item) => ({
        ...item,
        username: item.group,
      })),
    [filteredData]
  )

  const trendChartData = useMemo(
    () =>
      processUserChartData(
        isLoading ? [] : trendRemapped,
        filters.time_granularity ?? 'day',
        undefined,
        topN
      ),
    [trendRemapped, isLoading, filters.time_granularity, topN]
  )

  const trendSpec = trendChartData.spec_user_trend

  const billingHint =
    BILLING_SOURCE_HINT[filters.billing_source ?? ''] ?? '全部计费源'

  const totalQuota = useMemo(
    () => rankRows.reduce((s, r) => s + r.quota, 0),
    [rankRows]
  )

  return (
    <div className='space-y-3'>
      <div className='text-muted-foreground flex flex-wrap items-center gap-2 text-xs'>
        <span>当前维度：</span>
        <span className='bg-muted text-foreground rounded px-2 py-0.5'>
          {billingHint}
        </span>
        {filters.username ? (
          <span className='bg-muted text-foreground rounded px-2 py-0.5'>
            用户：{filters.username}
          </span>
        ) : null}
        <span className='bg-muted text-foreground rounded px-2 py-0.5'>
          Top {topN}
        </span>
        {isLoading ? <Loader2 className='size-3.5 animate-spin' /> : null}
      </div>

      {/* 分组消耗排行：列表 */}
      <div className='overflow-hidden rounded-lg border'>
        <div className='flex w-full items-center justify-between gap-2 border-b px-3 py-2 sm:px-5 sm:py-3'>
          <div className='flex items-center gap-2'>
            <Layers className='text-muted-foreground/60 size-4' />
            <div className='text-sm font-semibold'>分组消耗排行</div>
          </div>
          <div className='text-muted-foreground text-xs'>
            合计：{renderQuotaCompat(totalQuota, 2)}
          </div>
        </div>

        <div className='overflow-x-auto'>
          <table className='w-full text-sm'>
            <thead className='bg-muted/40 text-muted-foreground'>
              <tr className='text-left text-xs'>
                <th className='w-12 px-3 py-2 text-center sm:px-5'>#</th>
                <th className='px-3 py-2 sm:px-5'>分组</th>
                <th className='px-3 py-2 text-right sm:px-5'>调用次数</th>
                <th className='hidden px-3 py-2 text-right sm:table-cell sm:px-5'>
                  Tokens
                </th>
                <th className='px-3 py-2 text-right sm:px-5'>消耗</th>
                <th className='hidden w-40 px-3 py-2 sm:table-cell sm:px-5'>
                  占比
                </th>
              </tr>
            </thead>
            <tbody>
              {isLoading ? (
                Array.from({ length: 5 }).map((_, i) => (
                  <tr key={i} className='border-t'>
                    <td className='px-3 py-2 sm:px-5' colSpan={6}>
                      <Skeleton className='h-5 w-full' />
                    </td>
                  </tr>
                ))
              ) : rankRows.length === 0 ? (
                <tr>
                  <td
                    className='text-muted-foreground px-3 py-6 text-center sm:px-5'
                    colSpan={6}
                  >
                    暂无数据
                  </td>
                </tr>
              ) : (
                rankRows.map((row, idx) => {
                  const isExpanded = expandedGroups.has(row.group)
                  return (
                    <Fragment key={row.group}>
                      <tr
                        className='hover:bg-muted/40 cursor-pointer border-t transition-colors'
                        onClick={() => toggleGroupExpansion(row.group)}
                      >
                        <td className='px-3 py-2 text-center sm:px-5'>
                          <span
                            className={
                              idx < 3
                                ? 'inline-flex size-6 items-center justify-center rounded-full bg-primary/10 text-primary text-xs font-semibold'
                                : 'text-muted-foreground text-xs'
                            }
                          >
                            {idx + 1}
                          </span>
                        </td>
                        <td className='px-3 py-2 font-medium sm:px-5'>
                          <div className='flex items-center gap-1.5'>
                            {isExpanded ? (
                              <ChevronDown className='text-muted-foreground size-3.5 shrink-0' />
                            ) : (
                              <ChevronRight className='text-muted-foreground size-3.5 shrink-0' />
                            )}
                            <span>{row.group || '(空)'}</span>
                          </div>
                        </td>
                        <td className='px-3 py-2 text-right tabular-nums sm:px-5'>
                          {formatInt(row.count)}
                        </td>
                        <td className='hidden px-3 py-2 text-right tabular-nums sm:table-cell sm:px-5'>
                          {formatInt(row.tokenUsed)}
                        </td>
                        <td className='px-3 py-2 text-right font-semibold tabular-nums sm:px-5'>
                          {renderQuotaCompat(row.quota, 2)}
                        </td>
                        <td className='hidden px-3 py-2 sm:table-cell sm:px-5'>
                          <div className='bg-muted h-1.5 w-full overflow-hidden rounded-full'>
                            <div
                              className='bg-primary h-full rounded-full transition-all'
                              style={{
                                width: `${Math.max(row.ratio * 100, 2).toFixed(1)}%`,
                              }}
                            />
                          </div>
                        </td>
                      </tr>
                      {isExpanded && (
                        <GroupMembersRow
                          group={row.group}
                          startTimestamp={timeRange.start_timestamp}
                          endTimestamp={timeRange.end_timestamp}
                          billingSource={filters.billing_source}
                        />
                      )}
                    </Fragment>
                  )
                })
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* 分组消耗趋势：保留图表 */}
      <div className='overflow-hidden rounded-lg border'>
        <div className='flex w-full items-center gap-2 border-b px-3 py-2 sm:px-5 sm:py-3'>
          <Layers className='text-muted-foreground/60 size-4' />
          <div className='text-sm font-semibold'>分组消耗趋势</div>
        </div>

        <div className='h-[300px] p-1.5 sm:h-96 sm:p-2'>
          {isLoading ? (
            <Skeleton className='h-full w-full' />
          ) : (
            themeReady &&
            trendSpec && (
              <VChart
                key={`group-trend-${topN}-${filters.billing_source ?? ''}-${resolvedTheme}`}
                spec={{
                  ...trendSpec,
                  title: {
                    ...(trendSpec.title ?? {}),
                    text: '分组消耗趋势',
                  },
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

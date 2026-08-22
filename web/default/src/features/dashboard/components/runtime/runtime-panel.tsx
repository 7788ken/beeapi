// 运行时大盘 — 5min 滚动窗口 Top10。
// 数据来自 service.RuntimeMetricsTask（后台 60s 重算，存 Redis），不走 logs 实时查询。

import { useEffect, useRef, useState, useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { RefreshCw, Pause, Play, TrendingUp, Users, Layers, Cable, Network } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import { formatQuota } from '@/lib/format'
import { getRuntimeSnapshot, triggerRecomputeRuntime, RuntimeRateLimitedError } from './api'
import type {
  RuntimeSnapshot,
  RuntimeSortMode,
  TopUserItem,
  TopGroupItem,
  TopChannelItem,
  TopGroupChannelItem,
} from './types'

const QUERY_KEY = ['dashboard', 'runtime-metrics'] as const

function fmtRpm(rpm: number): string {
  if (rpm >= 100) return rpm.toFixed(0)
  if (rpm >= 10) return rpm.toFixed(1)
  return rpm.toFixed(2)
}

function fmtRelative(ts: number, t: (key: string) => string): string {
  if (!ts) return '-'
  const diff = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  if (diff < 60) return `${diff}${t('s ago')}`
  if (diff < 3600) return `${Math.floor(diff / 60)}${t('m ago')}`
  return `${Math.floor(diff / 3600)}${t('h ago')}`
}

interface RankBadgeProps {
  rank: number
}
function RankBadge({ rank }: RankBadgeProps) {
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

// 通用面板壳：标题 + 副标签（如 "按 RPM"） + 内容
interface PanelShellProps {
  title: string
  icon: React.ReactNode
  sortHint?: string // 当全局 sort 模式不适用本面板时显示
  children: React.ReactNode
}
function PanelShell({ title, icon, sortHint, children }: PanelShellProps) {
  return (
    <Card>
      <CardHeader className='py-3'>
        <div className='flex items-center justify-between'>
          <CardTitle className='flex items-center gap-2 text-sm font-semibold'>
            {icon}
            {title}
          </CardTitle>
          {sortHint && (
            <span className='text-muted-foreground text-[11px]'>
              {sortHint}
            </span>
          )}
        </div>
      </CardHeader>
      <CardContent className='space-y-1 py-1'>{children}</CardContent>
    </Card>
  )
}

// ── 用户面板 ──
function UsersPanel({ items, sortMode, t }: { items: TopUserItem[]; sortMode: RuntimeSortMode; t: (k: string) => string }) {
  return (
    <PanelShell title={t('Top 10 Users')} icon={<Users className='h-4 w-4' />}>
      {!items || items.length === 0 ? (
        <div className='text-muted-foreground py-6 text-center text-xs'>
          {t('No data')}
        </div>
      ) : (
        items.map((u, idx) => (
          <div
            key={u.user_id}
            className='flex items-center gap-2 py-1.5 text-xs'
          >
            <RankBadge rank={idx + 1} />
            <div className='min-w-0 flex-1'>
              <div className='truncate font-medium' title={u.username}>
                {u.username || `#${u.user_id}`}
              </div>
              {u.top_model && (
                <div className='text-muted-foreground truncate text-[10px]'>
                  {u.top_model}
                </div>
              )}
            </div>
            <div className='shrink-0 text-right tabular-nums'>
              {sortMode === 'cost' && (
                <span className='font-mono'>{formatQuota(u.cost)}</span>
              )}
              {sortMode === 'balance' && (
                <span className='font-mono'>{formatQuota(u.balance)}</span>
              )}
              {sortMode === 'rpm' && (
                <span className='font-mono'>{fmtRpm(u.rpm)} RPM</span>
              )}
            </div>
          </div>
        ))
      )}
    </PanelShell>
  )
}

// ── 分组面板 ──
function GroupsPanel({ items, sortMode, t }: { items: TopGroupItem[]; sortMode: RuntimeSortMode; t: (k: string) => string }) {
  // 余额维度对分组不适用，提示走 RPM
  const isBalance = sortMode === 'balance'
  return (
    <PanelShell
      title={t('Top 10 Groups')}
      icon={<Layers className='h-4 w-4' />}
      sortHint={isBalance ? t('shown by RPM') : undefined}
    >
      {!items || items.length === 0 ? (
        <div className='text-muted-foreground py-6 text-center text-xs'>
          {t('No data')}
        </div>
      ) : (
        items.map((g, idx) => (
          <div
            key={g.group}
            className='flex items-center gap-2 py-1.5 text-xs'
          >
            <RankBadge rank={idx + 1} />
            <div className='min-w-0 flex-1'>
              <div className='truncate font-medium' title={g.group}>
                {g.group}
              </div>
              <div className='text-muted-foreground truncate text-[10px]'>
                {g.user_count} {t('users')} · {g.channel_used} {t('channels')}
              </div>
            </div>
            <div className='shrink-0 text-right tabular-nums font-mono'>
              {sortMode === 'cost' ? (
                <>{formatQuota(g.cost)}</>
              ) : (
                <>{fmtRpm(g.rpm)} RPM</>
              )}
            </div>
          </div>
        ))
      )}
    </PanelShell>
  )
}

// ── 渠道面板 ──
function ChannelsPanel({ items, sortMode, t }: { items: TopChannelItem[]; sortMode: RuntimeSortMode; t: (k: string) => string }) {
  return (
    <PanelShell
      title={t('Top 10 Channels')}
      icon={<Cable className='h-4 w-4' />}
    >
      {!items || items.length === 0 ? (
        <div className='text-muted-foreground py-6 text-center text-xs'>
          {t('No data')}
        </div>
      ) : (
        items.map((c, idx) => {
          const total = c.success_count + c.error_count
          const successRate = total > 0 ? (c.success_count / total) * 100 : 0
          return (
            <div
              key={c.channel_id}
              className='flex items-center gap-2 py-1.5 text-xs'
            >
              <RankBadge rank={idx + 1} />
              <div className='min-w-0 flex-1'>
                <div className='truncate font-medium' title={c.channel_name}>
                  {c.channel_name || `#${c.channel_id}`}
                </div>
                {sortMode !== 'balance' && total > 0 && (
                  <div className='text-muted-foreground truncate text-[10px]'>
                    {successRate.toFixed(1)}% · {c.error_count > 0 && (
                      <span className='text-rose-500'>{c.error_count} err</span>
                    )}
                  </div>
                )}
              </div>
              <div className='shrink-0 text-right tabular-nums font-mono'>
                {sortMode === 'rpm' && <>{fmtRpm(c.rpm)} RPM</>}
                {sortMode === 'cost' && <>{formatQuota(c.cost)}</>}
                {sortMode === 'balance' && <>${c.balance.toFixed(2)}</>}
              </div>
            </div>
          )
        })
      )}
    </PanelShell>
  )
}

// ── 分组×渠道面板 ──
function GroupChannelsPanel({ items, sortMode, t }: { items: TopGroupChannelItem[]; sortMode: RuntimeSortMode; t: (k: string) => string }) {
  const isBalance = sortMode === 'balance'
  return (
    <PanelShell
      title={t('Top 10 Group × Channel')}
      icon={<Network className='h-4 w-4' />}
      sortHint={isBalance ? t('shown by RPM') : undefined}
    >
      {!items || items.length === 0 ? (
        <div className='text-muted-foreground py-6 text-center text-xs'>
          {t('No data')}
        </div>
      ) : (
        items.map((gc, idx) => (
          <div
            key={`${gc.group}-${gc.channel_id}`}
            className='flex items-center gap-2 py-1.5 text-xs'
          >
            <RankBadge rank={idx + 1} />
            <div className='min-w-0 flex-1'>
              <div className='truncate font-medium' title={gc.group}>
                {gc.group}
              </div>
              <div className='text-muted-foreground truncate text-[10px]'>
                → {gc.channel_name || `#${gc.channel_id}`}
              </div>
            </div>
            <div className='shrink-0 text-right tabular-nums font-mono'>
              {sortMode === 'cost' ? (
                <>{formatQuota(gc.cost)}</>
              ) : (
                <>{fmtRpm(gc.rpm)} RPM</>
              )}
            </div>
          </div>
        ))
      )}
    </PanelShell>
  )
}

// ──────────────────────────────────────────────────────────────────────────
// 主组件
// ──────────────────────────────────────────────────────────────────────────

export function RuntimePanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [sortMode, setSortMode] = useState<RuntimeSortMode>('rpm')
  const [autoRefresh, setAutoRefresh] = useState(false)
  const autoTimer = useRef<ReturnType<typeof setInterval> | null>(null)

  const { data, isLoading, isFetching, refetch, error } = useQuery({
    queryKey: QUERY_KEY,
    queryFn: async () => {
      const res = await getRuntimeSnapshot()
      if (!res.success || !res.data) {
        throw new Error(res.message || 'Failed to load runtime metrics')
      }
      return res.data as RuntimeSnapshot
    },
    staleTime: 60_000,
    // 429 退避：60s → 120s → 300s，最多 3 次；非 429 不自动重试
    retry: (failureCount, err) =>
      err instanceof RuntimeRateLimitedError && failureCount < 3,
    retryDelay: (failureCount) =>
      [60_000, 120_000, 300_000][Math.min(failureCount, 2)],
  })

  const isRateLimited = error instanceof RuntimeRateLimitedError

  const handleRefresh = useCallback(async () => {
    // 先触发后端重算，再 invalidate；后端是异步执行的，前端 invalidate 后短暂可能拿到旧数据
    try {
      await triggerRecomputeRuntime()
    } catch {
      // 静默：重算失败不阻塞 UI
    }
    queryClient.invalidateQueries({ queryKey: QUERY_KEY })
    void refetch()
  }, [queryClient, refetch])

  useEffect(() => {
    if (autoRefresh) {
      autoTimer.current = setInterval(() => {
        // 被限速时跳过本轮 tick，让 react-query 的 retryDelay 接管
        if (isRateLimited) return
        queryClient.invalidateQueries({ queryKey: QUERY_KEY })
      }, 60_000)
    }
    return () => {
      if (autoTimer.current) {
        clearInterval(autoTimer.current)
        autoTimer.current = null
      }
    }
  }, [autoRefresh, queryClient, isRateLimited])

  // 选择当前 sort 模式对应的数据
  const users = data
    ? sortMode === 'cost'
      ? data.top_users_by_cost
      : sortMode === 'balance'
        ? data.top_users_by_balance
        : data.top_users_by_rpm
    : []
  // 分组：余额维度不适用，强制走 RPM
  const groups = data
    ? sortMode === 'cost'
      ? data.top_groups_by_cost
      : data.top_groups_by_rpm
    : []
  const channels = data
    ? sortMode === 'cost'
      ? data.top_channels_by_cost
      : sortMode === 'balance'
        ? data.top_channels_by_balance
        : data.top_channels_by_rpm
    : []
  // 分组×渠道：余额不适用，强制 RPM
  const groupChannels = data
    ? sortMode === 'cost'
      ? data.top_group_channels_by_cost
      : data.top_group_channels_by_rpm
    : []

  return (
    <div className='space-y-3 sm:space-y-4'>
      {/* 429 限速横幅 */}
      {isRateLimited && (
        <div className='rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200'>
          {t('Rate limited by server. Auto-refresh paused; will retry with backoff (60s → 120s → 300s).')}
        </div>
      )}

      {/* 工具栏 */}
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex items-center gap-2'>
          {/* segmented control: 3 个 Button 拼成排序切换 */}
          <div className='inline-flex rounded-md border bg-muted/30 p-0.5'>
            {(['rpm', 'cost', 'balance'] as RuntimeSortMode[]).map((m) => (
              <Button
                key={m}
                variant={sortMode === m ? 'default' : 'ghost'}
                size='sm'
                className='h-7 px-3 text-xs'
                onClick={() => setSortMode(m)}
                aria-pressed={sortMode === m}
              >
                {m === 'rpm' && <TrendingUp className='h-3.5 w-3.5 mr-1' />}
                {m === 'rpm' ? 'RPM' : m === 'cost' ? t('Cost') : t('Balance')}
              </Button>
            ))}
          </div>
          {data && (
            <span className='text-muted-foreground text-[11px]'>
              {t('1min realtime window')} ·{' '}
              {t('Updated')} {fmtRelative(data.generated_at, t)}
            </span>
          )}
        </div>
        <div className='flex items-center gap-2'>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant='outline'
                size='sm'
                onClick={handleRefresh}
                disabled={isFetching}
                aria-label={t('Refresh')}
              >
                <RefreshCw className={cn('h-4 w-4', isFetching && 'animate-spin')} />
                <span className='hidden sm:inline ml-1.5'>{t('Refresh')}</span>
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t('Manually recompute and reload runtime metrics')}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant={autoRefresh ? 'default' : 'outline'}
                size='sm'
                onClick={() => setAutoRefresh((v) => !v)}
                aria-pressed={autoRefresh}
              >
                {autoRefresh ? <Pause className='h-4 w-4' /> : <Play className='h-4 w-4' />}
                <span className='hidden sm:inline ml-1.5'>
                  {autoRefresh ? t('Auto: 60s') : t('Auto refresh')}
                </span>
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              {autoRefresh
                ? t('Auto refresh enabled (60s). Click to pause.')
                : t('Click to enable auto refresh every 60s.')}
            </TooltipContent>
          </Tooltip>
        </div>
      </div>

      {/* 4 卡片 2×2 */}
      {isLoading ? (
        <div className='grid grid-cols-1 gap-3 sm:gap-4 lg:grid-cols-2'>
          {Array.from({ length: 4 }).map((_, i) => (
            <Card key={i}>
              <CardHeader className='py-3'>
                <Skeleton className='h-4 w-32' />
              </CardHeader>
              <CardContent className='space-y-2'>
                {Array.from({ length: 5 }).map((_, j) => (
                  <Skeleton key={j} className='h-6 w-full' />
                ))}
              </CardContent>
            </Card>
          ))}
        </div>
      ) : (
        <div className='grid grid-cols-1 gap-3 sm:gap-4 lg:grid-cols-2'>
          <UsersPanel items={users} sortMode={sortMode} t={t} />
          <GroupsPanel items={groups} sortMode={sortMode} t={t} />
          <ChannelsPanel items={channels} sortMode={sortMode} t={t} />
          <GroupChannelsPanel items={groupChannels} sortMode={sortMode} t={t} />
        </div>
      )}
    </div>
  )
}

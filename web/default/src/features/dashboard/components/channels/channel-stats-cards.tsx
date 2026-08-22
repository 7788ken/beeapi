// Top-N 渠道卡片网格 — 按模型类型分组（claude / codex / gemini / ...）。
// 每个 type 一个 section header + 一个卡片栅格。
// 交互：点击整行 = 展开/收起 Top 用户面板（全局单开）；行尾图标按钮 = 切换趋势曲线。

import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'
import { Activity, ChartLine, Coins, Hash, TrendingUp } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import { formatQuota } from '@/lib/format'
import dayjs from '@/lib/dayjs'
import { ChannelTopUsers } from './channel-top-users'
import { fmtCount, fmtRpm, RankBadge } from './format'
import {
  MODEL_TYPE_LABEL,
  type ChannelStatsGroup,
  type ChannelStatsItem,
  type ChannelStatsSortBy,
} from './types'

interface ChannelStatsCardsProps {
  groups: ChannelStatsGroup[]
  sortBy: ChannelStatsSortBy
  loading?: boolean
  onToggleTrend?: (item: ChannelStatsItem) => void
  selectedIds?: Set<number>
  expandedId?: number | null
  onToggleExpand?: (item: ChannelStatsItem) => void
}

function fmtPeakWhen(ts: number, t: (k: string) => string): string {
  if (!ts) return t('No data')
  return dayjs.unix(ts).fromNow()
}

interface ChannelCardProps {
  item: ChannelStatsItem
  rank: number
  sortBy: ChannelStatsSortBy
  selected: boolean
  expanded: boolean
  onToggleExpand?: () => void
  onToggleTrend?: () => void
  t: (key: string) => string
}

// 排行榜单行：grid 7 列（rank | 渠道名 | 4 个指标 | 趋势按钮），指标列等宽，
// 不让中间出现空白。指标按 sortBy 高亮主指标。
// 外层不能用 <button>（行尾趋势按钮会形成非法的 button 嵌套），用 div+role 代替。
function ChannelCard({
  item,
  rank,
  sortBy,
  selected,
  expanded,
  onToggleExpand,
  onToggleTrend,
  t,
}: ChannelCardProps) {
  const metrics: { key: ChannelStatsSortBy; label: string; value: string; icon: typeof Hash }[] = [
    { key: 'quota', label: t('Cost'), value: formatQuota(item.quota), icon: Coins },
    { key: 'call_count', label: t('Calls'), value: fmtCount(item.call_count), icon: Hash },
    { key: 'current_rpm', label: t('Current RPM'), value: fmtRpm(item.current_rpm), icon: Activity },
    { key: 'peak_rpm', label: t('Peak RPM'), value: fmtRpm(item.peak_rpm), icon: TrendingUp },
  ]

  return (
    <div
      role='button'
      tabIndex={0}
      aria-expanded={expanded}
      onClick={onToggleExpand}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onToggleExpand?.()
        }
      }}
      className={cn(
        'group bg-card cursor-pointer text-left transition-all hover:bg-accent/40 focus-visible:ring-ring grid w-full items-center gap-3 rounded-md border px-3 py-2 focus-visible:ring-2 focus-visible:outline-none',
        // rank | 渠道名 | 4 列指标 | 趋势按钮。名字给到 2fr，指标各 1fr。
        'grid-cols-[auto_minmax(160px,2fr)_repeat(4,minmax(0,1fr))_auto]',
        // ring = 趋势选中；展开态用背景 + 边框区分，两者可叠加。
        selected && 'ring-primary ring-2',
        expanded && 'border-primary/40 bg-accent/50'
      )}
    >
      <RankBadge rank={rank} />
      <div className='flex min-w-0 items-center gap-1.5'>
        <span
          className='truncate text-sm font-semibold'
          title={item.channel_name}
        >
          {item.channel_name || `#${item.channel_id}`}
        </span>
        <span className='text-muted-foreground shrink-0 text-[10px]'>
          #{item.channel_id}
        </span>
        {item.status !== 1 && (
          <Badge variant='outline' className='shrink-0 px-1 py-0 text-[10px]'>
            {t('Disabled')}
          </Badge>
        )}
      </div>
      {metrics.map((m) => {
        const Icon = m.icon
        const isPrimary = m.key === sortBy
        const cell = (
          <div
            className={cn(
              'flex flex-col items-end leading-tight tabular-nums',
              isPrimary
                ? 'text-foreground font-semibold'
                : 'text-muted-foreground'
            )}
          >
            <span className='font-mono text-sm'>{m.value}</span>
            <span className='inline-flex items-center gap-0.5 text-[10px]'>
              <Icon className='size-2.5' />
              {m.label}
            </span>
          </div>
        )
        if (m.key === 'peak_rpm') {
          return (
            <Tooltip key={m.key}>
              <TooltipTrigger asChild>{cell}</TooltipTrigger>
              <TooltipContent side='top'>
                {t('Peak RPM reached')}: {fmtPeakWhen(item.peak_rpm_at, t)}
              </TooltipContent>
            </Tooltip>
          )
        }
        return <div key={m.key}>{cell}</div>
      })}
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type='button'
            aria-pressed={selected}
            onClick={(e) => {
              e.stopPropagation()
              onToggleTrend?.()
            }}
            className={cn(
              'shrink-0 rounded-md border p-1.5 transition-colors focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none',
              selected
                ? 'border-primary/50 text-primary bg-primary/10'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent border-transparent'
            )}
          >
            <ChartLine className='size-3.5' />
          </button>
        </TooltipTrigger>
        <TooltipContent side='top'>{t('Toggle trend line')}</TooltipContent>
      </Tooltip>
    </div>
  )
}

export function ChannelStatsCards({
  groups,
  sortBy,
  loading,
  onToggleTrend,
  selectedIds,
  expandedId,
  onToggleExpand,
}: ChannelStatsCardsProps) {
  const { t } = useTranslation()

  if (loading) {
    return (
      <div className='text-muted-foreground py-8 text-center text-sm'>
        {t('Loading...')}
      </div>
    )
  }
  if (!groups.length) {
    return (
      <div className='text-muted-foreground py-8 text-center text-sm'>
        {t('No data')}
      </div>
    )
  }

  return (
    <div className='space-y-4'>
      {groups.map((g) => {
        const label = MODEL_TYPE_LABEL[g.model_type] ?? g.model_type
        return (
          <Card key={g.model_type}>
            <CardHeader className='py-3'>
              <div className='flex flex-wrap items-baseline justify-between gap-2'>
                <CardTitle className='flex items-baseline gap-2 text-sm font-semibold'>
                  <span>{label}</span>
                  <span className='text-muted-foreground text-xs font-normal'>
                    Top {g.channels.length}
                  </span>
                </CardTitle>
                <div className='text-muted-foreground flex gap-3 text-xs tabular-nums'>
                  <span>
                    {t('Total cost')}:{' '}
                    <span className='font-mono'>{formatQuota(g.total_quota)}</span>
                  </span>
                  <span>
                    {t('Total calls')}:{' '}
                    <span className='font-mono'>{fmtCount(g.total_calls)}</span>
                  </span>
                </div>
              </div>
            </CardHeader>
            <CardContent className='pb-3'>
              <div className='flex flex-col gap-1.5'>
                {g.channels.map((c, idx) => (
                  <Fragment key={c.channel_id}>
                    <ChannelCard
                      item={c}
                      rank={idx + 1}
                      sortBy={sortBy}
                      selected={selectedIds?.has(c.channel_id) ?? false}
                      expanded={expandedId === c.channel_id}
                      onToggleExpand={
                        onToggleExpand ? () => onToggleExpand(c) : undefined
                      }
                      onToggleTrend={
                        onToggleTrend ? () => onToggleTrend(c) : undefined
                      }
                      t={t}
                    />
                    {expandedId === c.channel_id && (
                      <ChannelTopUsers
                        channelId={c.channel_id}
                        channelName={c.channel_name}
                      />
                    )}
                  </Fragment>
                ))}
              </div>
            </CardContent>
          </Card>
        )
      })}
    </div>
  )
}

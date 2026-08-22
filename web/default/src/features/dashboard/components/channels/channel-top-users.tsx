// 渠道卡片展开面板 — 单渠道窗口内 Top 用户消费/RPM 排行（admin-only）。
// 自带迷你筛选条（默认"今天"），不跟随页面顶部 FilterBar（页面默认 24h）。

import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'
import { formatQuota } from '@/lib/format'
import dayjs from '@/lib/dayjs'
import { fetchChannelTopUsers } from './api'
import { fmtCount, fmtRpm, RankBadge } from './format'
import {
  RANGE_OPTIONS,
  RANGE_TODAY_SENTINEL,
  resolveRangeSeconds,
  type ChannelTopUsersResp,
  type TopUsersSortBy,
} from './types'

const TOP_USERS_LIMIT = 50

interface ChannelTopUsersProps {
  channelId: number
  channelName: string
}

export function ChannelTopUsers({ channelId, channelName }: ChannelTopUsersProps) {
  const { t } = useTranslation()
  const [rangeSeconds, setRangeSeconds] = useState<number>(RANGE_TODAY_SENTINEL)
  const [sortBy, setSortBy] = useState<TopUsersSortBy>('quota')

  // queryKey 存"今天"哨兵原值，effective 秒数延后到 queryFn 计算，防 key 每秒漂移。
  const query = useQuery<ChannelTopUsersResp | undefined>({
    queryKey: ['dashboard', 'channel-top-users', channelId, rangeSeconds, sortBy],
    queryFn: () =>
      fetchChannelTopUsers({
        channelId,
        rangeSeconds: resolveRangeSeconds(rangeSeconds),
        sortBy,
        limit: TOP_USERS_LIMIT,
      }).then((r) => r.data),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  })

  const users = query.data?.users ?? []
  // 占比分母 = Top50 合计（负数 quota 的免单/回滚行不计入，防分母被拉小）。
  const totalQuota = users.reduce((acc, u) => acc + Math.max(u.quota, 0), 0)

  return (
    <div className='border-l-primary/40 bg-muted/40 rounded-md border border-l-4 px-3 py-2'>
      <div className='flex flex-wrap items-center justify-between gap-2 pb-1.5'>
        <div className='flex min-w-0 items-baseline gap-1.5 text-xs'>
          <span className='font-semibold'>{t('Top Users')}</span>
          <span className='text-muted-foreground truncate'>
            {channelName || `#${channelId}`}
          </span>
          <span className='text-muted-foreground shrink-0 text-[10px]'>
            #{channelId}
          </span>
        </div>
        <div className='flex items-center gap-2'>
          <Select
            value={String(rangeSeconds)}
            onValueChange={(v) => setRangeSeconds(Number(v))}
          >
            <SelectTrigger size='sm' className='h-7 w-[130px] text-xs'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {RANGE_OPTIONS.map((opt) => (
                <SelectItem key={opt.value} value={String(opt.value)}>
                  {t(opt.labelKey)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Tabs
            value={sortBy}
            onValueChange={(v) => setSortBy(v as TopUsersSortBy)}
          >
            <TabsList className='h-7 p-[2px]'>
              <TabsTrigger value='quota' className='h-6 px-2 text-xs'>
                {t('By consumption')}
              </TabsTrigger>
              <TabsTrigger value='rpm' className='h-6 px-2 text-xs'>
                {t('By RPM')}
              </TabsTrigger>
            </TabsList>
          </Tabs>
        </div>
      </div>

      {query.isPending ? (
        <div className='space-y-1 py-1'>
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className='h-5 w-full' />
          ))}
        </div>
      ) : query.isError ? (
        <div className='text-destructive py-3 text-center text-xs'>
          {t('Failed to load')}
        </div>
      ) : users.length === 0 ? (
        <div className='text-muted-foreground py-3 text-center text-xs'>
          {t('No consumption in this period')}
        </div>
      ) : (
        <table className='w-full text-xs'>
          <thead className='text-muted-foreground'>
            <tr className='text-left'>
              <th className='w-10 py-1 pr-2 pl-1 text-center font-normal'>#</th>
              <th className='py-1 pr-2 font-normal'>{t('User')}</th>
              <th className='hidden w-28 py-1 pr-2 font-normal sm:table-cell'>
                {t('Share')}
              </th>
              <th className='py-1 pr-2 text-right font-normal'>{t('Cost')}</th>
              <th className='py-1 pr-2 text-right font-normal'>{t('Calls')}</th>
              <th className='py-1 pr-2 text-right font-normal'>RPM</th>
              <th className='hidden py-1 pr-2 text-right font-normal md:table-cell'>
                {t('Last request')}
              </th>
            </tr>
          </thead>
          <tbody>
            {users.map((u, idx) => {
              const share = totalQuota > 0 ? Math.max(u.quota, 0) / totalQuota : 0
              return (
                <tr key={u.user_id} className='border-border/40 border-t'>
                  <td className='py-1 pr-2 pl-1 text-center'>
                    <RankBadge rank={idx + 1} />
                  </td>
                  <td className='max-w-0 py-1 pr-2'>
                    <div className='flex items-baseline gap-1'>
                      <span className='truncate font-medium' title={u.username}>
                        {u.username || `#${u.user_id}`}
                      </span>
                      <span className='text-muted-foreground shrink-0 text-[10px]'>
                        #{u.user_id}
                      </span>
                    </div>
                  </td>
                  <td className='hidden py-1 pr-2 sm:table-cell'>
                    <div className='flex items-center gap-1.5'>
                      <div className='bg-background/60 h-1.5 w-full overflow-hidden rounded-full'>
                        <div
                          className='bg-primary/70 h-full rounded-full transition-all'
                          style={{
                            width: `${Math.max(share * 100, 2).toFixed(1)}%`,
                          }}
                        />
                      </div>
                      <span className='text-muted-foreground w-9 shrink-0 text-right text-[10px] tabular-nums'>
                        {(share * 100).toFixed(1)}%
                      </span>
                    </div>
                  </td>
                  <td
                    className={cn(
                      'py-1 pr-2 text-right font-mono tabular-nums',
                      sortBy === 'quota' && 'font-semibold'
                    )}
                  >
                    {formatQuota(u.quota)}
                  </td>
                  <td className='py-1 pr-2 text-right tabular-nums'>
                    {fmtCount(u.call_count)}
                  </td>
                  <td
                    className={cn(
                      'py-1 pr-2 text-right font-mono tabular-nums',
                      sortBy === 'rpm' && 'font-semibold'
                    )}
                  >
                    {fmtRpm(u.rpm)}
                  </td>
                  <td className='text-muted-foreground hidden py-1 pr-2 text-right md:table-cell'>
                    {u.last_seen ? dayjs.unix(u.last_seen).fromNow() : '-'}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </div>
  )
}

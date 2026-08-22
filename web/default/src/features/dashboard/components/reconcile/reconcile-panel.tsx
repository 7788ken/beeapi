// 对账面板 — 按天窗口（今天/昨天/指定日期）的渠道 × 模型精确聚合：
// 请求/成功/失败/超时/费用，用于核对上游账单。
// 渠道行点击展开模型明细（全局单开），明细内可按全部/单模型过滤。

import { useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { ChevronDown, ChevronRight, RefreshCw } from 'lucide-react'
import dayjs from '@/lib/dayjs'
import { cn } from '@/lib/utils'
import { formatQuota } from '@/lib/format'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { fmtCount } from '../channels/format'
import { fetchChannelReconcile, fetchChannelReconcileUpstreamBill } from './api'
import {
  dayRangeOf,
  formatUsd,
  localDateStr,
  type ChannelReconcileItem,
  type ChannelReconcileResp,
  type UpstreamBillResp,
  type UpstreamChannelBill,
} from './types'

const QUERY_KEY = ['dashboard', 'channel-reconcile'] as const

// Radix SelectItem 不允许空字符串 value，无模型名的行用哨兵代替。
const ALL_MODELS = '__all__'
const EMPTY_MODEL = '__empty__'

const modelValue = (name: string) => (name === '' ? EMPTY_MODEL : name)

function errClass(n: number) {
  return n > 0 ? 'text-destructive' : 'text-muted-foreground'
}

function timeoutClass(n: number) {
  return n > 0 ? 'text-amber-600 dark:text-amber-500' : 'text-muted-foreground'
}

export function ReconcilePanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const today = localDateStr()
  const [date, setDate] = useState<string>(today)
  // 当前展开模型明细的渠道（单选，再点收起）。
  const [expandedId, setExpandedId] = useState<number | null>(null)

  // queryKey 只放日期；start/end 延后到 queryFn 计算，避免"今天"窗口每秒漂移刷新。
  const query = useQuery<ChannelReconcileResp | undefined>({
    queryKey: [...QUERY_KEY, date],
    queryFn: () => fetchChannelReconcile(dayRangeOf(date)).then((r) => r.data),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  })

  // 上游账单（balance 面板）：按令牌绑定到渠道行。只覆盖今/昨两窗；
  // 历史日期整体隐藏。未配置时 configured=false 隐藏。失败不重试，直接暴露错误。
  const billDayReq: 'today' | 'yesterday' | null =
    date === today ? 'today' : date === localDateStr(-1) ? 'yesterday' : null
  const billQuery = useQuery<UpstreamBillResp>({
    queryKey: [...QUERY_KEY, 'upstream-bill', billDayReq],
    queryFn: () => fetchChannelReconcileUpstreamBill(billDayReq ?? 'today'),
    enabled: billDayReq != null,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    retry: false,
  })
  const bill = billQuery.data
  // 请求出错说明已配置（未配置是正常 200 + configured=false）。
  const billOn =
    billDayReq != null && (bill?.configured === true || billQuery.isError)
  const billDayTotal =
    bill && billDayReq
      ? billDayReq === 'today'
        ? bill.total_today
        : bill.total_yesterday
      : null
  // channel_id → 上游令牌账单（JSON 对象 key 为字符串）。
  const billByChannel = useMemo(() => {
    const map = new Map<number, UpstreamChannelBill>()
    for (const [id, b] of Object.entries(bill?.channel_bills ?? {})) {
      map.set(Number(id), b)
    }
    return map
  }, [bill?.channel_bills])
  const billFailedCount =
    (bill?.accounts ?? []).filter((a) => !a.success).length +
    (bill?.detail_failed?.length ?? 0)

  const channels = useMemo(
    () => query.data?.channels ?? [],
    [query.data?.channels]
  )
  const total = query.data?.total

  const totalRequests = total ? total.success_count + total.error_count : 0
  const successRate =
    total && totalRequests > 0
      ? ((total.success_count / totalRequests) * 100).toFixed(1)
      : null

  const handleRefresh = () => {
    void queryClient.invalidateQueries({ queryKey: QUERY_KEY })
  }

  const windowText = query.data
    ? `${dayjs.unix(query.data.start_ts).format('MM-DD HH:mm')} — ${dayjs
        .unix(query.data.end_ts)
        .format('MM-DD HH:mm')}`
    : ''

  const summary: {
    key: string
    label: string
    value: string
    sub?: string
    className?: string
  }[] = [
    {
      key: 'cost',
      label: t('Cost'),
      value: total ? formatQuota(total.quota) : '-',
    },
    ...(billOn
      ? [
          {
            key: 'upstream-bill',
            label: t('Upstream bill'),
            value: billQuery.isError
              ? t('Query failed')
              : billQuery.isPending
                ? '…'
                : billDayTotal != null
                  ? formatUsd(billDayTotal)
                  : '—',
            sub:
              !billQuery.isError && billFailedCount > 0
                ? `${billFailedCount} × ${t('Query failed')}`
                : undefined,
            className: billQuery.isError ? 'text-destructive' : undefined,
          },
        ]
      : []),
    {
      key: 'requests',
      label: t('Requests'),
      value: total ? fmtCount(totalRequests) : '-',
    },
    {
      key: 'success',
      label: t('Success'),
      value: total ? fmtCount(total.success_count) : '-',
      sub: successRate != null ? `${t('Success rate')} ${successRate}%` : undefined,
    },
    {
      key: 'failed',
      label: t('Failed'),
      value: total ? fmtCount(total.error_count) : '-',
      className: total ? errClass(total.error_count) : undefined,
    },
    {
      key: 'timeout',
      label: t('Timeout'),
      value: total ? fmtCount(total.timeout_count) : '-',
      className: total ? timeoutClass(total.timeout_count) : undefined,
    },
  ]

  return (
    <div className='space-y-3'>
      {/* 筛选条：今天 / 昨天 / 指定日期 + 刷新 */}
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex flex-wrap items-center gap-1.5'>
          <Button
            variant={date === today ? 'default' : 'outline'}
            size='sm'
            className='h-8 text-xs'
            onClick={() => setDate(today)}
          >
            {t('Today')}
          </Button>
          <Button
            variant={date === localDateStr(-1) ? 'default' : 'outline'}
            size='sm'
            className='h-8 text-xs'
            onClick={() => setDate(localDateStr(-1))}
          >
            {t('Yesterday')}
          </Button>
          <Input
            type='date'
            value={date}
            max={today}
            onChange={(e) => {
              if (e.target.value) setDate(e.target.value)
            }}
            className='h-8 w-[150px] text-xs'
            aria-label={t('Date')}
          />
          <Button
            variant='outline'
            size='sm'
            className='h-8 text-xs'
            onClick={handleRefresh}
            disabled={query.isFetching}
          >
            <RefreshCw
              className={cn('mr-1 size-3.5', query.isFetching && 'animate-spin')}
            />
            {t('Refresh')}
          </Button>
        </div>
        {windowText && (
          <span className='text-muted-foreground text-xs tabular-nums'>
            {windowText}
          </span>
        )}
      </div>

      {/* 汇总条 */}
      <div className='overflow-hidden rounded-lg border'>
        <div
          className={cn(
            'divide-border/60 grid grid-cols-2 sm:divide-x',
            summary.length > 5
              ? 'sm:grid-cols-3 lg:grid-cols-6'
              : 'sm:grid-cols-5'
          )}
        >
          {summary.map((s) => (
            <div key={s.key} className='px-4 py-3'>
              <div className='text-muted-foreground text-xs'>{s.label}</div>
              <div
                className={cn(
                  'mt-1 font-mono text-lg font-semibold tabular-nums',
                  s.className
                )}
              >
                {s.value}
              </div>
              {s.sub && (
                <div className='text-muted-foreground mt-0.5 text-[10px]'>
                  {s.sub}
                </div>
              )}
            </div>
          ))}
        </div>
      </div>

      {/* 渠道明细表 */}
      {query.isPending ? (
        <div className='space-y-1.5'>
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className='h-8 w-full' />
          ))}
        </div>
      ) : query.isError ? (
        <div className='text-destructive rounded-lg border py-8 text-center text-xs'>
          {t('Failed to load')}
        </div>
      ) : channels.length === 0 ? (
        <div className='text-muted-foreground rounded-lg border py-8 text-center text-xs'>
          {t('No data')}
        </div>
      ) : (
        <div className='overflow-x-auto rounded-lg border'>
          <table className='w-full min-w-[720px] text-xs'>
            <thead className='text-muted-foreground bg-muted/40'>
              <tr className='text-left'>
                <th className='py-2 pr-2 pl-3 font-normal'>{t('Channel')}</th>
                <th className='w-24 py-2 pr-2 text-right font-normal'>
                  {t('Requests')}
                </th>
                <th className='w-24 py-2 pr-2 text-right font-normal'>
                  {t('Success')}
                </th>
                <th className='w-24 py-2 pr-2 text-right font-normal'>
                  {t('Failed')}
                </th>
                <th className='w-24 py-2 pr-2 text-right font-normal'>
                  {t('Timeout')}
                </th>
                <th
                  className={cn(
                    'w-28 py-2 text-right font-normal',
                    billOn ? 'pr-2' : 'pr-3'
                  )}
                >
                  {t('Cost')}
                </th>
                {billOn && (
                  <th className='w-28 py-2 pr-3 text-right font-normal'>
                    {t('Upstream bill')}
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {channels.map((item) => {
                const expanded = expandedId === item.channel_id
                return (
                  <ChannelRow
                    key={item.channel_id}
                    item={item}
                    billOn={billOn}
                    bill={billByChannel.get(item.channel_id)}
                    billLoading={billQuery.isPending}
                    expanded={expanded}
                    onToggle={() =>
                      setExpandedId(expanded ? null : item.channel_id)
                    }
                  />
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function ChannelRow({
  item,
  billOn,
  bill,
  billLoading,
  expanded,
  onToggle,
}: {
  item: ChannelReconcileItem
  billOn: boolean
  bill?: UpstreamChannelBill
  billLoading: boolean
  expanded: boolean
  onToggle: () => void
}) {
  const { t } = useTranslation()
  const requests = item.success_count + item.error_count
  const billTitle = bill
    ? [
        `${bill.keyname} · ${bill.account}`,
        bill.via === 'key' ? t('Matched by key') : t('Matched by name'),
        bill.shared > 1
          ? t('{{n}} channels share this token', { n: bill.shared })
          : undefined,
      ]
        .filter(Boolean)
        .join(' · ')
    : t('No matching upstream token name')
  return (
    <>
      <tr
        className='border-border/40 hover:bg-muted/30 cursor-pointer border-t'
        onClick={onToggle}
      >
        <td className='max-w-0 py-1.5 pr-2 pl-3'>
          <div className='flex items-center gap-1.5'>
            {expanded ? (
              <ChevronDown className='text-muted-foreground size-3.5 shrink-0' />
            ) : (
              <ChevronRight className='text-muted-foreground size-3.5 shrink-0' />
            )}
            <span className='truncate font-medium' title={item.channel_name}>
              {item.channel_name || `#${item.channel_id}`}
            </span>
            <span className='text-muted-foreground shrink-0 text-[10px]'>
              #{item.channel_id}
            </span>
            {item.status !== 1 && (
              <span className='text-muted-foreground bg-muted shrink-0 rounded px-1 py-px text-[10px]'>
                {t('Disabled')}
              </span>
            )}
          </div>
        </td>
        <td className='py-1.5 pr-2 text-right tabular-nums'>
          {fmtCount(requests)}
        </td>
        <td className='py-1.5 pr-2 text-right tabular-nums'>
          {fmtCount(item.success_count)}
        </td>
        <td
          className={cn(
            'py-1.5 pr-2 text-right tabular-nums',
            errClass(item.error_count)
          )}
        >
          {fmtCount(item.error_count)}
        </td>
        <td
          className={cn(
            'py-1.5 pr-2 text-right tabular-nums',
            timeoutClass(item.timeout_count)
          )}
        >
          {fmtCount(item.timeout_count)}
        </td>
        <td
          className={cn(
            'py-1.5 text-right font-mono font-semibold tabular-nums',
            billOn ? 'pr-2' : 'pr-3'
          )}
        >
          {formatQuota(item.quota)}
        </td>
        {billOn && (
          <td className='py-1.5 pr-3 text-right font-mono tabular-nums'>
            {bill ? (
              <span title={billTitle}>
                {formatUsd(bill.amount)}
                {bill.shared > 1 && (
                  <sup className='text-muted-foreground ml-0.5 font-sans'>
                    ×{bill.shared}
                  </sup>
                )}
              </span>
            ) : (
              <span className='text-muted-foreground' title={billTitle}>
                {billLoading ? '…' : '—'}
              </span>
            )}
          </td>
        )}
      </tr>
      {expanded && (
        <tr className='border-border/40 border-t'>
          <td colSpan={billOn ? 7 : 6} className='bg-muted/20 px-3 py-2'>
            <ChannelModelDetail item={item} />
          </td>
        </tr>
      )}
    </>
  )
}

function ChannelModelDetail({ item }: { item: ChannelReconcileItem }) {
  const { t } = useTranslation()
  const [selected, setSelected] = useState<string>(ALL_MODELS)

  const models =
    selected === ALL_MODELS
      ? item.models
      : item.models.filter((m) => modelValue(m.model_name) === selected)
  const showTotalRow = selected === ALL_MODELS && item.models.length > 1

  return (
    <div className='border-l-primary/40 bg-background/60 rounded-md border border-l-4 px-3 py-2'>
      <div className='flex flex-wrap items-center justify-between gap-2 pb-1.5'>
        <span className='text-xs font-semibold'>{t('Model detail')}</span>
        <Select value={selected} onValueChange={setSelected}>
          <SelectTrigger size='sm' className='h-7 w-[220px] text-xs'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_MODELS}>{t('All models')}</SelectItem>
            {item.models.map((m) => (
              <SelectItem
                key={modelValue(m.model_name)}
                value={modelValue(m.model_name)}
              >
                {m.model_name || t('No model name')}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <table className='w-full text-xs'>
        <thead className='text-muted-foreground'>
          <tr className='text-left'>
            <th className='py-1 pr-2 font-normal'>{t('Model')}</th>
            <th className='w-24 py-1 pr-2 text-right font-normal'>
              {t('Requests')}
            </th>
            <th className='w-24 py-1 pr-2 text-right font-normal'>
              {t('Success')}
            </th>
            <th className='w-24 py-1 pr-2 text-right font-normal'>
              {t('Failed')}
            </th>
            <th className='w-24 py-1 pr-2 text-right font-normal'>
              {t('Timeout')}
            </th>
            <th className='w-28 py-1 pr-2 text-right font-normal'>
              {t('Cost')}
            </th>
          </tr>
        </thead>
        <tbody>
          {models.map((m) => (
            <tr
              key={modelValue(m.model_name)}
              className='border-border/40 border-t'
            >
              <td className='max-w-0 py-1 pr-2'>
                <span className='truncate font-mono' title={m.model_name}>
                  {m.model_name || t('No model name')}
                </span>
              </td>
              <td className='py-1 pr-2 text-right tabular-nums'>
                {fmtCount(m.success_count + m.error_count)}
              </td>
              <td className='py-1 pr-2 text-right tabular-nums'>
                {fmtCount(m.success_count)}
              </td>
              <td
                className={cn(
                  'py-1 pr-2 text-right tabular-nums',
                  errClass(m.error_count)
                )}
              >
                {fmtCount(m.error_count)}
              </td>
              <td
                className={cn(
                  'py-1 pr-2 text-right tabular-nums',
                  timeoutClass(m.timeout_count)
                )}
              >
                {fmtCount(m.timeout_count)}
              </td>
              <td className='py-1 pr-2 text-right font-mono tabular-nums'>
                {formatQuota(m.quota)}
              </td>
            </tr>
          ))}
          {showTotalRow && (
            <tr className='border-border border-t font-semibold'>
              <td className='py-1 pr-2'>{t('Total')}</td>
              <td className='py-1 pr-2 text-right tabular-nums'>
                {fmtCount(item.success_count + item.error_count)}
              </td>
              <td className='py-1 pr-2 text-right tabular-nums'>
                {fmtCount(item.success_count)}
              </td>
              <td
                className={cn(
                  'py-1 pr-2 text-right tabular-nums',
                  errClass(item.error_count)
                )}
              >
                {fmtCount(item.error_count)}
              </td>
              <td
                className={cn(
                  'py-1 pr-2 text-right tabular-nums',
                  timeoutClass(item.timeout_count)
                )}
              >
                {fmtCount(item.timeout_count)}
              </td>
              <td className='py-1 pr-2 text-right font-mono tabular-nums'>
                {formatQuota(item.quota)}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  )
}

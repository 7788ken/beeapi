import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { VChart } from '@visactor/react-vchart'
import { useTranslation } from 'react-i18next'
import dayjs from '@/lib/dayjs'
import { VCHART_OPTION } from '@/lib/vchart'
import { useTheme } from '@/context/theme-provider'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { getChannelQualityHistory } from '../../api'
import type { QualityHistoryData } from '../../api'
import { useChannels } from '../channels-provider'

// 渠道质量历史报表弹窗（列表"质量"列 hover → 查看更多）。
// 数据源 GET /api/channel/:id/quality/history；范围预设 24h/7d/30d/自定义（≤92 天）。

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

type RangePreset = '24h' | '7d' | '30d' | 'custom'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

function toTs(value: string): number {
  if (!value) return 0
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? 0 : Math.floor(d.getTime() / 1000)
}

function formatMs(ms: number): string {
  if (ms <= 0) return '—'
  if (ms < 1000) return `${ms} ms`
  return `${(ms / 1000).toFixed(1)} s`
}

export function ChannelQualityDialog({ open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const { resolvedTheme } = useTheme()
  const { currentRow } = useChannels()

  const [preset, setPreset] = useState<RangePreset>('24h')
  const [customStart, setCustomStart] = useState('')
  const [customEnd, setCustomEnd] = useState('')
  // anchor：打开弹窗时固定"现在"，避免 now 每次渲染变化打散 queryKey
  const [anchor, setAnchor] = useState(0)
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)

  useEffect(() => {
    if (open) {
      setAnchor(Math.floor(Date.now() / 1000))
      setPreset('24h')
    }
  }, [open])

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

  const range = useMemo(() => {
    if (preset === 'custom') {
      const s = toTs(customStart)
      const e = toTs(customEnd)
      return s > 0 && e > s ? { start: s, end: e } : null
    }
    if (!anchor) return null
    const secs =
      preset === '24h' ? 86400 : preset === '7d' ? 7 * 86400 : 30 * 86400
    return { start: anchor - secs, end: anchor }
  }, [preset, customStart, customEnd, anchor])

  const channelId = currentRow?.id
  const { data, isLoading, isError } = useQuery({
    queryKey: ['channels', 'quality-history', channelId, range?.start, range?.end],
    queryFn: () =>
      getChannelQualityHistory(channelId!, range!.start, range!.end),
    enabled: open && !!channelId && !!range,
    staleTime: 60_000,
  })
  const history: QualityHistoryData | undefined =
    data?.success && data.data ? data.data : undefined

  const buckets = useMemo(() => history?.buckets ?? [], [history])
  const timeFmt = (history?.bucket_seconds ?? 3600) >= 86400 ? 'MM-DD' : 'MM-DD HH:mm'
  const fmtBucket = (ts: number) => dayjs(ts * 1000).format(timeFmt)

  const totals = history?.totals
  const totalReq = (totals?.success_cnt ?? 0) + (totals?.error_cnt ?? 0)
  const errorRate = totalReq > 0 ? ((totals!.error_cnt / totalReq) * 100) : 0

  // ── VChart specs（沿用 dashboard 惯例：plain object spec + theme 注入）──
  const specs = useMemo(() => {
    if (!buckets.length) return null
    const reqValues: Record<string, unknown>[] = []
    const rateValues: Record<string, unknown>[] = []
    const latencyValues: Record<string, unknown>[] = []
    for (const b of buckets) {
      const label = fmtBucket(b.bucket_start)
      reqValues.push({ Time: label, Kind: t('Success'), Count: b.success_cnt })
      reqValues.push({ Time: label, Kind: t('Error'), Count: b.error_cnt })
      const total = b.success_cnt + b.error_cnt
      rateValues.push({
        Time: label,
        Rate: total > 0 ? Number(((b.error_cnt / total) * 100).toFixed(2)) : 0,
      })
      latencyValues.push({
        Time: label,
        Seconds: Number(b.avg_use_time.toFixed(2)),
      })
    }
    const base = { background: { fill: 'transparent' }, animation: false }
    const requests: Record<string, unknown> = {
      ...base,
      type: 'area',
      data: [{ id: 'req', values: reqValues }],
      xField: 'Time',
      yField: 'Count',
      seriesField: 'Kind',
      stack: true,
      color: ['#10b981', '#f43f5e'],
      legends: { visible: true },
      area: { style: { fillOpacity: 0.15, curveType: 'monotone' } },
      line: { style: { lineWidth: 2, curveType: 'monotone' } },
      point: { visible: false },
    }
    const rate: Record<string, unknown> = {
      ...base,
      type: 'line',
      data: [{ id: 'rate', values: rateValues }],
      xField: 'Time',
      yField: 'Rate',
      color: ['#f59e0b'],
      line: { style: { lineWidth: 2, curveType: 'monotone' } },
      point: { visible: false },
      tooltip: {
        mark: {
          content: [
            {
              key: () => t('Error rate'),
              value: (datum?: Record<string, unknown>) => `${datum?.Rate ?? 0}%`,
            },
          ],
        },
      },
    }
    const latency: Record<string, unknown> = {
      ...base,
      type: 'line',
      data: [{ id: 'latency', values: latencyValues }],
      xField: 'Time',
      yField: 'Seconds',
      color: ['#6366f1'],
      line: { style: { lineWidth: 2, curveType: 'monotone' } },
      point: { visible: false },
      tooltip: {
        mark: {
          content: [
            {
              key: () => t('Avg response time'),
              value: (datum?: Record<string, unknown>) =>
                `${datum?.Seconds ?? 0} s`,
            },
          ],
        },
      },
    }
    return { requests, rate, latency }
  }, [buckets, t, timeFmt])

  const errorCodeSpec = useMemo(() => {
    const entries = Object.entries(history?.error_codes ?? {}).sort(
      (a, b) => b[1] - a[1]
    )
    if (!entries.length) return null
    return {
      type: 'bar',
      direction: 'horizontal',
      data: [
        {
          id: 'codes',
          values: entries.map(([code, count]) => ({ Code: code, Count: count })),
        },
      ],
      xField: 'Count',
      yField: 'Code',
      seriesField: 'Code',
      legends: { visible: false },
      label: { visible: true, position: 'outside', style: { fontSize: 11 } },
      axes: [
        { orient: 'left', type: 'band' },
        { orient: 'bottom', type: 'linear', visible: false },
      ],
      background: { fill: 'transparent' },
      animation: false,
    } as Record<string, unknown>
  }, [history])

  const errorModelSpec = useMemo(() => {
    const models = (history?.error_models ?? []).filter((m) => m.error_cnt > 0)
    if (!models.length) return null
    return {
      type: 'bar',
      direction: 'horizontal',
      data: [
        {
          id: 'models',
          values: models.map((m) => ({
            Model: m.model_name || t('Unknown'),
            Count: m.error_cnt,
          })),
        },
      ],
      xField: 'Count',
      yField: 'Model',
      seriesField: 'Model',
      legends: { visible: false },
      label: { visible: true, position: 'outside', style: { fontSize: 11 } },
      axes: [
        { orient: 'left', type: 'band', maxWidth: 220 },
        { orient: 'bottom', type: 'linear', visible: false },
      ],
      background: { fill: 'transparent' },
      animation: false,
    } as Record<string, unknown>
  }, [history, t])

  const chartTheme = {
    theme: resolvedTheme === 'dark' ? 'dark' : ('light' as const),
    background: 'transparent',
  }
  const chartKey = [channelId, range?.start, range?.end, resolvedTheme].join('-')

  const presetButtons: { value: RangePreset; label: string }[] = [
    { value: '24h', label: t('Last 24 hours') },
    { value: '7d', label: t('Last 7 days') },
    { value: '30d', label: t('Last 30 days') },
    { value: 'custom', label: t('Custom') },
  ]

  const summaryCards = [
    { label: t('Total Requests'), value: totalReq.toLocaleString() },
    {
      label: t('Error rate'),
      value: totalReq > 0 ? `${errorRate.toFixed(2)}%` : '—',
      tone:
        totalReq > 0 && errorRate >= 10
          ? 'text-rose-600'
          : totalReq > 0 && errorRate >= 3
            ? 'text-amber-600'
            : 'text-emerald-600',
    },
    { label: t('Avg response time'), value: formatMs(totals?.avg_use_time_ms ?? 0) },
    { label: t('Avg first-token latency'), value: formatMs(totals?.avg_frt_ms ?? 0) },
  ]

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[90vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-5xl'>
        <DialogHeader className='border-b px-6 py-4'>
          <DialogTitle className='flex items-center gap-2'>
            {t('Quality History')}
            <span className='text-muted-foreground text-sm font-normal'>
              #{currentRow?.id} {currentRow?.name}
            </span>
          </DialogTitle>
          <DialogDescription>
            {t('Trends and error distributions computed from request logs of this channel.')}
          </DialogDescription>
        </DialogHeader>

        <div className='flex-1 space-y-4 overflow-y-auto px-6 py-4'>
          {/* 时间范围 */}
          <div className='flex flex-wrap items-center gap-2'>
            <div className='bg-muted/60 inline-flex h-8 overflow-x-auto rounded-md border p-0.5'>
              {presetButtons.map((item) => (
                <button
                  key={item.value}
                  type='button'
                  onClick={() => setPreset(item.value)}
                  className={cn(
                    'inline-flex shrink-0 items-center rounded-[5px] px-3 text-xs font-medium transition-colors',
                    preset === item.value
                      ? 'bg-background text-foreground shadow-sm'
                      : 'text-muted-foreground hover:text-foreground'
                  )}
                >
                  {item.label}
                </button>
              ))}
            </div>
            {preset === 'custom' && (
              <div className='flex flex-wrap items-center gap-2'>
                <Input
                  type='datetime-local'
                  value={customStart}
                  onChange={(e) => setCustomStart(e.target.value)}
                  className='h-8 w-auto text-xs'
                />
                <span className='text-muted-foreground text-xs'>~</span>
                <Input
                  type='datetime-local'
                  value={customEnd}
                  onChange={(e) => setCustomEnd(e.target.value)}
                  className='h-8 w-auto text-xs'
                />
                {!range && (
                  <span className='text-muted-foreground text-xs'>
                    {t('Select a valid start and end time (max 92 days).')}
                  </span>
                )}
              </div>
            )}
          </div>

          {/* 汇总卡片 */}
          <div className='grid grid-cols-2 gap-2 sm:grid-cols-4'>
            {summaryCards.map((card) => (
              <div key={card.label} className='rounded-lg border px-3 py-2'>
                <div className='text-muted-foreground text-xs'>{card.label}</div>
                <div
                  className={cn(
                    'mt-0.5 text-lg font-semibold tabular-nums',
                    'tone' in card ? card.tone : undefined
                  )}
                >
                  {card.value}
                </div>
              </div>
            ))}
          </div>

          {isLoading && (
            <div className='text-muted-foreground py-10 text-center text-sm'>
              {t('Loading...')}
            </div>
          )}
          {isError && (
            <div className='text-destructive py-10 text-center text-sm'>
              {t('Failed to load data')}
            </div>
          )}
          {!isLoading && !isError && !buckets.length && (
            <div className='text-muted-foreground py-10 text-center text-sm'>
              {t('No data in selected range')}
            </div>
          )}

          {themeReady && specs && (
            <>
              {/* 请求量趋势 */}
              <div className='overflow-hidden rounded-lg border'>
                <div className='border-b px-4 py-2 text-sm font-semibold'>
                  {t('Requests Trend')}
                </div>
                <div className='h-56 p-2'>
                  <VChart
                    key={`req-${chartKey}`}
                    spec={{ ...specs.requests, ...chartTheme }}
                    option={VCHART_OPTION}
                  />
                </div>
              </div>

              <div className='grid gap-4 lg:grid-cols-2'>
                <div className='overflow-hidden rounded-lg border'>
                  <div className='border-b px-4 py-2 text-sm font-semibold'>
                    {t('Error Rate Trend')}
                  </div>
                  <div className='h-52 p-2'>
                    <VChart
                      key={`rate-${chartKey}`}
                      spec={{ ...specs.rate, ...chartTheme }}
                      option={VCHART_OPTION}
                    />
                  </div>
                </div>
                <div className='overflow-hidden rounded-lg border'>
                  <div className='border-b px-4 py-2 text-sm font-semibold'>
                    {t('Latency Trend')}
                  </div>
                  <div className='h-52 p-2'>
                    <VChart
                      key={`lat-${chartKey}`}
                      spec={{ ...specs.latency, ...chartTheme }}
                      option={VCHART_OPTION}
                    />
                  </div>
                </div>
              </div>

              <div className='grid gap-4 lg:grid-cols-2'>
                <div className='overflow-hidden rounded-lg border'>
                  <div className='flex items-center gap-2 border-b px-4 py-2 text-sm font-semibold'>
                    {t('Error Code Distribution')}
                    {history?.error_codes_sampled && (
                      <span className='text-muted-foreground text-xs font-normal'>
                        {t('(sampled from latest errors)')}
                      </span>
                    )}
                  </div>
                  <div className='h-52 p-2'>
                    {errorCodeSpec ? (
                      <VChart
                        key={`codes-${chartKey}`}
                        spec={{ ...errorCodeSpec, ...chartTheme }}
                        option={VCHART_OPTION}
                      />
                    ) : (
                      <div className='text-muted-foreground flex h-full items-center justify-center text-sm'>
                        {t('No errors in selected range')}
                      </div>
                    )}
                  </div>
                </div>
                <div className='overflow-hidden rounded-lg border'>
                  <div className='border-b px-4 py-2 text-sm font-semibold'>
                    {t('Error Model Distribution')}
                  </div>
                  <div className='h-52 p-2'>
                    {errorModelSpec ? (
                      <VChart
                        key={`models-${chartKey}`}
                        spec={{ ...errorModelSpec, ...chartTheme }}
                        option={VCHART_OPTION}
                      />
                    ) : (
                      <div className='text-muted-foreground flex h-full items-center justify-center text-sm'>
                        {t('No errors in selected range')}
                      </div>
                    )}
                  </div>
                </div>
              </div>
            </>
          )}
        </div>

        <div className='flex justify-end border-t px-6 py-3'>
          <Button variant='outline' size='sm' onClick={() => onOpenChange(false)}>
            {t('Close')}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

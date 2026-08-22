// 渠道趋势图：多渠道多曲线 (VChart)。点击卡片切换可见渠道。
// 指标可切换：消耗金额 (quota) / 调用次数 (call_count)。

import { useEffect, useMemo, useRef, useState } from 'react'
import { VChart } from '@visactor/react-vchart'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Activity, BarChart3, Coins } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { useTheme } from '@/context/theme-provider'
import { VCHART_OPTION } from '@/lib/vchart'
import { formatQuota } from '@/lib/format'
import dayjs from '@/lib/dayjs'
import { fetchChannelTrend } from './api'
import { resolveRangeSeconds, type TrendMetric } from './types'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

interface ChannelStatsTrendProps {
  channelIds: number[]
  channelNames: Record<number, string>
  rangeSeconds: number
}

function bucketLabel(ts: number, rangeSec: number): string {
  // ≤6h 不会跨日，'HH:mm' 唯一；>6h 起带 MM-DD 防"昨天 14:30"和"今天 14:30"
  // 在 VChart band 轴上被折叠成一个 x 位置。
  if (rangeSec <= 6 * 3600) return dayjs.unix(ts).format('HH:mm')
  if (rangeSec <= 7 * 86400) return dayjs.unix(ts).format('MM-DD HH:mm')
  return dayjs.unix(ts).format('MM-DD')
}

function browserTzOffsetSec(): number {
  // getTimezoneOffset 返回"本地时间与 UTC 的差，单位分钟，西为正"。
  // 东八区返回 -480 → 我们要 +28800 秒。
  return -new Date().getTimezoneOffset() * 60
}

export function ChannelStatsTrend({
  channelIds,
  channelNames,
  rangeSeconds,
}: ChannelStatsTrendProps) {
  const { t } = useTranslation()
  const { resolvedTheme } = useTheme()
  const [metric, setMetric] = useState<TrendMetric>('quota')
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)

  const idsKey = channelIds.join(',')
  const enabled = channelIds.length > 0

  const tzOffsetSec = browserTzOffsetSec()
  // idsKey 是 channelIds 的稳定派生字符串，足以唯一定位查询；不重复列 channelIds 数组避免冗余 hash。
  // rangeSeconds 可能是"今天"哨兵 -1（每秒变值会让 key 漂走），故 queryKey 用原值，
  // 真实秒数在 queryFn 调用 resolveRangeSeconds 时计算。
  // eslint-disable-next-line @tanstack/query/exhaustive-deps
  const query = useQuery({
    queryKey: ['dashboard', 'channel-trend', idsKey, rangeSeconds, tzOffsetSec],
    queryFn: () =>
      fetchChannelTrend({
        channelIds,
        rangeSeconds: resolveRangeSeconds(rangeSeconds),
        tzOffsetSec,
      }).then((r) => r.data),
    enabled,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  })

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

  const spec = useMemo(() => {
    if (!query.data) return null
    const series = query.data.series
    // 拍平为 VChart values：{ time, channel, value }
    const values: Array<{ time: string; channel: string; value: number }> = []
    for (const s of series) {
      const name = channelNames[s.channel_id] || s.channel_name || `#${s.channel_id}`
      for (const p of s.points) {
        values.push({
          time: bucketLabel(p.bucket_start, rangeSeconds),
          channel: name,
          value: metric === 'quota' ? p.quota : p.call_count,
        })
      }
    }
    return {
      type: 'line',
      data: { values },
      xField: 'time',
      yField: 'value',
      seriesField: 'channel',
      point: { visible: values.length < 200 },
      line: { style: { lineWidth: 2 } },
      legends: { visible: true, position: 'top', orient: 'horizontal' },
      tooltip: {
        mark: {
          content: [
            {
              key: (d: Record<string, unknown>) => String(d.channel ?? ''),
              value: (d: Record<string, unknown>) => {
                const v = Number(d.value ?? 0)
                return metric === 'quota'
                  ? formatQuota(v)
                  : v.toLocaleString()
              },
            },
          ],
        },
      },
      axes: [
        { orient: 'left', type: 'linear' },
        { orient: 'bottom', type: 'band' },
      ],
    } as Record<string, unknown>
  }, [query.data, metric, channelNames, rangeSeconds])

  const chartKey = [
    metric,
    idsKey,
    rangeSeconds,
    resolvedTheme,
    query.dataUpdatedAt,
  ].join('-')

  return (
    <Card>
      <CardHeader className='py-3'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <CardTitle className='flex items-center gap-2 text-sm font-semibold'>
            <BarChart3 className='size-4' />
            {t('Channel Trend')}
            <span className='text-muted-foreground text-xs font-normal'>
              {channelIds.length > 0
                ? t('Selected channels: {{n}}', { n: channelIds.length })
                : t('Click a channel card to add to the trend chart')}
            </span>
          </CardTitle>
          <div className='bg-muted/60 inline-flex h-7 overflow-hidden rounded-md border p-0.5'>
            <button
              type='button'
              onClick={() => setMetric('quota')}
              className={`inline-flex items-center gap-1 rounded-sm px-2 text-xs font-medium transition ${
                metric === 'quota'
                  ? 'bg-background text-foreground shadow-sm'
                  : 'text-muted-foreground'
              }`}
            >
              <Coins className='size-3' />
              {t('Consumption')}
            </button>
            <button
              type='button'
              onClick={() => setMetric('call_count')}
              className={`inline-flex items-center gap-1 rounded-sm px-2 text-xs font-medium transition ${
                metric === 'call_count'
                  ? 'bg-background text-foreground shadow-sm'
                  : 'text-muted-foreground'
              }`}
            >
              <Activity className='size-3' />
              {t('Calls')}
            </button>
          </div>
        </div>
      </CardHeader>
      <CardContent className='pb-3'>
        {!enabled ? (
          <div className='text-muted-foreground py-12 text-center text-xs'>
            {t('No channels selected. Click cards above to add them.')}
          </div>
        ) : query.isPending ? (
          <Skeleton className='h-72 w-full' />
        ) : query.isError ? (
          <div className='text-destructive py-12 text-center text-xs'>
            {t('Failed to load trend data')}
          </div>
        ) : (
          <div className='h-72'>
            {themeReady && spec && (
              <VChart
                key={chartKey}
                spec={{
                  ...spec,
                  theme: resolvedTheme === 'dark' ? 'dark' : 'light',
                  background: 'transparent',
                }}
                option={VCHART_OPTION}
              />
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

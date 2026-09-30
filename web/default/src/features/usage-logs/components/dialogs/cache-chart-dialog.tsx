import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type PointerEvent,
} from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { VChart } from '@visactor/react-vchart'
import {
  BarChart3,
  Loader2,
  Plus,
  RefreshCw,
  RotateCcw,
  Trash2,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import dayjs from '@/lib/dayjs'
import { formatTokens } from '@/lib/format'
import { useChartTheme } from '@/lib/use-chart-theme'
import { VCHART_OPTION } from '@/lib/vchart'
import { useCanViewAllLogs } from '@/hooks/use-admin'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import { getLogTrend } from '../../api'
import { buildLogTrendParams } from '../../lib/utils'
import { CompactDateTimeRangePicker } from '../compact-date-time-range-picker'
import type { LogTrendData, LogTrendPoint } from '../../types'

type TrendPoint = {
  bucketStart: number
  inputTokens: number
  outputTokens: number
  cacheWriteTokens: number
  cacheReadTokens: number
  cacheHitTokens: number
  cacheHitDenominator: number
  cacheHitRate: number | null
}

const TREND_QUERY_KEY = 'usage-logs-cache-chart-v2'
const MAX_RENDER_POINTS = 1200
// Mirrors backend maxLogTrendPoints (model/log_trend.go); used to pre-warn.
const MAX_TREND_POINTS = 2000
const REF_LINE_COLORS = ['#f43f5e', '#8b5cf6', '#0ea5e9', '#f59e0b', '#10b981']

type RefLine = {
  id: string
  axis: 'x' | 'y'
  value: string | number
  label: string
  color: string
}

// Granularity presets: `seconds` is bucket_seconds sent to the trend endpoint.
// 'auto' lets the backend pick; 'custom' reveals a whole-minute input.
const GRANULARITY_PRESETS = [
  { value: '60', seconds: 60, labelKey: '1 minute' },
  { value: '300', seconds: 300, labelKey: '5 minutes' },
  { value: '900', seconds: 900, labelKey: '15 minutes' },
  { value: '1800', seconds: 1800, labelKey: '30 minutes' },
  { value: '3600', seconds: 3600, labelKey: '1 hour' },
  { value: '21600', seconds: 21600, labelKey: '6 hours' },
  { value: '86400', seconds: 86400, labelKey: '1 day' },
] as const
const TREND_STALE_TIME_MS = 60 * 1000
const QUERY_CACHE_TIME_MS = 15 * 60 * 1000

const route = getRouteApi('/_authenticated/usage-logs/$section')

function browserTzOffsetSec(): number {
  return -new Date().getTimezoneOffset() * 60
}

function toFiniteNumber(value: unknown, fallback = 0): number {
  const number = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(number) ? number : fallback
}

function nonNegative(value: unknown): number {
  return Math.max(0, toFiniteNumber(value))
}

function normalizeTrendPoint(point: LogTrendPoint): TrendPoint | null {
  const bucketStart = toFiniteNumber(point?.bucket_start, NaN)
  if (!Number.isFinite(bucketStart)) return null

  const inputTokens = nonNegative(point?.input_tokens)
  const outputTokens = nonNegative(point?.output_tokens)
  const cacheWriteTokens = nonNegative(point?.cache_write_tokens)
  const cacheReadTokens = nonNegative(point?.cache_read_tokens)
  const cacheHitTokens = nonNegative(point?.cache_hit_tokens)
  const cacheHitDenominator = nonNegative(point?.cache_hit_denominator)

  const suppliedRate =
    point?.cache_hit_rate == null
      ? null
      : toFiniteNumber(point.cache_hit_rate, NaN)
  const calculatedRate =
    cacheHitDenominator > 0
      ? (cacheHitTokens / cacheHitDenominator) * 100
      : null
  // Prefer the server's weighted rate. Derive it only for older/mixed-version
  // responses that omit the field, and keep a zero-denominator bucket as '--'.
  let cacheHitRate = calculatedRate
  if (suppliedRate !== null && Number.isFinite(suppliedRate)) {
    cacheHitRate =
      cacheHitDenominator > 0 || suppliedRate !== 0
        ? Math.min(100, Math.max(0, suppliedRate))
        : null
  }

  return {
    bucketStart,
    inputTokens,
    outputTokens,
    cacheWriteTokens,
    cacheReadTokens,
    cacheHitTokens,
    cacheHitDenominator,
    cacheHitRate,
  }
}

function normalizeTrendData(data: LogTrendData): LogTrendData {
  const rawPoints = Array.isArray(data?.points) ? data.points : []
  const points = rawPoints
    .map((point) => normalizeTrendPoint(point))
    .filter((point): point is TrendPoint => point !== null)
    .sort((a, b) => a.bucketStart - b.bucketStart)

  return {
    ...data,
    points: points.map((point) => ({
      bucket_start: point.bucketStart,
      input_tokens: point.inputTokens,
      output_tokens: point.outputTokens,
      cache_write_tokens: point.cacheWriteTokens,
      cache_read_tokens: point.cacheReadTokens,
      cache_hit_tokens: point.cacheHitTokens,
      cache_hit_denominator: point.cacheHitDenominator,
      cache_hit_rate: point.cacheHitRate,
    })),
  }
}

/**
 * Reduce the number of marks VChart has to draw while retaining the first and
 * last bucket plus local extrema. The aggregate endpoint already caps the
 * response, but this second bound keeps rendering predictable on low-end
 * clients and during backend configuration changes.
 */
function downsampleTrendPoints(
  points: TrendPoint[],
  maxPoints = MAX_RENDER_POINTS
): TrendPoint[] {
  if (points.length <= maxPoints || maxPoints < 3) return points

  const target = Math.max(3, Math.floor(maxPoints))
  const segmentCount = Math.max(1, Math.floor((target - 2) / 2))
  const segmentWidth = (points.length - 1) / segmentCount
  const selected = new Set<number>([0, points.length - 1])

  const importance = (point: TrendPoint): number => {
    const largestTokenMetric = Math.max(
      point.inputTokens,
      point.outputTokens,
      point.cacheWriteTokens,
      point.cacheReadTokens
    )
    // Log scaling prevents a single very large bucket from hiding cache-rate
    // changes while still prioritising meaningful token-volume spikes.
    return Math.log1p(largestTokenMetric) + (point.cacheHitRate ?? 0) / 100
  }

  for (let segment = 0; segment < segmentCount; segment += 1) {
    const start = Math.max(1, Math.floor(segment * segmentWidth))
    const end = Math.min(
      points.length - 2,
      Math.max(start, Math.floor((segment + 1) * segmentWidth))
    )
    let minIndex = start
    let maxIndex = start
    let minValue = importance(points[start])
    let maxValue = minValue

    for (let index = start + 1; index <= end; index += 1) {
      const value = importance(points[index])
      if (value < minValue) {
        minValue = value
        minIndex = index
      }
      if (value > maxValue) {
        maxValue = value
        maxIndex = index
      }
    }
    selected.add(minIndex)
    selected.add(maxIndex)
  }

  const indexes = [...selected].sort((a, b) => a - b)
  if (indexes.length <= target) return indexes.map((index) => points[index])

  // Overlapping segments can still produce more than the target. Thin the
  // extrema set uniformly, preserving its endpoints and chronological order.
  const stride = (indexes.length - 1) / (target - 1)
  const thinned = new Array<TrendPoint>(target)
  for (let index = 0; index < target; index += 1) {
    thinned[index] = points[indexes[Math.round(index * stride)]]
  }
  return thinned
}

function stableParamsKey(params: Record<string, unknown>): string {
  return JSON.stringify(
    Object.entries(params)
      .filter(([, value]) => value !== undefined)
      .sort(([left], [right]) => left.localeCompare(right))
  )
}

function formatBucketLabel(
  timestampSec: number,
  bucketSeconds: number,
  startSec: number,
  endSec: number
): string {
  const span = Math.max(0, endSec - startSec)
  if (span <= 3 * 24 * 60 * 60 && bucketSeconds < 24 * 60 * 60) {
    return dayjs.unix(timestampSec).format('MM-DD HH:mm')
  }
  if (span > 365 * 24 * 60 * 60) {
    return dayjs.unix(timestampSec).format('YYYY-MM-DD')
  }
  return dayjs.unix(timestampSec).format('MM-DD')
}

function buildSeries(
  points: TrendPoint[],
  data: LogTrendData,
  t: (key: string) => string,
  refLines: RefLine[]
) {
  const bucketSeconds = Math.max(60, toFiniteNumber(data.bucket_seconds, 3600))
  const fallbackStart = points[0]?.bucketStart ?? 0
  const fallbackEnd = points[points.length - 1]?.bucketStart ?? fallbackStart
  const startSec = toFiniteNumber(data.start_ts, fallbackStart)
  const endSec = toFiniteNumber(data.end_ts, fallbackEnd)
  const tokenValues: Record<string, unknown>[] = []
  const rateValues: Record<string, unknown>[] = []

  for (const point of points) {
    const time = formatBucketLabel(
      point.bucketStart,
      bucketSeconds,
      startSec,
      endSec
    )
    tokenValues.push(
      { Time: time, Metric: t('Input Tokens'), Value: point.inputTokens },
      { Time: time, Metric: t('Output Tokens'), Value: point.outputTokens },
      { Time: time, Metric: t('Cache Write'), Value: point.cacheWriteTokens },
      { Time: time, Metric: t('Cache Read'), Value: point.cacheReadTokens }
    )
    rateValues.push({
      Time: time,
      Rate: point.cacheHitRate,
    })
  }

  // Reference lines are session-only overlays. X lines (a time category) apply
  // to both charts since they share the time axis; Y lines (a hit-rate value)
  // only make sense on the rate chart.
  const toMarkLine = (line: RefLine) => ({
    ...(line.axis === 'x' ? { x: line.value } : { y: line.value }),
    line: { style: { stroke: line.color, lineWidth: 1, lineDash: [4, 4] } },
    label: {
      visible: true,
      text: line.label,
      style: { fill: line.color, fontSize: 10 },
      labelBackground: {
        visible: true,
        style: { fill: 'rgba(127,127,127,0.12)', cornerRadius: 3 },
      },
    },
  })
  const xMarkLines = refLines
    .filter((line) => line.axis === 'x')
    .map(toMarkLine)
  const rateMarkLines = refLines.map(toMarkLine)

  return {
    tokenSpec: {
      type: 'line',
      data: [{ id: 'cache-trend-tokens', values: tokenValues }],
      xField: 'Time',
      yField: 'Value',
      seriesField: 'Metric',
      legends: { visible: true, position: 'top' },
      color: ['#0ea5e9', '#f59e0b', '#14b8a6', '#ef4444'],
      ...(xMarkLines.length ? { markLine: xMarkLines } : {}),
      axes: [
        {
          orient: 'bottom',
          label: {
            style: { fill: 'currentColor', fontSize: 10 },
            autoHide: true,
            autoLimit: true,
          },
          tick: { visible: false },
        },
        {
          orient: 'left',
          label: {
            style: { fill: 'currentColor', fontSize: 10 },
            formatMethod: (value: number | string) =>
              formatTokens(Number(value) || 0),
          },
          grid: { visible: true, style: { lineDash: [3, 3] } },
        },
      ],
      line: { style: { lineWidth: 2, curveType: 'monotone' } },
      point: { visible: points.length < 180 },
      tooltip: {
        mark: {
          content: [
            {
              key: (datum: Record<string, unknown>) =>
                String(datum?.Metric ?? ''),
              value: (datum: Record<string, unknown>) =>
                formatTokens(Number(datum?.Value) || 0),
            },
          ],
        },
      },
      background: { fill: 'transparent' },
      animation: false,
    } as Record<string, unknown>,
    rateSpec: {
      type: 'line',
      data: [{ id: 'cache-trend-rate', values: rateValues }],
      xField: 'Time',
      yField: 'Rate',
      legends: { visible: false },
      color: ['#8b5cf6'],
      ...(rateMarkLines.length ? { markLine: rateMarkLines } : {}),
      axes: [
        {
          orient: 'bottom',
          label: {
            style: { fill: 'currentColor', fontSize: 10 },
            autoHide: true,
            autoLimit: true,
          },
          tick: { visible: false },
        },
        {
          orient: 'left',
          min: 0,
          max: 100,
          label: {
            style: { fill: 'currentColor', fontSize: 10 },
            formatMethod: (value: number | string) => `${Number(value) || 0}%`,
          },
          grid: { visible: true, style: { lineDash: [3, 3] } },
        },
      ],
      line: { style: { lineWidth: 2, curveType: 'monotone' } },
      point: { visible: points.length < 180 },
      tooltip: {
        mark: {
          content: [
            {
              key: () => t('Hit Rate'),
              value: (datum: Record<string, unknown>) => {
                const value = Number(datum?.Rate)
                return Number.isFinite(value) ? `${value.toFixed(2)}%` : '--'
              },
            },
          ],
        },
      },
      background: { fill: 'transparent' },
      animation: false,
    } as Record<string, unknown>,
  }
}

function isAbortError(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const candidate = error as { code?: unknown; name?: unknown }
  return candidate.code === 'ERR_CANCELED' || candidate.name === 'AbortError'
}

interface CacheChartDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function CacheChartDialog(props: CacheChartDialogProps) {
  const { t } = useTranslation()
  const { resolvedTheme, themeReady } = useChartTheme()
  const isAdmin = useCanViewAllLogs()
  const authUserId = useAuthStore((state) => state.auth.user?.id ?? 0)
  const searchParams = route.useSearch()
  const queryClient = useQueryClient()

  // In-dialog view controls (session-only; never written back to the URL).
  const [range, setRange] = useState<{ start?: Date; end?: Date }>({})
  const [granularity, setGranularity] = useState<string>('auto')
  const [customMinutes, setCustomMinutes] = useState<number>(5)
  const [rateLineInput, setRateLineInput] = useState<string>('80')
  const [refLines, setRefLines] = useState<RefLine[]>([])

  // Seed the range picker from the visible table filters each time the dialog
  // opens, and drop reference lines pinned against a previous view.
  useEffect(() => {
    if (!props.open) return
    const startMs = Number(searchParams.startTime) || undefined
    const endMs = Number(searchParams.endTime) || undefined
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setRange({
      start: startMs ? new Date(startMs) : undefined,
      end: endMs ? new Date(endMs) : undefined,
    })
    setRefLines([])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.open])

  const bucketSeconds = useMemo(() => {
    if (granularity === 'auto') return undefined
    if (granularity === 'custom') {
      const minutes = Math.floor(Number(customMinutes))
      return Number.isFinite(minutes) && minutes >= 1 ? minutes * 60 : undefined
    }
    return GRANULARITY_PRESETS.find((preset) => preset.value === granularity)
      ?.seconds
  }, [granularity, customMinutes])

  const startTs = range.start
    ? Math.floor(range.start.getTime() / 1000)
    : undefined
  const endTs = range.end ? Math.floor(range.end.getTime() / 1000) : undefined

  // Mirror the backend point cap: when an explicit fine bucket over a wide
  // window would exceed it, warn and skip the query rather than silently
  // changing the user's selection. The picker lets them narrow the range.
  const isOverCap = useMemo(() => {
    if (!bucketSeconds || startTs === undefined || endTs === undefined) {
      return false
    }
    if (endTs <= startTs) return false
    return Math.ceil((endTs - startTs) / bucketSeconds) + 1 > MAX_TREND_POINTS
  }, [bucketSeconds, startTs, endTs])

  const trendParams = useMemo(
    () =>
      buildLogTrendParams(
        searchParams as Record<string, unknown>,
        isAdmin,
        browserTzOffsetSec(),
        { startTs, endTs, bucketSeconds }
      ),
    [searchParams, isAdmin, startTs, endTs, bucketSeconds]
  )
  const paramsKey = useMemo(
    () => stableParamsKey(trendParams as Record<string, unknown>),
    [trendParams]
  )
  const queryKey = useMemo(
    () =>
      [
        TREND_QUERY_KEY,
        isAdmin ? 'admin' : 'self',
        authUserId,
        trendParams,
      ] as const,
    [authUserId, isAdmin, trendParams]
  )

  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      const result = await getLogTrend(trendParams, isAdmin, signal)
      if (!result.success || !result.data) {
        throw new Error(result.message || t('Failed to load logs'))
      }
      return normalizeTrendData(result.data)
    },
    enabled: props.open && !isOverCap,
    staleTime: TREND_STALE_TIME_MS,
    gcTime: QUERY_CACHE_TIME_MS,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: false,
  })

  const sourcePoints = useMemo(
    () =>
      (query.data?.points ?? [])
        .map((point) => normalizeTrendPoint(point))
        .filter((point): point is TrendPoint => point !== null),
    [query.data]
  )
  const points = useMemo(
    () => downsampleTrendPoints(sourcePoints),
    [sourcePoints]
  )
  const { tokenSpec, rateSpec } = useMemo(
    () =>
      query.data
        ? buildSeries(points, query.data, t, refLines)
        : { tokenSpec: null, rateSpec: null },
    [points, query.data, t, refLines]
  )
  const isPartial = Boolean(query.data?.partial || query.data?.truncated)
  const isSampled = sourcePoints.length > points.length
  const showError = query.isError && !isAbortError(query.error)
  const [hoverIndex, setHoverIndex] = useState<number | null>(null)
  const hoveredPoint = hoverIndex === null ? null : (points[hoverIndex] ?? null)
  const hoverRatio =
    hoverIndex === null || points.length < 2
      ? null
      : hoverIndex / (points.length - 1)
  const hoverBucketSeconds = Math.max(
    60,
    toFiniteNumber(query.data?.bucket_seconds, 3600)
  )
  const hoverStartSec = toFiniteNumber(
    query.data?.start_ts,
    points[0]?.bucketStart ?? 0
  )
  const hoverEndSec = toFiniteNumber(
    query.data?.end_ts,
    points[points.length - 1]?.bucketStart ?? hoverStartSec
  )
  const hoverLabel = hoveredPoint
    ? formatBucketLabel(
        hoveredPoint.bucketStart,
        hoverBucketSeconds,
        hoverStartSec,
        hoverEndSec
      )
    : ''

  const handleChartPointerMove = useCallback(
    (event: PointerEvent<HTMLDivElement>) => {
      if (points.length < 2) return
      const bounds = event.currentTarget.getBoundingClientRect()
      if (bounds.width <= 0) return
      const ratio = Math.min(
        1,
        Math.max(0, (event.clientX - bounds.left) / bounds.width)
      )
      setHoverIndex(Math.round(ratio * (points.length - 1)))
    },
    [points.length]
  )

  const handleChartPointerLeave = useCallback(() => {
    setHoverIndex(null)
  }, [])

  // Click-to-pin: drop a vertical reference line on the bucket under the cursor.
  // The X axis is a category (band) scale, so a vertical line can only sit on an
  // existing bucket; pinning the hovered bucket is the natural interaction.
  const handleChartClick = useCallback(() => {
    if (hoverIndex === null) return
    const point = points[hoverIndex]
    if (!point) return
    const time = formatBucketLabel(
      point.bucketStart,
      hoverBucketSeconds,
      hoverStartSec,
      hoverEndSec
    )
    setRefLines((prev) => {
      if (prev.some((line) => line.axis === 'x' && line.value === time)) {
        return prev
      }
      const color = REF_LINE_COLORS[prev.length % REF_LINE_COLORS.length]
      return [
        ...prev,
        { id: `x-${time}`, axis: 'x', value: time, label: time, color },
      ]
    })
  }, [hoverIndex, points, hoverBucketSeconds, hoverStartSec, hoverEndSec])

  const handleAddRateLine = useCallback(() => {
    const parsed = Number(rateLineInput)
    if (!Number.isFinite(parsed)) return
    const value = Math.min(100, Math.max(0, Math.round(parsed)))
    setRefLines((prev) => {
      const color = REF_LINE_COLORS[prev.length % REF_LINE_COLORS.length]
      return [
        ...prev,
        {
          id: `y-${value}-${Date.now()}`,
          axis: 'y',
          value,
          label: `${value}%`,
          color,
        },
      ]
    })
  }, [rateLineInput])

  const handleUpdateLabel = useCallback((id: string, label: string) => {
    setRefLines((prev) =>
      prev.map((line) => (line.id === id ? { ...line, label } : line))
    )
  }, [])

  const handleRemoveLine = useCallback((id: string) => {
    setRefLines((prev) => prev.filter((line) => line.id !== id))
  }, [])

  const handleClearLines = useCallback(() => setRefLines([]), [])

  const handleOpenChange = useCallback(
    (nextOpen: boolean) => {
      if (!nextOpen) {
        void queryClient.cancelQueries({ queryKey, exact: true })
      }
      props.onOpenChange(nextOpen)
    },
    [props, queryClient, queryKey]
  )

  return (
    <Dialog open={props.open} onOpenChange={handleOpenChange}>
      <DialogContent className='flex max-h-[calc(100dvh-2rem)] flex-col overflow-hidden sm:max-w-5xl'>
        <DialogHeader>
          <div className='flex items-start justify-between gap-3'>
            <div>
              <DialogTitle className='flex items-center gap-2'>
                <BarChart3 className='size-4' />
                {t('Cache Chart')}
              </DialogTitle>
              <DialogDescription>
                {t(
                  'Shows aggregated usage by time bucket. Raw log records are not loaded into the browser.'
                )}
              </DialogDescription>
            </div>
            {query.data && !showError && (
              <Button
                type='button'
                variant='ghost'
                size='icon'
                className='size-8 shrink-0'
                title={t('Refresh')}
                aria-label={t('Refresh')}
                onClick={() => void query.refetch()}
                disabled={query.isFetching}
              >
                <RefreshCw
                  className={
                    query.isFetching ? 'size-3.5 animate-spin' : 'size-3.5'
                  }
                />
              </Button>
            )}
          </div>
        </DialogHeader>

        <p className='text-muted-foreground text-xs leading-5'>
          {t(
            'Cache hit rate uses the aggregated hit and denominator totals returned by the server.'
          )}
        </p>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Hover over either chart to sync the time cursor and inspect all metrics.'
          )}
        </p>

        <div className='flex flex-wrap items-end gap-2 rounded-lg border bg-card/40 p-2'>
          <div className='min-w-[220px] flex-1 space-y-1'>
            <span className='text-muted-foreground text-xs'>
              {t('Time Range')}
            </span>
            <CompactDateTimeRangePicker
              start={range.start}
              end={range.end}
              onChange={(next) => setRange(next)}
            />
          </div>
          <div className='space-y-1'>
            <span className='text-muted-foreground text-xs'>
              {t('Granularity')}
            </span>
            <Select value={granularity} onValueChange={setGranularity}>
              <SelectTrigger className='h-9 w-[150px]'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='auto'>{t('Auto')}</SelectItem>
                {GRANULARITY_PRESETS.map((preset) => (
                  <SelectItem key={preset.value} value={preset.value}>
                    {t(preset.labelKey)}
                  </SelectItem>
                ))}
                <SelectItem value='custom'>{t('Custom')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {granularity === 'custom' && (
            <div className='space-y-1'>
              <span className='text-muted-foreground text-xs'>
                {t('Custom minutes')}
              </span>
              <Input
                type='number'
                min={1}
                step={1}
                value={customMinutes}
                onChange={(e) => setCustomMinutes(Number(e.target.value))}
                className='h-9 w-[110px]'
              />
            </div>
          )}
          <div className='space-y-1'>
            <span className='text-muted-foreground text-xs'>{t('Rate (%)')}</span>
            <div className='flex gap-1.5'>
              <Input
                type='number'
                min={0}
                max={100}
                step={1}
                value={rateLineInput}
                onChange={(e) => setRateLineInput(e.target.value)}
                className='h-9 w-[90px]'
              />
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='h-9 gap-1'
                onClick={handleAddRateLine}
              >
                <Plus className='size-3.5' />
                {t('Add line')}
              </Button>
            </div>
          </div>
        </div>
        <p className='text-muted-foreground text-xs'>
          {t('Click chart to pin a vertical line')}
        </p>

        <ScrollArea className='min-h-0 flex-1 pr-2'>
          <div className='space-y-4 py-2'>
            {isOverCap ? (
              <div className='flex h-[420px] flex-col items-center justify-center gap-2 rounded-lg border border-dashed text-center'>
                <p className='text-sm font-medium text-amber-600 dark:text-amber-400'>
                  {t('Range too large for this granularity')}
                </p>
                <p className='text-muted-foreground text-xs'>
                  {t('Narrow the time range or increase the granularity')}
                </p>
              </div>
            ) : query.isPending ? (
              <div className='flex h-[420px] items-center justify-center rounded-lg border border-dashed'>
                <div className='text-muted-foreground flex items-center gap-2 text-sm'>
                  <Loader2 className='size-4 animate-spin' />
                  {t('Loading...')}
                </div>
              </div>
            ) : showError ? (
              <div className='flex h-[420px] flex-col items-center justify-center gap-3 rounded-lg border border-dashed text-center'>
                <p className='text-sm text-red-500'>
                  {query.error instanceof Error
                    ? query.error.message
                    : t('Request failed')}
                </p>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => void query.refetch()}
                >
                  <RotateCcw className='size-3.5' />
                  {t('Retry')}
                </Button>
              </div>
            ) : points.length === 0 ? (
              <div className='flex h-[420px] items-center justify-center rounded-lg border border-dashed'>
                <p className='text-muted-foreground text-sm'>
                  {t('No data available')}
                </p>
              </div>
            ) : (
              <>
                {(isPartial || isSampled) && (
                  <div className='rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/20 dark:text-amber-200'>
                    {isPartial &&
                      t(
                        'The server marked this result as partial; the chart may not include every matching record.'
                      )}
                    {isPartial && isSampled ? ' ' : ''}
                    {isSampled &&
                      t('The chart is downsampled for responsive rendering.')}
                  </div>
                )}

                <section className='bg-card/40 space-y-2 rounded-lg border p-3'>
                  <div>
                    <h3 className='text-sm font-semibold'>
                      {t('Token Trend')}
                    </h3>
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'Input Tokens, Output Tokens, Cache Write, Cache Read'
                      )}
                    </p>
                  </div>
                  <div
                    className='relative h-[260px] cursor-crosshair touch-none'
                    onPointerMove={handleChartPointerMove}
                    onPointerLeave={handleChartPointerLeave}
                    onClick={handleChartClick}
                  >
                    {themeReady && tokenSpec && (
                      <VChart
                        key={`cache-token-${paramsKey}-${resolvedTheme}-${query.dataUpdatedAt}-${points.length}`}
                        spec={{
                          ...tokenSpec,
                          theme: resolvedTheme === 'dark' ? 'dark' : 'light',
                        }}
                        option={VCHART_OPTION}
                      />
                    )}
                    {hoverRatio !== null && (
                      <div
                        className='pointer-events-none absolute inset-y-1 z-10 border-l-2 border-violet-500/75'
                        style={{ left: `${hoverRatio * 100}%` }}
                      />
                    )}
                  </div>
                </section>

                <section className='bg-card/40 space-y-2 rounded-lg border p-3'>
                  <div>
                    <h3 className='text-sm font-semibold'>
                      {t('Cache Hit Rate')}
                    </h3>
                    <p className='text-muted-foreground text-xs'>
                      {t('Cache hit percentage by time bucket')}
                    </p>
                  </div>
                  <div
                    className='relative h-[220px] cursor-crosshair touch-none'
                    onPointerMove={handleChartPointerMove}
                    onPointerLeave={handleChartPointerLeave}
                    onClick={handleChartClick}
                  >
                    {themeReady && rateSpec && (
                      <VChart
                        key={`cache-rate-${paramsKey}-${resolvedTheme}-${query.dataUpdatedAt}-${points.length}`}
                        spec={{
                          ...rateSpec,
                          theme: resolvedTheme === 'dark' ? 'dark' : 'light',
                        }}
                        option={VCHART_OPTION}
                      />
                    )}
                    {hoverRatio !== null && (
                      <div
                        className='pointer-events-none absolute inset-y-1 z-10 border-l-2 border-violet-500/75'
                        style={{ left: `${hoverRatio * 100}%` }}
                      />
                    )}
                    {hoveredPoint && hoverRatio !== null && (
                      <div
                        className='bg-background/95 pointer-events-none absolute top-2 z-20 w-56 rounded-md border p-2 text-[11px] shadow-lg backdrop-blur'
                        style={{
                          left: `${hoverRatio * 100}%`,
                          transform:
                            hoverRatio > 0.72
                              ? 'translateX(-100%) translateX(-8px)'
                              : 'translateX(8px)',
                        }}
                      >
                        <div className='mb-1.5 font-medium'>
                          {t('Time')}: {hoverLabel}
                        </div>
                        <div className='grid grid-cols-[1fr_auto] gap-x-3 gap-y-1 tabular-nums'>
                          <span className='text-muted-foreground'>
                            {t('Input Tokens')}
                          </span>
                          <span>{formatTokens(hoveredPoint.inputTokens)}</span>
                          <span className='text-muted-foreground'>
                            {t('Output Tokens')}
                          </span>
                          <span>{formatTokens(hoveredPoint.outputTokens)}</span>
                          <span className='text-muted-foreground'>
                            {t('Cache Write')}
                          </span>
                          <span>
                            {formatTokens(hoveredPoint.cacheWriteTokens)}
                          </span>
                          <span className='text-muted-foreground'>
                            {t('Cache Read')}
                          </span>
                          <span>
                            {formatTokens(hoveredPoint.cacheReadTokens)}
                          </span>
                          <span className='text-muted-foreground'>
                            {t('Hit Rate')}
                          </span>
                          <span>
                            {hoveredPoint.cacheHitRate === null
                              ? '--'
                              : `${hoveredPoint.cacheHitRate.toFixed(2)}%`}
                          </span>
                        </div>
                      </div>
                    )}
                  </div>
                </section>

                {refLines.length > 0 && (
                  <section className='bg-card/40 space-y-2 rounded-lg border p-3'>
                    <div className='flex items-center justify-between gap-2'>
                      <h3 className='text-sm font-semibold'>
                        {t('Reference Lines')}
                      </h3>
                      <Button
                        type='button'
                        variant='ghost'
                        size='sm'
                        className='h-7 gap-1 text-xs'
                        onClick={handleClearLines}
                      >
                        <Trash2 className='size-3.5' />
                        {t('Clear all')}
                      </Button>
                    </div>
                    <ul className='space-y-1.5'>
                      {refLines.map((line) => (
                        <li key={line.id} className='flex items-center gap-2'>
                          <span
                            className='h-3 w-3 shrink-0 rounded-sm'
                            style={{ background: line.color }}
                          />
                          <span className='text-muted-foreground w-12 shrink-0 text-xs'>
                            {line.axis === 'x' ? t('Time') : t('Hit Rate')}
                          </span>
                          <span className='w-28 shrink-0 truncate font-mono text-xs'>
                            {line.axis === 'x'
                              ? String(line.value)
                              : `${line.value}%`}
                          </span>
                          <Input
                            value={line.label}
                            onChange={(e) =>
                              handleUpdateLabel(line.id, e.target.value)
                            }
                            placeholder={t('Label')}
                            className='h-7 flex-1 text-xs'
                          />
                          <Button
                            type='button'
                            variant='ghost'
                            size='icon'
                            className='size-7 shrink-0'
                            aria-label={t('Delete')}
                            onClick={() => handleRemoveLine(line.id)}
                          >
                            <X className='size-3.5' />
                          </Button>
                        </li>
                      ))}
                    </ul>
                  </section>
                )}
              </>
            )}
          </div>
        </ScrollArea>
      </DialogContent>
    </Dialog>
  )
}

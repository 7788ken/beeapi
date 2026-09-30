import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getPerfMetricsSummary } from '@/features/performance-metrics/api'
import type { PerfModelSummary } from '@/features/performance-metrics/types'

export const PERFORMANCE_WINDOW_HOURS = 24
const TOP_MODEL_LIMIT = 5

type WeightedMetric = 'avg_latency_ms' | 'avg_tps' | 'success_rate'

function simpleAverage(
  rows: PerfModelSummary[],
  metric: WeightedMetric,
  isValid: (value: number) => boolean
): number {
  let total = 0
  let count = 0
  for (const row of rows) {
    const value = Number(row[metric])
    if (!isValid(value)) continue
    total += value
    count++
  }
  return count > 0 ? total / count : NaN
}

export type RateLevel = 'normal' | 'attention' | 'failing' | 'unknown'

export function rateLevel(rate: number): RateLevel {
  if (!Number.isFinite(rate)) return 'unknown'
  if (rate >= 99.9) return 'normal'
  if (rate >= 99) return 'attention'
  return 'failing'
}

export function usePerformanceSummary(enabled: boolean) {
  const metricsQuery = useQuery({
    queryKey: ['perf-metrics-summary', PERFORMANCE_WINDOW_HOURS],
    queryFn: () => getPerfMetricsSummary(PERFORMANCE_WINDOW_HOURS),
    staleTime: 60 * 1000,
    retry: false,
    enabled,
  })

  const models = useMemo(
    () => metricsQuery.data?.data.models ?? [],
    [metricsQuery.data]
  )

  const summary = useMemo(() => {
    return {
      avgLatencyMs: Math.round(
        simpleAverage(models, 'avg_latency_ms', (v) => Number.isFinite(v) && v > 0)
      ),
      avgTps: simpleAverage(models, 'avg_tps', (v) => Number.isFinite(v) && v > 0),
      successRate: simpleAverage(models, 'success_rate', Number.isFinite),
    }
  }, [models])

  return {
    metricsQuery,
    models,
    summary,
    topModels: models.slice(0, TOP_MODEL_LIMIT),
    loading: enabled && metricsQuery.isLoading,
    hasData: models.length > 0,
    level: rateLevel(summary.successRate),
  }
}

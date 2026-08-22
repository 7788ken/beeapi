export type PerformanceSeriesPoint = {
  ts: number
  avg_ttft_ms: number
  avg_latency_ms: number
  success_rate: number
  count: number
  success_count: number
  ttft_count: number
}

export type PerformanceGroup = {
  group: string
  avg_ttft_ms: number
  avg_latency_ms: number
  success_rate: number
  request_count: number
  success_count: number
  ttft_count: number
  avg_tps: number
  series: PerformanceSeriesPoint[]
}

export type PerformanceMetricsData = {
  success: boolean
  message?: string
  data: {
    model_name: string
    series_schema?: string
    groups: PerformanceGroup[]
  }
}

export type PerfModelSummary = {
  model_name: string
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
  // 后端 ModelSummary.RequestCount 已 json:"-"，此字段保留仅作类型预留，前端拿到永远 undefined
  request_count?: number
}

export type PerfSummaryAllData = {
  success: boolean
  message?: string
  data: {
    models: PerfModelSummary[]
  }
}

import { api } from '@/lib/api'

export interface IQSetting {
  enabled: boolean
  enforcement_mode: 'enforce' | 'report_only'
  interval_minutes: number
  concurrency: number
  disable_below_baseline: boolean
  priority_step: number
  questions_per_round: number
  per_question_timeout_seconds: number
  notify_on_action: boolean
  retention_days: number
  version: number
}

export interface IQModel {
  id: number
  model_name: string
  baseline_score: number
  enabled: boolean
  version: number
}

export interface IQRun {
  id: number
  run_id: string
  status: string
  trigger: string
  channel_id: number
  candidate_count: number
  success_count: number
  invalid_count: number
  error_count: number
  started_at: number
  finished_at: number
  error_message?: string
  bank_version: string
}

export interface IQResult {
  id: number
  run_id: string
  channel_id: number
  requested_model: string
  upstream_model: string
  status: string
  score: number | null
  baseline_score_snapshot: number
  margin: number | null
  correct_count: number
  total_questions: number
  duration_ms: number
  detail?: string
  error_class?: string
  action: string
  action_reason?: string
  priority_before?: number
  priority_after?: number
  finished_at: number
}

export interface IQCoverageSkip {
  channel_id: number
  name: string
  type: number
  model_name: string
  reason: string
}

export interface IQCoverage {
  enabled: boolean
  enforcement_mode: string
  models: string[]
  eligible_channels: number
  eligible_pairs: number
  skipped: IQCoverageSkip[]
  skipped_total: number
}

// startIQRun is accepted asynchronously: the endpoint reports the created round,
// not its counters, which the caller then polls.
export interface IQRunStart {
  run_id: string
  status: string
}

export interface IQPage<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

interface Response<T> {
  success: boolean
  message?: string
  data: T
}
export interface IQFilters {
  page: number
  page_size: number
  channel_id?: number
  model_name?: string
  status?: string
  run_id?: string
}

function unwrap<T>(response: Response<T>): T {
  if (!response.success) throw new Error(response.message)
  return response.data
}

export const iqKeys = {
  all: ['iq-test'] as const,
  setting: ['iq-test', 'setting'] as const,
  models: ['iq-test', 'models'] as const,
  runs: ['iq-test', 'runs'] as const,
  results: ['iq-test', 'results'] as const,
  coverage: ['iq-test', 'coverage'] as const,
}
export const getIQSetting = async () =>
  unwrap((await api.get<Response<IQSetting>>('/api/iq_test/setting')).data)
export const saveIQSetting = async (setting: IQSetting) =>
  unwrap(
    (await api.put<Response<IQSetting>>('/api/iq_test/setting', setting)).data
  )
export const getIQModels = async (
  page: number,
  enabledOnly = false,
  pageSize = 20
) =>
  unwrap(
    (
      await api.get<Response<IQPage<IQModel>>>('/api/iq_test/models', {
        params: {
          page,
          page_size: pageSize,
          enabled_only: enabledOnly || undefined,
        },
      })
    ).data
  )
export const saveIQModel = async (
  model: Omit<IQModel, 'id' | 'version'> & { id?: number; version?: number }
) => {
  if (model.id)
    return unwrap(
      (
        await api.put<Response<IQModel>>(
          `/api/iq_test/models/${model.id}`,
          model
        )
      ).data
    )
  return unwrap(
    (await api.post<Response<IQModel>>('/api/iq_test/models', model)).data
  )
}
export const deleteIQModel = async (model: IQModel) =>
  unwrap(
    (
      await api.delete<Response<null>>(`/api/iq_test/models/${model.id}`, {
        params: { version: model.version },
      })
    ).data
  )
export const getIQRuns = async (params: IQFilters) =>
  unwrap(
    (await api.get<Response<IQPage<IQRun>>>('/api/iq_test/runs', { params }))
      .data
  )
export const getIQRun = async (runID: string) =>
  unwrap(
    (
      await api.get<Response<IQRun>>(
        `/api/iq_test/runs/${encodeURIComponent(runID)}`
      )
    ).data
  )
export const getIQResults = async (params: IQFilters) =>
  unwrap(
    (
      await api.get<Response<IQPage<IQResult>>>('/api/iq_test/results', {
        params: { ...params, model: params.model_name, model_name: undefined },
      })
    ).data
  )
export const startIQRun = async (request: {
  channel_id?: number
  model_names?: string[]
  idempotency_key: string
}) =>
  unwrap(
    (await api.post<Response<IQRunStart>>('/api/iq_test/run_now', request)).data
  )
export const getIQCoverage = async (params: {
  channel_id?: number
  model_names?: string[]
}) =>
  unwrap(
    (
      await api.get<Response<IQCoverage>>('/api/iq_test/coverage', {
        params: {
          channel_id: params.channel_id,
          model_names: params.model_names?.join(','),
        },
      })
    ).data
  )

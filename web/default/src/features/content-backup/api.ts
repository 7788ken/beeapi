import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { api } from '@/lib/api'
import {
  clampPageSize,
  normalizeJobTimes,
  normalizeNodeTimes,
  normalizeStatusTimes,
  sessionFingerprint,
} from './status'
import {
  CONTENT_BACKUP_DEFAULT_PAGE_SIZE,
  type ContentBackupChannelBackupResult,
  type ContentBackupConfig,
  type ContentBackupConfigData,
  type ContentBackupConfigSaveData,
  type ContentBackupJob,
  type ContentBackupJobPage,
  type ContentBackupJobsQuery,
  type ContentBackupNodeSnapshot,
  type ContentBackupPreview,
  type ContentBackupProbeResult,
  type ContentBackupResponse,
  type ContentBackupRetryResult,
  type ContentBackupSessionSearchPayload,
  type ContentBackupStatusSummary,
} from './types'

/**
 * 契约唯一落点：T09/T10 的最终字段名若有偏差，只需要改这个文件与 types.ts。
 * 所有响应都按文档 9.2 的 { success, message, data } 包装解析，时间统一归一化。
 */
const BASE = '/api/content_backup'

export const contentBackupKeys = {
  all: ['content-backup'] as const,
  status: () => [...contentBackupKeys.all, 'status'] as const,
  nodes: () => [...contentBackupKeys.all, 'nodes'] as const,
  jobs: (query: ContentBackupJobsQuery) =>
    [...contentBackupKeys.all, 'jobs', query] as const,
  sessionJobs: (key: SessionSearchKey | { disabled: true }) =>
    [...contentBackupKeys.all, 'session-jobs', key] as const,
  job: (jobId: string) => [...contentBackupKeys.all, 'job', jobId] as const,
  preview: (jobId: string) =>
    [...contentBackupKeys.all, 'preview', jobId] as const,
  config: () => [...contentBackupKeys.all, 'config'] as const,
}

function normalizeJobPage(page?: ContentBackupJobPage): ContentBackupJobPage {
  return {
    items: (page?.items ?? []).map(normalizeJobTimes),
    next_cursor: page?.next_cursor ?? null,
    has_more: page?.has_more === true,
  }
}

export function buildJobsParams(
  query: ContentBackupJobsQuery
): Record<string, string | number> {
  const params: Record<string, string | number> = {
    view: query.view,
    page_size: clampPageSize(query.page_size ?? CONTENT_BACKUP_DEFAULT_PAGE_SIZE),
  }
  if (query.status) params.status = query.status
  if (query.cleanup_state) params.cleanup_state = query.cleanup_state
  if (query.request_id) params.request_id = query.request_id
  if (typeof query.user_id === 'number') params.user_id = query.user_id
  if (typeof query.channel_id === 'number') params.channel_id = query.channel_id
  if (query.storage_node_id) params.storage_node_id = query.storage_node_id
  if (query.from) params.from = query.from
  if (query.to) params.to = query.to
  // 游标是不透明字符串，原样回传，前端不解析
  if (query.cursor) params.cursor = query.cursor
  return params
}

export async function fetchContentBackupStatus(): Promise<ContentBackupStatusSummary> {
  const res = await api.get<ContentBackupResponse<ContentBackupStatusSummary>>(
    `${BASE}/status`
  )
  return normalizeStatusTimes(res.data?.data ?? {})
}

export async function fetchContentBackupNodes(): Promise<
  ContentBackupNodeSnapshot[]
> {
  const res = await api.get<
    ContentBackupResponse<
      ContentBackupNodeSnapshot[] | { items?: ContentBackupNodeSnapshot[] }
    >
  >(`${BASE}/nodes`)
  const payload = res.data?.data
  const list = Array.isArray(payload) ? payload : (payload?.items ?? [])
  return list.map(normalizeNodeTimes)
}

export async function fetchContentBackupJobs(
  query: ContentBackupJobsQuery
): Promise<ContentBackupJobPage> {
  const res = await api.get<ContentBackupResponse<ContentBackupJobPage>>(
    `${BASE}/jobs`,
    { params: buildJobsParams(query) }
  )
  return normalizeJobPage(res.data?.data)
}

/** 原始会话值只在请求体里出现，绝不进 URL、查询键或本地存储 */
export async function searchContentBackupJobsBySession(
  payload: ContentBackupSessionSearchPayload
): Promise<ContentBackupJobPage> {
  const res = await api.post<ContentBackupResponse<ContentBackupJobPage>>(
    `${BASE}/jobs/search-session`,
    {
      user_id: payload.user_id,
      session_value: payload.session_value,
      session_source: payload.session_source,
      from: payload.from,
      to: payload.to,
      cursor: payload.cursor ?? undefined,
      page_size: clampPageSize(
        payload.page_size ?? CONTENT_BACKUP_DEFAULT_PAGE_SIZE
      ),
    }
  )
  return normalizeJobPage(res.data?.data)
}

export async function fetchContentBackupJob(
  jobId: string
): Promise<ContentBackupJob | undefined> {
  const res = await api.get<ContentBackupResponse<ContentBackupJob>>(
    `${BASE}/jobs/${encodeURIComponent(jobId)}`
  )
  const job = res.data?.data
  return job ? normalizeJobTimes(job) : undefined
}

/** Root only；每侧最多 256 KiB，正文按需拉取，列表从不带正文 */
export async function fetchContentBackupPreview(
  jobId: string
): Promise<ContentBackupPreview> {
  const res = await api.get<ContentBackupResponse<ContentBackupPreview>>(
    `${BASE}/jobs/${encodeURIComponent(jobId)}/preview`
  )
  return res.data?.data ?? {}
}

export async function retryContentBackupJobs(
  jobIds: string[]
): Promise<ContentBackupRetryResult[]> {
  const res = await api.post<
    ContentBackupResponse<
      ContentBackupRetryResult[] | {
        items?: ContentBackupRetryResult[]
        results?: ContentBackupRetryResult[]
      }
    >
  >(`${BASE}/jobs/retry`, { job_ids: jobIds })
  const payload = res.data?.data
  if (Array.isArray(payload)) return payload
  return payload?.items ?? payload?.results ?? []
}

/** Root only；流式下载原始 gzip，服务端固定文件名 */
export async function downloadContentBackupJob(
  jobId: string,
  filename?: string
): Promise<void> {
  const res = await api.get(
    `${BASE}/jobs/${encodeURIComponent(jobId)}/download`,
    { responseType: 'blob' }
  )
  const blob = res.data as Blob
  if (blob.type.includes('application/json')) {
    const parsed = JSON.parse(await blob.text()) as { message?: string }
    throw new Error(parsed.message || 'download failed')
  }
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename ?? `${jobId}.json.gz`
  anchor.click()
  URL.revokeObjectURL(url)
}

/**
 * Root only。密码从不回显：remote_password_set / remote_credentials_set 只是布尔徽章，
 * 用户名随配置返回，密码只以"已存"布尔位出现；表单里密码留空即沿用已存。
 */
export async function fetchContentBackupConfig(): Promise<ContentBackupConfigData> {
  const res = await api.get<ContentBackupResponse<ContentBackupConfigData>>(
    `${BASE}/config`
  )
  const data = res.data?.data
  if (!data) {
    throw new Error(res.data?.message || 'content backup config response missing data')
  }
  return data
}

/** Root only；整包保存，expected_version 做乐观并发控制，冲突返回 HTTP 409 */
export async function saveContentBackupConfig(
  config: ContentBackupConfig,
  expectedVersion: number
): Promise<ContentBackupConfig> {
  const res = await api.put<ContentBackupResponse<ContentBackupConfigSaveData>>(
    `${BASE}/config`,
    { config, expected_version: expectedVersion }
  )
  const saved = res.data?.data?.config
  if (!saved) {
    throw new Error(res.data?.message || 'content backup config save response missing data')
  }
  return saved
}

/**
 * Root only；请求体为空——服务端只探测自己当前已保存的 target，调用方无法
 * 指定任意主机（设计上的限制，见 controller ContentBackupTestConnection）。
 * 200 响应里 stages[] 各项仍可能 ok=false：只有完全无法尝试才是非 2xx。
 */
export async function testContentBackupConnection(): Promise<ContentBackupProbeResult> {
  const res = await api.post<ContentBackupResponse<ContentBackupProbeResult>>(
    `${BASE}/test_connection`
  )
  const data = res.data?.data
  if (!data) {
    throw new Error(res.data?.message || 'content backup test connection response missing data')
  }
  return data
}

/**
 * 需要 channel.edit；一次最多 100 个渠道 ID（后端硬校验）。后端按读-改-写
 * 事务逐条更新，只翻转 content_backup_enabled，其余 setting 字段原样保留，
 * 因此这里只发 { channel_ids, enabled }，不发整份渠道设置。
 */
export async function updateContentBackupChannelsBackup(
  channelIds: number[],
  enabled: boolean
): Promise<ContentBackupChannelBackupResult> {
  const res = await api.put<
    ContentBackupResponse<ContentBackupChannelBackupResult>
  >(`${BASE}/channels/backup`, { channel_ids: channelIds, enabled })
  const data = res.data?.data
  if (!data) {
    throw new Error(res.data?.message || 'content backup channel toggle response missing data')
  }
  return data
}

export function apiErrorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') return undefined
  const candidate = error as { response?: { status?: number } }
  return candidate.response?.status
}

export function describeApiError(error: unknown): string | undefined {
  if (!error || typeof error !== 'object') return undefined
  const candidate = error as {
    response?: { data?: { message?: string } }
    message?: string
  }
  return candidate.response?.data?.message ?? candidate.message
}

/**
 * 本功能的查询在页面内显示失败原因（节点面板保留上次成功数据、抽屉里显示读取失败），
 * 不让一次 500 把整个后台换成通用错误页（设计文档 9.3）。
 */
export const CONTENT_BACKUP_QUERY_META = { inlineError: true } as const

export interface ContentBackupQueryOptions {
  enabled?: boolean
  pollMs?: number
  /** 有选中项或详情抽屉打开时停止轮询，避免行在用户操作途中移动 */
  hold?: boolean
}

const DEFAULT_POLL_MS = 30_000
const FAST_POLL_MS = 15_000

/** 导出仅为可测：隐藏页停轮询与抽屉/选中暂停是验收项，必须能被断言 */
export function refetchPolicy(options?: ContentBackupQueryOptions) {
  return () => {
    if (options?.hold) return false
    if (typeof document !== 'undefined' && document.hidden) return false
    return options?.pollMs ?? DEFAULT_POLL_MS
  }
}

export function useContentBackupStatus(options?: ContentBackupQueryOptions) {
  return useQuery({
    meta: CONTENT_BACKUP_QUERY_META,
    queryKey: contentBackupKeys.status(),
    queryFn: fetchContentBackupStatus,
    enabled: options?.enabled !== false,
    refetchInterval: refetchPolicy({
      pollMs: FAST_POLL_MS,
      ...options,
    }),
    refetchOnWindowFocus: false,
  })
}

export function useContentBackupNodes(options?: ContentBackupQueryOptions) {
  return useQuery({
    meta: CONTENT_BACKUP_QUERY_META,
    queryKey: contentBackupKeys.nodes(),
    queryFn: fetchContentBackupNodes,
    enabled: options?.enabled !== false,
    refetchInterval: refetchPolicy({
      pollMs: FAST_POLL_MS,
      ...options,
    }),
    refetchOnWindowFocus: false,
  })
}

export function useContentBackupJobs(
  query: ContentBackupJobsQuery,
  options?: ContentBackupQueryOptions
) {
  return useQuery({
    meta: CONTENT_BACKUP_QUERY_META,
    queryKey: contentBackupKeys.jobs(query),
    queryFn: () => fetchContentBackupJobs(query),
    enabled: options?.enabled !== false,
    placeholderData: keepPreviousData,
    refetchInterval: refetchPolicy(options),
    refetchOnWindowFocus: false,
  })
}

export interface SessionSearchKey {
  user_id?: number
  session_source?: string
  from?: string
  to?: string
  cursor?: string | null
  page_size?: number
  /** 原值不进查询键，只用指纹区分不同会话 */
  fingerprint: string
}

export function sessionSearchKey(
  payload: ContentBackupSessionSearchPayload
): SessionSearchKey {
  return {
    user_id: payload.user_id,
    session_source: payload.session_source,
    from: payload.from,
    to: payload.to,
    cursor: payload.cursor ?? null,
    page_size: clampPageSize(
      payload.page_size ?? CONTENT_BACKUP_DEFAULT_PAGE_SIZE
    ),
    fingerprint: sessionFingerprint(payload.session_value),
  }
}

export function useContentBackupSessionJobs(
  payload: ContentBackupSessionSearchPayload | null,
  options?: ContentBackupQueryOptions
) {
  const key = payload ? sessionSearchKey(payload) : null
  // 原始会话值不能进查询键（会被序列化/打印），键里只放指纹，queryFn 才用原值
  // eslint-disable-next-line @tanstack/query/exhaustive-deps
  return useQuery({
    meta: CONTENT_BACKUP_QUERY_META,
    queryKey: contentBackupKeys.sessionJobs(key ?? { disabled: true }),
    queryFn: () => {
      if (!payload) return Promise.resolve(normalizeJobPage(undefined))
      return searchContentBackupJobsBySession(payload)
    },
    enabled: payload !== null && options?.enabled !== false,
    placeholderData: keepPreviousData,
    refetchOnWindowFocus: false,
  })
}

export function useContentBackupJob(
  jobId: string | null,
  options?: ContentBackupQueryOptions & { initialJob?: ContentBackupJob }
) {
  const initialJob = options?.initialJob
  return useQuery({
    meta: CONTENT_BACKUP_QUERY_META,
    queryKey: contentBackupKeys.job(jobId ?? ''),
    queryFn: () => {
      if (!jobId) return Promise.resolve(undefined)
      return fetchContentBackupJob(jobId)
    },
    enabled: jobId !== null && options?.enabled !== false,
    placeholderData: initialJob ? () => initialJob : undefined,
    refetchOnWindowFocus: false,
  })
}

/** 正文只在用户显式请求时拉取 */
export function useContentBackupPreview(
  jobId: string | null,
  options?: ContentBackupQueryOptions
) {
  return useQuery({
    meta: CONTENT_BACKUP_QUERY_META,
    queryKey: contentBackupKeys.preview(jobId ?? ''),
    queryFn: () => {
      if (!jobId) return Promise.resolve(undefined)
      return fetchContentBackupPreview(jobId)
    },
    enabled: jobId !== null && options?.enabled === true,
    refetchOnWindowFocus: false,
  })
}

/** 重试后只失效本功能相关查询，不清空整站缓存 */
export function useContentBackupRetry() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (jobIds: string[]) => retryContentBackupJobs(jobIds),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contentBackupKeys.all })
    },
  })
}

export function useContentBackupDownload() {
  return useMutation({
    mutationFn: (input: { jobId: string; filename?: string }) =>
      downloadContentBackupJob(input.jobId, input.filename),
  })
}

/** Root only；不轮询——配置很少变化，轮询反而可能在用户编辑途中把表单冲掉 */
export function useContentBackupConfig(options?: { enabled?: boolean }) {
  return useQuery({
    meta: CONTENT_BACKUP_QUERY_META,
    queryKey: contentBackupKeys.config(),
    queryFn: fetchContentBackupConfig,
    enabled: options?.enabled !== false,
    refetchOnWindowFocus: false,
  })
}

/** 保存成功后失效 config 缓存，用最新 version 重新拉一次，避免下一次保存立刻 409 */
export function useSaveContentBackupConfig() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: {
      config: ContentBackupConfig
      expectedVersion: number
    }) => saveContentBackupConfig(input.config, input.expectedVersion),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: contentBackupKeys.config() })
    },
  })
}

export function useTestContentBackupConnection() {
  return useMutation({
    mutationFn: testContentBackupConnection,
  })
}

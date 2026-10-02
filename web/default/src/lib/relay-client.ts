// 用用户自己的 API Key 直连同源 /v1 中转接口：计费、分组、模型限制都按这把 key 走，和用户在自己的客户端里调用完全一致。
// 不能用 @/lib/api 的 axios 实例：它的拦截器会把 Authorization 换成控制台登录凭证，401 时还会触发登出。

export class RelayError extends Error {
  readonly status: number
  readonly code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'RelayError'
    this.status = status
    this.code = code
  }
}

type RelayErrorBody = {
  error?: { message?: string; code?: string } | string
  message?: string
}

function readErrorBody(text: string): { message?: string; code?: string } {
  let body: RelayErrorBody
  try {
    body = JSON.parse(text) as RelayErrorBody
  } catch {
    // 网关/反代的 HTML 或纯文本错误页，原文就是最有用的信息
    return { message: text.trim().slice(0, 300) }
  }
  if (typeof body.error === 'string') return { message: body.error }
  if (body.error?.message) {
    return { message: body.error.message, code: body.error.code || undefined }
  }
  return { message: body.message }
}

/** 把非 2xx 响应转成 RelayError；SSE 的 error 事件也复用这里的解析。 */
export function parseRelayError(status: number, text: string): RelayError {
  const { message, code } = readErrorBody(text)
  return new RelayError(message || `HTTP ${status}`, status, code)
}

export function relayAuthHeaders(secret: string): Record<string, string> {
  return { Authorization: `Bearer ${secret}` }
}

interface RelayRequestInit {
  json?: unknown
  form?: FormData
  signal?: AbortSignal
}

export async function relayRequest<T>(
  secret: string,
  path: string,
  init: RelayRequestInit = {}
): Promise<T> {
  const headers = relayAuthHeaders(secret)
  let body: BodyInit | undefined = init.form
  if (init.json !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(init.json)
  }
  const res = await fetch(path, {
    method: body ? 'POST' : 'GET',
    headers,
    body,
    signal: init.signal,
    credentials: 'omit',
  })
  if (!res.ok) throw parseRelayError(res.status, await res.text())
  return (await res.json()) as T
}

export interface RelayModel {
  id: string
  /** 后端按模型元数据算出的可用端点：openai / anthropic / gemini / image-generation / embeddings … */
  endpointTypes: string[]
}

interface ListModelsBody {
  success?: boolean
  message?: string
  data?: Array<{ id: string; supported_endpoint_types?: string[] | null }>
}

/** 这把 key 能用的模型（已按 key 的分组与模型限制过滤），按名称排序。 */
export async function listKeyModels(secret: string): Promise<RelayModel[]> {
  const body = await relayRequest<ListModelsBody>(secret, '/v1/models')
  if (body.success === false || !Array.isArray(body.data)) {
    throw new RelayError(body.message || 'Failed to load models', 200)
  }
  return body.data
    .map((m) => ({ id: m.id, endpointTypes: m.supported_endpoint_types ?? [] }))
    .sort((a, b) => a.id.localeCompare(b.id))
}

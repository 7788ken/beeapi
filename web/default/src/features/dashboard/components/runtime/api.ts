import { api } from '@/lib/api'
import type { RuntimeSnapshot } from './types'

interface ApiResp<T> {
  success: boolean
  message?: string
  data?: T
}

// RuntimeRateLimitedError 标记后端 429 Too Many Requests，让 react-query 用退避算法重试，
// 同时让 UI 显示"已限速"横幅而不是全局 toast spam。
export class RuntimeRateLimitedError extends Error {
  constructor() {
    super('runtime_rate_limited')
    this.name = 'RuntimeRateLimitedError'
  }
}

export async function getRuntimeSnapshot(): Promise<ApiResp<RuntimeSnapshot>> {
  try {
    // skipErrorHandler: 429 由组件自己处理（退避 + banner），不走全局 toast
    const res = await api.get('/api/data/runtime', {
      skipErrorHandler: true,
    } as unknown as Record<string, unknown>)
    return res.data
  } catch (err: unknown) {
    const status = (err as { response?: { status?: number } })?.response?.status
    if (status === 429) {
      throw new RuntimeRateLimitedError()
    }
    throw err
  }
}

export async function triggerRecomputeRuntime(): Promise<ApiResp<{ triggered: boolean }>> {
  const res = await api.post('/api/data/runtime/recompute')
  return res.data
}

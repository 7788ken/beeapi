// 创作中心「先选 key，再按这把 key 列模型」的数据链：
// 我的 key 列表（脱敏）→ 选中项的完整 key（只放内存，不落 localStorage）→ /v1/models 按 key 过滤后的模型。
import { useQuery } from '@tanstack/react-query'
import { listKeyModels } from '@/lib/relay-client'
import { fetchTokenKey, getApiKeys } from '@/features/keys/api'
import { API_KEY_STATUS } from '@/features/keys/constants'
import type { ApiKey } from '@/features/keys/types'

/** 值是 i18n key，展示时 t(reason) */
export type KeyUnusableReason = 'Disabled' | 'Expired' | 'Exhausted'

/** 过期、耗尽只在调用时才写回库，列表里的 status 可能还是「启用」，所以同时看时间和额度。 */
export function getKeyUnusableReason(key: ApiKey): KeyUnusableReason | null {
  if (key.status === API_KEY_STATUS.DISABLED) return 'Disabled'
  const expired =
    key.status === API_KEY_STATUS.EXPIRED ||
    (key.expired_time !== -1 && key.expired_time * 1000 <= Date.now())
  if (expired) return 'Expired'
  const exhausted =
    key.status === API_KEY_STATUS.EXHAUSTED ||
    (!key.unlimited_quota && key.remain_quota <= 0)
  if (exhausted) return 'Exhausted'
  return null
}

function useMyApiKeys() {
  return useQuery({
    queryKey: ['relay-keys'],
    queryFn: async () => {
      // 后端单页上限 100
      const res = await getApiKeys({ p: 1, size: 100 })
      if (!res.success)
        throw new Error(res.message || 'Failed to load API keys')
      return res.data?.items ?? []
    },
    staleTime: 60_000,
    // 用户常会按提示去 /keys 新建 key 或放开模型再回来，回来就要看到新的
    refetchOnMount: 'always',
  })
}

/** 取完整 key 的接口与登录、刷新共用 IP 限额，所以整个页面会话只取一次，失败也不自动重试。 */
function useApiKeySecret(id: number | null) {
  return useQuery({
    queryKey: ['relay-key-secret', id],
    queryFn: async () => {
      const res = await fetchTokenKey(id as number)
      if (!res.success || !res.data?.key) {
        throw new Error(res.message || 'Failed to load API key')
      }
      return `sk-${res.data.key}`
    },
    enabled: id !== null,
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
  })
}

/**
 * @param selectedId 调用方记住的 key id。没选过、或选过的 key 已被删除时落到第一个可用 key；
 * 选过的 key 还在但已禁用/过期/耗尽时停在它上面（activeKeyUnusable 给出原因），不悄悄换成别的 key 计费。
 */
export function useRelayKey(selectedId: number | null) {
  const keysQuery = useMyApiKeys()
  const keys = keysQuery.data ?? []
  const activeKey =
    keys.find((k) => k.id === selectedId) ??
    keys.find((k) => getKeyUnusableReason(k) === null) ??
    null
  const activeKeyUnusable = activeKey ? getKeyUnusableReason(activeKey) : null
  const usableId = activeKey && !activeKeyUnusable ? activeKey.id : null

  const secretQuery = useApiKeySecret(usableId)
  // 禁用的查询仍会返回旧缓存，key 变成不可用后不能再拿它发请求
  const secret = usableId === null ? undefined : secretQuery.data

  const modelsQuery = useQuery({
    // 完整 key 只是内存里的缓存键，不会持久化
    queryKey: ['relay-key-models', usableId, secret],
    queryFn: () => listKeyModels(secret as string),
    enabled: usableId !== null && !!secret,
    staleTime: 5 * 60_000,
    refetchOnMount: 'always',
    retry: false,
  })

  return {
    keys,
    keysLoading: keysQuery.isLoading,
    keysError: keysQuery.error,
    activeKey,
    activeKeyUnusable,
    secret,
    secretError: usableId === null ? null : secretQuery.error,
    retrySecret: secretQuery.refetch,
    models: usableId === null ? [] : (modelsQuery.data ?? []),
    /** 还在取 key 或模型，模型选择器显示加载中 */
    modelsLoading:
      keysQuery.isLoading || secretQuery.isLoading || modelsQuery.isLoading,
    modelsError: usableId === null ? null : modelsQuery.error,
    retryModels: modelsQuery.refetch,
  }
}

export type RelayKeyState = ReturnType<typeof useRelayKey>

/**
 * 模型选择器里要说明的失败：沿「key 列表 → 完整 key → 模型」找第一个没拿到结果的环节。
 * 已有结果时的后台刷新失败不算，不打断正在用的列表。
 */
export function getRelayFailure(relay: RelayKeyState): {
  error: Error | null
  retry?: () => void
} {
  if (!relay.activeKey) return { error: relay.keysError }
  if (relay.activeKeyUnusable) return { error: null }
  if (!relay.secret) {
    return { error: relay.secretError, retry: () => void relay.retrySecret() }
  }
  if (relay.models.length === 0) {
    return { error: relay.modelsError, retry: () => void relay.retryModels() }
  }
  return { error: null }
}

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getUserModels, getUserGroups } from '@/lib/api'
import { useIsAdmin } from '@/hooks/use-admin'
import { useDebounce } from '@/hooks'
import { getGroups as getAllGroups, searchUsers } from '@/features/users/api'
import type { ComboboxInputOption } from '@/components/ui/combobox-input'

const STALE = 5 * 60 * 1000

export function useFilterOptions() {
  const isAdmin = useIsAdmin()

  const { data: modelsResp } = useQuery({
    queryKey: ['usage-logs', 'filter-options', 'models'],
    queryFn: getUserModels,
    staleTime: STALE,
  })

  const { data: userGroupsResp } = useQuery({
    queryKey: ['usage-logs', 'filter-options', 'user-groups'],
    queryFn: getUserGroups,
    staleTime: STALE,
    enabled: !isAdmin,
  })

  const { data: adminGroupsResp } = useQuery({
    queryKey: ['usage-logs', 'filter-options', 'admin-groups'],
    queryFn: getAllGroups,
    staleTime: STALE,
    enabled: isAdmin,
  })

  const modelOptions = useMemo<ComboboxInputOption[]>(() => {
    const arr = modelsResp?.data ?? []
    return [...new Set(arr)]
      .sort()
      .map((v) => ({ label: v, value: v }))
  }, [modelsResp])

  const groupOptions = useMemo<ComboboxInputOption[]>(() => {
    let names: string[] = []
    if (isAdmin) {
      names = adminGroupsResp?.data ?? []
    } else {
      names = Object.keys(userGroupsResp?.data ?? {})
    }
    return names
      .filter((n) => n !== 'auto')
      .sort()
      .map((n) => ({ label: n, value: n }))
  }, [isAdmin, userGroupsResp, adminGroupsResp])

  return { modelOptions, groupOptions }
}

/**
 * 用户名远程搜索 hook（仅 admin 有权访问 /api/user/search）。
 * - 输入空字符串：不发请求，options 为空
 * - 输入非空：debounce 300ms 后调 searchUsers
 * 调用方维护输入态（filter value），把它原样传进来即可。
 */
export function useUsernameSearch(keyword: string) {
  const isAdmin = useIsAdmin()
  const debounced = useDebounce(keyword.trim(), 300)
  const enabled = isAdmin && debounced.length > 0

  const { data, isFetching } = useQuery({
    queryKey: ['usage-logs', 'filter-options', 'username-search', debounced],
    queryFn: () => searchUsers({ keyword: debounced, p: 1, page_size: 20 }),
    enabled,
    staleTime: 60 * 1000,
  })

  const usernameOptions = useMemo<ComboboxInputOption[]>(() => {
    if (!enabled) return []
    const items = data?.data?.items ?? []
    const seen = new Set<string>()
    const out: ComboboxInputOption[] = []
    for (const u of items) {
      if (!u.username || seen.has(u.username)) continue
      seen.add(u.username)
      const label = u.display_name
        ? `${u.username} (${u.display_name})`
        : u.username
      out.push({ value: u.username, label })
    }
    return out
  }, [enabled, data])

  return { usernameOptions, isFetching }
}

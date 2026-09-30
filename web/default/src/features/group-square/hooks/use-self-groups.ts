import { useQuery } from '@tanstack/react-query'
import { getSelfGroups } from '../api'

/**
 * 当前用户可见的分组与倍率（GET /api/user/self/groups），可用分组页与侧栏入口共用。
 * inlineError：侧栏在每个控制台页面都会拉它，失败时由页面就地展示，不跳全站 /500。
 */
export function useSelfGroupsQuery() {
  return useQuery({
    queryKey: ['user', 'self', 'groups'] as const,
    queryFn: getSelfGroups,
    staleTime: 60_000,
    meta: { inlineError: true },
  })
}

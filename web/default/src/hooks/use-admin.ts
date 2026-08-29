/**
 * Hook for checking admin privileges
 */
import { useMemo } from 'react'
import { useAuthStore } from '@/stores/auth-store'
import { resolveAdminPermFlags, type AdminPermFlags } from '@/lib/admin-perms'
import { ROLE } from '@/lib/roles'

/**
 * Check if current user has admin privileges
 */
export function useIsAdmin(): boolean {
  const { user } = useAuthStore((state) => state.auth)
  return (user?.role ?? 0) >= ROLE.ADMIN
}

/** Check if current user is the super admin (root) */
export function useIsRoot(): boolean {
  const { user } = useAuthStore((state) => state.auth)
  return (user?.role ?? 0) >= ROLE.SUPER_ADMIN
}

/**
 * 能不能看全站日志。管理员被超级管理员收回「查看日志」权限后，
 * 日志页退化成只看自己的日志（后端 /api/log 管理端也会直接 403）。
 */
export function useCanViewAllLogs(): boolean {
  return useAdminPerms().log_view
}

/**
 * 当前登录者实际生效的管理员细粒度权限。
 * 仅用于隐藏入口，后端每个接口都会再校验一次。
 */
export function useAdminPerms(): AdminPermFlags {
  const { user } = useAuthStore((state) => state.auth)
  const role = user?.role
  const flags = user?.permissions?.admin
  const permList = user?.admin_perms
  // 必须 memo：返回值会进消费方的 useMemo 依赖，每次渲染换新对象会让那些 memo 永远失效
  return useMemo(
    () => resolveAdminPermFlags(role, flags, permList),
    [role, flags, permList]
  )
}

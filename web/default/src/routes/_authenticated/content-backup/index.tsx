import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { resolveAdminPermFlags } from '@/lib/admin-perms'
import { ContentBackup } from '@/features/content-backup'
import { ensureContentBackupModuleEnabled } from '@/features/content-backup/module'

export const Route = createFileRoute('/_authenticated/content-backup/')({
  beforeLoad: async ({ context }) => {
    // 本部署未启用内容备份模块：页面等同不存在
    if (!(await ensureContentBackupModuleEnabled(context.queryClient))) {
      throw redirect({ to: '/404' })
    }
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
    // 默认管理员不含 content_backup.view/manage：无权限直接 403，不渲染页面。
    const perms = resolveAdminPermFlags(
      auth.user.role,
      auth.user.permissions?.admin,
      auth.user.admin_perms
    )
    if (!perms.content_backup_view && !perms.content_backup_manage) {
      throw redirect({ to: '/403' })
    }
  },
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>): { requestId?: string } => {
    const requestId =
      typeof search.requestId === 'string' ? search.requestId : undefined
    return { requestId }
  },
})

function RouteComponent() {
  const { requestId } = Route.useSearch()
  return <ContentBackup requestId={requestId} />
}

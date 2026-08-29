import z from 'zod'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { resolveAdminPermFlags } from '@/lib/admin-perms'
import { ROLE } from '@/lib/roles'
import { Users } from '@/features/users'

const usersSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(10),
  filter: z.string().optional().catch(''),
  status: z
    .array(z.enum(['1', '2']))
    .optional()
    .catch([]),
  role: z
    .array(z.enum(['1', '10', '100']))
    .optional()
    .catch([]),
  group: z.string().optional().catch(''),
  edit: z.number().optional().catch(undefined),
})

export const Route = createFileRoute('/_authenticated/users/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()

    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({
        to: '/403',
      })
    }
    // 「调整额度」也需要用户列表：任一权限即可进页面，页内动作再各自收窄
    const perms = resolveAdminPermFlags(
      auth.user.role,
      auth.user.permissions?.admin,
      auth.user.admin_perms
    )
    if (!perms.user_manage && !perms.quota_grant) {
      throw redirect({
        to: '/403',
      })
    }
  },
  validateSearch: usersSearchSchema,
  component: Users,
})

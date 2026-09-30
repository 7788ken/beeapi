import z from 'zod'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { resolveAdminPermFlags } from '@/lib/admin-perms'
import { Redemptions } from '@/features/redemption-codes'
import { REDEMPTION_STATUS_VALUES } from '@/features/redemption-codes/constants'

const redemptionsSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(10),
  filter: z.string().optional().catch(''),
  status: z.array(z.enum(REDEMPTION_STATUS_VALUES)).optional().catch([]),
})

export const Route = createFileRoute('/_authenticated/redemption-codes/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()

    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({
        to: '/403',
      })
    }
    const perms = resolveAdminPermFlags(
      auth.user.role,
      auth.user.permissions?.admin,
      auth.user.admin_perms
    )
    if (!perms.redemption_manage) {
      throw redirect({
        to: '/403',
      })
    }
  },
  validateSearch: redemptionsSearchSchema,
  component: Redemptions,
})

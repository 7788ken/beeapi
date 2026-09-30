import { z } from 'zod'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { resolveAdminPermFlags } from '@/lib/admin-perms'
import { IQTest } from '@/features/iq-test'

export const Route = createFileRoute('/_authenticated/iq-test/')({
  validateSearch: z.object({
    channel_id: z.number().int().positive().optional().catch(undefined),
  }),
  beforeLoad: () => {
    const user = useAuthStore.getState().auth.user
    const perms = resolveAdminPermFlags(
      user?.role,
      user?.permissions?.admin,
      user?.admin_perms
    )
    if (!perms.channel_metrics || (!perms.channel_view && !perms.channel_edit))
      throw redirect({ to: '/403' })
  },
  component: IQTest,
})

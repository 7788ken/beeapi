import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { resolveAdminPermFlags } from '@/lib/admin-perms'
import { Subscriptions } from '@/features/subscriptions'

export const Route = createFileRoute('/_authenticated/subscriptions/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
    const perms = resolveAdminPermFlags(
      auth.user.role,
      auth.user.permissions?.admin,
      auth.user.admin_perms
    )
    if (!perms.subscription_manage) {
      throw redirect({ to: '/403' })
    }
  },
  component: Subscriptions,
})

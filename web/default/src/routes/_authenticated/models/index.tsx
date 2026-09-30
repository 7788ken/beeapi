import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { resolveAdminPermFlags } from '@/lib/admin-perms'
import { MODELS_DEFAULT_SECTION } from '@/features/models/section-registry'

export const Route = createFileRoute('/_authenticated/models/')({
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
    if (!perms.model_view) {
      throw redirect({
        to: '/403',
      })
    }

    throw redirect({
      to: '/models/$section',
      params: { section: MODELS_DEFAULT_SECTION },
    })
  },
})

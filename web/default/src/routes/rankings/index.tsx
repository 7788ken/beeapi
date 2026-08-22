import z from 'zod'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { Rankings } from '@/features/rankings'
import { getModuleAccess } from '@/lib/nav-modules'
import { useAuthStore } from '@/stores/auth-store'

const rankingsSearchSchema = z.object({
  period: z
    .enum(['today', 'week', 'month', 'year', 'all'])
    .optional()
    .catch(undefined),
})

export const Route = createFileRoute('/rankings/')({
  validateSearch: rankingsSearchSchema,
  beforeLoad: () => {
    const access = getModuleAccess('rankings')
    if (!access.enabled) {
      throw redirect({ to: '/' })
    }
    if (access.requireAuth) {
      const { auth } = useAuthStore.getState()
      if (!auth.user) {
        throw redirect({ to: '/sign-in', search: { redirect: '/rankings' } })
      }
    }
  },
  component: Rankings,
})

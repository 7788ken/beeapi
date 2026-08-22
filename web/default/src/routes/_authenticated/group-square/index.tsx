import { createFileRoute, redirect } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { GroupSquare } from '@/features/group-square'

export const Route = createFileRoute('/_authenticated/group-square/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user) {
      throw redirect({ to: '/sign-in' })
    }
  },
  component: GroupSquare,
})

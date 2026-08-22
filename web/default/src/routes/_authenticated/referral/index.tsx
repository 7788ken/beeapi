import { createFileRoute } from '@tanstack/react-router'
import { Referral } from '@/features/referral'

export const Route = createFileRoute('/_authenticated/referral/')({
  component: Referral,
})

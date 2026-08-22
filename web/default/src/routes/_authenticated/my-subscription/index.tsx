import { createFileRoute } from '@tanstack/react-router'
import { MySubscription } from '@/features/my-subscription'

export const Route = createFileRoute('/_authenticated/my-subscription/')({
  component: MySubscription,
})

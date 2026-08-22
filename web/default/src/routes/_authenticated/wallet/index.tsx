import { z } from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { Wallet } from '@/features/wallet'

const walletSearchSchema = z.object({
  show_history: z.boolean().optional(),
  pay_success: z.boolean().optional(),
})

export const Route = createFileRoute('/_authenticated/wallet/')({
  component: RouteComponent,
  validateSearch: walletSearchSchema,
})

function RouteComponent() {
  const { show_history, pay_success } = Route.useSearch()
  return (
    <Wallet initialShowHistory={show_history} paymentSuccess={pay_success} />
  )
}

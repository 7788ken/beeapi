import { createFileRoute } from '@tanstack/react-router'
import { CreateCenter } from '@/features/create-center'

export const Route = createFileRoute('/_authenticated/create-center/')({
  component: CreateCenter,
})

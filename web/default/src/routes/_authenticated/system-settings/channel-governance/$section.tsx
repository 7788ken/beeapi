import { createFileRoute, redirect } from '@tanstack/react-router'
import { tabFromSection } from '@/features/system-settings/channel-governance/blocks'

export const Route = createFileRoute(
  '/_authenticated/system-settings/channel-governance/$section'
)({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: '/system-settings/channel-governance',
      hash: tabFromSection(params.section),
      replace: true,
    })
  },
})

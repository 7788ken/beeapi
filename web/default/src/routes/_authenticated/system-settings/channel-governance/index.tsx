import { createFileRoute } from '@tanstack/react-router'
import { ChannelGovernanceSettingsPage } from '@/features/system-settings/channel-governance'

export const Route = createFileRoute(
  '/_authenticated/system-settings/channel-governance/'
)({
  component: ChannelGovernanceSettingsPage,
})

import { createFileRoute, redirect } from '@tanstack/react-router'
import { IntegrationSettings } from '@/features/system-settings/integrations'
import {
  INTEGRATIONS_DEFAULT_SECTION,
  INTEGRATIONS_SECTION_IDS,
} from '@/features/system-settings/integrations/section-registry.tsx'

export const Route = createFileRoute(
  '/_authenticated/system-settings/integrations/$section'
)({
  beforeLoad: ({ params }) => {
    const validSections = INTEGRATIONS_SECTION_IDS as unknown as string[]
    // 内容备份设置已不在公开后台。旧地址与其它不存在的分区一样回到默认分区。
    if (!validSections.includes(params.section)) {
      throw redirect({
        to: '/system-settings/integrations/$section',
        params: { section: INTEGRATIONS_DEFAULT_SECTION },
      })
    }
  },
  component: IntegrationSettings,
})

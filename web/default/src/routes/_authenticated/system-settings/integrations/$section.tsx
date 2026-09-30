import { createFileRoute, redirect } from '@tanstack/react-router'
import { ensureContentBackupModuleEnabled } from '@/features/content-backup/module'
import { IntegrationSettings } from '@/features/system-settings/integrations'
import {
  INTEGRATIONS_DEFAULT_SECTION,
  INTEGRATIONS_SECTION_IDS,
} from '@/features/system-settings/integrations/section-registry.tsx'

export const Route = createFileRoute(
  '/_authenticated/system-settings/integrations/$section'
)({
  beforeLoad: async ({ context, params }) => {
    const validSections = INTEGRATIONS_SECTION_IDS as unknown as string[]
    // 内容备份模块未启用时，该分区与不存在的分区同样处理
    const hiddenSection =
      params.section === 'content-backup' &&
      !(await ensureContentBackupModuleEnabled(context.queryClient))
    if (!validSections.includes(params.section) || hiddenSection) {
      throw redirect({
        to: '/system-settings/integrations/$section',
        params: { section: INTEGRATIONS_DEFAULT_SECTION },
      })
    }
  },
  component: IntegrationSettings,
})

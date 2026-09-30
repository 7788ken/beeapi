import { type QueryClient } from '@tanstack/react-query'
import { statusQueryOptions, useStatus } from '@/hooks/use-status'
import type { NavGroup } from '@/components/layout/types'
import type { SystemStatus } from '@/features/auth/types'

// 内容备份模块默认开启；部署环境变量 CONTENT_BACKUP_MODULE=off 时后端不注册 /api/content_backup/*、
// 前端隐藏所有入口。依据 /api/status 的 content_backup_module_enabled，仅明确为 false 才算关闭
function isContentBackupModuleEnabled(status: SystemStatus | null): boolean {
  return status?.content_backup_module_enabled !== false
}

export function useContentBackupModuleEnabled(): boolean {
  const { status } = useStatus()
  return isContentBackupModuleEnabled(status)
}

/** 路由守卫用：状态未缓存或已过期时现取 /api/status */
export async function ensureContentBackupModuleEnabled(
  queryClient: QueryClient
): Promise<boolean> {
  const status = await queryClient.fetchQuery(statusQueryOptions)
  return isContentBackupModuleEnabled(status)
}

const CONTENT_BACKUP_NAV_URL = '/content-backup'
const CONTENT_BACKUP_SETTINGS_NAV_URL =
  '/system-settings/integrations/content-backup'

export function withoutContentBackupNav(groups: NavGroup[]): NavGroup[] {
  return groups.map((group) => ({
    ...group,
    items: group.items
      .filter((item) => item.url !== CONTENT_BACKUP_NAV_URL)
      .map((item) =>
        item.items
          ? {
              ...item,
              items: item.items.filter(
                (subItem) => subItem.url !== CONTENT_BACKUP_SETTINGS_NAV_URL
              ),
            }
          : item
      ),
  }))
}

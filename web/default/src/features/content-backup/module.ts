import { type QueryClient } from '@tanstack/react-query'
import { statusQueryOptions, useStatus } from '@/hooks/use-status'
import type { NavGroup } from '@/components/layout/types'
import type { SystemStatus } from '@/features/auth/types'

// 公开后台不再挂内容备份入口。侧栏和命令面板始终摘掉这两条地址。
// content_backup_module_enabled 只表示采集模块是否在跑，不再决定菜单是否出现。
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

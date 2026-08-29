import { useMemo } from 'react'
import { useLocation } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { useLayout } from '@/context/layout-provider'
import { useAdminPerms } from '@/hooks/use-admin'
import { useSidebarConfig } from '@/hooks/use-sidebar-config'
import { useSidebarData } from '@/hooks/use-sidebar-data'
import {
  Sidebar,
  SidebarContent,
  SidebarHeader,
  SidebarRail,
} from '@/components/ui/sidebar'
import { getNavGroupsForPath } from '../lib/workspace-registry'
import { NavGroup } from './nav-group'
import { WorkspaceSwitcher } from './workspace-switcher'

/**
 * Application sidebar component
 * Fetches corresponding navigation menu from workspace registry based on current path
 * Dynamically filters navigation items based on backend SidebarModulesAdmin configuration
 *
 * Automatically matches workspace configuration for current path through workspace registry system
 * Adding new workspaces only requires registration in workspace-registry.ts
 */
export function AppSidebar() {
  const { t } = useTranslation()
  const { collapsible, variant } = useLayout()
  const { pathname } = useLocation()
  const userRole = useAuthStore((state) => state.auth.user?.role)
  const adminPerms = useAdminPerms()
  const sidebarData = useSidebarData()

  // Get navigation group configuration corresponding to current path from workspace registry
  // If workspace has its own nav groups, prepend them before the default nav groups
  const workspaceNavGroups = getNavGroupsForPath(pathname, t)
  const allNavGroups = workspaceNavGroups
    ? [...workspaceNavGroups, ...sidebarData.navGroups]
    : sidebarData.navGroups

  // Filter sidebar navigation items based on backend configuration
  const configFilteredNavGroups = useSidebarConfig(allNavGroups)

  // Filter navigation groups based on user role
  // Non-Admin users cannot see Admin navigation group
  // 管理员再按超级管理员配置的细粒度权限收窄入口（后端接口自己也会校验一次）
  const currentNavGroups = useMemo(() => {
    const isAdmin = Boolean(userRole && userRole >= ROLE.ADMIN)
    return configFilteredNavGroups
      .map((group) => {
        if (group.id !== 'admin' || !isAdmin) return group
        return {
          ...group,
          items: group.items.filter((item) => {
            const url = 'url' in item ? item.url : undefined
            if (url === '/channels') return adminPerms.channel_view
            if (url === '/users')
              return adminPerms.user_manage || adminPerms.quota_grant
            return true
          }),
        }
      })
      .filter((group) => {
        if (group.id === 'admin') {
          return isAdmin && group.items.length > 0
        }
        return true
      })
  }, [configFilteredNavGroups, userRole, adminPerms])

  return (
    <Sidebar collapsible={collapsible} variant={variant}>
      <SidebarHeader>
        <WorkspaceSwitcher workspaces={sidebarData.workspaces} />
      </SidebarHeader>
      <SidebarContent>
        {currentNavGroups.map((props) => {
          const key = props.id || props.title
          return <NavGroup key={key} {...props} />
        })}
      </SidebarContent>
      <SidebarRail />
    </Sidebar>
  )
}

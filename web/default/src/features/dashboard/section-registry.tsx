import type { TFunction } from 'i18next'
import { createSectionRegistry } from '@/features/system-settings/utils/section-registry'

/**
 * Dashboard page section definitions
 */
const DASHBOARD_SECTIONS = [
  {
    id: 'overview',
    titleKey: 'Overview',
    descriptionKey: 'View dashboard overview and statistics',
    build: () => null,
  },
  {
    id: 'models',
    titleKey: 'Model Call Analytics',
    descriptionKey: 'View model call count analytics and charts',
    build: () => null,
  },
  {
    id: 'users',
    titleKey: 'User Analytics',
    descriptionKey: 'View user consumption statistics and charts',
    adminOnly: true,
    build: () => null,
  },
  {
    id: 'groups',
    titleKey: '分组统计',
    descriptionKey: '按用户分组维度查看消耗，支持订阅抵扣 / 余额扣费切换',
    adminOnly: true,
    build: () => null,
  },
  {
    id: 'runtime',
    titleKey: 'Runtime',
    descriptionKey: 'Real-time top 10 over the past 5 minutes (users / groups / channels / pairs)',
    adminOnly: true,
    build: () => null,
  },
  {
    id: 'channels',
    titleKey: 'Channel Analytics',
    descriptionKey: 'Top channels grouped by model type with consumption, RPM, and trend charts',
    adminOnly: true,
    build: () => null,
  },
  {
    id: 'reconcile',
    titleKey: 'Reconciliation',
    descriptionKey: 'Daily per-channel success / failure / timeout / cost for reconciling upstream bills',
    adminOnly: true,
    build: () => null,
  },
] as const

export type DashboardSectionId = (typeof DASHBOARD_SECTIONS)[number]['id']

const ADMIN_ONLY_SECTIONS = new Set<string>(['users', 'groups', 'runtime', 'channels', 'reconcile'])

const dashboardRegistry = createSectionRegistry<
  DashboardSectionId,
  Record<string, never>,
  []
>({
  sections: DASHBOARD_SECTIONS,
  defaultSection: 'overview',
  basePath: '/dashboard',
  urlStyle: 'path',
})

export const DASHBOARD_SECTION_IDS = dashboardRegistry.sectionIds
export const DASHBOARD_DEFAULT_SECTION = dashboardRegistry.defaultSection

export function getDashboardSectionNavItems(
  t: TFunction,
  options?: { isAdmin?: boolean }
) {
  const all = dashboardRegistry.getSectionNavItems(t)
  if (options?.isAdmin) return all
  return all.filter(
    (_, idx) => !ADMIN_ONLY_SECTIONS.has(DASHBOARD_SECTIONS[idx].id)
  )
}

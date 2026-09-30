import { QueryClient } from '@tanstack/react-query'
// @ts-expect-error -- bun:test 的类型未随仓库安装，测试由 bun 运行器执行
import { describe, expect, test } from 'bun:test'
import type { NavGroup } from '@/components/layout/types'
import type { SystemStatus } from '@/features/auth/types'
import {
  ensureContentBackupModuleEnabled,
  withoutContentBackupNav,
} from './module.ts'

// 结构与真实侧边栏一致：默认工作区的管理员分组 + 系统设置工作区的集成分区
function buildNavGroups(): NavGroup[] {
  return [
    {
      id: 'chat',
      title: 'Chat',
      items: [
        { title: 'Playground', url: '/playground' },
        { title: 'Chat presets', type: 'chat-presets' },
      ],
    },
    {
      id: 'admin',
      title: 'Admin',
      items: [
        { title: 'Channels', url: '/channels' },
        { title: 'Content backup', url: '/content-backup' },
        {
          title: 'System Settings',
          url: '/system-settings/general',
          activeUrls: ['/system-settings'],
        },
      ],
    },
    {
      id: 'system-administration',
      title: 'System Administration',
      items: [
        {
          title: 'General',
          items: [
            { title: 'System Info', url: '/system-settings/general/info' },
          ],
        },
        {
          title: 'Channel Governance',
          url: '/system-settings/channel-governance',
        },
        {
          title: 'Integrations',
          items: [
            {
              title: 'Payment Gateway',
              url: '/system-settings/integrations/payment',
            },
            {
              title: 'Content backup',
              url: '/system-settings/integrations/content-backup',
            },
            {
              title: 'Sub-Site Sync',
              url: '/system-settings/integrations/sub-site-sync',
            },
          ],
        },
      ],
    },
  ]
}

function findGroup(groups: NavGroup[], id: string): NavGroup {
  const group = groups.find((g) => g.id === id)
  if (!group) throw new Error(`nav group ${id} missing`)
  return group
}

describe('withoutContentBackupNav', () => {
  test('removes the top-level /content-backup page entry', () => {
    const admin = findGroup(withoutContentBackupNav(buildNavGroups()), 'admin')
    expect(admin.items.map((item) => item.url)).toEqual([
      '/channels',
      '/system-settings/general',
    ])
  })

  test('removes the integrations content-backup section but keeps its sibling sections', () => {
    const settings = findGroup(
      withoutContentBackupNav(buildNavGroups()),
      'system-administration'
    )
    const integrations = settings.items.find(
      (item) => item.title === 'Integrations'
    )
    expect(integrations?.items?.map((subItem) => subItem.url)).toEqual([
      '/system-settings/integrations/payment',
      '/system-settings/integrations/sub-site-sync',
    ])
  })

  test('leaves everything else unchanged and does not mutate the input', () => {
    const input = buildNavGroups()
    const result = withoutContentBackupNav(input)

    // 期望值 = 原导航只少这两项
    const expected = buildNavGroups()
    findGroup(expected, 'admin').items.splice(1, 1)
    const integrations = findGroup(expected, 'system-administration').items[2]
    if (!integrations.items) throw new Error('Integrations must be collapsible')
    integrations.items.splice(1, 1)

    expect(result).toEqual(expected)
    expect(input).toEqual(buildNavGroups())
  })
})

// 默认开启：只有 /api/status 明确下发 false 才算关闭；旧后端/旧缓存没有该键视为开启
describe('ensureContentBackupModuleEnabled', () => {
  async function resolveWithStatus(status: SystemStatus): Promise<boolean> {
    const queryClient = new QueryClient()
    // 与 useStatus 共用 ['status'] 缓存；数据新鲜时守卫直接读缓存、不发请求
    queryClient.setQueryData(['status'], status)
    try {
      return await ensureContentBackupModuleEnabled(queryClient)
    } finally {
      queryClient.clear()
    }
  }

  test('content_backup_module_enabled=true => enabled', async () => {
    expect(
      await resolveWithStatus({ content_backup_module_enabled: true })
    ).toBe(true)
  })

  test('content_backup_module_enabled=false => disabled', async () => {
    expect(
      await resolveWithStatus({ content_backup_module_enabled: false })
    ).toBe(false)
  })

  test('key absent (older backend or cached status) => enabled', async () => {
    expect(await resolveWithStatus({ system_name: 'New API' })).toBe(true)
  })
})

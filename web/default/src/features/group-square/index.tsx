import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Skeleton } from '@/components/ui/skeleton'
import { SectionPageLayout } from '@/components/layout'
import { EmptyState } from '@/components/empty-state'
import { getSelfGroups } from './api'
import { GroupCard } from './components/group-card'
import { useGroupUptimeSeries } from './hooks/use-group-uptime'
import {
  PLATFORMS,
  OTHER_PLATFORM,
  classifyGroup,
  renderPlatformIcon,
  type Platform,
} from './lib/classify'
import type { GroupRecord } from './types'

interface PlatformSection {
  key: string
  labelKey: string
  toneClass: string
  icon: React.ReactNode
  groups: GroupRecord[]
}

// 分组广场不展示的分组（精确匹配）。default 是所有用户的兜底分组，卡片没有挑选价值；
// 只在本页渲染入口过滤——/api/user/self/groups 本身不动，Keys 分组选择、Playground、
// 日志筛选等共用该接口的地方仍能看到并选用 default。
const HIDDEN_GROUPS = new Set(['default'])

function sortByRatioThenName(a: GroupRecord, b: GroupRecord) {
  if (!Number.isNaN(a.ratio) && !Number.isNaN(b.ratio) && a.ratio !== b.ratio) {
    return a.ratio - b.ratio
  }
  return a.name.localeCompare(b.name)
}

export function GroupSquare() {
  const { t } = useTranslation()

  const { data, isLoading, isError } = useQuery({
    queryKey: ['user', 'self', 'groups'] as const,
    queryFn: async () => {
      const res = await getSelfGroups()
      return res?.data ?? {}
    },
    staleTime: 60_000,
  })

  const uptimeSeries = useGroupUptimeSeries()

  const groups = useMemo<GroupRecord[]>(() => {
    if (!data) return []
    return Object.entries(data)
      .filter(([name]) => !HIDDEN_GROUPS.has(name))
      .map(([name, info]) => ({
        name,
        ratio: Number(info?.ratio ?? 0),
        desc: info?.desc ?? '',
      }))
  }, [data])

  const sections = useMemo<PlatformSection[]>(() => {
    const buckets = new Map<string, GroupRecord[]>()
    for (const g of groups) {
      const k = classifyGroup(g.name, g.desc)
      if (!buckets.has(k)) buckets.set(k, [])
      buckets.get(k)!.push(g)
    }
    const out: PlatformSection[] = []
    for (const p of PLATFORMS as Platform[]) {
      const list = buckets.get(p.key)
      if (list?.length) {
        out.push({
          key: p.key,
          labelKey: p.labelKey,
          toneClass: p.toneClass,
          icon: renderPlatformIcon(p),
          groups: list.sort(sortByRatioThenName),
        })
      }
    }
    const otherList = buckets.get(OTHER_PLATFORM.key)
    if (otherList?.length) {
      out.push({
        key: OTHER_PLATFORM.key,
        labelKey: OTHER_PLATFORM.labelKey,
        toneClass: OTHER_PLATFORM.toneClass,
        icon: <span className='text-xs font-semibold'>·</span>,
        groups: otherList.sort(sortByRatioThenName),
      })
    }
    return out
  }, [groups])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Group Square')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Pick a group to create an API key for it')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        {isLoading ? (
          <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4'>
            {Array.from({ length: 8 }).map((_, i) => (
              <Skeleton key={i} className='h-44 rounded-2xl' />
            ))}
          </div>
        ) : isError ? (
          <EmptyState
            title={t('Failed to load groups')}
            description={t('Please try again later')}
          />
        ) : sections.length === 0 ? (
          <EmptyState
            title={t('No groups available')}
            description={t(
              'Your account currently has no eligible groups, please contact admin'
            )}
          />
        ) : (
          <div className='space-y-8'>
            {sections.map((section) => (
              <section key={section.key}>
                <div className='mb-3 flex items-center gap-2 px-1'>
                  <div
                    className={
                      'flex h-9 w-9 items-center justify-center rounded-xl ' +
                      section.toneClass
                    }
                    aria-label={t(section.labelKey)}
                    title={t(section.labelKey)}
                  >
                    {section.icon}
                  </div>
                  <h2 className='text-base font-semibold'>
                    {t(section.labelKey)}
                  </h2>
                  <span className='text-muted-foreground text-xs'>
                    ({section.groups.length})
                  </span>
                </div>
                <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4'>
                  {section.groups.map((g) => (
                    <GroupCard
                      key={g.name}
                      group={g}
                      toneClass={section.toneClass}
                      uptime={uptimeSeries[g.name]}
                    />
                  ))}
                </div>
              </section>
            ))}
          </div>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

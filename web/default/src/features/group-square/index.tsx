import { useEffect, useMemo, useState } from 'react'
import { Boxes, SearchX } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useNotificationStore } from '@/stores/notification-store'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { surfaceClass } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { EmptyState } from '@/components/empty-state'
import type { FilterChipOption } from '@/components/filter-chip'
import { SectionPageLayout } from '@/components/layout'
import { GroupListHeader, GroupRow } from './components/group-row'
import { GroupToolbar } from './components/group-toolbar'
import { secondaryButtonClass } from './components/styles'
import { useGroupDirectory } from './hooks/use-group-directory'
import { OTHER_PLATFORM, PLATFORMS, renderPlatformIcon } from './lib/classify'
import {
  matchesSearch,
  searchTokens,
  sortGroups,
  type GroupSortKey,
} from './lib/directory'

const ALL = 'all'
const CHIP_ICON_SIZE = 14

function ListSkeleton() {
  return (
    <div className={cn(surfaceClass, 'divide-y overflow-hidden')}>
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className='flex items-center gap-3 px-5 py-4'>
          <Skeleton className='size-9 rounded-lg' />
          <div className='flex-1 space-y-2'>
            <Skeleton className='h-4 w-48' />
            <Skeleton className='h-3 w-3/4' />
          </div>
        </div>
      ))}
    </div>
  )
}

export function GroupSquare() {
  const { t } = useTranslation()
  const directory = useGroupDirectory()
  const markGroupChangesSeen = useNotificationStore(
    (s) => s.markGroupChangesSeen
  )
  const [search, setSearch] = useState('')
  const [category, setCategory] = useState(ALL)
  const [onlyAvailable, setOnlyAvailable] = useState(true)
  const [sort, setSort] = useState<GroupSortKey>('default')

  // 进入页面即算看过当前这批分组变动，侧栏 NEW 角标随之熄灭；行上的 NEW 在展示窗口内保留
  useEffect(() => {
    if (directory.latestChangeAt > 0)
      markGroupChangesSeen(directory.latestChangeAt)
  }, [directory.latestChangeAt, markGroupChangesSeen])

  const tokens = useMemo(() => searchTokens(search), [search])
  const filterAvailable = onlyAvailable && directory.availabilityReady

  const matched = useMemo(
    () =>
      directory.entries.filter(
        (e) =>
          (!filterAvailable || e.availability.status === 'available') &&
          matchesSearch(e.name, e.desc, tokens)
      ),
    [directory.entries, filterAvailable, tokens]
  )

  // 芯片只列全量里出现过的分类（输入搜索词时不跳位），计数跟随搜索与「只看可用」
  const categories = useMemo<FilterChipOption[]>(() => {
    const countOf = (key: string) =>
      matched.filter((e) => e.platform === key).length
    const present = (key: string) =>
      directory.entries.some((e) => e.platform === key)
    const options: FilterChipOption[] = [
      { value: ALL, label: t('All'), count: matched.length },
    ]
    for (const p of PLATFORMS) {
      if (!present(p.key)) continue
      options.push({
        value: p.key,
        label: t(p.labelKey),
        icon: renderPlatformIcon(p, CHIP_ICON_SIZE),
        count: countOf(p.key),
      })
    }
    if (present(OTHER_PLATFORM.key)) {
      options.push({
        value: OTHER_PLATFORM.key,
        label: t(OTHER_PLATFORM.labelKey),
        icon: <Boxes className='size-3.5' aria-hidden />,
        count: countOf(OTHER_PLATFORM.key),
      })
    }
    return options
  }, [directory.entries, matched, t])

  const rows = useMemo(
    () =>
      sortGroups(
        category === ALL
          ? matched
          : matched.filter((e) => e.platform === category),
        sort
      ),
    [matched, category, sort]
  )

  // 列表为空可能只是因为「只看可用」（近 1～2 小时没请求），清除时一并关掉，不留死路
  const resetFilters = () => {
    setSearch('')
    setCategory(ALL)
    setOnlyAvailable(false)
  }

  let body: React.ReactNode
  if (directory.isLoading) {
    body = <ListSkeleton />
  } else if (directory.isError) {
    body = (
      <EmptyState
        title={t('Failed to load groups')}
        description={t('Please try again later')}
      />
    )
  } else if (directory.entries.length === 0) {
    body = (
      <EmptyState
        title={t('No groups available')}
        description={t(
          'Your account currently has no eligible groups, please contact admin'
        )}
      />
    )
  } else {
    body = (
      <div className={cn(surfaceClass, '@container overflow-hidden')}>
        <GroupToolbar
          search={search}
          onSearchChange={setSearch}
          onlyAvailable={onlyAvailable}
          onOnlyAvailableChange={setOnlyAvailable}
          availabilityReady={directory.availabilityReady}
          sort={sort}
          onSortChange={setSort}
          categories={categories}
          category={category}
          onCategoryChange={setCategory}
        />
        {rows.length === 0 ? (
          <div className='flex flex-col items-center gap-3 px-6 py-14 text-center'>
            <SearchX className='text-muted-foreground size-8' aria-hidden />
            <div className='space-y-1'>
              <p className='text-foreground font-medium'>
                {t('No group found.')}
              </p>
              <p className='text-muted-foreground text-sm'>
                {t('Try a different keyword or category')}
              </p>
            </div>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={resetFilters}
              className={secondaryButtonClass}
            >
              {t('Clear filters')}
            </Button>
          </div>
        ) : (
          <>
            <GroupListHeader sort={sort} onSortChange={setSort} />
            <ul className='divide-y'>
              {rows.map((entry) => (
                <GroupRow
                  key={entry.name}
                  entry={entry}
                  tokens={tokens}
                  availabilityPending={directory.availabilityPending}
                />
              ))}
            </ul>
          </>
        )}
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Available Groups')}
        {directory.availableCount !== undefined && (
          <span className='text-muted-foreground ms-2 font-normal tabular-nums'>
            ({directory.availableCount})
          </span>
        )}
      </SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Pick a group to create an API key for it')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>{body}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

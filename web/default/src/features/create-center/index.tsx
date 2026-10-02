import { lazy, Suspense } from 'react'
import { Link, getRouteApi, useNavigate } from '@tanstack/react-router'
import { History, KeyRound, MessageCircle, Palette } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { composerChip } from '@/components/composer-styles'
import { AppHeader, Main } from '@/components/layout'
import { HistoryTab } from './history-tab'
import { ImageTab } from './image-tab'

// Playground 是已有完整组件，直接 lazy 嵌入作为 Chat tab
const LazyPlayground = lazy(() =>
  import('@/features/playground').then((m) => ({ default: m.Playground }))
)

const route = getRouteApi('/_authenticated/create-center/')

type CreateCenterTab = 'chat' | 'image' | 'history'

function isValidTab(v: string | undefined): v is CreateCenterTab {
  return v === 'chat' || v === 'image' || v === 'history'
}

/**
 * 创作中心：顶部一行标题 + 分段切换，下面是整块工作区。
 * 对话、生图两个工作区都是「内容流 + 吸底输入卡」，各自内部滚动，页面本身不出外层滚动条；
 * 背景直接露出控制台的 40px 网格。
 */
export function CreateCenter() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const search = route.useSearch() as { tab?: string }
  const activeTab: CreateCenterTab = isValidTab(search.tab)
    ? search.tab
    : 'image'

  const handleTabChange = (value: string) => {
    if (!isValidTab(value)) return
    navigate({
      to: '/create-center',
      search: { tab: value },
      replace: true,
    })
  }

  return (
    <>
      <AppHeader />
      <Main className='flex h-full flex-col p-0'>
        <div className='flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2 px-4 pt-4 sm:px-6'>
          <h1 className='text-lg font-semibold'>{t('Create Center')}</h1>
          <Tabs value={activeTab} onValueChange={handleTabChange}>
            <TabsList className='h-8'>
              <TabsTrigger value='chat' className='px-2.5 text-xs'>
                <MessageCircle className='size-3.5' />
                {t('Chat')}
              </TabsTrigger>
              <TabsTrigger value='image' className='px-2.5 text-xs'>
                <Palette className='size-3.5' />
                {t('Image')}
              </TabsTrigger>
              <TabsTrigger value='history' className='px-2.5 text-xs'>
                <History className='size-3.5' />
                {t('Midjourney history')}
              </TabsTrigger>
            </TabsList>
          </Tabs>
          <Link
            to='/keys'
            aria-label={t('Manage API keys')}
            className={cn(composerChip, 'ml-auto')}
          >
            <KeyRound className='text-primary size-3.5' />
            <span className='hidden sm:inline'>{t('Manage API keys')}</span>
          </Link>
        </div>

        <div className='min-h-0 flex-1'>
          {activeTab === 'chat' && (
            <Suspense fallback={<TabSkeleton />}>
              <LazyPlayground />
            </Suspense>
          )}
          {activeTab === 'image' && <ImageTab />}
          {activeTab === 'history' && (
            <div className='h-full overflow-auto px-4 py-4 sm:px-6'>
              <HistoryTab />
            </div>
          )}
        </div>
      </Main>
    </>
  )
}

function TabSkeleton() {
  return (
    <div className='mx-auto h-full w-full max-w-3xl space-y-3 p-4'>
      <Skeleton className='h-12 w-full' />
      <Skeleton className='h-96 w-full' />
    </div>
  )
}

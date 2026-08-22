import { lazy, Suspense } from 'react'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { MessageCircle, Palette, History, Sparkles } from 'lucide-react'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Skeleton } from '@/components/ui/skeleton'
import { AppHeader, Main } from '@/components/layout'
import { ImageTab } from './image-tab'
import { HistoryTab } from './history-tab'

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
 * 创作中心整体布局：
 *  - 顶部 tabs bar 固定高度（shrink-0）
 *  - 下方内容区 flex-1 + overflow-hidden（外层不滚动）
 *  - Chat tab：Playground 自带内部 scroll，独占撑满
 *  - Image/History tab：自带 overflow-auto wrapper，内部自然增长
 *  这样保证页面只有一个滚动条（在内容区内部），不会出现 SectionPageLayout 的外层 + tab 内部双 scroll。
 */
export function CreateCenter() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const search = route.useSearch() as { tab?: string }
  const activeTab: CreateCenterTab = isValidTab(search.tab) ? search.tab : 'image'

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
      {/* 标准 AppHeader（breadcrumb / 用户菜单 / 主题切换 等） */}
      <AppHeader />
      <Main className='flex h-full flex-col p-0'>
      {/* Tabs bar */}
      <div className='bg-background/95 supports-[backdrop-filter]:bg-background/60 shrink-0 border-b px-3 py-2.5 backdrop-blur sm:px-4 sm:py-3'>
        <div className='flex items-center gap-2'>
          <Sparkles className='h-4 w-4 text-violet-500' />
          <Tabs value={activeTab} onValueChange={handleTabChange} className='flex-1'>
            <TabsList className='h-auto'>
              <TabsTrigger value='chat' className='gap-1.5'>
                <MessageCircle className='h-3.5 w-3.5' />
                {t('Chat')}
              </TabsTrigger>
              <TabsTrigger value='image' className='gap-1.5'>
                <Palette className='h-3.5 w-3.5' />
                {t('Image')}
              </TabsTrigger>
              <TabsTrigger value='history' className='gap-1.5'>
                <History className='h-3.5 w-3.5' />
                {t('History')}
              </TabsTrigger>
            </TabsList>
          </Tabs>
        </div>
      </div>

      {/* 内容区：外层 overflow-hidden，禁止 SectionPageLayout 风格的双 scroll */}
      <div className='min-h-0 flex-1 overflow-hidden'>
        {activeTab === 'chat' && (
          <Suspense fallback={<TabSkeleton />}>
            {/* Playground 自带 size-full + 内部 scroll，直接撑满即可 */}
            <LazyPlayground />
          </Suspense>
        )}
        {activeTab === 'image' && (
          <div className='h-full overflow-auto px-3 py-3 sm:px-4 sm:py-4'>
            <ImageTab />
          </div>
        )}
        {activeTab === 'history' && (
          <div className='h-full overflow-auto px-3 py-3 sm:px-4 sm:py-4'>
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
    <div className='h-full space-y-3 p-4'>
      <Skeleton className='h-12 w-full' />
      <Skeleton className='h-96 w-full' />
    </div>
  )
}

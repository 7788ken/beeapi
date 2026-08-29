import { useState, useCallback, useMemo, useEffect, useRef, lazy, Suspense, type ReactNode } from 'react'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { RefreshCw, Pause, Play } from 'lucide-react'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { isSidebarModuleEnabled } from '@/lib/nav-modules'
import { cn } from '@/lib/utils'
import { useAdminPerms } from '@/hooks/use-admin'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { SectionPageLayout } from '@/components/layout'
import {
  CardStaggerContainer,
  CardStaggerItem,
  FadeIn,
} from '@/components/page-transition'
import {
  buildDefaultDashboardFilters,
  getSavedChartPreferences,
  saveChartPreferences,
} from './lib'
import { ModelsChartPreferences } from './components/models/models-chart-preferences'
import { ModelsFilter } from './components/models/models-filter-dialog'
import {
  GroupsFilter,
  buildDefaultGroupFilters,
} from './components/groups/groups-filter-dialog'
import { AnnouncementsPanel } from './components/overview/announcements-panel'
import { ApiInfoPanel } from './components/overview/api-info-panel'
import { FAQPanel } from './components/overview/faq-panel'
import { PerformanceHealthPanel } from './components/overview/performance-health-panel'
import { SummaryCards } from './components/overview/summary-cards'
import { UptimePanel } from './components/overview/uptime-panel'
import { DEFAULT_TIME_GRANULARITY } from './constants'
import {
  type DashboardSectionId,
  DASHBOARD_DEFAULT_SECTION,
  DASHBOARD_SECTION_IDS,
} from './section-registry'
import {
  type DashboardChartPreferences,
  type DashboardFilters,
  type GroupDashboardFilters,
  type QuotaDataItem,
} from './types'

const route = getRouteApi('/_authenticated/dashboard/$section')

const LazyLogStatCards = lazy(() =>
  import('./components/models/log-stat-cards').then((m) => ({
    default: m.LogStatCards,
  }))
)

const LazyModelCharts = lazy(() =>
  import('./components/models/model-charts').then((m) => ({
    default: m.ModelCharts,
  }))
)

const LazyConsumptionDistributionChart = lazy(() =>
  import('./components/models/consumption-distribution-chart').then((m) => ({
    default: m.ConsumptionDistributionChart,
  }))
)

const LazyPerformanceOverview = lazy(() =>
  import('./components/models/performance-overview').then((m) => ({
    default: m.PerformanceOverview,
  }))
)

const LazyUserCharts = lazy(() =>
  import('./components/users/user-charts').then((m) => ({
    default: m.UserCharts,
  }))
)

const LazyGroupCharts = lazy(() =>
  import('./components/groups/group-charts').then((m) => ({
    default: m.GroupCharts,
  }))
)

const LazyRuntimePanel = lazy(() =>
  import('./components/runtime/runtime-panel').then((m) => ({
    default: m.RuntimePanel,
  }))
)

const LazyChannelStatsPanel = lazy(() =>
  import('./components/channels/channel-stats-panel').then((m) => ({
    default: m.ChannelStatsPanel,
  }))
)

const LazyReconcilePanel = lazy(() =>
  import('./components/reconcile/reconcile-panel').then((m) => ({
    default: m.ReconcilePanel,
  }))
)

function LogStatCardsFallback() {
  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='divide-border/60 grid grid-cols-2 divide-x sm:grid-cols-3 lg:grid-cols-5'>
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className='px-4 py-3.5 sm:px-5 sm:py-4'>
            <Skeleton className='h-3.5 w-16' />
            <Skeleton className='mt-2 h-7 w-20' />
            <Skeleton className='mt-1.5 h-3.5 w-28' />
          </div>
        ))}
      </div>
    </div>
  )
}

function ModelChartsFallback() {
  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex items-center justify-between border-b px-4 py-3 sm:px-5'>
        <Skeleton className='h-5 w-32' />
        <Skeleton className='h-8 w-72' />
      </div>
      <div className='h-96 p-2'>
        <Skeleton className='h-full w-full' />
      </div>
    </div>
  )
}

function PerformanceOverviewFallback() {
  return (
    <div className='space-y-3 sm:space-y-4'>
      <div className='overflow-hidden rounded-lg border'>
        <div className='divide-border/60 grid grid-cols-2 divide-x sm:grid-cols-4'>
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className='px-3 py-2.5 sm:px-5 sm:py-4'>
              <Skeleton className='h-4 w-24' />
              <Skeleton className='mt-2 h-7 w-20' />
              <Skeleton className='mt-1.5 h-3.5 w-28' />
            </div>
          ))}
        </div>
      </div>
      <div className='overflow-hidden rounded-lg border'>
        <div className='flex items-center justify-between border-b px-4 py-3 sm:px-5'>
          <Skeleton className='h-5 w-40' />
          <Skeleton className='h-4 w-48' />
        </div>
        <Skeleton className='h-44 w-full' />
      </div>
    </div>
  )
}

const SECTION_META: Record<
  DashboardSectionId,
  { titleKey: string; descriptionKey: string }
> = {
  overview: {
    titleKey: 'Overview',
    descriptionKey: 'View dashboard overview and statistics',
  },
  models: {
    titleKey: 'Model Call Analytics',
    descriptionKey: 'View model call count analytics and charts',
  },
  users: {
    titleKey: 'User Analytics',
    descriptionKey: 'View user consumption statistics and charts',
  },
  groups: {
    titleKey: '分组统计',
    descriptionKey: '按用户分组维度查看消耗，支持订阅抵扣 / 余额扣费切换',
  },
  runtime: {
    titleKey: 'Runtime',
    descriptionKey: 'Real-time top 10 over the past 5 minutes (users / groups / channels / pairs)',
  },
  channels: {
    titleKey: 'Channel Analytics',
    descriptionKey: 'Top channels grouped by model type with consumption, RPM, and trend charts',
  },
  reconcile: {
    titleKey: 'Reconciliation',
    descriptionKey: 'Daily per-channel success / failure / timeout / cost for reconciling upstream bills',
  },
}

export function Dashboard() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const params = route.useParams()
  const userRole = useAuthStore((state) => state.auth.user?.role)
  const adminPerms = useAdminPerms()
  const activeSection = (params.section ??
    DASHBOARD_DEFAULT_SECTION) as DashboardSectionId

  const [modelData, setModelData] = useState<QuotaDataItem[]>([])
  const [dataLoading, setDataLoading] = useState(false)
  const [chartPreferences, setChartPreferences] =
    useState<DashboardChartPreferences>(() => getSavedChartPreferences())
  const [modelFilters, setModelFilters] = useState<DashboardFilters>(() =>
    buildDefaultDashboardFilters(getSavedChartPreferences())
  )
  const [groupFilters, setGroupFilters] = useState<GroupDashboardFilters>(() =>
    buildDefaultGroupFilters(getSavedChartPreferences())
  )

  const handleFilterChange = useCallback((filters: DashboardFilters) => {
    setModelFilters(filters)
  }, [])

  const handleResetFilters = useCallback(() => {
    setModelFilters(buildDefaultDashboardFilters(chartPreferences))
  }, [chartPreferences])

  const handleGroupFilterChange = useCallback(
    (filters: GroupDashboardFilters) => {
      setGroupFilters(filters)
    },
    []
  )

  const handleResetGroupFilters = useCallback(() => {
    setGroupFilters(buildDefaultGroupFilters(chartPreferences))
  }, [chartPreferences])

  // 分组面板手动刷新 + 自动刷新（30s）
  const queryClient = useQueryClient()
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [isRefreshing, setIsRefreshing] = useState(false)
  const autoRefreshTimer = useRef<ReturnType<typeof setInterval> | null>(null)

  const refreshGroupData = useCallback(async () => {
    setIsRefreshing(true)
    try {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['dashboard', 'group-quota'] }),
        queryClient.invalidateQueries({ queryKey: ['dashboard', 'group-top-users'] }),
      ])
    } finally {
      // 给用户一个肉眼可见的旋转反馈
      setTimeout(() => setIsRefreshing(false), 300)
    }
  }, [queryClient])

  // 自动刷新 toggle：开启时启动 30s 周期；关闭/切换 section 时立即停。
  useEffect(() => {
    if (autoRefresh && activeSection === 'groups') {
      autoRefreshTimer.current = setInterval(() => {
        void refreshGroupData()
      }, 30_000)
    }
    return () => {
      if (autoRefreshTimer.current) {
        clearInterval(autoRefreshTimer.current)
        autoRefreshTimer.current = null
      }
    }
  }, [autoRefresh, activeSection, refreshGroupData])

  const handleDataUpdate = useCallback(
    (data: QuotaDataItem[], loading: boolean) => {
      setModelData(data)
      setDataLoading(loading)
    },
    []
  )

  const handleChartPreferencesChange = useCallback(
    (preferences: DashboardChartPreferences) => {
      setChartPreferences(preferences)
      setModelFilters(buildDefaultDashboardFilters(preferences))
      saveChartPreferences(preferences)
    },
    []
  )

  const meta = SECTION_META[activeSection] ?? SECTION_META.overview
  const isAdmin = Boolean(userRole && userRole >= ROLE.ADMIN)
  // 渠道分析 / 对账读的是 /api/channel/*，管理员被收回「查看渠道」权限后这两个 tab 会 403
  const canViewChannels = isAdmin && adminPerms.channel_view
  const visibleSections = useMemo(
    () =>
      DASHBOARD_SECTION_IDS.filter((section) => {
        if (section === 'overview') return false
        if (section === 'channels' || section === 'reconcile') {
          return canViewChannels
        }
        if (section === 'users' || section === 'groups') return isAdmin
        return true
      }),
    [isAdmin, canViewChannels]
  )
  const handleSectionChange = useCallback(
    (section: string) => {
      void navigate({
        to: '/dashboard/$section',
        params: { section: section as DashboardSectionId },
      })
    },
    [navigate]
  )
  const showSectionTabs = activeSection !== 'overview' && visibleSections.length > 1
  let sectionActions: ReactNode = null
  if (activeSection === 'models') {
    sectionActions = (
      <>
        <ModelsChartPreferences
          preferences={chartPreferences}
          onPreferencesChange={handleChartPreferencesChange}
        />
        <ModelsFilter
          preferences={chartPreferences}
          onFilterChange={handleFilterChange}
          onReset={handleResetFilters}
        />
      </>
    )
  } else if (activeSection === 'groups') {
    sectionActions = (
      <>
        {/* 手动刷新 */}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant='outline'
              size='sm'
              onClick={() => void refreshGroupData()}
              disabled={isRefreshing}
              aria-label={t('Refresh')}
            >
              <RefreshCw className={cn('h-4 w-4', isRefreshing && 'animate-spin')} />
              <span className='hidden sm:inline ml-1.5'>{t('Refresh')}</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>{t('Refresh group quota data')}</TooltipContent>
        </Tooltip>
        {/* 自动刷新 toggle（30s 周期） */}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant={autoRefresh ? 'default' : 'outline'}
              size='sm'
              onClick={() => setAutoRefresh((v) => !v)}
              aria-label={t('Auto refresh')}
              aria-pressed={autoRefresh}
            >
              {autoRefresh ? (
                <Pause className='h-4 w-4' />
              ) : (
                <Play className='h-4 w-4' />
              )}
              <span className='hidden sm:inline ml-1.5'>
                {autoRefresh ? t('Auto: 30s') : t('Auto refresh')}
              </span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            {autoRefresh
              ? t('Auto refresh enabled (30s). Click to pause.')
              : t('Click to enable auto refresh every 30s.')}
          </TooltipContent>
        </Tooltip>
        <GroupsFilter
          preferences={chartPreferences}
          filters={groupFilters}
          onFilterChange={handleGroupFilterChange}
          onReset={handleResetGroupFilters}
        />
      </>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t(meta.titleKey)}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t(meta.descriptionKey)}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <div className='space-y-3 sm:space-y-4'>
          {activeSection !== 'overview' && (
            <div className='flex flex-wrap items-center justify-between gap-1.5 sm:gap-2'>
              {showSectionTabs ? (
                <Tabs value={activeSection} onValueChange={handleSectionChange}>
                  <TabsList className='h-auto max-w-full flex-wrap justify-start'>
                    {visibleSections.map((section) => (
                      <TabsTrigger key={section} value={section}>
                        {t(SECTION_META[section].titleKey)}
                      </TabsTrigger>
                    ))}
                  </TabsList>
                </Tabs>
              ) : (
                <div />
              )}
              {sectionActions != null && (
                <div className='flex shrink-0 flex-wrap items-center gap-1.5 sm:gap-2'>
                  {sectionActions}
                </div>
              )}
            </div>
          )}
          {activeSection === 'overview' && (
            <>
              <SummaryCards />
              <CardStaggerContainer className='grid grid-cols-1 gap-3 sm:gap-4 lg:grid-cols-2'>
                {isAdmin &&
                  isSidebarModuleEnabled('dashboard', 'performanceHealth') && (
                    <CardStaggerItem className='lg:col-span-2'>
                      <PerformanceHealthPanel />
                    </CardStaggerItem>
                  )}
                <CardStaggerItem>
                  <ApiInfoPanel />
                </CardStaggerItem>
                <CardStaggerItem>
                  <AnnouncementsPanel />
                </CardStaggerItem>
                <CardStaggerItem>
                  <FAQPanel />
                </CardStaggerItem>
                <CardStaggerItem>
                  <UptimePanel />
                </CardStaggerItem>
              </CardStaggerContainer>
            </>
          )}
          {activeSection === 'models' && (
            <>
              <FadeIn>
                <Suspense fallback={<LogStatCardsFallback />}>
                  <LazyLogStatCards
                    filters={modelFilters}
                    onDataUpdate={handleDataUpdate}
                  />
                </Suspense>
              </FadeIn>
              {isAdmin &&
                isSidebarModuleEnabled('dashboard', 'performanceOverview') && (
                  <FadeIn delay={0.1}>
                    <Suspense fallback={<PerformanceOverviewFallback />}>
                      <LazyPerformanceOverview />
                    </Suspense>
                  </FadeIn>
                )}
              <FadeIn delay={0.15}>
                <Suspense fallback={<ModelChartsFallback />}>
                  <LazyConsumptionDistributionChart
                    data={modelData}
                    loading={dataLoading}
                    defaultChartType={
                      chartPreferences.consumptionDistributionChart
                    }
                    timeGranularity={
                      modelFilters.time_granularity || DEFAULT_TIME_GRANULARITY
                    }
                  />
                </Suspense>
              </FadeIn>
              <FadeIn delay={0.2}>
                <Suspense fallback={<ModelChartsFallback />}>
                  <LazyModelCharts
                    data={modelData}
                    loading={dataLoading}
                    defaultChartTab={chartPreferences.modelAnalyticsChart}
                    timeGranularity={
                      modelFilters.time_granularity || DEFAULT_TIME_GRANULARITY
                    }
                  />
                </Suspense>
              </FadeIn>
            </>
          )}
          {activeSection === 'users' && (
            <FadeIn>
              <Suspense fallback={<ModelChartsFallback />}>
                <LazyUserCharts />
              </Suspense>
            </FadeIn>
          )}
          {activeSection === 'groups' && (
            <FadeIn>
              <Suspense fallback={<ModelChartsFallback />}>
                <LazyGroupCharts filters={groupFilters} />
              </Suspense>
            </FadeIn>
          )}
          {activeSection === 'runtime' && (
            <FadeIn>
              <Suspense fallback={<ModelChartsFallback />}>
                <LazyRuntimePanel />
              </Suspense>
            </FadeIn>
          )}
          {activeSection === 'channels' && (
            <FadeIn>
              <Suspense fallback={<ModelChartsFallback />}>
                <LazyChannelStatsPanel />
              </Suspense>
            </FadeIn>
          )}
          {activeSection === 'reconcile' && (
            <FadeIn>
              <Suspense fallback={<ModelChartsFallback />}>
                <LazyReconcilePanel />
              </Suspense>
            </FadeIn>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

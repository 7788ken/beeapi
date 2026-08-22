import { useState, lazy, Suspense } from 'react'
import { Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { SectionPageLayout } from '@/components/layout'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SubscriptionsDialogs } from './components/subscriptions-dialogs'
import { SubscriptionsPrimaryButtons } from './components/subscriptions-primary-buttons'
import { SubscriptionsProvider } from './components/subscriptions-provider'
import { SubscriptionsTable } from './components/subscriptions-table'

type SubscriptionTab = 'plans' | 'assigned' | 'stats'

const TABS: { value: SubscriptionTab; label: string }[] = [
  { value: 'plans', label: '订阅计划' },
  { value: 'assigned', label: '已分配订阅' },
  { value: 'stats', label: '数据统计' },
]

const LazyUserSubscriptionsTable = lazy(() =>
  import('./components/user-subscriptions-table').then((m) => ({
    default: m.UserSubscriptionsTable,
  }))
)

const LazyGroupBudgetStats = lazy(() =>
  import('./components/group-budget-stats').then((m) => ({
    default: m.GroupBudgetStats,
  }))
)

function Fallback() {
  return <Skeleton className='h-64 w-full' />
}

export function Subscriptions() {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState<SubscriptionTab>('plans')

  return (
    <SubscriptionsProvider>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Subscription Management')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {t('Manage subscription plan creation, pricing and status')}
        </SectionPageLayout.Description>
        <SectionPageLayout.Actions>
          <div className='flex items-center gap-2'>
            {activeTab === 'plans' ? (
              <>
                <Alert variant='default' className='hidden px-3 py-2 sm:flex'>
                  <Info className='h-4 w-4' />
                  <AlertDescription className='text-xs'>
                    {t(
                      'Stripe/Creem requires creating products on the third-party platform and entering the ID'
                    )}
                  </AlertDescription>
                </Alert>
                <SubscriptionsPrimaryButtons />
              </>
            ) : null}
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='space-y-3 sm:space-y-4'>
            <Tabs
              value={activeTab}
              onValueChange={(v) => setActiveTab(v as SubscriptionTab)}
            >
              <TabsList className='h-auto max-w-full flex-wrap justify-start'>
                {TABS.map((tab) => (
                  <TabsTrigger key={tab.value} value={tab.value}>
                    {tab.label}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>

            {activeTab === 'plans' && <SubscriptionsTable />}
            {activeTab === 'assigned' && (
              <Suspense fallback={<Fallback />}>
                <LazyUserSubscriptionsTable />
              </Suspense>
            )}
            {activeTab === 'stats' && (
              <Suspense fallback={<Fallback />}>
                <LazyGroupBudgetStats />
              </Suspense>
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <SubscriptionsDialogs />
    </SubscriptionsProvider>
  )
}

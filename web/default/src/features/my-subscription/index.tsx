import { useTranslation } from 'react-i18next'
import { SectionPageLayout } from '@/components/layout'
import { useTopupInfo } from '@/features/wallet/hooks'
import { SubscriptionPlansCard } from './components/subscription-plans-card'

export function MySubscription() {
  const { t } = useTranslation()
  const { topupInfo } = useTopupInfo()

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Subscription Plans')}</SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t('Subscribe to a plan for model access')}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
          <SubscriptionPlansCard topupInfo={topupInfo} />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

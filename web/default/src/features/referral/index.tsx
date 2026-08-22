import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { getSelf } from '@/lib/api'
import { SectionPageLayout } from '@/components/layout'
import { AffiliateRewardsCard } from './components/affiliate-rewards-card'
import { InviteeList } from './components/invitee-list'
import { TransferDialog } from './components/transfer-dialog'
import { useAffiliate, useInvitees } from './hooks'
import type { ReferralUser } from './types'

export function Referral() {
  const { t } = useTranslation()
  const [user, setUser] = useState<ReferralUser | null>(null)
  const [userLoading, setUserLoading] = useState(true)
  const [transferDialogOpen, setTransferDialogOpen] = useState(false)

  const {
    affiliateLink,
    loading: affiliateLoading,
    transferQuota,
    transferring,
  } = useAffiliate()
  const {
    data: inviteesData,
    loading: inviteesLoading,
    refetch: refetchInvitees,
  } = useInvitees()

  const fetchUser = useCallback(async () => {
    try {
      setUserLoading(true)
      const response = await getSelf()
      if (response.success && response.data) {
        setUser(response.data as ReferralUser)
      }
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Failed to fetch user data:', error)
    } finally {
      setUserLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchUser()
  }, [fetchUser])

  const handleTransfer = async (amount: number) => {
    const success = await transferQuota(amount)
    if (success) {
      await Promise.all([fetchUser(), refetchInvitees()])
    }
    return success
  }

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Referral Program')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {t(
            'Earn rewards when your referrals add funds. Transfer accumulated rewards to your balance anytime.'
          )}
        </SectionPageLayout.Description>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
            <AffiliateRewardsCard
              user={user}
              affiliateLink={affiliateLink}
              onTransfer={() => setTransferDialogOpen(true)}
              loading={userLoading || affiliateLoading}
            />
            <InviteeList data={inviteesData} loading={inviteesLoading} />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <TransferDialog
        open={transferDialogOpen}
        onOpenChange={setTransferDialogOpen}
        onConfirm={handleTransfer}
        availableQuota={user?.aff_quota ?? 0}
        transferring={transferring}
      />
    </>
  )
}

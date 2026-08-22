import { useTranslation } from 'react-i18next'
import { formatQuota } from '@/lib/format'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { CopyButton } from '@/components/copy-button'
import type { ReferralUser } from '../types'

interface AffiliateRewardsCardProps {
  user: ReferralUser | null
  affiliateLink: string
  onTransfer: () => void
  loading?: boolean
}

export function AffiliateRewardsCard({
  user,
  affiliateLink,
  onTransfer,
  loading,
}: AffiliateRewardsCardProps) {
  const { t } = useTranslation()

  if (loading) {
    return (
      <Card>
        <CardContent className='space-y-5 p-4 sm:p-6'>
          <div className='grid grid-cols-3 gap-4'>
            {Array.from({ length: 3 }).map((_, i) => (
              <div key={i} className='space-y-2'>
                <Skeleton className='h-3 w-16' />
                <Skeleton className='h-7 w-24' />
              </div>
            ))}
          </div>
          <Separator />
          <div className='flex items-center gap-2'>
            <Skeleton className='h-10 flex-1 rounded-lg' />
            <Skeleton className='h-10 w-10 rounded-lg' />
          </div>
        </CardContent>
      </Card>
    )
  }

  const hasRewards = (user?.aff_quota ?? 0) > 0

  const stats: Array<[string, string]> = [
    [t('Pending'), formatQuota(user?.aff_quota ?? 0)],
    [t('Total Earned'), formatQuota(user?.aff_history_quota ?? 0)],
    [t('Invites'), String(user?.aff_count ?? 0)],
  ]

  return (
    <Card>
      <CardContent className='space-y-5 p-4 sm:p-6'>
        <div className='grid grid-cols-3 gap-4'>
          {stats.map(([label, value]) => (
            <div key={label} className='min-w-0'>
              <div className='text-muted-foreground truncate text-[10px] font-medium tracking-wider uppercase'>
                {label}
              </div>
              <div className='mt-1 truncate text-xl font-semibold tabular-nums sm:text-2xl'>
                {value}
              </div>
            </div>
          ))}
        </div>

        <Separator />

        <div className='flex flex-wrap items-center gap-2'>
          <Input
            value={affiliateLink}
            readOnly
            className='border-muted bg-background/70 h-10 min-w-0 flex-1 font-mono text-xs sm:text-sm'
          />
          <CopyButton
            value={affiliateLink}
            variant='outline'
            className='size-10 shrink-0'
            iconClassName='size-4'
            tooltip={t('Copy referral link')}
            aria-label={t('Copy referral link')}
          />
          {hasRewards && (
            <Button onClick={onTransfer} className='h-10 shrink-0 px-4'>
              {t('Transfer to Balance')}
            </Button>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

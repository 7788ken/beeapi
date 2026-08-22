import { useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Coins, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { cn } from '@/lib/utils'
import { formatQuota } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Progress } from '@/components/ui/progress'
import { Separator } from '@/components/ui/separator'
import { ScrollArea } from '@/components/ui/scroll-area'
import { getSelfSubscriptionFull } from '@/features/subscriptions/api'
import type {
  UserSubscription,
  UserSubscriptionRecord,
} from '@/features/subscriptions/types'

const QUERY_KEY = ['subscription', 'self', 'summary'] as const
const REFRESH_MS = 60_000

function progressTone(pct: number): 'ok' | 'warn' | 'danger' {
  if (pct < 60) return 'ok'
  if (pct < 85) return 'warn'
  return 'danger'
}

const TONE_INDICATOR: Record<'ok' | 'warn' | 'danger', string> = {
  ok: '[&>[data-slot=progress-indicator]]:bg-emerald-500',
  warn: '[&>[data-slot=progress-indicator]]:bg-amber-500',
  danger: '[&>[data-slot=progress-indicator]]:bg-rose-500',
}

const TONE_TEXT: Record<'ok' | 'warn' | 'danger', string> = {
  ok: 'text-emerald-500',
  warn: 'text-amber-500',
  danger: 'text-rose-500',
}

function computePercent(sub: UserSubscription | undefined): number {
  const total = Number(sub?.amount_total || 0)
  const used = Number(sub?.amount_used || 0)
  if (total <= 0) return 0
  return Math.min(100, Math.round((used / total) * 100))
}

interface UsageRowProps {
  record: UserSubscriptionRecord
}

function UsageRow({ record }: UsageRowProps) {
  const { t } = useTranslation()
  const sub = record.subscription
  const plan = record.plan
  const total = Number(sub?.amount_total || 0)
  const used = Number(sub?.amount_used || 0)
  const remain = Math.max(0, total - used)
  const pct = computePercent(sub)
  const tone = progressTone(pct)
  const isUnlimited = total <= 0
  const title = plan?.title?.trim() || `${t('Subscription')} #${sub?.id ?? '-'}`
  const boundGroup = plan?.bound_group?.trim() || ''

  return (
    <div className='space-y-1.5'>
      <div className='flex items-center justify-between gap-2 text-xs'>
        <span className='text-foreground truncate font-medium' title={title}>
          {title}
        </span>
        {!isUnlimited && (
          <span className={cn('tabular-nums font-medium', TONE_TEXT[tone])}>
            {pct}%
          </span>
        )}
      </div>
      {boundGroup && (
        <div className='flex flex-wrap gap-1'>
          <Badge variant='secondary' className='h-4 px-1.5 text-[10px] font-normal'>
            {boundGroup}
          </Badge>
        </div>
      )}
      {isUnlimited ? (
        <div className='text-muted-foreground text-[11px]'>
          {t('Total quota')}: {t('Unlimited')}
        </div>
      ) : (
        <>
          <Progress
            value={pct}
            className={cn('h-1.5', TONE_INDICATOR[tone])}
          />
          <div className='text-muted-foreground flex items-center justify-between text-[11px] tabular-nums'>
            <span>
              {formatQuota(used)} / {formatQuota(total)}
            </span>
            <span>
              {t('Remaining')} {formatQuota(remain)}
            </span>
          </div>
        </>
      )}
    </div>
  )
}

/**
 * Header pill that shows aggregated subscription quota usage.
 * - Hidden when the user is not signed in or has no active subscription.
 * - Shows the highest-utilization subscription's percentage on the trigger.
 * - Click to open a popover listing all active subscriptions with progress bars.
 */
export function SubscriptionUsageButton({ className }: { className?: string }) {
  const { t } = useTranslation()
  const userId = useAuthStore((s) => s.auth.user?.id)
  const queryClient = useQueryClient()

  const { data, isFetching, refetch } = useQuery({
    queryKey: QUERY_KEY,
    queryFn: async () => {
      const res = await getSelfSubscriptionFull()
      return res?.data ?? { subscriptions: [], all_subscriptions: [], billing_preference: '' }
    },
    enabled: !!userId,
    refetchInterval: REFRESH_MS,
    staleTime: REFRESH_MS,
  })

  const subscriptions = data?.subscriptions ?? []
  const handleRefresh = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: QUERY_KEY })
    void refetch()
  }, [queryClient, refetch])

  if (!userId) return null
  if (subscriptions.length === 0 && !isFetching) return null

  // Trigger label: highest pct among active subscriptions, fall back to 'Subscription'
  const topPct = subscriptions
    .map((r) => computePercent(r.subscription))
    .reduce((max, p) => (p > max ? p : max), 0)
  const triggerLabel =
    subscriptions.length > 0 && topPct > 0 ? `${topPct}%` : t('Subscription')

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          variant='ghost'
          size='sm'
          aria-label={t('Subscription quota')}
          className={cn('h-9 gap-1.5 rounded-full px-3 text-xs', className)}
        >
          <Coins className='size-4' />
          <span className='tabular-nums'>{triggerLabel}</span>
          {subscriptions.length > 1 && (
            <Badge variant='secondary' className='ml-1 h-4 rounded-full px-1.5 text-[10px]'>
              {subscriptions.length}
            </Badge>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align='end' className='w-80 p-3'>
        <div className='mb-2 flex items-center justify-between'>
          <span className='text-sm font-semibold'>{t('Subscription quota')}</span>
          <Button
            variant='ghost'
            size='icon'
            className='size-7'
            onClick={handleRefresh}
            aria-label={t('Refresh')}
          >
            <RefreshCw
              className={cn('size-3.5', isFetching && 'animate-spin')}
            />
          </Button>
        </div>
        <Separator className='mb-2' />
        {subscriptions.length === 0 ? (
          <div className='text-muted-foreground py-2 text-xs'>
            {t('No active subscriptions')}
          </div>
        ) : (
          <ScrollArea className='max-h-72 pr-1'>
            <div className='space-y-3 py-1'>
              {subscriptions.map((rec) => (
                <UsageRow key={rec.subscription?.id} record={rec} />
              ))}
            </div>
          </ScrollArea>
        )}
      </PopoverContent>
    </Popover>
  )
}

import { useState, useEffect, useMemo, useCallback } from 'react'
import {
  RefreshCw,
  Sparkles,
  Check,
  Crown,
  Clock,
  Coins,
  ShoppingCart,
  CalendarClock,
  Zap,
  Gift,
  Package,
  CircleCheck,
  CircleX,
  CircleAlert,
  CodeXml,
  Bot,
  Layers,
  Trash2,
  type LucideIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useStatus } from '@/hooks/use-status'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  StatusBadge,
  dotColorMap,
  textColorMap,
} from '@/components/status-badge'
import {
  getPublicPlans,
  getSelfSubscriptionFull,
  hideSelfSubscription,
  updateBillingPreference,
} from '@/features/subscriptions/api'
import { SubscriptionPurchaseDialog } from '@/features/subscriptions/components/dialogs/subscription-purchase-dialog'
import { formatDuration } from '@/features/subscriptions/lib'
import type {
  PlanRecord,
  UserSubscriptionRecord,
} from '@/features/subscriptions/types'
import type { PaymentMethod, TopupInfo } from '@/features/wallet/types'

interface SubscriptionPlansCardProps {
  topupInfo: TopupInfo | null
  onAvailabilityChange?: (available: boolean) => void
}

function getEpayMethods(payMethods: PaymentMethod[] = []): PaymentMethod[] {
  return payMethods.filter(
    (m) => m?.type && m.type !== 'stripe' && m.type !== 'creem'
  )
}

// 按分组名识别品牌：含 codex / claude 关键字时返回专属 icon + 配色
function getGroupBrand(group: string): {
  icon: LucideIcon
  iconClass: string
  ringClass: string
  activeClass: string
} {
  const g = group.toLowerCase()
  if (g.includes('codex')) {
    return {
      icon: CodeXml,
      iconClass:
        'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
      ringClass: 'ring-emerald-200/60 dark:ring-emerald-500/20',
      activeClass:
        'bg-emerald-600 text-white hover:bg-emerald-700 dark:bg-emerald-600 dark:hover:bg-emerald-500',
    }
  }
  if (g.includes('claude')) {
    return {
      icon: Bot,
      iconClass:
        'bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
      ringClass: 'ring-amber-200/60 dark:ring-amber-500/20',
      activeClass:
        'bg-amber-600 text-white hover:bg-amber-700 dark:bg-amber-600 dark:hover:bg-amber-500',
    }
  }
  return {
    icon: Layers,
    iconClass: 'bg-muted text-muted-foreground',
    ringClass: 'ring-muted/60',
    activeClass: '',
  }
}

export function SubscriptionPlansCard({
  topupInfo,
  onAvailabilityChange,
}: SubscriptionPlansCardProps) {
  const { t } = useTranslation()
  const { status } = useStatus()

  const [plans, setPlans] = useState<PlanRecord[]>([])
  const [activeSubscriptions, setActiveSubscriptions] = useState<
    UserSubscriptionRecord[]
  >([])
  const [allSubscriptions, setAllSubscriptions] = useState<
    UserSubscriptionRecord[]
  >([])
  const [billingPreference, setBillingPreference] =
    useState('subscription_first')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)

  const [purchaseOpen, setPurchaseOpen] = useState(false)
  const [selectedPlan, setSelectedPlan] = useState<PlanRecord | null>(null)
  // 购买订阅 tab 的分组筛选 'all' = 全部
  const [selectedGroup, setSelectedGroup] = useState<string>('all')
  // 软删除（隐藏）确认对话框
  const [hideTarget, setHideTarget] = useState<UserSubscriptionRecord | null>(
    null
  )
  const [hiding, setHiding] = useState(false)

  const enableStripe = !!status?.enable_stripe_topup
  const enableCreem = !!topupInfo?.enable_creem_topup
  const enableOnlineTopUp = !!status?.enable_online_topup
  const epayMethods = useMemo(
    () => getEpayMethods(topupInfo?.pay_methods),
    [topupInfo?.pay_methods]
  )

  const fetchPlans = useCallback(async () => {
    try {
      const res = await getPublicPlans()
      if (res.success) {
        setPlans(res.data || [])
      }
    } catch {
      setPlans([])
    }
  }, [])

  const fetchSelfSubscription = useCallback(async () => {
    try {
      const res = await getSelfSubscriptionFull()
      if (res.success && res.data) {
        setBillingPreference(
          res.data.billing_preference || 'subscription_first'
        )
        setActiveSubscriptions(res.data.subscriptions || [])
        setAllSubscriptions(res.data.all_subscriptions || [])
      }
    } catch {
      // ignore
    }
  }, [])

  useEffect(() => {
    const init = async () => {
      setLoading(true)
      await Promise.all([fetchPlans(), fetchSelfSubscription()])
      setLoading(false)
    }
    init()
  }, [fetchPlans, fetchSelfSubscription])

  const handleRefresh = async () => {
    setRefreshing(true)
    try {
      await fetchSelfSubscription()
    } finally {
      setRefreshing(false)
    }
  }

  const handleConfirmHide = async () => {
    if (!hideTarget?.subscription?.id) return
    setHiding(true)
    try {
      const res = await hideSelfSubscription(hideTarget.subscription.id)
      if (res.success) {
        toast.success(t('Removed from list'))
        await fetchSelfSubscription()
        setHideTarget(null)
      } else {
        toast.error(res.message || t('Request failed'))
      }
    } catch {
      toast.error(t('Request failed'))
    } finally {
      setHiding(false)
    }
  }

  const handlePreferenceChange = async (pref: string) => {
    const previous = billingPreference
    setBillingPreference(pref)
    try {
      const res = await updateBillingPreference(pref)
      if (res.success) {
        toast.success(t('Updated successfully'))
        const normalized = res.data?.billing_preference || pref
        setBillingPreference(normalized)
      } else {
        toast.error(res.message || t('Update failed'))
        setBillingPreference(previous)
      }
    } catch {
      toast.error(t('Request failed'))
      setBillingPreference(previous)
    }
  }

  const hasActive = activeSubscriptions.length > 0
  const hasAny = allSubscriptions.length > 0
  const isAvailable = loading || plans.length > 0 || hasAny
  const disablePref = !hasActive
  // 直接反映后端真实偏好，不做显示纠偏（旧逻辑会把订阅类偷显示成 wallet_first，导致显示≠存储）
  const displayPref = billingPreference

  const planPurchaseCountMap = useMemo(() => {
    const map = new Map<number, number>()
    for (const sub of allSubscriptions) {
      const planId = sub?.subscription?.plan_id
      if (!planId) continue
      map.set(planId, (map.get(planId) || 0) + 1)
    }
    return map
  }, [allSubscriptions])

  useEffect(() => {
    onAvailabilityChange?.(isAvailable)
  }, [isAvailable, onAvailabilityChange])

  const planTitleMap = useMemo(() => {
    const map = new Map<number, string>()
    for (const p of plans) {
      if (p?.plan?.id) {
        map.set(p.plan.id, p.plan.title || '')
      }
    }
    return map
  }, [plans])

  // 整体推荐：sort_order desc 后的第一个套餐自动打"推荐"标
  const popularPlanId = useMemo(
    () => (plans.length > 1 ? plans[0]?.plan?.id : undefined),
    [plans]
  )

  // 按 bound_group 聚合套餐
  const groupedPlans = useMemo(() => {
    const groups = new Map<string, typeof plans>()
    for (const p of plans) {
      const key = (p?.plan?.bound_group || '').trim() || '__default__'
      if (!groups.has(key)) groups.set(key, [])
      groups.get(key)!.push(p)
    }
    return Array.from(groups.entries())
  }, [plans])

  // 根据选中的分组过滤
  const visiblePlans = useMemo(() => {
    if (selectedGroup === 'all') return plans
    return plans.filter(
      (p) =>
        ((p?.plan?.bound_group || '').trim() || '__default__') === selectedGroup
    )
  }, [plans, selectedGroup])

  const getRemainingDays = (sub: UserSubscriptionRecord) => {
    const endTime = sub?.subscription?.end_time || 0
    if (!endTime) return 0
    const now = Date.now() / 1000
    return Math.max(0, Math.ceil((endTime - now) / 86400))
  }

  const getUsagePercent = (sub: UserSubscriptionRecord) => {
    const total = Number(sub?.subscription?.amount_total || 0)
    const used = Number(sub?.subscription?.amount_used || 0)
    if (total <= 0) return 0
    return Math.round((used / total) * 100)
  }

  if (loading) {
    return (
      <div className='space-y-4 sm:space-y-5'>
        <Skeleton className='h-24 w-full rounded-xl' />
        <div className='grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3'>
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className='h-48 w-full' />
          ))}
        </div>
      </div>
    )
  }

  if (plans.length === 0 && !hasAny) {
    return null
  }

  return (
    <>
      <Tabs defaultValue='my' className='w-full'>
        <TabsList className='grid w-full max-w-md grid-cols-2'>
          <TabsTrigger value='my' className='gap-1.5'>
            <Crown className='h-3.5 w-3.5' />
            {t('My Subscriptions')}
          </TabsTrigger>
          <TabsTrigger value='buy' className='gap-1.5'>
            <ShoppingCart className='h-3.5 w-3.5' />
            {t('Purchase Subscription')}
          </TabsTrigger>
        </TabsList>

        {/* ==================== Tab 1: 我的订阅 ==================== */}
        <TabsContent value='my' className='mt-4 space-y-4 sm:space-y-5'>
          {/* 顶部：状态摘要 + 偏好选择 + 刷新 */}
          <div className='rounded-xl border p-3 sm:p-4'>
            <div className='flex flex-wrap items-center justify-between gap-2.5 sm:gap-3'>
              <div className='flex min-w-0 flex-wrap items-center gap-2'>
                <span className='text-sm font-medium'>
                  {t('My Subscriptions')}
                </span>
                <span className='flex items-center gap-1.5 text-xs font-medium'>
                  <span
                    className={cn(
                      'size-1.5 shrink-0 rounded-full',
                      hasActive ? dotColorMap.success : dotColorMap.neutral
                    )}
                    aria-hidden='true'
                  />
                  {hasActive ? (
                    <span className={cn(textColorMap.success)}>
                      {activeSubscriptions.length} {t('active')}
                    </span>
                  ) : (
                    <span className='text-muted-foreground'>
                      {t('No Active')}
                    </span>
                  )}
                  {allSubscriptions.length > activeSubscriptions.length && (
                    <>
                      <span className='text-muted-foreground/30'>·</span>
                      <span className='text-muted-foreground'>
                        {allSubscriptions.length - activeSubscriptions.length}{' '}
                        {t('expired')}
                      </span>
                    </>
                  )}
                </span>
              </div>
              <div className='flex w-full items-center gap-2 sm:w-auto'>
                <Select
                  value={displayPref}
                  onValueChange={handlePreferenceChange}
                >
                  <SelectTrigger className='h-8 flex-1 text-xs sm:w-[140px] sm:flex-none'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem
                      value='subscription_first'
                      disabled={disablePref}
                    >
                      {t('Subscription First')}
                      {disablePref ? ` (${t('No Active')})` : ''}
                    </SelectItem>
                    <SelectItem
                      value='subscription_then_auto'
                      disabled={disablePref}
                    >
                      {t('Subscription First (Auto Fallback)')}
                      {disablePref ? ` (${t('No Active')})` : ''}
                    </SelectItem>
                    <SelectItem value='wallet_first'>
                      {t('Wallet First')}
                    </SelectItem>
                    <SelectItem
                      value='subscription_only'
                      disabled={disablePref}
                    >
                      {t('Subscription Only')}
                      {disablePref ? ` (${t('No Active')})` : ''}
                    </SelectItem>
                    <SelectItem value='wallet_only'>
                      {t('Wallet Only')}
                    </SelectItem>
                  </SelectContent>
                </Select>
                <Button
                  variant='ghost'
                  size='icon'
                  className='h-8 w-8'
                  onClick={handleRefresh}
                  disabled={refreshing}
                >
                  <RefreshCw
                    className={`h-3.5 w-3.5 ${refreshing ? 'animate-spin' : ''}`}
                  />
                </Button>
              </div>
            </div>

            {disablePref && billingPreference === 'subscription_only' && (
              <div className='mt-2 flex items-start gap-1.5 rounded-md border border-amber-300/60 bg-amber-50/80 px-2.5 py-1.5 text-xs text-amber-700 dark:border-amber-500/30 dark:bg-amber-950/30 dark:text-amber-400'>
                <CircleAlert
                  className='mt-0.5 h-3.5 w-3.5 shrink-0'
                  aria-hidden='true'
                />
                <span>
                  {t(
                    'Your subscription has expired and {{pref}} mode cannot make calls. Switch to Wallet First or Wallet Only to continue.',
                    { pref: t('Subscription Only') }
                  )}
                </span>
              </div>
            )}

            {disablePref &&
              (billingPreference === 'subscription_first' ||
                billingPreference === 'subscription_then_auto') && (
              <div className='mt-2 flex items-start gap-1.5 rounded-md border border-sky-300/60 bg-sky-50/80 px-2.5 py-1.5 text-xs text-sky-700 dark:border-sky-500/30 dark:bg-sky-950/30 dark:text-sky-400'>
                <Coins
                  className='mt-0.5 h-3.5 w-3.5 shrink-0'
                  aria-hidden='true'
                />
                <span>
                  {t(
                    'No active subscription. Requests are billed to your wallet balance.'
                  )}
                </span>
              </div>
            )}
          </div>

          {/* 订阅卡片网格（与"购买订阅"卡片同栅格） */}
          {hasAny ? (
            <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4 lg:grid-cols-3 2xl:grid-cols-4'>
              {allSubscriptions.map((sub) => {
                const subscription = sub.subscription
                const totalAmount = Number(subscription?.amount_total || 0)
                const usedAmount = Number(subscription?.amount_used || 0)
                const remainAmount =
                  totalAmount > 0 ? Math.max(0, totalAmount - usedAmount) : 0
                const planTitle = planTitleMap.get(subscription?.plan_id) || ''
                const remainDays = getRemainingDays(sub)
                const usagePercent = getUsagePercent(sub)
                const now = Date.now() / 1000
                const isExpired = (subscription?.end_time || 0) < now
                const isCancelled = subscription?.status === 'cancelled'
                const isExhausted =
                  subscription?.status === 'exhausted' && !isExpired
                const isActive = subscription?.status === 'active' && !isExpired

                const StatusIcon = isActive
                  ? CircleCheck
                  : isCancelled
                    ? CircleX
                    : CircleAlert
                const accent = isActive
                  ? 'border-emerald-300/70 bg-gradient-to-br from-emerald-50/80 to-transparent shadow-sm dark:border-emerald-500/30 dark:from-emerald-950/40 dark:to-transparent'
                  : isExhausted
                    ? 'border-amber-300/70 bg-gradient-to-br from-amber-50/70 to-transparent dark:border-amber-500/30 dark:from-amber-950/30 dark:to-transparent'
                    : isCancelled
                      ? 'border-rose-200/60 bg-gradient-to-br from-rose-50/60 to-transparent dark:border-rose-500/20 dark:from-rose-950/30 dark:to-transparent'
                      : 'border-muted-foreground/15 bg-gradient-to-br from-muted/40 to-transparent'
                const headerIconBg = isActive
                  ? 'bg-emerald-100 text-emerald-600 dark:bg-emerald-500/20 dark:text-emerald-400'
                  : isExhausted
                    ? 'bg-amber-100 text-amber-600 dark:bg-amber-500/20 dark:text-amber-400'
                    : isCancelled
                      ? 'bg-rose-100 text-rose-500 dark:bg-rose-500/20 dark:text-rose-400'
                      : 'bg-muted text-muted-foreground'

                return (
                  <Card
                    key={subscription?.id}
                    className={cn(
                      'overflow-hidden transition-shadow hover:shadow-md',
                      accent
                    )}
                  >
                    <CardContent className='flex h-full flex-col p-3.5 sm:p-4'>
                      <div className='mb-2 flex items-start justify-between gap-3'>
                        <div className='flex min-w-0 items-start gap-2.5'>
                          <div
                            className={cn(
                              'flex h-9 w-9 shrink-0 items-center justify-center rounded-lg',
                              headerIconBg
                            )}
                          >
                            <Crown className='h-4.5 w-4.5' />
                          </div>
                          <div className='min-w-0'>
                            <h4 className='truncate font-semibold'>
                              {planTitle || t('Subscription Plans')}
                            </h4>
                            <p className='text-muted-foreground truncate text-xs'>
                              #{subscription?.id}
                            </p>
                          </div>
                        </div>
                        <div className='flex shrink-0 items-center gap-1'>
                          <StatusBadge
                            label={
                              isActive
                                ? t('Active')
                                : isExhausted
                                  ? t('Quota Exhausted')
                                  : isCancelled
                                    ? t('Cancelled')
                                    : t('Expired')
                            }
                            variant={isActive ? 'success' : 'neutral'}
                            copyable={false}
                          >
                            <StatusIcon className='h-3 w-3' />
                          </StatusBadge>
                          {!isActive && !isExhausted && (
                            <Tooltip>
                              <TooltipTrigger asChild>
                                <Button
                                  variant='ghost'
                                  size='icon'
                                  className='text-muted-foreground h-6 w-6 hover:text-rose-500'
                                  onClick={() => setHideTarget(sub)}
                                  aria-label={t('Remove from list')}
                                >
                                  <Trash2 className='h-3.5 w-3.5' />
                                </Button>
                              </TooltipTrigger>
                              <TooltipContent>
                                {t(
                                  'Remove from my subscriptions (does not count toward purchase limit)'
                                )}
                              </TooltipContent>
                            </Tooltip>
                          )}
                        </div>
                      </div>

                      {isActive || isExhausted ? (
                        <div className='py-2'>
                          <div className='flex items-baseline gap-1.5'>
                            <CalendarClock className='text-primary h-4 w-4' />
                            <span className='text-primary text-2xl font-bold'>
                              {remainDays}
                            </span>
                            <span className='text-muted-foreground text-sm'>
                              {t('days remaining')}
                            </span>
                          </div>
                        </div>
                      ) : (
                        <div className='py-2'>
                          <span className='text-muted-foreground inline-flex items-center gap-1.5 text-sm'>
                            <StatusIcon className='h-4 w-4' />
                            {isCancelled ? t('Cancelled') : t('Expired')}
                          </span>
                        </div>
                      )}

                      <div className='flex-1 space-y-1.5 pb-3 text-xs'>
                        <div className='text-muted-foreground flex items-center gap-1.5'>
                          <Clock className='h-3.5 w-3.5 shrink-0' />
                          <span>
                            {isActive
                              ? t('Until')
                              : isCancelled
                                ? t('Cancelled at')
                                : t('Expired at')}
                            :{' '}
                            {new Date(
                              (subscription?.end_time || 0) * 1000
                            ).toLocaleString()}
                          </span>
                        </div>
                        {isActive &&
                          (subscription?.next_reset_time ?? 0) > 0 && (
                            <div className='text-muted-foreground flex items-center gap-1.5'>
                              <RefreshCw className='h-3.5 w-3.5 shrink-0' />
                              <span>
                                {t('Next reset')}:{' '}
                                {new Date(
                                  subscription!.next_reset_time! * 1000
                                ).toLocaleString()}
                              </span>
                            </div>
                          )}
                        <div className='text-muted-foreground flex items-center gap-1.5'>
                          <Coins className='h-3.5 w-3.5 shrink-0' />
                          <span>
                            {t('Current Quota')}:{' '}
                            {totalAmount > 0 ? (
                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <span className='cursor-help'>
                                    {formatQuota(usedAmount)}/
                                    {formatQuota(totalAmount)}
                                  </span>
                                </TooltipTrigger>
                                <TooltipContent>
                                  {t('Raw Quota')}: {usedAmount}/{totalAmount} ·{' '}
                                  {t('Remaining')} {remainAmount}
                                </TooltipContent>
                              </Tooltip>
                            ) : (
                              t('Unlimited')
                            )}
                            {totalAmount > 0 && (
                              <span className='ml-2'>
                                · {t('Used')} {usagePercent}%
                              </span>
                            )}
                          </span>
                        </div>
                        {totalAmount > 0 && isActive && (
                          <Progress
                            value={usagePercent}
                            className={cn(
                              'mt-2 h-1.5',
                              usagePercent < 70
                                ? '[&>[data-slot=progress-indicator]]:bg-emerald-500'
                                : usagePercent < 90
                                  ? '[&>[data-slot=progress-indicator]]:bg-amber-500'
                                  : '[&>[data-slot=progress-indicator]]:bg-rose-500'
                            )}
                          />
                        )}
                      </div>
                    </CardContent>
                  </Card>
                )
              })}
            </div>
          ) : (
            <div className='from-muted/30 flex flex-col items-center gap-2 rounded-xl border border-dashed bg-gradient-to-br to-transparent p-8 text-center'>
              <div className='bg-primary/10 text-primary flex h-12 w-12 items-center justify-center rounded-full'>
                <Gift className='h-6 w-6' />
              </div>
              <p className='text-muted-foreground text-sm'>
                {t('Subscribe to a plan for model access')}
              </p>
            </div>
          )}
        </TabsContent>

        {/* ==================== Tab 2: 购买订阅 ==================== */}
        <TabsContent value='buy' className='mt-4 space-y-4'>
          {plans.length > 0 ? (
            <>
              {/* 分组筛选标签栏（仅当 >1 组时显示） */}
              {groupedPlans.length > 1 && (
                <div className='flex flex-wrap items-center gap-2'>
                  <Button
                    size='sm'
                    variant={selectedGroup === 'all' ? 'default' : 'outline'}
                    className='h-8 gap-1.5 rounded-full'
                    onClick={() => setSelectedGroup('all')}
                  >
                    <Layers className='h-3.5 w-3.5' />
                    {t('All')}
                    <span className='text-[11px] opacity-70'>
                      {plans.length}
                    </span>
                  </Button>
                  {groupedPlans.map(([groupKey, groupPlans]) => {
                    const isDefault = groupKey === '__default__'
                    const groupName = isDefault ? t('Default Group') : groupKey
                    const brand = getGroupBrand(isDefault ? '' : groupKey)
                    const GroupIcon = brand.icon
                    const active = selectedGroup === groupKey
                    return (
                      <Button
                        key={groupKey}
                        size='sm'
                        variant={active ? 'default' : 'outline'}
                        className={cn(
                          'h-8 gap-1.5 rounded-full',
                          active && brand.activeClass
                        )}
                        onClick={() => setSelectedGroup(groupKey)}
                      >
                        <GroupIcon className='h-3.5 w-3.5' />
                        {groupName}
                        <span className='text-[11px] opacity-70'>
                          {groupPlans.length}
                        </span>
                      </Button>
                    )
                  })}
                </div>
              )}

              {/* 套餐栅格（同高对齐） */}
              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4 lg:grid-cols-3 2xl:grid-cols-4'>
                {visiblePlans.map((p) => {
                  const plan = p?.plan
                  if (!plan) return null
                  const totalAmount = Number(plan.total_amount || 0)
                  const price = Number(plan.price_amount || 0).toFixed(2)
                  const isPopular = !!popularPlanId && plan.id === popularPlanId
                  const limit = Number(plan.max_purchase_per_user || 0)
                  const count = planPurchaseCountMap.get(plan.id) || 0
                  const reached = limit > 0 && count >= limit

                  // 重置周期内的额度（plan.total_amount 是单期额度，每次重置回填）
                  const periodAmount = totalAmount
                  const resetPeriod = plan.quota_reset_period || 'never'

                  // 把"有效期 / 重置周期"折算成秒，估算整个订阅期内的重置次数
                  const durationSecs = (() => {
                    const u = plan.duration_unit
                    const v = Number(plan.duration_value || 0)
                    if (u === 'year') return v * 365 * 86400
                    if (u === 'month') return v * 30 * 86400
                    if (u === 'day') return v * 86400
                    if (u === 'hour') return v * 3600
                    if (u === 'custom') return Number(plan.custom_seconds || 0)
                    return 0
                  })()
                  const periodSecs = (() => {
                    if (resetPeriod === 'daily') return 86400
                    if (resetPeriod === 'weekly') return 7 * 86400
                    if (resetPeriod === 'monthly') return 30 * 86400
                    if (resetPeriod === 'custom')
                      return Number(plan.quota_reset_custom_seconds || 0)
                    return 0
                  })()
                  const periodsCount =
                    resetPeriod === 'never' || periodSecs <= 0
                      ? 1
                      : Math.max(1, Math.round(durationSecs / periodSecs))

                  const totalAcrossDuration = periodAmount * periodsCount

                  // 单期标签：每天/每周/每月/...
                  const periodLabelMap: Record<string, string> = {
                    daily: t('Daily Quota'),
                    weekly: t('Weekly Quota'),
                    monthly: t('Monthly Quota'),
                    custom: t('Quota per cycle'),
                  }
                  const periodLabel = periodLabelMap[resetPeriod] || ''

                  const totalQuotaLabel =
                    periodAmount > 0
                      ? `${t('Total Quota')}: ${formatQuota(totalAcrossDuration)}`
                      : `${t('Total Quota')}: ${t('Unlimited')}`
                  const periodQuotaLabel =
                    periodAmount > 0 && resetPeriod !== 'never' && periodLabel
                      ? `${periodLabel}: ${formatQuota(periodAmount)}`
                      : ''

                  // 库存：-1 / undefined = 不限量；>=0 = 显示剩余可购
                  const stockTotal =
                    plan.stock_total === undefined || plan.stock_total === null
                      ? -1
                      : Number(plan.stock_total)
                  const stockSold = Number(plan.stock_sold || 0)
                  const stockRemaining =
                    stockTotal < 0 ? -1 : Math.max(0, stockTotal - stockSold)
                  const stockSoldOut = stockTotal >= 0 && stockRemaining === 0

                  const benefitItems: { label: string; icon: typeof Check }[] =
                    [
                      {
                        label: `${t('Validity Period')}: ${formatDuration(plan, t)}`,
                        icon: CalendarClock,
                      },
                      {
                        label: totalQuotaLabel,
                        icon: Coins,
                      },
                      periodQuotaLabel
                        ? {
                            label: periodQuotaLabel,
                            icon: RefreshCw,
                          }
                        : null,
                      stockTotal >= 0
                        ? {
                            label: `${t('Remaining Stock')}: ${stockRemaining}`,
                            icon: Package,
                          }
                        : null,
                      limit > 0
                        ? {
                            label: `${t('Per-User Limit')}: ${limit}`,
                            icon: Gift,
                          }
                        : null,
                      plan.upgrade_group
                        ? {
                            label: `${t('Upgrade Group')}: ${plan.upgrade_group}`,
                            icon: Zap,
                          }
                        : null,
                    ].filter(Boolean) as { label: string; icon: typeof Check }[]

                  const coverUrl = (plan.cover_image_url || '').trim()

                  return (
                    <Card
                      key={plan.id}
                      className={cn(
                        'flex h-full flex-col gap-0 overflow-hidden p-0 transition-shadow hover:shadow-md',
                        isPopular
                          ? 'border-primary/70 from-primary/8 via-primary/2 ring-primary/10 dark:from-primary/15 dark:via-primary/5 bg-gradient-to-br to-transparent shadow-sm ring-1'
                          : 'bg-gradient-to-br from-sky-50/50 to-transparent dark:from-sky-950/20'
                      )}
                    >
                      {coverUrl && (
                        <div className='bg-muted/20 relative aspect-[16/9] w-full overflow-hidden border-b'>
                          <img
                            src={coverUrl}
                            alt={plan.title || ''}
                            className='absolute inset-0 h-full w-full object-cover'
                            loading='lazy'
                            onError={(e) => {
                              ;(
                                e.currentTarget.parentElement as HTMLDivElement
                              ).style.display = 'none'
                            }}
                          />
                        </div>
                      )}
                      <CardContent className='flex h-full flex-col p-3.5 sm:p-4'>
                        <div className='mb-2 flex items-start justify-between gap-3'>
                          <div className='flex min-w-0 items-start gap-2.5'>
                            <div
                              className={cn(
                                'flex h-9 w-9 shrink-0 items-center justify-center rounded-lg',
                                isPopular
                                  ? 'bg-primary/15 text-primary'
                                  : 'bg-sky-100 text-sky-600 dark:bg-sky-500/15 dark:text-sky-400'
                              )}
                            >
                              <Crown className='h-4.5 w-4.5' />
                            </div>
                            <div className='min-w-0'>
                              <h4 className='truncate font-semibold'>
                                {plan.title || t('Subscription Plans')}
                              </h4>
                              {plan.subtitle && (
                                <p className='text-muted-foreground truncate text-xs'>
                                  {plan.subtitle}
                                </p>
                              )}
                            </div>
                          </div>
                          {isPopular && (
                            <StatusBadge
                              variant='info'
                              copyable={false}
                              className='shrink-0'
                            >
                              <Sparkles className='h-3 w-3' />
                              {t('Recommended')}
                            </StatusBadge>
                          )}
                        </div>

                        {plan.description && (
                          <p className='text-muted-foreground mt-2 text-xs leading-relaxed whitespace-pre-line'>
                            {plan.description}
                          </p>
                        )}

                        <div className='py-2'>
                          <span className='text-muted-foreground text-sm'>
                            $
                          </span>
                          <span className='text-primary text-3xl font-bold tracking-tight'>
                            {price}
                          </span>
                        </div>

                        <div className='flex-1 space-y-1.5 pb-3'>
                          {benefitItems.map(({ label, icon: Icon }) => (
                            <div
                              key={label}
                              className='text-muted-foreground flex items-center gap-2 text-xs'
                            >
                              <Icon className='text-primary/80 h-3.5 w-3.5 shrink-0' />
                              <span>{label}</span>
                            </div>
                          ))}
                        </div>

                        <Separator className='mb-3' />

                        {stockSoldOut ? (
                          <Button
                            variant='outline'
                            className='w-full gap-1.5'
                            disabled
                          >
                            <CircleAlert className='h-4 w-4' />
                            {t('Sold Out')}
                          </Button>
                        ) : reached ? (
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <div>
                                <Button
                                  variant='outline'
                                  className='w-full gap-1.5'
                                  disabled
                                >
                                  <CircleAlert className='h-4 w-4' />
                                  {t('Limit Reached')}
                                </Button>
                              </div>
                            </TooltipTrigger>
                            <TooltipContent>
                              {t('Purchase limit reached')} ({count}/{limit})
                            </TooltipContent>
                          </Tooltip>
                        ) : (
                          <Button
                            className={cn(
                              'w-full gap-1.5',
                              isPopular
                                ? 'bg-primary hover:bg-primary/90'
                                : 'bg-sky-600 text-white hover:bg-sky-700 dark:bg-sky-600 dark:hover:bg-sky-500'
                            )}
                            onClick={() => {
                              setSelectedPlan(p)
                              setPurchaseOpen(true)
                            }}
                          >
                            <ShoppingCart className='h-4 w-4' />
                            {t('Subscribe Now')}
                          </Button>
                        )}
                      </CardContent>
                    </Card>
                  )
                })}
              </div>

              {visiblePlans.length === 0 && (
                <p className='text-muted-foreground py-8 text-center text-sm'>
                  {t('No plans in this group')}
                </p>
              )}
            </>
          ) : (
            <p className='text-muted-foreground py-4 text-center text-sm'>
              {t('No plans available')}
            </p>
          )}
        </TabsContent>
      </Tabs>

      <SubscriptionPurchaseDialog
        open={purchaseOpen}
        onOpenChange={(open) => {
          setPurchaseOpen(open)
          if (!open) {
            fetchSelfSubscription()
          }
        }}
        plan={selectedPlan}
        enableStripe={enableStripe}
        enableCreem={enableCreem}
        enableOnlineTopUp={enableOnlineTopUp}
        epayMethods={epayMethods}
        purchaseLimit={
          selectedPlan?.plan?.max_purchase_per_user
            ? Number(selectedPlan.plan.max_purchase_per_user)
            : undefined
        }
        purchaseCount={
          selectedPlan?.plan?.id
            ? planPurchaseCountMap.get(selectedPlan.plan.id)
            : undefined
        }
      />

      <AlertDialog
        open={!!hideTarget}
        onOpenChange={(open) => !open && setHideTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t('Remove this subscription from your list?')}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'This only hides the subscription from your view; data is retained. After removal, this subscription no longer counts toward the per-plan purchase limit.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={hiding}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={hiding}
              onClick={handleConfirmHide}
              className='bg-rose-500 hover:bg-rose-600'
            >
              {hiding ? t('Removing...') : t('Remove')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

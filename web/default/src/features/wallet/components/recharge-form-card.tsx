// 添加资金卡片 — Tabs 双路径（在线充值 / 兑换码）+ 三步纵向流。
// 步骤 2 为"选中制"：点卡片只选中（radio 语义），步骤 3 主按钮才发起支付；
// 不可用方式灰显虚框 + 内联琥珀文字（不再依赖 Tooltip，修复移动端盲区）。
import { useState, useEffect, useMemo } from 'react'
import type { ReactNode } from 'react'
import {
  Gift,
  ExternalLink,
  Info,
  Loader2,
  Receipt,
  WalletCards,
  CheckCircle2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import { PAYMENT_TYPES } from '../constants'
import {
  formatCurrency,
  getDiscountLabel,
  getPaymentIcon,
  getChannelIcon,
  getMinTopupAmount,
  getSelectedMethodMinTopup,
  calculatePresetPricing,
} from '../lib'
import type {
  PresetAmount,
  SelectedPayMethod,
  TopupInfo,
  CreemProduct,
  WaffoPayMethod,
  AgouPayMethod,
  PayProvider,
  PayProviderChannel,
} from '../types'
import { CreemProductsSection } from './creem-products-section'
import { StepSection } from './step-section'

interface RechargeFormCardProps {
  topupInfo: TopupInfo | null
  presetAmounts: PresetAmount[]
  selectedPreset: number | null
  onSelectPreset: (preset: PresetAmount) => void
  topupAmount: number
  onTopupAmountChange: (amount: number) => void
  paymentAmount: number
  calculating: boolean
  /** 选中方式下各预设金额的真实价（后端询价，按 preset.value 索引） */
  presetPrices?: Record<number, number>
  /** 当前金额下各支付方式的真实实付（按支付方式 type 索引） */
  methodPrices?: Record<string, number>
  /** 首轮方式询价是否结束（区分加载中与算不出价，避免永久 Skeleton） */
  methodPricesReady?: boolean
  selectedMethod: SelectedPayMethod | null
  onSelectMethod: (sel: SelectedPayMethod) => void
  onInitiatePayment: () => void
  initiating?: boolean
  discountRate?: number
  redemptionCode: string
  onRedemptionCodeChange: (code: string) => void
  onRedeem: () => void
  redeeming: boolean
  topupLink?: string
  loading?: boolean
  priceRatio?: number
  usdExchangeRate?: number
  onOpenBilling?: () => void
  creemProducts?: CreemProduct[]
  enableCreemTopup?: boolean
  onCreemProductSelect?: (product: CreemProduct) => void
  enableWaffoTopup?: boolean
  waffoPayMethods?: WaffoPayMethod[]
  waffoMinTopup?: number
  enableWaffoPancakeTopup?: boolean
  enableCryptomusTopup?: boolean
  enableAgouTopup?: boolean
  agouPayMethods?: AgouPayMethod[]
  agouMinTopup?: number
  /** 统一支付平台列表（Waffo Pancake | Cryptomus | Agou），两层选择渲染 */
  providers?: PayProvider[]
}

// 步骤 2 的统一渲染条目：标准方式 + Waffo + agou 方式拍平成同构卡片数据。
interface MethodEntry {
  key: string
  sel: SelectedPayMethod
  /** 支付方式 type，用于查 methodPrices 的真实实付 */
  payType: string
  name: string
  icon: ReactNode
  minRequired: number
  group: 'standard' | 'waffo' | 'sfpay' | 'provider'
  /** provider 平台卡片支持的渠道（卡片内纯展示 icon+文字，非独立可选项） */
  channels?: PayProviderChannel[]
  /** 永久禁用占位（如微信通道未绑定）：灰显且不可选，与金额门槛无关 */
  comingSoon?: boolean
  /** 当前用户分组不允许：灰显且不可选（依旧显示该渠道），与金额门槛无关 */
  groupBlocked?: boolean
}

interface PayMethodCardProps {
  entry: MethodEntry
  available: boolean
  selected: boolean
  /** 当前金额下该方式的真实实付（未就绪为 undefined） */
  price?: number
  /** 首轮询价是否结束：true 且无 price 表示该方式算不出价，隐藏 Pay 行 */
  pricesReady?: boolean
  formatPrice: (amount: number) => string
  onSelect: () => void
}

// 步骤2 支付方式卡片：图标色块 + 方式名/状态 + 该方式当前金额的真实实付预览，
// 让用户在选择前就能横向比价（Crypto 与其它方式单价不同，金额差异显著）。
function PayMethodCard({
  entry,
  available,
  selected,
  price,
  pricesReady,
  formatPrice,
  onSelect,
}: PayMethodCardProps) {
  const { t } = useTranslation()
  return (
    <button
      type='button'
      onClick={available ? onSelect : undefined}
      disabled={!available}
      aria-pressed={selected}
      className={cn(
        'relative flex min-h-[4.75rem] flex-col justify-between gap-2 rounded-xl border p-3 text-left transition-all',
        available &&
          !selected &&
          'border-border bg-card hover:border-primary/40 hover:shadow-sm',
        available &&
          selected &&
          'border-primary ring-primary/30 bg-primary/5 ring-2',
        !available && 'cursor-not-allowed border-dashed opacity-60 grayscale'
      )}
    >
      {selected && (
        <CheckCircle2 className='text-primary absolute top-2 right-2 h-4 w-4' />
      )}
      <span className='flex w-full min-w-0 items-center gap-2.5 pr-5'>
        <span
          className={cn(
            'flex h-9 w-9 shrink-0 items-center justify-center rounded-lg transition-colors',
            selected ? 'bg-primary/10' : 'bg-muted'
          )}
        >
          {entry.icon}
        </span>
        <span className='flex min-w-0 flex-1 flex-col'>
          <span className='truncate text-sm font-semibold'>{entry.name}</span>
          {available ? (
            <span
              className={cn(
                'text-[11px]',
                selected ? 'text-primary' : 'text-muted-foreground'
              )}
            >
              {selected ? t('Selected') : t('Available')}
            </span>
          ) : entry.groupBlocked ? (
            <span className='text-muted-foreground text-[11px]'>
              {t('Not available for your group')}
            </span>
          ) : entry.comingSoon ? (
            <span className='text-muted-foreground text-[11px]'>
              {t('Coming soon')}
            </span>
          ) : (
            // 禁用原因内联可见（移动端无 hover，Tooltip 看不到）
            <span className='text-[11px] text-amber-600 dark:text-amber-400'>
              {t('Requires at least {{min}}', { min: entry.minRequired })}
            </span>
          )}
        </span>
      </span>
      {available && (price != null || !pricesReady) && (
        <span className='flex items-baseline gap-1 border-t pt-1.5'>
          <span className='text-muted-foreground text-[11px]'>Pay</span>
          {price != null ? (
            <span className='text-base font-bold tabular-nums'>
              {formatPrice(price)}
            </span>
          ) : (
            <Skeleton className='h-4 w-12' />
          )}
        </span>
      )}
      {entry.channels && entry.channels.length > 0 && (
        <span className='flex flex-wrap items-center gap-x-2 gap-y-1 border-t pt-1.5'>
          {entry.channels.map((ch) => (
            <span
              key={ch.key}
              className='text-muted-foreground flex items-center gap-1 text-[11px]'
            >
              {getChannelIcon(ch.icon, 'h-3 w-3', ch.name)}
              {ch.name}
            </span>
          ))}
        </span>
      )}
    </button>
  )
}

export function RechargeFormCard({
  topupInfo,
  presetAmounts,
  selectedPreset,
  onSelectPreset,
  topupAmount,
  onTopupAmountChange,
  paymentAmount,
  calculating,
  presetPrices,
  methodPrices,
  methodPricesReady,
  selectedMethod,
  onSelectMethod,
  onInitiatePayment,
  initiating,
  discountRate = 1,
  redemptionCode,
  onRedemptionCodeChange,
  onRedeem,
  redeeming,
  topupLink,
  loading,
  priceRatio = 1,
  usdExchangeRate = 1,
  onOpenBilling,
  creemProducts,
  enableCreemTopup,
  onCreemProductSelect,
  enableWaffoTopup,
  waffoPayMethods,
  waffoMinTopup,
  enableWaffoPancakeTopup,
  enableCryptomusTopup,
  enableAgouTopup,
  agouMinTopup,
  providers,
}: RechargeFormCardProps) {
  const { t } = useTranslation()
  const [localAmount, setLocalAmount] = useState(topupAmount.toString())

  useEffect(() => {
    setLocalAmount(topupAmount.toString())
  }, [topupAmount])

  const handleAmountChange = (value: string) => {
    setLocalAmount(value)
    const numValue = parseInt(value) || 0
    if (numValue >= 0) {
      onTopupAmountChange(numValue)
    }
  }

  const hasConfigurableTopup =
    topupInfo?.enable_online_topup ||
    topupInfo?.enable_stripe_topup ||
    enableWaffoTopup ||
    enableWaffoPancakeTopup ||
    enableCryptomusTopup ||
    enableAgouTopup
  const hasWaffoPaymentMethods =
    enableWaffoTopup &&
    Array.isArray(waffoPayMethods) &&
    waffoPayMethods.length > 0
  const amountEntered = topupAmount > 0

  // 标准方式 + Waffo 方式拍平成统一选中制网格（Waffo 用分组小标区隔）。
  // minRequired 与 index.tsx 的失效自动取消共用 getSelectedMethodMinTopup 规则。
  const methodEntries = useMemo<MethodEntry[]>(() => {
    const entries: MethodEntry[] = []
    const blockedSet = new Set(topupInfo?.group_blocked_methods ?? [])

    // ① 统一支付平台（providers）：每个平台一张卡片（选择单位），
    //    卡片内展示该平台支持的渠道 icon+文字（channels，纯展示）。后端已过滤仅返回启用渠道。
    if (Array.isArray(providers)) {
      for (const provider of providers) {
        const sel: SelectedPayMethod = { kind: 'provider', provider }
        entries.push({
          key: `provider-${provider.id}`,
          sel,
          payType: provider.id,
          name: provider.name,
          icon: getChannelIcon(
            provider.logo || provider.channels[0]?.icon,
            'h-4 w-4',
            provider.name
          ),
          minRequired: provider.min_topup || 0,
          group: 'provider',
          channels: provider.channels,
          groupBlocked: provider.blocked_by_group,
        })
      }
    }

    // ② legacy 静态方式（epay/stripe/waffo旧）；waffo_pancake/cryptomus/agou 已并入 providers，去重排除。
    const providerCovered = new Set(['waffo_pancake', 'cryptomus', 'sfpay'])
    if (Array.isArray(topupInfo?.pay_methods)) {
      for (const method of topupInfo.pay_methods) {
        if (providerCovered.has(method.type)) continue
        const sel: SelectedPayMethod = { kind: 'standard', method }
        entries.push({
          key: `std-${method.type}`,
          sel,
          payType: method.type,
          name: method.name,
          icon: getPaymentIcon(method.type, 'h-4 w-4', method.icon, method.name),
          minRequired: getSelectedMethodMinTopup(sel, waffoMinTopup),
          group: 'standard',
          groupBlocked: blockedSet.has(method.type),
        })
      }
    }

    // ③ legacy Waffo 旧聚合方式（Card/ApplePay/GooglePay 子方式）。
    if (hasWaffoPaymentMethods) {
      waffoPayMethods!.forEach((method, index) => {
        const sel: SelectedPayMethod = { kind: 'waffo', method, index }
        entries.push({
          key: `waffo-${index}`,
          sel,
          payType: 'waffo',
          name: method.name,
          icon: method.icon ? (
            <img
              src={method.icon}
              alt={method.name}
              className='h-4 w-4 object-contain'
            />
          ) : (
            getPaymentIcon('waffo', 'h-4 w-4')
          ),
          minRequired: getSelectedMethodMinTopup(sel, waffoMinTopup),
          group: 'waffo',
          groupBlocked: blockedSet.has(PAYMENT_TYPES.WAFFO),
        })
      })
    }

    // 注：agou 已并入 providers（含支付宝/微信渠道），不再单独渲染 sfpay_pay_methods。
    return entries
  }, [
    providers,
    topupInfo?.pay_methods,
    hasWaffoPaymentMethods,
    waffoPayMethods,
    waffoMinTopup,
    topupInfo?.group_blocked_methods,
  ])

  const standardEntries = methodEntries.filter((e) => e.group === 'standard')
  const waffoEntries = methodEntries.filter((e) => e.group === 'waffo')
  const providerEntries = methodEntries.filter((e) => e.group === 'provider')

  // comingSoon（占位禁用）既不算"可用"也不算"金额不足"，只渲染为灰显卡片。
  const availableEntries = methodEntries.filter(
    (e) => !e.comingSoon && !e.groupBlocked && e.minRequired <= topupAmount
  )
  const unavailableEntries = methodEntries.filter(
    (e) => !e.comingSoon && !e.groupBlocked && e.minRequired > topupAmount
  )

  const isEntrySelected = (entry: MethodEntry): boolean => {
    if (!selectedMethod) return false
    if (selectedMethod.kind === 'standard' && entry.sel.kind === 'standard') {
      return selectedMethod.method.type === entry.sel.method.type
    }
    if (selectedMethod.kind === 'waffo' && entry.sel.kind === 'waffo') {
      return selectedMethod.index === entry.sel.index
    }
    if (selectedMethod.kind === 'sfpay' && entry.sel.kind === 'sfpay') {
      return selectedMethod.method.type === entry.sel.method.type
    }
    if (selectedMethod.kind === 'provider' && entry.sel.kind === 'provider') {
      return selectedMethod.provider.id === entry.sel.provider.id
    }
    return false
  }

  const selectedEntryName =
    selectedMethod?.kind === 'provider'
      ? selectedMethod.provider.name
      : (selectedMethod?.method.name ?? '')

  // 各支付方式独立门槛：每个方式只看自己的 min_topup（如 USDT=10 / Waffo=30 各自判断），
  // 不再用单一全局 minTopup 一刀切锁死整个步骤。lowestMethodMin = 任意方式可用的最低金额，仅用于输入提示。
  const lowestMethodMin = methodEntries.length
    ? Math.min(...methodEntries.map((e) => e.minRequired))
    : getMinTopupAmount(topupInfo)
  const selectedMin = selectedMethod
    ? getSelectedMethodMinTopup(selectedMethod, waffoMinTopup, agouMinTopup)
    : 0
  const selectionValid = !!selectedMethod && topupAmount >= selectedMin
  // 金额"可用" = 输入了正数且至少有一个支付方式接受该金额
  const amountUsable = amountEntered && availableEntries.length > 0

  // 步骤态推导：步骤 2 只要输入了金额就解锁（让用户看到各方式可用/不可用），
  // 不因金额低于某个全局门槛而误显"请先选择金额"。
  const step1State = amountUsable ? 'done' : 'active'
  const step2State = !amountEntered
    ? 'locked'
    : selectionValid
      ? 'done'
      : 'active'
  const step3State = amountUsable && selectionValid ? 'active' : 'locked'

  const displayAmount = formatNumber(topupAmount * usdExchangeRate)
  const hasDiscount = discountRate < 1
  // saved 必须与"实付"同源：实付(paymentAmount)是后端按所选方式算的折后价，
  // 原价 = 实付 / 折扣率，省 = 原价 - 实付。旧实现用 priceRatio(线上充值单价)
  // 另算，会与 Crypto 等方式的实付基准错位（实付 $78.4 却显示省 $66.6）。
  const savedAmount =
    hasDiscount && discountRate > 0
      ? paymentAmount / discountRate - paymentAmount
      : 0
  // 输入了金额但没有任何方式接受 → 内联提示真实最低门槛（仅当确有"金额不足"方式，
  // 全是分组禁用时不显示该提示，避免把"分组不可用"误说成"金额不足"）
  const showAmountTooLow =
    amountEntered &&
    availableEntries.length === 0 &&
    unavailableEntries.length > 0

  const payDisabled =
    !selectionValid || calculating || !!initiating || paymentAmount <= 0

  const renderMethodGrid = (entries: MethodEntry[]) => (
    <div className='grid grid-cols-2 gap-1.5 sm:gap-3 lg:grid-cols-3'>
      {entries.map((entry) => (
        <PayMethodCard
          key={entry.key}
          entry={entry}
          available={
            !entry.comingSoon &&
            !entry.groupBlocked &&
            entry.minRequired <= topupAmount
          }
          selected={isEntrySelected(entry)}
          price={methodPrices?.[entry.payType]}
          pricesReady={methodPricesReady}
          formatPrice={formatCurrency}
          onSelect={() => onSelectMethod(entry.sel)}
        />
      ))}
    </div>
  )

  if (loading) {
    return (
      <Card className='gap-0 overflow-hidden py-0'>
        <CardHeader className='border-b p-3 !pb-3 sm:p-5 sm:!pb-5'>
          <Skeleton className='h-6 w-32' />
          <Skeleton className='mt-2 h-4 w-48' />
        </CardHeader>
        <CardContent className='space-y-4 p-3 sm:space-y-6 sm:p-5'>
          <Skeleton className='h-9 w-56' />
          <div className='space-y-3'>
            <Skeleton className='h-5 w-32' />
            <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
              {Array.from({ length: 8 }).map((_, i) => (
                <Skeleton key={i} className='h-[72px] rounded-lg' />
              ))}
            </div>
          </div>
          <div className='space-y-3'>
            <Skeleton className='h-5 w-36' />
            <div className='grid grid-cols-2 gap-3 lg:grid-cols-3'>
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className='h-14 rounded-lg' />
              ))}
            </div>
          </div>
          <Skeleton className='h-11 w-full rounded-lg' />
        </CardContent>
      </Card>
    )
  }

  return (
    <TitledCard
      title={t('Add Funds')}
      description={t('Choose an amount and payment method')}
      icon={<WalletCards className='h-4 w-4' />}
      action={
        onOpenBilling ? (
          <Button
            variant='outline'
            size='sm'
            onClick={onOpenBilling}
            className='w-full gap-2 sm:w-auto'
          >
            <Receipt className='h-4 w-4' />
            {t('Order History')}
          </Button>
        ) : null
      }
    >
      {/* 兑换码：常驻卡片顶部（红线下方），用户无需切换 tab 即可直接使用 */}
      <div className='bg-muted/30 space-y-2.5 rounded-lg border p-3 sm:p-4'>
        <div className='flex items-center gap-2'>
          <Gift className='text-muted-foreground h-4 w-4' />
          <Label htmlFor='redemption-code' className='text-sm font-semibold'>
            {t('Redeem a Code')}
          </Label>
        </div>
        <div className='grid max-w-md grid-cols-[minmax(0,1fr)_auto] gap-2'>
          <Input
            id='redemption-code'
            value={redemptionCode}
            onChange={(e) => onRedemptionCodeChange(e.target.value)}
            placeholder={t('Enter your redemption code')}
            className='bg-background h-9 min-w-0'
          />
          <Button
            onClick={onRedeem}
            disabled={redeeming}
            variant='outline'
            className='h-9 px-4'
          >
            {redeeming && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {t('Redeem')}
          </Button>
        </div>
        {topupLink && (
          <p className='text-muted-foreground text-xs'>
            {t('Need a redemption code?')}{' '}
            <a
              href={topupLink}
              target='_blank'
              rel='noopener noreferrer'
              className='inline-flex items-center gap-1 underline-offset-4 hover:underline'
            >
              {t('Get one here')}
              <ExternalLink className='h-3 w-3' />
            </a>
          </p>
        )}
      </div>

      {/* 在线充值（三步） */}
      <div className='mt-5 space-y-5 border-t pt-5 sm:mt-6 sm:space-y-6 sm:pt-6'>
        <p className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
          {t('Online Top-up')}
        </p>
        {hasConfigurableTopup ? (
          <>
            {/* ① 选择充值金额 */}
            <StepSection
              step={1}
              title={t('Choose Amount')}
              description={t('Pick a preset or enter a custom amount')}
              state={step1State}
            >
              <div className='space-y-3'>
                {presetAmounts.length > 0 && (
                  <div className='grid grid-cols-2 gap-1.5 sm:gap-3 md:grid-cols-4'>
                    {presetAmounts.map((preset, index) => {
                      const discount =
                        preset.discount ||
                        topupInfo?.discount?.[preset.value] ||
                        1.0
                      const fallback = calculatePresetPricing(
                        preset.value,
                        priceRatio,
                        discount,
                        usdExchangeRate
                      )
                      // 优先用后端按选中方式询得的真实价（Crypto/Pancake 单价各异），
                      // 未就绪时回退到线上充值单价估算；Pay 与 Save 同源于 actualPrice。
                      const realPrice = presetPrices?.[preset.value]
                      const actualPrice = realPrice ?? fallback.actualPrice
                      const displayValue = fallback.displayValue
                      const presetHasDiscount = discount < 1.0
                      const presetSaved =
                        presetHasDiscount && discount > 0
                          ? actualPrice / discount - actualPrice
                          : 0
                      return (
                        <Button
                          key={index}
                          variant='outline'
                          className={cn(
                            'hover:border-foreground flex min-h-16 flex-col items-start rounded-lg px-3 py-2.5 text-left whitespace-normal sm:min-h-[72px] sm:p-4',
                            selectedPreset === preset.value
                              ? 'border-foreground bg-foreground/5'
                              : 'border-muted'
                          )}
                          onClick={() => onSelectPreset(preset)}
                        >
                          <div className='flex w-full items-center justify-between'>
                            <div className='text-base font-semibold sm:text-lg'>
                              {formatNumber(displayValue)}
                            </div>
                            {presetHasDiscount && (
                              <div className='text-xs font-medium text-green-600'>
                                {getDiscountLabel(discount)}
                              </div>
                            )}
                          </div>
                          <div className='text-muted-foreground mt-1.5 w-full text-xs sm:mt-2'>
                            Pay {formatCurrency(actualPrice)}
                            {presetHasDiscount && presetSaved > 0 && (
                              <span className='text-green-600'>
                                {' '}
                                • Save {formatCurrency(presetSaved)}
                              </span>
                            )}
                          </div>
                        </Button>
                      )
                    })}
                  </div>
                )}
                <div className='space-y-1.5'>
                  <Label
                    htmlFor='topup-amount'
                    className='text-muted-foreground text-xs'
                  >
                    {t('Custom Amount')}
                  </Label>
                  <Input
                    id='topup-amount'
                    type='number'
                    value={localAmount}
                    onChange={(e) => handleAmountChange(e.target.value)}
                    min={lowestMethodMin}
                    placeholder={`Minimum ${lowestMethodMin}`}
                    className='h-9 max-w-xs text-base'
                  />
                  {showAmountTooLow && (
                    <p className='text-xs text-amber-600 dark:text-amber-400'>
                      {t('No payment method available below {{min}}', {
                        min: lowestMethodMin,
                      })}
                    </p>
                  )}
                </div>
              </div>
            </StepSection>

            {/* ② 选择支付方式 */}
            <StepSection
              step={2}
              title={t('Choose Payment Method')}
              state={step2State}
              lockedHint={t('Select an amount first')}
              hint={
                amountEntered && methodEntries.length > 0 ? (
                  <span className='text-muted-foreground text-xs'>
                    {t('{{available}}/{{total}} methods available', {
                      available: availableEntries.length,
                      total: methodEntries.length,
                    })}
                  </span>
                ) : undefined
              }
            >
              {methodEntries.length > 0 ? (
                <div className='space-y-3'>
                  {/* 部分不可用 → 联动提示条（amber），全部可用 → muted 行内字 */}
                  {amountEntered &&
                    (unavailableEntries.length > 0 ? (
                      <Alert className='border-amber-500/50 [&>svg]:text-amber-600 dark:[&>svg]:text-amber-400'>
                        <Info className='h-4 w-4' />
                        <AlertDescription className='text-amber-700 dark:text-amber-400'>
                          {t(
                            'Available for {{amount}}: {{available}} · {{unavailable}} require a higher amount',
                            {
                              amount: displayAmount,
                              available:
                                availableEntries
                                  .map((e) => e.name)
                                  .join(', ') || '—',
                              unavailable: unavailableEntries
                                .map((e) => e.name)
                                .join(', '),
                            }
                          )}{' '}
                          {t('To pay with {{method}}, choose {{min}} or more', {
                            method: unavailableEntries[0].name,
                            min: unavailableEntries[0].minRequired,
                          })}
                        </AlertDescription>
                      </Alert>
                    ) : (
                      <p className='text-muted-foreground text-xs'>
                        {t(
                          'All {{count}} payment methods available for {{amount}}',
                          {
                            count: methodEntries.length,
                            amount: displayAmount,
                          }
                        )}
                      </p>
                    ))}
                  {/* 支付平台：每个平台一张卡片，卡片内展示其支持的渠道 icon+文字 */}
                  {providerEntries.length > 0 &&
                    renderMethodGrid(providerEntries)}
                  {/* legacy 其他方式（epay/stripe/waffo旧），仅在启用时出现 */}
                  {standardEntries.length > 0 && (
                    <div className='space-y-1.5'>
                      <p className='text-muted-foreground text-xs'>
                        {t('Other payment methods')}
                      </p>
                      {renderMethodGrid(standardEntries)}
                    </div>
                  )}
                  {waffoEntries.length > 0 && (
                    <div className='space-y-1.5'>
                      <p className='text-muted-foreground text-xs'>
                        {t('Waffo Payment')}
                      </p>
                      {renderMethodGrid(waffoEntries)}
                    </div>
                  )}
                </div>
              ) : (
                <Alert>
                  <AlertDescription>
                    {t(
                      'No payment methods available. Please contact administrator.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
            </StepSection>

            {/* ③ 确认支付 */}
            <StepSection
              step={3}
              title={t('Confirm & Pay')}
              state={step3State}
              lockedHint={
                !amountEntered
                  ? t('Select an amount first')
                  : t('Choose a payment method')
              }
            >
              <div className='space-y-2.5'>
                {selectionValid && (
                  <div className='bg-muted/40 flex flex-wrap items-center gap-x-1.5 rounded-md border px-3 py-2 text-sm'>
                    {calculating ? (
                      <Skeleton className='h-5 w-48' />
                    ) : hasDiscount ? (
                      <span>
                        {t(
                          'Top up {{amount}} → pay {{pay}} ({{discount}}, save {{saved}}) · via {{method}}',
                          {
                            amount: displayAmount,
                            pay: formatCurrency(paymentAmount),
                            discount: getDiscountLabel(discountRate),
                            saved: formatCurrency(savedAmount),
                            method: selectedEntryName,
                          }
                        )}
                      </span>
                    ) : (
                      <span>
                        {t('Top up {{amount}} → pay {{pay}} · via {{method}}', {
                          amount: displayAmount,
                          pay: formatCurrency(paymentAmount),
                          method: selectedEntryName,
                        })}
                      </span>
                    )}
                  </div>
                )}
                <Button
                  size='lg'
                  className='h-11 w-full text-base font-semibold'
                  disabled={payDisabled}
                  onClick={onInitiatePayment}
                >
                  {initiating ? (
                    <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                  ) : null}
                  {/* 未就绪时不带金额：清除选中后 paymentAmount 是上一次的陈旧值 */}
                  {calculating
                    ? t('Calculating...')
                    : selectionValid
                      ? t('Pay {{amount}} Now', {
                          amount: formatCurrency(paymentAmount),
                        })
                      : t('Confirm & Pay')}
                </Button>
                {!amountEntered ? (
                  <p className='text-muted-foreground text-center text-xs'>
                    {t('Select an amount first')}
                  </p>
                ) : !selectedMethod ? (
                  <p className='text-muted-foreground text-center text-xs'>
                    {t('Choose a payment method')}
                  </p>
                ) : null}
              </div>
            </StepSection>
          </>
        ) : !enableCreemTopup ? (
          <Alert>
            <AlertDescription>
              {t(
                'Online topup is not enabled. Please use redemption code or contact administrator.'
              )}
            </AlertDescription>
          </Alert>
        ) : null}

        {/* 或直接购买套餐（Creem 固定价格产品，不走金额选择） */}
        {enableCreemTopup &&
          Array.isArray(creemProducts) &&
          creemProducts.length > 0 &&
          onCreemProductSelect && (
            <div
              className={cn(
                'space-y-2.5',
                hasConfigurableTopup && 'border-t pt-4 sm:pt-5'
              )}
            >
              <p className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
                {t('Or buy a package directly')}
              </p>
              <CreemProductsSection
                products={creemProducts}
                onProductSelect={onCreemProductSelect}
              />
            </div>
          )}
      </div>
    </TitledCard>
  )
}

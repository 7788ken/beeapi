import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getSelf } from '@/lib/api'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'
import { SectionPageLayout } from '@/components/layout'
import { BillingHistoryDialog } from './components/dialogs/billing-history-dialog'
import { CreemConfirmDialog } from './components/dialogs/creem-confirm-dialog'
import { PaymentConfirmDialog } from './components/dialogs/payment-confirm-dialog'
import { RechargeFormCard } from './components/recharge-form-card'
import { WalletStatsCard } from './components/wallet-stats-card'
import { DEFAULT_DISCOUNT_RATE } from './constants'
import {
  useTopupInfo,
  usePayment,
  fetchAmountByType,
  useRedemption,
  useCreemPayment,
  useWaffoPayment,
  useWaffoPancakePayment,
  useCryptomusPayment,
  useAgouPayment,
} from './hooks'
import {
  getDefaultPaymentType,
  getMinTopupAmount,
  getSelectedMethodMinTopup,
  isWaffoPancakePayment,
  isCryptomusPayment,
} from './lib'
import type {
  UserWalletData,
  PresetAmount,
  CreemProduct,
  SelectedPayMethod,
} from './types'

interface WalletProps {
  initialShowHistory?: boolean
  paymentSuccess?: boolean
}

export function Wallet(props: WalletProps) {
  const { t } = useTranslation()
  const [user, setUser] = useState<UserWalletData | null>(null)
  const [userLoading, setUserLoading] = useState(true)
  const [topupAmount, setTopupAmount] = useState(0)
  const [selectedPreset, setSelectedPreset] = useState<number | null>(null)
  // 步骤 2 选中的支付方式（standard / waffo 统一 radio 语义），选中≠下单。
  const [selectedMethod, setSelectedMethod] =
    useState<SelectedPayMethod | null>(null)
  const [paymentLoading, setPaymentLoading] = useState<string | null>(null)
  const [confirmDialogOpen, setConfirmDialogOpen] = useState(false)
  const [billingDialogOpen, setBillingDialogOpen] = useState(false)
  const [redemptionCode, setRedemptionCode] = useState('')
  const [creemDialogOpen, setCreemDialogOpen] = useState(false)
  const [selectedCreemProduct, setSelectedCreemProduct] =
    useState<CreemProduct | null>(null)

  const { status } = useStatus()
  const { currency } = useSystemConfig()
  const { topupInfo, presetAmounts, loading: topupLoading } = useTopupInfo()

  // Calculate effective exchange rate - when display type is USD, use rate of 1
  const effectiveUsdExchangeRate = useMemo(() => {
    return currency?.quotaDisplayType === 'USD'
      ? 1
      : currency?.usdExchangeRate || 1
  }, [currency?.quotaDisplayType, currency?.usdExchangeRate])
  const {
    amount: paymentAmount,
    calculating,
    processing,
    calculatePaymentAmount,
    processPayment,
  } = usePayment()
  const { redeeming, redeemCode } = useRedemption()
  const { processing: creemProcessing, processCreemPayment } = useCreemPayment()
  const { processWaffoPayment } = useWaffoPayment()
  const { processing: pancakeProcessing, processWaffoPancakePayment } =
    useWaffoPancakePayment()
  const { processing: cryptomusProcessing, processCryptomusPayment } =
    useCryptomusPayment()
  const { processing: agouProcessing, processAgouPayment } = useAgouPayment()

  // Fetch and refresh user data
  const fetchUser = useCallback(async () => {
    try {
      setUserLoading(true)
      const response = await getSelf()
      if (response.success && response.data) {
        setUser(response.data as UserWalletData)
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

  useEffect(() => {
    if (props.initialShowHistory) {
      setBillingDialogOpen(true)
    }
  }, [props.initialShowHistory])

  const [billingRefreshTick, setBillingRefreshTick] = useState(0)
  const [pollingActive, setPollingActive] = useState(false)

  // 把 prop 一次性触发锁进 local state，避免后续 replaceState 让 prop 翻空导致轮询被打断
  useEffect(() => {
    if (props.paymentSuccess) setPollingActive(true)
  }, [props.paymentSuccess])

  useEffect(() => {
    if (!pollingActive) return
    toast.success(t('Payment received, balance is updating...'))
    // 每 3 秒轮询一次余额 + 账单列表，最多 10 次（30 秒），覆盖 webhook 落库延迟
    let ticks = 0
    const maxTicks = 10
    const interval = setInterval(() => {
      ticks++
      fetchUser()
      setBillingRefreshTick((k) => k + 1)
      if (ticks >= maxTicks) {
        clearInterval(interval)
        setPollingActive(false)
      }
    }, 3000)
    return () => clearInterval(interval)
  }, [pollingActive, fetchUser, t])

  // 清理付款回跳带回来的 query string，避免刷新或回退被重复触发
  useEffect(() => {
    if (props.initialShowHistory || props.paymentSuccess) {
      window.history.replaceState({}, '', window.location.pathname)
    }
  }, [props.initialShowHistory, props.paymentSuccess])

  // Initialize topup amount when topup info is loaded.
  // 默认预选第一个预设金额，进页即有可用/不可用的色彩对照（步骤 2）。
  useEffect(() => {
    if (topupInfo && topupAmount === 0) {
      const firstPreset = presetAmounts[0]?.value
      const initialAmount = firstPreset || getMinTopupAmount(topupInfo)
      setTopupAmount(initialAmount)
      setSelectedPreset(firstPreset ?? null)

      // Calculate initial payment amount with default payment type
      const defaultPaymentType = getDefaultPaymentType(topupInfo)
      calculatePaymentAmount(initialAmount, defaultPaymentType)
    }
  }, [topupInfo, topupAmount, presetAmounts, calculatePaymentAmount])

  // Get current payment type (selected or default)
  const getCurrentPaymentType = useCallback(() => {
    if (selectedMethod?.kind === 'standard') return selectedMethod.method.type
    if (selectedMethod?.kind === 'waffo') return 'waffo'
    if (selectedMethod?.kind === 'sfpay') return 'sfpay'
    if (selectedMethod?.kind === 'provider') return selectedMethod.provider.id
    return getDefaultPaymentType(topupInfo)
  }, [selectedMethod, topupInfo])

  // 询价缓存：同一 (方式,金额) 只打一次后端，避免预设逐档询价 + 反复切换方式
  // 放大请求量触发限流（每页加载 ~档位数×方式数 次询价）。
  const priceCacheRef = useRef<Map<string, number>>(new Map())
  const cachedFetchAmount = useCallback(
    async (amount: number, paymentType: string) => {
      const cacheKey = `${paymentType}:${amount}`
      const hit = priceCacheRef.current.get(cacheKey)
      if (hit != null) return hit
      const value = await fetchAmountByType(amount, paymentType)
      if (value != null) priceCacheRef.current.set(cacheKey, value)
      return value
    },
    []
  )

  // ② 步骤1预设卡片"按选中方式"显示真实价：前端拿不到各方式单价（仅后端有），
  // 故选中方式（未选时用默认方式）变化时，逐预设金额向后端询价并缓存。
  const previewPaymentType = useMemo(
    () => getCurrentPaymentType(),
    [getCurrentPaymentType]
  )
  const [presetPrices, setPresetPrices] = useState<Record<number, number>>({})
  useEffect(() => {
    const amounts = presetAmounts.map((p) => p.value)
    if (amounts.length === 0) {
      setPresetPrices({})
      return
    }
    let cancelled = false
    Promise.all(
      amounts.map(
        async (v) =>
          [v, await cachedFetchAmount(v, previewPaymentType)] as const
      )
    ).then((entries) => {
      if (cancelled) return
      const next: Record<number, number> = {}
      for (const [v, price] of entries) {
        if (price != null) next[v] = price
      }
      setPresetPrices(next)
    })
    return () => {
      cancelled = true
    }
  }, [presetAmounts, previewPaymentType, cachedFetchAmount])

  // ③ 步骤2方式卡片"按当前金额"显示各方式真实实付：每个启用方式各询价一次。
  const previewPayTypes = useMemo(() => {
    // waffo 经典无独立询价接口，逐方式询价会落到 epay 价（不准），故不纳入实付预览
    const types = new Set<string>()
    topupInfo?.pay_methods?.forEach((m) => types.add(m.type))
    // 统一支付平台（Waffo Pancake/Cryptomus/Agou）按 provider.id 各询价一次
    // （同平台各渠道共用一档价，故用平台 id 询价而非逐渠道，避免放大请求量）
    topupInfo?.providers?.forEach((p) => types.add(p.id))
    return Array.from(types)
  }, [topupInfo])
  const [methodPrices, setMethodPrices] = useState<Record<string, number>>({})
  // 首轮询价是否结束：用于区分"加载中"(Skeleton) 与"该方式算不出价"(隐藏 Pay 行)，
  // 避免某方式询价失败时方式卡片永久转圈。
  const [methodPricesReady, setMethodPricesReady] = useState(false)
  useEffect(() => {
    if (!topupAmount || previewPayTypes.length === 0) {
      setMethodPrices({})
      setMethodPricesReady(true)
      return
    }
    let cancelled = false
    setMethodPricesReady(false)
    Promise.all(
      previewPayTypes.map(
        async (type) =>
          [type, await cachedFetchAmount(topupAmount, type)] as const
      )
    ).then((entries) => {
      if (cancelled) return
      const next: Record<string, number> = {}
      for (const [type, price] of entries) {
        if (price != null) next[type] = price
      }
      setMethodPrices(next)
      setMethodPricesReady(true)
    })
    return () => {
      cancelled = true
    }
  }, [topupAmount, previewPayTypes, cachedFetchAmount])

  // 金额变更导致已选方式不满足 min_topup 时自动取消选中并提示原因（B3.3）。
  useEffect(() => {
    if (!selectedMethod) return
    const minRequired = getSelectedMethodMinTopup(
      selectedMethod,
      topupInfo?.waffo_min_topup,
      topupInfo?.sfpay_min_topup
    )
    if (topupAmount < minRequired) {
      const methodName =
        selectedMethod.kind === 'provider'
          ? selectedMethod.provider.name
          : selectedMethod.method.name
      setSelectedMethod(null)
      toast.warning(
        t('{{method}} requires at least {{min}}, selection cleared', {
          method: methodName,
          min: minRequired,
        })
      )
    }
  }, [topupAmount, selectedMethod, topupInfo, t])

  // Handle preset selection
  const handleSelectPreset = (preset: PresetAmount) => {
    setTopupAmount(preset.value)
    setSelectedPreset(preset.value)
    calculatePaymentAmount(preset.value, getCurrentPaymentType())
  }

  // Handle topup amount change
  const handleTopupAmountChange = (amount: number) => {
    setTopupAmount(amount)
    setSelectedPreset(null)
    calculatePaymentAmount(amount, getCurrentPaymentType())
  }

  // 步骤 2：点支付方式 = 仅选中（radio 语义）+ 实时算价，不再直接弹下单确认框。
  const handleSelectMethod = (sel: SelectedPayMethod) => {
    setSelectedMethod(sel)
    const paymentType =
      sel.kind === 'standard'
        ? sel.method.type
        : sel.kind === 'sfpay'
          ? 'sfpay'
          : sel.kind === 'provider'
            ? sel.provider.id
            : 'waffo'
    calculatePaymentAmount(topupAmount, paymentType)
  }

  // 步骤 3：主按钮发起支付。standard 走既有确认弹窗；waffo 保持原直发语义。
  const handleInitiatePayment = async () => {
    if (!selectedMethod) return

    // 统一支付平台：下单后跳转对应收银台（直发，不走确认弹窗，金额已在按钮上明示）。
    // Waffo Pancake 为托管收银台不接受预选渠道；Cryptomus/Agou 把渠道 key 传给后端锁定币种/方式。
    if (selectedMethod.kind === 'provider') {
      const { provider } = selectedMethod
      // 渠道为卡片内展示项；发起支付走平台默认：
      // Waffo Pancake / Cryptomus 跳转收银台由用户自选；Agou 需 payType，用首个启用渠道。
      const defaultChannel = provider.channels[0]
      setPaymentLoading(provider.id)
      try {
        if (provider.id === 'cryptomus') {
          await processCryptomusPayment(topupAmount)
        } else if (provider.id === 'sfpay') {
          await processAgouPayment(topupAmount, defaultChannel?.key ?? '')
        } else {
          await processWaffoPancakePayment(topupAmount)
        }
      } finally {
        setPaymentLoading(null)
      }
      return
    }

    if (selectedMethod.kind === 'waffo') {
      const loadingKey = `waffo-${selectedMethod.index}`
      setPaymentLoading(loadingKey)
      try {
        await processWaffoPayment(topupAmount, selectedMethod.index)
      } finally {
        setPaymentLoading(null)
      }
      return
    }

    // agou：下单后整页跳转收银台（与 waffo 同为直发，不走确认弹窗）。
    if (selectedMethod.kind === 'sfpay') {
      setPaymentLoading('sfpay')
      try {
        await processAgouPayment(topupAmount, selectedMethod.method.type)
      } finally {
        setPaymentLoading(null)
      }
      return
    }

    // 算价已在选中/金额变更时完成，直接复用既有确认弹窗，后续流程零改动。
    setConfirmDialogOpen(true)
  }

  // Handle payment confirmation
  const handlePaymentConfirm = async () => {
    if (selectedMethod?.kind !== 'standard') return
    const paymentType = selectedMethod.method.type

    const isPancake = isWaffoPancakePayment(paymentType)
    const isCryptomus = isCryptomusPayment(paymentType)
    let success = false
    if (isPancake) {
      success = await processWaffoPancakePayment(topupAmount)
    } else if (isCryptomus) {
      success = await processCryptomusPayment(topupAmount)
    } else {
      success = await processPayment(topupAmount, paymentType)
    }

    if (success) {
      setConfirmDialogOpen(false)
      await fetchUser()
    }
  }

  // Handle redemption
  const handleRedeem = async () => {
    if (!redemptionCode) return

    const success = await redeemCode(redemptionCode)
    if (success) {
      setRedemptionCode('')
      await fetchUser()
    }
  }

  // Handle Creem product selection
  const handleCreemProductSelect = (product: CreemProduct) => {
    setSelectedCreemProduct(product)
    setCreemDialogOpen(true)
  }

  // Handle Creem payment confirmation
  const handleCreemConfirm = async () => {
    if (!selectedCreemProduct) return

    const success = await processCreemPayment(selectedCreemProduct.productId)
    if (success) {
      setCreemDialogOpen(false)
      setSelectedCreemProduct(null)
      await fetchUser()
    }
  }

  // Get discount rate for current topup amount
  const getDiscountRate = useCallback(() => {
    return topupInfo?.discount?.[topupAmount] || DEFAULT_DISCOUNT_RATE
  }, [topupInfo, topupAmount])

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Wallet')}</SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {t('Manage your balance and payment methods')}
        </SectionPageLayout.Description>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
            <WalletStatsCard user={user} loading={userLoading} />

            <div id='wallet-add-funds' className='scroll-mt-4'>
              <RechargeFormCard
                topupInfo={topupInfo}
                presetAmounts={presetAmounts}
                selectedPreset={selectedPreset}
                onSelectPreset={handleSelectPreset}
                topupAmount={topupAmount}
                onTopupAmountChange={handleTopupAmountChange}
                paymentAmount={paymentAmount}
                calculating={calculating}
                presetPrices={presetPrices}
                methodPrices={methodPrices}
                methodPricesReady={methodPricesReady}
                selectedMethod={selectedMethod}
                onSelectMethod={handleSelectMethod}
                onInitiatePayment={handleInitiatePayment}
                initiating={
                  processing ||
                  pancakeProcessing ||
                  cryptomusProcessing ||
                  agouProcessing ||
                  !!paymentLoading
                }
                discountRate={getDiscountRate()}
                redemptionCode={redemptionCode}
                onRedemptionCodeChange={setRedemptionCode}
                onRedeem={handleRedeem}
                redeeming={redeeming}
                topupLink={topupInfo?.topup_link}
                loading={topupLoading}
                priceRatio={(status?.price as number) || 1}
                usdExchangeRate={effectiveUsdExchangeRate}
                onOpenBilling={() => setBillingDialogOpen(true)}
                creemProducts={topupInfo?.creem_products}
                enableCreemTopup={topupInfo?.enable_creem_topup}
                onCreemProductSelect={handleCreemProductSelect}
                enableWaffoTopup={topupInfo?.enable_waffo_topup}
                waffoPayMethods={topupInfo?.waffo_pay_methods}
                waffoMinTopup={topupInfo?.waffo_min_topup}
                enableWaffoPancakeTopup={
                  topupInfo?.enable_waffo_pancake_topup
                }
                enableCryptomusTopup={topupInfo?.enable_cryptomus_topup}
                enableAgouTopup={topupInfo?.enable_sfpay_topup}
                agouPayMethods={topupInfo?.sfpay_pay_methods}
                agouMinTopup={topupInfo?.sfpay_min_topup}
                providers={topupInfo?.providers}
              />
            </div>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <PaymentConfirmDialog
        open={confirmDialogOpen}
        onOpenChange={setConfirmDialogOpen}
        onConfirm={handlePaymentConfirm}
        topupAmount={topupAmount}
        paymentAmount={paymentAmount}
        paymentMethod={
          selectedMethod?.kind === 'standard'
            ? selectedMethod.method
            : undefined
        }
        calculating={calculating}
        processing={processing || pancakeProcessing || cryptomusProcessing}
        discountRate={getDiscountRate()}
        usdExchangeRate={effectiveUsdExchangeRate}
      />

      <BillingHistoryDialog
        open={billingDialogOpen}
        onOpenChange={setBillingDialogOpen}
        refreshTick={billingRefreshTick}
      />

      <CreemConfirmDialog
        open={creemDialogOpen}
        onOpenChange={setCreemDialogOpen}
        onConfirm={handleCreemConfirm}
        product={selectedCreemProduct}
        processing={creemProcessing}
      />
    </>
  )
}

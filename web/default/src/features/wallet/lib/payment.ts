import {
  PAYMENT_TYPES,
  DEFAULT_PRESET_MULTIPLIERS,
  DEFAULT_PAYMENT_TYPE,
  DEFAULT_MIN_TOPUP,
} from '../constants'
import type { PresetAmount, SelectedPayMethod, TopupInfo } from '../types'

// ============================================================================
// Payment Processing Functions
// ============================================================================

/**
 * Check if browser is Safari
 */
function isSafariBrowser(): boolean {
  return (
    navigator.userAgent.indexOf('Safari') > -1 &&
    navigator.userAgent.indexOf('Chrome') < 1
  )
}

/**
 * Submit payment form (for non-Stripe payments)
 */
export function submitPaymentForm(
  url: string,
  params: Record<string, unknown>
): void {
  const form = document.createElement('form')
  form.action = url
  form.method = 'POST'

  // Don't open in new tab for Safari
  if (!isSafariBrowser()) {
    form.target = '_blank'
  }

  // Add form parameters
  Object.entries(params).forEach(([key, value]) => {
    const input = document.createElement('input')
    input.type = 'hidden'
    input.name = key
    input.value = String(value)
    form.appendChild(input)
  })

  document.body.appendChild(form)
  form.submit()
  document.body.removeChild(form)
}

/**
 * Check if payment method is Stripe
 */
export function isStripePayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.STRIPE
}

/**
 * Check if payment method is Waffo Pancake
 *
 * Pancake is a metered-style payment that goes through a dedicated checkout
 * URL flow rather than the generic epay form submission, so it must be
 * special-cased in payment dispatch logic.
 */
export function isWaffoPancakePayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.WAFFO_PANCAKE
}

/**
 * Check if payment method is Cryptomus (crypto USDT/BTC/ETH)
 *
 * Cryptomus 走自己的托管收银台 URL，跟 pancake 一样要特判。
 */
export function isCryptomusPayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.CRYPTOMUS
}

/**
 * Check if payment method is agou (支付宝/微信).
 *
 * agou 走自己的 /agou/amount 询价与 /agou/pay 收银台跳转，用合成 key 'sfpay'
 * 做分发（其下方式以 alipay/wxpay 渲染图标，但价格与渠道由 agou 后端决定）。
 */
export function isAgouPayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.AGOU
}

/**
 * Get default payment type from topup info
 */
export function getDefaultPaymentType(topupInfo: TopupInfo | null): string {
  if (!topupInfo) {
    return DEFAULT_PAYMENT_TYPE
  }

  // Return first available payment method or default
  if (topupInfo.pay_methods?.length > 0) {
    return topupInfo.pay_methods[0].type
  }

  if (topupInfo.enable_stripe_topup) {
    return PAYMENT_TYPES.STRIPE
  }

  if (topupInfo.enable_waffo_topup) {
    return PAYMENT_TYPES.WAFFO
  }

  if (topupInfo.enable_waffo_pancake_topup) {
    return PAYMENT_TYPES.WAFFO_PANCAKE
  }

  if (topupInfo.enable_cryptomus_topup) {
    return PAYMENT_TYPES.CRYPTOMUS
  }

  if (topupInfo.enable_sfpay_topup) {
    return PAYMENT_TYPES.AGOU
  }

  return DEFAULT_PAYMENT_TYPE
}

/**
 * 步骤 2 选中项的最低充值额。失效自动取消（wallet/index.tsx）与方式卡片
 * 可用性判断（recharge-form-card.tsx）共用同一规则，避免两处实现漂移。
 */
export function getSelectedMethodMinTopup(
  sel: SelectedPayMethod,
  waffoMinTopup: number | undefined,
  agouMinTopup?: number | undefined
): number {
  if (sel.kind === 'standard') return sel.method.min_topup || 0
  if (sel.kind === 'sfpay') return agouMinTopup || 0
  if (sel.kind === 'provider') return sel.provider.min_topup || 0
  return waffoMinTopup || 0
}

/**
 * Get minimum topup amount from topup info
 */
export function getMinTopupAmount(topupInfo: TopupInfo | null): number {
  if (!topupInfo) {
    return DEFAULT_MIN_TOPUP
  }

  if (topupInfo.enable_online_topup) {
    return topupInfo.min_topup
  }

  if (topupInfo.enable_stripe_topup) {
    return topupInfo.stripe_min_topup
  }

  if (topupInfo.enable_waffo_topup) {
    return topupInfo.waffo_min_topup || DEFAULT_MIN_TOPUP
  }

  if (topupInfo.enable_waffo_pancake_topup) {
    return topupInfo.waffo_pancake_min_topup || DEFAULT_MIN_TOPUP
  }

  if (topupInfo.enable_cryptomus_topup) {
    return topupInfo.cryptomus_min_topup || DEFAULT_MIN_TOPUP
  }

  if (topupInfo.enable_sfpay_topup) {
    return topupInfo.sfpay_min_topup || DEFAULT_MIN_TOPUP
  }

  return DEFAULT_MIN_TOPUP
}

/**
 * Generate preset amounts based on minimum topup
 */
export function generatePresetAmounts(minAmount: number): PresetAmount[] {
  return DEFAULT_PRESET_MULTIPLIERS.map((multiplier) => ({
    value: minAmount * multiplier,
  }))
}

/**
 * Merge custom preset amounts with discounts
 */
export function mergePresetAmounts(
  amountOptions: number[],
  discounts: Record<number, number>
): PresetAmount[] {
  if (!amountOptions || amountOptions.length === 0) {
    return []
  }

  return amountOptions.map((amount) => ({
    value: amount,
    discount: discounts[amount] || 1.0,
  }))
}

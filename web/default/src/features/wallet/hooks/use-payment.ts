import { useState, useCallback } from 'react'
import i18next from 'i18next'
import { toast } from 'sonner'
import {
  calculateAmount,
  calculateStripeAmount,
  calculateWaffoPancakeAmount,
  calculateCryptomusAmount,
  calculateAgouAmount,
  requestPayment,
  requestStripePayment,
  isApiSuccess,
} from '../api'
import {
  isStripePayment,
  isWaffoPancakePayment,
  isCryptomusPayment,
  isAgouPayment,
  submitPaymentForm,
} from '../lib'

// ============================================================================
// Payment Hook
// ============================================================================

/**
 * 按支付方式向后端询价（单价/分组倍率/折扣都在后端算）。
 * 抽成 module 级纯函数，供单笔算价与批量预览（各预设金额 / 各方式）复用，
 * 避免每个支付方式的 calculate 接口 dispatch 逻辑在多处漂移。
 * 失败返回 null（由调用方决定回退展示），不抛错。
 */
export async function fetchAmountByType(
  topupAmount: number,
  paymentType: string
): Promise<number | null> {
  const isStripe = isStripePayment(paymentType)
  const isPancake = isWaffoPancakePayment(paymentType)
  const isCryptomus = isCryptomusPayment(paymentType)
  const isAgou = isAgouPayment(paymentType)
  try {
    const response = isStripe
      ? await calculateStripeAmount({ amount: topupAmount })
      : isPancake
        ? await calculateWaffoPancakeAmount({ amount: topupAmount })
        : isCryptomus
          ? await calculateCryptomusAmount({ amount: topupAmount })
          : isAgou
            ? await calculateAgouAmount({ amount: topupAmount })
            : await calculateAmount({ amount: topupAmount })

    if (isApiSuccess(response) && response.data) {
      const value = parseFloat(response.data)
      return Number.isFinite(value) ? value : null
    }
  } catch (_error) {
    // 算价失败静默回退，不打断 UI
  }
  return null
}

export function usePayment() {
  const [amount, setAmount] = useState<number>(0)
  const [calculating, setCalculating] = useState(false)
  const [processing, setProcessing] = useState(false)

  // Calculate payment amount
  const calculatePaymentAmount = useCallback(
    async (topupAmount: number, paymentType: string) => {
      try {
        setCalculating(true)
        const value = await fetchAmountByType(topupAmount, paymentType)
        const calculatedAmount = value ?? 0
        setAmount(calculatedAmount)
        return calculatedAmount
      } finally {
        setCalculating(false)
      }
    },
    []
  )

  // Process payment
  const processPayment = useCallback(
    async (topupAmount: number, paymentType: string) => {
      try {
        setProcessing(true)

        const isStripe = isStripePayment(paymentType)
        const amount = Math.floor(topupAmount)

        const response = isStripe
          ? await requestStripePayment({
              amount,
              payment_method: 'stripe',
            })
          : await requestPayment({
              amount,
              payment_method: paymentType,
            })

        if (!isApiSuccess(response)) {
          toast.error(response.message || i18next.t('Payment request failed'))
          return false
        }

        // Handle Stripe payment
        if (isStripe && response.data?.pay_link) {
          window.open(response.data.pay_link as string, '_blank')
          toast.success(i18next.t('Redirecting to payment page...'))
          return true
        }

        // Handle non-Stripe payment
        if (!isStripe && response.data) {
          const url = (response as unknown as { url?: string }).url
          if (url) {
            submitPaymentForm(url, response.data)
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }
        }

        return false
      } catch (_error) {
        toast.error(i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    []
  )

  return {
    amount,
    calculating,
    processing,
    calculatePaymentAmount,
    processPayment,
    setAmount,
  }
}

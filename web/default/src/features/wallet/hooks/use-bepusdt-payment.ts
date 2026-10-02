import { useState, useCallback } from 'react'
import i18next from 'i18next'
import { toast } from 'sonner'
import { requestBepusdtPayment, isApiSuccess } from '../api'

function getCheckoutUrl(data: unknown): string | null {
  if (!data || typeof data !== 'object') {
    return null
  }
  if ('checkout_url' in data && typeof data.checkout_url === 'string') {
    return data.checkout_url
  }
  return null
}

function isSafeHttpCheckoutUrl(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) {
    return false
  }
  try {
    const u = new URL(trimmed)
    return u.protocol === 'http:' || u.protocol === 'https:'
  } catch {
    return false
  }
}

function getErrorMessage(message: string | undefined, data: unknown): string {
  if (typeof data === 'string' && data.trim()) {
    return data
  }
  return message || i18next.t('Payment request failed')
}

/**
 * Hook for BEpusdt（自建 USDT 收款网关）支付
 *
 * 后端在网关建单后返回 checkout_url，新开标签页跳到网关收银台，
 * 用户在收银台自选 USDT 的链（TRC20 / BEP20 / ERC20 / Arbitrum / Solana）付款。
 */
export function useBepusdtPayment() {
  const [processing, setProcessing] = useState(false)

  const processBepusdtPayment = useCallback(async (topupAmount: number) => {
    setProcessing(true)

    try {
      const response = await requestBepusdtPayment({
        amount: Math.floor(topupAmount),
      })

      if (isApiSuccess(response)) {
        const checkoutUrl = getCheckoutUrl(response.data)
        if (checkoutUrl) {
          if (!isSafeHttpCheckoutUrl(checkoutUrl)) {
            toast.error(i18next.t('Invalid payment redirect URL'))
            return false
          }
          window.open(checkoutUrl, '_blank', 'noopener,noreferrer')
          toast.success(i18next.t('Redirecting to payment page...'))
          return true
        }
      }

      toast.error(getErrorMessage(response.message, response.data))
      return false
    } catch (_error) {
      toast.error(i18next.t('Payment request failed'))
      return false
    } finally {
      setProcessing(false)
    }
  }, [])

  return { processing, processBepusdtPayment }
}

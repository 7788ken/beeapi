import { useState, useCallback } from 'react'
import i18next from 'i18next'
import { toast } from 'sonner'
import { requestCryptomusPayment, isApiSuccess } from '../api'

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
 * Hook for Cryptomus 数字货币支付
 *
 * 后端创建 cryptomus invoice 后返回 checkout_url，新开标签页跳到
 * cryptomus 托管收银台让用户付 USDT/BTC 等。
 */
export function useCryptomusPayment() {
  const [processing, setProcessing] = useState(false)

  const processCryptomusPayment = useCallback(async (topupAmount: number, channelKey?: string) => {
    setProcessing(true)

    try {
      const response = await requestCryptomusPayment({
        amount: Math.floor(topupAmount),
        channel_key: channelKey,
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

  return { processing, processCryptomusPayment }
}

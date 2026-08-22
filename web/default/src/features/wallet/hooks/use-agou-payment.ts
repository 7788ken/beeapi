import { useState, useCallback } from 'react'
import i18next from 'i18next'
import { toast } from 'sonner'
import { requestAgouPayment, isApiSuccess } from '../api'

function getRedirectUrl(data: unknown): string | null {
  if (!data || typeof data !== 'object') {
    return null
  }
  if ('url' in data && typeof data.url === 'string') {
    return data.url
  }
  return null
}

function isSafeHttpUrl(value: string): boolean {
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
 * Hook for agou（支付宝/微信）支付
 *
 * 后端 /agou/pay 下单后返回收银台 url，整页跳转（window.location.href）让用户
 * 完成付款；付款结果由 agou 回调入账。payType 传前端方式 type（alipay/wxpay），
 * 后端映射为 agou 真实 payType。
 */
export function useAgouPayment() {
  const [processing, setProcessing] = useState(false)

  const processAgouPayment = useCallback(
    async (topupAmount: number, channelKey: string) => {
      setProcessing(true)

      try {
        const response = await requestAgouPayment({
          amount: Math.floor(topupAmount),
          channel_key: channelKey,
        })

        if (isApiSuccess(response)) {
          const url = getRedirectUrl(response.data)
          if (url) {
            if (!isSafeHttpUrl(url)) {
              toast.error(i18next.t('Invalid payment redirect URL'))
              return false
            }
            toast.success(i18next.t('Redirecting to payment page...'))
            window.location.href = url
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
    },
    []
  )

  return { processing, processAgouPayment }
}

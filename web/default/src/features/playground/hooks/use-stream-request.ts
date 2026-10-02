import { useCallback, useRef } from 'react'
import { SSE, type ReadyStateEvent, type SSEvent } from 'sse.js'
import { parseRelayError, relayAuthHeaders } from '@/lib/relay-client'
import { API_ENDPOINTS, ERROR_MESSAGES } from '../constants'
import type { ChatCompletionRequest, ChatCompletionChunk } from '../types'

type StreamUpdate = (type: 'reasoning' | 'content', chunk: string) => void
type StreamError = (error: string, errorCode?: string) => void

/**
 * 流式对话：带所选 API Key 直连 /v1/chat/completions，不带控制台登录凭证。
 * 每路请求只回调一次结束：收到 [DONE]、报错、或连接自己关闭，以先到者为准。
 */
export function useStreamRequest() {
  const sseSourceRef = useRef<SSE | null>(null)

  const sendStreamRequest = useCallback(
    (
      secret: string,
      payload: ChatCompletionRequest,
      onUpdate: StreamUpdate,
      onComplete: () => void,
      onError: StreamError
    ) => {
      const source = new SSE(API_ENDPOINTS.CHAT_COMPLETIONS, {
        headers: {
          'Content-Type': 'application/json',
          ...relayAuthHeaders(secret),
        },
        method: 'POST',
        payload: JSON.stringify(payload),
        start: false,
      })
      sseSourceRef.current = source

      // 被停止或已经收尾后，这一路后续的事件一律不再处理
      const isCurrent = () => sseSourceRef.current === source
      let received = false
      const settle = (notify: () => void) => {
        if (!isCurrent()) return
        sseSourceRef.current = null
        source.close()
        notify()
      }

      source.addEventListener('message', (e: SSEvent) => {
        if (!isCurrent()) return
        if (e.data === '[DONE]') {
          settle(onComplete)
          return
        }

        let chunk: ChatCompletionChunk
        try {
          chunk = JSON.parse(e.data)
        } catch {
          settle(() => onError(ERROR_MESSAGES.PARSE_ERROR))
          return
        }
        const delta = chunk.choices?.[0]?.delta
        if (delta?.reasoning_content) {
          received = true
          onUpdate('reasoning', delta.reasoning_content)
        }
        if (delta?.content) {
          received = true
          onUpdate('content', delta.content)
        }
      })

      // 非 2xx 时 data 是 /v1 的错误体 {error:{message,code}}；状态码 0 是网络没连上
      source.addEventListener('error', (e: SSEvent) => {
        const status = e.responseCode ?? 0
        settle(() => {
          // 状态码 0 是网络断开：已经收到内容就按已收到的收尾（与断流规则一致），否则报网络错误
          if (status === 0) {
            if (received) onComplete()
            else onError(ERROR_MESSAGES.NETWORK_ERROR)
            return
          }
          const error = parseRelayError(status, String(e.data ?? ''))
          onError(error.message, error.code)
        })
      })

      // 没发 [DONE] 就断开：收到过内容就按已收到的收尾，一点没收到算中断（与刷新时恢复未完成消息的规则一致）
      source.addEventListener('readystatechange', (e: ReadyStateEvent) => {
        if (e.readyState !== SSE.CLOSED) return
        settle(
          received ? onComplete : () => onError(ERROR_MESSAGES.INTERRUPTED)
        )
      })

      try {
        source.stream()
      } catch {
        settle(() => onError(ERROR_MESSAGES.STREAM_START_ERROR))
      }
    },
    []
  )

  const stopStream = useCallback(() => {
    const source = sseSourceRef.current
    if (!source) return
    sseSourceRef.current = null
    source.close()
  }, [])

  // eslint-disable-next-line react-hooks/refs
  const isStreaming = sseSourceRef.current !== null

  return {
    sendStreamRequest,
    stopStream,
    // eslint-disable-next-line react-hooks/refs
    isStreaming,
  }
}

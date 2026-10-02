import { relayRequest } from '@/lib/relay-client'
import { API_ENDPOINTS } from './constants'
import type { ChatCompletionRequest, ChatCompletionResponse } from './types'

/**
 * 非流式对话：带所选 API Key 直连 /v1，非 2xx 抛 RelayError。
 */
export function sendChatCompletion(
  secret: string,
  payload: ChatCompletionRequest
): Promise<ChatCompletionResponse> {
  return relayRequest<ChatCompletionResponse>(
    secret,
    API_ENDPOINTS.CHAT_COMPLETIONS,
    { json: payload }
  )
}

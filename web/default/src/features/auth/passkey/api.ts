import { api } from '@/lib/api'
import type { ApiResponse, PasskeyOptionsPayload, PasskeyStatus } from './types'

export async function getPasskeyStatus(): Promise<ApiResponse<PasskeyStatus>> {
  const res = await api.get<ApiResponse<PasskeyStatus>>('/api/user/passkey')
  return res.data
}

export async function beginPasskeyRegistration(
  proof?: string
): Promise<ApiResponse<PasskeyOptionsPayload>> {
  const res = await api.post<ApiResponse<PasskeyOptionsPayload>>(
    '/api/user/passkey/register/begin',
    undefined,
    proof ? { headers: { 'X-Security-Proof': proof } } : undefined
  )
  return res.data
}

export async function finishPasskeyRegistration(
  payload: Record<string, unknown>,
  flowToken: string,
  proof?: string
): Promise<ApiResponse> {
  const res = await api.post<ApiResponse>(
    '/api/user/passkey/register/finish',
    payload,
    {
      headers: {
        'X-Auth-Flow': flowToken,
        ...(proof ? { 'X-Security-Proof': proof } : {}),
      },
    }
  )
  return res.data
}

export async function deletePasskey(proof: string): Promise<ApiResponse> {
  const res = await api.delete<ApiResponse>('/api/user/passkey', {
    headers: { 'X-Security-Proof': proof },
  })
  return res.data
}

export async function beginPasskeyLogin(): Promise<
  ApiResponse<PasskeyOptionsPayload>
> {
  const res = await api.post<ApiResponse<PasskeyOptionsPayload>>(
    '/api/user/passkey/login/begin'
  )
  return res.data
}

export async function finishPasskeyLogin(
  payload: Record<string, unknown>,
  flowToken: string
): Promise<ApiResponse> {
  const res = await api.post<ApiResponse>(
    '/api/user/passkey/login/finish',
    payload,
    { headers: { 'X-Auth-Flow': flowToken } }
  )
  return res.data
}

export async function beginPasskeyVerification(
  scope: string
): Promise<ApiResponse<PasskeyOptionsPayload>> {
  const res = await api.post<ApiResponse<PasskeyOptionsPayload>>(
    '/api/user/passkey/verify/begin',
    undefined,
    { params: { scope } }
  )
  return res.data
}

export async function finishPasskeyVerification(
  payload: Record<string, unknown>,
  flowToken: string
): Promise<ApiResponse<{ proof?: string }>> {
  const res = await api.post<ApiResponse<{ proof?: string }>>(
    '/api/user/passkey/verify/finish',
    payload,
    { headers: { 'X-Auth-Flow': flowToken } }
  )
  return res.data
}

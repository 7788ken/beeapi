import type { AxiosError } from 'axios'

export interface VerificationRequiredInfo {
  code?: string
  message: string
  required: boolean
  scope?: string
}

/**
 * Determine whether an Axios error indicates secure verification is required.
 */
export function isVerificationRequiredError(
  error: unknown
): error is AxiosError {
  if (!error || typeof error !== 'object') return false
  const axiosError = error as AxiosError<{ code?: string }>
  const status = axiosError.response?.status
  if (status !== 403) return false

  const code = axiosError.response?.data?.code
  if (!code) return false

  const verificationCodes = new Set([
    'SECURITY_PROOF_REQUIRED',
    'SECURITY_PROOF_EXPIRED',
    'SECURITY_PROOF_INVALID',
    'SECURITY_PROOF_SCOPE_MISMATCH',
    'SECURITY_PROOF_METHOD_MISMATCH',
  ])

  return verificationCodes.has(code)
}

/**
 * Extract verification requirement info from an Axios error.
 */
export function extractVerificationInfo(
  error: unknown
): VerificationRequiredInfo {
  const axiosError = error as AxiosError<{
    code?: string
    message?: string
    scope?: string
  }>
  const code = axiosError.response?.data?.code
  const message =
    axiosError.response?.data?.message ?? 'Secure verification is required'

  return {
    code,
    message,
    required: true,
    scope: axiosError.response?.data?.scope,
  }
}

import { api, get2FAStatus } from '@/lib/api'
import {
  buildAssertionResult,
  prepareCredentialRequestOptions,
  isPasskeySupported as detectPasskeySupport,
} from '@/lib/passkey'
import {
  beginPasskeyVerification,
  finishPasskeyVerification,
  getPasskeyStatus,
} from '../passkey'
import type { VerificationMethod, VerificationMethods } from './types'

/**
 * Fetch available verification methods for the current user.
 */
export async function checkVerificationMethods(): Promise<VerificationMethods> {
  try {
    const [twoFAResponse, passkeyResponse, passkeySupported] =
      await Promise.all([
        get2FAStatus(),
        getPasskeyStatus(),
        detectPasskeySupport(),
      ])

    const has2FA =
      Boolean(twoFAResponse?.success) && Boolean(twoFAResponse?.data?.enabled)
    const hasPasskey =
      Boolean(passkeyResponse?.success) &&
      Boolean(passkeyResponse?.data?.enabled)

    return {
      has2FA,
      hasPasskey,
      passkeySupported,
    }
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('[Secure Verification] Failed to check methods', error)
    return {
      has2FA: false,
      hasPasskey: false,
      passkeySupported: false,
    }
  }
}

/**
 * Execute a verification flow based on the method type.
 */
export async function verify(
  method: VerificationMethod,
  scope: string,
  code?: string
): Promise<string> {
  switch (method) {
    case '2fa':
      return verifyTwoFA(scope, code)
    case 'passkey':
      return verifyPasskey(scope)
    default:
      throw new Error(`Unsupported verification method: ${method}`)
  }
}

/**
 * Perform 2FA verification flow.
 */
async function verifyTwoFA(
  scope: string,
  code?: string | null
): Promise<string> {
  const trimmed = code?.trim()
  if (!trimmed) {
    throw new Error('Please enter the verification code or backup code')
  }

  const res = await api.post('/api/verify', {
    method: '2fa',
    code: trimmed,
    scope,
  })

  if (!res.data?.success) {
    throw new Error(res.data?.message || 'Verification failed')
  }
  const proof = res.data?.data?.proof
  if (typeof proof !== 'string' || !proof) {
    throw new Error('Verification proof was not returned')
  }
  return proof
}

/**
 * Perform Passkey verification flow.
 */
async function verifyPasskey(scope: string): Promise<string> {
  if (typeof navigator === 'undefined' || !navigator.credentials) {
    throw new Error('Passkey verification is not supported in this environment')
  }

  try {
    const beginResponse = await beginPasskeyVerification(scope)
    if (!beginResponse.success) {
      throw new Error(beginResponse.message || 'Failed to start verification')
    }

    const publicKey = prepareCredentialRequestOptions(
      beginResponse.data?.options ?? beginResponse.data
    )

    const credential = (await navigator.credentials.get({
      publicKey,
    })) as PublicKeyCredential | null

    if (!credential) {
      throw new Error('Passkey verification was cancelled')
    }

    const assertion = buildAssertionResult(credential)
    if (!assertion) {
      throw new Error('Unable to build Passkey assertion')
    }

    const flowToken = beginResponse.data?.flow_token
    if (!flowToken) {
      throw new Error('Passkey verification flow was not returned')
    }
    const finishResponse = await finishPasskeyVerification(assertion, flowToken)
    if (!finishResponse.success) {
      throw new Error(finishResponse.message || 'Passkey verification failed')
    }

    const proof = finishResponse.data?.proof
    if (typeof proof !== 'string' || !proof) {
      throw new Error('Passkey verification proof was not returned')
    }
    return proof
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === 'NotAllowedError') {
      throw new Error('Passkey verification was cancelled or timed out', {
        cause: error,
      })
    }
    if (error instanceof DOMException && error.name === 'InvalidStateError') {
      throw new Error(
        'Passkey verification is not available in the current state',
        { cause: error }
      )
    }
    throw error
  }
}

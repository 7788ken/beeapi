import axios from 'axios'
import type { AxiosRequestConfig } from 'axios'
import i18next from 'i18next'
import { toast } from 'sonner'
import { useAuthStore } from '@/stores/auth-store'

// ============================================================================
// Axios Instance Configuration
// ============================================================================

// Base URL: empty string for same-origin API requests
const baseURL = ''

// Create axios instance with default config
export const api = axios.create({
  baseURL,
  withCredentials: true, // Include cookies in cross-origin requests
  headers: {
    'Cache-Control': 'no-store', // Prevent caching
  },
})

let accessToken: string | null = null
let accessTokenExpiresAt = 0
let refreshPromise: Promise<void> | null = null
const refreshLockName = 'new-api:dashboard-auth-refresh'
const refreshLeaseKey = `${refreshLockName}:lease`
const refreshChannel =
  typeof BroadcastChannel === 'undefined'
    ? null
    : new BroadcastChannel(refreshLockName)

type DashboardAuthRequestConfig = AxiosRequestConfig & {
  dashboardAuthRetried?: boolean
}

function setAccessToken(
  token: string | null,
  broadcast = false,
  expiresAt = 0
) {
  accessToken = token
  accessTokenExpiresAt = token ? expiresAt : 0
  if (broadcast) {
    refreshChannel?.postMessage({
      type: token ? 'token' : 'logout',
      token,
      expiresAt: accessTokenExpiresAt,
    })
  }
}

refreshChannel?.addEventListener('message', (event) => {
  if (event.data?.type === 'token' && typeof event.data.token === 'string') {
    setAccessToken(event.data.token, false, Number(event.data.expiresAt) || 0)
  } else if (event.data?.type === 'logout') {
    setAccessToken(null)
  }
})

function hasPersistedDashboardUser(): boolean {
  return (
    typeof window !== 'undefined' &&
    window.localStorage.getItem('user') !== null
  )
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds))
}

function clearExpiredAccessToken() {
  if (
    accessToken &&
    accessTokenExpiresAt > 0 &&
    accessTokenExpiresAt <= Math.floor(Date.now() / 1000) + 5
  ) {
    setAccessToken(null)
  }
}

async function withCrossTabRefreshLock(refresh: () => Promise<void>) {
  if (typeof navigator !== 'undefined' && navigator.locks) {
    await navigator.locks.request(refreshLockName, async () => {
      if (!accessToken) await refresh()
    })
    return
  }

  const owner =
    typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random()}`
  for (;;) {
    if (accessToken) return
    const now = Date.now()
    let lease: { owner?: string; expiresAt?: number } | null = null
    try {
      lease = JSON.parse(window.localStorage.getItem(refreshLeaseKey) || 'null')
    } catch {
      lease = null
    }
    if (!lease?.expiresAt || lease.expiresAt <= now) {
      window.localStorage.setItem(
        refreshLeaseKey,
        JSON.stringify({ owner, expiresAt: now + 15_000 })
      )
      await delay(25)
      const claimed = JSON.parse(
        window.localStorage.getItem(refreshLeaseKey) || 'null'
      )
      if (claimed?.owner === owner) {
        try {
          if (!accessToken) await refresh()
          return
        } finally {
          const current = JSON.parse(
            window.localStorage.getItem(refreshLeaseKey) || 'null'
          )
          if (current?.owner === owner) {
            window.localStorage.removeItem(refreshLeaseKey)
          }
        }
      }
    }
    await delay(75)
  }
}

async function refreshDashboardAccessToken(): Promise<void> {
  refreshPromise ??= withCrossTabRefreshLock(async () => {
    const response = await axios.post('/api/user/refresh', undefined, {
      withCredentials: true,
    })
    const token = response?.data?.data?.access_token
    if (
      response?.data?.success !== true ||
      typeof token !== 'string' ||
      !token
    ) {
      throw new Error(response?.data?.message || 'refresh failed')
    }
    setAccessToken(
      token,
      true,
      Number(response?.data?.data?.access_expires_at) || 0
    )
  }).finally(() => {
    refreshPromise = null
  })
  try {
    await refreshPromise
  } catch (error) {
    if (isTerminalDashboardAuthError(error)) {
      setAccessToken(null, true)
      useAuthStore.getState().auth.reset()
    }
    throw error
  }
}

export function isTerminalDashboardAuthError(error: unknown): boolean {
  const authError = error as {
    response?: { status?: number }
    config?: { url?: string }
  }
  const status = authError?.response?.status
  const isRefreshRequest = authError?.config?.url?.endsWith('/api/user/refresh')
  return status === 401 || (isRefreshRequest === true && status === 403)
}

function isDashboardAuthResponse(url = ''): boolean {
  return (
    url.includes('/api/user/login') ||
    url.includes('/api/user/register') ||
    url.includes('/api/user/passkey/login/finish') ||
    url.includes('/api/user/refresh') ||
    url.includes('/api/oauth/')
  )
}

api.interceptors.request.use(async (config) => {
  const url = config.url ?? ''
  clearExpiredAccessToken()
  if (
    !accessToken &&
    hasPersistedDashboardUser() &&
    !url.endsWith('/api/user/refresh')
  ) {
    await refreshDashboardAccessToken()
  }
  if (accessToken) config.headers.Authorization = `Bearer ${accessToken}`
  return config
})

// ============================================================================
// Request Deduplication
// ============================================================================

// Deduplicate concurrent GET requests to the same URL
// Prevents multiple identical requests from being sent simultaneously
const inFlightGet = new Map<string, Promise<unknown>>()
const originalGet = api.get.bind(api)

api.get = ((url: string, config = {}) => {
  const disableDuplicate = (config as unknown as Record<string, unknown>)
    ?.disableDuplicate
  if (disableDuplicate) return originalGet(url, config)

  const params = (config as unknown as Record<string, unknown>)?.params
    ? JSON.stringify((config as unknown as Record<string, unknown>).params)
    : '{}'
  const key = `${url}?${params}`

  // Return existing in-flight request if available
  if (inFlightGet.has(key)) return inFlightGet.get(key)!

  // Create new request and clean up after completion
  const req = originalGet(url, config).finally(() => inFlightGet.delete(key))
  inFlightGet.set(key, req)
  return req
}) as typeof api.get

// ============================================================================
// Response Interceptor
// ============================================================================

// Handle business logic errors and HTTP errors globally
api.interceptors.response.use(
  (response) => {
    if (response.config.url?.endsWith('/api/user/logout')) {
      setAccessToken(null, true)
    }
    const data = response?.data?.data
    if (
      isDashboardAuthResponse(response.config.url) &&
      data &&
      typeof data === 'object' &&
      typeof data.access_token === 'string'
    ) {
      setAccessToken(
        data.access_token,
        true,
        Number(data.access_expires_at) || 0
      )
      delete data.access_token
      delete data.refresh_token
      delete data.token
    }
    const skipBusiness = (response.config as unknown as Record<string, unknown>)
      ?.skipBusinessError

    // Unified business response format: { success, message, data }
    if (
      !skipBusiness &&
      response &&
      response.data &&
      typeof response.data.success === 'boolean'
    ) {
      if (!response.data.success) {
        // Show error toast for business failures
        const msg = response.data.message || 'Request failed'
        toast.error(msg)
      }
    }
    return response
  },
  async (error) => {
    const skip = error?.config?.skipErrorHandler
    const status = error?.response?.status
    const config = error?.config as DashboardAuthRequestConfig | undefined
    const isRefreshRequest = config?.url?.endsWith('/api/user/refresh')
    if (
      status === 401 &&
      !isRefreshRequest &&
      !config?.dashboardAuthRetried &&
      hasPersistedDashboardUser()
    ) {
      if (!config) return Promise.reject(error)
      config.dashboardAuthRetried = true
      setAccessToken(null)
      try {
        await refreshDashboardAccessToken()
        if (config.headers && accessToken) {
          config.headers.Authorization = `Bearer ${accessToken}`
        }
        return api.request(config)
      } catch (refreshError) {
        error = refreshError
      }
    }
    if (!skip) {
      if (isTerminalDashboardAuthError(error)) {
        setAccessToken(null, true)
        // Unauthorized: clear auth state and show toast
        toast.error(i18next.t('Session expired!'))
        try {
          useAuthStore.getState().auth.reset()
        } catch {
          /* empty */
        }
      } else {
        // Other errors: show error message from response or default
        const msg =
          error?.response?.data?.message || error?.message || 'Request error'
        toast.error(msg)
      }
    }
    return Promise.reject(error)
  }
)

// ============================================================================
// Common Headers Utility
// ============================================================================

/**
 * Get user ID from localStorage
 */
function getUserId(): string | null {
  try {
    if (typeof window !== 'undefined') {
      return window.localStorage.getItem('uid')
    }
  } catch {
    /* empty */
  }
  return null
}

/**
 * Get common request headers (for both axios and SSE requests)
 */
export function getCommonHeaders(): Record<string, string> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }

  const uid = getUserId()
  if (uid) {
    headers['New-Api-User'] = uid
  }

  return headers
}

export async function getDashboardAuthHeaders(): Promise<
  Record<string, string>
> {
  clearExpiredAccessToken()
  if (!accessToken && hasPersistedDashboardUser()) {
    await refreshDashboardAccessToken()
  }
  const headers = getCommonHeaders()
  if (accessToken) {
    headers.Authorization = `Bearer ${accessToken}`
  }
  return headers
}

// ============================================================================
// Request Interceptor
// ============================================================================

// Attach user ID header for all requests
api.interceptors.request.use((config) => {
  const uid = getUserId()
  if (uid) {
    // Custom header for user identification
    ;(config.headers as Record<string, string>)['New-Api-User'] = uid
  }
  return config
})

// ============================================================================
// Common API Functions
// ============================================================================

// ----------------------------------------------------------------------------
// User APIs
// ----------------------------------------------------------------------------

// Get current user info
export async function getSelf() {
  const res = await api.get('/api/user/self', {
    // Avoid global 401 toast during guards/preloads
    skipErrorHandler: true,
  } as Record<string, unknown>)
  return res.data
}

// Get user available models
export async function getUserModels(): Promise<{
  success: boolean
  message?: string
  data?: string[]
}> {
  const res = await api.get('/api/user/models')
  return res.data
}

// Get user groups with descriptions and ratios
export async function getUserGroups(): Promise<{
  success: boolean
  message?: string
  data?: Record<string, { desc: string; ratio: number | string }>
}> {
  const res = await api.get('/api/user/self/groups')
  return res.data
}

// ----------------------------------------------------------------------------
// System APIs
// ----------------------------------------------------------------------------

// Get system status
export async function getStatus() {
  const res = await api.get('/api/status')
  return res.data?.data as Record<string, unknown>
}

// Get system notice
export async function getNotice(): Promise<{
  success: boolean
  message?: string
  data?: string
}> {
  const res = await api.get('/api/notice')
  return res.data
}

// ----------------------------------------------------------------------------
// 2FA Management APIs
// ----------------------------------------------------------------------------

// Get 2FA status
export async function get2FAStatus() {
  const res = await api.get('/api/user/2fa/status')
  return res.data
}

// Setup 2FA
export async function setup2FA() {
  const res = await api.post('/api/user/2fa/setup')
  return res.data
}

// Enable 2FA with verification code
export async function enable2FA(code: string) {
  const res = await api.post('/api/user/2fa/enable', { code })
  return res.data
}

// Disable 2FA with verification code
export async function disable2FA(code: string) {
  const verified = await api.post('/api/verify', {
    method: '2fa',
    code,
    scope: '2fa.disable',
  })
  const proof = verified.data?.data?.proof
  if (verified.data?.success !== true || typeof proof !== 'string' || !proof) {
    return verified.data
  }
  const res = await api.post(
    '/api/user/2fa/disable',
    {},
    { headers: { 'X-Security-Proof': proof } }
  )
  return res.data
}

// Regenerate 2FA backup codes
export async function regenerate2FABackupCodes(code: string) {
  const res = await api.post('/api/user/2fa/backup_codes', { code })
  return res.data
}

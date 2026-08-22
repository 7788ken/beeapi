import { create } from 'zustand'

export type UserPermissions = {
  sidebar_settings?: boolean
  sidebar_modules?: Record<string, unknown>
}

export interface AuthUser {
  id: number
  username: string
  display_name?: string
  email?: string
  role: number
  status?: number
  group?: string
  quota?: number
  used_quota?: number
  request_count?: number
  aff_code?: string
  aff_count?: number
  aff_quota?: number
  aff_history_quota?: number
  inviter_id?: number
  github_id?: string
  oidc_id?: string
  wechat_id?: string
  telegram_id?: string
  linux_do_id?: string
  setting?: Record<string, unknown> | string
  stripe_customer?: string
  sidebar_modules?: string
  permissions?: UserPermissions
}

interface AuthState {
  auth: {
    user: AuthUser | null
    setUser: (user: AuthUser | null) => void
    reset: () => void
  }
}

function sanitizeUser(user: AuthUser | null): AuthUser | null {
  if (!user) return null
  const clean = { ...user } as AuthUser & Record<string, unknown>
  delete clean.token
  delete clean.access_token
  delete clean.refresh_token
  return clean
}

export const useAuthStore = create<AuthState>()((set) => {
  // Restore user info from localStorage
  const initUser = (() => {
    try {
      if (typeof window !== 'undefined') {
        const saved = window.localStorage.getItem('user')
        if (!saved) return null
        const cleanUser = sanitizeUser(JSON.parse(saved))
        window.localStorage.setItem('user', JSON.stringify(cleanUser))
        return cleanUser
      }
    } catch {
      // Clear dirty data when parsing fails
      if (typeof window !== 'undefined') {
        window.localStorage.removeItem('user')
      }
    }
    return null
  })()

  return {
    auth: {
      user: initUser,
      setUser: (user) =>
        set((state) => {
          const cleanUser = sanitizeUser(user)
          // Persist user to localStorage
          if (typeof window !== 'undefined') {
            if (cleanUser) {
              window.localStorage.setItem('user', JSON.stringify(cleanUser))
            } else {
              window.localStorage.removeItem('user')
            }
          }
          return { ...state, auth: { ...state.auth, user: cleanUser } }
        }),
      reset: () =>
        set((state) => {
          if (typeof window !== 'undefined') {
            window.localStorage.removeItem('user')
          }
          return {
            ...state,
            auth: { ...state.auth, user: null },
          }
        }),
    },
  }
})

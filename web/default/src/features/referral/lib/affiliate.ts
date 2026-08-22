// ============================================================================
// Affiliate Functions
// ============================================================================

/**
 * Generate affiliate registration link.
 *
 * 新 UI 的注册路径是 /sign-up，老 UI（classic）用的是 /register。
 * 这里直接生成新地址；老地址 /register?aff=xxx 在 routes/(auth)/register.tsx
 * 里有兼容路由（写 localStorage 后 redirect 到 /sign-up），不会丢失邀请码。
 */
export function generateAffiliateLink(affCode: string): string {
  if (typeof window === 'undefined') return ''
  return `${window.location.origin}/sign-up?aff=${affCode}`
}

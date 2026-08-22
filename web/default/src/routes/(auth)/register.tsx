import { createFileRoute, redirect } from '@tanstack/react-router'
import { saveAffiliateCode } from '@/features/auth/lib/storage'

// 老 UI 注册路径 /register?aff=xxx 的兼容入口（推荐计划早期生成的邀请链接走这条）。
// 这里把 aff 写入 localStorage 后立即重定向到新 UI 的 /sign-up，参数透传给 sign-up
// 自己的 beforeLoad 兜底 —— 即便用户清了 localStorage 直接访问 /sign-up?aff=xxx 也能落库。
export const Route = createFileRoute('/(auth)/register')({
  beforeLoad: ({ search }) => {
    const aff = (search as { aff?: string } | undefined)?.aff
    if (aff && typeof aff === 'string' && aff.trim() !== '') {
      saveAffiliateCode(aff.trim())
    }
    throw redirect({
      to: '/sign-up',
      search: aff ? { aff } : undefined,
      replace: true,
    })
  },
})

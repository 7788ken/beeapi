import { createFileRoute } from '@tanstack/react-router'
import { SignUp } from '@/features/auth/sign-up'
import { saveAffiliateCode } from '@/features/auth/lib/storage'

export const Route = createFileRoute('/(auth)/sign-up')({
  // 兼容直接访问 /sign-up?aff=xxx：把邀请码写入 localStorage，sign-up-form
  // 提交时会通过 getAffiliateCode() 读出来透传给后端
  beforeLoad: ({ search }) => {
    const aff = (search as { aff?: string } | undefined)?.aff
    if (aff && typeof aff === 'string' && aff.trim() !== '') {
      saveAffiliateCode(aff.trim())
    }
  },
  component: SignUp,
})

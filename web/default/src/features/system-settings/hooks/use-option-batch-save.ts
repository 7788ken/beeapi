import { useCallback, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { updateSystemOption } from '../api'

export type OptionValue = string | boolean | number

export type BatchSaveResult<T> = {
  /** 只包含服务端确认成功的键，调用方据此推进 baseline */
  saved: Partial<T>
  failedKeys: string[]
  attempted: number
}

/**
 * 整组表单、逐项提交的保存器。
 *
 * 后端 /api/option/ 一次只收一个键，所以一组设置要发多次。这里有两件事必须做对：
 *
 *  1. `/api/option/` 保存失败时会返回 HTTP 200 + success:false，promise 不会 reject。
 *     只要不看 success 就会把失败当成功，把 baseline 整体推进——那个键此后与 baseline
 *     相等、被差异计算跳过，用户再点保存也永远存不上。因此只有确认 success 的键才回传。
 *  2. 整组一个 toast。逐项 toast 在字段多的表单里会刷屏。
 */
export function useOptionBatchSave() {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  const [isSaving, setIsSaving] = useState(false)

  const save = useCallback(
    async <T extends Record<string, OptionValue>>(
      normalized: T,
      baseline: T
    ): Promise<BatchSaveResult<T>> => {
      const changedKeys = (Object.keys(normalized) as Array<keyof T>).filter(
        (key) => normalized[key] !== baseline[key]
      )

      if (changedKeys.length === 0) {
        toast.info(t('No changes to save'))
        return { saved: {}, failedKeys: [], attempted: 0 }
      }

      const saved: Partial<T> = {}
      const failedKeys: string[] = []

      setIsSaving(true)
      try {
        for (const key of changedKeys) {
          try {
            const res = await updateSystemOption({
              key: String(key),
              value: normalized[key],
            })
            if (res?.success) {
              saved[key] = normalized[key]
            } else {
              failedKeys.push(String(key))
            }
          } catch {
            failedKeys.push(String(key))
          }
        }
      } finally {
        setIsSaving(false)
        queryClient.invalidateQueries({ queryKey: ['system-options'] })
      }

      if (failedKeys.length === 0) {
        toast.success(t('Settings saved'))
      } else if (Object.keys(saved).length === 0) {
        toast.error(t('Nothing was saved. All changes were rejected.'))
      } else {
        toast.error(
          t(
            'Saved partially: {{failed}} of {{total}} settings were rejected and left unchanged',
            { failed: failedKeys.length, total: changedKeys.length }
          )
        )
      }

      return { saved, failedKeys, attempted: changedKeys.length }
    },
    [queryClient, t]
  )

  return { save, isSaving }
}

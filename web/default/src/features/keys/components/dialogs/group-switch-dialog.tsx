import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { updateApiKey } from '../../api'
import { ERROR_MESSAGES } from '../../constants'
import { type ApiKey, type ApiKeyFormData } from '../../types'
import { useApiKeys } from '../api-keys-provider'
import { GroupPickerDialog } from '../group-picker-dialog'

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

// 只改 group，其余字段沿用当前 key 的后端原值（均为后端格式，无需再转换）。
function buildPayload(row: ApiKey, group: string): ApiKeyFormData {
  return {
    name: row.name,
    remain_quota: row.remain_quota,
    expired_time: row.expired_time,
    unlimited_quota: row.unlimited_quota,
    model_limits_enabled: row.model_limits_enabled,
    model_limits: row.model_limits ?? '',
    allow_ips: row.allow_ips ?? '',
    group,
    // 沿用原值，不因切组而静默重置用户的跨组重试偏好（非 auto 时后端本就不读该字段）
    cross_group_retry: !!row.cross_group_retry,
    relay_retry_policy: row.relay_retry_policy ?? 'system',
  }
}

export function GroupSwitchDialog({ open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const { currentRow, triggerRefresh } = useApiKeys()
  const [confirming, setConfirming] = useState(false)

  const handleConfirm = async (group: string) => {
    if (!currentRow) return
    if (group === (currentRow.group ?? '')) {
      onOpenChange(false)
      return
    }
    setConfirming(true)
    try {
      const res = await updateApiKey({
        ...buildPayload(currentRow, group),
        id: currentRow.id,
      })
      if (res.success) {
        toast.success(t('Group switched successfully'))
        onOpenChange(false)
        triggerRefresh()
      } else {
        toast.error(res.message || t(ERROR_MESSAGES.UPDATE_FAILED))
      }
    } catch {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setConfirming(false)
    }
  }

  return (
    <GroupPickerDialog
      open={open}
      onOpenChange={onOpenChange}
      value={currentRow?.group ?? ''}
      currentGroup={currentRow?.group ?? ''}
      onConfirm={handleConfirm}
      confirming={confirming}
      title={t('Switch group')}
      description={
        currentRow
          ? t('Switch the group for "{{name}}"', { name: currentRow.name })
          : undefined
      }
      confirmText={t('Confirm switch')}
    />
  )
}

import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  ADMIN_PERM,
  ADMIN_PERM_ITEMS,
  DEFAULT_ADMIN_PERMS,
} from '@/lib/admin-perms'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Switch } from '@/components/ui/switch'
import { updateUserAdminPerms } from '../../api'
import { ERROR_MESSAGES } from '../../constants'
import { type User } from '../../types'

interface UserAdminPermsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: User
  onSuccess: () => void
}

/**
 * 超级管理员给管理员配置细粒度权限。
 * 未配置过的管理员默认全开（等于本功能上线前的能力），这里关掉哪项就收哪项。
 */
export function UserAdminPermsDialog(props: UserAdminPermsDialogProps) {
  const { t } = useTranslation()
  const [granted, setGranted] = useState<string[]>([])
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!props.open) return
    setGranted(props.user.admin_perms ?? DEFAULT_ADMIN_PERMS)
  }, [props.open, props.user.admin_perms])

  const toggle = (key: string, checked: boolean) => {
    setGranted((prev) => {
      let next = checked
        ? [...new Set([...prev, key])]
        : prev.filter((p) => p !== key)
      // 改渠道必然要看得到渠道，两个开关联动，避免配出「能改但进不去」的死组合
      if (checked && key === ADMIN_PERM.CHANNEL_EDIT) {
        next = [...new Set([...next, ADMIN_PERM.CHANNEL_VIEW])]
      }
      if (!checked && key === ADMIN_PERM.CHANNEL_VIEW) {
        next = next.filter((p) => p !== ADMIN_PERM.CHANNEL_EDIT)
      }
      return next
    })
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      const result = await updateUserAdminPerms(props.user.id, granted)
      if (result.success) {
        toast.success(t('Permissions updated'))
        props.onOpenChange(false)
        props.onSuccess()
      } else {
        toast.error(result.message || t('Failed to update permissions'))
      }
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-[520px]'>
        <DialogHeader>
          <DialogTitle>{t('Admin Permissions')}</DialogTitle>
          <DialogDescription>
            {t('Choose what {{username}} is allowed to do in the admin area', {
              username: props.user.username,
            })}
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-2'>
          {ADMIN_PERM_ITEMS.map((item) => (
            <label
              key={item.key}
              className='flex items-start justify-between gap-3 rounded-md border px-3 py-2.5 text-sm'
            >
              <span className='space-y-0.5'>
                <span className='block font-medium'>{t(item.labelKey)}</span>
                <span className='text-muted-foreground block text-xs'>
                  {t(item.descKey)}
                </span>
              </span>
              <Switch
                checked={granted.includes(item.key)}
                onCheckedChange={(checked) => toggle(item.key, checked)}
                aria-label={t(item.labelKey)}
              />
            </label>
          ))}
        </div>

        <p className='text-muted-foreground text-xs'>
          {t(
            'Admins can only add quota to regular users. Subtracting, zeroing out and overriding quota stay super-admin only.'
          )}
        </p>

        <DialogFooter>
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={saving}
          >
            {t('Cancel')}
          </Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? t('Saving...') : t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAdminPerms } from '@/hooks/use-admin'
import { Button } from '@/components/ui/button'
import { useUsers } from './users-provider'

export function UsersPrimaryButtons() {
  const { t } = useTranslation()
  const { setOpen, setCurrentRow } = useUsers()
  const perms = useAdminPerms()

  const handleCreate = () => {
    setCurrentRow(null)
    setOpen('create')
  }

  // 只有「调整额度」权限的管理员进得来这个页面，但不能建用户
  if (!perms.user_manage) {
    return null
  }

  return (
    <div className='flex gap-2'>
      <Button size='sm' onClick={handleCreate}>
        <Plus className='h-4 w-4' />
        {t('Add User')}
      </Button>
    </div>
  )
}

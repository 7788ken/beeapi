import { useEffect } from 'react'
import { useSearch } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { useAdminPerms } from '@/hooks/use-admin'
import { SectionPageLayout } from '@/components/layout'
import { getUser } from './api'
import { UsersDeleteDialog } from './components/users-delete-dialog'
import { UsersMutateDrawer } from './components/users-mutate-drawer'
import { UsersPrimaryButtons } from './components/users-primary-buttons'
import { UsersProvider, useUsers } from './components/users-provider'
import { UsersTable } from './components/users-table'

function UsersContent() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, setCurrentRow } = useUsers()
  const search = useSearch({ strict: false }) as { edit?: number }
  const canManage = useAdminPerms().user_manage

  useEffect(() => {
    // ?edit=<id> 深链只对有「管理用户」权限的人开；否则抽屉里的保存也只会被后端 403
    if (!canManage) return
    if (search.edit && !open) {
      getUser(search.edit).then((res) => {
        if (res.success && res.data) {
          setCurrentRow(res.data)
          setOpen('update')
        }
      })
    }
  }, [search.edit, canManage])

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Users')}</SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {t('Manage users and their permissions')}
        </SectionPageLayout.Description>
        <SectionPageLayout.Actions>
          <UsersPrimaryButtons />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <UsersTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <UsersMutateDrawer
        open={open === 'create' || open === 'update'}
        onOpenChange={(isOpen) => !isOpen && setOpen(null)}
        currentRow={open === 'update' ? currentRow || undefined : undefined}
      />
      <UsersDeleteDialog />
    </>
  )
}

export function Users() {
  return (
    <UsersProvider>
      <UsersContent />
    </UsersProvider>
  )
}

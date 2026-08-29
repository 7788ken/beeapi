import { useState } from 'react'
import { type Row } from '@tanstack/react-table'
import {
  MoreHorizontal,
  Pencil,
  Trash2,
  Power,
  PowerOff,
  ArrowUp,
  ArrowDown,
  KeyRound,
  ShieldAlert,
  ShieldCheck,
  Link2,
  CreditCard,
  Wallet,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useAdminPerms, useIsRoot } from '@/hooks/use-admin'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { UserSubscriptionsDialog } from '@/features/subscriptions/components/dialogs/user-subscriptions-dialog'
import { manageUser, resetUserPasskey, resetUserTwoFA } from '../api'
import {
  USER_STATUS,
  USER_ROLE,
  ERROR_MESSAGES,
  isUserDeleted,
} from '../constants'
import { getUserActionMessage } from '../lib'
import { type User, type ManageUserAction } from '../types'
import { UserAdminPermsDialog } from './dialogs/user-admin-perms-dialog'
import { UserBindingDialog } from './dialogs/user-binding-dialog'
import { UserQuotaDialog } from './user-quota-dialog'
import { useUsers } from './users-provider'

interface DataTableRowActionsProps {
  row: Row<User>
}

export function DataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const user = row.original
  const { setOpen, setCurrentRow, triggerRefresh } = useUsers()
  const [resetPasskeyOpen, setResetPasskeyOpen] = useState(false)
  const [resetTwoFAOpen, setResetTwoFAOpen] = useState(false)
  const [bindingDialogOpen, setBindingDialogOpen] = useState(false)
  const [subscriptionsDialogOpen, setSubscriptionsDialogOpen] = useState(false)
  const [permsDialogOpen, setPermsDialogOpen] = useState(false)
  const [quotaDialogOpen, setQuotaDialogOpen] = useState(false)
  const isRootOperator = useIsRoot()
  const perms = useAdminPerms()

  const handleEdit = () => {
    setCurrentRow(user)
    setOpen('update')
  }

  const handleDelete = () => {
    setCurrentRow(user)
    setOpen('delete')
  }

  const handleManage = async (action: Exclude<ManageUserAction, 'delete'>) => {
    try {
      const result = await manageUser(user.id, action)
      if (result.success) {
        toast.success(t(getUserActionMessage(action)))
        triggerRefresh()
      } else {
        toast.error(
          result.message || t('Failed to {{action}} user', { action })
        )
      }
    } catch (_error) {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    }
  }

  const handleResetPasskey = async () => {
    try {
      const result = await resetUserPasskey(user.id)
      if (result.success) {
        toast.success(t('Passkey reset successfully'))
        triggerRefresh()
      } else {
        toast.error(result.message || t('Failed to reset Passkey'))
      }
    } catch (_error) {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setResetPasskeyOpen(false)
    }
  }

  const handleResetTwoFA = async () => {
    try {
      const result = await resetUserTwoFA(user.id)
      if (result.success) {
        toast.success(t('Two-factor authentication reset'))
        triggerRefresh()
      } else {
        toast.error(result.message || t('Failed to reset 2FA'))
      }
    } catch (_error) {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setResetTwoFAOpen(false)
    }
  }

  const isDisabled = user.status === USER_STATUS.DISABLED
  const isAdmin = user.role >= USER_ROLE.ADMIN
  const isRoot = user.role === USER_ROLE.ROOT
  // 只有「调整额度」权限的管理员进得来用户列表，但不能做任何用户管理动作
  const canManage = perms.user_manage
  // 权限配置只有超级管理员能做，且只对管理员账号有意义
  const canEditPerms = isRootOperator && user.role === USER_ROLE.ADMIN
  // 只给普通用户加额度是管理员能做的；超级管理员不受此限
  const canAdjustQuota =
    perms.quota_grant && (isRootOperator || user.role === USER_ROLE.USER)

  if (isUserDeleted(user)) {
    return null
  }

  if (!canManage && !canEditPerms && !canAdjustQuota) {
    return null
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant='ghost'
            className='data-[state=open]:bg-muted flex h-8 w-8 p-0'
          >
            <MoreHorizontal className='h-4 w-4' />
            <span className='sr-only'>{t('Open menu')}</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end' className='w-[180px]'>
          {canEditPerms && (
            <DropdownMenuItem
              onSelect={(event) => {
                event.preventDefault()
                setPermsDialogOpen(true)
              }}
            >
              {t('Admin Permissions')}
              <DropdownMenuShortcut>
                <ShieldCheck size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          {canAdjustQuota && (
            <DropdownMenuItem
              onSelect={(event) => {
                event.preventDefault()
                setQuotaDialogOpen(true)
              }}
            >
              {t('Adjust Quota')}
              <DropdownMenuShortcut>
                <Wallet size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          {(canEditPerms || canAdjustQuota) && canManage && (
            <DropdownMenuSeparator />
          )}

          {canManage && (
            <>
              <DropdownMenuItem onClick={handleEdit}>
                {t('Edit')}
                <DropdownMenuShortcut>
                  <Pencil size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>

              <DropdownMenuSeparator />

              {isDisabled ? (
                <DropdownMenuItem onClick={() => handleManage('enable')}>
                  {t('Enable')}
                  <DropdownMenuShortcut>
                    <Power size={16} />
                  </DropdownMenuShortcut>
                </DropdownMenuItem>
              ) : (
                <DropdownMenuItem
                  onClick={() => handleManage('disable')}
                  disabled={isRoot}
                >
                  {t('Disable')}
                  <DropdownMenuShortcut>
                    <PowerOff size={16} />
                  </DropdownMenuShortcut>
                </DropdownMenuItem>
              )}

              {isAdmin && !isRoot && (
                <DropdownMenuItem onClick={() => handleManage('demote')}>
                  {t('Demote')}
                  <DropdownMenuShortcut>
                    <ArrowDown size={16} />
                  </DropdownMenuShortcut>
                </DropdownMenuItem>
              )}

              {!isAdmin && (
                <DropdownMenuItem onClick={() => handleManage('promote')}>
                  {t('Promote')}
                  <DropdownMenuShortcut>
                    <ArrowUp size={16} />
                  </DropdownMenuShortcut>
                </DropdownMenuItem>
              )}

              <DropdownMenuItem
                onSelect={(event) => {
                  event.preventDefault()
                  setBindingDialogOpen(true)
                }}
              >
                {t('Manage Bindings')}
                <DropdownMenuShortcut>
                  <Link2 size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>

              <DropdownMenuItem
                onSelect={(event) => {
                  event.preventDefault()
                  setSubscriptionsDialogOpen(true)
                }}
              >
                {t('Manage Subscriptions')}
                <DropdownMenuShortcut>
                  <CreditCard size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>

              <DropdownMenuSeparator />

              <DropdownMenuItem
                onSelect={(event) => {
                  event.preventDefault()
                  setResetPasskeyOpen(true)
                }}
                disabled={isRoot}
              >
                {t('Reset Passkey')}
                <DropdownMenuShortcut>
                  <KeyRound size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>

              <DropdownMenuItem
                onSelect={(event) => {
                  event.preventDefault()
                  setResetTwoFAOpen(true)
                }}
                disabled={isRoot}
              >
                {t('Reset 2FA')}
                <DropdownMenuShortcut>
                  <ShieldAlert size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>

              <DropdownMenuSeparator />

              <DropdownMenuItem
                onClick={handleDelete}
                className='text-destructive focus:text-destructive'
                disabled={isRoot}
              >
                {t('Delete')}
                <DropdownMenuShortcut>
                  <Trash2 size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      <ConfirmDialog
        open={resetPasskeyOpen}
        onOpenChange={setResetPasskeyOpen}
        title={t('Reset Passkey')}
        desc={`Reset Passkey for ${user.username}? The user will need to register a new Passkey before using passwordless login.`}
        confirmText='Reset Passkey'
        handleConfirm={handleResetPasskey}
      />

      <ConfirmDialog
        open={resetTwoFAOpen}
        onOpenChange={setResetTwoFAOpen}
        title={t('Reset Two-Factor Authentication')}
        desc={`Reset 2FA for ${user.username}? The user must set up 2FA again to continue using it.`}
        confirmText='Reset 2FA'
        handleConfirm={handleResetTwoFA}
      />

      <UserBindingDialog
        open={bindingDialogOpen}
        onOpenChange={setBindingDialogOpen}
        userId={user.id}
        onUnbindSuccess={triggerRefresh}
      />

      <UserSubscriptionsDialog
        open={subscriptionsDialogOpen}
        onOpenChange={setSubscriptionsDialogOpen}
        user={{ id: user.id, username: user.username }}
        onSuccess={triggerRefresh}
      />

      {canAdjustQuota && (
        <UserQuotaDialog
          open={quotaDialogOpen}
          onOpenChange={setQuotaDialogOpen}
          userId={user.id}
          currentQuota={user.quota}
          onSuccess={triggerRefresh}
        />
      )}

      {canEditPerms && (
        <UserAdminPermsDialog
          open={permsDialogOpen}
          onOpenChange={setPermsDialogOpen}
          user={user}
          onSuccess={triggerRefresh}
        />
      )}
    </>
  )
}

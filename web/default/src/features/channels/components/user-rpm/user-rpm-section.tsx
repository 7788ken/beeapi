import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Gauge, Loader2, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  createUserRpmRule,
  deleteUserRpmRule,
  listUserRpmRules,
  updateUserRpmRule,
  userRpmQueryKey,
  type UserRpmRule,
} from './api'
import { UserRpmForm, type UserRpmFormValues } from './user-rpm-form'
import { UserRpmTable } from './user-rpm-table'

type Props = {
  channelId: number | null
  // 新建渠道时未保存的占位状态
  disabledReason?: string
}

export function UserRpmSection({ channelId, disabledReason }: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<UserRpmRule | null>(null)
  const [creating, setCreating] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<UserRpmRule | null>(null)

  const enabled = Boolean(channelId)
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: enabled
      ? userRpmQueryKey(channelId!)
      : ['channel', 0, 'user_rpm'],
    queryFn: () => listUserRpmRules(channelId!),
    enabled,
  })

  const invalidate = () => {
    if (channelId) {
      queryClient.invalidateQueries({ queryKey: userRpmQueryKey(channelId) })
    }
  }

  const createMutation = useMutation({
    mutationFn: (values: UserRpmFormValues) =>
      createUserRpmRule(channelId!, values),
    onSuccess: (res) => {
      if (!res.success) {
        toast.error(res.message || t('Failed to create rule'))
        return
      }
      toast.success(t('Rule created'))
      setCreating(false)
      invalidate()
    },
    onError: (err: Error) => {
      toast.error(err.message)
    },
  })

  const updateMutation = useMutation({
    mutationFn: (args: { ruleId: number; values: UserRpmFormValues }) =>
      updateUserRpmRule(channelId!, args.ruleId, {
        // user_id 不可改
        base_rpm: args.values.base_rpm,
        jitter_pct: args.values.jitter_pct,
        min_delay_ms: args.values.min_delay_ms,
        max_delay_ms: args.values.max_delay_ms,
        enabled: args.values.enabled,
      }),
    onSuccess: (res) => {
      if (!res.success) {
        toast.error(res.message || t('Failed to update rule'))
        return
      }
      toast.success(t('Rule updated'))
      setEditing(null)
      invalidate()
    },
    onError: (err: Error) => {
      toast.error(err.message)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (rule: UserRpmRule) => deleteUserRpmRule(channelId!, rule.id),
    onSuccess: (res) => {
      if (!res.success) {
        toast.error(res.message || t('Failed to delete rule'))
        return
      }
      toast.success(t('Rule deleted'))
      setPendingDelete(null)
      invalidate()
    },
    onError: (err: Error) => {
      toast.error(err.message)
    },
  })

  const rules = data?.data?.items ?? []
  const busy =
    createMutation.isPending ||
    updateMutation.isPending ||
    deleteMutation.isPending

  const handleCreateSubmit = (values: UserRpmFormValues) => {
    if (!channelId) return
    createMutation.mutate(values)
  }

  const handleEditSubmit = (values: UserRpmFormValues) => {
    if (!editing) return
    updateMutation.mutate({ ruleId: editing.id, values })
  }

  return (
    <div className='space-y-3'>
      <div className='flex items-center gap-2'>
        <Gauge className='size-4' />
        <div>
          <div className='text-[13px] font-semibold'>
            {t('User-level RPM limit')}
          </div>
          <div className='text-muted-foreground text-xs'>
            {disabledReason
              ? disabledReason
              : t('Per-user soft RPM quota with jitter and randomized delay')}
          </div>
        </div>
      </div>

      {!enabled ? (
        <div className='bg-muted/30 text-muted-foreground rounded-lg border p-4 text-sm'>
          {t('Save the channel first, then add user-level RPM rules.')}
        </div>
      ) : (
        <>
          <div className='flex items-center justify-between'>
            <div className='text-muted-foreground text-xs'>
              {isLoading ? (
                <span className='flex items-center gap-1'>
                  <Loader2 className='size-3 animate-spin' />
                  {t('Loading...')}
                </span>
              ) : isError ? (
                <span className='text-rose-600'>
                  {t('Failed to load. ')}
                  <button
                    type='button'
                    className='underline'
                    onClick={() => refetch()}
                  >
                    {t('Retry')}
                  </button>
                </span>
              ) : (
                t('Total rules: {{n}}', { n: rules.length })
              )}
            </div>
            <Button
              type='button'
              size='sm'
              onClick={() => {
                setEditing(null)
                setCreating(true)
              }}
              disabled={busy || creating || Boolean(editing)}
            >
              <Plus className='mr-1 size-4' />
              {t('Add rule')}
            </Button>
          </div>

          {creating && (
            <UserRpmForm
              submitting={createMutation.isPending}
              onSubmit={handleCreateSubmit}
              onCancel={() => setCreating(false)}
            />
          )}

          {editing && (
            <UserRpmForm
              initial={editing}
              lockUserId
              submitting={updateMutation.isPending}
              onSubmit={handleEditSubmit}
              onCancel={() => setEditing(null)}
            />
          )}

          <UserRpmTable
            rules={rules}
            busyId={
              deleteMutation.isPending && pendingDelete
                ? pendingDelete.id
                : null
            }
            onEdit={(rule) => {
              setCreating(false)
              setEditing(rule)
            }}
            onDelete={(rule) => setPendingDelete(rule)}
          />
        </>
      )}

      <AlertDialog
        open={Boolean(pendingDelete)}
        onOpenChange={(v) => !v && setPendingDelete(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete RPM rule?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'This will remove the RPM limit for user {{uid}}. The user will fall back to no per-channel cap.',
                { uid: pendingDelete?.user_id }
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteMutation.isPending}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={deleteMutation.isPending}
              onClick={() =>
                pendingDelete && deleteMutation.mutate(pendingDelete)
              }
            >
              {deleteMutation.isPending && (
                <Loader2 className='mr-1 size-4 animate-spin' />
              )}
              {t('Delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

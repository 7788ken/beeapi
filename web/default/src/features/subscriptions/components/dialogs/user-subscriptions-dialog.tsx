import { useCallback, useEffect, useMemo, useState } from 'react'
import { Plus, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DateTimePicker } from '@/components/datetime-picker'
import { StatusBadge } from '@/components/status-badge'
import {
  getAdminPlans,
  getUserSubscriptions,
  createUserSubscription,
  invalidateUserSubscription,
  deleteUserSubscription,
  updateUserSubscriptionExpiry,
  resetUserSubscriptionsByPlan,
} from '../../api'
import { formatTimestamp } from '../../lib'
import type { PlanRecord, UserSubscriptionRecord } from '../../types'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: { id: number; username?: string } | null
  onSuccess?: () => void
}

function SubscriptionStatusBadge(props: {
  sub: UserSubscriptionRecord['subscription']
  t: (key: string) => string
}) {
  // eslint-disable-next-line react-hooks/purity
  const now = Date.now() / 1000
  const isExpired = (props.sub.end_time || 0) > 0 && props.sub.end_time < now
  const isActive = props.sub.status === 'active' && !isExpired
  if (isActive)
    return (
      <StatusBadge
        label={props.t('Active')}
        variant='success'
        copyable={false}
      />
    )
  if (props.sub.status === 'cancelled')
    return (
      <StatusBadge
        label={props.t('Invalidated')}
        variant='neutral'
        copyable={false}
      />
    )
  if (props.sub.status === 'exhausted' && !isExpired)
    return (
      <StatusBadge
        label={props.t('Exhausted')}
        variant='warning'
        copyable={false}
      />
    )
  return (
    <StatusBadge
      label={props.t('Expired')}
      variant='neutral'
      copyable={false}
    />
  )
}

export function UserSubscriptionsDialog(props: Props) {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [plans, setPlans] = useState<PlanRecord[]>([])
  const [subs, setSubs] = useState<UserSubscriptionRecord[]>([])
  const [selectedPlanId, setSelectedPlanId] = useState<string>('')
  const [resetting, setResetting] = useState(false)
  const [advanceResetTime, setAdvanceResetTime] = useState(true)
  const [resetAction, setResetAction] = useState<{
    planId: number
    planTitle: string
  } | null>(null)
  const [confirmAction, setConfirmAction] = useState<{
    type: 'invalidate' | 'delete'
    subId: number
  } | null>(null)
  const [editingExpiry, setEditingExpiry] = useState<{
    subId: number
  } | null>(null)
  const [editExpiryValue, setEditExpiryValue] = useState<Date | undefined>(
    undefined
  )
  const [editExpirySubmitting, setEditExpirySubmitting] = useState(false)

  const planTitleMap = useMemo(() => {
    const map = new Map<number, string>()
    plans.forEach((p) => {
      if (p.plan.id) map.set(p.plan.id, p.plan.title || `#${p.plan.id}`)
    })
    return map
  }, [plans])

  const loadData = useCallback(async () => {
    if (!props.user?.id) return
    setLoading(true)
    try {
      const [plansRes, subsRes] = await Promise.all([
        getAdminPlans(),
        getUserSubscriptions(props.user.id),
      ])
      if (plansRes.success) setPlans(plansRes.data || [])
      if (subsRes.success) setSubs(subsRes.data || [])
    } catch {
      toast.error(t('Loading failed'))
    } finally {
      setLoading(false)
    }
  }, [props.user?.id, t])

  useEffect(() => {
    if (props.open && props.user?.id) {
      setSelectedPlanId('')
      loadData()
    }
  }, [props.open, props.user?.id, loadData])

  const handleCreate = async () => {
    if (!props.user?.id || !selectedPlanId) {
      toast.error(t('Please select a subscription plan'))
      return
    }
    setCreating(true)
    try {
      const res = await createUserSubscription(props.user.id, {
        plan_id: Number(selectedPlanId),
      })
      if (res.success) {
        toast.success(res.data?.message || t('Added successfully'))
        setSelectedPlanId('')
        await loadData()
        props.onSuccess?.()
      }
    } catch {
      toast.error(t('Request failed'))
    } finally {
      setCreating(false)
    }
  }

  const openEditExpiry = (sub: UserSubscriptionRecord['subscription']) => {
    setEditExpiryValue(
      sub.end_time > 0 ? new Date(sub.end_time * 1000) : new Date()
    )
    setEditingExpiry({ subId: sub.id })
  }

  const closeEditExpiry = () => {
    setEditingExpiry(null)
    setEditExpiryValue(undefined)
  }

  const submitEditExpiry = async () => {
    if (!editingExpiry || !editExpiryValue) {
      toast.error(t('Please pick a date'))
      return
    }
    const endTime = Math.floor(editExpiryValue.getTime() / 1000)
    if (!Number.isFinite(endTime) || endTime <= 0) {
      toast.error(t('Invalid date'))
      return
    }
    setEditExpirySubmitting(true)
    try {
      const res = await updateUserSubscriptionExpiry(
        editingExpiry.subId,
        endTime
      )
      if (res.success) {
        toast.success(res.data?.message || t('Updated'))
        closeEditExpiry()
        await loadData()
        // 故意不调 props.onSuccess() ——
        // 它会触发父组件 users 表 refetch，让 row 重 mount、SideSheet 关闭。
        // edit expiry 只改订阅记录，不影响 users 表显示字段，本 Dialog 内的
        // loadData() 已刷新订阅列表足够。
      } else {
        toast.error(t('Operation failed'))
      }
    } catch {
      toast.error(t('Operation failed'))
    } finally {
      setEditExpirySubmitting(false)
    }
  }

  const handleConfirmAction = async () => {
    if (!confirmAction) return
    try {
      if (confirmAction.type === 'invalidate') {
        const res = await invalidateUserSubscription(confirmAction.subId)
        if (res.success) {
          toast.success(res.data?.message || t('Has been invalidated'))
          await loadData()
          props.onSuccess?.()
        }
      } else {
        const res = await deleteUserSubscription(confirmAction.subId)
        if (res.success) {
          toast.success(t('Deleted'))
          await loadData()
          props.onSuccess?.()
        }
      }
    } catch {
      toast.error(t('Operation failed'))
    } finally {
      setConfirmAction(null)
    }
  }

  const handleResetConfirm = async () => {
    if (!props.user?.id || !resetAction) return
    setResetting(true)
    try {
      const res = await resetUserSubscriptionsByPlan(props.user.id, {
        plan_id: resetAction.planId,
        advance_reset_time: advanceResetTime,
      })
      if (res.success) {
        toast.success(
          t('Reset {{count}} eligible subscriptions', {
            count: res.data?.reset_count || 0,
          })
        )
        await loadData()
        props.onSuccess?.()
      } else {
        toast.error(res.message || t('Operation failed'))
      }
    } catch {
      toast.error(t('Operation failed'))
    } finally {
      setResetting(false)
      setResetAction(null)
    }
  }

  return (
    <>
      <Sheet open={props.open} onOpenChange={props.onOpenChange}>
        <SheetContent className='overflow-y-auto sm:max-w-2xl'>
          <SheetHeader>
            <SheetTitle>{t('User Subscription Management')}</SheetTitle>
            <SheetDescription>
              {props.user?.username || '-'} (ID: {props.user?.id || '-'})
            </SheetDescription>
          </SheetHeader>

          <div className='mt-4 space-y-4'>
            <div className='flex gap-2'>
              <Select value={selectedPlanId} onValueChange={setSelectedPlanId}>
                <SelectTrigger className='flex-1'>
                  <SelectValue placeholder={t('Select subscription plan')} />
                </SelectTrigger>
                <SelectContent>
                  {plans.map((p) => (
                    <SelectItem key={p.plan.id} value={String(p.plan.id)}>
                      {p.plan.title} ($
                      {Number(p.plan.price_amount || 0).toFixed(2)})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button
                onClick={handleCreate}
                disabled={creating || !selectedPlanId}
              >
                <Plus className='mr-1 h-4 w-4' />
                {t('Add subscription')}
              </Button>
            </div>

            <div className='rounded-md border'>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>ID</TableHead>
                    <TableHead>{t('Plan')}</TableHead>
                    <TableHead>{t('Status')}</TableHead>
                    <TableHead>{t('Validity')}</TableHead>
                    <TableHead>{t('Total Quota')}</TableHead>
                    <TableHead className='text-right'>{t('Actions')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {loading ? (
                    <TableRow>
                      <TableCell colSpan={6} className='py-8 text-center'>
                        {t('Loading...')}
                      </TableCell>
                    </TableRow>
                  ) : subs.length === 0 ? (
                    <TableRow>
                      <TableCell
                        colSpan={6}
                        className='text-muted-foreground py-8 text-center'
                      >
                        {t('No subscription records')}
                      </TableCell>
                    </TableRow>
                  ) : (
                    subs.map((record) => {
                      const sub = record.subscription
                      const now = Date.now() / 1000
                      const isExpired =
                        (sub.end_time || 0) > 0 && sub.end_time < now
                      const isActive = sub.status === 'active' && !isExpired
                      const isResettable =
                        (sub.status === 'active' ||
                          sub.status === 'exhausted') &&
                        !isExpired
                      const total = Number(sub.amount_total || 0)
                      const used = Number(sub.amount_used || 0)

                      return (
                        <TableRow key={sub.id}>
                          <TableCell>#{sub.id}</TableCell>
                          <TableCell>
                            <div>
                              <div className='font-medium'>
                                {planTitleMap.get(sub.plan_id) ||
                                  `#${sub.plan_id}`}
                              </div>
                              <div className='text-muted-foreground text-xs'>
                                {t('Source')}: {sub.source || '-'}
                              </div>
                            </div>
                          </TableCell>
                          <TableCell>
                            <SubscriptionStatusBadge sub={sub} t={t} />
                          </TableCell>
                          <TableCell>
                            <div className='text-xs'>
                              <div>
                                {t('Start')}: {formatTimestamp(sub.start_time)}
                              </div>
                              <div>
                                {t('End')}: {formatTimestamp(sub.end_time)}
                              </div>
                            </div>
                          </TableCell>
                          <TableCell>
                            {total > 0 ? `${used}/${total}` : t('Unlimited')}
                          </TableCell>
                          <TableCell className='text-right'>
                            <div className='flex justify-end gap-1'>
                              <Button
                                size='sm'
                                variant='outline'
                                disabled={!isResettable}
                                onClick={() => {
                                  setAdvanceResetTime(true)
                                  setResetAction({
                                    planId: sub.plan_id,
                                    planTitle:
                                      planTitleMap.get(sub.plan_id) ||
                                      `#${sub.plan_id}`,
                                  })
                                }}
                              >
                                <RotateCcw className='mr-1 h-3.5 w-3.5' />
                                {t('Reset quota')}
                              </Button>
                              <Button
                                size='sm'
                                variant='outline'
                                onClick={() => openEditExpiry(sub)}
                              >
                                {t('Edit')}
                              </Button>
                              <Button
                                size='sm'
                                variant='outline'
                                disabled={!isActive}
                                onClick={() =>
                                  setConfirmAction({
                                    type: 'invalidate',
                                    subId: sub.id,
                                  })
                                }
                              >
                                {t('Invalidate')}
                              </Button>
                              <Button
                                size='sm'
                                variant='destructive'
                                onClick={() =>
                                  setConfirmAction({
                                    type: 'delete',
                                    subId: sub.id,
                                  })
                                }
                              >
                                {t('Delete')}
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                      )
                    })
                  )}
                </TableBody>
              </Table>
            </div>
          </div>
        </SheetContent>
      </Sheet>

      <Dialog
        open={!!editingExpiry}
        onOpenChange={(v) => {
          if (!v) closeEditExpiry()
        }}
      >
        <DialogContent onCloseAutoFocus={(e) => e.preventDefault()}>
          <DialogHeader>
            <DialogTitle>{t('Edit validity')}</DialogTitle>
            <DialogDescription>
              {t('Adjust the end time of this subscription.')}
            </DialogDescription>
          </DialogHeader>
          <div className='py-4'>
            <DateTimePicker
              value={editExpiryValue}
              onChange={setEditExpiryValue}
              placeholder={t('Pick end time')}
            />
          </div>
          <DialogFooter>
            <Button
              variant='outline'
              onClick={closeEditExpiry}
              disabled={editExpirySubmitting}
            >
              {t('Cancel')}
            </Button>
            <Button
              onClick={submitEditExpiry}
              disabled={!editExpiryValue || editExpirySubmitting}
            >
              {editExpirySubmitting ? t('Saving...') : t('Save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {confirmAction && (
        <ConfirmDialog
          open
          onOpenChange={(v) => !v && setConfirmAction(null)}
          title={
            confirmAction.type === 'invalidate'
              ? t('Confirm invalidate')
              : t('Confirm delete')
          }
          desc={
            confirmAction.type === 'invalidate'
              ? t(
                  'After invalidating, this subscription will be immediately deactivated. Historical records are not affected. Continue?'
                )
              : t(
                  'Deleting will permanently remove this subscription record (including benefit details). Continue?'
                )
          }
          handleConfirm={handleConfirmAction}
          destructive={confirmAction.type === 'delete'}
        />
      )}

      {resetAction && (
        <ConfirmDialog
          open
          onOpenChange={(v) => !v && setResetAction(null)}
          title={t('Reset subscription quota')}
          desc={t('Reset eligible {{plan}} subscriptions for this user?', {
            plan: resetAction.planTitle,
          })}
          confirmText={t('Reset quota')}
          handleConfirm={handleResetConfirm}
          isLoading={resetting}
        >
          <label className='flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm'>
            <span>{t('Advance next reset time')}</span>
            <Switch
              checked={advanceResetTime}
              onCheckedChange={(checked) => setAdvanceResetTime(checked)}
              aria-label={t('Advance next reset time')}
            />
          </label>
        </ConfirmDialog>
      )}
    </>
  )
}

import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import {
  CheckCircle2,
  Loader2,
  Pencil,
  Plus,
  ShieldAlert,
  ShieldQuestion,
  Trash2,
  TriangleAlert,
  WifiOff,
} from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
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

import {
  deleteSubSite,
  listSubSites,
  upsertSubSite,
  verifySubSite,
} from '../api'
import type { SubSite, SubSiteVerifyStatus } from '../types'
import { SubSiteEditDialog } from './sub-site-edit-dialog'

dayjs.extend(relativeTime)

export const SUB_SITES_QUERY_KEY = ['system-settings', 'sub-sites'] as const

const statusVariant: Record<
  SubSiteVerifyStatus,
  { label: string; cls: string; Icon: typeof CheckCircle2 }
> = {
  '': { label: 'Not Verified', cls: 'bg-muted text-muted-foreground', Icon: ShieldQuestion },
  ok: { label: 'OK', cls: 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400', Icon: CheckCircle2 },
  auth_failed: { label: 'Auth Failed', cls: 'bg-red-500/15 text-red-700 dark:text-red-400', Icon: ShieldAlert },
  role_insufficient: { label: 'Role Insufficient', cls: 'bg-amber-500/15 text-amber-700 dark:text-amber-400', Icon: TriangleAlert },
  network_error: { label: 'Network Error', cls: 'bg-orange-500/15 text-orange-700 dark:text-orange-400', Icon: WifiOff },
  unknown: { label: 'Unknown', cls: 'bg-muted text-muted-foreground', Icon: ShieldQuestion },
}

export function SubSiteSettingsSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editOpen, setEditOpen] = useState(false)
  const [editing, setEditing] = useState<SubSite | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<SubSite | null>(null)
  const [verifyingId, setVerifyingId] = useState<number | null>(null)

  const { data: sites = [], isLoading } = useQuery({
    queryKey: SUB_SITES_QUERY_KEY,
    queryFn: listSubSites,
  })

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: SUB_SITES_QUERY_KEY })

  const upsertMutation = useMutation({
    mutationFn: upsertSubSite,
    onSuccess: (res) => {
      if (!res.success) {
        toast.error(res.message ?? t('Save failed'))
        return
      }
      toast.success(t('Saved'))
      setEditOpen(false)
      setEditing(null)
      invalidate()
    },
    onError: (e: Error) => toast.error(e.message ?? t('Save failed')),
  })

  const deleteMutation = useMutation({
    mutationFn: deleteSubSite,
    onSuccess: (res) => {
      if (!res.success) {
        toast.error(res.message ?? t('Delete failed'))
        return
      }
      toast.success(t('Deleted'))
      setConfirmDelete(null)
      invalidate()
    },
    onError: (e: Error) => toast.error(e.message ?? t('Delete failed')),
  })

  const verifyMutation = useMutation({
    mutationFn: ({ id }: { id: number }) => verifySubSite({ id }),
    onMutate: ({ id }) => setVerifyingId(id),
    onSettled: () => setVerifyingId(null),
    onSuccess: (res, vars) => {
      if (!res.success || !res.data) {
        toast.error(res.message ?? t('Verify failed'))
        return
      }
      const s = res.data.status
      if (s === 'ok') {
        toast.success(
          t('Verified OK · {{ms}}ms · {{ver}}', {
            ms: res.data.latency_ms,
            ver: res.data.version ?? '',
          })
        )
      } else {
        toast.warning(`${s}: ${res.data.message ?? ''}`)
      }
      void vars
      invalidate()
    },
    onError: (e: Error) => toast.error(e.message ?? t('Verify failed')),
  })

  const sorted = useMemo(
    () => [...sites].sort((a, b) => (b.updated_time ?? 0) - (a.updated_time ?? 0)),
    [sites]
  )

  return (
    <TooltipProvider>
      <div className='space-y-4'>
        <div className='flex items-center justify-between'>
          <div>
            <h3 className='text-base font-semibold'>{t('Sub-Site Sync')}</h3>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Upstream new-api sites used to sync groups & pricing and quickly create channels.'
              )}
            </p>
          </div>
          <Button
            size='sm'
            onClick={() => {
              setEditing(null)
              setEditOpen(true)
            }}
          >
            <Plus className='h-4 w-4' />
            {t('Add Sub-Site')}
          </Button>
        </div>

        <div className='rounded-md border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Name')}</TableHead>
                <TableHead>{t('Base URL')}</TableHead>
                <TableHead>{t('Enabled')}</TableHead>
                <TableHead>{t('Last Verified')}</TableHead>
                <TableHead>{t('Note')}</TableHead>
                <TableHead className='text-right'>{t('Actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {isLoading && (
                <TableRow>
                  <TableCell colSpan={6} className='text-muted-foreground text-center'>
                    <Loader2 className='mr-2 inline h-4 w-4 animate-spin' />
                    {t('Loading...')}
                  </TableCell>
                </TableRow>
              )}
              {!isLoading && sorted.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className='text-muted-foreground text-center'>
                    {t('No sub-sites yet. Click "Add Sub-Site" to start.')}
                  </TableCell>
                </TableRow>
              )}
              {sorted.map((s) => {
                const variant = statusVariant[s.last_verified_status ?? '']
                const StatusIcon = variant.Icon
                const verifiedAtText =
                  s.last_verified_at > 0
                    ? dayjs.unix(s.last_verified_at).fromNow()
                    : t('Never')
                const verifiedAtAbsolute =
                  s.last_verified_at > 0
                    ? dayjs.unix(s.last_verified_at).format('YYYY-MM-DD HH:mm:ss')
                    : ''
                return (
                  <TableRow key={s.id}>
                    <TableCell className='font-medium'>{s.name}</TableCell>
                    <TableCell className='text-muted-foreground truncate max-w-[16rem]'>
                      {s.base_url}
                    </TableCell>
                    <TableCell>
                      <Badge variant={s.enabled ? 'default' : 'outline'}>
                        {s.enabled ? t('Enabled') : t('Disabled')}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span
                            className={`inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs ${variant.cls}`}
                          >
                            <StatusIcon className='h-3 w-3' />
                            {t(variant.label)} · {verifiedAtText}
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>
                          <div className='space-y-0.5 text-xs'>
                            <div>{verifiedAtAbsolute || t('Never verified')}</div>
                            {s.last_verified_version && (
                              <div>version: {s.last_verified_version}</div>
                            )}
                            {s.last_verified_latency_ms > 0 && (
                              <div>latency: {s.last_verified_latency_ms} ms</div>
                            )}
                            {s.last_verified_msg && (
                              <div className='text-destructive'>
                                {s.last_verified_msg}
                              </div>
                            )}
                          </div>
                        </TooltipContent>
                      </Tooltip>
                    </TableCell>
                    <TableCell className='text-muted-foreground truncate max-w-[12rem]'>
                      {s.note}
                    </TableCell>
                    <TableCell className='text-right'>
                      <div className='flex justify-end gap-1'>
                        <Button
                          size='sm'
                          variant='outline'
                          disabled={verifyingId === s.id || verifyMutation.isPending}
                          onClick={() => verifyMutation.mutate({ id: s.id })}
                        >
                          {verifyingId === s.id ? (
                            <Loader2 className='h-4 w-4 animate-spin' />
                          ) : (
                            t('Verify')
                          )}
                        </Button>
                        <Button
                          size='sm'
                          variant='ghost'
                          onClick={() => {
                            setEditing(s)
                            setEditOpen(true)
                          }}
                        >
                          <Pencil className='h-4 w-4' />
                        </Button>
                        <Button
                          size='sm'
                          variant='ghost'
                          className='text-destructive hover:text-destructive'
                          onClick={() => setConfirmDelete(s)}
                        >
                          <Trash2 className='h-4 w-4' />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>

        <SubSiteEditDialog
          open={editOpen}
          onOpenChange={(o) => {
            setEditOpen(o)
            if (!o) setEditing(null)
          }}
          onSave={(payload) => upsertMutation.mutate(payload)}
          saving={upsertMutation.isPending}
          editing={editing}
        />

        <AlertDialog
          open={!!confirmDelete}
          onOpenChange={(o) => {
            if (!o) setConfirmDelete(null)
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {t('Delete sub-site "{{name}}"?', {
                  name: confirmDelete?.name ?? '',
                })}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t(
                  'This will remove the upstream entry. Channels already created from it remain.'
                )}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
              <AlertDialogAction
                onClick={() =>
                  confirmDelete && deleteMutation.mutate(confirmDelete.id)
                }
              >
                {t('Delete')}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>
    </TooltipProvider>
  )
}

import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ChevronLeft,
  ChevronRight,
  Loader2,
  RefreshCw,
  Send,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatDateTimeObject } from '@/lib/time'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  getPendingPriceChanges,
  getPriceChangeBatch,
  getPriceChangeBatches,
  publishPriceChanges,
} from '@/features/price-changes/api'
import {
  ChangeItemsTable,
  SummaryBadges,
} from '@/features/price-changes/components'
import { PRICE_CHANGES_QUERY_KEY } from '@/features/price-changes/hooks'
import type { PriceChangeBatchMeta } from '@/features/price-changes/types'

export const PENDING_PRICE_CHANGES_QUERY_KEY = ['price-changes-pending']

const HISTORY_PAGE_SIZE = 10

function EmailStateCell({ batch }: { batch: PriceChangeBatchMeta }) {
  const { t } = useTranslation()
  switch (batch.email_state) {
    case 'sending':
      return (
        <span className='inline-flex items-center gap-1 text-xs text-sky-600 dark:text-sky-400'>
          <Loader2 className='size-3 animate-spin' />
          {t('Sending')} {batch.email_sent + batch.email_failed}/
          {batch.email_total}
        </span>
      )
    case 'done':
      return (
        <span className='text-xs text-emerald-600 dark:text-emerald-400'>
          {t('Sent')} {batch.email_sent}/{batch.email_total}
        </span>
      )
    case 'partial':
      return (
        <span className='text-xs text-amber-600 dark:text-amber-400'>
          {t('Partially failed')} ({batch.email_failed})
        </span>
      )
    default:
      return <span className='text-muted-foreground text-xs'>—</span>
  }
}

/**
 * Admin panel: preview pending price changes, publish them (optionally with
 * email notification), watch email progress and browse publish history.
 */
export function PricePublishPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [note, setNote] = useState('')
  const [sendEmail, setSendEmail] = useState(true)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [trackedBatchId, setTrackedBatchId] = useState<number | null>(null)
  const [historyPage, setHistoryPage] = useState(1)

  const pendingQuery = useQuery({
    queryKey: PENDING_PRICE_CHANGES_QUERY_KEY,
    queryFn: getPendingPriceChanges,
    retry: false,
  })
  const pending = pendingQuery.data

  const historyQuery = useQuery({
    queryKey: ['price-change-batches', historyPage],
    queryFn: () => getPriceChangeBatches(historyPage, HISTORY_PAGE_SIZE),
  })
  const history = historyQuery.data
  const historyTotalPages = Math.max(
    1,
    Math.ceil((history?.total ?? 0) / HISTORY_PAGE_SIZE)
  )

  // Poll the freshly published batch while emails are being sent
  const trackedBatchQuery = useQuery({
    queryKey: ['price-change-batch', trackedBatchId],
    queryFn: () => getPriceChangeBatch(trackedBatchId as number),
    enabled: trackedBatchId != null,
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.email_state === 'sending' ? 3000 : false,
  })
  const trackedBatch = trackedBatchQuery.data ?? null

  // Refresh the history list once email sending settles
  const trackedEmailState = trackedBatch?.email_state
  useEffect(() => {
    if (trackedEmailState === 'done' || trackedEmailState === 'partial') {
      queryClient.invalidateQueries({ queryKey: ['price-change-batches'] })
    }
  }, [trackedEmailState, queryClient])

  const publishMutation = useMutation({
    mutationFn: publishPriceChanges,
    onSuccess: (data) => {
      setConfirmOpen(false)
      if (!data.success) {
        // Business failure toast is already shown by the api interceptor
        pendingQuery.refetch()
        return
      }
      toast.success(t('Price changes published'))
      setNote('')
      if (data.data?.batch_id != null) {
        setTrackedBatchId(data.data.batch_id)
      }
      setHistoryPage(1)
      queryClient.invalidateQueries({
        queryKey: PENDING_PRICE_CHANGES_QUERY_KEY,
      })
      queryClient.invalidateQueries({ queryKey: ['price-change-batches'] })
      queryClient.invalidateQueries({ queryKey: PRICE_CHANGES_QUERY_KEY })
    },
  })

  const impactSummary = useMemo(() => {
    if (!pending?.has_changes) return ''
    return sendEmail
      ? t(
          '{{groups}} group(s) / {{models}} model(s) affected, about {{users}} user(s) will receive an email notification.',
          {
            groups: pending.group_count,
            models: pending.model_count,
            users: pending.estimated_email_users,
          }
        )
      : t(
          '{{groups}} group(s) / {{models}} model(s) affected, no email will be sent.',
          {
            groups: pending.group_count,
            models: pending.model_count,
          }
        )
  }, [pending, sendEmail, t])

  const emailProgress =
    trackedBatch && trackedBatch.email_total > 0
      ? Math.min(
          100,
          Math.round(
            ((trackedBatch.email_sent + trackedBatch.email_failed) /
              trackedBatch.email_total) *
              100
          )
        )
      : 0

  return (
    <div className='space-y-6'>
      {/* Pending changes */}
      <div className='space-y-3 rounded-lg border p-4'>
        <div className='flex items-center justify-between gap-2'>
          <div>
            <h4 className='text-sm font-medium'>
              {t('Unpublished price changes')}
            </h4>
            <p className='text-muted-foreground mt-0.5 text-xs'>
              {t(
                'Changes are computed against the last published snapshot and only become visible to users after publishing.'
              )}
            </p>
          </div>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='shrink-0 gap-1.5'
            onClick={() => pendingQuery.refetch()}
            disabled={pendingQuery.isFetching}
          >
            <RefreshCw
              className={
                pendingQuery.isFetching ? 'size-3.5 animate-spin' : 'size-3.5'
              }
            />
            {t('Refresh')}
          </Button>
        </div>

        {pendingQuery.isLoading ? (
          <p className='text-muted-foreground py-6 text-center text-sm'>
            {t('Loading...')}
          </p>
        ) : pendingQuery.isError ? (
          <p className='text-muted-foreground py-6 text-center text-sm'>
            {t('Failed to load pending price changes')}
          </p>
        ) : !pending?.has_changes ? (
          <p className='text-muted-foreground py-6 text-center text-sm'>
            {t('No unpublished price changes')}
          </p>
        ) : (
          <>
            <div className='flex flex-wrap items-center gap-2 text-sm'>
              <Badge variant='secondary'>
                {t('{{count}} group(s)', { count: pending.group_count })}
              </Badge>
              <Badge variant='secondary'>
                {t('{{count}} model(s)', { count: pending.model_count })}
              </Badge>
              <Badge variant='secondary'>
                {t('~{{count}} email recipient(s)', {
                  count: pending.estimated_email_users,
                })}
              </Badge>
            </div>

            <ChangeItemsTable items={pending.items ?? []} />

            <div className='space-y-3 pt-1'>
              <div className='space-y-1.5'>
                <Label htmlFor='priceChangeNote'>
                  {t('Note (optional, shown to users)')}
                </Label>
                <Textarea
                  id='priceChangeNote'
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  placeholder={t('e.g. upstream price adjustment')}
                  rows={2}
                />
              </div>

              <div className='flex items-center justify-between gap-3 rounded-lg border p-3'>
                <div className='space-y-0.5'>
                  <Label htmlFor='priceChangeSendEmail'>
                    {t('Also notify affected users by email')}
                  </Label>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Only users in affected groups who have not opted out will be notified.'
                    )}
                  </p>
                </div>
                <Switch
                  id='priceChangeSendEmail'
                  className='shrink-0'
                  checked={sendEmail}
                  onCheckedChange={setSendEmail}
                />
              </div>

              <div className='flex justify-end'>
                <Button
                  type='button'
                  className='gap-1.5'
                  onClick={() => setConfirmOpen(true)}
                  disabled={publishMutation.isPending}
                >
                  <Send className='size-4' />
                  {t('Publish price changes')}
                </Button>
              </div>
            </div>
          </>
        )}
      </div>

      {/* Email sending progress of the freshly published batch */}
      {trackedBatch && trackedBatch.email_state !== 'none' && (
        <div className='space-y-2 rounded-lg border p-4'>
          <div className='flex items-center justify-between gap-2 text-sm'>
            <span className='font-medium'>
              {t('Email notification progress (batch #{{id}})', {
                id: trackedBatch.id,
              })}
            </span>
            <EmailStateCell batch={trackedBatch} />
          </div>
          {trackedBatch.email_state === 'sending' && (
            <Progress value={emailProgress} />
          )}
        </div>
      )}

      {/* Publish history */}
      <div className='space-y-3'>
        <h4 className='text-sm font-medium'>{t('Publish history')}</h4>
        <div className='overflow-hidden rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Time')}</TableHead>
                <TableHead>{t('Note')}</TableHead>
                <TableHead>{t('Changes')}</TableHead>
                <TableHead>{t('Email Status')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {historyQuery.isLoading ? (
                <TableRow>
                  <TableCell
                    colSpan={4}
                    className='text-muted-foreground py-6 text-center text-sm'
                  >
                    {t('Loading...')}
                  </TableCell>
                </TableRow>
              ) : (history?.items ?? []).length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={4}
                    className='text-muted-foreground py-6 text-center text-sm'
                  >
                    {t('No publish history')}
                  </TableCell>
                </TableRow>
              ) : (
                (history?.items ?? []).map((batch) => (
                  <TableRow key={batch.id}>
                    <TableCell className='text-xs whitespace-nowrap'>
                      {formatDateTimeObject(
                        new Date(batch.published_at * 1000)
                      )}
                    </TableCell>
                    <TableCell className='max-w-[240px]'>
                      <span className='text-muted-foreground line-clamp-2 text-xs'>
                        {batch.is_baseline ? t('Baseline snapshot') : batch.note || '—'}
                      </span>
                    </TableCell>
                    <TableCell>
                      {batch.is_baseline ? (
                        <span className='text-muted-foreground text-xs'>—</span>
                      ) : (
                        <SummaryBadges summary={batch.summary} />
                      )}
                    </TableCell>
                    <TableCell>
                      <EmailStateCell batch={batch} />
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>

        {historyTotalPages > 1 && (
          <div className='flex items-center justify-end gap-2'>
            <span className='text-muted-foreground text-xs'>
              {t('Page {{current}} of {{total}}', {
                current: historyPage,
                total: historyTotalPages,
              })}
            </span>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => setHistoryPage((p) => Math.max(1, p - 1))}
              disabled={historyPage <= 1}
            >
              <ChevronLeft className='size-4' />
            </Button>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() =>
                setHistoryPage((p) => Math.min(historyTotalPages, p + 1))
              }
              disabled={historyPage >= historyTotalPages}
            >
              <ChevronRight className='size-4' />
            </Button>
          </div>
        )}
      </div>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Publish price changes?')}
        desc={impactSummary}
        isLoading={publishMutation.isPending}
        handleConfirm={() =>
          publishMutation.mutate({ note: note.trim(), send_email: sendEmail })
        }
        confirmText={t('Publish')}
      />
    </div>
  )
}

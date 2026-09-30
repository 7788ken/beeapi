import { useState } from 'react'
import { ChevronLeft, ChevronRight, Eye, RefreshCw, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { surfaceClass } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
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
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { TableEmpty } from '@/components/data-table'
import {
  describeApiError,
  useContentBackupJobs,
  useContentBackupNodes,
  useContentBackupRetry,
  useContentBackupStatus,
} from '../api'
import {
  clampRetryBatch,
  emptyStateDescription,
  emptyStateLabel,
  formatBytes,
  formatTimestamp,
  isRetryableStatus,
  parseRetryReason,
  retryBlockedLabel,
  retryOutcomeLabel,
  retryOutcomeTone,
  sortQueueJobs,
  statusLabel,
  statusTone,
} from '../status'
import {
  CONTENT_BACKUP_MAX_RETRY_BATCH,
  CONTENT_BACKUP_STATUS,
  type ContentBackupJob,
  type ContentBackupListFilter,
  type ContentBackupRetryResult,
} from '../types'

export interface QueueViewProps {
  canView: boolean
  canManage: boolean
  drawerOpen: boolean
  filter: ContentBackupListFilter
  onFilterChange: (filter: ContentBackupListFilter) => void
  onOpenJob: (job: ContentBackupJob) => void
}

export function QueueView(props: QueueViewProps) {
  const { t } = useTranslation()
  const [cursors, setCursors] = useState<(string | null)[]>([null])
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const [retryResults, setRetryResults] = useState<ContentBackupRetryResult[]>(
    []
  )

  const statusQuery = useContentBackupStatus({ enabled: props.canView })
  const nodesQuery = useContentBackupNodes({ enabled: props.canView })
  const retryMutation = useContentBackupRetry()
  const cursor = cursors[cursors.length - 1]

  const jobsQuery = useContentBackupJobs(
    {
      view: 'queue',
      status: props.filter.status,
      storage_node_id: props.filter.storageNodeId,
      from: props.filter.from,
      to: props.filter.to,
      cursor,
    },
    {
      enabled: props.canView,
      // 有选中项或抽屉打开时停止轮询：定时刷新不能让行在用户手里移动
      hold: props.drawerOpen || selected.size > 0,
    }
  )

  const items = sortQueueJobs(jobsQuery.data?.items ?? [])
  const nextCursor = jobsQuery.data?.next_cursor ?? null
  const hasMore = jobsQuery.data?.has_more === true
  const summary = statusQuery.data
  const nodes = nodesQuery.data ?? []
  const itemsById = new Map(items.map((item) => [item.job_id, item]))
  const selectedJobs = [...selected]
    .map((jobId) => itemsById.get(jobId))
    .filter((job): job is ContentBackupJob => job !== undefined)
  const selectedRetryable = selectedJobs.filter((job) =>
    isRetryableStatus(job.status)
  )
  const failedOnPage = items.filter((job) => isRetryableStatus(job.status))
  const atCap = selected.size >= CONTENT_BACKUP_MAX_RETRY_BATCH
  const lowestFreeBytes = nodes.reduce<number | undefined>((lowest, node) => {
    if (typeof node.free_bytes !== 'number') return lowest
    if (lowest === undefined) return node.free_bytes
    return Math.min(lowest, node.free_bytes)
  }, undefined)

  const updateFilter = (next: ContentBackupListFilter) => {
    // 筛选变化重置游标
    setCursors([null])
    props.onFilterChange(next)
  }

  const warnAboutCap = (rejectedCount: number) => {
    if (rejectedCount === 0) return
    toast.warning(
      t('At most {{max}} jobs can be retried in one batch.', {
        max: CONTENT_BACKUP_MAX_RETRY_BATCH,
      })
    )
  }

  const toggleSelect = (jobId: string, next: boolean) => {
    if (next && atCap) {
      warnAboutCap(1)
      return
    }
    const updated = new Set(selected)
    if (next) updated.add(jobId)
    else updated.delete(jobId)
    setSelected(updated)
  }

  const toggleSelectAll = (next: boolean) => {
    if (!next) {
      setSelected(new Set())
      return
    }
    const clamped = clampRetryBatch([
      ...selected,
      ...failedOnPage.map((job) => job.job_id),
    ])
    warnAboutCap(clamped.rejected.length)
    setSelected(new Set(clamped.accepted))
  }

  const runRetry = (jobIds: string[]) => {
    const clamped = clampRetryBatch(jobIds)
    warnAboutCap(clamped.rejected.length)
    if (clamped.accepted.length === 0) return
    retryMutation.mutate(clamped.accepted, {
      onSuccess: (results) => {
        setRetryResults(results)
        const queued = results.filter((item) => item.result === 'queued').length
        // 入队不是成功：只有上传完成才算备份成功
        toast.success(t('{{count}} jobs queued for upload', { count: queued }))
        setSelected(new Set())
      },
      onError: () => toast.error(t('Retry failed')),
    })
  }

  const emptyCtx = {
    canView: props.canView,
    isError: jobsQuery.isError,
    itemCount: items.length,
    configured: summary?.configured,
    enabled: summary?.enabled,
    uploadPaused: summary?.upload_paused,
  }
  const emptyKey = emptyStateLabel(emptyCtx)

  const allFailedSelected =
    failedOnPage.length > 0 &&
    failedOnPage.every((job) => selected.has(job.job_id))

  return (
    <div className='space-y-3'>
      {summary?.upload_paused ? (
        <Alert>
          <AlertDescription className='flex flex-wrap items-center gap-2'>
            <span>
              {t(
                'Uploads are paused. The backlog is kept and already uploaded files are still cleaned up.'
              )}
            </span>
            {lowestFreeBytes === undefined ? null : (
              <span className='tabular-nums'>
                {t('Lowest node free space')}: {formatBytes(lowestFreeBytes)}
              </span>
            )}
          </AlertDescription>
        </Alert>
      ) : null}

      <div className='flex flex-wrap items-end gap-2'>
        <div className='space-y-1'>
          <Label className='text-muted-foreground text-xs'>{t('Status')}</Label>
          <Select
            value={props.filter.status ?? 'all'}
            onValueChange={(value) =>
              updateFilter({
                ...props.filter,
                status: value === 'all' ? undefined : value,
              })
            }
          >
            <SelectTrigger className='h-10 w-[170px]'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('Failed first, then all')}</SelectItem>
              {CONTENT_BACKUP_STATUS.filter((status) => status !== 'uploaded').map(
                (status) => (
                  <SelectItem key={status} value={status}>
                    {t(statusLabel(status))}
                  </SelectItem>
                )
              )}
            </SelectContent>
          </Select>
        </div>

        <div className='space-y-1'>
          <Label className='text-muted-foreground text-xs'>{t('Node')}</Label>
          <Select
            value={props.filter.storageNodeId ?? 'all'}
            onValueChange={(value) =>
              updateFilter({
                ...props.filter,
                storageNodeId: value === 'all' ? undefined : value,
              })
            }
          >
            <SelectTrigger className='h-10 w-[180px]'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('All nodes')}</SelectItem>
              {nodes.map((node) => (
                <SelectItem
                  key={node.storage_node_id ?? '-'}
                  value={node.storage_node_id ?? '-'}
                >
                  {node.storage_node_id ?? '-'}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <Button
          variant='outline'
          className='h-10'
          disabled={jobsQuery.isFetching}
          onClick={() => void jobsQuery.refetch()}
        >
          <RefreshCw className='size-4' aria-hidden='true' />
          {t('Refresh')}
        </Button>

        {selected.size > 0 ? (
          <div className='flex flex-wrap items-center gap-2'>
            <span className='text-xs tabular-nums'>
              {t('{{count}} selected', { count: selected.size })}
              {selectedRetryable.length === selected.size
                ? ''
                : ` · ${t('{{count}} retryable', {
                    count: selectedRetryable.length,
                  })}`}
            </span>
            {props.canManage ? (
              <Button
                className='h-10'
                disabled={
                  retryMutation.isPending || selectedRetryable.length === 0
                }
                onClick={() =>
                  runRetry(selectedRetryable.map((job) => job.job_id))
                }
              >
                <RotateCcw className='size-4' aria-hidden='true' />
                {t('Retry selected')}
              </Button>
            ) : null}
            <Button
              variant='ghost'
              className='h-10'
              onClick={() => setSelected(new Set())}
            >
              {t('Clear selection')}
            </Button>
          </div>
        ) : null}
      </div>

      {props.canManage ? null : (
        <p className='text-muted-foreground text-xs'>
          {t('Retry requires the content backup manage permission.')}
        </p>
      )}

      {retryResults.length > 0 ? (
        <div className={cn(surfaceClass, 'space-y-1.5 p-3')}>
          <div className='flex items-center justify-between gap-2'>
            <span className='text-xs font-medium'>{t('Retry results')}</span>
            <Button
              variant='ghost'
              size='sm'
              className='h-10'
              onClick={() => setRetryResults([])}
            >
              {t('Close')}
            </Button>
          </div>
          {/* 部分失败必须逐行反馈，不能只给一个总数 */}
          <ul className='space-y-1'>
            {retryResults.map((result) => {
              const reason = parseRetryReason(result.reason)
              return (
                <li
                  key={result.job_id}
                  className='flex flex-wrap items-center gap-2 text-xs'
                >
                  <span className='max-w-[220px] truncate font-mono'>
                    {result.job_id}
                  </span>
                  <Badge variant={retryOutcomeTone(result.result)}>
                    {t(retryOutcomeLabel(result.result))}
                  </Badge>
                  {reason.blockedLabel ? (
                    <span className='text-destructive'>{t(reason.blockedLabel)}</span>
                  ) : (
                    <span className='text-muted-foreground break-all'>
                      {result.reason || '-'}
                    </span>
                  )}
                </li>
              )
            })}
          </ul>
        </div>
      ) : null}

      {jobsQuery.isError ? (
        <Alert variant='destructive'>
          <AlertDescription className='flex flex-wrap items-center gap-2'>
            <span>
              {describeApiError(jobsQuery.error) ??
                t('Content backup request failed')}
            </span>
            {jobsQuery.dataUpdatedAt ? (
              <span className='tabular-nums'>
                {t('Last successful data at {{time}}', {
                  time: formatTimestamp(
                    new Date(jobsQuery.dataUpdatedAt).toISOString()
                  ),
                })}
              </span>
            ) : null}
          </AlertDescription>
        </Alert>
      ) : null}

      <div className={cn(surfaceClass, 'overflow-x-auto')}>
        <Table className='min-w-[1200px] table-fixed'>
          <TableHeader>
            <TableRow>
              <TableHead className='w-[50px]'>
                <Checkbox
                  checked={allFailedSelected}
                  disabled={failedOnPage.length === 0}
                  onCheckedChange={(checked) => toggleSelectAll(checked === true)}
                  aria-label={t('Select all failed jobs')}
                />
              </TableHead>
              <TableHead className='w-[130px]'>{t('Status')}</TableHead>
              <TableHead className='w-[170px]'>{t('Time')}</TableHead>
              <TableHead className='w-[80px]'>{t('User')}</TableHead>
              <TableHead className='w-[150px]'>{t('Channel')}</TableHead>
              <TableHead className='w-[150px]'>{t('Node')}</TableHead>
              <TableHead className='w-[110px]'>{t('Attempts')}</TableHead>
              <TableHead className='w-[170px]'>{t('Next attempt at')}</TableHead>
              <TableHead className='w-[260px]'>{t('Reason')}</TableHead>
              <TableHead className='w-[120px] text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {jobsQuery.isLoading ? (
              <TableRow className='h-12'>
                <TableCell colSpan={10} className='text-center'>
                  {t('Loading...')}
                </TableCell>
              </TableRow>
            ) : items.length === 0 ? (
              <TableEmpty
                colSpan={10}
                title={emptyKey ? t(emptyKey) : t('No Data')}
                description={emptyStateDescription(
                  emptyCtx,
                  t('Nothing is waiting to be uploaded.')
                )}
              />
            ) : (
              items.map((job) => {
                const blocked = retryBlockedLabel(job.last_error_code)
                const retryable = isRetryableStatus(job.status)
                return (
                  <TableRow key={job.job_id} className='h-12 align-middle'>
                    <TableCell className='w-[50px]'>
                      <Checkbox
                        checked={selected.has(job.job_id)}
                        disabled={!retryable || (atCap && !selected.has(job.job_id))}
                        onCheckedChange={(checked) =>
                          toggleSelect(job.job_id, checked === true)
                        }
                        aria-label={t('Select job')}
                      />
                    </TableCell>
                    <TableCell className='w-[130px]'>
                      <Badge variant={statusTone(job.status)}>
                        {t(statusLabel(job.status))}
                      </Badge>
                    </TableCell>
                    <TableCell className='w-[170px] text-xs tabular-nums'>
                      {formatTimestamp(job.created_at)}
                    </TableCell>
                    <TableCell className='w-[80px] text-xs tabular-nums'>
                      {job.user_id ?? '-'}
                    </TableCell>
                    <TableCell className='w-[150px]'>
                      <div className='max-w-[140px] truncate text-xs'>
                        {job.channel_name || '-'}
                        {job.channel_id ? ` (#${job.channel_id})` : ''}
                      </div>
                    </TableCell>
                    <TableCell className='w-[150px]'>
                      <div className='max-w-[140px] truncate font-mono text-xs'>
                        {job.storage_node_id || '-'}
                      </div>
                    </TableCell>
                    <TableCell className='w-[110px] text-xs tabular-nums'>
                      {job.attempts ?? '-'} / {job.total_attempts ?? '-'}
                      {job.retry_round ? ` · ${t('Round {{round}}', { round: job.retry_round })}` : ''}
                    </TableCell>
                    <TableCell className='w-[170px] text-xs tabular-nums'>
                      {formatTimestamp(job.available_at)}
                    </TableCell>
                    <TableCell className='w-[260px]'>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <div className='max-w-[250px] truncate text-xs'>
                            {job.last_error_code ? (
                              <span className='font-mono'>
                                {job.last_error_code}
                              </span>
                            ) : null}
                            {job.last_error_message ? ` ${job.last_error_message}` : ''}
                            {blocked ? ` · ${t(blocked)}` : ''}
                          </div>
                        </TooltipTrigger>
                        <TooltipContent className='max-w-[420px]'>
                          <p className='break-all'>
                            {job.last_error_code || '-'}
                            {job.last_error_message ? `: ${job.last_error_message}` : ''}
                          </p>
                          {blocked ? <p>{t(blocked)}</p> : null}
                        </TooltipContent>
                      </Tooltip>
                    </TableCell>
                    <TableCell className='w-[120px]'>
                      <div className='flex justify-end gap-1'>
                        <Button
                          variant='ghost'
                          size='icon-lg'
                          aria-label={t('View detail')}
                          onClick={() => props.onOpenJob(job)}
                        >
                          <Eye className='size-4' aria-hidden='true' />
                        </Button>
                        {props.canManage && retryable ? (
                          <Button
                            variant='ghost'
                            size='icon-lg'
                            aria-label={t('Retry upload')}
                            disabled={retryMutation.isPending}
                            onClick={() => runRetry([job.job_id])}
                          >
                            <RotateCcw className='size-4' aria-hidden='true' />
                          </Button>
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })
            )}
          </TableBody>
        </Table>
      </div>

      <div className='flex flex-wrap items-center justify-between gap-2'>
        <span className='text-muted-foreground text-xs tabular-nums'>
          {t('Page {{page}}', { page: cursors.length })}
          {jobsQuery.isFetching ? ` · ${t('Refreshing...')}` : ''}
        </span>
        <div className='flex flex-wrap items-center gap-2'>
          <Button
            variant='outline'
            className='h-10'
            disabled={cursors.length <= 1 || jobsQuery.isFetching}
            onClick={() => setCursors((stack) => stack.slice(0, -1))}
          >
            <ChevronLeft className='size-4' aria-hidden='true' />
            {t('Prev')}
          </Button>
          <Button
            variant='outline'
            className='h-10'
            disabled={!hasMore || !nextCursor || jobsQuery.isFetching}
            onClick={() => setCursors((stack) => [...stack, nextCursor])}
          >
            {t('Next')}
            <ChevronRight className='size-4' aria-hidden='true' />
          </Button>
        </div>
      </div>
    </div>
  )
}

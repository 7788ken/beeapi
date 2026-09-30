import { useState } from 'react'
import { ChevronLeft, ChevronRight, Eye, RefreshCw, Search, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import dayjs from '@/lib/dayjs'
import { cn } from '@/lib/utils'
import { useDebounce } from '@/hooks/use-debounce'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { surfaceClass } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
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
  useContentBackupSessionJobs,
  useContentBackupStatus,
} from '../api'
import {
  cleanupLabel,
  cleanupTone,
  emptyStateDescription,
  emptyStateLabel,
  formatBytes,
  formatTimestamp,
  integrityLabel,
  integrityTone,
  isWithinMaxRange,
  statusLabel,
  statusTone,
} from '../status'
import {
  CONTENT_BACKUP_CLEANUP_STATE,
  CONTENT_BACKUP_MAX_RANGE_DAYS,
  CONTENT_BACKUP_SESSION_SOURCE,
  CONTENT_BACKUP_STATUS,
  type ContentBackupJob,
  type ContentBackupListFilter,
  type ContentBackupSessionSearchPayload,
} from '../types'
import { CopyValueButton } from './detail-drawer'

type RangePreset = '24h' | '7d' | '31d' | 'all' | 'custom'

const PRESET_HOURS: Record<string, number> = {
  '24h': 24,
  '7d': 24 * 7,
  '31d': 24 * 31,
}

function presetRange(preset: RangePreset): { from?: string; to?: string } {
  const hours = PRESET_HOURS[preset]
  if (!hours) return {}
  return { from: new Date(Date.now() - hours * 3600 * 1000).toISOString() }
}

function toLocalInput(value?: string): string {
  if (!value) return ''
  const parsed = dayjs(value)
  if (!parsed.isValid()) return ''
  return parsed.format('YYYY-MM-DDTHH:mm')
}

function fromLocalInput(value: string): string | undefined {
  if (!value) return undefined
  const parsed = dayjs(value)
  if (!parsed.isValid()) return undefined
  return parsed.toISOString()
}

function parsePositiveInt(value: string): number | undefined {
  const trimmed = value.trim()
  if (trimmed === '') return undefined
  const parsed = Number(trimmed)
  if (!Number.isInteger(parsed) || parsed <= 0) return undefined
  return parsed
}

export interface ArchivesViewProps {
  canView: boolean
  isRoot: boolean
  drawerOpen: boolean
  filter: ContentBackupListFilter
  onFilterChange: (filter: ContentBackupListFilter) => void
  onOpenJob: (job: ContentBackupJob) => void
}

export function ArchivesView(props: ArchivesViewProps) {
  const { t } = useTranslation()
  const [cursors, setCursors] = useState<(string | null)[]>([null])
  const [rangePreset, setRangePreset] = useState<RangePreset>('24h')
  const [sessionPanelOpen, setSessionPanelOpen] = useState(false)
  const [sessionValue, setSessionValue] = useState('')
  const [sessionUserId, setSessionUserId] = useState('')
  const [sessionSource, setSessionSource] = useState('')
  const [sessionFrom, setSessionFrom] = useState('')
  const [sessionTo, setSessionTo] = useState('')
  const [sessionSearch, setSessionSearch] =
    useState<ContentBackupSessionSearchPayload | null>(null)

  const statusQuery = useContentBackupStatus({ enabled: props.canView })
  const nodesQuery = useContentBackupNodes({ enabled: props.canView })

  const debouncedRequestId = useDebounce(props.filter.requestId ?? '', 400)
  const debouncedUserId = useDebounce(props.filter.userId ?? '', 400)
  const debouncedChannelId = useDebounce(props.filter.channelId ?? '', 400)
  const cursor = cursors[cursors.length - 1]

  // 精确 request_id 查询可覆盖完整索引保留期，此时不再叠加时间范围
  const useRange = debouncedRequestId.trim() === ''
  const rangeValid = isWithinMaxRange(props.filter.from, props.filter.to)
  const jobsQuery = useContentBackupJobs(
    {
      view: 'archive',
      request_id: debouncedRequestId.trim() || undefined,
      user_id: parsePositiveInt(debouncedUserId),
      channel_id: parsePositiveInt(debouncedChannelId),
      storage_node_id: props.filter.storageNodeId,
      status: props.filter.status,
      cleanup_state: props.filter.cleanupState,
      from: useRange ? props.filter.from : undefined,
      to: useRange ? props.filter.to : undefined,
      cursor,
    },
    {
      enabled: props.canView && sessionSearch === null && rangeValid,
      hold: props.drawerOpen,
    }
  )
  const sessionQuery = useContentBackupSessionJobs(
    sessionSearch ? { ...sessionSearch, cursor } : null,
    { enabled: props.canView && sessionSearch !== null }
  )

  const activeQuery = sessionSearch ? sessionQuery : jobsQuery
  const items = activeQuery.data?.items ?? []
  const nextCursor = activeQuery.data?.next_cursor ?? null
  const hasMore = activeQuery.data?.has_more === true
  const nodes = nodesQuery.data ?? []
  const summary = statusQuery.data

  const updateFilter = (next: ContentBackupListFilter) => {
    // 筛选变化必须重置游标，否则会拿着上一页的位置查新条件
    setCursors([null])
    props.onFilterChange(next)
  }

  const applyPreset = (preset: RangePreset) => {
    setRangePreset(preset)
    updateFilter({ ...props.filter, ...presetRange(preset) })
  }

  const refresh = () => {
    if (sessionSearch) void sessionQuery.refetch()
    else void jobsQuery.refetch()
  }

  const submitSessionSearch = () => {
    const value = sessionValue.trim()
    if (value === '') return
    setCursors([null])
    setSessionSearch({
      session_value: value,
      user_id: parsePositiveInt(sessionUserId),
      session_source: sessionSource || undefined,
      from: fromLocalInput(sessionFrom),
      to: fromLocalInput(sessionTo),
    })
  }

  const exitSessionSearch = () => {
    setSessionSearch(null)
    setSessionValue('')
    setCursors([null])
  }

  const emptyCtx = {
    canView: props.canView,
    isError: activeQuery.isError,
    itemCount: items.length,
    configured: summary?.configured,
    enabled: summary?.enabled,
  }
  const emptyKey = emptyStateLabel(emptyCtx)

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-end gap-2'>
        <div className='space-y-1'>
          <Label className='text-muted-foreground text-xs'>
            {t('Time range')}
          </Label>
          <Select
            value={rangePreset}
            onValueChange={(value) => applyPreset(value as RangePreset)}
          >
            <SelectTrigger className='h-10 w-[150px]'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='24h'>{t('Last 24 hours')}</SelectItem>
              <SelectItem value='7d'>{t('Last 7 days')}</SelectItem>
              <SelectItem value='31d'>{t('Last 31 days')}</SelectItem>
              <SelectItem value='all'>{t('Whole retention period')}</SelectItem>
              <SelectItem value='custom'>{t('Custom range')}</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {rangePreset === 'custom' ? (
          <>
            <div className='space-y-1'>
              <Label className='text-muted-foreground text-xs'>{t('From')}</Label>
              <Input
                type='datetime-local'
                className='h-10 w-[200px]'
                value={toLocalInput(props.filter.from)}
                onChange={(event) =>
                  updateFilter({
                    ...props.filter,
                    from: fromLocalInput(event.target.value),
                  })
                }
              />
            </div>
            <div className='space-y-1'>
              <Label className='text-muted-foreground text-xs'>{t('To')}</Label>
              <Input
                type='datetime-local'
                className='h-10 w-[200px]'
                value={toLocalInput(props.filter.to)}
                onChange={(event) =>
                  updateFilter({
                    ...props.filter,
                    to: fromLocalInput(event.target.value),
                  })
                }
              />
            </div>
          </>
        ) : null}

        <div className='space-y-1'>
          <Label className='text-muted-foreground text-xs'>
            {t('Request ID')}
          </Label>
          <Input
            className='h-10 w-[220px] font-mono'
            placeholder={t('Exact request ID')}
            value={props.filter.requestId ?? ''}
            onChange={(event) =>
              updateFilter({
                ...props.filter,
                requestId: event.target.value || undefined,
              })
            }
          />
        </div>

        <div className='space-y-1'>
          <Label className='text-muted-foreground text-xs'>{t('User')}</Label>
          <Input
            className='h-10 w-[110px] tabular-nums'
            placeholder={t('User ID')}
            inputMode='numeric'
            value={props.filter.userId ?? ''}
            onChange={(event) =>
              updateFilter({ ...props.filter, userId: event.target.value })
            }
          />
        </div>

        <div className='space-y-1'>
          <Label className='text-muted-foreground text-xs'>
            {t('Channel')}
          </Label>
          <Input
            className='h-10 w-[110px] tabular-nums'
            placeholder={t('Channel ID')}
            inputMode='numeric'
            value={props.filter.channelId ?? ''}
            onChange={(event) =>
              updateFilter({ ...props.filter, channelId: event.target.value })
            }
          />
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
            <SelectTrigger className='h-10 w-[170px]'>
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

        <div className='space-y-1'>
          <Label className='text-muted-foreground text-xs'>
            {t('Status')}
          </Label>
          <Select
            value={props.filter.status ?? 'all'}
            onValueChange={(value) =>
              updateFilter({
                ...props.filter,
                status: value === 'all' ? undefined : value,
              })
            }
          >
            <SelectTrigger className='h-10 w-[160px]'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('All statuses')}</SelectItem>
              {CONTENT_BACKUP_STATUS.map((status) => (
                <SelectItem key={status} value={status}>
                  {t(statusLabel(status))}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className='space-y-1'>
          <Label className='text-muted-foreground text-xs'>
            {t('Local cleanup')}
          </Label>
          <Select
            value={props.filter.cleanupState ?? 'all'}
            onValueChange={(value) =>
              updateFilter({
                ...props.filter,
                cleanupState: value === 'all' ? undefined : value,
              })
            }
          >
            <SelectTrigger className='h-10 w-[190px]'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('All cleanup states')}</SelectItem>
              {CONTENT_BACKUP_CLEANUP_STATE.map((state) => (
                <SelectItem key={state} value={state}>
                  {t(cleanupLabel('uploaded', state))}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <Button
          variant='outline'
          className='h-10'
          onClick={() => {
            setRangePreset('24h')
            setCursors([null])
            props.onFilterChange({ ...presetRange('24h') })
          }}
        >
          <RefreshCw className='size-4' aria-hidden='true' />
          {t('Reset')}
        </Button>

        <Button
          variant='outline'
          className='h-10'
          onClick={() => setSessionPanelOpen((open) => !open)}
          aria-expanded={sessionPanelOpen}
        >
          <Search className='size-4' aria-hidden='true' />
          {t('Search by session')}
        </Button>
      </div>

      {sessionPanelOpen ? (
        <div className={cn(surfaceClass, 'space-y-2 p-3')}>
          <p className='text-muted-foreground text-xs'>
            {t(
              'The raw session value is sent in the request body only. It is never written to the URL or to local storage.'
            )}
          </p>
          <div className='flex flex-wrap items-end gap-2'>
            <div className='space-y-1'>
              <Label className='text-muted-foreground text-xs'>
                {t('User ID')}
              </Label>
              <Input
                className='h-10 w-[120px] tabular-nums'
                inputMode='numeric'
                value={sessionUserId}
                onChange={(event) => setSessionUserId(event.target.value)}
              />
            </div>
            <div className='space-y-1'>
              <Label className='text-muted-foreground text-xs'>
                {t('Session value')}
              </Label>
              <Input
                className='h-10 w-[280px] font-mono'
                value={sessionValue}
                onChange={(event) => setSessionValue(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') submitSessionSearch()
                }}
              />
            </div>
            <div className='space-y-1'>
              <Label className='text-muted-foreground text-xs'>
                {t('Source')}
              </Label>
              <Select
                value={sessionSource || 'all'}
                onValueChange={(value) =>
                  setSessionSource(value === 'all' ? '' : value)
                }
              >
                <SelectTrigger className='h-10 w-[180px]'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='all'>{t('All sources')}</SelectItem>
                  {CONTENT_BACKUP_SESSION_SOURCE.map((source) => (
                    <SelectItem key={source} value={source}>
                      {source}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className='space-y-1'>
              <Label className='text-muted-foreground text-xs'>
                {t('From')}
              </Label>
              <Input
                type='datetime-local'
                className='h-10 w-[200px]'
                value={sessionFrom}
                onChange={(event) => setSessionFrom(event.target.value)}
              />
            </div>
            <div className='space-y-1'>
              <Label className='text-muted-foreground text-xs'>{t('To')}</Label>
              <Input
                type='datetime-local'
                className='h-10 w-[200px]'
                value={sessionTo}
                onChange={(event) => setSessionTo(event.target.value)}
              />
            </div>
            <Button
              className='h-10'
              disabled={sessionValue.trim() === '' || sessionQuery.isFetching}
              onClick={submitSessionSearch}
            >
              <Search className='size-4' aria-hidden='true' />
              {t('Search')}
            </Button>
            {sessionSearch ? (
              <Button variant='ghost' className='h-10' onClick={exitSessionSearch}>
                <X className='size-4' aria-hidden='true' />
                {t('Exit session search')}
              </Button>
            ) : null}
          </div>
          <p className='text-muted-foreground text-xs'>
            {t('The server hashes the value; you never need to compute it yourself.')}
          </p>
        </div>
      ) : null}

      {!rangeValid ? (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('A single list query covers at most {{days}} days.', {
              days: CONTENT_BACKUP_MAX_RANGE_DAYS,
            })}
          </AlertDescription>
        </Alert>
      ) : null}

      {activeQuery.isError ? (
        <Alert variant='destructive'>
          <AlertDescription className='flex flex-wrap items-center gap-2'>
            <span>
              {describeApiError(activeQuery.error) ??
                t('Content backup request failed')}
            </span>
            {/* 出错时保留最后一次成功的数据与时间，不清成空表也不写成 0 */}
            {activeQuery.dataUpdatedAt ? (
              <span className='tabular-nums'>
                {t('Last successful data at {{time}}', {
                  time: formatTimestamp(
                    new Date(activeQuery.dataUpdatedAt).toISOString()
                  ),
                })}
              </span>
            ) : null}
            <Button variant='outline' size='sm' className='h-10' onClick={refresh}>
              {t('Refresh')}
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}

      {props.drawerOpen ? (
        <div className='text-muted-foreground flex flex-wrap items-center gap-2 text-xs'>
          <span>{t('Auto refresh paused while the detail panel is open.')}</span>
          <Button
            variant='outline'
            size='sm'
            className='h-10'
            disabled={activeQuery.isFetching}
            onClick={refresh}
          >
            <RefreshCw className='size-4' aria-hidden='true' />
            {t('Refresh')}
          </Button>
        </div>
      ) : null}

      <div className={cn(surfaceClass, 'overflow-x-auto')}>
        <Table className='min-w-[1100px] table-fixed'>
          <TableHeader>
            <TableRow>
              <TableHead className='w-[170px]'>{t('Time')}</TableHead>
              <TableHead className='w-[80px]'>{t('User')}</TableHead>
              <TableHead className='w-[160px]'>{t('Channel')}</TableHead>
              <TableHead className='w-[220px]'>{t('Request ID')}</TableHead>
              <TableHead className='w-[130px]'>{t('Session source')}</TableHead>
              <TableHead className='w-[210px]'>{t('Status')}</TableHead>
              <TableHead className='w-[110px]'>{t('Integrity')}</TableHead>
              <TableHead className='w-[100px] text-right'>{t('Size')}</TableHead>
              <TableHead className='w-[80px] text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {activeQuery.isLoading ? (
              <TableRow className='h-12'>
                <TableCell colSpan={9} className='text-center'>
                  {t('Loading...')}
                </TableCell>
              </TableRow>
            ) : items.length === 0 ? (
              <TableEmpty
                colSpan={9}
                title={emptyKey ? t(emptyKey) : t('No Data')}
                description={emptyStateDescription(
                  emptyCtx,
                  sessionSearch
                    ? t('No archive record matches this session search.')
                    : t('No records found. Try adjusting your filters.')
                )}
              />
            ) : (
              items.map((job) => {
                const integrity = integrityLabel(job)
                return (
                <TableRow key={job.job_id} className='h-12 align-middle'>
                  <TableCell className='w-[170px] text-xs tabular-nums'>
                    {formatTimestamp(job.created_at)}
                  </TableCell>
                  <TableCell className='w-[80px] text-xs tabular-nums'>
                    {job.user_id ?? '-'}
                  </TableCell>
                  <TableCell className='w-[160px]'>
                    <div className='max-w-[150px] truncate text-xs'>
                      {job.channel_name || '-'}
                      {job.channel_id ? ` (#${job.channel_id})` : ''}
                    </div>
                  </TableCell>
                  <TableCell className='w-[220px]'>
                    <div className='flex items-center gap-1'>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span className='max-w-[140px] truncate font-mono text-xs'>
                            {job.request_id || '-'}
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>
                          <p className='font-mono'>{job.request_id || '-'}</p>
                        </TooltipContent>
                      </Tooltip>
                      <CopyValueButton
                        value={job.request_id}
                        label={t('Copy request ID')}
                      />
                    </div>
                  </TableCell>
                  <TableCell className='w-[130px]'>
                    <div className='max-w-[120px] truncate text-xs'>
                      {job.session_source || t('No session')}
                    </div>
                  </TableCell>
                  <TableCell className='w-[210px]'>
                    <div className='flex flex-wrap items-center gap-1'>
                      <Badge variant={statusTone(job.status)}>
                        {t(statusLabel(job.status))}
                      </Badge>
                      {/* 上传成功与本地空间释放分开显示，不能合并成一个「已完成」 */}
                      <Badge variant={cleanupTone(job.status, job.cleanup_state)}>
                        {t(cleanupLabel(job.status, job.cleanup_state))}
                      </Badge>
                    </div>
                  </TableCell>
                  <TableCell className='w-[110px]'>
                    <Badge variant={integrityTone(integrity)}>
                      {t(integrity)}
                    </Badge>
                  </TableCell>
                  <TableCell className='w-[100px] text-right text-xs tabular-nums'>
                    {formatBytes(job.compressed_bytes)}
                  </TableCell>
                  <TableCell className='w-[80px] text-right'>
                    <Button
                      variant='ghost'
                      size='icon-lg'
                      aria-label={t('View detail')}
                      onClick={() => props.onOpenJob(job)}
                    >
                      <Eye className='size-4' aria-hidden='true' />
                    </Button>
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
          {activeQuery.isFetching ? ` · ${t('Refreshing...')}` : ''}
        </span>
        <div className='flex flex-wrap items-center gap-2'>
          <Button
            variant='outline'
            className='h-10'
            disabled={cursors.length <= 1 || activeQuery.isFetching}
            onClick={() => setCursors((stack) => stack.slice(0, -1))}
          >
            <ChevronLeft className='size-4' aria-hidden='true' />
            {t('Prev')}
          </Button>
          <Button
            variant='outline'
            className='h-10'
            disabled={!hasMore || !nextCursor || activeQuery.isFetching}
            onClick={() => setCursors((stack) => [...stack, nextCursor])}
          >
            {t('Next')}
            <ChevronRight className='size-4' aria-hidden='true' />
          </Button>
        </div>
      </div>

      {props.isRoot ? (
        <p className='text-muted-foreground text-xs'>
          {t('Preview and download are available in the detail panel (root only).')}
        </p>
      ) : null}
    </div>
  )
}

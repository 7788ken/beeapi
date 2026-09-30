import { AlertCircle, Archive, ListTodo } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { surfaceClass } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
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
import { describeApiError, useContentBackupNodes } from '../api'
import {
  backlogSeconds,
  emptyStateDescription,
  emptyStateLabel,
  formatBytes,
  formatRate,
  formatTimestamp,
  hasConfigDrift,
  humanizeSeconds,
  isInodePressure,
  isSpoolCritical,
  isSpoolWarning,
  nodeHeartbeat,
  spoolPercent,
  statusLabel,
  usagePercent,
} from '../status'
import {
  CONTENT_BACKUP_NODE_OFFLINE_SECONDS,
  type ContentBackupListFilter,
  type ContentBackupNodeSnapshot,
  type ContentBackupTab,
} from '../types'

export interface NodesViewProps {
  canView: boolean
  onShowJobs: (view: ContentBackupTab, filter: ContentBackupListFilter) => void
}

function NodeCell(props: {
  node: ContentBackupNodeSnapshot
  onShowJobs: (view: ContentBackupTab, filter: ContentBackupListFilter) => void
}) {
  const { t } = useTranslation()
  const heartbeat = nodeHeartbeat(props.node.last_seen_at)
  const offline = heartbeat.state === 'offline'
  const offlineDuration =
    heartbeat.secondsSince === undefined
      ? undefined
      : humanizeSeconds(heartbeat.secondsSince)
  const drift = hasConfigDrift(props.node)
  const percent = spoolPercent(props.node)
  const diskPercent = usagePercent(
    typeof props.node.disk_total_bytes === 'number' &&
      typeof props.node.free_bytes === 'number'
      ? props.node.disk_total_bytes - props.node.free_bytes
      : undefined,
    props.node.disk_total_bytes
  )
  const backlog = backlogSeconds(props.node.oldest_pending_at)
  const backlogDuration = backlog === undefined ? undefined : humanizeSeconds(backlog)
  const nodeId = props.node.storage_node_id ?? '-'

  return (
    <TableRow
      className={cn(
        'h-12 align-middle',
        offline ? 'bg-destructive/5' : undefined,
        drift ? 'bg-amber-500/5' : undefined
      )}
    >
      <TableCell className='w-[180px]'>
        <div className='max-w-[170px] truncate font-mono text-xs'>{nodeId}</div>
        <Tooltip>
          <TooltipTrigger asChild>
            <div className='text-muted-foreground max-w-[170px] truncate font-mono text-[10px]'>
              {props.node.process_id || '-'}
            </div>
          </TooltipTrigger>
          <TooltipContent>
            <p className='font-mono'>{props.node.process_id || '-'}</p>
          </TooltipContent>
        </Tooltip>
      </TableCell>

      <TableCell className='w-[200px]'>
        <div className='text-xs tabular-nums'>
          {formatTimestamp(props.node.last_seen_at)}
        </div>
        <div className='flex flex-wrap items-center gap-1'>
          {heartbeat.state === 'offline' ? (
            <Badge variant='destructive'>
              {offlineDuration
                ? t('Offline for {{value}} {{unit}}', {
                    value: offlineDuration.value,
                    unit: t(offlineDuration.unitKey),
                  })
                : t('Offline')}
            </Badge>
          ) : null}
          {heartbeat.state === 'online' ? (
            <Badge variant='secondary'>{t('Online')}</Badge>
          ) : null}
          {heartbeat.state === 'unknown' ? (
            <Badge variant='outline'>{t('No heartbeat reported')}</Badge>
          ) : null}
        </div>
      </TableCell>

      <TableCell className='w-[150px]'>
        <div className='text-xs tabular-nums'>
          v{props.node.config_version ?? '-'} → v
          {props.node.applied_config_version ?? '-'}
        </div>
        {drift ? (
          <Badge variant='destructive'>{t('Config not applied')}</Badge>
        ) : null}
      </TableCell>

      <TableCell className='w-[180px]'>
        <div className='text-xs tabular-nums'>
          {formatBytes(props.node.spool_bytes)} /{' '}
          {formatBytes(props.node.spool_limit_bytes)}
        </div>
        {percent === undefined ? null : (
          <div className='flex items-center gap-2'>
            <Progress
              value={percent}
              className={cn(
                'h-1.5 w-20',
                isSpoolCritical(props.node) ? '[&>div]:bg-destructive' : undefined
              )}
              aria-label={t('Spool usage')}
            />
            <span className='text-xs tabular-nums'>{percent}%</span>
          </div>
        )}
        {isSpoolCritical(props.node) ? (
          <Badge variant='destructive'>{t('New handoffs stopped')}</Badge>
        ) : isSpoolWarning(props.node) ? (
          <Badge variant='outline'>{t('Spool above alert threshold')}</Badge>
        ) : null}
      </TableCell>

      <TableCell className='w-[160px]'>
        <div className='text-xs tabular-nums'>
          {formatBytes(props.node.free_bytes)} /{' '}
          {formatBytes(props.node.disk_total_bytes)}
        </div>
        <div className='text-muted-foreground text-[10px] tabular-nums'>
          {diskPercent === undefined
            ? '-'
            : t('{{percent}}% used', { percent: diskPercent })}
        </div>
      </TableCell>

      <TableCell className='w-[140px]'>
        <div className='text-xs tabular-nums'>
          {props.node.free_inodes ?? '-'} / {props.node.inode_total ?? '-'}
        </div>
        {isInodePressure(props.node) ? (
          <Badge variant='destructive'>{t('Inode pressure')}</Badge>
        ) : null}
      </TableCell>

      <TableCell className='w-[200px]'>
        <div className='flex flex-wrap items-center gap-1 text-xs tabular-nums'>
          <Button
            variant='link'
            className='h-10 px-1 tabular-nums'
            onClick={() =>
              props.onShowJobs('queue', { storageNodeId: nodeId })
            }
          >
            {t(statusLabel('pending'))}: {props.node.pending_count ?? '-'}
          </Button>
          <span className='text-muted-foreground'>
            {t(statusLabel('processing'))}: {props.node.processing_count ?? '-'}
          </span>
          <Button
            variant='link'
            className={cn(
              'h-10 px-1 tabular-nums',
              (props.node.failed_count ?? 0) > 0
                ? 'text-destructive'
                : undefined
            )}
            onClick={() =>
              props.onShowJobs('queue', {
                storageNodeId: nodeId,
                status: 'failed',
              })
            }
          >
            {t(statusLabel('failed'))}: {props.node.failed_count ?? '-'}
          </Button>
        </div>
      </TableCell>

      <TableCell className='w-[130px]'>
        {backlogDuration ? (
          <Button
            variant='link'
            className='h-10 px-0 text-xs tabular-nums'
            onClick={() =>
              props.onShowJobs('queue', { storageNodeId: nodeId })
            }
          >
            {t('{{value}} {{unit}}', {
              value: backlogDuration.value,
              unit: t(backlogDuration.unitKey),
            })}
          </Button>
        ) : (
          <span className='text-muted-foreground text-xs'>-</span>
        )}
      </TableCell>

      <TableCell className='w-[170px]'>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant='link'
              className='h-10 px-0 text-xs tabular-nums'
              onClick={() =>
                props.onShowJobs('archives', {
                  storageNodeId: nodeId,
                  cleanupState: 'pending',
                })
              }
            >
              {props.node.cleanup_pending_count ?? '-'} ·{' '}
              {formatBytes(props.node.cleanup_pending_bytes)}
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            {/* 待清理=已上传但仍占本地空间，不等于已释放 */}
            <p>{t('Uploaded but still occupying local disk space.')}</p>
          </TooltipContent>
        </Tooltip>
      </TableCell>

      <TableCell className='w-[130px]'>
        <div className='text-xs tabular-nums'>
          {t('Orphans')}: {props.node.orphan_count ?? '-'}
        </div>
        <div className='text-xs tabular-nums'>
          {t('Incomplete spool')}: {props.node.incomplete_spool_count ?? '-'}
        </div>
      </TableCell>

      <TableCell className='w-[130px]'>
        <div
          className={cn(
            'text-xs tabular-nums',
            (props.node.handoff_rejected_count ?? 0) > 0
              ? 'text-destructive'
              : undefined
          )}
        >
          {t('Rejected')}: {props.node.handoff_rejected_count ?? '-'}
        </div>
        <div className='text-xs tabular-nums'>
          {t('Unknown result')}: {props.node.handoff_unknown_count ?? '-'}
        </div>
      </TableCell>

      <TableCell className='w-[110px] text-xs tabular-nums'>
        {formatRate(props.node.upload_bytes_per_second)}
      </TableCell>

      <TableCell className='w-[130px]'>
        <div className='flex justify-end gap-1'>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant='ghost'
                size='icon-lg'
                aria-label={t('View archives for this node')}
                onClick={() =>
                  props.onShowJobs('archives', { storageNodeId: nodeId })
                }
              >
                <Archive className='size-4' aria-hidden='true' />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              <p>{t('View archives for this node')}</p>
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant='ghost'
                size='icon-lg'
                aria-label={t('View queue for this node')}
                onClick={() =>
                  props.onShowJobs('queue', { storageNodeId: nodeId })
                }
              >
                <ListTodo className='size-4' aria-hidden='true' />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              <p>{t('View queue for this node')}</p>
            </TooltipContent>
          </Tooltip>
        </div>
      </TableCell>
    </TableRow>
  )
}

export function NodesView(props: NodesViewProps) {
  const { t } = useTranslation()
  const nodesQuery = useContentBackupNodes({ enabled: props.canView })
  const nodes = nodesQuery.data ?? []
  const anyOffline = nodes.some(
    (node) => nodeHeartbeat(node.last_seen_at).state === 'offline'
  )
  const emptyCtx = {
    canView: props.canView,
    isError: nodesQuery.isError,
    itemCount: nodes.length,
    nodeOffline: anyOffline,
  }
  const emptyKey = emptyStateLabel(emptyCtx)

  return (
    <div className='space-y-3'>
      <p className='text-muted-foreground text-xs'>
        {t('A node is offline after {{seconds}} seconds without a heartbeat.', {
          seconds: CONTENT_BACKUP_NODE_OFFLINE_SECONDS,
        })}
      </p>

      {nodesQuery.isError ? (
        <Alert variant='destructive'>
          <AlertDescription className='flex flex-wrap items-center gap-2'>
            <AlertCircle className='size-4' aria-hidden='true' />
            <span>
              {describeApiError(nodesQuery.error) ??
                t('Content backup request failed')}
            </span>
            {nodesQuery.dataUpdatedAt ? (
              <span className='tabular-nums'>
                {t('Last successful data at {{time}}', {
                  time: formatTimestamp(
                    new Date(nodesQuery.dataUpdatedAt).toISOString()
                  ),
                })}
              </span>
            ) : null}
          </AlertDescription>
        </Alert>
      ) : null}

      <div className={cn(surfaceClass, 'overflow-x-auto')}>
        <Table className='min-w-[1700px] table-fixed'>
          <TableHeader>
            <TableRow>
              <TableHead className='w-[180px]'>{t('Node')}</TableHead>
              <TableHead className='w-[200px]'>{t('Heartbeat')}</TableHead>
              <TableHead className='w-[150px]'>{t('Config version')}</TableHead>
              <TableHead className='w-[180px]'>{t('Spool')}</TableHead>
              <TableHead className='w-[160px]'>{t('Disk free')}</TableHead>
              <TableHead className='w-[140px]'>{t('Inodes free')}</TableHead>
              <TableHead className='w-[200px]'>{t('Queue')}</TableHead>
              <TableHead className='w-[130px]'>{t('Oldest backlog')}</TableHead>
              <TableHead className='w-[170px]'>{t('Awaiting local cleanup')}</TableHead>
              <TableHead className='w-[130px]'>{t('Spool files')}</TableHead>
              <TableHead className='w-[130px]'>{t('Handoff')}</TableHead>
              <TableHead className='w-[110px]'>{t('Upload rate')}</TableHead>
              <TableHead className='w-[130px] text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {nodesQuery.isLoading ? (
              <TableRow className='h-12'>
                <TableCell colSpan={13} className='text-center'>
                  {t('Loading...')}
                </TableCell>
              </TableRow>
            ) : nodes.length === 0 ? (
              <TableEmpty
                colSpan={13}
                title={emptyKey ? t(emptyKey) : t('No Data')}
                description={emptyStateDescription(
                  emptyCtx,
                  t('No storage node has reported a heartbeat yet.')
                )}
              />
            ) : (
              nodes.map((node) => (
                <NodeCell
                  key={`${node.site_id ?? ''}-${node.storage_node_id ?? ''}`}
                  node={node}
                  onShowJobs={props.onShowJobs}
                />
              ))
            )}
          </TableBody>
        </Table>
      </div>

      {nodesQuery.dataUpdatedAt ? (
        <p className='text-muted-foreground text-xs tabular-nums'>
          {t('Last successful data at {{time}}', {
            time: formatTimestamp(new Date(nodesQuery.dataUpdatedAt).toISOString()),
          })}
        </p>
      ) : null}
    </div>
  )
}

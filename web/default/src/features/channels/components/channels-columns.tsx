/* eslint-disable react-refresh/only-export-components */
import { type ReactNode, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { type ColumnDef } from '@tanstack/react-table'
import {
  AlertTriangle,
  BarChart3,
  ChevronDown,
  ChevronRight,
  ListOrdered,
  Shuffle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getCurrencyLabel } from '@/lib/currency'
import {
  formatTimestampToDate,
  formatQuota as formatQuotaValue,
} from '@/lib/format'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn, truncateText } from '@/lib/utils'
import { useAdminPerms } from '@/hooks/use-admin'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTableColumnHeader } from '@/components/data-table/column-header'
import { GroupBadge } from '@/components/group-badge'
import {
  StatusBadge,
  dotColorMap,
  textColorMap,
} from '@/components/status-badge'
import { UptimeSparkline } from '@/components/uptime-sparkline'
import { getCodexUsage } from '../api'
import {
  CHANNEL_STATUS_CONFIG,
  MODEL_FETCHABLE_TYPES,
  RATIO_BADGE_WINDOW_DAYS,
} from '../constants'
import { useChannelUptime } from '../hooks/use-channel-uptime'
import {
  formatBalance,
  formatRatioSummary,
  parseRatioSummary,
  formatRelativeTime,
  formatResponseTime,
  getBalanceVariant,
  getChannelTypeIcon,
  getChannelTypeLabel,
  getResponseTimeConfig,
  isMultiKeyChannel,
  parseModelsList,
  parseGroupsList,
  parseChannelSettings,
  handleUpdateChannelField,
  handleUpdateTagField,
  handleUpdateChannelBalance,
  isTagAggregateRow,
  type TagRow,
} from '../lib'
import { parseUpstreamUpdateMeta } from '../lib/upstream-update-utils'
import type { Channel } from '../types'
import { useChannels } from './channels-provider'
import { DataTableRowActions } from './data-table-row-actions'
import { DataTableTagRowActions } from './data-table-tag-row-actions'
import {
  CodexUsageDialog,
  type CodexUsageDialogData,
} from './dialogs/codex-usage-dialog'
import { NumericSpinnerInput } from './numeric-spinner-input'

function parseIonetMeta(otherInfo: string | null | undefined): null | {
  source?: string
  deployment_id?: string
} {
  if (!otherInfo) return null
  try {
    const parsed = JSON.parse(otherInfo)
    if (parsed && typeof parsed === 'object') {
      return parsed
    }
  } catch {
    return null
  }
  return null
}

/**
 * 质量分原始指标快照（后端 service.buildQualityDetail 写入 channels.quality_detail）。
 * 空串/坏 JSON/零流量返回 null，tooltip 回退为通用说明文案。
 */
function parseQualityDetail(raw: string | null | undefined): null | {
  successCnt: number
  errorCnt: number
  total: number
  avgUseTimeMs: number
  avgFrtMs: number
} {
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') return null
    const successCnt = Number(parsed.success_cnt) || 0
    const errorCnt = Number(parsed.error_cnt) || 0
    const total = successCnt + errorCnt
    if (total <= 0) return null
    return {
      successCnt,
      errorCnt,
      total,
      avgUseTimeMs: Number(parsed.avg_use_time_ms) || 0,
      avgFrtMs: Number(parsed.avg_frt_ms) || 0,
    }
  } catch {
    return null
  }
}

/**
 * Render limited items with "and X more" indicator
 */
function renderLimitedItems(
  items: React.ReactNode[],
  maxDisplay: number = 2
): React.ReactNode {
  if (items.length === 0)
    return <span className='text-muted-foreground text-xs'>-</span>

  const displayed = items.slice(0, maxDisplay)
  const remaining = items.length - maxDisplay

  return (
    <div className='flex max-w-full items-center gap-1 overflow-hidden'>
      {displayed}
      {remaining > 0 && (
        <StatusBadge
          label={`+${remaining}`}
          variant='neutral'
          size='sm'
          copyable={false}
          className='flex-shrink-0'
        />
      )}
    </div>
  )
}

/**
 * Upstream update tags (+N / -N) shown on channel name for model-fetchable channels
 */
function UpstreamUpdateTags({ channel }: { channel: Channel }) {
  const { upstream, setCurrentRow } = useChannels()
  if (!MODEL_FETCHABLE_TYPES.has(channel.type)) return null

  const meta = parseUpstreamUpdateMeta(channel.settings)
  if (!meta.enabled) return null

  const addCount = meta.pendingAddModels.length
  const removeCount = meta.pendingRemoveModels.length
  if (addCount === 0 && removeCount === 0) return null

  return (
    <div className='flex items-center gap-0.5'>
      {addCount > 0 && (
        <StatusBadge
          label={`+${addCount}`}
          variant='success'
          size='sm'
          copyable={false}
          className='cursor-pointer'
          onClick={(e: React.MouseEvent) => {
            e.stopPropagation()
            setCurrentRow(channel)
            upstream.openModal(
              channel,
              meta.pendingAddModels,
              meta.pendingRemoveModels,
              'add'
            )
          }}
        />
      )}
      {removeCount > 0 && (
        <StatusBadge
          label={`-${removeCount}`}
          variant='danger'
          size='sm'
          copyable={false}
          className='cursor-pointer'
          onClick={(e: React.MouseEvent) => {
            e.stopPropagation()
            setCurrentRow(channel)
            upstream.openModal(
              channel,
              meta.pendingAddModels,
              meta.pendingRemoveModels,
              'remove'
            )
          }}
        />
      )}
    </div>
  )
}

/**
 * Priority cell component with inline editing
 */
function PriorityCell({ channel }: { channel: Channel }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // 内联改 priority 走 PUT /api/channel(/tag)，属渠道写操作；没权限就渲染成纯文本
  const canEdit = useAdminPerms().channel_edit
  const isTagRow = isTagAggregateRow(channel)
  const priority = channel.priority
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [pendingValue, setPendingValue] = useState<number | null>(null)

  if (!canEdit) {
    return <span className='tabular-nums'>{priority ?? 0}</span>
  }

  // Tag row - editable with confirmation for all tag channels
  if (isTagRow) {
    const tag = channel.tag || ''
    const channelCount = channel.children?.length || 0

    return (
      <>
        <NumericSpinnerInput
          value={priority ?? 0}
          onChange={(value) => {
            setPendingValue(value)
            setConfirmOpen(true)
          }}
          min={-999}
        />
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t('Confirm Batch Update')}
          desc={`This will update the priority to ${pendingValue} for all ${channelCount} channel(s) with tag "${tag}". Continue?`}
          confirmText='Update'
          handleConfirm={() => {
            if (pendingValue !== null) {
              handleUpdateTagField(tag, 'priority', pendingValue, queryClient)
            }
            setConfirmOpen(false)
          }}
        />
      </>
    )
  }

  // Regular channel row - editable
  return (
    <NumericSpinnerInput
      value={priority ?? 0}
      onChange={(value) => {
        handleUpdateChannelField(channel.id, 'priority', value, queryClient)
      }}
      min={-999}
    />
  )
}

/**
 * Weight cell component with inline editing
 */
function WeightCell({ channel }: { channel: Channel }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // 内联改 weight 走 PUT /api/channel(/tag)，属渠道写操作；没权限就渲染成纯文本
  const canEdit = useAdminPerms().channel_edit
  const isTagRow = isTagAggregateRow(channel)
  const weight = channel.weight
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [pendingValue, setPendingValue] = useState<number | null>(null)

  if (!canEdit) {
    return <span className='tabular-nums'>{weight ?? 0}</span>
  }

  // Tag row - editable with confirmation for all tag channels
  if (isTagRow) {
    const tag = channel.tag || ''
    const channelCount = channel.children?.length || 0

    return (
      <>
        <NumericSpinnerInput
          value={weight ?? 0}
          onChange={(value) => {
            setPendingValue(value)
            setConfirmOpen(true)
          }}
          min={0}
        />
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t('Confirm Batch Update')}
          desc={`This will update the weight to ${pendingValue} for all ${channelCount} channel(s) with tag "${tag}". Continue?`}
          confirmText='Update'
          handleConfirm={() => {
            if (pendingValue !== null) {
              handleUpdateTagField(tag, 'weight', pendingValue, queryClient)
            }
            setConfirmOpen(false)
          }}
        />
      </>
    )
  }

  // Regular channel row - editable
  return (
    <NumericSpinnerInput
      value={weight ?? 0}
      onChange={(value) => {
        handleUpdateChannelField(channel.id, 'weight', value, queryClient)
      }}
      min={0}
    />
  )
}

/**
 * Routing mode cell（docs/2026-05-26-channel-routing-mode-switchable.md）
 * 显示 inherit / probabilistic / capacity，只读，编辑走渠道详情抽屉
 */
function RoutingModeCell({ channel }: { channel: Channel }) {
  const { t } = useTranslation()
  if (isTagAggregateRow(channel)) {
    return <span className='text-muted-foreground text-xs'>-</span>
  }
  const mode = channel.routing_mode ?? 0
  if (mode === 1) {
    return (
      <StatusBadge
        label={t('Probabilistic')}
        variant='grey'
        size='sm'
        copyable={false}
      />
    )
  }
  if (mode === 2) {
    const limit = channel.capacity_limit ?? channel.weight ?? 0
    const win = channel.capacity_window_sec
    const label = win
      ? `${t('Capacity')} (${limit}/${win}s)`
      : `${t('Capacity')} (${limit})`
    return (
      <StatusBadge label={label} variant='amber' size='sm' copyable={false} />
    )
  }
  return <span className='text-muted-foreground text-xs'>{t('Inherit')}</span>
}

/**
 * Balance cell component with click to update
 */
function BalanceCell({ channel }: { channel: Channel }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isTagRow = isTagAggregateRow(channel)
  const balance = channel.balance || 0
  const usedQuota = channel.used_quota || 0
  const [isUpdating, setIsUpdating] = useState(false)
  const [codexUsageOpen, setCodexUsageOpen] = useState(false)
  const [codexUsageResponse, setCodexUsageResponse] =
    useState<CodexUsageDialogData | null>(null)
  const currencyLabel = getCurrencyLabel()
  const tokenSuffix = currencyLabel === 'Tokens' ? ' Tokens' : ''
  const withSuffix = (value: string) =>
    tokenSuffix && value !== '-' ? `${value}${tokenSuffix}` : value

  const usedDisplay = withSuffix(formatQuotaValue(usedQuota))
  const remainingDisplay = withSuffix(formatBalance(balance))

  // Tag row: only show cumulative used quota
  if (isTagRow) {
    return (
      <StatusBadge
        label={`Used: ${usedDisplay}`}
        variant='neutral'
        size='sm'
        copyable={false}
      />
    )
  }

  // Regular channel row: show used and remaining with click to update
  const variant = getBalanceVariant(balance)

  const handleClickUpdate = async () => {
    if (isUpdating) return

    setIsUpdating(true)
    if (channel.type === 57) {
      try {
        const res = await getCodexUsage(channel.id)
        if (!res.success) {
          throw new Error(res.message || t('Failed to fetch usage'))
        }
        setCodexUsageResponse(res)
        setCodexUsageOpen(true)
      } catch (error) {
        toast.error(
          error instanceof Error ? error.message : t('Failed to fetch usage')
        )
      } finally {
        setIsUpdating(false)
      }
      return
    }

    await handleUpdateChannelBalance(channel.id, queryClient)
    setIsUpdating(false)
  }

  return (
    <TooltipProvider>
      <div className='flex items-center gap-1.5 text-xs font-medium'>
        <span
          className={cn(
            'size-1.5 shrink-0 rounded-full',
            dotColorMap[isUpdating ? 'neutral' : variant]
          )}
          aria-hidden='true'
        />
        <Tooltip>
          <TooltipTrigger asChild>
            <span className='text-muted-foreground cursor-help'>
              {usedDisplay}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <p>
              {t('Used:')} {usedDisplay}
            </p>
          </TooltipContent>
        </Tooltip>
        <span className='text-muted-foreground/30'>·</span>
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              className={cn(
                'cursor-pointer transition-opacity hover:opacity-70',
                channel.type === 57
                  ? 'text-primary'
                  : textColorMap[isUpdating ? 'neutral' : variant]
              )}
              onClick={handleClickUpdate}
            >
              {isUpdating
                ? 'Updating...'
                : channel.type === 57
                  ? t('Account Info')
                  : remainingDisplay}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <p>
              {channel.type === 57
                ? t('Click to view Codex usage')
                : `${t('Remaining:')} ${remainingDisplay}`}
            </p>
            {channel.type !== 57 && <p>{t('Click to update balance')}</p>}
          </TooltipContent>
        </Tooltip>
      </div>

      <CodexUsageDialog
        open={codexUsageOpen}
        onOpenChange={setCodexUsageOpen}
        channelName={channel.name}
        channelId={channel.id}
        response={codexUsageResponse}
        onRefresh={async () => {
          if (isUpdating) return
          setIsUpdating(true)
          try {
            const res = await getCodexUsage(channel.id)
            if (!res.success) {
              throw new Error(res.message || t('Failed to fetch usage'))
            }
            setCodexUsageResponse(res)
          } catch (error) {
            toast.error(
              error instanceof Error
                ? error.message
                : t('Failed to fetch usage')
            )
          } finally {
            setIsUpdating(false)
          }
        }}
        isRefreshing={isUpdating}
      />
    </TooltipProvider>
  )
}

/**
 * Generate channels columns configuration
 */
export function useChannelsColumns(): ColumnDef<Channel>[] {
  const { t } = useTranslation()
  const { setOpen, setCurrentRow } = useChannels()
  // 可用性条形图：整表只拉一次 /api/channel/uptime，行内按 channel id 取序列
  const uptimeByChannel = useChannelUptime(24)
  return [
    // Checkbox column
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={
            table.getIsAllPageRowsSelected() ||
            (table.getIsSomePageRowsSelected() && 'indeterminate')
          }
          onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
          aria-label='Select all'
        />
      ),
      cell: ({ row }) => {
        const isTagRow = isTagAggregateRow(row.original)

        // Don't show checkbox for tag rows
        if (isTagRow) {
          return null
        }

        return (
          <Checkbox
            checked={row.getIsSelected()}
            onCheckedChange={(value) => row.toggleSelected(!!value)}
            aria-label='Select row'
          />
        )
      },
      enableSorting: false,
      enableHiding: false,
      size: 40,
    },

    // ID column
    {
      accessorKey: 'id',
      meta: { label: t('ID'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title='ID' />
      ),
      cell: ({ row }) => {
        const id = row.getValue('id') as number
        return (
          <StatusBadge
            label={String(id)}
            variant='neutral'
            copyText={String(id)}
            size='sm'
            className='font-mono'
          />
        )
      },
      size: 80,
    },

    // Quality Score column (0-100; docs/2026-05-12-channel-quality-rpm-list-plan.md)
    {
      accessorKey: 'quality_score',
      meta: { label: t('Quality'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Quality')} />
      ),
      cell: ({ row }) => {
        const score = row.original.quality_score
        const updatedAt = row.original.quality_updated_at ?? 0
        const detail = parseQualityDetail(row.original.quality_detail)
        if (score === null || score === undefined) {
          return <span className='text-muted-foreground text-xs'>N/A</span>
        }
        const textTone =
          score >= 80
            ? 'text-emerald-600'
            : score >= 60
              ? 'text-amber-600'
              : 'text-rose-600'
        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <span
                  className={cn('text-xs font-medium tabular-nums', textTone)}
                >
                  {score}
                </span>
              </TooltipTrigger>
              <TooltipContent side='top'>
                {detail ? (
                  <div className='grid grid-cols-[auto_auto] gap-x-5 gap-y-1 text-xs'>
                    <span className='text-background/70'>
                      {t('Success rate (24h)')}
                    </span>
                    <span className='text-right tabular-nums'>
                      {((detail.successCnt / detail.total) * 100).toFixed(1)}% (
                      {detail.successCnt}/{detail.total})
                    </span>
                    <span className='text-background/70'>
                      {t('Avg response time')}
                    </span>
                    <span className='text-right tabular-nums'>
                      {(detail.avgUseTimeMs / 1000).toFixed(1)} s
                    </span>
                    <span className='text-background/70'>
                      {t('Avg first-token latency')}
                    </span>
                    <span className='text-right tabular-nums'>
                      {detail.avgFrtMs > 0 ? `${detail.avgFrtMs} ms` : '—'}
                    </span>
                    <span className='text-background/70'>
                      {t('Error rate (24h)')}
                    </span>
                    <span className='text-right tabular-nums'>
                      {((detail.errorCnt / detail.total) * 100).toFixed(1)}% (
                      {detail.errorCnt})
                    </span>
                  </div>
                ) : (
                  t(
                    'Composite score 0-100 based on success rate, TTFT, response time, and error patterns over the past 24h.'
                  )
                )}
                {updatedAt > 0 && (
                  <div className='text-background/70 mt-1 text-xs'>
                    {t('Updated')} {formatRelativeTime(updatedAt)}
                  </div>
                )}
                <button
                  type='button'
                  className='bg-background/15 text-background hover:bg-background/30 border-background/25 mt-1.5 inline-flex w-full items-center justify-center gap-1.5 rounded border px-2 py-1 text-xs font-semibold'
                  onClick={(e) => {
                    e.stopPropagation()
                    setCurrentRow(row.original)
                    setOpen('channel-quality')
                  }}
                >
                  <BarChart3 className='size-3.5' />
                  {t('View more')}
                </button>
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        )
      },
      size: 70,
    },

    // Availability column (近 24h 每小时成功率条形图；数据整表拉一次后按行分发)
    {
      id: 'availability',
      meta: { label: t('Availability'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Availability')} />
      ),
      cell: ({ row }) => {
        if (isTagAggregateRow(row.original)) {
          return null
        }
        return (
          <UptimeSparkline
            size='sm'
            series={uptimeByChannel.get(row.original.id) ?? []}
            emptyLabel={t('No data')}
          />
        )
      },
      enableSorting: false,
      size: 160,
    },

    // Verify Score column (外部测评分数，来自外部测评网关 /api/verify/claude)
    {
      accessorKey: 'verify_score',
      meta: { label: t('Verify Score'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Verify Score')} />
      ),
      cell: ({ row }) => {
        const score = row.original.verify_score
        const grade = row.original.verify_grade || ''
        const testedAt = row.original.verify_tested_at || 0
        const channelType = row.original.type
        const prevScore = row.original.verify_prev_score
        // 趋势着色：本次比上次低（任意下降）即标鲜艳红 + ↓
        const dropped = prevScore != null && score != null && score < prevScore
        // 可测评类型：Anthropic (14) + OpenAI (1)
        const supported = channelType === 1 || channelType === 14
        if (!supported) {
          return <span className='text-muted-foreground text-xs'>—</span>
        }
        const tone =
          grade === 'A+' || grade === 'A'
            ? 'bg-emerald-500 text-white hover:bg-emerald-600'
            : grade === 'B'
              ? 'bg-blue-500 text-white hover:bg-blue-600'
              : grade === 'C'
                ? 'bg-amber-500 text-white hover:bg-amber-600'
                : grade === 'D'
                  ? 'bg-orange-500 text-white hover:bg-orange-600'
                  : grade === 'F'
                    ? 'bg-rose-600 text-white hover:bg-rose-700'
                    : ''

        if (score === null || score === undefined) {
          return (
            <button
              type='button'
              onClick={(e) => {
                e.stopPropagation()
                setCurrentRow(row.original)
                setOpen('channel-verify')
              }}
              className='text-muted-foreground hover:bg-muted hover:text-foreground inline-flex items-center rounded border border-dashed px-2 py-0.5 text-xs transition'
            >
              {t('Pending test')}
            </button>
          )
        }
        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  type='button'
                  onClick={(e) => {
                    e.stopPropagation()
                    setCurrentRow(row.original)
                    setOpen('channel-verify')
                  }}
                  className={cn(
                    'inline-flex cursor-pointer items-center gap-1 rounded px-2 py-0.5 text-xs font-medium transition',
                    dropped
                      ? 'bg-rose-600 font-bold text-white ring-2 ring-rose-300 hover:bg-rose-700'
                      : tone || 'bg-muted'
                  )}
                >
                  {dropped && <span aria-hidden='true'>↓</span>}
                  <span className='tabular-nums'>{score}</span>
                  {grade && <span className='font-bold'>{grade}</span>}
                </button>
              </TooltipTrigger>
              <TooltipContent side='top'>
                {dropped && (
                  <div className='mb-1 text-xs font-medium text-rose-400'>
                    {t('Score dropped vs last test')}: {prevScore} → {score} (-
                    {(prevScore ?? 0) - (score ?? 0)})
                  </div>
                )}
                {t('External health verification via the external verification gateway.')}
                {testedAt > 0 && (
                  <div className='text-muted-foreground mt-1 text-xs'>
                    {t('Last tested')} {formatRelativeTime(testedAt)}
                  </div>
                )}
                <div className='text-muted-foreground mt-1 text-xs'>
                  {t('Click to view report or retest.')}
                </div>
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        )
      },
      size: 100,
    },

    // 上游分组倍率变化角标（docs/2026-08-05-upstream-group-ratio-monitor.md）
    // 只显示最近一批次且限窗口内，超窗不显示，避免红点永久挂着。
    {
      id: 'ratio_change',
      meta: { label: t('Upstream ratio'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Upstream ratio')} />
      ),
      cell: ({ row }) => {
        if (isTagAggregateRow(row.original)) {
          return null
        }
        const status = row.original.ratio_fetch_status || ''
        const msg = row.original.ratio_fetch_msg || ''
        const fetchedAt = row.original.ratio_fetched_at ?? 0
        const changedAt = row.original.ratio_changed_at ?? 0
        const up = row.original.ratio_up_count ?? 0
        const down = row.original.ratio_down_count ?? 0

        const withTooltip = (trigger: ReactNode, content: ReactNode) => (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>{trigger}</TooltipTrigger>
              <TooltipContent side='top'>{content}</TooltipContent>
            </Tooltip>
          </TooltipProvider>
        )

        // 实付反推值（0.01 精度）：分组固定倍率拿不到时的兜底
        const effectiveRaw = row.original.ratio_effective
        const hasEffective =
          Number.isFinite(effectiveRaw) && (effectiveRaw ?? 0) > 0
        const effective2 = hasEffective
          ? Number((effectiveRaw as number).toFixed(2))
          : null
        const expected = row.original.ratio_expected
        const overPaying =
          hasEffective &&
          Number.isFinite(expected) &&
          (expected ?? 0) > 0 &&
          (effectiveRaw as number) > (expected as number) * 1.1

        if (status === 'unsupported') {
          // 上游不提供倍率查询，但账单端点往往仍可用（pomoai 实测如此）——有实付值就显示它
          if (hasEffective) {
            return withTooltip(
              <span
                className={
                  overPaying
                    ? 'cursor-help text-xs font-medium text-rose-600 tabular-nums'
                    : 'cursor-help text-xs tabular-nums'
                }
              >
                {effective2}
              </span>,
              <>
                {t(
                  'Measured actual ratio {{v}} (derived from upstream billing)',
                  {
                    v: effective2,
                  }
                )}
                <div className='text-background/70 mt-1 text-xs'>
                  {t('This upstream does not support ratio query')}
                </div>
              </>
            )
          }
          return withTooltip(
            <span className='text-muted-foreground cursor-help text-xs'>
              —
            </span>,
            <>
              {t('This upstream does not support ratio query')}
              {msg && (
                <div className='text-background/70 mt-1 text-xs break-all'>
                  {msg}
                </div>
              )}
            </>
          )
        }

        if (status === 'error') {
          return withTooltip(
            <span className='inline-flex cursor-help items-center gap-1 text-xs text-amber-600'>
              <AlertTriangle className='size-3.5' />
            </span>,
            <>
              {t('Failed to fetch upstream ratio')}
              {msg && (
                <div className='text-background/70 mt-1 text-xs break-all'>
                  {msg}
                </div>
              )}
              {fetchedAt > 0 && (
                <div className='text-background/70 mt-1 text-xs'>
                  {t('Last fetched')} {formatRelativeTime(fetchedAt)}
                </div>
              )}
            </>
          )
        }

        // 从未抓取：与"抓到了且没涨"必须可区分，不能显示成正常。
        if (fetchedAt <= 0) {
          return withTooltip(
            <span className='text-muted-foreground cursor-help text-xs'>
              {t('Not fetched')}
            </span>,
            t('Upstream group ratio has never been fetched for this channel.')
          )
        }

        const windowStart =
          Math.floor(Date.now() / 1000) - RATIO_BADGE_WINDOW_DAYS * 86400
        const inWindow = changedAt > 0 && changedAt >= windowStart
        const hasBadge = inWindow && (up > 0 || down > 0)

        // 主体是"此刻的上游倍率"；角标只是次要提示
        const summary = parseRatioSummary(row.original.ratio_detail)
        const openDetail = (e: React.MouseEvent) => {
          e.stopPropagation()
          setCurrentRow(row.original)
          setOpen('channel-ratio-changes')
        }

        if (!summary) {
          return withTooltip(
            <span className='text-muted-foreground cursor-help text-xs'>
              {t('Not fetched')}
            </span>,
            t('Upstream group ratio has never been fetched for this channel.')
          )
        }

        // 展示优先级：① key 所属分组的固定倍率（人工指定或反推定位）
        // ② 实付反推兜底（0.01 精度）③ 全表区间弱化显示
        const displayValue = summary.g
          ? formatRatioSummary(summary)
          : hasEffective
            ? String(effective2)
            : formatRatioSummary(summary)

        return withTooltip(
          <button
            type='button'
            onClick={openDetail}
            className='inline-flex cursor-pointer items-center gap-1.5 text-xs transition'
          >
            <span
              className={
                overPaying
                  ? 'font-medium text-rose-600 tabular-nums'
                  : summary.g || hasEffective
                    ? 'font-medium tabular-nums'
                    : 'text-muted-foreground tabular-nums'
              }
            >
              {displayValue}
            </span>
            {hasBadge && up > 0 && (
              <span className='text-rose-600 tabular-nums'>↑{up}</span>
            )}
            {hasBadge && down > 0 && (
              <span className='text-emerald-600 tabular-nums'>↓{down}</span>
            )}
          </button>,
          <>
            {summary.g
              ? t('Group {{g}} on upstream, ratio {{v}}', {
                  g: summary.g,
                  v: summary.min,
                })
              : hasEffective
                ? t(
                    'Measured actual ratio {{v}} (derived from upstream billing)',
                    {
                      v: effective2,
                    }
                  )
                : summary.n > 1
                  ? t(
                      'Upstream group for this key is undetermined; showing the range across {{n}} groups ({{min}}~{{max}}). Set it on the channel to pin the exact ratio.',
                      { n: summary.n, min: summary.min, max: summary.max }
                    )
                  : t('Current upstream ratio {{v}}', { v: summary.min })}
            {hasEffective && summary.g && (
              <div className='text-background/70 mt-1 text-xs'>
                {t('Measured actual ratio ≈ {{v}}', { v: effective2 })}
              </div>
            )}
            {Number.isFinite(expected) && (expected ?? 0) > 0 && (
              <div
                className={
                  overPaying
                    ? 'mt-1 text-xs font-medium'
                    : 'text-background/70 mt-1 text-xs'
                }
              >
                {t('Registered purchase ratio')} {expected}
                {overPaying ? ` — ${t('upstream charges more than this')}` : ''}
              </div>
            )}
            {!summary.g && msg && (
              <div className='text-background/70 mt-1 text-xs break-all'>
                {msg}
              </div>
            )}
            {hasBadge && (
              <div className='text-background/70 mt-1 text-xs'>
                {t('Changed at')} {formatRelativeTime(changedAt)}
              </div>
            )}
            <div className='text-background/70 mt-1 text-xs'>
              {t('Last fetched')} {formatRelativeTime(fetchedAt)}
            </div>
            <div className='text-background/70 mt-1 text-xs'>
              {t('Click to view group-level detail.')}
            </div>
          </>
        )
      },
      enableSorting: false,
      size: 90,
    },

    // RPM column (realtime, last ~60s sliding window)
    {
      accessorKey: 'rpm_24h',
      meta: { label: t('RPM'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('RPM')} />
      ),
      cell: ({ row }) => {
        const rpm = row.original.rpm_24h ?? 0
        if (rpm <= 0) {
          return <span className='text-muted-foreground text-xs'>0</span>
        }
        const display = rpm >= 10 ? rpm.toFixed(0) : rpm.toFixed(1)
        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <span className='cursor-help font-mono text-xs tabular-nums'>
                  {display}
                </span>
              </TooltipTrigger>
              <TooltipContent side='top'>
                {t('Realtime requests per minute (sliding 60s window).')}
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        )
      },
      size: 80,
    },

    // Name column
    {
      accessorKey: 'name',
      meta: { label: t('Name'), mobileTitle: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Name')} />
      ),
      cell: ({ row }) => {
        const isTagRow = isTagAggregateRow(row.original)
        const name = row.getValue('name') as string
        const channel = row.original
        const isMultiKey = isMultiKeyChannel(channel)

        // Tag row with expand/collapse
        if (isTagRow) {
          const tag = (row.original as TagRow).tag || name
          const childrenCount = (row.original as TagRow).children?.length || 0

          return (
            <div className='flex items-center gap-2'>
              <Button
                variant='ghost'
                size='sm'
                className='h-6 w-6 p-0'
                onClick={row.getToggleExpandedHandler()}
              >
                {row.getIsExpanded() ? (
                  <ChevronDown className='h-4 w-4' />
                ) : (
                  <ChevronRight className='h-4 w-4' />
                )}
              </Button>
              <div className='flex items-center gap-1.5'>
                <span className='font-semibold'>Tag：{tag}</span>
                <StatusBadge
                  label={`${childrenCount} channels`}
                  variant='blue'
                  size='sm'
                  copyable={false}
                />
              </div>
            </div>
          )
        }

        // Regular channel row
        const settings = parseChannelSettings(channel.setting)
        const isPassThrough = settings.pass_through_body_enabled === true

        return (
          <div className='flex items-center gap-2'>
            <div className='flex flex-col gap-1'>
              <div className='flex items-center gap-1.5'>
                <span className='font-medium'>{truncateText(name, 30)}</span>
                {isPassThrough && (
                  <TooltipProvider delayDuration={100}>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <AlertTriangle className='h-3.5 w-3.5 flex-shrink-0 text-amber-500' />
                      </TooltipTrigger>
                      <TooltipContent side='top'>
                        {t(
                          'Request body pass-through is enabled. The request body will be sent directly to the upstream without any conversion.'
                        )}
                      </TooltipContent>
                    </Tooltip>
                  </TooltipProvider>
                )}
                {isMultiKey && (
                  <StatusBadge
                    label={`${channel.channel_info.multi_key_size} keys`}
                    variant='purple'
                    size='sm'
                    copyable={false}
                  />
                )}
                <UpstreamUpdateTags channel={channel} />
              </div>
              {channel.remark && (
                <TooltipProvider delayDuration={200}>
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span className='text-muted-foreground text-xs'>
                        {truncateText(channel.remark, 40)}
                      </span>
                    </TooltipTrigger>
                    <TooltipContent side='bottom' className='max-w-xs'>
                      {channel.remark}
                    </TooltipContent>
                  </Tooltip>
                </TooltipProvider>
              )}
            </div>
          </div>
        )
      },
      minSize: 200,
    },

    // Type column
    {
      accessorKey: 'type',
      meta: { label: t('Type') },
      header: t('Type'),
      cell: ({ row }) => {
        const isTagRow = isTagAggregateRow(row.original)

        if (isTagRow) {
          return (
            <StatusBadge
              label={t('Tag Aggregate')}
              variant='blue'
              size='sm'
              copyable={false}
            />
          )
        }

        const type = row.getValue('type') as number
        const typeNameKey = getChannelTypeLabel(type)
        const typeName = t(typeNameKey)
        const iconName = getChannelTypeIcon(type)
        const icon = getLobeIcon(`${iconName}.Color`, 20)
        const channel = row.original as Channel
        const isMultiKey = isMultiKeyChannel(channel)
        const multiKeyMode = channel.channel_info?.multi_key_mode ?? 'random'
        const MultiKeyModeIcon =
          multiKeyMode === 'random' ? Shuffle : ListOrdered
        const multiKeyTooltip =
          multiKeyMode === 'random'
            ? t('Multi-key: Random rotation')
            : t('Multi-key: Polling rotation')

        const ionetMeta = parseIonetMeta(channel.other_info)
        const isIonet = ionetMeta?.source === 'ionet'
        const deploymentId =
          typeof ionetMeta?.deployment_id === 'string'
            ? ionetMeta?.deployment_id
            : undefined

        return (
          <div className='flex items-center gap-2'>
            <div className='flex items-center gap-1.5'>
              {isMultiKey && (
                <TooltipProvider delayDuration={100}>
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span className='border-border bg-muted text-primary inline-flex h-6 w-6 items-center justify-center rounded-full border'>
                        <MultiKeyModeIcon className='h-3.5 w-3.5' />
                      </span>
                    </TooltipTrigger>
                    <TooltipContent side='top'>
                      {multiKeyTooltip}
                    </TooltipContent>
                  </Tooltip>
                </TooltipProvider>
              )}
              {icon}
            </div>
            <StatusBadge
              label={typeName}
              autoColor={typeName}
              size='sm'
              copyable={false}
            />
            {isIonet && (
              <TooltipProvider delayDuration={100}>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span
                      className='flex cursor-pointer items-center gap-1.5 text-xs font-medium'
                      onClick={(e) => {
                        e.stopPropagation()
                        if (!deploymentId) return
                        const targetUrl = `/console/deployment?deployment_id=${deploymentId}`
                        window.open(targetUrl, '_blank', 'noopener')
                      }}
                    >
                      <span className='text-muted-foreground/30'>·</span>
                      <span className={cn(textColorMap.purple)}>IO.NET</span>
                    </span>
                  </TooltipTrigger>
                  <TooltipContent side='top'>
                    <div className='max-w-xs space-y-1'>
                      <div className='text-xs'>
                        {t('From IO.NET deployment')}
                      </div>
                      {deploymentId && (
                        <div className='text-muted-foreground font-mono text-xs'>
                          {t('Deployment ID')}: {deploymentId}
                        </div>
                      )}
                      <div className='text-muted-foreground text-xs'>
                        {t('Click to open deployment')}
                      </div>
                    </div>
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
            )}
          </div>
        )
      },
      filterFn: (row, id, value) => {
        if (!value || value.length === 0 || value.includes('all')) return true
        return value.includes(String(row.getValue(id)))
      },
      size: 140,
      enableSorting: false,
    },

    // Status column
    {
      accessorKey: 'status',
      meta: { label: t('Status'), mobileBadge: true },
      header: t('Status'),
      cell: ({ row }) => {
        const isTagRow = isTagAggregateRow(row.original)
        const status = row.getValue('status') as number
        const channel = row.original as Channel

        // Tag row: show aggregated status
        if (isTagRow) {
          const childrenCount = (row.original as TagRow).children?.length || 0
          const hasEnabled = status === 1

          if (hasEnabled) {
            return (
              <StatusBadge
                label={`Active (${childrenCount})`}
                variant='success'
                showDot
                size='sm'
                copyable={false}
              />
            )
          } else {
            return (
              <StatusBadge
                label={`Inactive (${childrenCount})`}
                variant='neutral'
                size='sm'
                copyable={false}
              />
            )
          }
        }

        // Regular channel row
        const config =
          CHANNEL_STATUS_CONFIG[status as keyof typeof CHANNEL_STATUS_CONFIG] ||
          CHANNEL_STATUS_CONFIG[0]

        const isMultiKey = isMultiKeyChannel(channel)
        const keySize = channel.channel_info?.multi_key_size ?? 0
        const disabledCount = channel.channel_info?.multi_key_status_list
          ? Object.keys(channel.channel_info.multi_key_status_list).length
          : 0
        const enabledCount = Math.max(0, keySize - disabledCount)
        const label =
          isMultiKey && keySize > 0
            ? `${t(config.label)} (${enabledCount}/${keySize})`
            : t(config.label)

        // Auto-disabled: show reason and time tooltip
        if (status === 3) {
          let statusReason = ''
          let statusTime = ''
          try {
            const otherInfo = channel.other_info
              ? JSON.parse(channel.other_info)
              : null
            if (otherInfo) {
              statusReason = otherInfo.status_reason || ''
              statusTime = otherInfo.status_time
                ? formatTimestampToDate(otherInfo.status_time)
                : ''
            }
          } catch {
            /* empty */
          }

          if (statusReason || statusTime) {
            return (
              <TooltipProvider delayDuration={100}>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span>
                      <StatusBadge
                        label={label}
                        variant={config.variant}
                        showDot={config.showDot}
                        size='sm'
                        copyable={false}
                      />
                    </span>
                  </TooltipTrigger>
                  <TooltipContent side='top' className='max-w-xs'>
                    <div className='space-y-1 text-xs'>
                      {statusReason && (
                        <div>
                          {t('Reason:')} {statusReason}
                        </div>
                      )}
                      {statusTime && (
                        <div>
                          {t('Time:')} {statusTime}
                        </div>
                      )}
                    </div>
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
            )
          }
        }

        return (
          <StatusBadge
            label={label}
            variant={config.variant}
            showDot={config.showDot}
            size='sm'
            copyable={false}
          />
        )
      },
      filterFn: (row, id, value) => {
        if (!value || value.length === 0 || value.includes('all')) return true
        const status = row.getValue(id) as number
        if (value.includes('enabled')) return status === 1
        if (value.includes('disabled')) return status !== 1
        return false
      },
      size: 120,
      enableSorting: false,
    },

    // Health column (passive auto-degrade)
    {
      accessorKey: 'degrade_level',
      meta: { label: t('Health'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Health')} />
      ),
      cell: ({ row }) => {
        if (isTagAggregateRow(row.original)) return null
        const channel = row.original as Channel
        const level = channel.degrade_level ?? 0
        const permLocked = (channel.permanent_disabled ?? 0) === 1
        const status = channel.status

        let label = 'L0'
        let variant: 'success' | 'warning' | 'orange' | 'danger' | 'red' =
          'success'
        let showDot = true
        if (permLocked) {
          label = t('Locked')
          variant = 'red'
        } else if (status === 2 || status === 3) {
          label = t('Disabled')
          variant = 'danger'
        } else if (level >= 8) {
          label = `L${level}`
          variant = 'red'
        } else if (level >= 4) {
          label = `L${level}`
          variant = 'orange'
        } else if (level >= 1) {
          label = `L${level}`
          variant = 'warning'
        } else {
          label = 'L0'
          variant = 'success'
          showDot = false
        }

        const lastDemoteAt = channel.last_demote_at ?? 0
        const lastDemoteReason = channel.last_demote_reason ?? ''
        const rebounceCount = channel.rebounce_count ?? 0
        const originalPriority = channel.original_priority ?? 0
        const originalWeight = channel.original_weight ?? 0
        const hasTooltip =
          permLocked || level > 0 || status === 2 || status === 3

        if (!hasTooltip) {
          return (
            <StatusBadge
              label={label}
              variant={variant}
              showDot={showDot}
              size='sm'
              copyable={false}
            />
          )
        }

        return (
          <TooltipProvider delayDuration={100}>
            <Tooltip>
              <TooltipTrigger asChild>
                <span>
                  <StatusBadge
                    label={label}
                    variant={variant}
                    showDot={showDot}
                    size='sm'
                    copyable={false}
                  />
                </span>
              </TooltipTrigger>
              <TooltipContent side='top' className='max-w-xs'>
                <div className='space-y-1 text-xs'>
                  {lastDemoteAt > 0 && (
                    <div>
                      {t('Last demote:')} {formatTimestampToDate(lastDemoteAt)}
                    </div>
                  )}
                  {lastDemoteReason && (
                    <div className='break-all'>
                      {t('Reason:')} {truncateText(lastDemoteReason, 200)}
                    </div>
                  )}
                  {(originalPriority !== 0 || originalWeight !== 0) && (
                    <div>
                      {t('Snapshot:')} priority={originalPriority}, weight=
                      {originalWeight}
                    </div>
                  )}
                  {rebounceCount > 0 && (
                    <div>
                      {t('Rebounce count:')} {rebounceCount}
                    </div>
                  )}
                  {permLocked && (
                    <div className='text-red-500'>
                      {t('Permanently locked — manual recovery required')}
                    </div>
                  )}
                </div>
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        )
      },
      size: 100,
      enableSorting: true,
    },

    // Models column
    {
      accessorKey: 'models',
      meta: { label: t('Models'), mobileHidden: true },
      header: t('Models'),
      cell: ({ row }) => {
        const models = row.getValue('models') as string
        const modelArray = parseModelsList(models)

        if (modelArray.length === 0) {
          return <span className='text-muted-foreground text-xs'>-</span>
        }

        const modelBadges = modelArray.map((model, idx) => (
          <StatusBadge
            key={idx}
            label={model}
            autoColor={model}
            size='sm'
            className='font-mono'
          />
        ))

        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <div>{renderLimitedItems(modelBadges, 2)}</div>
              </TooltipTrigger>
              {modelArray.length > 2 && (
                <TooltipContent
                  side='top'
                  className='border-border bg-popover max-h-48 max-w-[320px] overflow-y-auto p-2'
                >
                  <div className='flex flex-wrap gap-1'>{modelBadges}</div>
                </TooltipContent>
              )}
            </Tooltip>
          </TooltipProvider>
        )
      },
      size: 200,
      enableSorting: false,
    },

    // Group column
    {
      accessorKey: 'group',
      meta: { label: t('Groups'), mobileHidden: true },
      header: t('Groups'),
      cell: ({ row }) => {
        const group = row.getValue('group') as string
        const groupArray = parseGroupsList(group)

        const groupBadges = groupArray.map((g) => (
          <GroupBadge key={g} group={g} size='sm' />
        ))

        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <div>{renderLimitedItems(groupBadges, 2)}</div>
              </TooltipTrigger>
              {groupArray.length > 2 && (
                <TooltipContent
                  side='top'
                  className='border-border bg-popover max-h-48 max-w-[320px] overflow-y-auto p-2'
                >
                  <div className='flex flex-wrap gap-1'>{groupBadges}</div>
                </TooltipContent>
              )}
            </Tooltip>
          </TooltipProvider>
        )
      },
      filterFn: (row, id, value) => {
        if (!value || value.length === 0 || value.includes('all')) return true
        const group = row.getValue(id) as string
        const groupArray = parseGroupsList(group)
        return groupArray.some((g) => value.includes(g))
      },
      size: 150,
      enableSorting: false,
    },

    // Tag column
    {
      accessorKey: 'tag',
      meta: { label: t('Tag'), mobileHidden: true },
      header: t('Tag'),
      cell: ({ row }) => {
        const tag = row.getValue('tag') as string | null
        if (!tag)
          return <span className='text-muted-foreground text-xs'>-</span>

        return <StatusBadge label={tag} autoColor={tag} size='sm' />
      },
      size: 120,
      enableSorting: false,
    },

    // Priority column
    {
      accessorKey: 'priority',
      meta: { label: t('Priority'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Priority')} />
      ),
      cell: ({ row }) => <PriorityCell channel={row.original} />,
      size: 100,
    },

    // Weight column
    {
      accessorKey: 'weight',
      meta: { label: t('Weight'), mobileHidden: true },
      header: t('Weight'),
      cell: ({ row }) => <WeightCell channel={row.original} />,
      size: 90,
      enableSorting: false,
    },

    // Routing mode column（docs/2026-05-26-channel-routing-mode-switchable.md）
    {
      accessorKey: 'routing_mode',
      meta: { label: t('Routing'), mobileHidden: true },
      header: t('Routing'),
      cell: ({ row }) => <RoutingModeCell channel={row.original} />,
      size: 110,
      enableSorting: false,
    },

    // Balance column (Used/Remaining)
    {
      accessorKey: 'balance',
      meta: { label: t('Used / Remaining') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Used / Remaining')} />
      ),
      cell: ({ row }) => <BalanceCell channel={row.original} />,
      size: 180,
    },

    // Response Time column
    {
      accessorKey: 'response_time',
      meta: { label: t('Response'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Response')} />
      ),
      cell: ({ row }) => {
        const responseTime = row.getValue('response_time') as number
        const config = getResponseTimeConfig(responseTime)

        return (
          <StatusBadge
            label={formatResponseTime(responseTime, t)}
            variant={config.variant}
            size='sm'
            copyable={false}
          />
        )
      },
      size: 110,
    },

    // Test Time column
    {
      accessorKey: 'test_time',
      meta: { label: t('Last Tested'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Last Tested')} />
      ),
      cell: ({ row }) => {
        const testTime = row.getValue('test_time') as number

        // For invalid timestamps, show "Never" badge
        if (!testTime || testTime === 0) {
          return <span className='text-muted-foreground text-xs'>-</span>
        }

        const timeText = formatRelativeTime(testTime)
        const fullDate = formatTimestampToDate(testTime)

        // For valid timestamps, show tooltip with full date
        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <span className='text-muted-foreground cursor-pointer font-mono text-sm'>
                  {timeText}
                </span>
              </TooltipTrigger>
              <TooltipContent side='top'>
                <p className='font-mono text-sm'>{fullDate}</p>
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        )
      },
      size: 120,
      enableSorting: false,
    },

    // Actions column
    {
      id: 'actions',
      cell: ({ row }) => {
        // Check if this is a tag row (has children)
        const isTagRow = isTagAggregateRow(row.original)

        if (isTagRow) {
          return (
            <DataTableTagRowActions
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              row={row as any}
            />
          )
        }

        return <DataTableRowActions row={row} />
      },
      size: 132,
      enableSorting: false,
      enableHiding: false,
    },
  ]
}

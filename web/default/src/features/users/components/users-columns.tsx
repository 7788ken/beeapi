import { type ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { formatQuota, formatTimestamp } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Checkbox } from '@/components/ui/checkbox'
import { Progress } from '@/components/ui/progress'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { DataTableColumnHeader } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { LongText } from '@/components/long-text'
import { StatusBadge, dotColorMap } from '@/components/status-badge'
import {
  USER_STATUSES,
  USER_ROLES,
  isUserDeleted,
} from '../constants'
import { type User } from '../types'
import { DataTableRowActions } from './data-table-row-actions'

function getQuotaProgressColor(percentage: number): string {
  if (percentage <= 10) return '[&_[data-slot=progress-indicator]]:bg-rose-500'
  if (percentage <= 30) return '[&_[data-slot=progress-indicator]]:bg-amber-500'
  return '[&_[data-slot=progress-indicator]]:bg-emerald-500'
}

export function useUsersColumns(): ColumnDef<User>[] {
  const { t } = useTranslation()
  return [
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
          className='translate-y-[2px]'
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(value) => row.toggleSelected(!!value)}
          aria-label='Select row'
          className='translate-y-[2px]'
        />
      ),
      enableSorting: false,
      enableHiding: false,
      meta: { label: t('Select') },
    },
    {
      accessorKey: 'id',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title='ID' />
      ),
      cell: ({ row }) => {
        return <div className='w-[60px]'>{row.getValue('id')}</div>
      },
      meta: { label: t('ID'), mobileHidden: true },
    },
    // RPM 列紧贴 ID：最近 60s 滑动窗口实时 RPM（Redis 桶 + 加权法）。
    // 注意：rpm_24h 字段名保留（向后兼容），数值已由 controller.overlayRealtimeUserRPM 用 Redis 值覆盖。
    {
      id: 'rpm_24h',
      accessorKey: 'rpm_24h',
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
          <Tooltip>
            <TooltipTrigger asChild>
              <span className='font-mono text-xs tabular-nums cursor-help font-medium'>
                {display}
              </span>
            </TooltipTrigger>
            <TooltipContent>
              {t('Realtime requests per minute (sliding 60s window).')}
            </TooltipContent>
          </Tooltip>
        )
      },
      meta: { label: t('RPM') },
    },
    {
      accessorKey: 'username',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Username')} />
      ),
      cell: ({ row }) => {
        const username = row.getValue('username') as string
        const displayName = row.original.display_name
        const remark = row.original.remark

        return (
          <div className='flex min-w-[160px] flex-col gap-1'>
            <div className='flex items-center gap-2'>
              <LongText className='max-w-[140px] font-medium'>
                {username}
              </LongText>
              {remark && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <StatusBadge variant='success' copyable={false}>
                      <LongText className='max-w-[80px]'>{remark}</LongText>
                    </StatusBadge>
                  </TooltipTrigger>
                  <TooltipContent>
                    <p className='text-xs'>{remark}</p>
                  </TooltipContent>
                </Tooltip>
              )}
            </div>
            {displayName && displayName !== username && (
              <LongText className='text-muted-foreground max-w-[180px] text-xs'>
                {displayName}
              </LongText>
            )}
          </div>
        )
      },
      enableHiding: false,
      meta: { label: t('Username'), mobileTitle: true },
    },
    {
      accessorKey: 'status',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Status')} />
      ),
      cell: ({ row }) => {
        const user = row.original
        const requestCount = user.request_count

        const statusConfig = isUserDeleted(user)
          ? USER_STATUSES.DELETED
          : USER_STATUSES[user.status as keyof typeof USER_STATUSES]

        if (!statusConfig) {
          return null
        }

        return (
          <Tooltip>
            <TooltipTrigger asChild>
              <div className='cursor-help'>
                <StatusBadge
                  label={t(statusConfig.labelKey)}
                  variant={statusConfig.variant}
                  showDot={statusConfig.showDot}
                  copyable={false}
                />
              </div>
            </TooltipTrigger>
            <TooltipContent>
              <p className='text-xs'>
                {t('Requests:')} {requestCount.toLocaleString()}
              </p>
            </TooltipContent>
          </Tooltip>
        )
      },
      filterFn: (row, id, value) => {
        return value.includes(String(row.getValue(id)))
      },
      enableSorting: false,
      meta: { label: t('Status'), mobileBadge: true },
    },
    {
      // 余额列：当前可用额度（user.quota）。
      id: 'quota',
      accessorKey: 'quota',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Balance')} />
      ),
      cell: ({ row }) => {
        const user = row.original
        const used = user.used_quota
        const remaining = user.quota
        const total = used + remaining
        const percentage = total > 0 ? (remaining / total) * 100 : 0

        if (total === 0) {
          return (
            <StatusBadge
              label={t('No Quota')}
              variant='neutral'
              copyable={false}
            />
          )
        }

        return (
          <Tooltip>
            <TooltipTrigger asChild>
              <div className='w-[120px] cursor-help space-y-1'>
                <div className='font-medium tabular-nums text-xs'>
                  {formatQuota(remaining)}
                </div>
                <Progress
                  value={percentage}
                  className={cn('h-1.5', getQuotaProgressColor(percentage))}
                />
              </div>
            </TooltipTrigger>
            <TooltipContent>
              <div className='space-y-1 text-xs'>
                <div>
                  {t('Remaining:')} {formatQuota(remaining)}
                </div>
                <div>
                  {t('Total:')} {formatQuota(total)}
                </div>
                <div>
                  {t('Percentage:')} {percentage.toFixed(1)}%
                </div>
              </div>
            </TooltipContent>
          </Tooltip>
        )
      },
      meta: { label: t('Balance') },
    },
    // 累计使用量列：user.used_quota（历史已消耗），与"余额"并列展示。
    {
      id: 'used_quota',
      accessorKey: 'used_quota',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Used')} />
      ),
      cell: ({ row }) => {
        const used = row.original.used_quota
        if (!used || used <= 0) {
          return <span className='text-muted-foreground text-xs'>-</span>
        }
        return (
          <Tooltip>
            <TooltipTrigger asChild>
              <span className='font-mono text-xs tabular-nums cursor-help'>
                {formatQuota(used)}
              </span>
            </TooltipTrigger>
            <TooltipContent>
              {t('Cumulative quota consumed since account creation.')}
            </TooltipContent>
          </Tooltip>
        )
      },
      meta: { label: t('Used') },
    },
    {
      accessorKey: 'group',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Group')} />
      ),
      cell: ({ row }) => {
        const group = row.getValue('group') as string
        return <GroupBadge group={group} />
      },
      filterFn: (row, id, value) => {
        const group = String(row.getValue(id) || t('User Group')).toLowerCase()
        const searchValue = String(value).toLowerCase()
        return group.includes(searchValue)
      },
      meta: { label: t('Group') },
    },
    {
      accessorKey: 'role',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Role')} />
      ),
      cell: ({ row }) => {
        const roleValue = row.getValue('role') as number
        const roleConfig = USER_ROLES[roleValue as keyof typeof USER_ROLES]

        if (!roleConfig) {
          return null
        }

        return (
          <div className='flex items-center gap-x-2'>
            {roleConfig.icon && (
              <roleConfig.icon size={16} className='text-muted-foreground' />
            )}
            <span className='text-sm'>{t(roleConfig.labelKey)}</span>
          </div>
        )
      },
      filterFn: (row, id, value) => {
        return value.includes(String(row.getValue(id)))
      },
      enableSorting: false,
      meta: { label: t('Role') },
    },
    {
      id: 'invite_info',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Invite Info')} />
      ),
      cell: ({ row }) => {
        const user = row.original
        const affCount = user.aff_count || 0
        const affHistoryQuota = user.aff_history_quota || 0
        const inviterId = user.inviter_id || 0

        return (
          <div className='flex items-center gap-1.5 text-xs font-medium'>
            <span
              className={cn(
                'size-1.5 shrink-0 rounded-full',
                dotColorMap.neutral
              )}
              aria-hidden='true'
            />
            <Tooltip>
              <TooltipTrigger asChild>
                <span className='text-muted-foreground cursor-help'>
                  {t('Invited')}: {affCount}
                </span>
              </TooltipTrigger>
              <TooltipContent>
                <p className='text-xs'>{t('Number of users invited')}</p>
              </TooltipContent>
            </Tooltip>
            <span className='text-muted-foreground/30'>·</span>
            <Tooltip>
              <TooltipTrigger asChild>
                <span className='text-muted-foreground cursor-help'>
                  {t('Revenue')}: {formatQuota(affHistoryQuota)}
                </span>
              </TooltipTrigger>
              <TooltipContent>
                <p className='text-xs'>{t('Total invitation revenue')}</p>
              </TooltipContent>
            </Tooltip>
            {inviterId > 0 && (
              <>
                <span className='text-muted-foreground/30'>·</span>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span className='text-muted-foreground cursor-help'>
                      {t('Inviter')}: {inviterId}
                    </span>
                  </TooltipTrigger>
                  <TooltipContent>
                    <p className='text-xs'>
                      {t('Invited by user ID')} {inviterId}
                    </p>
                  </TooltipContent>
                </Tooltip>
              </>
            )}
            {inviterId === 0 && (
              <>
                <span className='text-muted-foreground/30'>·</span>
                <span className='text-muted-foreground'>{t('No Inviter')}</span>
              </>
            )}
          </div>
        )
      },
      enableSorting: false,
      meta: { label: t('Invite Info'), mobileHidden: true },
    },
    {
      accessorKey: 'created_at',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Created At')} />
      ),
      cell: ({ row }) => {
        const ts = row.getValue('created_at') as number | undefined
        return (
          <span className='text-muted-foreground text-sm'>
            {ts ? formatTimestamp(ts) : '-'}
          </span>
        )
      },
      meta: { label: t('Created At'), mobileHidden: true },
    },
    {
      accessorKey: 'last_login_at',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Last Login')} />
      ),
      cell: ({ row }) => {
        const ts = row.getValue('last_login_at') as number | undefined
        return (
          <span className='text-muted-foreground text-sm'>
            {ts ? formatTimestamp(ts) : '-'}
          </span>
        )
      },
      meta: { label: t('Last Login'), mobileHidden: true },
    },
    {
      id: 'actions',
      cell: ({ row }) => <DataTableRowActions row={row} />,
      meta: { label: t('Actions') },
    },
  ]
}

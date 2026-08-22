import { Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatQuota } from '@/lib/format'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { StatusBadge } from '@/components/status-badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { InviteesData } from '../types'

interface InviteeListProps {
  data: InviteesData | null
  loading?: boolean
}

function formatDate(ts: number): string {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleDateString()
}

// 隐私脱敏：邮箱保留首字符 + 末位 + 完整域名；普通名保留首尾，中间打 *。
function maskName(raw: string): string {
  if (!raw) return '-'
  const at = raw.indexOf('@')
  if (at > 0) {
    const local = raw.slice(0, at)
    const domain = raw.slice(at)
    if (local.length <= 1) return `${local}***${domain}`
    if (local.length === 2) return `${local[0]}*${domain}`
    if (local.length === 3) return `${local[0]}*${local[2]}${domain}`
    return `${local[0]}${'*'.repeat(local.length - 2)}${local[local.length - 1]}${domain}`
  }
  if (raw.length <= 1) return '*'
  if (raw.length === 2) return `${raw[0]}*`
  if (raw.length === 3) return `${raw[0]}*${raw[2]}`
  return `${raw[0]}${'*'.repeat(raw.length - 2)}${raw[raw.length - 1]}`
}

export function InviteeList({ data, loading }: InviteeListProps) {
  const { t } = useTranslation()

  if (loading) {
    return (
      <Card>
        <CardContent className='space-y-3 p-4 sm:p-6'>
          <Skeleton className='h-4 w-32' />
          <Skeleton className='h-48 w-full rounded-lg' />
        </CardContent>
      </Card>
    )
  }

  const invitees = data?.invitees ?? []
  const enabled = data?.enabled ?? false
  const ratio = data?.commission_ratio ?? 0

  return (
    <Card>
      <CardContent className='space-y-4 p-4 sm:p-6'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div className='flex items-center gap-2'>
            <Users className='text-muted-foreground size-4' />
            <h3 className='text-sm font-semibold'>{t('Invited Users')}</h3>
            <span className='text-muted-foreground text-xs'>
              · {invitees.length}
            </span>
          </div>
          <div className='flex items-center gap-2 text-xs'>
            {enabled && ratio > 0 ? (
              <StatusBadge variant='success' copyable={false}>
                {t('Commission')} {(ratio * 100).toFixed(1)}%
              </StatusBadge>
            ) : (
              <StatusBadge variant='neutral' copyable={false}>
                {t('Commission disabled')}
              </StatusBadge>
            )}
          </div>
        </div>

        {invitees.length === 0 ? (
          <p className='text-muted-foreground py-8 text-center text-sm'>
            {t('No invited users yet. Share your referral link to start earning.')}
          </p>
        ) : (
          <div className='overflow-x-auto'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Username')}</TableHead>
                  <TableHead>{t('Joined')}</TableHead>
                  <TableHead className='text-right'>
                    {t('Their Consumption')}
                  </TableHead>
                  <TableHead className='text-right'>
                    {t('Commission Earned')}
                  </TableHead>
                  <TableHead>{t('Status')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {invitees.map((inv) => (
                  <TableRow key={inv.id}>
                    <TableCell className='font-medium'>
                      <div className='truncate'>
                        {maskName(inv.display_name || inv.username)}
                      </div>
                    </TableCell>
                    <TableCell className='text-muted-foreground text-xs'>
                      {formatDate(inv.created_at)}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {formatQuota(inv.total_consume)}
                    </TableCell>
                    <TableCell className='text-right font-semibold tabular-nums'>
                      {formatQuota(inv.total_commission)}
                    </TableCell>
                    <TableCell>
                      {inv.status === 1 ? (
                        <StatusBadge
                          label={t('Active')}
                          variant='success'
                          copyable={false}
                        />
                      ) : (
                        <StatusBadge
                          label={t('Disabled')}
                          variant='neutral'
                          copyable={false}
                        />
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { formatDateTimeObject } from '@/lib/time'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { getUserQuotaDataByUserGroups } from '@/features/dashboard/api'
import { renderQuotaCompat } from '@/features/dashboard/lib'

interface UserGroupsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  username: string
  startTimestamp: number
  endTimestamp: number
}

const intFormatter = new Intl.NumberFormat(undefined, {
  maximumFractionDigits: 0,
})
const formatInt = (n: number) => intFormatter.format(n)

export function UserGroupsDialog(props: UserGroupsDialogProps) {
  const { t } = useTranslation()
  const { open, onOpenChange, username, startTimestamp, endTimestamp } = props

  const { data, isLoading, isError } = useQuery({
    queryKey: ['user-groups', username, startTimestamp, endTimestamp],
    queryFn: async () => {
      const res = await getUserQuotaDataByUserGroups({
        username,
        start_timestamp: startTimestamp,
        end_timestamp: endTimestamp,
      })
      if (!res.success) {
        throw new Error('failed to load user groups')
      }
      return res.data
    },
    staleTime: 60_000,
    enabled: open,
  })

  const rows = useMemo(
    () =>
      (data?.items ?? [])
        .map((item) => ({
          group: item.group ?? '',
          count: Number(item.count) || 0,
          quota: Number(item.quota) || 0,
          tokenUsed: Number(item.token_used) || 0,
        }))
        .sort((a, b) => b.quota - a.quota),
    [data]
  )

  const totals = useMemo(
    () =>
      rows.reduce(
        (sum, row) => ({
          count: sum.count + row.count,
          quota: sum.quota + row.quota,
          tokenUsed: sum.tokenUsed + row.tokenUsed,
        }),
        { count: 0, quota: 0, tokenUsed: 0 }
      ),
    [rows]
  )

  const startLabel = formatDateTimeObject(new Date(startTimestamp * 1000))
  const endLabel = formatDateTimeObject(new Date(endTimestamp * 1000))

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>
            {username} · {t('Group Breakdown')}
          </DialogTitle>
          <DialogDescription>
            {startLabel} - {endLabel}
          </DialogDescription>
        </DialogHeader>

        <ScrollArea className='max-h-[65vh] pr-4'>
          {isLoading ? (
            <div className='space-y-2'>
              {Array.from({ length: 5 }).map((_, i) => (
                <Skeleton key={i} className='h-8 w-full' />
              ))}
            </div>
          ) : isError ? (
            <div className='text-muted-foreground py-8 text-center text-sm'>
              {t('Failed to load group breakdown')}
            </div>
          ) : rows.length === 0 ? (
            <div className='text-muted-foreground py-8 text-center text-sm'>
              {t('No group consumption records in this period')}
            </div>
          ) : (
            <div className='overflow-x-auto'>
              <table className='w-full text-sm'>
                <thead className='bg-muted/40 text-muted-foreground'>
                  <tr className='text-left text-xs whitespace-nowrap'>
                    <th className='px-4 py-2.5'>{t('Group')}</th>
                    <th className='hidden w-44 px-4 py-2.5 sm:table-cell'>
                      {t('Share')}
                    </th>
                    <th className='px-4 py-2.5 text-right'>{t('Quota')}</th>
                    <th className='px-4 py-2.5 text-right'>{t('Calls')}</th>
                    <th className='hidden px-4 py-2.5 text-right sm:table-cell'>
                      {t('Tokens')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row) => {
                    const ratio =
                      totals.quota > 0 ? row.quota / totals.quota : 0
                    return (
                      <tr
                        key={row.group || '(ungrouped)'}
                        className='border-border/40 border-t'
                      >
                        <td className='px-4 py-2.5 font-medium'>
                          {row.group || t('(ungrouped)')}
                        </td>
                        <td className='hidden px-4 py-2.5 sm:table-cell'>
                          <div className='bg-background/60 h-1.5 w-full overflow-hidden rounded-full'>
                            <div
                              className='bg-primary/70 h-full rounded-full transition-all'
                              style={{
                                width: `${Math.max(ratio * 100, row.quota > 0 ? 2 : 0).toFixed(1)}%`,
                              }}
                            />
                          </div>
                        </td>
                        <td className='px-4 py-2.5 text-right font-semibold tabular-nums'>
                          {renderQuotaCompat(row.quota, 2)}
                        </td>
                        <td className='px-4 py-2.5 text-right tabular-nums'>
                          {formatInt(row.count)}
                        </td>
                        <td className='hidden px-4 py-2.5 text-right tabular-nums sm:table-cell'>
                          {formatInt(row.tokenUsed)}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
                <tfoot>
                  <tr className='bg-muted/40 border-t font-semibold'>
                    <td className='px-4 py-2.5'>{t('Total')}</td>
                    <td className='hidden px-4 py-2.5 sm:table-cell' />
                    <td className='px-4 py-2.5 text-right tabular-nums'>
                      {renderQuotaCompat(totals.quota, 2)}
                    </td>
                    <td className='px-4 py-2.5 text-right tabular-nums'>
                      {formatInt(totals.count)}
                    </td>
                    <td className='hidden px-4 py-2.5 text-right tabular-nums sm:table-cell'>
                      {formatInt(totals.tokenUsed)}
                    </td>
                  </tr>
                </tfoot>
              </table>
            </div>
          )}
        </ScrollArea>

        <DialogFooter>
          <div className='text-muted-foreground text-xs'>
            {t('For exact reconciliation, use usage logs.')}
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

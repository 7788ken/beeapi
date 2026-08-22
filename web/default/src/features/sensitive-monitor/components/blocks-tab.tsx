import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Eye, Snowflake, Sun } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import dayjs from '@/lib/dayjs'
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
import { listBlocks, toggleBlockToken } from '../api'
import type { SensitiveBlockLog } from '../types'
import { BlockDetailDialog } from './block-detail-dialog'

const PAGE_SIZE = 20

function fmtTime(ts?: number) {
  if (!ts) return '-'
  return dayjs(ts * 1000).format('YYYY-MM-DD HH:mm:ss')
}

export function BlocksTab() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [detailFor, setDetailFor] = useState<SensitiveBlockLog | null>(null)

  const query = useQuery({
    queryKey: ['sensitive', 'blocks', page] as const,
    queryFn: async () => {
      const res = await listBlocks({ page, pageSize: PAGE_SIZE })
      return res?.data ?? { items: [], total: 0, page: 1, page_size: PAGE_SIZE }
    },
    refetchInterval: 30_000,
  })

  const toggleMutation = useMutation({
    mutationFn: ({ id, disabled }: { id: number; disabled: boolean }) =>
      toggleBlockToken(id, disabled),
    onSuccess: (_res, vars) => {
      toast.success(vars.disabled ? t('Token frozen') : t('Token unfrozen'))
      queryClient.invalidateQueries({ queryKey: ['sensitive', 'blocks'] })
    },
    onError: () => toast.error(t('Action failed')),
  })

  const items = query.data?.items ?? []
  const total = query.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <div className='space-y-3'>
      <div className='rounded-lg border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className='w-12'>{t('ID')}</TableHead>
              <TableHead>{t('Hit keyword')}</TableHead>
              <TableHead className='w-32'>{t('User')}</TableHead>
              <TableHead className='w-32'>{t('Token')}</TableHead>
              <TableHead className='w-32'>{t('Path')}</TableHead>
              <TableHead className='w-44'>{t('Occurred at')}</TableHead>
              <TableHead className='w-32 text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {query.isLoading ? (
              <TableRow>
                <TableCell colSpan={7} className='text-center'>
                  {t('Loading...')}
                </TableCell>
              </TableRow>
            ) : items.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className='text-muted-foreground text-center'
                >
                  {t('No hits yet')}
                </TableCell>
              </TableRow>
            ) : (
              items.map((row) => (
                <TableRow key={row.id}>
                  <TableCell className='tabular-nums'>{row.id}</TableCell>
                  <TableCell className='max-w-[280px] truncate font-medium'>
                    {row.matched_pattern}
                  </TableCell>
                  <TableCell>
                    <div className='truncate'>
                      {row.username || `#${row.user_id}`}
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className='flex items-center gap-1.5'>
                      <span className='truncate'>
                        {row.token_name || `#${row.token_id}`}
                      </span>
                      {row.token_disabled && (
                        <Badge variant='destructive' className='shrink-0'>
                          {t('Frozen')}
                        </Badge>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <span className='text-muted-foreground truncate text-xs'>
                      {row.path ?? '-'}
                    </span>
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {fmtTime(row.created_at)}
                  </TableCell>
                  <TableCell className='text-right'>
                    <div className='flex justify-end gap-1'>
                      <Button
                        variant='ghost'
                        size='icon'
                        className='size-7'
                        onClick={() => setDetailFor(row)}
                        aria-label={t('View detail')}
                      >
                        <Eye className='size-3.5' />
                      </Button>
                      {row.token_id > 0 ? (
                        row.token_disabled ? (
                          <Button
                            variant='ghost'
                            size='icon'
                            className='size-7'
                            onClick={() =>
                              toggleMutation.mutate({
                                id: row.id,
                                disabled: false,
                              })
                            }
                            aria-label={t('Unfreeze token')}
                          >
                            <Sun className='size-3.5' />
                          </Button>
                        ) : (
                          <Button
                            variant='ghost'
                            size='icon'
                            className='size-7 text-rose-500 hover:text-rose-500'
                            onClick={() =>
                              toggleMutation.mutate({
                                id: row.id,
                                disabled: true,
                              })
                            }
                            aria-label={t('Freeze token')}
                          >
                            <Snowflake className='size-3.5' />
                          </Button>
                        )
                      ) : null}
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      {totalPages > 1 && (
        <div className='flex items-center justify-end gap-2'>
          <span className='text-muted-foreground text-xs'>
            {t('Page {{page}} / {{total}}', { page, total: totalPages })}
          </span>
          <Button
            variant='outline'
            size='sm'
            disabled={page <= 1}
            onClick={() => setPage((p) => p - 1)}
          >
            {t('Prev')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            disabled={page >= totalPages}
            onClick={() => setPage((p) => p + 1)}
          >
            {t('Next')}
          </Button>
        </div>
      )}

      <BlockDetailDialog
        block={detailFor}
        onOpenChange={(o) => !o && setDetailFor(null)}
      />
    </div>
  )
}

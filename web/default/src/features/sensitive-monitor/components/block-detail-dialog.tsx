import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import dayjs from '@/lib/dayjs'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { getBlockBody } from '../api'
import type { SensitiveBlockLog } from '../types'

interface Props {
  block: SensitiveBlockLog | null
  onOpenChange: (open: boolean) => void
}

function fmtTime(ts?: number) {
  if (!ts) return '-'
  return dayjs(ts * 1000).format('YYYY-MM-DD HH:mm:ss')
}

export function BlockDetailDialog({ block, onOpenChange }: Props) {
  const { t } = useTranslation()
  const open = !!block
  const hasDump = block?.dump_exists !== false

  const bodyQuery = useQuery({
    queryKey: ['sensitive', 'block-body', block?.id] as const,
    queryFn: async () => (await getBlockBody(block!.id)).data?.body ?? '',
    enabled: open && hasDump,
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Hit detail')}</DialogTitle>
          <DialogDescription>
            #{block?.id} · {fmtTime(block?.created_at)}
          </DialogDescription>
        </DialogHeader>
        <div className='grid grid-cols-2 gap-3 text-sm'>
          <div>
            <span className='text-muted-foreground'>{t('Hit keyword')}: </span>
            <span className='font-medium'>{block?.matched_pattern}</span>
          </div>
          <div>
            <span className='text-muted-foreground'>{t('User')}: </span>
            <span>{block?.username || `#${block?.user_id}`}</span>
          </div>
          <div>
            <span className='text-muted-foreground'>{t('Token')}: </span>
            <span>{block?.token_name || `#${block?.token_id}`}</span>
          </div>
          <div>
            <span className='text-muted-foreground'>{t('Path')}: </span>
            <span className='truncate'>{block?.path || '-'}</span>
          </div>
          {block?.model_name && (
            <div>
              <span className='text-muted-foreground'>{t('Model')}: </span>
              <span className='truncate'>{block.model_name}</span>
            </div>
          )}
          {block?.ip && (
            <div>
              <span className='text-muted-foreground'>IP: </span>
              <span className='truncate'>{block.ip}</span>
            </div>
          )}
          {block?.matched_snippet && (
            <div className='col-span-2'>
              <span className='text-muted-foreground'>
                {t('Matched snippet')}:{' '}
              </span>
              <span className='break-all'>{block.matched_snippet}</span>
            </div>
          )}
        </div>
        <div className='space-y-1'>
          <div className='text-sm font-medium'>{t('Captured body')}</div>
          {!hasDump ? (
            <div className='text-muted-foreground rounded border bg-muted px-2 py-3 text-xs'>
              {t('No body captured (persistence disabled)')}
            </div>
          ) : bodyQuery.isLoading ? (
            <Skeleton className='h-40 w-full' />
          ) : (
            <ScrollArea className='bg-muted h-60 w-full rounded p-2 text-xs'>
              <pre className='whitespace-pre-wrap break-all'>
                {bodyQuery.data ||
                  t('No body captured (persistence disabled)')}
              </pre>
            </ScrollArea>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

import type { ReactNode } from 'react'
import {
  ChevronLeft,
  ChevronRight,
  LoaderCircle,
  RefreshCw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { iqErrorMessage } from '../lib'

export function IQIconButton(props: {
  label: string
  children: ReactNode
  onClick: () => void
  disabled?: boolean
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant='ghost'
          size='icon'
          className='size-8 shrink-0'
          type='button'
          aria-label={props.label}
          disabled={props.disabled}
          onClick={props.onClick}
        >
          {props.children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{props.label}</TooltipContent>
    </Tooltip>
  )
}

export function IQStatus(props: { status: string; className?: string }) {
  const { t } = useTranslation()
  const labels: Record<string, string> = {
    running: t('IQ Running'),
    finished: t('IQ Completed'),
    partial: t('IQ Partial'),
    failed: t('IQ Failed'),
    aborted: t('IQ Aborted'),
    success: t('IQ Valid'),
    invalid: t('IQ Invalid'),
    error: t('IQ Error'),
    never: t('IQ Not tested'),
  }
  let className = 'text-muted-foreground'
  if (['success', 'finished'].includes(props.status))
    className = 'text-emerald-700 dark:text-emerald-400'
  if (['error', 'failed'].includes(props.status))
    className = 'text-rose-700 dark:text-rose-400'
  return (
    <Badge variant='outline' className={cn(className, props.className)}>
      {props.status === 'running' && (
        <LoaderCircle className='mr-1 size-3 animate-spin' />
      )}
      {labels[props.status] ?? props.status}
    </Badge>
  )
}

export function IQQueryState(props: {
  loading: boolean
  error: Error | null
  empty: boolean
  retry: () => void
}) {
  const { t } = useTranslation()
  if (props.loading)
    return (
      <div
        className='text-muted-foreground flex min-h-32 items-center justify-center gap-2 text-sm'
        role='status'
      >
        <LoaderCircle className='size-4 animate-spin' />
        {t('Loading...')}
      </div>
    )
  if (props.error)
    return (
      <div
        className='flex min-h-32 flex-col items-center justify-center gap-2 text-sm'
        role='alert'
      >
        <span>{t('IQ Load failed')}</span>
        <span className='text-muted-foreground max-w-full break-words'>
          {iqErrorMessage(props.error)}
        </span>
        <Button size='sm' variant='outline' onClick={props.retry}>
          <RefreshCw className='size-4' />
          {t('Retry')}
        </Button>
      </div>
    )
  if (props.empty)
    return (
      <div className='text-muted-foreground flex min-h-32 items-center justify-center text-sm'>
        {t('No data')}
      </div>
    )
  return null
}

export function IQPagination(props: {
  page: number
  total: number
  size?: number
  setPage: (page: number) => void
  fetching?: boolean
}) {
  const { t } = useTranslation()
  const pages = Math.max(1, Math.ceil(props.total / (props.size ?? 20)))
  return (
    <div className='flex items-center justify-between gap-3 border-t pt-3 text-sm'>
      <span className='text-muted-foreground'>
        {t('IQ Total records', { count: props.total })}
      </span>
      <div className='flex items-center gap-2'>
        <IQIconButton
          label={t('Previous page')}
          disabled={props.page <= 1 || props.fetching}
          onClick={() => props.setPage(props.page - 1)}
        >
          <ChevronLeft className='size-4' />
        </IQIconButton>
        <span className='min-w-14 text-center tabular-nums'>
          {props.page} / {pages}
        </span>
        <IQIconButton
          label={t('Next page')}
          disabled={props.page >= pages || props.fetching}
          onClick={() => props.setPage(props.page + 1)}
        >
          <ChevronRight className='size-4' />
        </IQIconButton>
      </div>
    </div>
  )
}

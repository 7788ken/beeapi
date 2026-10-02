// History tab：聚合用户的 Midjourney 历史任务（来自 /api/mj/self）。
// 仅展示，不能从这里发起新任务（MJ 走 token auth，session 不能直调 submit）。
import { useQuery } from '@tanstack/react-query'
import {
  CircleAlert,
  History as HistoryIcon,
  ImagePlus,
  Loader2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { surfaceClass } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { getMidjourneyHistory } from './api'
import type { MidjourneyTask } from './types'

function fmtTime(unixSec: number): string {
  if (!unixSec) return '-'
  return new Date(unixSec * 1000).toLocaleString()
}

export function HistoryTab() {
  const { t } = useTranslation()
  const { data: tasks = [], isLoading } = useQuery({
    queryKey: ['create-center', 'mj-history'],
    queryFn: () => getMidjourneyHistory(50),
    staleTime: 60_000,
  })

  if (isLoading) {
    return (
      <div className='mx-auto grid w-full max-w-6xl grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6'>
        {Array.from({ length: 12 }).map((_, i) => (
          <Skeleton key={i} className='aspect-square w-full rounded-xl' />
        ))}
      </div>
    )
  }

  if (tasks.length === 0) {
    return <EmptyHistory />
  }

  return (
    <div className='mx-auto w-full max-w-6xl space-y-3'>
      <div className='text-muted-foreground flex items-center gap-1.5 text-xs'>
        <HistoryIcon className='size-3.5' />
        {t('Showing latest {{n}} Midjourney tasks', { n: tasks.length })}
      </div>
      <div className='grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6'>
        {tasks.map((task) => (
          <HistoryTile key={task.id} task={task} />
        ))}
      </div>
    </div>
  )
}

function EmptyHistory() {
  const { t } = useTranslation()
  return (
    <div className='flex flex-col items-center py-16 text-center'>
      <span className='bg-card text-primary shadow-surface flex size-11 items-center justify-center rounded-xl border'>
        <ImagePlus className='size-5' />
      </span>
      <p className='mt-4 text-sm font-semibold'>{t('No Midjourney history')}</p>
      <p className='text-muted-foreground mt-1 max-w-sm text-xs leading-5'>
        {t(
          'When you submit Midjourney tasks via API, results will appear here.'
        )}
      </p>
    </div>
  )
}

const tileInteractive =
  'transition-all duration-[400ms] ease-out hover:-translate-y-1 hover:shadow-[0_12px_30px_rgba(0,0,0,0.08)] focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none motion-reduce:transition-none motion-reduce:hover:translate-y-0'

function HistoryTile({ task }: { task: MidjourneyTask }) {
  const { t } = useTranslation()
  const status = task.status?.toLowerCase() ?? ''
  const isFailed = status === 'failure' || status === 'failed'
  const isRunning = status === 'in_progress' || status === 'submitted'
  const prompt = task.prompt_en || task.prompt
  const src = task.image_url

  const preview = src ? (
    <img
      src={src}
      alt={prompt}
      loading='lazy'
      className='size-full object-cover'
    />
  ) : (
    <div className='bg-muted/60 text-muted-foreground flex size-full flex-col items-center justify-center gap-2 text-[11px]'>
      {isRunning ? (
        <>
          <Loader2 className='text-primary size-5 animate-spin motion-reduce:animate-none' />
          {task.progress || t('In progress')}
        </>
      ) : isFailed ? (
        <>
          <CircleAlert className='text-foreground size-5' />
          <span className='text-foreground font-semibold'>{t('Failed')}</span>
        </>
      ) : (
        <ImagePlus className='size-5' />
      )}
    </div>
  )

  const body = (
    <>
      <div className='aspect-square overflow-hidden rounded-t-xl'>
        {preview}
      </div>
      <div className='space-y-0.5 p-2.5'>
        <p className='line-clamp-2 text-[11px] leading-4'>{prompt}</p>
        <p className='text-muted-foreground truncate text-[10px] tabular-nums'>
          {task.action} · {fmtTime(task.start_time)}
        </p>
        {isFailed && task.fail_reason && (
          <p className='line-clamp-2 text-[10px] font-semibold'>
            {task.fail_reason}
          </p>
        )}
      </div>
    </>
  )

  return src ? (
    <a
      href={src}
      target='_blank'
      rel='noreferrer'
      title={prompt}
      className={cn(surfaceClass, 'block overflow-hidden', tileInteractive)}
    >
      {body}
    </a>
  ) : (
    <div title={prompt} className={cn(surfaceClass, 'overflow-hidden')}>
      {body}
    </div>
  )
}

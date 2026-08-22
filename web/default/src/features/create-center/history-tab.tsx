// History tab：聚合用户的 Midjourney 历史任务（来自 /api/mj/self）。
// 仅展示，不能从这里发起新任务（MJ 走 token auth，session 不能直调 submit）。

import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { History as HistoryIcon, ImagePlus, Clock, CircleAlert } from 'lucide-react'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import { getMidjourneyHistory } from './api'
import type { MidjourneyTask } from './types'

function fmtTime(unixSec: number): string {
  if (!unixSec) return '-'
  return new Date(unixSec * 1000).toLocaleString()
}

function statusTone(status: string): string {
  const lower = status.toLowerCase()
  if (lower === 'success') return 'text-emerald-600'
  if (lower === 'failure' || lower === 'failed') return 'text-rose-600'
  if (lower === 'in_progress' || lower === 'submitted') return 'text-amber-600'
  return 'text-muted-foreground'
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
      <div className='grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6'>
        {Array.from({ length: 12 }).map((_, i) => (
          <Skeleton key={i} className='aspect-square w-full' />
        ))}
      </div>
    )
  }

  if (tasks.length === 0) {
    return <EmptyHistory t={t} />
  }

  return (
    <div className='space-y-3'>
      <div className='text-muted-foreground flex items-center gap-1.5 text-xs'>
        <HistoryIcon className='h-3.5 w-3.5' />
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

function EmptyHistory({ t }: { t: (k: string) => string }) {
  return (
    <Card className='flex flex-col items-center gap-3 border-dashed py-12 text-center'>
      <div className='bg-gradient-to-br from-violet-500/15 to-pink-500/15 rounded-full p-4'>
        <ImagePlus className='text-violet-500 h-8 w-8' />
      </div>
      <p className='text-sm font-medium'>{t('No Midjourney history')}</p>
      <p className='text-muted-foreground max-w-sm text-xs'>
        {t(
          'When you submit Midjourney tasks via API, results will appear here.'
        )}
      </p>
    </Card>
  )
}

function HistoryTile({ task }: { task: MidjourneyTask }) {
  const { t } = useTranslation()
  const src = task.image_url
  const isFailed = task.status?.toLowerCase() === 'failure'
  const isRunning = ['in_progress', 'submitted'].includes(
    task.status?.toLowerCase()
  )

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div
          className={cn(
            'group relative aspect-square overflow-hidden rounded-lg border',
            isFailed && 'border-rose-300/50',
            src && 'cursor-pointer hover:ring-2 hover:ring-violet-400 hover:ring-offset-2'
          )}
          onClick={() => src && window.open(src, '_blank')}
        >
          {src ? (
            <>
              <img
                src={src}
                alt={task.prompt_en || task.prompt}
                loading='lazy'
                className='h-full w-full object-cover transition-transform duration-300 group-hover:scale-105'
              />
              <div className='absolute inset-0 bg-gradient-to-t from-black/70 via-transparent to-transparent opacity-0 transition-opacity group-hover:opacity-100' />
              <div className='absolute right-0 bottom-0 left-0 space-y-0.5 p-2 text-left text-[10px] text-white opacity-0 transition-opacity group-hover:opacity-100'>
                <p className='line-clamp-2'>{task.prompt_en || task.prompt}</p>
                <p className='opacity-70'>
                  {task.action} · {fmtTime(task.start_time)}
                </p>
              </div>
            </>
          ) : isRunning ? (
            <div className='bg-muted/40 flex h-full w-full flex-col items-center justify-center gap-2'>
              <Clock className='h-6 w-6 animate-pulse text-amber-500' />
              <span className='text-amber-600 text-[10px]'>
                {task.progress || t('In progress')}
              </span>
            </div>
          ) : isFailed ? (
            <div className='bg-rose-50/30 flex h-full w-full flex-col items-center justify-center gap-2 dark:bg-rose-950/20'>
              <CircleAlert className='h-6 w-6 text-rose-500' />
              <span className={cn('text-[10px]', statusTone(task.status))}>
                {t('Failed')}
              </span>
            </div>
          ) : (
            <div className='bg-muted/40 flex h-full w-full items-center justify-center'>
              <ImagePlus className='text-muted-foreground h-6 w-6' />
            </div>
          )}
        </div>
      </TooltipTrigger>
      <TooltipContent side='top' className='max-w-xs'>
        <p className='text-xs'>{task.prompt_en || task.prompt}</p>
        <p className='text-muted-foreground mt-1 text-[10px]'>
          {task.action} · {task.status} · {fmtTime(task.start_time)}
        </p>
        {task.fail_reason && (
          <p className='mt-1 text-[10px] text-rose-500'>{task.fail_reason}</p>
        )}
      </TooltipContent>
    </Tooltip>
  )
}

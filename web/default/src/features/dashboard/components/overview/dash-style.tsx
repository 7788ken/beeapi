import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { dashPrimaryShadow } from './dash-emphasis'

const press =
  'hover:-translate-y-0.5 active:translate-y-0 active:scale-[0.98] active:shadow-[inset_0_2px_4px_rgba(0,0,0,0.2)] transition-all duration-[300ms] ease-out focus-visible:outline-none focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100 disabled:pointer-events-none disabled:opacity-50'

const btnSize = 'box-border h-10 px-4 text-sm leading-none'

export const dashPrimaryBtn = `inline-flex items-center justify-center gap-2 rounded-lg bg-primary font-medium text-primary-foreground ${dashPrimaryShadow} ${btnSize} ${press}`

export const dashSecondaryBtn = `inline-flex items-center justify-center gap-2 rounded-lg border border-border bg-card font-medium text-card-foreground shadow-[0_1px_2px_rgba(0,0,0,0.05),inset_0_1px_0_rgba(255,255,255,0.9)] dark:shadow-[0_1px_2px_rgba(0,0,0,0.4),inset_0_1px_0_rgba(255,255,255,0.08)] ${btnSize} ${press}`

export const toneUp = 'text-primary'
export const toneDown = 'font-semibold text-foreground'
export const toneFlat = 'text-muted-foreground'

export function DashCard(props: { className?: string; children: ReactNode }) {
  return (
    <section
      className={cn(
        'border-border bg-card text-card-foreground flex min-w-0 flex-col rounded-xl border p-6 shadow-[0_2px_4px_rgba(0,0,0,0.04),0_8px_16px_rgba(0,0,0,0.08)] transition-all duration-[400ms] ease-out hover:-translate-y-1 hover:shadow-[0_12px_30px_rgba(0,0,0,0.08)] motion-reduce:transition-none motion-reduce:hover:translate-y-0 md:p-8 dark:shadow-[0_2px_4px_rgba(0,0,0,0.32),0_8px_16px_rgba(0,0,0,0.38)]',
        props.className
      )}
    >
      {props.children}
    </section>
  )
}

/**
 * 卡片正文。并排卡片被拉伸到同一行高时，正文吃掉剩余高度，
 * 卡片高度由这一行里最高的内容决定，而不是各自缩成内容高度。
 */
export function DashBody(props: { className?: string; children: ReactNode }) {
  return (
    <div className={cn('flex min-w-0 flex-1 flex-col', props.className)}>
      {props.children}
    </div>
  )
}

export function DashTitle(props: {
  title: string
  description?: string
  actions?: ReactNode
}) {
  return (
    <div className='mb-6 flex flex-wrap items-start justify-between gap-4'>
      <div className='min-w-0'>
        <h3 className='text-foreground text-xl font-semibold md:text-2xl'>
          {props.title}
        </h3>
        {props.description ? (
          <p className='text-muted-foreground mt-2 max-w-prose text-base text-pretty'>
            {props.description}
          </p>
        ) : null}
      </div>
      {props.actions ? (
        <div className='flex max-w-full shrink-0 flex-wrap items-center gap-4'>
          {props.actions}
        </div>
      ) : null}
    </div>
  )
}

export function DashSkeleton(props: { className?: string }) {
  return (
    <div
      className={cn(
        'bg-foreground/5 animate-pulse rounded-lg motion-reduce:animate-none',
        props.className
      )}
    />
  )
}

type TrendTone = 'up' | 'down' | 'flat'

const trendClass: Record<TrendTone, string> = {
  up: toneUp,
  down: toneDown,
  flat: toneFlat,
}

export function KpiCard(props: {
  label: string
  value: string
  trend: string
  tone: TrendTone
  loading?: boolean
  highlight?: boolean
  children?: ReactNode
}) {
  return (
    <article
      className={cn(
        'min-w-0 rounded-xl p-6 transition-all duration-[400ms] ease-out hover:-translate-y-1 motion-reduce:transition-none motion-reduce:hover:translate-y-0 md:p-8',
        props.highlight
          ? 'bg-primary text-primary-foreground shadow-[0_2px_5px_rgba(99,91,255,0.4),inset_0_1px_0_rgba(255,255,255,0.2)] hover:shadow-[0_12px_30px_rgba(99,91,255,0.28)]'
          : 'border-border bg-card text-card-foreground border shadow-[0_2px_4px_rgba(0,0,0,0.04),0_8px_16px_rgba(0,0,0,0.08)] hover:shadow-[0_12px_30px_rgba(0,0,0,0.08)]'
      )}
    >
      <p
        className={cn(
          'text-base',
          props.highlight ? 'text-white' : 'text-muted-foreground'
        )}
      >
        {props.label}
      </p>
      {props.loading ? (
        <DashSkeleton
          className={cn('mt-3 h-8 w-28', props.highlight && 'bg-white/20')}
        />
      ) : (
        <p
          className={cn(
            'mt-2 text-3xl font-semibold tabular-nums md:text-4xl',
            props.highlight ? 'text-white' : 'text-foreground'
          )}
        >
          {props.value}
        </p>
      )}
      <p
        className={cn(
          'mt-2 text-sm',
          props.highlight ? 'text-white' : trendClass[props.tone]
        )}
      >
        {props.trend}
      </p>
      {props.children}
    </article>
  )
}

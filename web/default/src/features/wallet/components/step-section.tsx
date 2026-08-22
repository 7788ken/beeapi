// 充值流程的步骤区块：数字圆徽 + 标题 + 说明 + 锁定/完成态。
// 仅服务 /wallet 三步充值流，不做组件库级抽象（YAGNI）。

import type { ReactNode } from 'react'
import { Check } from 'lucide-react'
import { cn } from '@/lib/utils'

export type StepState = 'active' | 'done' | 'locked'

interface StepSectionProps {
  step: number
  title: string
  description?: string
  state: StepState
  /** 标题行右侧的联动提示（如"当前金额 $20，2/4 种方式可用"） */
  hint?: ReactNode
  /** locked 时整块降透明并禁止交互时显示的原因文字 */
  lockedHint?: string
  children: ReactNode
}

export function StepSection({
  step,
  title,
  description,
  state,
  hint,
  lockedHint,
  children,
}: StepSectionProps) {
  return (
    <section aria-current={state === 'active' ? 'step' : undefined}>
      <div className='flex flex-wrap items-center gap-2'>
        <span
          className={cn(
            'inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold',
            state === 'done' && 'bg-green-600 text-white dark:bg-green-500',
            state === 'active' && 'bg-primary text-primary-foreground',
            state === 'locked' && 'bg-muted text-muted-foreground'
          )}
        >
          {state === 'done' ? <Check className='h-3 w-3' /> : step}
        </span>
        <h4
          className={cn(
            'text-sm font-semibold',
            state === 'locked' && 'text-muted-foreground'
          )}
        >
          {title}
        </h4>
        {hint && <div className='min-w-0 flex-1 text-right'>{hint}</div>}
      </div>
      {description && (
        <p className='text-muted-foreground mt-0.5 ml-7 text-xs'>
          {description}
        </p>
      )}
      {/* inert 同时阻断鼠标与键盘焦点（pointer-events-none 只挡鼠标，Tab 仍可聚焦内部按钮） */}
      <div
        className={cn('mt-2.5 sm:ml-7', state === 'locked' && 'opacity-50 select-none')}
        inert={state === 'locked'}
        aria-disabled={state === 'locked'}
      >
        {children}
      </div>
      {state === 'locked' && lockedHint && (
        <p className='text-muted-foreground mt-1.5 ml-7 text-xs'>
          {lockedHint}
        </p>
      )}
    </section>
  )
}

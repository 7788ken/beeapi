import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export type FilterChipOption = {
  value: string
  label: string
  count?: number
  suffix?: string
  icon?: ReactNode
}

/** 筛选芯片（模型广场厂商筛选、可用分组分类筛选共用）：选中紫底白字，见 STYLE.md「层级与强调」 */
export function FilterChip(props: {
  option: FilterChipOption
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type='button'
      onClick={props.onClick}
      aria-pressed={props.active}
      className={cn(
        'group inline-flex max-w-full items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium transition-all duration-[300ms] ease-out focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none active:scale-[0.98] motion-reduce:transition-none motion-reduce:active:scale-100',
        props.active
          ? 'border-primary bg-primary text-primary-foreground shadow-[0_2px_5px_rgba(99,91,255,0.4),inset_0_1px_0_rgba(255,255,255,0.2)]'
          : 'border-border/70 bg-background text-muted-foreground hover:border-border hover:bg-muted/50 hover:text-foreground'
      )}
      title={props.option.label}
    >
      {props.option.icon && (
        // 选中时垫白底：彩色厂商 logo 压在紫底上看不清；单色 logo 在白底上显示为主色
        <span
          className={cn(
            'inline-flex size-4 shrink-0 items-center justify-center rounded-sm',
            props.active && 'text-primary bg-white'
          )}
        >
          {props.option.icon}
        </span>
      )}
      <span className='truncate'>{props.option.label}</span>
      {(props.option.suffix || props.option.count != null) && (
        <span
          className={cn(
            'rounded-full px-1.5 py-0.5 text-[10px]',
            props.active
              ? 'text-primary-foreground bg-white/20'
              : 'bg-muted text-muted-foreground'
          )}
        >
          {props.option.suffix ?? props.option.count}
        </span>
      )}
    </button>
  )
}

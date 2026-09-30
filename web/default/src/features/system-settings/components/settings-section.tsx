import { cn } from '@/lib/utils'
import { surfaceClass } from '@/components/ui/card'

type SettingsSectionProps = {
  title: string
  titleProps?: React.HTMLAttributes<HTMLHeadingElement>
  description?: string
  children: React.ReactNode
  className?: string
  /** 内容自带 Card、或本分区嵌在另一个分区的实底里时关掉，避免卡片套卡片 */
  surface?: boolean
}

export function SettingsSection({
  title,
  titleProps,
  description,
  children,
  className,
  surface = true,
}: SettingsSectionProps) {
  const { className: titleClassName, ...restTitleProps } = titleProps ?? {}

  return (
    <section className={cn('space-y-4', className)}>
      <div className='space-y-1 border-s-2 border-primary ps-3'>
        <h3
          {...restTitleProps}
          className={cn(
            'text-base font-semibold tracking-tight text-foreground',
            titleClassName
          )}
        >
          {title}
        </h3>
        {description && (
          <p className='text-muted-foreground text-sm'>{description}</p>
        )}
      </div>
      {surface ? (
        <div className={cn(surfaceClass, 'space-y-4 p-6')}>{children}</div>
      ) : (
        children
      )}
    </section>
  )
}

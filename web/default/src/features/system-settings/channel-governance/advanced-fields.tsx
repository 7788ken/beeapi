import { useState } from 'react'
import { ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { cn } from '@/lib/utils'

/**
 * 折叠后仍挂在 DOM 上。react-hook-form 的字段卸载会从本次提交里消失，
 * 收起高级项不能把那些值丢掉。
 */
export function AdvancedFields({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger className='text-muted-foreground hover:text-foreground flex w-full items-center justify-between rounded-md border border-dashed px-3 py-2 text-sm'>
        <span>{t('Advanced')}</span>
        <ChevronDown
          className={cn('size-4 transition-transform', open && 'rotate-180')}
        />
      </CollapsibleTrigger>
      <CollapsibleContent
        forceMount
        className='data-[state=closed]:hidden space-y-6 pt-4'
      >
        {children}
      </CollapsibleContent>
    </Collapsible>
  )
}

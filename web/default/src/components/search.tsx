import { SearchIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { useSearch } from '@/context/search-provider'
import { Button } from './ui/button'

type SearchProps = {
  className?: string
  type?: React.HTMLInputTypeAttribute
  placeholder?: string
}

export function Search({ className = '', placeholder }: SearchProps) {
  const { t } = useTranslation()
  const { setOpen } = useSearch()
  const resolvedPlaceholder = placeholder ?? t('Search')
  return (
    <Button
      variant='outline'
      className={cn(
        'group border-input bg-card text-foreground relative size-9 rounded-lg border px-0 text-sm font-normal shadow-[0_1px_2px_rgba(0,0,0,0.05)] transition-all duration-[300ms] ease-out hover:-translate-y-0.5 hover:border-[#635bff] focus-visible:border-transparent focus-visible:ring-2 focus-visible:ring-[#635bff] active:translate-y-0 active:scale-[0.98] motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100 xl:h-10 xl:w-64 xl:justify-start xl:px-3 xl:pe-12',
        className
      )}
      onClick={() => setOpen(true)}
      aria-label={resolvedPlaceholder}
    >
      <SearchIcon
        aria-hidden='true'
        className='xl:absolute xl:start-1.5 xl:top-1/2 xl:-translate-y-1/2'
        size={16}
      />
      <span className='ms-4 hidden xl:inline'>{resolvedPlaceholder}</span>
      <kbd className='bg-muted group-hover:bg-accent pointer-events-none absolute end-[0.3rem] top-[0.3rem] hidden h-5 items-center gap-1 rounded border px-1.5 font-mono text-[10px] font-medium opacity-100 select-none xl:flex'>
        <span className='text-xs'>⌘</span>
        {t('K')}
      </kbd>
    </Button>
  )
}

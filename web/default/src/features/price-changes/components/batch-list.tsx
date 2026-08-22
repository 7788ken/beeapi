import { useState } from 'react'
import { ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatDateTimeObject } from '@/lib/time'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import type { PriceChangeBatch, PriceChangeSummary } from '../types'
import { ChangeItemsTable } from './change-items-table'

export function SummaryBadges({
  summary,
  className,
}: {
  summary: PriceChangeSummary
  className?: string
}) {
  const { t } = useTranslation()
  const entries = [
    {
      count: summary?.up ?? 0,
      label: t('Up'),
      cls: 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-300',
    },
    {
      count: summary?.down ?? 0,
      label: t('Down'),
      cls: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300',
    },
    {
      count: summary?.added ?? 0,
      label: t('Added'),
      cls: 'bg-sky-100 text-sky-700 dark:bg-sky-500/20 dark:text-sky-300',
    },
    {
      count: summary?.removed ?? 0,
      label: t('Removed'),
      cls: 'bg-muted text-muted-foreground',
    },
  ].filter((entry) => entry.count > 0)

  if (entries.length === 0) return null

  return (
    <span className={cn('inline-flex flex-wrap items-center gap-1', className)}>
      {entries.map((entry) => (
        <Badge
          key={entry.label}
          variant='outline'
          className={cn('border-transparent px-1.5 text-[10px]', entry.cls)}
        >
          {entry.label} {entry.count}
        </Badge>
      ))}
    </span>
  )
}

function BatchRow({
  batch,
  preferredGroup,
  defaultOpen,
}: {
  batch: PriceChangeBatch
  preferredGroup?: string
  defaultOpen?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen ?? false)

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <div className='rounded-lg border'>
        <CollapsibleTrigger asChild>
          <button
            type='button'
            className='hover:bg-muted/40 flex w-full items-start gap-2 rounded-lg p-3 text-start transition-colors'
          >
            <div className='min-w-0 flex-1 space-y-1'>
              <div className='flex flex-wrap items-center gap-2'>
                <span className='text-xs font-medium'>
                  {formatDateTimeObject(new Date(batch.published_at * 1000))}
                </span>
                <SummaryBadges summary={batch.summary} />
              </div>
              {batch.note && (
                <p className='text-muted-foreground text-xs leading-relaxed whitespace-pre-wrap'>
                  {batch.note}
                </p>
              )}
            </div>
            <ChevronDown
              className={cn(
                'text-muted-foreground mt-0.5 size-4 shrink-0 transition-transform',
                open && 'rotate-180'
              )}
            />
          </button>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <div className='px-3 pb-3'>
            <ChangeItemsTable
              items={batch.items ?? []}
              preferredGroup={preferredGroup}
            />
          </div>
        </CollapsibleContent>
      </div>
    </Collapsible>
  )
}

interface PriceChangeBatchListProps {
  batches: PriceChangeBatch[]
  /** Group whose effective USD price should be displayed for model rows */
  preferredGroup?: string
  loading?: boolean
  className?: string
}

/**
 * Timeline of published price change batches (last 30 days). Shared by the
 * pricing page drawer and the notification bell "Price Updates" tab.
 */
export function PriceChangeBatchList({
  batches,
  preferredGroup,
  loading,
  className,
}: PriceChangeBatchListProps) {
  const { t } = useTranslation()

  if (loading) {
    return (
      <p className='text-muted-foreground py-12 text-center text-sm'>
        {t('Loading...')}
      </p>
    )
  }

  if (!batches || batches.length === 0) {
    return (
      <p className='text-muted-foreground py-12 text-center text-sm'>
        {t('No price changes in the last 30 days')}
      </p>
    )
  }

  return (
    <div className={cn('space-y-2', className)}>
      {batches.map((batch, idx) => (
        <BatchRow
          key={batch.id}
          batch={batch}
          preferredGroup={preferredGroup}
          defaultOpen={idx === 0}
        />
      ))}
    </div>
  )
}

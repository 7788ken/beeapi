import type { TFunction } from 'i18next'
import { TrendingDown, TrendingUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatDate } from '@/lib/time'
import { cn } from '@/lib/utils'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { formatRatioValue, formatUsd, resolveDisplayEntry } from '../lib'
import type { ModelPriceChange, PriceChangeItem } from '../types'

export function getPriceTypeLabel(t: TFunction, priceType: string): string {
  switch (priceType) {
    case 'group_ratio':
      return t('Group Ratio')
    case 'group_group_ratio':
      return t('Group-specific Ratio')
    case 'model_ratio':
      return t('Input')
    case 'completion_ratio':
      return t('Output')
    case 'cache_ratio':
      return t('Cached')
    case 'create_cache_ratio':
      return t('Cache Write')
    case 'model_price':
      return t('Per Request')
    default:
      return priceType
  }
}

export function formatItemOldNew(
  t: TFunction,
  item: PriceChangeItem,
  preferredGroup?: string
): string {
  const resolved = resolveDisplayEntry(item, preferredGroup)
  if (resolved) {
    const unit =
      resolved.entry.unit === 'per_call' ? ` / ${t('request')}` : ' / 1M'
    return `${formatUsd(resolved.entry.old_usd)} → ${formatUsd(resolved.entry.new_usd)}${unit}`
  }
  return `${formatRatioValue(item.old_value)} → ${formatRatioValue(item.new_value)}`
}

interface PriceChangeBadgeProps {
  change: ModelPriceChange
  /** Group whose effective USD price should be shown in the tooltip */
  preferredGroup?: string
  className?: string
}

/**
 * Small up (red) / down (green) badge shown next to a model name on the
 * pricing page. Hover shows old -> new effective USD price and change date.
 */
export function PriceChangeBadge({
  change,
  preferredGroup,
  className,
}: PriceChangeBadgeProps) {
  const { t } = useTranslation()
  const isUp = change.direction === 'up'
  const Icon = isUp ? TrendingUp : TrendingDown

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span
            role='img'
            aria-label={isUp ? t('Price increased') : t('Price decreased')}
            onClick={(e) => e.stopPropagation()}
            className={cn(
              'inline-flex shrink-0 cursor-default items-center gap-0.5 rounded px-1 py-px text-[10px] font-semibold',
              isUp
                ? 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-300'
                : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300',
              className
            )}
          >
            <Icon className='size-3' />
            {isUp ? '↑' : '↓'}
          </span>
        </TooltipTrigger>
        <TooltipContent className='max-w-xs'>
          <div className='space-y-1'>
            <p className='font-medium'>
              {isUp ? t('Price increased') : t('Price decreased')}
            </p>
            {change.items.map((item) => (
              <p key={item.price_type} className='font-mono text-xs'>
                {getPriceTypeLabel(t, item.price_type)}:{' '}
                {formatItemOldNew(t, item, preferredGroup)}
              </p>
            ))}
            <p className='text-xs opacity-70'>
              {formatDate(change.publishedAt)}
            </p>
          </div>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

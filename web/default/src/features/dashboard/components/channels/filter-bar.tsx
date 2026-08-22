// 渠道统计页顶部筛选条：时间窗 / 排序 / TopN / 类型 / 手动刷新。

import { useTranslation } from 'react-i18next'
import { RefreshCw } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  MODEL_TYPE_LABEL,
  RANGE_OPTIONS,
  SORT_OPTIONS,
  type ChannelStatsFilters,
  type ChannelStatsSortBy,
} from './types'

interface FilterBarProps {
  filters: ChannelStatsFilters
  onChange: (filters: ChannelStatsFilters) => void
  onRefresh: () => void
  refreshing?: boolean
  availableTypes: string[]
}

export function FilterBar({
  filters,
  onChange,
  onRefresh,
  refreshing,
  availableTypes,
}: FilterBarProps) {
  const { t } = useTranslation()

  return (
    <div className='flex flex-wrap items-center gap-2'>
      <div className='flex items-center gap-1.5'>
        <span className='text-muted-foreground text-xs'>{t('Range')}</span>
        <Select
          value={String(filters.rangeSeconds)}
          onValueChange={(v) =>
            onChange({ ...filters, rangeSeconds: Number(v) })
          }
        >
          <SelectTrigger size='sm' className='h-8 w-[140px] text-xs'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {RANGE_OPTIONS.map((opt) => (
              <SelectItem key={opt.value} value={String(opt.value)}>
                {t(opt.labelKey)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className='flex items-center gap-1.5'>
        <span className='text-muted-foreground text-xs'>{t('Sort by')}</span>
        <Select
          value={filters.sortBy}
          onValueChange={(v) =>
            onChange({ ...filters, sortBy: v as ChannelStatsSortBy })
          }
        >
          <SelectTrigger size='sm' className='h-8 w-[140px] text-xs'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {SORT_OPTIONS.map((opt) => (
              <SelectItem key={opt.value} value={opt.value}>
                {t(opt.labelKey)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className='flex items-center gap-1.5'>
        <span className='text-muted-foreground text-xs'>{t('Top N')}</span>
        <Input
          type='number'
          min={1}
          max={50}
          value={filters.topN}
          onChange={(e) => {
            const n = Math.max(1, Math.min(50, Number(e.target.value) || 10))
            onChange({ ...filters, topN: n })
          }}
          className='h-8 w-16 text-xs'
        />
      </div>

      <div className='flex items-center gap-1.5'>
        <span className='text-muted-foreground text-xs'>{t('Type')}</span>
        <Select
          value={filters.modelType || '__all__'}
          onValueChange={(v) =>
            onChange({ ...filters, modelType: v === '__all__' ? '' : v })
          }
        >
          <SelectTrigger size='sm' className='h-8 w-[120px] text-xs'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value='__all__'>{t('All types')}</SelectItem>
            {availableTypes.map((tp) => (
              <SelectItem key={tp} value={tp}>
                {MODEL_TYPE_LABEL[tp] ?? tp}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <Button
        variant='outline'
        size='sm'
        className='h-8 text-xs'
        onClick={onRefresh}
        disabled={refreshing}
      >
        <RefreshCw className={cn('mr-1 size-3.5', refreshing && 'animate-spin')} />
        {t('Refresh')}
      </Button>
    </div>
  )
}

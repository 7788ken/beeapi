import { ArrowUpDown, Search, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { FilterChip, type FilterChipOption } from '@/components/filter-chip'
import type { GroupSortKey } from '../lib/directory'
import { secondaryButtonClass } from './styles'

const SORT_KEYS: GroupSortKey[] = [
  'default',
  'availability',
  'ratio_asc',
  'ratio_desc',
]

function useSortLabels(): Record<GroupSortKey, string> {
  const { t } = useTranslation()
  return {
    default: t('Default order'),
    availability: t('Availability: High to Low'),
    ratio_asc: t('Ratio: Low to High'),
    ratio_desc: t('Ratio: High to Low'),
  }
}

export interface GroupToolbarProps {
  search: string
  onSearchChange: (value: string) => void
  onlyAvailable: boolean
  onOnlyAvailableChange: (value: boolean) => void
  /** 可用率数据没加载到时「只看可用」无从判断，开关置灰 */
  availabilityReady: boolean
  sort: GroupSortKey
  onSortChange: (value: GroupSortKey) => void
  categories: FilterChipOption[]
  category: string
  onCategoryChange: (value: string) => void
}

export function GroupToolbar(props: GroupToolbarProps) {
  const { t } = useTranslation()
  const sortLabels = useSortLabels()

  return (
    <div className='space-y-3 border-b p-3 sm:p-4'>
      <div className='flex flex-col gap-3 @2xl:flex-row @2xl:items-center'>
        <div className='relative min-w-0 flex-1 @2xl:max-w-sm'>
          <Search
            className='text-muted-foreground pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2'
            aria-hidden
          />
          <Input
            value={props.search}
            onChange={(e) => props.onSearchChange(e.target.value)}
            placeholder={t('Search groups by name or description')}
            aria-label={t('Search groups by name or description')}
            className='h-9 ps-9 pe-9'
          />
          {props.search && (
            <button
              type='button'
              onClick={() => props.onSearchChange('')}
              aria-label={t('Clear search')}
              className='text-muted-foreground hover:text-foreground absolute end-2 top-1/2 inline-flex size-6 -translate-y-1/2 items-center justify-center rounded-md transition-all duration-[300ms] ease-out focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none active:scale-[0.98] motion-reduce:transition-none motion-reduce:active:scale-100'
            >
              <X className='size-3.5' />
            </button>
          )}
        </div>

        <div className='flex min-w-0 flex-wrap items-center justify-between gap-3 @2xl:ms-auto @2xl:justify-end'>
          {/* 数据没到时如实显示「关」：列表此时本来就是全部分组 */}
          <label className='text-foreground flex cursor-pointer items-center gap-2 text-sm select-none has-disabled:cursor-not-allowed'>
            <Switch
              checked={props.onlyAvailable && props.availabilityReady}
              onCheckedChange={props.onOnlyAvailableChange}
              disabled={!props.availabilityReady}
            />
            {t('Only available')}
          </label>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type='button'
                variant='outline'
                size='sm'
                className={cn(
                  secondaryButtonClass,
                  'h-9 max-w-full gap-1.5 px-3'
                )}
              >
                <ArrowUpDown className='size-3.5' />
                <span className='truncate'>{sortLabels[props.sort]}</span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align='end' className='w-52'>
              <DropdownMenuRadioGroup
                value={props.sort}
                onValueChange={(value) =>
                  props.onSortChange(value as GroupSortKey)
                }
              >
                {SORT_KEYS.map((key) => (
                  <DropdownMenuRadioItem key={key} value={key}>
                    {sortLabels[key]}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      <div
        className='flex flex-wrap gap-1.5'
        role='group'
        aria-label={t('Platform')}
      >
        {props.categories.map((option) => (
          <FilterChip
            key={option.value}
            option={option}
            active={props.category === option.value}
            onClick={() => props.onCategoryChange(option.value)}
          />
        ))}
      </div>
    </div>
  )
}

import * as React from 'react'
import { Check, ChevronsUpDown, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'

export type MultiSelectOption = {
  value: string
  label: string
}

interface MultiSelectComboboxProps {
  options: MultiSelectOption[]
  value?: string[]
  onValueChange: (value: string[]) => void
  placeholder?: string
  searchPlaceholder?: string
  emptyText?: string
  className?: string
}

// MultiSelectCombobox 可搜索多选下拉：基于 Popover + Command。
// 与单选 Combobox 的区别：选中项以 Badge chip 展示、可单独删除，选择时不关闭面板。
export function MultiSelectCombobox({
  options,
  value,
  onValueChange,
  placeholder = 'Select options...',
  searchPlaceholder = 'Search...',
  emptyText = 'No option found.',
  className,
}: MultiSelectComboboxProps) {
  const { t } = useTranslation()
  const [open, setOpen] = React.useState(false)
  const [searchValue, setSearchValue] = React.useState('')

  const selected = React.useMemo(() => value ?? [], [value])

  const filteredOptions = React.useMemo(() => {
    if (!searchValue) return options
    const search = searchValue.toLowerCase()
    return options.filter(
      (option) =>
        option.label.toLowerCase().includes(search) ||
        option.value.toLowerCase().includes(search)
    )
  }, [options, searchValue])

  const toggle = (v: string) => {
    if (selected.includes(v)) {
      onValueChange(selected.filter((x) => x !== v))
    } else {
      onValueChange([...selected, v])
    }
  }

  const removeOne = (v: string) => {
    onValueChange(selected.filter((x) => x !== v))
  }

  const labelFor = (v: string) => options.find((o) => o.value === v)?.label || v

  return (
    <div className={cn('space-y-1.5', className)}>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            variant='outline'
            role='combobox'
            aria-expanded={open}
            className='w-full justify-between font-normal'
          >
            <span className='truncate'>
              {selected.length === 0
                ? placeholder
                : t('{{count}} selected', { count: selected.length })}
            </span>
            <ChevronsUpDown className='ml-2 h-4 w-4 shrink-0 opacity-50' />
          </Button>
        </PopoverTrigger>
        <PopoverContent
          className='w-[var(--radix-popover-trigger-width)] p-0'
          align='start'
          onWheel={(e) => e.stopPropagation()}
          onTouchMove={(e) => e.stopPropagation()}
          onPointerDown={(e) => e.stopPropagation()}
        >
          <Command shouldFilter={false}>
            <CommandInput
              placeholder={searchPlaceholder}
              value={searchValue}
              onValueChange={setSearchValue}
            />
            <CommandList>
              <CommandEmpty>{emptyText}</CommandEmpty>
              <CommandGroup>
                {filteredOptions.map((option) => {
                  const checked = selected.includes(option.value)
                  return (
                    <CommandItem
                      key={option.value}
                      value={option.value}
                      onSelect={() => toggle(option.value)}
                    >
                      <Check
                        className={cn(
                          'mr-2 h-4 w-4',
                          checked ? 'opacity-100' : 'opacity-0'
                        )}
                      />
                      {option.label}
                    </CommandItem>
                  )
                })}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
      {selected.length > 0 && (
        <div className='flex flex-wrap gap-1.5'>
          {selected.map((v) => (
            <Badge key={v} variant='secondary' className='gap-1 pr-1'>
              {labelFor(v)}
              <button
                type='button'
                className='text-muted-foreground hover:text-foreground ml-0.5 rounded-full p-0.5'
                onClick={() => removeOne(v)}
                aria-label={t('Remove')}
              >
                <X className='h-3 w-3' />
              </button>
            </Badge>
          ))}
        </div>
      )}
    </div>
  )
}

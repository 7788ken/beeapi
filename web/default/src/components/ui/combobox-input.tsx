import * as React from 'react'
import { createPortal } from 'react-dom'
import { Check, ChevronsUpDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Input } from '@/components/ui/input'

export type ComboboxInputOption = {
  value: string
  label: string
  icon?: React.ReactNode
}

interface ComboboxInputProps {
  options: ComboboxInputOption[]
  value?: string
  onValueChange: (value: string) => void
  placeholder?: string
  emptyText?: string
  className?: string
  id?: string
  type?: 'text' | 'password'
  /** 用户在输入框按 Enter 且未选中候选项时触发（适合"输入 + 回车 = 提交"场景） */
  onEnter?: () => void
}

export function ComboboxInput({
  options,
  value = '',
  onValueChange,
  placeholder = 'Select or type...',
  emptyText = 'No option found.',
  className,
  id,
  type = 'text',
  onEnter,
}: ComboboxInputProps) {
  const { t } = useTranslation()
  const [open, setOpen] = React.useState(false)
  const [highlightedIndex, setHighlightedIndex] = React.useState(-1)
  const [rect, setRect] = React.useState<{ left: number; top: number; width: number } | null>(null)
  const containerRef = React.useRef<HTMLDivElement>(null)
  const inputRef = React.useRef<HTMLInputElement>(null)
  const listRef = React.useRef<HTMLUListElement>(null)
  const dropdownRef = React.useRef<HTMLDivElement>(null)

  const filteredOptions = React.useMemo(() => {
    if (!value.trim()) return options
    const search = value.toLowerCase().trim()
    return options.filter(
      (option) =>
        option.label.toLowerCase().includes(search) ||
        option.value.toLowerCase().includes(search)
    )
  }, [options, value])

  // Reset highlight when filtered options change
  React.useEffect(() => {
    setHighlightedIndex(-1)
  }, [filteredOptions])

  // Handle click outside to close（dropdown 已 portal 出去，需同时排除 dropdownRef）
  React.useEffect(() => {
    if (!open) return

    const handleClickOutside = (e: MouseEvent) => {
      const target = e.target as Node
      if (containerRef.current && containerRef.current.contains(target)) return
      if (dropdownRef.current && dropdownRef.current.contains(target)) return
      setOpen(false)
    }

    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [open])

  // 计算下拉位置（portal 用，跟随滚动/缩放更新）
  React.useLayoutEffect(() => {
    if (!open) {
      setRect(null)
      return
    }
    const update = () => {
      const el = containerRef.current
      if (!el) return
      const r = el.getBoundingClientRect()
      setRect({ left: r.left, top: r.bottom + 4, width: r.width })
    }
    update()
    window.addEventListener('scroll', update, true)
    window.addEventListener('resize', update)
    return () => {
      window.removeEventListener('scroll', update, true)
      window.removeEventListener('resize', update)
    }
  }, [open])

  const handleSelect = (selectedValue: string) => {
    onValueChange(selectedValue)
    setOpen(false)
    inputRef.current?.focus()
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (!open && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      setOpen(true)
      return
    }

    if (!open) {
      // 关闭下拉时 Enter 直接走 onEnter（保留 "输入+回车 = 提交" 行为）
      if (e.key === 'Enter' && onEnter) {
        onEnter()
      }
      return
    }

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setHighlightedIndex((prev) =>
          prev < filteredOptions.length - 1 ? prev + 1 : 0
        )
        break
      case 'ArrowUp':
        e.preventDefault()
        setHighlightedIndex((prev) =>
          prev > 0 ? prev - 1 : filteredOptions.length - 1
        )
        break
      case 'Enter':
        e.preventDefault()
        if (highlightedIndex >= 0 && filteredOptions[highlightedIndex]) {
          handleSelect(filteredOptions[highlightedIndex].value)
        } else {
          // 无高亮项：关闭下拉，保留输入值，并触发 onEnter（用于父组件 Apply）
          setOpen(false)
          onEnter?.()
        }
        break
      case 'Escape':
        e.preventDefault()
        setOpen(false)
        break
    }
  }

  // Scroll highlighted item into view
  React.useEffect(() => {
    if (highlightedIndex < 0 || !listRef.current) return
    const item = listRef.current.children[highlightedIndex] as HTMLElement
    item?.scrollIntoView({ block: 'nearest' })
  }, [highlightedIndex])

  const showDropdown = open && (filteredOptions.length > 0 || value.trim())

  return (
    <div ref={containerRef} className='relative'>
      <Input
        ref={inputRef}
        id={id}
        type={type}
        role='combobox'
        aria-expanded={open}
        aria-haspopup='listbox'
        aria-autocomplete='list'
        autoComplete='off'
        placeholder={placeholder}
        value={value}
        onChange={(e) => {
          onValueChange(e.target.value)
          if (!open) setOpen(true)
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={handleKeyDown}
        className={cn('pr-9', className)}
      />
      <ChevronsUpDown className='pointer-events-none absolute top-1/2 right-3 size-4 shrink-0 -translate-y-1/2 opacity-50' />

      {showDropdown && rect && createPortal(
        <div
          ref={dropdownRef}
          className='bg-popover text-popover-foreground fixed z-[1000] rounded-md border shadow-md'
          // pointerEvents:auto 必须显式声明：portal 到 body 后，若处于 Radix 模态 Dialog 内，
          // body 会被设为 pointer-events:none，下拉会变成"可见但点不动"
          style={{ left: rect.left, top: rect.top, width: rect.width, pointerEvents: 'auto' }}
        >
          {filteredOptions.length > 0 ? (
            <ul
              ref={listRef}
              role='listbox'
              className='max-h-[200px] overflow-y-auto p-1'
            >
              {filteredOptions.map((option, index) => (
                <li
                  key={option.value}
                  role='option'
                  aria-selected={value === option.value}
                  data-highlighted={index === highlightedIndex}
                  className={cn(
                    'relative flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm select-none',
                    index === highlightedIndex &&
                      'bg-accent text-accent-foreground',
                    value === option.value && 'font-medium'
                  )}
                  onMouseEnter={() => setHighlightedIndex(index)}
                  onMouseDown={(e) => {
                    e.preventDefault() // Prevent blur
                    handleSelect(option.value)
                  }}
                >
                  <Check
                    className={cn(
                      'size-4 shrink-0',
                      value === option.value ? 'opacity-100' : 'opacity-0'
                    )}
                  />
                  {option.icon && <span>{option.icon}</span>}
                  <span className='truncate'>{option.label}</span>
                </li>
              ))}
            </ul>
          ) : (
            <div className='px-2 py-6 text-center text-sm'>
              {emptyText}
              {value.trim() && (
                <div className='text-muted-foreground mt-1 text-xs'>
                  {t('Press Enter to use "{{value}}"', { value: value.trim() })}
                </div>
              )}
            </div>
          )}
        </div>,
        document.body
      )}
    </div>
  )
}

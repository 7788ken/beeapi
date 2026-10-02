// 创作中心输入卡里的「API Key → 模型」两级选择器：模型列表来自所选 key 的 /v1/models。
import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  AlertCircle,
  Check,
  ChevronsUpDown,
  CpuIcon,
  KeyRound,
  Loader2,
  RotateCw,
  Settings2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'
import {
  getKeyUnusableReason,
  type KeyUnusableReason,
} from '@/hooks/use-relay-key'
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
import { composerChip } from '@/components/composer-styles'
import type { ApiKey } from '@/features/keys/types'

const listPanel =
  'bg-popover w-[min(22rem,calc(100vw-2rem))] rounded-xl border p-0 shadow-surface'

interface KeySelectorProps {
  keys: ApiKey[]
  activeKey: ApiKey | null
  onSelect: (id: number) => void
  loading?: boolean
  disabled?: boolean
}

export function KeySelector({
  keys,
  activeKey,
  onSelect,
  loading = false,
  disabled = false,
}: KeySelectorProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  const label = loading
    ? t('Loading API keys')
    : (activeKey?.name ?? t('No usable API key'))
  const activeReason = activeKey ? getKeyUnusableReason(activeKey) : null

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type='button'
          disabled={disabled || loading}
          aria-label={`${t('API key')}: ${label}`}
          className={composerChip}
        >
          {loading ? (
            <Loader2 className='size-3.5 shrink-0 animate-spin motion-reduce:animate-none' />
          ) : (
            <KeyRound className='text-primary size-3.5 shrink-0' />
          )}
          <span className='max-w-[7rem] truncate sm:max-w-[11rem]'>
            {label}
          </span>
          {activeReason && (
            <span className='shrink-0 font-semibold'>· {t(activeReason)}</span>
          )}
          <ChevronsUpDown className='text-muted-foreground size-3.5 shrink-0' />
        </button>
      </PopoverTrigger>
      <PopoverContent align='start' className={listPanel}>
        <Command>
          {keys.length > 6 && (
            <CommandInput
              placeholder={t('Search API keys...')}
              className='h-9'
            />
          )}
          <CommandList className='max-h-72'>
            <CommandEmpty>{t('No API keys')}</CommandEmpty>
            <CommandGroup
              heading={t('Requests are billed to the selected key')}
            >
              {keys.map((key) => (
                <KeyItem
                  key={key.id}
                  apiKey={key}
                  selected={key.id === activeKey?.id}
                  onSelect={() => {
                    onSelect(key.id)
                    setOpen(false)
                  }}
                />
              ))}
            </CommandGroup>
          </CommandList>
          <div className='border-t p-1'>
            <Link
              to='/keys'
              className='text-muted-foreground hover:bg-foreground/5 hover:text-foreground flex h-8 items-center gap-2 rounded-lg px-2 text-xs font-medium transition-colors duration-[300ms] ease-out focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none'
            >
              <Settings2 className='size-3.5' />
              {t('Manage API keys')}
            </Link>
          </div>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

function KeyItem({
  apiKey,
  selected,
  onSelect,
}: {
  apiKey: ApiKey
  selected: boolean
  onSelect: () => void
}) {
  const { t } = useTranslation()
  const reason = getKeyUnusableReason(apiKey)
  const modelLimits = apiKey.model_limits_enabled
    ? (apiKey.model_limits ?? '').split(',').filter(Boolean).length
    : 0

  const meta = [
    apiKey.group || t('Account group'),
    apiKey.unlimited_quota
      ? t('Unlimited')
      : t('Remaining {{amount}}', { amount: formatQuota(apiKey.remain_quota) }),
    modelLimits > 0 ? t('Limited to {{n}} models', { n: modelLimits }) : null,
    apiKey.allow_ips ? t('IP allowlist') : null,
  ].filter(Boolean)

  return (
    <CommandItem
      value={`${apiKey.name} ${apiKey.id}`}
      disabled={reason !== null}
      onSelect={onSelect}
      className='items-start gap-2 rounded-lg px-2 py-2'
    >
      <div className='min-w-0 flex-1'>
        <div className='flex items-center gap-2'>
          <span className='truncate text-xs font-semibold'>{apiKey.name}</span>
          {reason && (
            <span className='text-muted-foreground shrink-0 rounded-lg border px-1.5 text-[10px] leading-4'>
              {t(reason)}
            </span>
          )}
        </div>
        <div className='text-muted-foreground mt-0.5 truncate text-[11px] tabular-nums'>
          {meta.join(' · ')}
        </div>
      </div>
      <Check
        className={cn(
          'text-primary mt-0.5 size-4',
          selected ? 'opacity-100' : 'opacity-0'
        )}
      />
    </CommandItem>
  )
}

function KeyNotice({
  title,
  detail,
  action,
}: {
  title: string
  detail: string
  action: string
}) {
  return (
    <div className='bg-card shadow-surface flex flex-wrap items-center gap-3 rounded-xl border px-4 py-3'>
      <KeyRound className='text-primary size-4 shrink-0' />
      <p className='min-w-0 flex-1 text-xs leading-5'>
        <span className='font-semibold'>{title}</span>
        <span className='text-muted-foreground'> {detail}</span>
      </p>
      <Link to='/keys' className={composerChip}>
        <Settings2 className='size-3.5' />
        {action}
      </Link>
    </div>
  )
}

/** 没有可用 key 时放在输入卡上方：说明原因并给出创建入口。 */
export function NoUsableKeyNotice({ hasKeys }: { hasKeys: boolean }) {
  const { t } = useTranslation()
  return (
    <KeyNotice
      title={
        hasKeys
          ? t('None of your API keys is usable')
          : t('You have no API key yet')
      }
      detail={t('Requests here are sent with your own key and billed to it.')}
      action={hasKeys ? t('Manage API keys') : t('Create API key')}
    />
  )
}

/** 选中的 key 没有本页能用的模型（例如只放开了对话模型却来生图）。 */
export function KeyHasNoModelsNotice({
  keyName,
  kind,
}: {
  keyName: string
  kind: 'chat' | 'image'
}) {
  const { t } = useTranslation()
  return (
    <KeyNotice
      title={
        kind === 'image'
          ? t('Key "{{name}}" has no image models', { name: keyName })
          : t('Key "{{name}}" has no chat models', { name: keyName })
      }
      detail={t(
        'Switch to another key, or allow more models in the key settings.'
      )}
      action={t('Manage API keys')}
    />
  )
}

const UNUSABLE_TITLES: Record<KeyUnusableReason, string> = {
  Disabled: 'Key "{{name}}" is disabled',
  Expired: 'Key "{{name}}" has expired',
  Exhausted: 'Key "{{name}}" has run out of quota',
}

/** 选中的 key 已禁用/过期/耗尽：停在这把 key 上提示，不悄悄换成别的 key 计费。 */
export function KeyUnusableNotice({
  keyName,
  reason,
}: {
  keyName: string
  reason: KeyUnusableReason
}) {
  const { t } = useTranslation()
  return (
    <KeyNotice
      title={t(UNUSABLE_TITLES[reason], { name: keyName })}
      detail={t(
        'Pick another key below, or update this one in key management.'
      )}
      action={t('Manage API keys')}
    />
  )
}

export interface ModelChoice {
  id: string
  /** 下拉里的副标题，例如生图模型的家族名 */
  hint?: string
}

interface ModelSelectorProps {
  models: ModelChoice[]
  value: string
  onChange: (id: string) => void
  loading?: boolean
  error?: Error | null
  onRetry?: () => void
  disabled?: boolean
  /** 列表为空时的说明，例如「这个 key 没有可生图的模型」 */
  emptyText: string
}

export function ModelSelector({
  models,
  value,
  onChange,
  loading = false,
  error = null,
  onRetry,
  disabled = false,
  emptyText,
}: ModelSelectorProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const current = models.find((m) => m.id === value)

  let label = current?.id ?? t('Select model')
  if (loading) label = t('Loading models')
  else if (error) label = t('Models unavailable')
  else if (models.length === 0) label = t('No models')

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type='button'
          disabled={disabled || loading}
          aria-label={`${t('Model')}: ${label}`}
          className={composerChip}
        >
          {loading ? (
            <Loader2 className='size-3.5 shrink-0 animate-spin motion-reduce:animate-none' />
          ) : error ? (
            <AlertCircle className='size-3.5 shrink-0' />
          ) : (
            <CpuIcon className='text-muted-foreground size-3.5 shrink-0' />
          )}
          <span className='max-w-[8rem] truncate sm:max-w-[14rem]'>
            {label}
          </span>
          <ChevronsUpDown className='text-muted-foreground size-3.5 shrink-0' />
        </button>
      </PopoverTrigger>
      <PopoverContent align='start' className={listPanel}>
        {error ? (
          <div className='space-y-3 p-4'>
            <p className='flex items-start gap-2 text-xs font-semibold'>
              <AlertCircle className='mt-0.5 size-3.5 shrink-0' />
              <span className='break-words'>{error.message}</span>
            </p>
            {onRetry && (
              <button
                type='button'
                onClick={() => onRetry()}
                className={composerChip}
              >
                <RotateCw className='size-3.5' />
                {t('Retry')}
              </button>
            )}
          </div>
        ) : models.length === 0 ? (
          <p className='text-muted-foreground p-4 text-xs leading-5'>
            {emptyText}
          </p>
        ) : (
          <Command>
            {models.length > 8 && (
              <CommandInput
                placeholder={t('Search models...')}
                className='h-9'
              />
            )}
            <CommandList className='max-h-80'>
              <CommandEmpty>{t('No model found.')}</CommandEmpty>
              <CommandGroup heading={t('Models available to this key')}>
                {models.map((model) => (
                  <CommandItem
                    key={model.id}
                    value={model.id}
                    onSelect={() => {
                      onChange(model.id)
                      setOpen(false)
                    }}
                    className='gap-2 rounded-lg px-2 py-1.5'
                  >
                    <span className='min-w-0 flex-1 truncate font-mono text-xs'>
                      {model.id}
                    </span>
                    {model.hint && (
                      <span className='text-muted-foreground shrink-0 text-[10px]'>
                        {model.hint}
                      </span>
                    )}
                    <Check
                      className={cn(
                        'text-primary size-4',
                        model.id === value ? 'opacity-100' : 'opacity-0'
                      )}
                    />
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        )}
      </PopoverContent>
    </Popover>
  )
}

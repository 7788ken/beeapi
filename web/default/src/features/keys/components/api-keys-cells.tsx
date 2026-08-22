import { useState, useCallback } from 'react'
import { Check, ChevronDown, Copy, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { GroupBadge, GroupRatioBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import { type ApiKey } from '../types'
import { useApiKeys } from './api-keys-provider'

export function ApiKeyCell({ apiKey }: { apiKey: ApiKey }) {
  const { t } = useTranslation()
  const {
    resolveRealKey,
    resolvedKeys,
    loadingKeys,
    copiedKeyId,
    markKeyCopied,
  } = useApiKeys()
  const [popoverOpen, setPopoverOpen] = useState(false)

  const isLoading = !!loadingKeys[apiKey.id]
  const resolvedFullKey = resolvedKeys[apiKey.id]
  const isCopied = copiedKeyId === apiKey.id
  const maskedKey = `sk-${apiKey.key}`

  const handlePopoverOpen = useCallback(
    (open: boolean) => {
      setPopoverOpen(open)
      if (open && !resolvedFullKey) {
        resolveRealKey(apiKey.id)
      }
    },
    [resolvedFullKey, resolveRealKey, apiKey.id]
  )

  const handleCopy = useCallback(async () => {
    const realKey = resolvedFullKey || (await resolveRealKey(apiKey.id))
    if (realKey) {
      const ok = await copyToClipboard(realKey)
      if (ok) markKeyCopied(apiKey.id)
    }
  }, [resolvedFullKey, resolveRealKey, apiKey.id, markKeyCopied])

  return (
    <div className='flex items-center'>
      <Popover open={popoverOpen} onOpenChange={handlePopoverOpen}>
        <PopoverTrigger asChild>
          <Button
            variant='ghost'
            size='sm'
            className='text-muted-foreground h-7 font-mono text-xs'
          >
            {maskedKey}
          </Button>
        </PopoverTrigger>
        <PopoverContent
          className='w-auto max-w-[min(90vw,28rem)]'
          align='start'
        >
          <div className='space-y-2'>
            <p className='text-muted-foreground text-xs'>{t('Full API Key')}</p>
            {isLoading ? (
              <div className='flex items-center gap-2 py-2'>
                <Loader2 className='size-3.5 animate-spin' />
                <span className='text-muted-foreground text-xs'>
                  {t('Loading...')}
                </span>
              </div>
            ) : (
              <input
                readOnly
                value={resolvedFullKey || maskedKey}
                autoFocus
                onFocus={(e) => e.target.select()}
                className='bg-muted/50 w-full min-w-[280px] rounded-md border px-3 py-2 font-mono text-xs outline-none'
              />
            )}
          </div>
        </PopoverContent>
      </Popover>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant='ghost'
            size='icon'
            className='size-7 shrink-0'
            onClick={handleCopy}
            disabled={isLoading}
          >
            {isLoading ? (
              <Loader2 className='size-3.5 animate-spin' />
            ) : isCopied ? (
              <Check className='size-3.5 text-green-600' />
            ) : (
              <Copy className='size-3.5' />
            )}
          </Button>
        </TooltipTrigger>
        <TooltipContent>
          {isLoading
            ? t('Loading...')
            : isCopied
              ? t('Copied!')
              : t('Copy API key')}
        </TooltipContent>
      </Tooltip>
    </div>
  )
}

export function ModelLimitsCell({ apiKey }: { apiKey: ApiKey }) {
  const { t } = useTranslation()

  if (!apiKey.model_limits_enabled || !apiKey.model_limits) {
    return (
      <StatusBadge label={t('Unlimited')} variant='neutral' copyable={false} />
    )
  }

  const models = apiKey.model_limits.split(',').filter(Boolean)

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span>
          <StatusBadge
            label={t('{{count}} model(s)', { count: models.length })}
            variant='neutral'
            copyable={false}
          />
        </span>
      </TooltipTrigger>
      <TooltipContent side='top' className='max-w-xs'>
        <div className='max-h-[200px] space-y-0.5 overflow-y-auto text-xs'>
          {models.map((m) => (
            <div key={m} className='font-mono'>
              {m}
            </div>
          ))}
        </div>
      </TooltipContent>
    </Tooltip>
  )
}

export function IpRestrictionsCell({ apiKey }: { apiKey: ApiKey }) {
  const { t } = useTranslation()
  const allowIps = apiKey.allow_ips?.trim()

  if (!allowIps) {
    return (
      <StatusBadge
        label={t('No restriction')}
        variant='neutral'
        copyable={false}
      />
    )
  }

  const ips = allowIps
    .split('\n')
    .map((ip) => ip.trim())
    .filter(Boolean)

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span>
          <StatusBadge
            label={t('{{count}} IP(s)', { count: ips.length })}
            variant='neutral'
            copyable={false}
          />
        </span>
      </TooltipTrigger>
      <TooltipContent side='top' className='max-w-xs'>
        <div className='max-h-[200px] space-y-0.5 overflow-y-auto text-xs'>
          {ips.map((ip) => (
            <div key={ip} className='font-mono'>
              {ip}
            </div>
          ))}
        </div>
      </TooltipContent>
    </Tooltip>
  )
}

export function GroupCell({
  apiKey,
  ratio,
}: {
  apiKey: ApiKey
  ratio?: number
}) {
  const { t } = useTranslation()
  const { setOpen, setCurrentRow } = useApiKeys()
  const group = (apiKey.group ?? '').trim()
  const isAuto = group === 'auto'

  const handleClick = () => {
    setCurrentRow(apiKey)
    setOpen('switch-group')
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type='button'
          onClick={handleClick}
          aria-label={t('Click to switch group')}
          className='group/grp hover:bg-muted/60 hover:border-border flex w-full cursor-pointer items-center justify-between gap-1.5 rounded-md border border-dashed border-muted-foreground/30 px-2 py-1 transition-colors hover:border-solid'
        >
          <span className='inline-flex min-w-0 items-center gap-1.5 text-xs'>
            {isAuto ? (
              <>
                <GroupBadge group='auto' />
                {apiKey.cross_group_retry && (
                  <>
                    <span className='text-muted-foreground/30'>·</span>
                    <span className='text-muted-foreground/60'>
                      {t('Cross-group')}
                    </span>
                  </>
                )}
              </>
            ) : (
              <GroupBadge group={group} />
            )}
          </span>
          <span className='inline-flex w-20 shrink-0 items-center justify-end gap-1.5'>
            {!isAuto && typeof ratio === 'number' && (
              <GroupRatioBadge ratio={ratio} className='w-14 justify-center' />
            )}
            <ChevronDown className='text-muted-foreground/50 group-hover/grp:text-muted-foreground size-3.5 shrink-0 transition-colors' />
          </span>
        </button>
      </TooltipTrigger>
      <TooltipContent className='max-w-xs'>
        {isAuto ? (
          <>
            <span className='block text-xs'>
              {t(
                'Automatically selects the best available group with circuit breaker mechanism'
              )}
            </span>
            <span className='text-muted-foreground mt-0.5 block text-[11px]'>
              {t('Click to switch group')}
            </span>
          </>
        ) : (
          <span className='text-xs'>{t('Click to switch group')}</span>
        )}
      </TooltipContent>
    </Tooltip>
  )
}

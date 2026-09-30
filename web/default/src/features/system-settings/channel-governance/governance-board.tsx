import { useEffect, useState } from 'react'
import type { TFunction } from 'i18next'
import { useLocation } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import type { ChannelGovernanceSettings } from '../types'
import {
  GOVERNANCE_OPEN_BLOCK,
  GOVERNANCE_TABS,
  openGovernanceBlock,
  tabFromHash,
  type GovernanceOpenDetail,
  type GovernanceTabId,
} from './blocks'
import { getChannelGovernanceSectionContent } from './section-registry.tsx'

const TAB_TITLE: Record<GovernanceTabId, string> = {
  overview: 'Governance Overview',
  routing: 'Routing & Capacity',
  affinity: 'Channel Affinity',
  retry: 'Retry & Timeout',
  'response-quality': 'Response Quality',
  degradation: 'Degradation & Stopping',
  removal: 'Model-Level Removal',
  probes: 'Scheduled Probes',
}

function onOff(t: TFunction, value: boolean) {
  return value ? t('On') : t('Off')
}

export function GovernanceBoard({
  settings,
}: {
  settings: ChannelGovernanceSettings
}) {
  const { t } = useTranslation()
  const locationHash = useLocation({ select: (location) => location.hash })
  const fromRouter = tabFromHash(locationHash ?? '')
  const [local, setLocal] = useState<GovernanceTabId | null>(null)
  const [seenHash, setSeenHash] = useState(locationHash)
  if (locationHash !== seenHash) {
    setSeenHash(locationHash)
    setLocal(null)
  }
  const tab = local ?? fromRouter ?? 'overview'

  const select = (next: GovernanceTabId) => {
    setLocal(next)
    const hash = `#${next}`
    if (window.location.hash !== hash) {
      window.history.replaceState(
        null,
        '',
        `${window.location.pathname}${window.location.search}${hash}`
      )
    }
  }

  useEffect(() => {
    const onOpen = (event: Event) => {
      const detail = (event as CustomEvent<GovernanceOpenDetail>).detail
      if (!detail?.tab) return
      setLocal(detail.tab)
    }
    window.addEventListener(GOVERNANCE_OPEN_BLOCK, onOpen)
    return () => window.removeEventListener(GOVERNANCE_OPEN_BLOCK, onOpen)
  }, [])

  return (
    <div className='space-y-4 [&_h4]:font-semibold [&_h4]:text-primary'>
      <div className='space-y-1 border-b pb-3'>
        <p className='text-primary text-xs font-medium'>
          {t('Channel Governance')}
        </p>
        <h2 className='text-foreground text-xl font-semibold tracking-tight'>
          {t('How requests run')}
        </h2>
        <p className='text-muted-foreground text-sm'>
          {t(
            'One tab is one function, and that tab has the only save button for its settings.'
          )}
        </p>
      </div>

      <div className='flex flex-wrap items-center gap-2'>
        <StatusChip
          label={t('Scheduled probing')}
          value={onOff(t, settings['monitor_setting.auto_test_channel_enabled'])}
          on={settings['monitor_setting.auto_test_channel_enabled']}
          onClick={() => openGovernanceBlock('probes')}
        />
        <StatusChip
          label={t('Channel health (live traffic)')}
          value={onOff(t, settings['channel_health_setting.enabled'])}
          on={settings['channel_health_setting.enabled']}
          onClick={() => openGovernanceBlock('degradation')}
        />
        <StatusChip
          label={t('Model-level removal')}
          value={onOff(t, settings.ModelMissingRemovalEnabled)}
          on={settings.ModelMissingRemovalEnabled}
          onClick={() => openGovernanceBlock('removal')}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Each chip opens the tab that owns that switch. Status codes and keywords are on the same tab.'
          )}
        </p>
      </div>

      <Tabs value={tab} onValueChange={(value) => select(value as GovernanceTabId)}>
        <TabsList className='flex h-auto w-full flex-wrap justify-start gap-1'>
          {GOVERNANCE_TABS.map((id) => (
            <TabsTrigger key={id} value={id} className='flex-none'>
              {t(TAB_TITLE[id])}
            </TabsTrigger>
          ))}
        </TabsList>
        {GOVERNANCE_TABS.map((id) => (
          <TabsContent
            key={id}
            value={id}
            forceMount
            className='data-[state=inactive]:hidden'
          >
            {getChannelGovernanceSectionContent(id, settings)}
          </TabsContent>
        ))}
      </Tabs>
    </div>
  )
}

function StatusChip({
  label,
  value,
  on,
  onClick,
}: {
  label: string
  value: string
  on: boolean
  onClick: () => void
}) {
  return (
    <button
      type='button'
      onClick={onClick}
      className={
        on
          ? 'border-primary/40 bg-primary/10 text-primary hover:bg-primary/15 rounded-md border px-2.5 py-1 text-sm font-medium'
          : 'text-muted-foreground bg-muted/50 hover:bg-muted rounded-md border px-2.5 py-1 text-sm'
      }
    >
      {label} · {value}
    </button>
  )
}

import { useParams } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { parseCurrencyDisplayType } from '@/lib/currency'
import { useSystemOptions, getOptionValue } from '../hooks/use-system-options'
import type { GeneralSettings } from '../types'
import {
  GENERAL_DEFAULT_SECTION,
  getGeneralSectionContent,
} from './section-registry.tsx'

const defaultGeneralSettings: GeneralSettings = {
  'theme.frontend': 'default',
  Notice: '',
  SystemName: 'New API',
  Logo: '',
  Footer: '',
  About: '',
  HomePageContent: '',
  ServerAddress: '',
  'legal.user_agreement': '',
  'legal.privacy_policy': '',
  QuotaForNewUser: 0,
  PreConsumedQuota: 0,
  QuotaForInviter: 0,
  QuotaForInvitee: 0,
  RewardInviterOnEffectiveOnly: false,
  EffectiveInviteeConsumeThreshold: 0,
  AffiliateCommissionEnabled: false,
  AffiliateCommissionRatio: 0,
  TopUpLink: '',
  'general_setting.docs_link': '',
  'quota_setting.enable_free_model_pre_consume': true,
  'quota_setting.billing_refund_when_no_output': true,
  'quota_setting.refund_no_output_client_gone_min_seconds': 60,
  QuotaPerUnit: 500000,
  USDExchangeRate: 7,
  'general_setting.quota_display_type': 'USD',
  'general_setting.custom_currency_symbol': '¤',
  'general_setting.custom_currency_exchange_rate': 1,
  RetryTimes: 0,
  DisplayInCurrencyEnabled: true,
  DisplayTokenStatEnabled: true,
  DefaultCollapseSidebar: false,
  DemoSiteEnabled: false,
  SelfUseModeEnabled: false,
  'checkin_setting.enabled': false,
  'checkin_setting.min_quota': 1000,
  'checkin_setting.max_quota': 10000,
  'channel_affinity_setting.enabled': false,
  'channel_affinity_setting.switch_on_success': true,
  'channel_affinity_setting.max_entries': 100000,
  'channel_affinity_setting.default_ttl_seconds': 3600,
  'channel_affinity_setting.rules': '[]',
  'relay_retry_setting.total_timeout_seconds': 0,
  'relay_retry_setting.backoff_base_ms': 0,
  'relay_retry_setting.backoff_max_ms': 2000,
  'relay_retry_setting.model_timeouts': '',
  'url_health_setting.fail_threshold': 3,
  'url_health_setting.cooldown_seconds': 60,
  'url_health_setting.ewma_alpha': 0.2,
  'url_health_setting.hysteresis_ratio': 0.2,
  'url_health_setting.hysteresis_min_ms': 50,
  'url_health_setting.exploration_gap_seconds': 30,
  'channel_routing_setting.mode': 'probabilistic',
  'channel_routing_setting.capacity_window_sec': 60,
  'channel_routing_setting.full_strategy': 'fallback',
  'channel_routing_setting.fail_mode': 'fail_open',
  'channel_routing_setting.dry_run': false,
  'channel_routing_setting.dry_run_sample_rate': 0.01,
  'channel_routing_setting.queue_max_wait_ms': 30000,
  'channel_routing_setting.queue_poll_interval_ms': 500,
  'channel_verify_setting.auto_verify_enabled': false,
  'channel_verify_setting.global_interval_minutes': 360,
  'channel_verify_setting.score_drop_threshold': 5,
  'channel_verify_setting.notify_on_failure': false,
  'channel_verify_setting.scheduler_tick_minutes': 30,
}

export function GeneralSettings() {
  const { t } = useTranslation()
  const { data, isLoading } = useSystemOptions()
  const params = useParams({
    from: '/_authenticated/system-settings/general/$section',
  })

  if (isLoading) {
    return (
      <div className='flex items-center justify-center py-12'>
        <div className='text-muted-foreground'>{t('Loading settings...')}</div>
      </div>
    )
  }

  const settings = getOptionValue(data?.data, defaultGeneralSettings)
  const quotaDisplayType = parseCurrencyDisplayType(
    settings['general_setting.quota_display_type']
  )
  const activeSection = (params?.section ?? GENERAL_DEFAULT_SECTION) as
    | 'system-info'
    | 'quota'
    | 'pricing'
    | 'checkin'
    | 'behavior'
    | 'channel-affinity'
    | 'relay-retry'
    | 'channel-routing'
    | 'channel-verify'
  const sectionContent = getGeneralSectionContent(
    activeSection,
    settings,
    quotaDisplayType
  )

  return (
    <div className='flex h-full w-full flex-1 flex-col'>
      <div className='faded-bottom h-full w-full overflow-y-auto scroll-smooth pe-4 pb-12'>
        <div className='space-y-4'>{sectionContent}</div>
      </div>
    </div>
  )
}

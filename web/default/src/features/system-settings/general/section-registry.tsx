import type { GeneralSettings } from '../types'
import { createSectionRegistry } from '../utils/section-registry'
import { ChannelAffinitySection } from './channel-affinity'
import { ChannelRoutingSection } from './channel-routing-section'
import { ChannelVerifySection } from './channel-verify-section'
import { CheckinSettingsSection } from './checkin-settings-section'
import { PricingSection } from './pricing-section'
import { QuotaSettingsSection } from './quota-settings-section'
import { RelayRetrySection } from './relay-retry-section'
import { UrlHealthSection } from './url-health-section'
import { SystemBehaviorSection } from './system-behavior-section'
import { SystemInfoSection } from './system-info-section'

const GENERAL_SECTIONS = [
  {
    id: 'system-info',
    titleKey: 'System Information',
    descriptionKey: 'Configure basic system information and branding',
    build: (settings: GeneralSettings) => (
      <SystemInfoSection
        defaultValues={{
          theme: {
            frontend: settings['theme.frontend'] as 'default' | 'classic',
          },
          Notice: settings.Notice,
          SystemName: settings.SystemName,
          Logo: settings.Logo,
          Footer: settings.Footer,
          About: settings.About,
          HomePageContent: settings.HomePageContent,
          ServerAddress: settings.ServerAddress,
          legal: {
            user_agreement: settings['legal.user_agreement'],
            privacy_policy: settings['legal.privacy_policy'],
          },
        }}
      />
    ),
  },
  {
    id: 'quota',
    titleKey: 'Quota Settings',
    descriptionKey: 'Configure user quota allocation and rewards',
    build: (settings: GeneralSettings) => (
      <QuotaSettingsSection
        defaultValues={{
          QuotaForNewUser: settings.QuotaForNewUser,
          PreConsumedQuota: settings.PreConsumedQuota,
          QuotaForInviter: settings.QuotaForInviter,
          QuotaForInvitee: settings.QuotaForInvitee,
          RewardInviterOnEffectiveOnly: settings.RewardInviterOnEffectiveOnly,
          EffectiveInviteeConsumeThreshold:
            settings.EffectiveInviteeConsumeThreshold,
          AffiliateCommissionEnabled: settings.AffiliateCommissionEnabled,
          AffiliateCommissionRatio: settings.AffiliateCommissionRatio,
          TopUpLink: settings.TopUpLink,
          general_setting: {
            docs_link: settings['general_setting.docs_link'],
          },
          quota_setting: {
            enable_free_model_pre_consume:
              settings['quota_setting.enable_free_model_pre_consume'],
            billing_refund_when_no_output:
              settings['quota_setting.billing_refund_when_no_output'],
            refund_no_output_client_gone_min_seconds:
              settings['quota_setting.refund_no_output_client_gone_min_seconds'],
          },
        }}
      />
    ),
  },
  {
    id: 'pricing',
    titleKey: 'Pricing & Display',
    descriptionKey: 'Configure pricing model and display options',
    build: (
      settings: GeneralSettings,
      quotaDisplayType: 'USD' | 'CNY' | 'TOKENS' | 'CUSTOM'
    ) => (
      <PricingSection
        defaultValues={{
          QuotaPerUnit: settings.QuotaPerUnit,
          USDExchangeRate: settings.USDExchangeRate,
          DisplayInCurrencyEnabled: settings.DisplayInCurrencyEnabled,
          DisplayTokenStatEnabled: settings.DisplayTokenStatEnabled,
          general_setting: {
            quota_display_type: quotaDisplayType,
            custom_currency_symbol:
              settings['general_setting.custom_currency_symbol'] ?? '¤',
            custom_currency_exchange_rate:
              settings['general_setting.custom_currency_exchange_rate'] ?? 1,
          },
        }}
      />
    ),
  },
  {
    id: 'checkin',
    titleKey: 'Check-in Settings',
    descriptionKey: 'Configure daily check-in rewards for users',
    build: (settings: GeneralSettings) => (
      <CheckinSettingsSection
        defaultValues={{
          enabled: settings['checkin_setting.enabled'],
          minQuota: settings['checkin_setting.min_quota'],
          maxQuota: settings['checkin_setting.max_quota'],
        }}
      />
    ),
  },
  {
    id: 'behavior',
    titleKey: 'System Behavior',
    descriptionKey: 'Configure system-wide behavior and defaults',
    build: (settings: GeneralSettings) => (
      <SystemBehaviorSection
        defaultValues={{
          RetryTimes: settings.RetryTimes,
          DefaultCollapseSidebar: settings.DefaultCollapseSidebar,
          DemoSiteEnabled: settings.DemoSiteEnabled,
          SelfUseModeEnabled: settings.SelfUseModeEnabled,
        }}
      />
    ),
  },
  {
    id: 'channel-affinity',
    titleKey: 'Channel Affinity',
    descriptionKey: 'Configure channel affinity (sticky routing) rules',
    build: (settings: GeneralSettings) => (
      <ChannelAffinitySection
        defaultValues={{
          'channel_affinity_setting.enabled':
            settings['channel_affinity_setting.enabled'],
          'channel_affinity_setting.switch_on_success':
            settings['channel_affinity_setting.switch_on_success'],
          'channel_affinity_setting.max_entries':
            settings['channel_affinity_setting.max_entries'],
          'channel_affinity_setting.default_ttl_seconds':
            settings['channel_affinity_setting.default_ttl_seconds'],
          'channel_affinity_setting.rules':
            settings['channel_affinity_setting.rules'],
        }}
      />
    ),
  },
  {
    id: 'relay-retry',
    titleKey: 'Relay Retry & Timeout',
    descriptionKey:
      'Configure per-request deadline, exponential backoff and per-model timeout',
    build: (settings: GeneralSettings) => (
      <RelayRetrySection
        defaultValues={{
          'relay_retry_setting.total_timeout_seconds':
            settings['relay_retry_setting.total_timeout_seconds'],
          'relay_retry_setting.backoff_base_ms':
            settings['relay_retry_setting.backoff_base_ms'],
          'relay_retry_setting.backoff_max_ms':
            settings['relay_retry_setting.backoff_max_ms'],
          'relay_retry_setting.model_timeouts':
            settings['relay_retry_setting.model_timeouts'],
        }}
      />
    ),
  },
  {
    id: 'url-health',
    titleKey: 'Multi Base-URL Failover',
    descriptionKey:
      'Circuit breaker and fastest-routing thresholds for channels with backup base URLs',
    build: (settings: GeneralSettings) => (
      <UrlHealthSection
        defaultValues={{
          'url_health_setting.fail_threshold':
            settings['url_health_setting.fail_threshold'],
          'url_health_setting.cooldown_seconds':
            settings['url_health_setting.cooldown_seconds'],
          'url_health_setting.ewma_alpha':
            settings['url_health_setting.ewma_alpha'],
          'url_health_setting.hysteresis_ratio':
            settings['url_health_setting.hysteresis_ratio'],
          'url_health_setting.hysteresis_min_ms':
            settings['url_health_setting.hysteresis_min_ms'],
          'url_health_setting.exploration_gap_seconds':
            settings['url_health_setting.exploration_gap_seconds'],
        }}
      />
    ),
  },
  {
    id: 'channel-routing',
    titleKey: 'Channel Routing Mode',
    descriptionKey:
      'Switch between probabilistic (weighted random) and capacity (bucket overflow) routing',
    build: (settings: GeneralSettings) => (
      <ChannelRoutingSection
        defaultValues={{
          'channel_routing_setting.mode':
            settings['channel_routing_setting.mode'],
          'channel_routing_setting.capacity_window_sec':
            settings['channel_routing_setting.capacity_window_sec'],
          'channel_routing_setting.full_strategy':
            settings['channel_routing_setting.full_strategy'],
          'channel_routing_setting.fail_mode':
            settings['channel_routing_setting.fail_mode'],
          'channel_routing_setting.dry_run':
            settings['channel_routing_setting.dry_run'],
          'channel_routing_setting.dry_run_sample_rate':
            settings['channel_routing_setting.dry_run_sample_rate'],
          'channel_routing_setting.queue_max_wait_ms':
            settings['channel_routing_setting.queue_max_wait_ms'],
          'channel_routing_setting.queue_poll_interval_ms':
            settings['channel_routing_setting.queue_poll_interval_ms'],
        }}
      />
    ),
  },
  {
    id: 'channel-verify',
    titleKey: 'Channel Verify Schedule',
    descriptionKey: 'Schedule external verification and alert on score drops',
    build: (settings: GeneralSettings) => (
      <ChannelVerifySection
        defaultValues={{
          'channel_verify_setting.auto_verify_enabled':
            settings['channel_verify_setting.auto_verify_enabled'],
          'channel_verify_setting.global_interval_minutes':
            settings['channel_verify_setting.global_interval_minutes'],
          'channel_verify_setting.score_drop_threshold':
            settings['channel_verify_setting.score_drop_threshold'],
          'channel_verify_setting.notify_on_failure':
            settings['channel_verify_setting.notify_on_failure'],
          'channel_verify_setting.scheduler_tick_minutes':
            settings['channel_verify_setting.scheduler_tick_minutes'],
        }}
      />
    ),
  },
] as const

export type GeneralSectionId = (typeof GENERAL_SECTIONS)[number]['id']

const generalRegistry = createSectionRegistry<
  GeneralSectionId,
  GeneralSettings,
  ['USD' | 'CNY' | 'TOKENS' | 'CUSTOM']
>({
  sections: GENERAL_SECTIONS,
  defaultSection: 'system-info',
  basePath: '/system-settings/general',
  urlStyle: 'path',
})

export const GENERAL_SECTION_IDS = generalRegistry.sectionIds
export const GENERAL_DEFAULT_SECTION = generalRegistry.defaultSection
export const getGeneralSectionNavItems = generalRegistry.getSectionNavItems
export const getGeneralSectionContent = generalRegistry.getSectionContent

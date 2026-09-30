import type { ChannelGovernanceSettings } from '../types'
import { createSectionRegistry } from '../utils/section-registry'
import { ChannelAffinitySection } from './channel-affinity'
import { DegradationSection } from './degradation-section'
import { GovernanceOverviewSection } from './overview-section'
import { ScheduledProbesSection } from './probes-section'
import { ModelRemovalSection } from './removal-section'
import { ResponseQualitySection } from './response-quality-section'
import { RetrySection } from './retry-section'
import { RoutingSection } from './routing-section'

const CHANNEL_GOVERNANCE_SECTIONS = [
  {
    id: 'overview',
    titleKey: 'Governance Overview',
    descriptionKey:
      'The three switches that decide whether anything touches your channels unattended',
    build: (settings: ChannelGovernanceSettings) => (
      <GovernanceOverviewSection
        context={{
          autoTestEnabled:
            settings['monitor_setting.auto_test_channel_enabled'],
          healthEnabled: settings['channel_health_setting.enabled'],
          modelMissingRemovalEnabled: settings.ModelMissingRemovalEnabled,
          autoTestMinutes:
            settings['monitor_setting.auto_test_channel_minutes'],
          automaticDisableChannelEnabled:
            settings.AutomaticDisableChannelEnabled,
          automaticEnableChannelEnabled: settings.AutomaticEnableChannelEnabled,
          disableThreshold:
            settings['channel_health_setting.disable_threshold'],
          baseDegradeThreshold:
            settings['channel_health_setting.base_degrade_threshold'],
          levelStepThreshold:
            settings['channel_health_setting.level_step_threshold'],
          maxDegradeLevel: settings['channel_health_setting.max_degrade_level'],
          count429AsError:
            settings['channel_health_setting.count_429_as_error'],
          recoveryStrategy:
            settings['channel_health_setting.recovery_strategy'],
          modelRateLimitRemovalEnabled: settings.ModelRateLimitRemovalEnabled,
          modelForbiddenRemovalEnabled: settings.ModelForbiddenRemovalEnabled,
          degradeProbeEnabled:
            settings['channel_health_setting.degrade_probe_enabled'],
          rebounceProtectionMinutes:
            settings['channel_health_setting.rebounce_protection_minutes'],
        }}
      />
    ),
  },
  {
    id: 'probes',
    titleKey: 'Scheduled Probes',
    descriptionKey:
      'The four timers that send requests on their own: all-channel testing, degrade probing, recovery probing and channel verification',
    build: (settings: ChannelGovernanceSettings) => (
      <ScheduledProbesSection
        defaultValues={{
          ChannelDisableThreshold: settings.ChannelDisableThreshold,
          AutomaticDisableChannelEnabled:
            settings.AutomaticDisableChannelEnabled,
          AutomaticEnableChannelEnabled: settings.AutomaticEnableChannelEnabled,
          AutomaticDisableStatusCodes: settings.AutomaticDisableStatusCodes,
          AutomaticDisableKeywords: settings.AutomaticDisableKeywords,
          'monitor_setting.auto_test_channel_enabled':
            settings['monitor_setting.auto_test_channel_enabled'],
          'monitor_setting.auto_test_channel_minutes':
            settings['monitor_setting.auto_test_channel_minutes'],
          'channel_health_setting.degrade_probe_enabled':
            settings['channel_health_setting.degrade_probe_enabled'],
          'channel_health_setting.degrade_probe_min_level':
            settings['channel_health_setting.degrade_probe_min_level'],
          'channel_health_setting.degrade_probe_minutes':
            settings['channel_health_setting.degrade_probe_minutes'],
          'channel_health_setting.degrade_probe_count':
            settings['channel_health_setting.degrade_probe_count'],
          'channel_health_setting.recovery_strategy':
            settings['channel_health_setting.recovery_strategy'],
          'channel_health_setting.recovery_probe_minutes':
            settings['channel_health_setting.recovery_probe_minutes'],
          'channel_verify_setting.auto_verify_enabled':
            settings['channel_verify_setting.auto_verify_enabled'],
          'channel_verify_setting.global_interval_minutes':
            settings['channel_verify_setting.global_interval_minutes'],
          'channel_verify_setting.scheduler_tick_minutes':
            settings['channel_verify_setting.scheduler_tick_minutes'],
          'channel_verify_setting.score_drop_threshold':
            settings['channel_verify_setting.score_drop_threshold'],
          'channel_verify_setting.notify_on_failure':
            settings['channel_verify_setting.notify_on_failure'],
        }}
      />
    ),
  },
  {
    id: 'degradation',
    titleKey: 'Degradation & Stopping',
    descriptionKey:
      'What live traffic does to a channel: how far it slides down the ladder, and when it is taken out entirely',
    build: (settings: ChannelGovernanceSettings) => (
      <DegradationSection
        defaultValues={{
          'channel_health_setting.enabled':
            settings['channel_health_setting.enabled'],
          'channel_health_setting.countable_status_codes':
            settings['channel_health_setting.countable_status_codes'],
          'channel_health_setting.count_429_as_error':
            settings['channel_health_setting.count_429_as_error'],
          'channel_health_setting.base_degrade_threshold':
            settings['channel_health_setting.base_degrade_threshold'],
          'channel_health_setting.level_step_threshold':
            settings['channel_health_setting.level_step_threshold'],
          'channel_health_setting.max_degrade_level':
            settings['channel_health_setting.max_degrade_level'],
          'channel_health_setting.min_weight_factor':
            settings['channel_health_setting.min_weight_factor'],
          'channel_health_setting.upgrade_threshold':
            settings['channel_health_setting.upgrade_threshold'],
          'channel_health_setting.streak_window_sec':
            settings['channel_health_setting.streak_window_sec'],
          'channel_health_setting.demote_cooldown_sec':
            settings['channel_health_setting.demote_cooldown_sec'],
          'channel_health_setting.disable_threshold':
            settings['channel_health_setting.disable_threshold'],
          'channel_health_setting.max_ttft_ms':
            settings['channel_health_setting.max_ttft_ms'],
          'channel_health_setting.latency_degrade_base':
            settings['channel_health_setting.latency_degrade_base'],
          'channel_health_setting.latency_degrade_step':
            settings['channel_health_setting.latency_degrade_step'],
          'channel_health_setting.count_latency_as_error':
            settings['channel_health_setting.count_latency_as_error'],
          'channel_health_setting.rebounce_protection_minutes':
            settings['channel_health_setting.rebounce_protection_minutes'],
          'channel_health_setting.rebounce_protection_threshold':
            settings['channel_health_setting.rebounce_protection_threshold'],
          'channel_health_setting.notify_on_degrade':
            settings['channel_health_setting.notify_on_degrade'],
          'channel_health_setting.notify_on_upgrade':
            settings['channel_health_setting.notify_on_upgrade'],
        }}
      />
    ),
  },
  {
    id: 'removal',
    titleKey: 'Model-Level Removal',
    descriptionKey:
      'Take one model out of a channel instead of stopping the whole channel',
    build: (settings: ChannelGovernanceSettings) => (
      <ModelRemovalSection
        defaultValues={{
          ModelMissingRemovalEnabled: settings.ModelMissingRemovalEnabled,
          ModelMissingKeywords: settings.ModelMissingKeywords,
          ModelMissingRemovalCooldownSeconds:
            settings.ModelMissingRemovalCooldownSeconds,
          ModelMissingRecheckIntervalSeconds:
            settings.ModelMissingRecheckIntervalSeconds,
          ModelRateLimitRemovalEnabled: settings.ModelRateLimitRemovalEnabled,
          ModelRateLimitRecheckIntervalSeconds:
            settings.ModelRateLimitRecheckIntervalSeconds,
          ModelForbiddenRemovalEnabled: settings.ModelForbiddenRemovalEnabled,
          ModelForbiddenStatusCodes: settings.ModelForbiddenStatusCodes,
          ModelForbiddenKeywords: settings.ModelForbiddenKeywords,
          ModelForbiddenRecheckIntervalSeconds:
            settings.ModelForbiddenRecheckIntervalSeconds,
          ModelRemovalMaxRemovedPerChannel:
            settings.ModelRemovalMaxRemovedPerChannel,
          ModelRemovalConsecutiveThreshold:
            settings.ModelRemovalConsecutiveThreshold,
          ModelRemovalCapAction: settings.ModelRemovalCapAction,
          ModelMissingRemovalCapEnabled: settings.ModelMissingRemovalCapEnabled,
          ModelRemovalNotifyEnabled: settings.ModelRemovalNotifyEnabled,
        }}
        disableKeywords={settings.AutomaticDisableKeywords}
      />
    ),
  },
  {
    id: 'response-quality',
    titleKey: 'Response Quality',
    descriptionKey:
      'Intercept apology or very short replies and return the HTTP status and message configured here. With apply-all on, the global switch is enough; turn it off to require the matching channel switch as well.',
    build: (settings: ChannelGovernanceSettings) => (
      <ResponseQualitySection
        defaultValues={{
          'response_quality_setting.block_apology_enabled':
            settings['response_quality_setting.block_apology_enabled'],
          'response_quality_setting.apology_status_code':
            settings['response_quality_setting.apology_status_code'],
          'response_quality_setting.apology_message':
            settings['response_quality_setting.apology_message'],
          'response_quality_setting.apology_keywords':
            settings['response_quality_setting.apology_keywords'],
          'response_quality_setting.apply_all_channels':
            settings['response_quality_setting.apply_all_channels'],
          'response_quality_setting.block_low_token_enabled':
            settings['response_quality_setting.block_low_token_enabled'],
          'response_quality_setting.low_token_threshold':
            settings['response_quality_setting.low_token_threshold'],
          'response_quality_setting.low_token_status_code':
            settings['response_quality_setting.low_token_status_code'],
          'response_quality_setting.low_token_message':
            settings['response_quality_setting.low_token_message'],
          'response_quality_setting.retry_on_block':
            settings['response_quality_setting.retry_on_block'],
        }}
      />
    ),
  },
  {
    id: 'retry',
    titleKey: 'Retry & Timeout',
    descriptionKey:
      'How many times a failed request is retried, how long the whole attempt may take, and when a replay the client already gave up on is refused outright',
    build: (settings: ChannelGovernanceSettings) => (
      <RetrySection
        defaultValues={{
          RetryTimes: settings.RetryTimes,
          AutomaticRetryStatusCodes: settings.AutomaticRetryStatusCodes,
          'relay_retry_setting.total_timeout_seconds':
            settings['relay_retry_setting.total_timeout_seconds'],
          'relay_retry_setting.backoff_base_ms':
            settings['relay_retry_setting.backoff_base_ms'],
          'relay_retry_setting.backoff_max_ms':
            settings['relay_retry_setting.backoff_max_ms'],
          'relay_retry_setting.model_timeouts':
            settings['relay_retry_setting.model_timeouts'],
          'retry_short_circuit_setting.enabled':
            settings['retry_short_circuit_setting.enabled'],
          'retry_short_circuit_setting.min_duration_seconds':
            settings['retry_short_circuit_setting.min_duration_seconds'],
          'retry_short_circuit_setting.ttl_minutes':
            settings['retry_short_circuit_setting.ttl_minutes'],
        }}
      />
    ),
  },
  {
    id: 'routing',
    titleKey: 'Routing & Capacity',
    descriptionKey:
      'Which channel a request is handed to: how load is spread across a priority layer, and which base URL of a channel is used',
    build: (settings: ChannelGovernanceSettings) => (
      <RoutingSection
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
    id: 'affinity',
    titleKey: 'Channel Affinity',
    descriptionKey:
      'Keep a client on the same channel when a rule matches, instead of picking again on every request',
    build: (settings: ChannelGovernanceSettings) => (
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
] as const

export type ChannelGovernanceSectionId =
  (typeof CHANNEL_GOVERNANCE_SECTIONS)[number]['id']

const channelGovernanceRegistry = createSectionRegistry<
  ChannelGovernanceSectionId,
  ChannelGovernanceSettings
>({
  sections: CHANNEL_GOVERNANCE_SECTIONS,
  defaultSection: 'overview',
  basePath: '/system-settings/channel-governance',
  urlStyle: 'path',
})

export const CHANNEL_GOVERNANCE_SECTION_IDS =
  channelGovernanceRegistry.sectionIds
export const CHANNEL_GOVERNANCE_DEFAULT_SECTION =
  channelGovernanceRegistry.defaultSection
export const getChannelGovernanceSectionNavItems =
  channelGovernanceRegistry.getSectionNavItems
export const getChannelGovernanceSectionContent =
  channelGovernanceRegistry.getSectionContent

import { useTranslation } from 'react-i18next'
import { getOptionValue, useSystemOptions } from '../hooks/use-system-options'
import type { ChannelGovernanceSettings } from '../types'
import { GovernanceBoard } from './governance-board'

/**
 * 默认值必须与后端一致：setting/operation_setting/{monitor_setting,channel_health_setting}.go、
 * setting/operation_setting/{channel_verify_setting,channel_affinity_setting,response_quality_setting}.go、
 * setting/operation_setting/model_missing.go 与 common/constants.go。
 */
const defaultChannelGovernanceSettings: ChannelGovernanceSettings = {
  'monitor_setting.auto_test_channel_enabled': false,
  'monitor_setting.auto_test_channel_minutes': 10,
  'channel_health_setting.enabled': false,
  'channel_health_setting.base_degrade_threshold': 5,
  'channel_health_setting.level_step_threshold': 5,
  'channel_health_setting.max_degrade_level': 10,
  'channel_health_setting.min_weight_factor': 0.05,
  'channel_health_setting.upgrade_threshold': 20,
  'channel_health_setting.streak_window_sec': 600,
  'channel_health_setting.demote_cooldown_sec': 60,
  'channel_health_setting.disable_threshold': 0,
  'channel_health_setting.max_ttft_ms': 0,
  'channel_health_setting.latency_degrade_base': 5,
  'channel_health_setting.latency_degrade_step': 5,
  'channel_health_setting.count_latency_as_error': false,
  'channel_health_setting.rebounce_protection_minutes': 0,
  'channel_health_setting.rebounce_protection_threshold': 3,
  'channel_health_setting.notify_on_degrade': false,
  'channel_health_setting.notify_on_upgrade': false,
  'channel_health_setting.count_429_as_error': true,
  'channel_health_setting.countable_status_codes': '',
  'channel_health_setting.degrade_probe_enabled': true,
  'channel_health_setting.degrade_probe_min_level': 1,
  'channel_health_setting.degrade_probe_minutes': 10,
  'channel_health_setting.degrade_probe_count': 5,
  'channel_health_setting.recovery_strategy': 'probe',
  'channel_health_setting.recovery_probe_minutes': 30,
  ChannelDisableThreshold: '',
  AutomaticDisableChannelEnabled: false,
  AutomaticEnableChannelEnabled: false,
  AutomaticDisableKeywords: '',
  AutomaticDisableStatusCodes: '401',
  AutomaticRetryStatusCodes:
    '100-199,300-399,401-407,409-499,500-503,505-523,525-599',
  ModelMissingKeywords: '',
  ModelMissingRemovalEnabled: true,
  ModelMissingRemovalCooldownSeconds: 60,
  ModelMissingRecheckIntervalSeconds: 600,
  ModelRateLimitRemovalEnabled: false,
  ModelRateLimitRecheckIntervalSeconds: 180,
  // 与后端 setting/operation_setting/model_missing.go 的 ModelForbiddenKeywords 保持一致：
  // 库里没有该键时界面必须显示后端实际生效的那几条，否则用户看到空白、一保存就把它们清掉了
  ModelForbiddenKeywords:
    'Permission denied\ndoes not have access to model\nOperation not allowed\nmodel not supported\nunsupported model',
  ModelForbiddenStatusCodes: '400,403',
  ModelForbiddenRemovalEnabled: false,
  ModelForbiddenRecheckIntervalSeconds: 1800,
  ModelRemovalMaxRemovedPerChannel: 5,
  ModelRemovalConsecutiveThreshold: 10,
  ModelRemovalCapAction: 'alert_only',
  ModelMissingRemovalCapEnabled: false,
  // 与后端 operation_setting.ModelRemovalNotifyEnabled 一致：默认 false = 上线前行为
  // （改造前只有停用渠道会发通知，摘除路径从来不发）
  ModelRemovalNotifyEnabled: false,
  'channel_verify_setting.auto_verify_enabled': false,
  'channel_verify_setting.global_interval_minutes': 360,
  'channel_verify_setting.score_drop_threshold': 5,
  'channel_verify_setting.notify_on_failure': false,
  'channel_verify_setting.scheduler_tick_minutes': 30,
  RetryTimes: 0,
  'relay_retry_setting.total_timeout_seconds': 0,
  'relay_retry_setting.backoff_base_ms': 0,
  'relay_retry_setting.backoff_max_ms': 2000,
  'relay_retry_setting.model_timeouts': '',
  'retry_short_circuit_setting.enabled': false,
  'retry_short_circuit_setting.min_duration_seconds': 300,
  'retry_short_circuit_setting.ttl_minutes': 15,
  'response_quality_setting.block_apology_enabled': false,
  'response_quality_setting.apology_status_code': 503,
  'response_quality_setting.apology_message': 'upstream apology reply blocked',
  'response_quality_setting.apology_keywords': '',
  'response_quality_setting.apply_all_channels': true,
  'response_quality_setting.block_low_token_enabled': false,
  'response_quality_setting.low_token_threshold': 300,
  'response_quality_setting.low_token_status_code': 503,
  'response_quality_setting.low_token_message':
    'upstream completion tokens {tokens} below {threshold}',
  'response_quality_setting.retry_on_block': false,
  'channel_routing_setting.mode': 'probabilistic',
  'channel_routing_setting.capacity_window_sec': 60,
  'channel_routing_setting.full_strategy': 'fallback',
  'channel_routing_setting.fail_mode': 'fail_open',
  'channel_routing_setting.dry_run': false,
  'channel_routing_setting.dry_run_sample_rate': 0.01,
  'channel_routing_setting.queue_max_wait_ms': 30000,
  'channel_routing_setting.queue_poll_interval_ms': 500,
  'url_health_setting.fail_threshold': 3,
  'url_health_setting.cooldown_seconds': 60,
  'url_health_setting.ewma_alpha': 0.2,
  'url_health_setting.hysteresis_ratio': 0.2,
  'url_health_setting.hysteresis_min_ms': 50,
  'url_health_setting.exploration_gap_seconds': 30,
  'channel_affinity_setting.enabled': false,
  'channel_affinity_setting.switch_on_success': true,
  'channel_affinity_setting.max_entries': 100000,
  'channel_affinity_setting.default_ttl_seconds': 3600,
  'channel_affinity_setting.rules': '[]',
}

export function ChannelGovernanceSettingsPage() {
  const { t } = useTranslation()
  const { data, isLoading } = useSystemOptions()

  if (isLoading) {
    return (
      <div className='flex items-center justify-center py-12'>
        <div className='text-muted-foreground'>{t('Loading settings...')}</div>
      </div>
    )
  }

  const settings = getOptionValue(
    data?.data,
    defaultChannelGovernanceSettings
  ) as ChannelGovernanceSettings

  return <GovernanceBoard settings={settings} />
}

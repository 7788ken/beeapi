export type SystemOption = {
  key: string
  value: string
}

export type SystemOptionKey = string

export type SystemOptionsResponse = {
  success: boolean
  message: string
  data: SystemOption[]
}

export type UpdateOptionRequest = {
  key: string
  value: string | boolean | number
}

export type UpdateOptionResponse = {
  success: boolean
  message: string
}

export type DeleteLogsResponse = {
  success: boolean
  message: string
  data?: number
}

export type GeneralSettings = {
  'theme.frontend': string
  Notice: string
  SystemName: string
  Logo: string
  Footer: string
  About: string
  HomePageContent: string
  ServerAddress: string
  'legal.user_agreement': string
  'legal.privacy_policy': string
  QuotaForNewUser: number
  PreConsumedQuota: number
  QuotaForInviter: number
  QuotaForInvitee: number
  RewardInviterOnEffectiveOnly: boolean
  EffectiveInviteeConsumeThreshold: number
  AffiliateCommissionEnabled: boolean
  AffiliateCommissionRatio: number
  TopUpLink: string
  'general_setting.docs_link': string
  'quota_setting.enable_free_model_pre_consume': boolean
  'quota_setting.billing_refund_when_no_output': boolean
  'quota_setting.refund_no_output_client_gone_min_seconds': number
  'quota_setting.refund_no_output_exclude_upstream_refusal': boolean
  QuotaPerUnit: number
  USDExchangeRate: number
  'general_setting.quota_display_type': string
  'general_setting.custom_currency_symbol': string
  'general_setting.custom_currency_exchange_rate': number
  RetryTimes: number
  DisplayInCurrencyEnabled: boolean
  DisplayTokenStatEnabled: boolean
  DefaultCollapseSidebar: boolean
  DemoSiteEnabled: boolean
  SelfUseModeEnabled: boolean
  'checkin_setting.enabled': boolean
  'checkin_setting.min_quota': number
  'checkin_setting.max_quota': number
  'channel_affinity_setting.enabled': boolean
  'channel_affinity_setting.switch_on_success': boolean
  'channel_affinity_setting.max_entries': number
  'channel_affinity_setting.default_ttl_seconds': number
  'channel_affinity_setting.rules': string
  'relay_retry_setting.total_timeout_seconds': number
  'relay_retry_setting.backoff_base_ms': number
  'relay_retry_setting.backoff_max_ms': number
  'relay_retry_setting.model_timeouts': string
  'url_health_setting.fail_threshold': number
  'url_health_setting.cooldown_seconds': number
  'url_health_setting.ewma_alpha': number
  'url_health_setting.hysteresis_ratio': number
  'url_health_setting.hysteresis_min_ms': number
  'url_health_setting.exploration_gap_seconds': number
  'channel_routing_setting.mode': string
  'channel_routing_setting.capacity_window_sec': number
  'channel_routing_setting.full_strategy': string
  'channel_routing_setting.fail_mode': string
  'channel_routing_setting.dry_run': boolean
  'channel_routing_setting.dry_run_sample_rate': number
  'channel_routing_setting.queue_max_wait_ms': number
  'channel_routing_setting.queue_poll_interval_ms': number
  'channel_verify_setting.auto_verify_enabled': boolean
  'channel_verify_setting.global_interval_minutes': number
  'channel_verify_setting.score_drop_threshold': number
  'channel_verify_setting.notify_on_failure': boolean
  'channel_verify_setting.scheduler_tick_minutes': number
}

export type AuthSettings = {
  PasswordLoginEnabled: boolean
  PasswordRegisterEnabled: boolean
  EmailVerificationEnabled: boolean
  RegisterEnabled: boolean
  EmailDomainRestrictionEnabled: boolean
  EmailAliasRestrictionEnabled: boolean
  EmailDomainWhitelist: string
  GitHubOAuthEnabled: boolean
  GitHubClientId: string
  GitHubClientSecret: string
  'discord.enabled': boolean
  'discord.client_id': string
  'discord.client_secret': string
  'oidc.enabled': boolean
  'oidc.client_id': string
  'oidc.client_secret': string
  'oidc.well_known': string
  'oidc.authorization_endpoint': string
  'oidc.token_endpoint': string
  'oidc.user_info_endpoint': string
  TelegramOAuthEnabled: boolean
  TelegramBotToken: string
  TelegramBotName: string
  LinuxDOOAuthEnabled: boolean
  LinuxDOClientId: string
  LinuxDOClientSecret: string
  LinuxDOMinimumTrustLevel: string
  WeChatAuthEnabled: boolean
  WeChatServerAddress: string
  WeChatServerToken: string
  WeChatAccountQRCodeImageURL: string
  TurnstileCheckEnabled: boolean
  TurnstileSiteKey: string
  TurnstileSecretKey: string
  'passkey.enabled': boolean
  'passkey.rp_display_name': string
  'passkey.rp_id': string
  'passkey.origins': string
  'passkey.allow_insecure_origin': boolean
  'passkey.user_verification': 'required' | 'preferred' | 'discouraged'
  'passkey.attachment_preference': '' | 'platform' | 'cross-platform'
}

export type ContentSettings = {
  'console_setting.api_info': string
  'console_setting.announcements': string
  'console_setting.faq': string
  'console_setting.uptime_kuma_groups': string
  'console_setting.api_info_enabled': boolean
  'console_setting.announcements_enabled': boolean
  'console_setting.faq_enabled': boolean
  'console_setting.uptime_kuma_enabled': boolean
  DataExportEnabled: boolean
  DataExportDefaultTime: string
  DataExportInterval: number
  Chats: string
  DrawingEnabled: boolean
  MjNotifyEnabled: boolean
  MjAccountFilterEnabled: boolean
  MjForwardUrlEnabled: boolean
  MjModeClearEnabled: boolean
  MjActionCheckSuccessEnabled: boolean
}

export type IntegrationSettings = {
  SMTPServer: string
  SMTPPort: string
  SMTPAccount: string
  SMTPFrom: string
  SMTPToken: string
  SMTPSSLEnabled: boolean
  SMTPForceAuthLogin: boolean
  WorkerUrl: string
  WorkerValidKey: string
  WorkerAllowHttpImageRequestEnabled: boolean
  ReconcileBalancePanelBaseURL: string
  ReconcileBalancePanelToken: string
  ChannelDisableThreshold: string
  QuotaRemindThreshold: string
  AutomaticDisableChannelEnabled: boolean
  AutomaticEnableChannelEnabled: boolean
  AutomaticDisableKeywords: string
  AutomaticDisableStatusCodes: string
  AutomaticRetryStatusCodes: string
  'monitor_setting.auto_test_channel_enabled': boolean
  'monitor_setting.auto_test_channel_minutes': number
  'channel_health_setting.enabled': boolean
  'channel_health_setting.base_degrade_threshold': number
  'channel_health_setting.level_step_threshold': number
  'channel_health_setting.max_degrade_level': number
  'channel_health_setting.min_weight_factor': number
  'channel_health_setting.max_ttft_ms': number
  'channel_health_setting.latency_degrade_base': number
  'channel_health_setting.latency_degrade_step': number
  'channel_health_setting.count_latency_as_error': boolean
  'channel_health_setting.disable_threshold': number
  'channel_health_setting.upgrade_threshold': number
  'channel_health_setting.degrade_probe_minutes': number
  'channel_health_setting.degrade_probe_count': number
  'channel_health_setting.count_429_as_error': boolean
  'channel_health_setting.countable_status_codes': string
  'channel_health_setting.notify_on_degrade': boolean
  'channel_health_setting.notify_on_upgrade': boolean
  'channel_health_setting.streak_window_sec': number
  'channel_health_setting.rebounce_protection_minutes': number
  'channel_health_setting.rebounce_protection_threshold': number
  'channel_health_setting.demote_cooldown_sec': number
  'channel_health_setting.recovery_strategy': string
  'channel_health_setting.recovery_probe_minutes': number
  'channel_health_setting.recovery_probe_model': string
  'channel_health_setting.shadow_sample_rate': number
  'channel_health_setting.skip_channel_tags': string
  'model_deployment.ionet.api_key': string
  'model_deployment.ionet.enabled': boolean
  PayAddress: string
  EpayId: string
  EpayKey: string
  Price: number
  MinTopUp: number
  CustomCallbackAddress: string
  PayMethods: string
  'payment_setting.amount_options': string
  'payment_setting.amount_discount': string
  StripeApiSecret: string
  StripeWebhookSecret: string
  StripePriceId: string
  StripeUnitPrice: number
  StripeMinTopUp: number
  StripePromotionCodesEnabled: boolean
  CreemApiKey: string
  CreemWebhookSecret: string
  CreemTestMode: boolean
  CreemProducts: string
  WaffoEnabled: boolean
  WaffoApiKey: string
  WaffoPrivateKey: string
  WaffoPublicCert: string
  WaffoSandboxPublicCert: string
  WaffoSandboxApiKey: string
  WaffoSandboxPrivateKey: string
  WaffoSandbox: boolean
  WaffoMerchantId: string
  WaffoCurrency: string
  WaffoUnitPrice: number
  WaffoMinTopUp: number
  WaffoNotifyUrl: string
  WaffoReturnUrl: string
  WaffoPayMethods: string
  WaffoAllowedGroups: string
  WaffoPancakeEnabled: boolean
  WaffoPancakeSandbox: boolean
  WaffoPancakeMerchantID: string
  WaffoPancakePrivateKey: string
  WaffoPancakeWebhookPublicKey: string
  WaffoPancakeWebhookTestKey: string
  WaffoPancakeStoreID: string
  WaffoPancakeProductID: string
  WaffoPancakeReturnURL: string
  WaffoPancakeCurrency: string
  WaffoPancakeUnitPrice: number
  WaffoPancakeMinTopUp: number
  WaffoPancakeAllowedGroups: string
  WaffoPancakePayChannels: string
  WaffoPancakeLogo: string
  CryptomusEnabled: boolean
  CryptomusMerchantID: string
  CryptomusPaymentApiKey: string
  CryptomusWebhookApiKey: string
  CryptomusDefaultCurrency: string
  CryptomusDefaultNetwork: string
  CryptomusAllowedCurrencies: string
  CryptomusUnitPrice: number
  CryptomusMinTopUp: number
  CryptomusReturnURL: string
  CryptomusAllowedGroups: string
  CryptomusPayChannels: string
  CryptomusLogo: string
  SfpayEnabled: boolean
  SfpayBaseURL: string
  SfpayAppId: string
  SfpayAppSecret: string
  SfpayGroupCode: string
  SfpayNotifyUrl: string
  SfpayReturnUrl: string
  SfpayUnitPrice: number
  SfpayMinTopUp: number
  SfpayMaxTopUp: number
  SfpayAllowedCallbackIPs: string
  SfpayAlipayPayType: string
  SfpayWechatEnabled: boolean
  SfpayWechatPayType: string
  SfpayAllowedGroups: string
  SfpayPayChannels: string
  SfpayLogo: string
}

export type ModelSettings = {
  'global.pass_through_request_enabled': boolean
  'global.thinking_model_blacklist': string
  'global.chat_completions_to_responses_policy': string
  'general_setting.ping_interval_enabled': boolean
  'general_setting.ping_interval_seconds': number
  'gemini.safety_settings': string
  'gemini.version_settings': string
  'gemini.supported_imagine_models': string
  'gemini.supported_mime_types': string
  'gemini.thinking_adapter_enabled': boolean
  'gemini.thinking_adapter_budget_tokens_percentage': number
  'gemini.function_call_thought_signature_enabled': boolean
  'gemini.remove_function_response_id_enabled': boolean
  'claude.model_headers_settings': string
  'claude.default_max_tokens': string
  'claude.thinking_adapter_enabled': boolean
  'claude.thinking_adapter_budget_tokens_percentage': number
  'retry_short_circuit_setting.enabled': boolean
  'retry_short_circuit_setting.min_duration_seconds': number
  'retry_short_circuit_setting.ttl_minutes': number
  'grok.violation_deduction_enabled': boolean
  'grok.violation_deduction_amount': number
  ModelPrice: string
  ModelRatio: string
  CacheRatio: string
  CreateCacheRatio: string
  CompletionRatio: string
  ImageRatio: string
  AudioRatio: string
  AudioCompletionRatio: string
  ExposeRatioEnabled: boolean
  'billing_setting.billing_mode': string
  'billing_setting.billing_expr': string
  'tool_price_setting.prices': string
  TopupGroupRatio: string
  GroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  DefaultUseAutoGroup: boolean
  'group_ratio_setting.group_special_usable_group': string
}

export type MaintenanceSettings = {
  Notice: string
  LogConsumeEnabled: boolean
  HeaderNavModules: string
  SidebarModulesAdmin: string
  'performance_setting.disk_cache_enabled': boolean
  'performance_setting.disk_cache_threshold_mb': number
  'performance_setting.disk_cache_max_size_mb': number
  'performance_setting.disk_cache_path': string
  'performance_setting.monitor_enabled': boolean
  'performance_setting.monitor_cpu_threshold': number
  'performance_setting.monitor_memory_threshold': number
  'performance_setting.monitor_disk_threshold': number
  'perf_metrics_setting.enabled': boolean
  'perf_metrics_setting.flush_interval': number
  'perf_metrics_setting.bucket_time': 'hour' | 'minute' | '5min'
  'perf_metrics_setting.retention_days': number
}

export type RequestLimitsSettings = {
  ModelRequestRateLimitEnabled: boolean
  ModelRequestRateLimitCount: number
  ModelRequestRateLimitSuccessCount: number
  ModelRequestRateLimitDurationMinutes: number
  ModelRequestRateLimitGroup: string
  CheckSensitiveEnabled: boolean
  CheckSensitiveOnPromptEnabled: boolean
  SensitiveWords: string
  'fetch_setting.enable_ssrf_protection': boolean
  'fetch_setting.allow_private_ip': boolean
  'fetch_setting.domain_filter_mode': boolean
  'fetch_setting.ip_filter_mode': boolean
  'fetch_setting.domain_list': string[]
  'fetch_setting.ip_list': string[]
  'fetch_setting.allowed_ports': number[]
  'fetch_setting.apply_ip_filter_for_domain': boolean
  'token_health_setting.enabled': boolean
  'token_health_setting.window_minutes': number
  'token_health_setting.min_requests': number
  'token_health_setting.error_rate_threshold': number
  'token_health_setting.cooldown_minutes': number
  'token_health_setting.excluded_status_codes': string
}

export type UpstreamChannel = {
  id: number
  name: string
  base_url: string
  status: number
  type?: number
}

export type RatioType =
  | 'model_ratio'
  | 'completion_ratio'
  | 'cache_ratio'
  | 'create_cache_ratio'
  | 'image_ratio'
  | 'audio_ratio'
  | 'audio_completion_ratio'
  | 'model_price'
  | 'billing_mode'
  | 'billing_expr'

export type RatioDifference = {
  current: number | string | null
  upstreams: Record<string, number | string | 'same'>
  confidence: Record<string, boolean>
}

export type DifferencesMap = Record<
  string,
  Partial<Record<RatioType, RatioDifference>>
>

export type UpstreamChannelsResponse = {
  success: boolean
  message: string
  data: UpstreamChannel[]
}

export type UpstreamConfig = {
  id: number
  name: string
  base_url: string
  endpoint: string
}

export type FetchUpstreamRatiosRequest = {
  upstreams: UpstreamConfig[]
  timeout: number
}

export type TestResult = {
  name: string
  status: 'success' | 'error'
  error?: string
}

export type UpstreamRatiosResponse = {
  success: boolean
  message: string
  data: {
    differences: DifferencesMap
    test_results: TestResult[]
  }
}

// ============================================================================
// Sub-Site Sync types — docs/2026-05-27-sub-site-sync-plan.md
// ============================================================================

export type SubSiteVerifyStatus =
  | ''
  | 'ok'
  | 'auth_failed'
  | 'role_insufficient'
  | 'network_error'
  | 'unknown'

export type SubSite = {
  id: number
  name: string
  base_url: string
  upstream_user_id: number
  enabled: boolean
  token_set: boolean
  note: string
  last_verified_at: number
  last_verified_status: SubSiteVerifyStatus
  last_verified_msg: string
  last_verified_latency_ms: number
  last_verified_version: string
  created_time: number
  updated_time: number
}

export type SubSiteListResponse = {
  success: boolean
  message?: string
  data: SubSite[]
}

export type SubSiteUpsertRequest = {
  id?: number
  name: string
  base_url: string
  upstream_user_id: number
  token?: string // 编辑场景留空保留旧值
  enabled?: boolean
  note?: string
}

export type SubSiteUpsertResponse = {
  success: boolean
  message?: string
  data?: SubSite
}

export type SubSiteVerifyRequest = {
  id?: number
  base_url?: string
  token?: string
}

export type SubSiteVerifyResult = {
  status: SubSiteVerifyStatus
  version?: string
  role?: number
  latency_ms: number
  message?: string
}

export type SubSiteVerifyResponse = {
  success: boolean
  message?: string
  data?: SubSiteVerifyResult
}

export type SubSiteGroup = {
  group: string
  description?: string
  ratio: number
  tier_overrides?: Record<string, number>
  models: string[]
}

export type SubSiteGroupsResponse = {
  success: boolean
  message?: string
  data?: {
    groups: SubSiteGroup[]
    source?: string
    cached?: boolean
  }
}

export type SubSiteCreateGroupRow = {
  group: string
  local_name: string
  model_mapping?: Record<string, string>
  models?: string[]
}

export type SubSiteCreateChannelsRequest = {
  strategy: 'create' | 'overwrite' | 'skip'
  confirm?: boolean
  provision_keys?: boolean
  groups: SubSiteCreateGroupRow[]
}

export type SubSiteCreatePlanItem = {
  group: string
  local_name: string
  action: 'will_create' | 'will_overwrite' | 'will_skip'
  channel_id?: number
  reason?: string
  will_provision_key?: boolean
}

export type SubSiteCreateOKItem = {
  group: string
  local_name: string
  channel_id: number
  action: 'create' | 'overwrite'
  provisioned_key?: boolean
  upstream_token_id?: number
}

export type SubSiteCreateSkippedItem = {
  group: string
  local_name: string
  reason: string
}

export type SubSiteCreateFailedItem = {
  group: string
  local_name: string
  error: string
}

export type SubSiteCreateResult = {
  dry_run: boolean
  plan?: SubSiteCreatePlanItem[]
  ok?: SubSiteCreateOKItem[]
  skipped?: SubSiteCreateSkippedItem[]
  failed?: SubSiteCreateFailedItem[]
}

export type SubSiteCreateChannelsResponse = {
  success: boolean
  message?: string
  data?: SubSiteCreateResult
}

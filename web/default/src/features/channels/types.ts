import { z } from 'zod'

// ============================================================================
// Channel Schema & Types
// ============================================================================

export const channelInfoSchema = z.object({
  is_multi_key: z.boolean().default(false),
  multi_key_size: z.number().default(0),
  multi_key_status_list: z.record(z.string(), z.number()).optional(),
  multi_key_disabled_reason: z.record(z.string(), z.string()).optional(),
  multi_key_disabled_time: z.record(z.string(), z.number()).optional(),
  multi_key_polling_index: z.number().default(0),
  multi_key_mode: z.enum(['random', 'polling']).default('random'),
})

export type ChannelInfo = z.infer<typeof channelInfoSchema>

export const channelSchema = z.object({
  id: z.number(),
  type: z.number(),
  key: z.string(),
  openai_organization: z.string().nullish(),
  test_model: z.string().nullish(),
  status: z.number(), // 1: enabled, 0: manual disabled, 2: auto disabled
  name: z.string(),
  weight: z.number().nullish(),
  created_time: z.number(),
  test_time: z.number(),
  response_time: z.number(), // in milliseconds
  base_url: z.string().nullish(),
  other: z.string().default(''),
  balance: z.number().default(0), // in USD
  balance_updated_time: z.number(),
  models: z.string().default(''),
  group: z.string().default('default'),
  used_quota: z.number().default(0),
  model_mapping: z.string().nullish(),
  status_code_mapping: z.string().nullish(),
  priority: z.number().nullish(),
  auto_ban: z.number().nullish(),
  other_info: z.string().default(''),
  tag: z.string().nullish(),
  setting: z.string().nullish(),
  param_override: z.string().nullish(),
  header_override: z.string().nullish(),
  remark: z.string().default(''),
  max_input_tokens: z.number().default(0),
  channel_info: channelInfoSchema.default({
    is_multi_key: false,
    multi_key_size: 0,
    multi_key_polling_index: 0,
    multi_key_mode: 'random',
  }),
  settings: z.string().default('{}'), // other_settings JSON
  // 渠道运行质量快照（被动机制，docs/2026-05-12-channel-quality-rpm-list-plan.md）
  rpm_24h: z.number().default(0), // 实时 RPM（最近 60s 滑动窗口）；字段名 rpm_24h 保留向后兼容
  quality_score: z.number().int().min(0).max(100).nullable().optional(), // 0-100 综合评分；null=无流量
  quality_updated_at: z.number().default(0), // 上次重算 unix sec
  quality_detail: z.string().default(''), // 评分原始指标 JSON 快照（success_cnt/error_cnt/avg_use_time_ms/avg_frt_ms）；空=未算过
  // 外部测评分数快照（由管理员主动触发 /api/channel/:id/verify，外部测评网关健康分）；
  // 历史报告完整记录在 channel_verify_reports 表中。
  verify_score: z.number().int().min(0).max(100).nullable().optional(), // null=未测过
  verify_grade: z.string().default(''), // A+/A/B/C/D/F
  verify_tested_at: z.number().default(0),
  verify_report_id: z.number().default(0),
  // 自动定时测评 + 阈值启停（docs/2026-06-18-channel-verify-auto-schedule-plan.md）
  verify_prev_score: z.number().int().min(0).max(100).nullish(), // 上次成功分数：列表趋势着色对比
  verify_interval_minutes: z.number().int().nullish(), // 自动测评间隔(分)：null=继承全局 / 0=关闭 / >0=自定义
  verify_auto_disable_below: z.number().int().min(0).max(100).nullish(), // 评分<此值自动禁用；null=不启用
  verify_auto_enable_above: z.number().int().min(0).max(100).nullish(), // 评分>=此值自动启用；null=不启用
  verify_disabled: z.number().nullish(), // 1=当前被 verify 低分禁用
  // 上游分组倍率监控快照（docs/2026-08-05-upstream-group-ratio-monitor.md）
  ratio_panel_url: z.string().nullish(), // 面板域名，空则后端回落 base_url
  ratio_upstream_kind: z.string().nullish(), // newapi|sub2api|donehub|unsupported
  ratio_fetched_at: z.number().nullish(), // 0=从未抓取
  ratio_fetch_status: z.string().nullish(), // ok | unsupported | error
  ratio_fetch_msg: z.string().nullish(),
  ratio_up_count: z.number().nullish(),
  ratio_down_count: z.number().nullish(),
  ratio_changed_at: z.number().nullish(),
  ratio_detail: z.string().nullish(),
  ratio_upstream_group: z.string().nullish(), // 人工指定的上游分组名
  ratio_resolved_group: z.string().nullish(),
  ratio_effective: z.number().nullish(), // 实付反推倍率；null=无流量或不可算
  ratio_effective_at: z.number().nullish(),
  ratio_expected: z.number().nullish(), // 人工登记的采购倍率基准
  // 健康度自动降级（被动机制，详见 docs/2026-05-04-channel-health-auto-degrade-plan.md）
  degrade_level: z.number().nullish(), // 0=Healthy, 1=L1, 2=L2
  original_priority: z.number().nullish(),
  original_weight: z.number().nullish(),
  last_demote_at: z.number().nullish(),
  last_upgrade_at: z.number().nullish(),
  last_demote_reason: z.string().nullish(),
  last_disabled_at: z.number().nullish(),
  permanent_disabled: z.number().nullish(),
  rebounce_count: z.number().nullish(),
  // 路由模式可切换（docs/2026-05-26-channel-routing-mode-switchable.md）
  // 0=inherit（默认）, 1=probabilistic, 2=capacity
  routing_mode: z.number().nullish(),
  capacity_limit: z.number().nullish(),
  capacity_window_sec: z.number().nullish(),
  capacity_full_strategy: z.string().nullish(),
  // 渠道级重试策略（docs/2026-06-07-claude-retry-cache-loss.md）
  // 0=inherit, 1=cost_guard, 2=same_domain, 3=cross_channel
  retry_strategy: z.number().nullish(),
  cache_domain: z.string().nullish(),
})

export type Channel = z.infer<typeof channelSchema>

// ============================================================================
// Channel Settings Types
// ============================================================================

export interface ChannelSettings {
  force_format?: boolean
  thinking_to_content?: boolean
  proxy?: string
  pass_through_body_enabled?: boolean
  system_prompt?: string
  system_prompt_override?: boolean
}

export interface ChannelOtherSettings {
  azure_responses_version?: string
  vertex_key_type?: 'json' | 'api_key'
  openrouter_enterprise?: boolean
  aws_key_type?: 'ak_sk' | 'api_key'
  allow_service_tier?: boolean
  disable_store?: boolean
  allow_safety_identifier?: boolean
  allow_include_obfuscation?: boolean
  allow_inference_geo?: boolean
  allow_speed?: boolean
  claude_beta_query?: boolean
  upstream_model_update_check_enabled?: boolean
  upstream_model_update_auto_sync_enabled?: boolean
  upstream_model_update_ignored_models?: string[]
  upstream_model_update_last_check_time?: number
  upstream_model_update_last_detected_models?: string[]
  advanced_custom?: AdvancedCustomConfig
}

export interface AdvancedCustomConfig {
  advanced_routes?: AdvancedCustomRoute[]
}

export interface AdvancedCustomRoute {
  incoming_path?: string
  upstream_path?: string
  converter?: string
  models?: string[]
  auth?: {
    type?: 'none' | 'header' | 'query'
    name?: string
    value?: string
  }
}

// ============================================================================
// API Response Types
// ============================================================================

export interface GetChannelsResponse {
  success: boolean
  message?: string
  data?: {
    items: Channel[]
    total: number
    page: number
    page_size: number
    type_counts?: Record<string, number>
  }
}

export interface SearchChannelsResponse {
  success: boolean
  message?: string
  data?: {
    items: Channel[]
    total: number
    type_counts?: Record<string, number>
  }
}

export interface GetChannelResponse {
  success: boolean
  message?: string
  data?: Channel
}

export interface ChannelTestResponse {
  success: boolean
  message?: string
  error_code?: string
  /** 后端 controller/channel-test.go::TestChannel 返回顶层 `time` 字段，单位**秒**（float） */
  time?: number
}

export interface ChannelBalanceResponse {
  success: boolean
  message?: string
  balance?: number
  currency?: string
}

export interface FetchModelsResponse {
  success: boolean
  message?: string
  data?: string[]
}

export interface CopyChannelResponse {
  success: boolean
  message?: string
  data?: {
    id: number
  }
}

// ============================================================================
// Multi-Key Management Types
// ============================================================================

export interface KeyStatus {
  index: number
  status: number // 1: enabled, 2: manual disabled, 3: auto disabled
  disabled_time?: number
  reason?: string
  key_preview?: string
}

export type MultiKeyConfirmAction = {
  type:
    | 'enable'
    | 'disable'
    | 'delete'
    | 'enable-all'
    | 'disable-all'
    | 'delete-disabled'
  keyIndex?: number
}

export interface MultiKeyStatusResponse {
  success: boolean
  message?: string
  data?: {
    keys: KeyStatus[]
    total: number
    page: number
    page_size: number
    total_pages: number
    enabled_count: number
    manual_disabled_count: number
    auto_disabled_count: number
  }
}

// ============================================================================
// API Request Parameters
// ============================================================================

export interface GetChannelsParams {
  p?: number
  page_size?: number
  status?: string // 'enabled', 'disabled', or empty for all
  type?: number
  group?: string
  id_sort?: boolean
  tag_mode?: boolean
  // 后端 ORDER BY：目前仅支持 rpm_24h（Go 层按 Redis 实时值排序），见 controller.GetAllChannels
  order_by?: string
  order?: 'asc' | 'desc'
}

export interface SearchChannelsParams {
  keyword?: string
  group?: string
  model?: string
  status?: string
  type?: number
  id_sort?: boolean
  tag_mode?: boolean
  p?: number
  page_size?: number
}

export interface ChannelTestParams {
  test_model?: string
}

// ============================================================================
// External Verify (external gateway) Types
// ============================================================================

export interface VerifyReportSummary {
  id: number
  channel_id: number
  model: string
  score: number
  grade: string
  status: 'running' | 'success' | 'failed' | 'cancelled'
  duration_ms: number
  error_msg?: string
  tested_at: number
  operator_id: number
}

// 完整测评报告（外部测评网关 /api/verify/claude 的 done payload，经 transform.ts 转成 snake_case）。
// 字段形状以 verify.example.com/vue-app/server/utils/verify/transform.ts 为准。
// 注意：维度无 per-dimension score，只有 weight + status（pass/warning/fail）。
export interface VerifyDimension {
  dimension_id: number
  name?: string
  weight?: number
  status?: 'pass' | 'warning' | 'fail'
  detail?: string
  // dim 6 = 随机数指纹 {samples, hits_247, unique_count, suspected_cached}
  // dim 12 = 小说提示词 {samples, mojibake_count, misaki_first_name_count, format_compliant_count}
  evidence?: Record<string, unknown>
}

export interface VerifyHealth {
  total_score?: number
  grade?: string
  stage_cap?: number | null
  token_penalty?: number
  output_token_sum?: number
  dimensions?: VerifyDimension[]
}

export interface VerifyCache {
  hit_rate_identical?: number
  hit_rate_vary_tail?: number
  warm_hit_rate_identical?: number
  warm_hit_rate_vary_tail?: number
}

export interface VerifyCompatProbe {
  name: string
  type: 'required' | 'optional' | string
  pass: boolean
  reason?: string
}

export interface VerifyCompatRecommendation {
  field_name: string
  value: unknown
  reason_zh?: string
}

export interface VerifyCompat {
  probes?: VerifyCompatProbe[]
  recommendations?: VerifyCompatRecommendation[]
}

export interface VerifyFingerprint {
  verdict?: string // claude: official_anthropic|aws_bedrock|max_subscription|ide_reverse_proxy|unknown_proxy; openai: official_openai|azure_openai|chatgpt_reverse|thirdparty_proxy|unknown_proxy
  confidence?: number
  signals?: string[]
  headers?: {
    has_anthropic_ratelimit?: boolean
    has_cloud_trace?: boolean
    has_aws_headers?: boolean
    request_id_format?: string
    server_header?: string | null
    custom_headers?: string[]
    all_headers?: Record<string, string>
  } | null
  usage_shape?: {
    has_cache_creation?: boolean
    has_cache_read?: boolean
    has_service_tier?: boolean
    has_inference_geo?: boolean
    uses_camel_case?: boolean
    extra_fields?: string[]
    raw_usage?: Record<string, unknown>
  } | null
  model_echo?: {
    sent_model?: string
    received_model?: string | null
    matches?: boolean
    message_id_prefix?: string | null
  } | null
  error_format?: {
    http_status?: number
    error_type?: string | null
    error_message?: string | null
    has_anthropic_format?: boolean
    has_aws_format?: boolean
    raw_body?: string
  } | null
  injection?: {
    detected_injection?: boolean
    injected_content?: string | null
    confidence?: string
  } | null
  timing?: {
    ttft_ms?: number
    total_ms?: number
    tokens_per_second?: number
    chunk_count?: number
    avg_chunk_size?: number
    output_tokens?: number
  } | null
  beta_support?: {
    thinking?: boolean
    interleaved_thinking?: boolean
    pdfs?: boolean
  } | null
  baseline_matches?: Array<{
    id?: string
    label?: string
    source?: string
    score?: number
    max_score?: number
    match_rate?: number
    matched_signals?: string[]
    missed_signals?: string[]
  }>
  findings?: {
    key_findings?: Array<{
      title?: string
      detail?: string
      evidence?: string | null
    }>
    conclusion?: string
    relay_info?: string | null
  } | null
}

export interface VerifyFullReport {
  health?: VerifyHealth
  cache?: VerifyCache | null
  compat?: VerifyCompat | null
  fingerprint?: VerifyFingerprint | null
}

export interface VerifyReportDetail extends VerifyReportSummary {
  report?: VerifyFullReport
}

export interface ListVerifyReportsResponse {
  success: boolean
  message?: string
  data?: {
    items: VerifyReportSummary[]
    total: number
    page: number
    page_size: number
  }
}

export interface GetVerifyReportResponse {
  success: boolean
  message?: string
  data?: VerifyReportDetail
}

// SSE events emitted by POST /api/channel/:id/verify
// (透传测评网关上游事件 + 后端注入的 queued/acquired/report_created)
export type VerifySSEEvent =
  | { type: 'queued'; data: { position: number } }
  | { type: 'acquired'; data: { position: number } }
  | { type: 'report_created'; data: { report_id: number } }
  | { type: 'preflight_ok'; data?: any }
  | { type: 'health_progress'; data: { dimensions: VerifyDimension[] } }
  | { type: 'health_score'; data: VerifyHealth }
  | { type: 'early_stop'; data: VerifyHealth }
  | { type: 'cache_progress'; data: any }
  | { type: 'cache_result'; data: VerifyCache }
  | { type: 'compat_result'; data: VerifyCompat }
  | { type: 'fingerprint_progress'; data: any }
  | { type: 'fingerprint_result'; data: VerifyFingerprint }
  | { type: 'done'; data: VerifyFullReport }
  | { type: 'error'; data: { message: string } }
  | { type: 'heartbeat'; data?: any }

export interface CopyChannelParams {
  suffix?: string
  reset_balance?: boolean
}

export interface MultiKeyManageParams {
  channel_id: number
  action:
    | 'get_key_status'
    | 'disable_key'
    | 'enable_key'
    | 'enable_all_keys'
    | 'disable_all_keys'
    | 'delete_key'
    | 'delete_disabled_keys'
  key_index?: number
  page?: number
  page_size?: number
  status?: number // 1=enabled, 2=manual_disabled, 3=auto_disabled
}

export interface BatchDeleteParams {
  ids: number[]
}

export interface BatchSetTagParams {
  ids: number[]
  tag: string | null
}

export interface TagOperationParams {
  tag: string
  new_tag?: string
  priority?: number
  weight?: number
  model_mapping?: string
  models?: string
  groups?: string
}

// ============================================================================
// Form Data Types
// ============================================================================

export interface ChannelFormData {
  name: string
  type: number
  base_url: string
  key: string
  openai_organization?: string
  models: string
  group: string
  model_mapping?: string
  priority?: number
  weight?: number
  test_model?: string
  auto_ban?: number
  status: number
  status_code_mapping?: string
  tag?: string
  remark?: string
  setting?: string
  param_override?: string
  header_override?: string
  settings?: string
  other?: string
  // Multi-key specific
  multi_key_mode?: 'single' | 'batch' | 'multi_to_single'
  multi_key_type?: 'random' | 'polling'
  batch_add_set_key_prefix_2_name?: boolean
}

// ============================================================================
// Add Channel Request (special structure)
// ============================================================================

export interface AddChannelRequest {
  mode: 'single' | 'batch' | 'multi_to_single'
  multi_key_mode?: 'random' | 'polling'
  batch_add_set_key_prefix_2_name?: boolean
  channel: Partial<Channel>
}

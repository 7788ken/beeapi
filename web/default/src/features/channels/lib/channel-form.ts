import { z } from 'zod'
import { CHANNEL_STATUS, MODEL_FETCHABLE_TYPES } from '../constants'
import type { Channel } from '../types'
import {
  CHANNEL_TYPE_ADVANCED_CUSTOM,
  advancedCustomConfigUsesRelativeUpstreamPath,
  hasAdvancedCustomModelListRoute,
  parseAdvancedCustomConfig,
  stringifyAdvancedCustomConfig,
  validateAdvancedCustomConfig,
} from './advanced-custom'

// ============================================================================
// Form Validation Schema
// ============================================================================

export const channelFormSchema = z
  .object({
    name: z.string().min(1, 'Channel name is required'),
    type: z.number().min(0, 'Channel type is required'),
    base_url: z.string().optional(),
    key: z.string(),
    openai_organization: z.string().optional(),
    models: z.string().min(1, 'At least one model is required'),
    group: z.array(z.string()).min(1, 'At least one group is required'),
    model_mapping: z.string().optional(),
    priority: z.number().optional(),
    weight: z.number().optional(),
    test_model: z.string().optional(),
    auto_ban: z.number().optional(),
    status: z.number(),
    status_code_mapping: z.string().optional(),
    tag: z.string().optional(),
    remark: z
      .string()
      .max(255, 'Remark must be less than 255 characters')
      .optional(),
    setting: z.string().optional(),
    param_override: z.string().optional(),
    header_override: z.string().optional(),
    settings: z.string().optional(),
    advanced_custom: z.string().optional(),
    other: z.string().optional(),
    // Multi-key options (not sent to backend directly)
    multi_key_mode: z.enum(['single', 'batch', 'multi_to_single']).optional(),
    multi_key_type: z.enum(['random', 'polling']).optional(),
    batch_add_set_key_prefix_2_name: z.boolean().optional(),
    key_mode: z.enum(['append', 'replace']).optional(), // For editing multi-key channels
    // Channel extra settings (stored in setting JSON, not sent directly)
    force_format: z.boolean().optional(),
    thinking_to_content: z.boolean().optional(),
    proxy: z.string().optional(),
    pass_through_body_enabled: z.boolean().optional(),
    system_prompt: z.string().optional(),
    system_prompt_override: z.boolean().optional(),
    // 备用 base_url（多行文本，每行一个 URL；存 setting.backup_base_urls 数组）
    backup_base_urls: z.string().optional(),
    // Type-specific settings (stored in settings JSON)
    is_enterprise_account: z.boolean().optional(), // OpenRouter specific
    vertex_key_type: z.enum(['json', 'api_key']).optional(), // Vertex AI specific
    aws_key_type: z.enum(['ak_sk', 'api_key']).optional(), // AWS specific
    azure_responses_version: z.string().optional(), // Azure specific
    // Field passthrough controls (stored in settings JSON)
    allow_service_tier: z.boolean().optional(), // OpenAI/Anthropic
    disable_store: z.boolean().optional(), // OpenAI only
    allow_safety_identifier: z.boolean().optional(), // OpenAI only
    allow_include_obfuscation: z.boolean().optional(), // OpenAI: include usage obfuscation
    allow_inference_geo: z.boolean().optional(), // OpenAI/Anthropic: inference geography
    allow_speed: z.boolean().optional(), // Anthropic: speed mode control
    claude_beta_query: z.boolean().optional(), // Anthropic: beta query passthrough
    // Upstream model update settings (stored in settings JSON)
    upstream_model_update_check_enabled: z.boolean().optional(),
    upstream_model_update_auto_sync_enabled: z.boolean().optional(),
    upstream_model_update_ignored_models: z.string().optional(),
    // 路由模式可切换（docs/2026-05-26-channel-routing-mode-switchable.md）
    routing_mode: z.number().int().min(0).max(2).optional(), // 0=inherit, 1=probabilistic, 2=capacity
    capacity_limit: z.number().int().min(0).nullable().optional(),
    capacity_window_sec: z.number().int().min(0).nullable().optional(),
    capacity_full_strategy: z.string().max(16).nullable().optional(), // 空/null=继承全局
    // 渠道级重试策略（docs/2026-06-07-claude-retry-cache-loss.md）
    // 0=inherit, 1=cost_guard(成本保护), 2=same_domain(同缓存域), 3=cross_channel(跨渠道)
    retry_strategy: z.number().int().min(0).max(3).optional(),
    cache_domain: z.string().max(64).optional(),
    // 上游倍率监控面板域名；与 base_url 不同时才填（ai-wave 的 relay 域名 /api/* 全 404）
    ratio_panel_url: z.string().max(255).optional(),
    // 我方 key 在上游所属的分组名；填了才能显示确切采购倍率
    ratio_upstream_group: z.string().max(191).optional(),
    // 人工登记的采购倍率；与实测值偏差超阈值即告警
    ratio_expected: z.number().min(0).nullable().optional(),
    // 渠道级 verify 自动测评 / 阈值启停（docs/2026-06-18）；null=继承全局或不启用
    verify_interval_minutes: z.number().int().min(0).nullable().optional(),
    verify_auto_disable_below: z
      .number()
      .int()
      .min(0)
      .max(100)
      .nullable()
      .optional(),
    verify_auto_enable_above: z
      .number()
      .int()
      .min(0)
      .max(100)
      .nullable()
      .optional(),
  })
  .superRefine((data, ctx) => {
    // 滞回校验：自动启用阈值必须 >= 自动禁用阈值，否则中间分数区会反复启停
    const lo = data.verify_auto_disable_below
    const hi = data.verify_auto_enable_above
    if (lo != null && hi != null && hi < lo) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['verify_auto_enable_above'],
        message: 'Auto-enable threshold must be >= auto-disable threshold',
      })
    }
    if (data.type === CHANNEL_TYPE_ADVANCED_CUSTOM) {
      const config = parseAdvancedCustomConfig(data.advanced_custom)
      const error = validateAdvancedCustomConfig(config)
      if (error) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['advanced_custom'],
          message: error,
        })
      }
      if (
        advancedCustomConfigUsesRelativeUpstreamPath(config) &&
        !data.base_url?.trim()
      ) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['base_url'],
          message: 'Base URL is required when routes use relative paths',
        })
      }
      if (
        data.upstream_model_update_check_enabled === true &&
        !hasAdvancedCustomModelListRoute(config)
      ) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ['advanced_custom'],
          message: 'A native /v1/models route is required for model checks',
        })
      }
    }
  })

export type ChannelFormValues = z.infer<typeof channelFormSchema>

// ============================================================================
// Default Form Values
// ============================================================================

export const CHANNEL_FORM_DEFAULT_VALUES: ChannelFormValues = {
  name: '',
  type: 1,
  base_url: '',
  key: '',
  openai_organization: '',
  models: '',
  group: ['default'],
  model_mapping: '',
  priority: 0,
  weight: 0,
  test_model: '',
  auto_ban: 1,
  status: CHANNEL_STATUS.ENABLED,
  status_code_mapping: '',
  tag: '',
  remark: '',
  setting: '',
  param_override: '',
  header_override: '',
  settings: '{}',
  other: '',
  multi_key_mode: 'single',
  multi_key_type: 'random',
  batch_add_set_key_prefix_2_name: false,
  key_mode: 'append',
  // Channel extra settings
  force_format: false,
  thinking_to_content: false,
  proxy: '',
  pass_through_body_enabled: false,
  system_prompt: '',
  system_prompt_override: false,
  backup_base_urls: '',
  // Type-specific settings
  is_enterprise_account: false,
  vertex_key_type: 'json',
  aws_key_type: 'ak_sk',
  azure_responses_version: '',
  // Field passthrough controls
  allow_service_tier: false,
  disable_store: false,
  allow_safety_identifier: false,
  allow_include_obfuscation: false,
  allow_inference_geo: false,
  allow_speed: false,
  claude_beta_query: false,
  upstream_model_update_check_enabled: false,
  upstream_model_update_auto_sync_enabled: false,
  upstream_model_update_ignored_models: '',
  advanced_custom: '',
  // 路由模式默认 inherit（跟随全局），完全兼容现有渠道
  routing_mode: 0,
  capacity_limit: null,
  capacity_window_sec: null,
  capacity_full_strategy: null,
  // 重试策略默认 inherit（跟随全局/当前跨渠道行为），完全兼容现有渠道
  retry_strategy: 0,
  cache_domain: '',
  ratio_panel_url: '',
  ratio_upstream_group: '',
  ratio_expected: null,
  verify_interval_minutes: null,
  verify_auto_disable_below: null,
  verify_auto_enable_above: null,
}

// ============================================================================
// Transform Functions
// ============================================================================

/**
 * Transform Channel from API to Form default values
 */
export function transformChannelToFormDefaults(
  channel: Channel
): ChannelFormValues {
  // Parse channel extra settings from setting field
  let extraSettings = {
    force_format: false,
    thinking_to_content: false,
    proxy: '',
    pass_through_body_enabled: false,
    system_prompt: '',
    system_prompt_override: false,
    backup_base_urls: '',
  }

  if (channel.setting) {
    try {
      const parsed = JSON.parse(channel.setting)
      extraSettings = {
        force_format: parsed.force_format || false,
        thinking_to_content: parsed.thinking_to_content || false,
        proxy: parsed.proxy || '',
        pass_through_body_enabled: parsed.pass_through_body_enabled || false,
        system_prompt: parsed.system_prompt || '',
        system_prompt_override: parsed.system_prompt_override || false,
        backup_base_urls: Array.isArray(parsed.backup_base_urls)
          ? parsed.backup_base_urls.join('\n')
          : '',
      }
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Failed to parse channel setting:', error)
    }
  }

  // Parse type-specific settings from settings field
  let vertexKeyType: 'json' | 'api_key' = 'json'
  let azureResponsesVersion = ''
  let isEnterpriseAccount = false
  let awsKeyType: 'ak_sk' | 'api_key' = 'ak_sk'
  let allowServiceTier = false
  let disableStore = false
  let allowSafetyIdentifier = false
  let allowIncludeObfuscation = false
  let allowInferenceGeo = false
  let allowSpeed = false
  let claudeBetaQuery = false
  let upstreamModelUpdateCheckEnabled = false
  let upstreamModelUpdateAutoSyncEnabled = false
  let upstreamModelUpdateIgnoredModels = ''
  let advancedCustom = ''

  if (channel.settings) {
    try {
      const parsed = JSON.parse(channel.settings)
      vertexKeyType = parsed.vertex_key_type || 'json'
      azureResponsesVersion = parsed.azure_responses_version || ''
      isEnterpriseAccount = parsed.openrouter_enterprise === true
      awsKeyType = parsed.aws_key_type || 'ak_sk'
      allowServiceTier = parsed.allow_service_tier === true
      disableStore = parsed.disable_store === true
      allowSafetyIdentifier = parsed.allow_safety_identifier === true
      allowIncludeObfuscation = parsed.allow_include_obfuscation === true
      allowInferenceGeo = parsed.allow_inference_geo === true
      allowSpeed = parsed.allow_speed === true
      claudeBetaQuery = parsed.claude_beta_query === true
      upstreamModelUpdateCheckEnabled =
        parsed.upstream_model_update_check_enabled === true
      upstreamModelUpdateAutoSyncEnabled =
        parsed.upstream_model_update_auto_sync_enabled === true
      upstreamModelUpdateIgnoredModels = Array.isArray(
        parsed.upstream_model_update_ignored_models
      )
        ? parsed.upstream_model_update_ignored_models.join(',')
        : ''
      if (parsed.advanced_custom) {
        advancedCustom = stringifyAdvancedCustomConfig(parsed.advanced_custom)
      }
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Failed to parse channel settings:', error)
    }
  }

  return {
    name: channel.name || '',
    type: channel.type,
    base_url: channel.base_url || '',
    key: '', // Never populate key from backend for security
    openai_organization: channel.openai_organization || '',
    models: channel.models || '',
    group: parseGroups(channel.group || 'default'),
    model_mapping: channel.model_mapping || '',
    priority: channel.priority || 0,
    weight: channel.weight || 0,
    test_model: channel.test_model || '',
    auto_ban: channel.auto_ban ?? 1,
    status: channel.status,
    status_code_mapping: channel.status_code_mapping || '',
    tag: channel.tag || '',
    remark: channel.remark || '',
    setting: channel.setting || '',
    param_override: channel.param_override || '',
    header_override: channel.header_override || '',
    settings: channel.settings || '{}',
    other: channel.other || '',
    multi_key_mode: 'single',
    multi_key_type: channel.channel_info.multi_key_mode || 'random',
    batch_add_set_key_prefix_2_name: false,
    key_mode: 'append', // Default to append mode for editing multi-key channels
    // Channel extra settings
    ...extraSettings,
    // Type-specific settings
    is_enterprise_account: isEnterpriseAccount,
    vertex_key_type: vertexKeyType,
    azure_responses_version: azureResponsesVersion,
    aws_key_type: awsKeyType,
    allow_service_tier: allowServiceTier,
    disable_store: disableStore,
    allow_include_obfuscation: allowIncludeObfuscation,
    allow_inference_geo: allowInferenceGeo,
    allow_speed: allowSpeed,
    claude_beta_query: claudeBetaQuery,
    allow_safety_identifier: allowSafetyIdentifier,
    upstream_model_update_check_enabled: upstreamModelUpdateCheckEnabled,
    upstream_model_update_auto_sync_enabled: upstreamModelUpdateAutoSyncEnabled,
    upstream_model_update_ignored_models: upstreamModelUpdateIgnoredModels,
    advanced_custom: advancedCustom,
    routing_mode: channel.routing_mode ?? 0,
    capacity_limit: channel.capacity_limit ?? null,
    capacity_window_sec: channel.capacity_window_sec ?? null,
    capacity_full_strategy: channel.capacity_full_strategy ?? null,
    retry_strategy: channel.retry_strategy ?? 0,
    cache_domain: channel.cache_domain ?? '',
    ratio_panel_url: channel.ratio_panel_url ?? '',
    ratio_upstream_group: channel.ratio_upstream_group ?? '',
    ratio_expected: channel.ratio_expected ?? null,
    verify_interval_minutes: channel.verify_interval_minutes ?? null,
    verify_auto_disable_below: channel.verify_auto_disable_below ?? null,
    verify_auto_enable_above: channel.verify_auto_enable_above ?? null,
  }
}

/**
 * Build the setting JSON string from form extra settings
 */
function buildSettingJSON(formData: ChannelFormValues): string {
  const settingObj = {
    force_format: formData.force_format || false,
    thinking_to_content: formData.thinking_to_content || false,
    proxy: formData.proxy || '',
    pass_through_body_enabled: formData.pass_through_body_enabled || false,
    system_prompt: formData.system_prompt || '',
    system_prompt_override: formData.system_prompt_override || false,
    backup_base_urls: parseBackupBaseUrls(formData.backup_base_urls),
  }
  return JSON.stringify(settingObj)
}

/**
 * Build the settings JSON string (for type-specific config like vertex_key_type)
 */
function buildSettingsJSON(formData: ChannelFormValues): string {
  let settingsObj: Record<string, unknown> = {}

  // Try to parse existing settings first
  if (formData.settings && formData.settings !== '{}') {
    try {
      settingsObj = JSON.parse(formData.settings)
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Failed to parse existing settings:', error)
    }
  }

  // Add vertex_key_type for Vertex AI channels (type 41)
  if (formData.type === 41) {
    settingsObj.vertex_key_type = formData.vertex_key_type || 'json'
  } else if ('vertex_key_type' in settingsObj) {
    delete settingsObj.vertex_key_type
  }

  // Add azure_responses_version for Azure channels (type 3)
  if (formData.type === 3 && formData.azure_responses_version) {
    settingsObj.azure_responses_version = formData.azure_responses_version
  } else if ('azure_responses_version' in settingsObj) {
    delete settingsObj.azure_responses_version
  }

  // Add enterprise account setting for OpenRouter (type 20)
  if (formData.type === 20) {
    settingsObj.openrouter_enterprise = formData.is_enterprise_account === true
  } else if ('openrouter_enterprise' in settingsObj) {
    delete settingsObj.openrouter_enterprise
  }

  // Add aws_key_type for AWS channels (type 33)
  if (formData.type === 33) {
    settingsObj.aws_key_type = formData.aws_key_type || 'ak_sk'
  } else if ('aws_key_type' in settingsObj) {
    delete settingsObj.aws_key_type
  }

  // Field passthrough controls:
  // - OpenAI (type 1) and Anthropic (type 14): allow_service_tier
  // - OpenAI only: disable_store, allow_safety_identifier
  if (formData.type === 1 || formData.type === 14) {
    settingsObj.allow_service_tier = formData.allow_service_tier === true
  } else if ('allow_service_tier' in settingsObj) {
    delete settingsObj.allow_service_tier
  }

  if (formData.type === 1) {
    settingsObj.disable_store = formData.disable_store === true
    settingsObj.allow_safety_identifier =
      formData.allow_safety_identifier === true
    settingsObj.allow_include_obfuscation =
      formData.allow_include_obfuscation === true
    settingsObj.allow_inference_geo = formData.allow_inference_geo === true
  } else {
    if ('disable_store' in settingsObj) delete settingsObj.disable_store
    if ('allow_safety_identifier' in settingsObj)
      delete settingsObj.allow_safety_identifier
    if ('allow_include_obfuscation' in settingsObj)
      delete settingsObj.allow_include_obfuscation
    if (formData.type !== 14 && 'allow_inference_geo' in settingsObj)
      delete settingsObj.allow_inference_geo
  }

  // Anthropic (type 14): claude_beta_query, allow_inference_geo, allow_speed
  if (formData.type === 14) {
    settingsObj.allow_inference_geo = formData.allow_inference_geo === true
    settingsObj.allow_speed = formData.allow_speed === true
    settingsObj.claude_beta_query = formData.claude_beta_query === true
  } else {
    if ('allow_speed' in settingsObj) delete settingsObj.allow_speed
    if ('claude_beta_query' in settingsObj) delete settingsObj.claude_beta_query
  }

  // Upstream model update settings (for model-fetchable channel types)
  if (MODEL_FETCHABLE_TYPES.has(formData.type)) {
    settingsObj.upstream_model_update_check_enabled =
      formData.upstream_model_update_check_enabled === true
    settingsObj.upstream_model_update_auto_sync_enabled =
      settingsObj.upstream_model_update_check_enabled === true &&
      formData.upstream_model_update_auto_sync_enabled === true
    settingsObj.upstream_model_update_ignored_models = Array.from(
      new Set(
        String(formData.upstream_model_update_ignored_models || '')
          .split(',')
          .map((model) => model.trim())
          .filter(Boolean)
      )
    )
    if (
      !Array.isArray(settingsObj.upstream_model_update_last_detected_models) ||
      settingsObj.upstream_model_update_check_enabled !== true
    ) {
      settingsObj.upstream_model_update_last_detected_models = []
    }
    if (typeof settingsObj.upstream_model_update_last_check_time !== 'number') {
      settingsObj.upstream_model_update_last_check_time = 0
    }
  }

  if (formData.type === CHANNEL_TYPE_ADVANCED_CUSTOM) {
    const config = parseAdvancedCustomConfig(formData.advanced_custom)
    if (config) settingsObj.advanced_custom = config
  } else if ('advanced_custom' in settingsObj) {
    delete settingsObj.advanced_custom
  }

  return JSON.stringify(settingsObj)
}

/**
 * Transform form data to API payload for creating channel
 */
export function transformFormDataToCreatePayload(formData: ChannelFormValues): {
  mode: 'single' | 'batch' | 'multi_to_single'
  multi_key_mode?: 'random' | 'polling'
  batch_add_set_key_prefix_2_name?: boolean
  channel: Partial<Channel>
} {
  const mode = formData.multi_key_mode || 'single'

  const channel: Partial<Channel> = {
    name: formData.name,
    type: formData.type,
    base_url: formData.base_url || null,
    key: formData.key,
    openai_organization: formData.openai_organization || null,
    models: formData.models,
    group: formatGroups(formData.group),
    model_mapping: formData.model_mapping || null,
    priority: formData.priority || null,
    weight: formData.weight || null,
    test_model: formData.test_model || null,
    auto_ban: formData.auto_ban ?? 1,
    status: formData.status,
    status_code_mapping: formData.status_code_mapping || null,
    tag: formData.tag || null,
    remark: formData.remark || '',
    setting: buildSettingJSON(formData),
    param_override: formData.param_override || null,
    header_override: formData.header_override || null,
    settings: buildSettingsJSON(formData),
    other: formData.other || '',
    routing_mode: formData.routing_mode ?? 0,
    capacity_limit: formData.capacity_limit ?? null,
    capacity_window_sec: formData.capacity_window_sec ?? null,
    capacity_full_strategy: formData.capacity_full_strategy || null,
    retry_strategy: formData.retry_strategy ?? 0,
    cache_domain: formData.cache_domain || null,
    ratio_panel_url: formData.ratio_panel_url || null,
    ratio_upstream_group: formData.ratio_upstream_group || null,
    ratio_expected: formData.ratio_expected ?? null,
    verify_interval_minutes: formData.verify_interval_minutes ?? null,
    verify_auto_disable_below: formData.verify_auto_disable_below ?? null,
    verify_auto_enable_above: formData.verify_auto_enable_above ?? null,
  }

  // Clean up empty strings to null for optional fields
  Object.keys(channel).forEach((key) => {
    if (channel[key as keyof typeof channel] === '') {
      ;(channel as Record<string, unknown>)[key] = null
    }
  })

  return {
    mode,
    multi_key_mode:
      mode === 'multi_to_single' ? formData.multi_key_type : undefined,
    batch_add_set_key_prefix_2_name:
      mode === 'batch' ? formData.batch_add_set_key_prefix_2_name : undefined,
    channel,
  }
}

/**
 * Transform form data to API payload for updating channel
 */
export function transformFormDataToUpdatePayload(
  formData: ChannelFormValues,
  channelId: number
): Partial<Channel> {
  const payload: Partial<Channel> = {
    id: channelId,
    name: formData.name,
    type: formData.type,
    base_url: formData.base_url || null,
    openai_organization: formData.openai_organization || null,
    models: formData.models,
    group: formatGroups(formData.group),
    model_mapping: formData.model_mapping || null,
    priority: formData.priority || null,
    weight: formData.weight || null,
    test_model: formData.test_model || null,
    auto_ban: formData.auto_ban ?? 1,
    status: formData.status,
    status_code_mapping: formData.status_code_mapping || null,
    tag: formData.tag || null,
    remark: formData.remark || '',
    setting: buildSettingJSON(formData),
    param_override: formData.param_override || null,
    header_override: formData.header_override || null,
    settings: buildSettingsJSON(formData),
    other: formData.other || '',
    routing_mode: formData.routing_mode ?? 0,
    capacity_limit: formData.capacity_limit ?? null,
    capacity_window_sec: formData.capacity_window_sec ?? null,
    retry_strategy: formData.retry_strategy ?? 0,
    verify_interval_minutes: formData.verify_interval_minutes ?? null,
    verify_auto_disable_below: formData.verify_auto_disable_below ?? null,
    verify_auto_enable_above: formData.verify_auto_enable_above ?? null,
  }

  // Only include key if it was changed (not empty)
  if (formData.key && formData.key.trim()) {
    payload.key = formData.key
  }

  // Clean up empty strings to null for optional fields
  Object.keys(payload).forEach((key) => {
    if (payload[key as keyof typeof payload] === '') {
      ;(payload as Record<string, unknown>)[key] = null
    }
  })

  // Send explicit empty strings for nullable JSON/text fields so GORM updates can clear them.
  payload.model_mapping = formData.model_mapping || ''
  payload.status_code_mapping = formData.status_code_mapping || ''
  payload.param_override = formData.param_override || ''
  payload.header_override = formData.header_override || ''
  // cache_domain 是 *string；显式发空串让后端能清空（默认回退到 ch:<id>）
  payload.cache_domain = formData.cache_domain || ''
  // ratio_panel_url 是 *string；显式发空串让后端能清空（回退用 base_url）
  payload.ratio_panel_url = formData.ratio_panel_url || ''
  payload.ratio_upstream_group = formData.ratio_upstream_group || ''
  payload.ratio_expected = formData.ratio_expected ?? null
  // capacity_full_strategy 同为 *string；显式空串让后端清空，回退继承全局
  payload.capacity_full_strategy = formData.capacity_full_strategy || ''

  return payload
}

// ============================================================================
// Validation Helpers
// ============================================================================

/**
 * Validate JSON string
 */
export function validateJSON(value: string): boolean {
  if (!value || value.trim() === '') return true
  try {
    JSON.parse(value)
    return true
  } catch {
    return false
  }
}

/**
 * Validate model mapping format
 */
export function validateModelMapping(value: string): boolean {
  if (!value || value.trim() === '') return true
  return validateJSON(value)
}

/**
 * Parse models string to array
 */
export function parseModels(models: string): string[] {
  if (!models) return []
  return models
    .split(',')
    .map((m) => m.trim())
    .filter((m) => m.length > 0)
}

/**
 * Parse groups string to array
 */
export function parseGroups(groups: string): string[] {
  if (!groups) return []
  return groups
    .split(',')
    .map((g) => g.trim())
    .filter((g) => g.length > 0)
}

/**
 * Format models array to string
 */
export function formatModels(models: string[]): string {
  return models.join(',')
}

/**
 * Format groups array to string
 */
export function formatGroups(groups: string[]): string {
  return groups.join(',')
}

/**
 * Parse multi-line backup base URLs textarea to string array.
 * Split on newline, trim, drop empty lines. Empty input => [].
 */
export function parseBackupBaseUrls(value: string | undefined): string[] {
  if (!value) return []
  return value
    .split('\n')
    .map((url) => url.trim())
    .filter((url) => url.length > 0)
}

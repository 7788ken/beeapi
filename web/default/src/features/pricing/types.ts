// ----------------------------------------------------------------------------
// Pricing Types
// ----------------------------------------------------------------------------

export type PricingVendor = {
  id: number
  name: string
  icon?: string
  description?: string
}

export type Modality = 'text' | 'image' | 'audio' | 'video' | 'file'

export type ModelCapability =
  | 'function_calling'
  | 'streaming'
  | 'vision'
  | 'json_mode'
  | 'structured_output'
  | 'reasoning'
  | 'tools'
  | 'system_prompt'
  | 'web_search'
  | 'code_interpreter'
  | 'caching'
  | 'embeddings'

export type PricingModel = {
  id: number
  model_name: string
  description?: string
  vendor_id?: number
  vendor_name?: string
  vendor_icon?: string
  vendor_description?: string
  quota_type: number
  model_ratio: number
  completion_ratio: number
  model_price?: number
  cache_ratio?: number | null
  create_cache_ratio?: number | null
  image_ratio?: number | null
  audio_ratio?: number | null
  audio_completion_ratio?: number | null
  enable_groups: string[]
  tags?: string
  supported_endpoint_types?: string[]
  key?: string
  group_ratio?: Record<string, number>
  /** Billing mode (e.g. "tiered_expr") used to flag dynamic pricing */
  billing_mode?: string
  /** Raw expression describing dynamic / tiered billing */
  billing_expr?: string
  /** Pricing version returned by backend, useful for cache busting */
  pricing_version?: string
  // --- Model metadata (optional; backend may not return these yet) ---
  /** Supported input modalities. */
  input_modalities?: Modality[]
  /** Supported output modalities. */
  output_modalities?: Modality[]
  /** Capability flags reported for the model. */
  capabilities?: ModelCapability[]
  /** Maximum input context window, in tokens. */
  context_length?: number
  /** Maximum tokens per response. */
  max_output_tokens?: number
  /** Knowledge cutoff (YYYY-MM or YYYY-MM-DD). */
  knowledge_cutoff?: string
  /** Release date (YYYY-MM or YYYY-MM-DD). */
  release_date?: string
  /** Parameter count bucket (e.g. "70B"). */
  parameter_count?: string
}

export type PricingData = {
  success: boolean
  message?: string
  data: PricingModel[]
  vendors: PricingVendor[]
  group_ratio: Record<string, number>
  /** 全局原价倍率（未经用户分组专属倍率覆盖），用于"默认价"对比 */
  base_group_ratio?: Record<string, number>
  usable_group: Record<string, { desc: string; ratio: number }>
  supported_endpoint: Record<string, string>
  auto_groups: string[]
}

export type TokenUnit = 'M' | 'K'
export type PriceType =
  | 'input'
  | 'output'
  | 'cache'
  | 'create_cache'
  | 'image'
  | 'audio_input'
  | 'audio_output'
export type QuotaType = 0 | 1 // 0: token-based, 1: per-request

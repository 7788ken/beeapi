// ----------------------------------------------------------------------------
// Price Changes Types (contract: GET /api/price_changes & admin endpoints)
// ----------------------------------------------------------------------------

export type PriceChangeDirection = 'up' | 'down' | 'added' | 'removed'
export type PriceChangeScope = 'group' | 'model'
export type PriceChangeUnit = 'per_1m' | 'per_call'

export type PriceChangePriceType =
  | 'group_ratio'
  | 'group_group_ratio'
  | 'model_ratio'
  | 'completion_ratio'
  | 'cache_ratio'
  | 'create_cache_ratio'
  | 'model_price'

export interface PriceChangeDisplayEntry {
  old_usd: number
  new_usd: number
  unit: PriceChangeUnit
}

export interface PriceChangeItem {
  scope: PriceChangeScope
  group_name: string
  model_name: string
  price_type: PriceChangePriceType
  old_value: number
  new_value: number
  direction: PriceChangeDirection
  affected_groups: string[]
  /** Per-group effective USD price (old -> new), keyed by group name */
  display: Record<string, PriceChangeDisplayEntry>
}

export interface PriceChangeSummary {
  up: number
  down: number
  added: number
  removed: number
}

export interface PriceChangeBatch {
  id: number
  published_at: number // unix seconds
  note: string
  affected_groups: string[]
  summary: PriceChangeSummary
  items: PriceChangeItem[]
}

export interface PriceChangesData {
  enabled: boolean
  badge_days: number
  batches: PriceChangeBatch[]
}

// ---------------------------------------------------------------------------
// Admin
// ---------------------------------------------------------------------------

export interface PendingPriceChanges {
  has_changes: boolean
  affected_groups: string[]
  group_count: number
  model_count: number
  estimated_email_users: number
  items: PriceChangeItem[]
}

export type PriceChangeEmailState = 'none' | 'sending' | 'done' | 'partial'

export interface PriceChangeBatchMeta {
  id: number
  published_at: number
  operator_id: number
  note: string
  affected_groups: string[]
  summary: PriceChangeSummary
  is_baseline: boolean
  email_state: PriceChangeEmailState
  email_total: number
  email_sent: number
  email_failed: number
  /** Only present on GET /api/price_changes/batches/:id */
  items?: PriceChangeItem[]
}

export interface PriceChangeBatchesPage {
  items: PriceChangeBatchMeta[]
  total: number
}

/** Latest in-window change of a single model, used for pricing page badges */
export interface ModelPriceChange {
  batchId: number
  publishedAt: number
  direction: 'up' | 'down'
  /** All up/down items for this model in that batch */
  items: PriceChangeItem[]
}

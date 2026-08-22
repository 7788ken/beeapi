// ----------------------------------------------------------------------------
// Rankings types
// ----------------------------------------------------------------------------
//
// Shape of the real data shown on the /rankings page.

export type RankingPeriod = 'today' | 'week' | 'month' | 'year' | 'all'

export type RankingCategoryId =
  | 'all'
  | 'programming'
  | 'roleplay'
  | 'marketing'
  | 'translation'
  | 'science'
  | 'finance'
  | 'health'
  | 'legal'
  | 'education'
  | 'productivity'
  | 'multimodal'

export type ModelRanking = {
  rank: number
  /** Previous rank in the same period; undefined means "new". */
  previous_rank?: number
  model_name: string
  vendor: string
  vendor_icon?: string
  category: RankingCategoryId
  /** Total tokens routed through this model in the period. */
  total_tokens: number
  /** Share of all tokens served (0..1). */
  share: number
  /** Period-over-period change in token volume (%). */
  growth_pct: number
}

export type VendorRanking = {
  rank: number
  vendor: string
  vendor_icon?: string
  total_tokens: number
  share: number
  growth_pct: number
  /** Number of distinct models from this vendor with traffic. */
  models_count: number
  /** Top model from this vendor in the period. */
  top_model: string
}

export type RankingMover = {
  model_name: string
  vendor: string
  vendor_icon?: string
  /** Positive = climbed, negative = dropped. */
  rank_delta: number
  current_rank: number
  /** Token-volume change percent. */
  growth_pct: number
}

/**
 * One sample of a model's token usage at a given timestamp.
 * Flat shape ready to feed VChart's stacked-bar spec.
 */
export type ModelHistoryPoint = {
  ts: string
  /** Pre-formatted x-axis label (e.g. "May 5", "12:00"). */
  label: string
  /** Model display name shown in tooltip / legend. */
  model: string
  vendor: string
  /** Token count routed through the model in this bucket. */
  tokens: number
}

export type ModelHistorySeries = {
  /** Flat points ready for VChart, ordered oldest → newest. */
  points: ModelHistoryPoint[]
  /** Models that appear in the series, sorted by total tokens desc. */
  models: Array<{ name: string; vendor: string; total: number }>
  /** Bucket count (used for sizing axis ticks). */
  buckets: number
}

/**
 * One sample of a vendor's market share at a given timestamp. `share` is
 * normalised within the bucket (sums to 1.0 across all vendors at the same
 * `ts`); `tokens` is preserved for tooltip use.
 */
export type VendorSharePoint = {
  ts: string
  label: string
  vendor: string
  share: number
  tokens: number
}

export type VendorShareSeries = {
  /** Flat points ready for VChart, ordered oldest → newest. */
  points: VendorSharePoint[]
  /** Vendors that appear in the series, sorted by aggregate tokens desc. */
  vendors: Array<{ name: string; total: number; share: number }>
  buckets: number
}

export type RankingsSnapshot = {
  // Overall (all categories) ------------------------------------------------
  models: ModelRanking[]
  vendors: VendorRanking[]
  /** Largest rank gainers in this period. */
  top_movers: RankingMover[]
  /** Largest rank losers in this period. */
  top_droppers: RankingMover[]
  /** Stacked-bar history of token usage by model over the period. */
  models_history: ModelHistorySeries
  /** 100%-stacked area history of token share by vendor over the period. */
  vendor_share_history: VendorShareSeries
}

/**
 * One row in the "Top Apps" leaderboard — an application consuming tokens
 * through new-api during the active period.
 */
export type AppListing = {
  rank: number
  name: string
  /** Single-letter avatar fallback shown when no icon is available. */
  initial: string
  /** Optional external link to the app. */
  url?: string
  /** Display category label (e.g. "Coding", "Writing"). */
  category: string
  description: string
  total_tokens: number
  /** Period-over-period change in token volume (%). */
  growth_pct: number
}

/**
 * One per-category ranking unit: a stacked-bar usage history plus the top
 * models in that category. Rendered as a self-contained card.
 */
export type CategorySection = {
  category: RankingCategoryId
  /** Section heading (i18n key). */
  label: string
  /** Section subheading (i18n key). */
  description: string
  total_tokens: number
  /** Stacked-bar history of token usage by model in this category. */
  models_history: ModelHistorySeries
  /** Top models in this category. */
  models: ModelRanking[]
}

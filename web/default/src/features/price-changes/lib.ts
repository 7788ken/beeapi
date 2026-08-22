import type {
  ModelPriceChange,
  PriceChangeDisplayEntry,
  PriceChangeItem,
  PriceChangesData,
} from './types'

// ----------------------------------------------------------------------------
// Price Changes derivation helpers
// ----------------------------------------------------------------------------

/** Priority used to pick the primary changed field of a model */
const PRICE_TYPE_PRIORITY = [
  'model_price',
  'model_ratio',
  'completion_ratio',
  'cache_ratio',
  'create_cache_ratio',
]

function priceTypeRank(priceType: string): number {
  const idx = PRICE_TYPE_PRIORITY.indexOf(priceType)
  return idx === -1 ? PRICE_TYPE_PRIORITY.length : idx
}

export function badgeWindowStart(badgeDays: number, now = Date.now()): number {
  const days = badgeDays > 0 ? badgeDays : 7
  return Math.floor(now / 1000) - days * 86400
}

/**
 * Build model_name -> latest in-window up/down change map for pricing badges.
 * Only the newest batch (within badge_days) mentioning a model wins.
 */
export function buildModelChangeMap(
  data: PriceChangesData | null | undefined
): Map<string, ModelPriceChange> {
  const map = new Map<string, ModelPriceChange>()
  if (!data?.enabled || !Array.isArray(data.batches)) return map

  const windowStart = badgeWindowStart(data.badge_days)
  const batches = [...data.batches]
    .filter((b) => b.published_at >= windowStart)
    .sort((a, b) => b.published_at - a.published_at)

  for (const batch of batches) {
    const byModel = new Map<string, PriceChangeItem[]>()
    for (const item of batch.items ?? []) {
      if (item.scope !== 'model' || !item.model_name) continue
      if (item.direction !== 'up' && item.direction !== 'down') continue
      const list = byModel.get(item.model_name) ?? []
      list.push(item)
      byModel.set(item.model_name, list)
    }

    for (const [modelName, items] of byModel) {
      if (map.has(modelName)) continue // an earlier (newer) batch already won
      const sorted = [...items].sort(
        (a, b) => priceTypeRank(a.price_type) - priceTypeRank(b.price_type)
      )
      map.set(modelName, {
        batchId: batch.id,
        publishedAt: batch.published_at,
        direction: sorted[0].direction as 'up' | 'down',
        items: sorted,
      })
    }
  }

  return map
}

export interface RecentGroupChange {
  batchId: number
  publishedAt: number
  item: PriceChangeItem
}

/**
 * Group-scope changes within the badge window that affect the given group.
 * Pass 'all' (or empty) to match every group. Deduped to the latest change
 * per group_name + price_type.
 */
export function findRecentGroupChanges(
  data: PriceChangesData | null | undefined,
  group: string
): RecentGroupChange[] {
  if (!data?.enabled || !Array.isArray(data.batches)) return []

  const windowStart = badgeWindowStart(data.badge_days)
  const batches = [...data.batches]
    .filter((b) => b.published_at >= windowStart)
    .sort((a, b) => b.published_at - a.published_at)

  const seen = new Set<string>()
  const result: RecentGroupChange[] = []
  for (const batch of batches) {
    for (const item of batch.items ?? []) {
      if (item.scope !== 'group' || !item.group_name) continue
      if (group && group !== 'all' && item.group_name !== group) continue
      const key = `${item.group_name}:${item.price_type}`
      if (seen.has(key)) continue
      seen.add(key)
      result.push({
        batchId: batch.id,
        publishedAt: batch.published_at,
        item,
      })
    }
  }
  return result
}

export interface ResolvedDisplay {
  group: string
  entry: PriceChangeDisplayEntry
}

/**
 * Pick the display entry (effective USD old -> new) for the preferred group,
 * falling back to the first available group in the display map.
 */
export function resolveDisplayEntry(
  item: PriceChangeItem,
  preferredGroup?: string
): ResolvedDisplay | null {
  const display = item.display ?? {}
  if (preferredGroup && display[preferredGroup]) {
    return { group: preferredGroup, entry: display[preferredGroup] }
  }
  const firstGroup = Object.keys(display)[0]
  if (firstGroup) return { group: firstGroup, entry: display[firstGroup] }
  return null
}

/** Compact USD formatting for tooltip / table cells */
export function formatUsd(value: number): string {
  if (!Number.isFinite(value)) return '-'
  const abs = Math.abs(value)
  const digits = abs >= 100 ? 2 : abs >= 1 ? 3 : 4
  const text = value.toFixed(digits).replace(/\.?0+$/, '')
  return `$${text === '' || text === '-' ? '0' : text}`
}

/** Plain ratio value formatting (group ratios, fallback when no display) */
export function formatRatioValue(value: number): string {
  if (!Number.isFinite(value)) return '-'
  return String(Math.round(value * 10000) / 10000)
}

/**
 * group_group_ratio items carry a composite group_name ("default->vip").
 * Returns the two sides so the UI can render them readably.
 */
export function parseGroupGroupRatioName(
  groupName: string
): { userGroup: string; targetGroup: string } | null {
  const idx = groupName.indexOf('->')
  if (idx <= 0) return null
  return {
    userGroup: groupName.slice(0, idx),
    targetGroup: groupName.slice(idx + 2),
  }
}

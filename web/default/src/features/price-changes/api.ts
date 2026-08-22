import { api } from '@/lib/api'
import type {
  PendingPriceChanges,
  PriceChangeBatchMeta,
  PriceChangeBatchesPage,
  PriceChangesData,
} from './types'

// ----------------------------------------------------------------------------
// Price Changes APIs
// ----------------------------------------------------------------------------

const silentConfig = {
  skipErrorHandler: true,
  skipBusinessError: true,
} as unknown as Parameters<typeof api.get>[1]

/**
 * User-side price changes feed (works for both logged-in and anonymous users).
 * Fails silently (returns null) so the pricing page never breaks on errors.
 */
export async function getPriceChanges(
  days = 30
): Promise<PriceChangesData | null> {
  try {
    const res = await api.get('/api/price_changes', {
      ...silentConfig,
      params: { days },
    })
    const body = res.data as { success?: boolean; data?: PriceChangesData }
    if (!body?.success || !body.data) return null
    return body.data
  } catch {
    return null
  }
}

/** Admin: real-time unpublished diff preview */
export async function getPendingPriceChanges(): Promise<PendingPriceChanges> {
  const res = await api.get('/api/price_changes/pending', silentConfig)
  const body = res.data as { success?: boolean; data?: PendingPriceChanges }
  if (!body?.success || !body.data) {
    throw new Error('Failed to load pending price changes')
  }
  return body.data
}

/** Admin: publish current pending changes */
export async function publishPriceChanges(payload: {
  note: string
  send_email: boolean
}): Promise<{ success: boolean; message?: string; data?: { batch_id: number } }> {
  const res = await api.post('/api/price_changes/publish', payload)
  return res.data
}

/** Admin: paginated publish history */
export async function getPriceChangeBatches(
  page = 1,
  pageSize = 10
): Promise<PriceChangeBatchesPage> {
  const res = await api.get('/api/price_changes/batches', {
    params: { p: page, page_size: pageSize },
  })
  const body = res.data as { success?: boolean; data?: PriceChangeBatchesPage }
  return body?.data ?? { items: [], total: 0 }
}

/** Admin: single batch with items (used to poll email progress) */
export async function getPriceChangeBatch(
  id: number
): Promise<PriceChangeBatchMeta | null> {
  const res = await api.get(`/api/price_changes/batches/${id}`, silentConfig)
  const body = res.data as { success?: boolean; data?: PriceChangeBatchMeta }
  if (!body?.success || !body.data) return null
  return body.data
}

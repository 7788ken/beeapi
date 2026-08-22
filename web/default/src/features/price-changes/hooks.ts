import { useQuery } from '@tanstack/react-query'
import { useAuthStore } from '@/stores/auth-store'
import { getPriceChanges } from './api'

export const PRICE_CHANGES_QUERY_KEY = ['price-changes', 30] as const

/**
 * Shared user-side price changes feed (pricing page badges/banner/drawer and
 * the notification bell all read from this one cached query).
 * Errors degrade silently: data is null and consumers render nothing.
 */
export function usePriceChanges() {
  return useQuery({
    queryKey: PRICE_CHANGES_QUERY_KEY,
    queryFn: () => getPriceChanges(30),
    staleTime: 5 * 60 * 1000,
    retry: false,
  })
}

/**
 * The signed-in viewer's own group, used as the default preferredGroup so
 * effective prices are always shown from the viewer's perspective instead of
 * an arbitrary first key of the display map.
 */
export function useViewerGroup(): string | undefined {
  return useAuthStore((state) => state.auth.user?.group) || undefined
}

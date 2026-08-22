import { useState, useMemo, useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNotificationStore } from '@/stores/notification-store'
import { getNotice } from '@/lib/api'
import { useStatus } from '@/hooks/use-status'
import { usePriceChanges, useViewerGroup } from '@/features/price-changes/hooks'

export type NotificationTab = 'notice' | 'announcements' | 'price-changes'

function hashString(input: string): string {
  let hash = 0
  if (!input) return '0'

  for (let i = 0; i < input.length; i += 1) {
    const chr = input.charCodeAt(i)
    hash = (hash << 5) - hash + chr
    hash |= 0
  }

  return hash.toString(36)
}

/**
 * Generate a unique key for an announcement
 * Prefer backend id, fall back to a content hash so edits register
 */
function getAnnouncementKey(item: Record<string, unknown>): string {
  if (!item) return ''

  if (item.id !== undefined && item.id !== null) {
    return `id:${item.id}`
  }

  const fingerprint = JSON.stringify({
    publishDate: (item?.publishDate as string) || '',
    content: ((item?.content as string) || '').trim(),
    extra: ((item?.extra as string) || '').trim(),
    type: (item?.type as string) || '',
    title: ((item?.title as string) || '').trim(),
    link: ((item?.link as string) || '').trim(),
  })
  return `hash:${hashString(fingerprint)}`
}

/**
 * Hook to manage notifications (Notice + Announcements)
 * Provides unread counts and read status management
 */
export function useNotifications() {
  const [dialogOpen, setDialogOpen] = useState(false)
  const [activeTab, setActiveTab] = useState<NotificationTab>('notice')

  // Fetch Notice from API
  const {
    data: noticeResponse,
    isLoading: noticeLoading,
    refetch: refetchNotice,
  } = useQuery({
    queryKey: ['notice'],
    queryFn: getNotice,
    staleTime: 1000 * 60 * 5, // 5 minutes
  })

  // Fetch Announcements from status
  const { status, loading: statusLoading } = useStatus()
  const announcementsEnabled = status?.announcements_enabled ?? false
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const announcements: Record<string, unknown>[] = announcementsEnabled
    ? ((status?.announcements || []) as Record<string, unknown>[]).slice(0, 20)
    : []

  // Notification store
  const {
    lastReadNotice,
    markNoticeRead,
    markAnnouncementsRead,
    isAnnouncementRead,
    isNoticeClosed,
    setClosedUntilDate,
    readPriceBatchIds,
    markPriceBatchesRead,
  } = useNotificationStore()

  // Price change batches (silent degradation: null data hides the tab)
  const priceChangesQuery = usePriceChanges()
  const viewerGroup = useViewerGroup()
  const priceChangesEnabled = priceChangesQuery.data?.enabled === true
  const priceChangeBatches = useMemo(
    () => (priceChangesEnabled ? (priceChangesQuery.data?.batches ?? []) : []),
    [priceChangesEnabled, priceChangesQuery.data]
  )

  // Extract notice content
  const noticeContent = noticeResponse?.success
    ? (noticeResponse.data || '').trim()
    : ''

  // Calculate unread counts
  const unreadCounts = useMemo(() => {
    const noticeUnread =
      noticeContent && noticeContent !== lastReadNotice ? 1 : 0

    const announcementsUnread = announcements.filter(
      (item: Record<string, unknown>) => {
        const key = getAnnouncementKey(item)
        return !isAnnouncementRead(key)
      }
    ).length

    const readBatchIds = readPriceBatchIds ?? []
    const priceChangesUnread = priceChangeBatches.filter(
      (batch) => !readBatchIds.includes(batch.id)
    ).length

    return {
      notice: noticeUnread,
      announcements: announcementsUnread,
      priceChanges: priceChangesUnread,
      total: noticeUnread + announcementsUnread + priceChangesUnread,
    }
  }, [
    noticeContent,
    lastReadNotice,
    announcements,
    isAnnouncementRead,
    priceChangeBatches,
    readPriceBatchIds,
  ])

  // Handle dialog open
  const handleOpenDialog = (tab?: NotificationTab) => {
    // Mark Notice as read when opening dialog
    if (noticeContent) {
      markNoticeRead(noticeContent)
    }

    if (tab === 'price-changes' && priceChangeBatches.length > 0) {
      markPriceBatchesRead(priceChangeBatches.map((batch) => batch.id))
    }

    setActiveTab(tab || 'notice')
    setDialogOpen(true)
  }

  // Handle tab change - mark announcements as read when switching to that tab
  const handleTabChange = (tab: NotificationTab) => {
    setActiveTab(tab)

    if (tab === 'announcements' && announcements.length > 0) {
      const allKeys = announcements.map((item: Record<string, unknown>) =>
        getAnnouncementKey(item)
      )
      markAnnouncementsRead(allKeys)
    }

    if (tab === 'price-changes' && priceChangeBatches.length > 0) {
      markPriceBatchesRead(priceChangeBatches.map((batch) => batch.id))
    }
  }

  // Handle "Close Today" action
  const handleCloseToday = () => {
    const today = new Date().toDateString()
    setClosedUntilDate(today)
    setDialogOpen(false)
  }

  // Auto-popup: proactively open the dialog on the price-changes tab once per
  // mount when the viewer has unread price change batches. Opening marks them
  // read (same semantics as a manual open), so the same batches never pop again.
  const autoOpenedPriceChangesRef = useRef(false)
  useEffect(() => {
    if (autoOpenedPriceChangesRef.current) return
    if (dialogOpen) {
      // User is already interacting with notifications this session; stay quiet.
      autoOpenedPriceChangesRef.current = true
      return
    }
    if (!priceChangesEnabled || priceChangesQuery.isLoading) return
    if (unreadCounts.priceChanges <= 0) return
    autoOpenedPriceChangesRef.current = true
    markPriceBatchesRead(priceChangeBatches.map((batch) => batch.id))
    setActiveTab('price-changes')
    setDialogOpen(true)
  }, [
    dialogOpen,
    priceChangesEnabled,
    priceChangesQuery.isLoading,
    unreadCounts.priceChanges,
    priceChangeBatches,
    markPriceBatchesRead,
  ])

  return {
    // Data
    notice: noticeContent,
    announcements,
    loading: noticeLoading || statusLoading,
    priceChangeBatches,
    priceChangesEnabled,
    priceChangesLoading: priceChangesQuery.isLoading,
    viewerGroup,

    // Unread counts
    unreadCount: unreadCounts.total,
    unreadNoticeCount: unreadCounts.notice,
    unreadAnnouncementsCount: unreadCounts.announcements,
    unreadPriceChangesCount: unreadCounts.priceChanges,

    // Dialog state
    dialogOpen,
    setDialogOpen,
    activeTab,
    setActiveTab: handleTabChange,

    // Actions
    openDialog: handleOpenDialog,
    closeDialog: () => setDialogOpen(false),
    closeToday: handleCloseToday,
    refetchNotice,

    // Status
    isNoticeClosed: isNoticeClosed(),
  }
}

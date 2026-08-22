import { useState, useEffect, useCallback } from 'react'
import { getMyInvitees } from '../api'
import type { InviteesData } from '../types'

// ============================================================================
// Invitees Hook — 拉取我的被推荐人列表 + 分成统计
// ============================================================================

export function useInvitees() {
  const [data, setData] = useState<InviteesData | null>(null)
  const [loading, setLoading] = useState(true)

  const fetchInvitees = useCallback(async () => {
    try {
      setLoading(true)
      const response = await getMyInvitees()
      if (response.success && response.data) {
        setData(response.data)
      }
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Failed to fetch invitees:', error)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchInvitees()
  }, [fetchInvitees])

  return {
    data,
    loading,
    refetch: fetchInvitees,
  }
}

import { api } from '@/lib/api'
import type {
  AffiliateCodeResponse,
  AffiliateTransferRequest,
  AffiliateTransferResponse,
  InviteesResponse,
} from './types'

// ============================================================================
// Referral API Functions
// ============================================================================

/**
 * Get affiliate code
 */
export async function getAffiliateCode(): Promise<AffiliateCodeResponse> {
  const res = await api.get('/api/user/aff')
  return res.data
}

/**
 * Transfer affiliate quota to balance
 */
export async function transferAffiliateQuota(
  request: AffiliateTransferRequest
): Promise<AffiliateTransferResponse> {
  const res = await api.post('/api/user/aff_transfer', request)
  return res.data
}

/**
 * Get the list of users I invited with cumulative commission stats
 */
export async function getMyInvitees(): Promise<InviteesResponse> {
  const res = await api.get('/api/user/aff/users')
  return res.data
}

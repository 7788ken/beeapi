// ============================================================================
// Referral Type Definitions
// ============================================================================

export interface ApiResponse<T = unknown> {
  success?: boolean
  message?: string
  data?: T
}

export type AffiliateCodeResponse = ApiResponse<string>
export type AffiliateTransferResponse = ApiResponse

export interface AffiliateTransferRequest {
  /** Quota amount to transfer */
  quota: number
}

/**
 * Referral user data (subset of user with affiliate fields)
 */
export interface ReferralUser {
  id: number
  username: string
  /** Affiliate quota (pending rewards) */
  aff_quota: number
  /** Total affiliate quota earned (historical) */
  aff_history_quota: number
  /** Number of successful affiliate invites */
  aff_count: number
}

/**
 * 单个被推荐用户（含累计分成）
 */
export interface Invitee {
  id: number
  username: string
  display_name: string
  created_at: number
  used_quota: number
  status: number
  /** 累计给我的分成 quota */
  total_commission: number
  /** 该用户被分成的累计余额消费 quota */
  total_consume: number
}

export interface InviteesData {
  invitees: Invitee[]
  /** 当前分成比例 (0-1) */
  commission_ratio: number
  /** 分成功能是否开启 */
  enabled: boolean
}

export type InviteesResponse = ApiResponse<InviteesData>

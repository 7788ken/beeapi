// ============================================================================
// Wallet Type Definitions
// ============================================================================

/**
 * Generic API response
 */
export interface ApiResponse<T = unknown> {
  success?: boolean
  message?: string
  data?: T
}

/**
 * Standard API response types
 */
export type TopupInfoResponse = ApiResponse<TopupInfo>
export type RedemptionResponse = ApiResponse<number>
export type AmountResponse = ApiResponse<string>
export type PaymentResponse = ApiResponse<Record<string, unknown>> & {
  url?: string
}
export type StripePaymentResponse = ApiResponse<{ pay_link: string }>
export type CreemPaymentResponse = ApiResponse<{ checkout_url: string }>
export type WaffoPaymentResponse = ApiResponse<
  { payment_url?: string } | string
>
export type WaffoPancakePaymentResponse = ApiResponse<
  | {
      checkout_url?: string
      session_id?: string
      expires_at?: number | string
      order_id?: string
    }
  | string
>
export type CryptomusPaymentResponse = ApiResponse<
  | {
      checkout_url?: string
      uuid?: string
      order_id?: string
    }
  | string
>
export type AgouPaymentResponse = ApiResponse<
  | {
      url?: string
      order_id?: string
    }
  | string
>

/**
 * Creem product configuration
 */
export interface CreemProduct {
  /** Product display name */
  name: string
  /** Creem product ID */
  productId: string
  /** Product price */
  price: number
  /** Quota amount to credit */
  quota: number
  /** Currency (USD or EUR) */
  currency: 'USD' | 'EUR'
}

/**
 * Creem payment request
 */
export interface CreemPaymentRequest {
  /** Creem product ID */
  product_id: string
  /** Payment method identifier */
  payment_method: 'creem'
}

/**
 * Payment method configuration
 */
export interface PaymentMethod {
  /** Display name of payment method */
  name: string
  /** Payment method type identifier */
  type: string
  /** Optional color for UI display */
  color?: string
  /** Minimum topup amount for this payment method */
  min_topup?: number
  /** Optional icon URL provided by backend (preferred over built-in icons) */
  icon?: string
}

/**
 * Waffo payment method configuration
 */
export interface WaffoPayMethod {
  /** Display name of payment method */
  name: string
  /** Optional icon path */
  icon?: string
  /** Waffo pay method type */
  payMethodType?: string
  /** Waffo pay method name */
  payMethodName?: string
}

/**
 * Agou payment method configuration（支付宝/微信，前端按 type 复用现成图标）
 */
export interface AgouPayMethod {
  /** Display name (English source, localized via i18n) */
  name: string
  /** Frontend icon key: 'alipay' | 'wxpay' */
  type: string
  /** agou payType code (e.g. ZFBPAY) — backend only, not sent by frontend */
  pay_type: string
  /** Whether this method is currently usable (微信 false until channel bound) */
  enabled: boolean
}

/**
 * 支付平台的单个支付渠道（充值页展示用，不含后端内部网关参数）
 */
export interface PayProviderChannel {
  /** 渠道唯一键：alipay / usdt_trc20 / card ... */
  key: string
  /** 显示名 */
  name: string
  /** 图标键（react-icons，如 alipay/tether）或图片 URL（/pay-card.png） */
  icon: string
}

/**
 * 支付平台（Waffo Pancake / Cryptomus / Agou），含其后台可配置的支付渠道。
 */
export interface PayProvider {
  /** 平台 id：waffo_pancake / cryptomus / agou */
  id: string
  /** 平台显示名 */
  name: string
  /** 平台 logo：图片 URL 或 base64 dataURL（卡片优先展示，空则回退首个渠道图标） */
  logo?: string
  /** 该平台最低充值额 */
  min_topup: number
  /** 该平台最高充值额（0=无上限） */
  max_topup: number
  /** 当前用户分组是否被禁用（置灰不隐藏） */
  blocked_by_group: boolean
  /** 该平台已启用的支付渠道 */
  channels: PayProviderChannel[]
}

/**
 * Topup configuration information
 */
export interface TopupInfo {
  /** Whether online topup is enabled */
  enable_online_topup: boolean
  /** Whether Stripe topup is enabled */
  enable_stripe_topup: boolean
  /** Available payment methods */
  pay_methods: PaymentMethod[]
  /** Minimum topup amount for online topup */
  min_topup: number
  /** Minimum topup amount for Stripe */
  stripe_min_topup: number
  /** Preset amount options */
  amount_options: number[]
  /** Discount rates by amount */
  discount: Record<number, number>
  /** Optional topup link for purchasing codes */
  topup_link?: string
  /** Whether Creem topup is enabled */
  enable_creem_topup?: boolean
  /** Available Creem products */
  creem_products?: CreemProduct[]
  /** Whether Waffo topup is enabled */
  enable_waffo_topup?: boolean
  /** Available Waffo payment methods */
  waffo_pay_methods?: WaffoPayMethod[]
  /** Minimum topup amount for Waffo */
  waffo_min_topup?: number
  /** Whether Waffo Pancake topup is enabled */
  enable_waffo_pancake_topup?: boolean
  /** Minimum topup amount for Waffo Pancake */
  waffo_pancake_min_topup?: number
  /** Whether Cryptomus (crypto) topup is enabled */
  enable_cryptomus_topup?: boolean
  /** Minimum topup amount for Cryptomus */
  cryptomus_min_topup?: number
  /** Whether agou (支付宝/微信) topup is enabled */
  enable_sfpay_topup?: boolean
  /** Available agou payment methods (支付宝/微信) */
  sfpay_pay_methods?: AgouPayMethod[]
  /** Minimum topup amount for agou */
  sfpay_min_topup?: number
  /** Maximum topup amount for agou (0 = no extra cap) */
  sfpay_max_topup?: number
  /** 当前用户分组不可用的支付方式 type 列表（前端置灰显示但禁用，不隐藏） */
  group_blocked_methods?: string[]
  /** 统一支付平台列表（Waffo Pancake | Cryptomus | Agou），充值页两层选择用 */
  providers?: PayProvider[]
}

/**
 * 步骤 2 的统一选中模型 — 标准方式（epay/stripe/cryptomus 等）与 Waffo 方式
 * 共用一套 radio 语义；发起支付时按 kind 分流（standard 走确认弹窗，waffo 直发）。
 */
export type SelectedPayMethod =
  | { kind: 'standard'; method: PaymentMethod }
  | { kind: 'waffo'; method: WaffoPayMethod; index: number }
  | { kind: 'sfpay'; method: AgouPayMethod }
  | { kind: 'provider'; provider: PayProvider }

/**
 * Preset amount option with optional discount
 */
export interface PresetAmount {
  /** Preset amount value */
  value: number
  /** Optional discount rate (0-1) */
  discount?: number
}

/**
 * Redemption code request
 */
export interface RedemptionRequest {
  /** Redemption code key */
  key: string
}

/**
 * Payment request parameters
 */
export interface PaymentRequest {
  /** Topup amount */
  amount: number
  /** Payment method identifier */
  payment_method: string
}

/**
 * Waffo payment request parameters
 */
export interface WaffoPaymentRequest {
  /** Topup amount */
  amount: number
  /** Optional server-side Waffo payment method index */
  pay_method_index?: number
}

/**
 * Waffo Pancake payment request parameters
 */
export interface WaffoPancakePaymentRequest {
  /** Topup amount */
  amount: number
}

/**
 * Cryptomus payment request parameters
 */
export interface CryptomusPaymentRequest {
  /** Topup amount */
  amount: number
  /** 选中的加密货币渠道 key（可选，后端据此锁定 to_currency/network） */
  channel_key?: string
}

/**
 * Agou payment request parameters
 */
export interface AgouPaymentRequest {
  /** Topup amount */
  amount: number
  /** 旧字段：前端方式 type ('alipay' | 'wxpay')；后端映射为 agou payType */
  pay_type?: string
  /** 新字段：两层选择的渠道 key（与 pay_type 同源，后端优先取此值） */
  channel_key?: string
}

/**
 * Amount calculation request
 */
export interface AmountRequest {
  /** Topup amount to calculate */
  amount: number
}

/**
 * User wallet data
 */
export interface UserWalletData {
  /** User ID */
  id: number
  /** Username */
  username: string
  /** Current quota balance */
  quota: number
  /** Total used quota */
  used_quota: number
  /** Total request count */
  request_count: number
  /** Affiliate quota (pending rewards) */
  aff_quota: number
  /** Total affiliate quota earned (historical) */
  aff_history_quota: number
  /** Number of successful affiliate invites */
  aff_count: number
  /** User group */
  group: string
}

/**
 * Topup record status
 */
export type TopupStatus = 'success' | 'pending' | 'expired'

/**
 * Topup billing record
 */
export interface TopupRecord {
  /** Record ID */
  id: number
  /** User ID */
  user_id: number
  /** Topup amount (quota) */
  amount: number
  /** Payment amount (actual money paid) */
  money: number
  /** Trade/order number */
  trade_no: string
  /** Payment method type */
  payment_method: string
  /** Creation timestamp */
  create_time: number
  /** Completion timestamp */
  complete_time?: number
  /** Payment status */
  status: TopupStatus
}

/**
 * Billing history response
 */
export interface BillingHistoryResponse {
  items: TopupRecord[]
  total: number
}

/**
 * Complete order request (admin only)
 */
export interface CompleteOrderRequest {
  trade_no: string
}

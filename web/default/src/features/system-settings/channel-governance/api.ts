import { api } from '@/lib/api'

/**
 * 渠道治理的两个只读观测接口。
 *
 * 后端实现在 controller/channel_governance.go，都不写库、不改渠道状态、不触发处置、不发通知。
 * 分布只查 channels 表（百行级），与扫 logs 表的用量统计（/api/channel/statistics）刻意分开。
 */

/** 一个降级档位。边界跟 channel_health_setting.max_degrade_level 走，所以标签必须用 min/max 渲染，不能写死。 */
export type GovernanceDegradeBucket = {
  key: 'healthy' | 'light' | 'moderate' | 'deep' | 'max'
  min_level: number
  max_level: number
  count: number
}

export type GovernanceRemovalStats = {
  total_models: number
  channels: number
  /** model_missing / rate_limit / forbidden / unknown */
  by_reason: Record<string, number>
  /** 原因未知的模型数：原因表是后加的，之前摘掉的模型只记了名字 */
  unknown_reason_models: number
  /** 所有渠道里最早的一次复核时间（unix 秒）；0=当前无待复核 */
  next_recheck_at: number
}

export type ChannelGovernanceDistribution = {
  generated_at: number
  total_channels: number
  enabled: number
  max_degrade_level: number
  /** 各档之和恒等于 total_channels（停用不清 degrade_level，所以已停用渠道也在档里） */
  degrade_buckets: GovernanceDegradeBucket[]
  degraded_channels: number
  auto_disabled: number
  /** auto_disabled 的子集，不要与它相加 */
  permanent_disabled: number
  manually_disabled: number
  /** 被测评低分禁用；巡检会跳过这些渠道，实践中也是 auto_disabled 的子集 */
  verify_disabled: number
  removed_models: GovernanceRemovalStats
}

export async function getGovernanceDistribution(): Promise<{
  success: boolean
  message?: string
  data?: ChannelGovernanceDistribution
}> {
  const res = await api.get('/api/channel/governance/distribution')
  return res.data
}

export type GovernanceDryRunRequest = {
  status_code: number
  error_message: string
  model?: string
}

export type GovernanceDryRunResult = {
  status_code: number
  model: string
  /** 三条摘除路径的原始分类，不含各自的开关 */
  verdicts: {
    model_missing: boolean
    forbidden: boolean
    rate_limit: boolean
  }
  removal: {
    matched: boolean
    reason: string
    requires_upstream_verify: boolean
    recheck_interval_seconds: number
    cap_applies: boolean
    cap_value: number
    cap_action: string
    model_provided: boolean
  }
  channel_disable: {
    would_disable_channel: boolean
    automatic_disable_channel_enabled: boolean
    short_circuited_by_removal: boolean
    /** 恒为 true：渠道级 auto_ban 是渠道属性，只有状态码和文案时无从判定 */
    auto_ban_not_evaluated: boolean
  }
  degrade_streak: {
    countable: boolean
    channel_health_enabled: boolean
    effective: boolean
  }
  retry: {
    /** 只覆盖状态码那道闸门；重试次数余量、亲和性、重试作用域属请求上下文，不在试算范围 */
    status_code_retryable: boolean
  }
}

export async function postGovernanceDryRun(
  payload: GovernanceDryRunRequest
): Promise<{
  success: boolean
  message?: string
  data?: GovernanceDryRunResult
}> {
  const res = await api.post('/api/channel/governance/dry_run', payload)
  return res.data
}

import { z } from 'zod'
import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'
import { DEFAULT_GROUP } from '../constants'
import { type ApiKeyFormData, type ApiKey } from '../types'

// ============================================================================
// Form Schema
// ============================================================================

export const apiKeyFormSchema = z
  .object({
    name: z.string().min(1, 'Name is required'),
    remain_quota_dollars: z.number().optional(),
    expired_time: z.date().optional(),
    unlimited_quota: z.boolean(),
    model_limits: z.array(z.string()),
    allow_ips: z.string().optional(),
    group: z.string().optional(),
    cross_group_retry: z.boolean().optional(),
    relay_retry_policy: z
      .enum([
        'system',
        'disabled',
        'cache_domain_only',
        'allow_cross_channel',
      ])
      .optional(),
    tokenCount: z.number().min(1).optional(),
  })
  .superRefine((data, ctx) => {
    // Skip quota validation when unlimited — historical unlimited tokens may
    // carry negative remain_quota (consumption decremented past 0), and editing
    // them shouldn't be blocked by a min(0) check on a field the backend ignores.
    if (data.unlimited_quota) return
    const v = data.remain_quota_dollars
    if (v === undefined || Number.isNaN(v) || v < 0) {
      ctx.addIssue({
        code: 'custom',
        path: ['remain_quota_dollars'],
        message: 'Quota must be a non-negative number',
      })
    }
  })

export type ApiKeyFormValues = z.infer<typeof apiKeyFormSchema>

// ============================================================================
// Form Defaults
// ============================================================================

export const API_KEY_FORM_DEFAULT_VALUES: ApiKeyFormValues = {
  name: '',
  remain_quota_dollars: 10,
  expired_time: undefined,
  unlimited_quota: true,
  model_limits: [],
  allow_ips: '',
  group: DEFAULT_GROUP,
  cross_group_retry: true,
  relay_retry_policy: 'system',
  tokenCount: 1,
}

export function getApiKeyFormDefaultValues(
  defaultUseAutoGroup: boolean
): ApiKeyFormValues {
  return {
    ...API_KEY_FORM_DEFAULT_VALUES,
    group: defaultUseAutoGroup ? 'auto' : DEFAULT_GROUP,
    cross_group_retry: defaultUseAutoGroup,
  }
}

// ============================================================================
// Form Data Transformation
// ============================================================================

/**
 * Transform form data to API payload
 */
export function transformFormDataToPayload(
  data: ApiKeyFormValues
): ApiKeyFormData {
  return {
    name: data.name,
    remain_quota: data.unlimited_quota
      ? 0
      : parseQuotaFromDollars(data.remain_quota_dollars || 0),
    expired_time: data.expired_time
      ? Math.floor(data.expired_time.getTime() / 1000)
      : -1,
    unlimited_quota: data.unlimited_quota,
    model_limits_enabled: data.model_limits.length > 0,
    model_limits: data.model_limits.join(','),
    allow_ips: data.allow_ips || '',
    group: data.group || '',
    cross_group_retry: data.group === 'auto' ? !!data.cross_group_retry : false,
    relay_retry_policy: data.relay_retry_policy || 'system',
  }
}

/**
 * Transform API key data to form defaults
 */
export function transformApiKeyToFormDefaults(
  apiKey: ApiKey
): ApiKeyFormValues {
  return {
    name: apiKey.name,
    remain_quota_dollars: quotaUnitsToDollars(apiKey.remain_quota),
    expired_time:
      apiKey.expired_time > 0
        ? new Date(apiKey.expired_time * 1000)
        : undefined,
    unlimited_quota: apiKey.unlimited_quota,
    model_limits: apiKey.model_limits
      ? apiKey.model_limits.split(',').filter(Boolean)
      : [],
    allow_ips: apiKey.allow_ips || '',
    group: apiKey.group || DEFAULT_GROUP,
    cross_group_retry: !!apiKey.cross_group_retry,
    relay_retry_policy: apiKey.relay_retry_policy || 'system',
    tokenCount: 1,
  }
}

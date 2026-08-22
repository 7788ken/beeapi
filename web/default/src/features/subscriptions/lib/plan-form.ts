import { z } from 'zod'
import type { TFunction } from 'i18next'
import type { SubscriptionPlan, PlanPayload } from '../types'

export function getPlanFormSchema(t: TFunction) {
  return z.object({
    title: z.string().min(1, t('Please enter plan title')),
    subtitle: z.string().optional(),
    description: z.string().max(2000).optional(),
    cover_image_url: z
      .string()
      .max(500, t('URL is too long'))
      .optional()
      .or(z.literal('')),
    price_amount: z.coerce.number().min(0, t('Please enter amount')),
    duration_unit: z.enum(['year', 'month', 'day', 'hour', 'custom']),
    duration_value: z.coerce.number().min(1),
    custom_seconds: z.coerce.number().min(0).optional(),
    quota_reset_period: z.enum([
      'never',
      'daily',
      'weekly',
      'monthly',
      'custom',
    ]),
    quota_reset_custom_seconds: z.coerce.number().min(0).optional(),
    enabled: z.boolean(),
    sort_order: z.coerce.number(),
    max_purchase_per_user: z.coerce.number().min(0),
    stock_total: z.coerce.number().min(-1, t('Stock must be -1 or greater')),
    total_amount: z.coerce.number().min(0),
    upgrade_group: z.string().optional(),
    bound_group: z.string().optional(),
    stripe_price_id: z.string().optional(),
    creem_product_id: z.string().optional(),
  })
}

export type PlanFormValues = z.infer<ReturnType<typeof getPlanFormSchema>>

export const PLAN_FORM_DEFAULTS: PlanFormValues = {
  title: '',
  subtitle: '',
  description: '',
  cover_image_url: '',
  price_amount: 0,
  duration_unit: 'month',
  duration_value: 1,
  custom_seconds: 0,
  quota_reset_period: 'never',
  quota_reset_custom_seconds: 0,
  enabled: true,
  sort_order: 0,
  max_purchase_per_user: 0,
  stock_total: -1,
  total_amount: 0,
  upgrade_group: '',
  bound_group: '',
  stripe_price_id: '',
  creem_product_id: '',
}

export function planToFormValues(plan: SubscriptionPlan): PlanFormValues {
  return {
    title: plan.title || '',
    subtitle: plan.subtitle || '',
    description: plan.description || '',
    cover_image_url: plan.cover_image_url || '',
    price_amount: Number(plan.price_amount || 0),
    duration_unit: plan.duration_unit || 'month',
    duration_value: Number(plan.duration_value || 1),
    custom_seconds: Number(plan.custom_seconds || 0),
    quota_reset_period: plan.quota_reset_period || 'never',
    quota_reset_custom_seconds: Number(plan.quota_reset_custom_seconds || 0),
    enabled: plan.enabled !== false,
    sort_order: Number(plan.sort_order || 0),
    max_purchase_per_user: Number(plan.max_purchase_per_user || 0),
    stock_total:
      plan.stock_total === undefined || plan.stock_total === null
        ? -1
        : Number(plan.stock_total),
    total_amount: Number(plan.total_amount || 0),
    upgrade_group: plan.upgrade_group || '',
    bound_group: plan.bound_group || '',
    stripe_price_id: plan.stripe_price_id || '',
    creem_product_id: plan.creem_product_id || '',
  }
}

export function formValuesToPlanPayload(values: PlanFormValues): PlanPayload {
  return {
    plan: {
      ...values,
      price_amount: Number(values.price_amount || 0),
      currency: 'USD',
      duration_value: Number(values.duration_value || 0),
      custom_seconds: Number(values.custom_seconds || 0),
      quota_reset_period: values.quota_reset_period || 'never',
      quota_reset_custom_seconds:
        values.quota_reset_period === 'custom'
          ? Number(values.quota_reset_custom_seconds || 0)
          : 0,
      sort_order: Number(values.sort_order || 0),
      max_purchase_per_user: Number(values.max_purchase_per_user || 0),
      stock_total: Number(values.stock_total ?? -1),
      total_amount: Number(values.total_amount || 0),
      upgrade_group: values.upgrade_group || '',
      bound_group: values.bound_group || '',
    },
  }
}

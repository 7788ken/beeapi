import { useMemo, useRef } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { parseHttpStatusCodeRules } from '@/lib/http-status-code-rules'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { useSystemConfig } from '@/hooks/use-system-config'

// 阈值后端按内部额度(quota)存储，UI 以美元展示：读入÷、提交×quotaPerUnit
const quotaToUsdString = (quota: string, quotaPerUnit: number): string => {
  const trimmed = quota.trim()
  if (!trimmed) return ''
  const parsed = Number(trimmed)
  if (Number.isNaN(parsed) || quotaPerUnit <= 0) return trimmed
  return String(parsed / quotaPerUnit)
}

const usdToQuotaString = (usd: string, quotaPerUnit: number): string => {
  const trimmed = usd.trim()
  if (!trimmed) return ''
  const parsed = Number(trimmed)
  if (Number.isNaN(parsed) || quotaPerUnit <= 0) return trimmed
  return String(Math.round(parsed * quotaPerUnit))
}

const numericString = z.string().refine((value) => {
  const trimmed = value.trim()
  if (!trimmed) return true
  return !Number.isNaN(Number(trimmed)) && Number(trimmed) >= 0
}, 'Enter a non-negative number or leave empty')

const monitoringSchema = z
  .object({
    ChannelDisableThreshold: numericString,
    QuotaRemindThreshold: numericString,
    AutomaticDisableChannelEnabled: z.boolean(),
    AutomaticEnableChannelEnabled: z.boolean(),
    AutomaticDisableKeywords: z.string(),
    AutomaticDisableStatusCodes: z.string(),
    AutomaticRetryStatusCodes: z.string(),
    monitor_setting: z.object({
      auto_test_channel_enabled: z.boolean(),
      auto_test_channel_minutes: z.coerce
        .number()
        .int()
        .min(1, 'Interval must be at least 1 minute'),
    }),
    channel_health_setting: z.object({
      enabled: z.boolean(),
      base_degrade_threshold: z.coerce.number().int().min(1),
      level_step_threshold: z.coerce.number().int().min(1),
      max_degrade_level: z.coerce.number().int().min(1).max(20),
      min_weight_factor: z.coerce.number().min(0.01).max(1.0),
      max_ttft_ms: z.coerce.number().int().min(0),
      latency_degrade_base: z.coerce.number().int().min(1),
      latency_degrade_step: z.coerce.number().int().min(1),
      count_latency_as_error: z.boolean(),
      // 0=禁用本功能（不再触发自动 disable）
      disable_threshold: z.coerce.number().int().min(0),
      upgrade_threshold: z.coerce.number().int().min(1),
      degrade_probe_minutes: z.coerce.number().int().min(1),
      degrade_probe_count: z.coerce.number().int().min(1),
      count_429_as_error: z.boolean(),
      countable_status_codes: z.string(),
      notify_on_degrade: z.boolean(),
      notify_on_upgrade: z.boolean(),
      streak_window_sec: z.coerce.number().int().min(60),
      // 0=禁用反弹保护功能
      rebounce_protection_minutes: z.coerce.number().int().min(0),
      rebounce_protection_threshold: z.coerce.number().int().min(0),
      demote_cooldown_sec: z.coerce.number().int().min(0),
      recovery_strategy: z.enum(['probe', 'manual']),
      recovery_probe_minutes: z.coerce.number().int().min(1),
      recovery_probe_model: z.string(),
      shadow_sample_rate: z.coerce.number().min(0).max(1.0),
      skip_channel_tags: z.string(),
    }),
  })
  .superRefine((values, ctx) => {
    const disableParsed = parseHttpStatusCodeRules(
      values.AutomaticDisableStatusCodes
    )
    if (!disableParsed.ok) {
      ctx.addIssue({
        code: 'custom',
        path: ['AutomaticDisableStatusCodes'],
        message: `Invalid status code rules: ${disableParsed.invalidTokens.join(
          ', '
        )}`,
      })
    }

    const retryParsed = parseHttpStatusCodeRules(
      values.AutomaticRetryStatusCodes
    )
    if (!retryParsed.ok) {
      ctx.addIssue({
        code: 'custom',
        path: ['AutomaticRetryStatusCodes'],
        message: `Invalid status code rules: ${retryParsed.invalidTokens.join(
          ', '
        )}`,
      })
    }

    const h = values.channel_health_setting

    const countableParsed = parseHttpStatusCodeRules(h.countable_status_codes)
    if (!countableParsed.ok) {
      ctx.addIssue({
        code: 'custom',
        path: ['channel_health_setting', 'countable_status_codes'],
        message: `Invalid status code rules: ${countableParsed.invalidTokens.join(', ')}`,
      })
    }

    // disable_threshold=0 表示禁用本功能
    if (h.disable_threshold > 0 && h.disable_threshold <= h.base_degrade_threshold) {
      ctx.addIssue({
        code: 'custom',
        path: ['channel_health_setting', 'disable_threshold'],
        message: 'Disable threshold must be greater than base degrade threshold (or 0 to disable)',
      })
    }
  })

type MonitoringFormValues = z.output<typeof monitoringSchema>
type MonitoringFormInput = z.input<typeof monitoringSchema>

type MonitoringSettingsSectionProps = {
  defaultValues: {
    ChannelDisableThreshold: string
    QuotaRemindThreshold: string
    AutomaticDisableChannelEnabled: boolean
    AutomaticEnableChannelEnabled: boolean
    AutomaticDisableKeywords: string
    AutomaticDisableStatusCodes: string
    AutomaticRetryStatusCodes: string
    'monitor_setting.auto_test_channel_enabled': boolean
    'monitor_setting.auto_test_channel_minutes': number
    'channel_health_setting.enabled': boolean
    'channel_health_setting.base_degrade_threshold': number
    'channel_health_setting.level_step_threshold': number
    'channel_health_setting.max_degrade_level': number
    'channel_health_setting.min_weight_factor': number
    'channel_health_setting.max_ttft_ms': number
    'channel_health_setting.latency_degrade_base': number
    'channel_health_setting.latency_degrade_step': number
    'channel_health_setting.count_latency_as_error': boolean
    'channel_health_setting.disable_threshold': number
    'channel_health_setting.upgrade_threshold': number
    'channel_health_setting.degrade_probe_minutes': number
    'channel_health_setting.degrade_probe_count': number
    'channel_health_setting.count_429_as_error': boolean
    'channel_health_setting.countable_status_codes': string
    'channel_health_setting.notify_on_degrade': boolean
    'channel_health_setting.notify_on_upgrade': boolean
    'channel_health_setting.streak_window_sec': number
    'channel_health_setting.rebounce_protection_minutes': number
    'channel_health_setting.rebounce_protection_threshold': number
    'channel_health_setting.demote_cooldown_sec': number
    'channel_health_setting.recovery_strategy': string
    'channel_health_setting.recovery_probe_minutes': number
    'channel_health_setting.recovery_probe_model': string
    'channel_health_setting.shadow_sample_rate': number
    'channel_health_setting.skip_channel_tags': string
  }
}

function normalizeLineEndings(value: string) {
  return value.replace(/\r\n/g, '\n')
}

function parseTagsFromStorage(raw: string): string {
  if (!raw || raw === 'null') return ''
  try {
    const arr = JSON.parse(raw)
    if (Array.isArray(arr)) return arr.join(', ')
  } catch { /* not JSON, treat as comma-separated */ }
  return raw
}

function tagsToStorage(display: string): string {
  const tags = display.split(',').map((s) => s.trim()).filter(Boolean)
  return JSON.stringify(tags)
}

type NormalizedMonitoringValues = {
  ChannelDisableThreshold: string
  QuotaRemindThreshold: string
  AutomaticDisableChannelEnabled: boolean
  AutomaticEnableChannelEnabled: boolean
  AutomaticDisableKeywords: string
  AutomaticDisableStatusCodes: string
  AutomaticRetryStatusCodes: string
  'monitor_setting.auto_test_channel_enabled': boolean
  'monitor_setting.auto_test_channel_minutes': number
  'channel_health_setting.enabled': boolean
  'channel_health_setting.base_degrade_threshold': number
  'channel_health_setting.level_step_threshold': number
  'channel_health_setting.max_degrade_level': number
  'channel_health_setting.min_weight_factor': number
  'channel_health_setting.max_ttft_ms': number
  'channel_health_setting.latency_degrade_base': number
  'channel_health_setting.latency_degrade_step': number
  'channel_health_setting.count_latency_as_error': boolean
  'channel_health_setting.disable_threshold': number
  'channel_health_setting.upgrade_threshold': number
  'channel_health_setting.degrade_probe_minutes': number
  'channel_health_setting.degrade_probe_count': number
  'channel_health_setting.count_429_as_error': boolean
  'channel_health_setting.countable_status_codes': string
  'channel_health_setting.notify_on_degrade': boolean
  'channel_health_setting.notify_on_upgrade': boolean
  'channel_health_setting.streak_window_sec': number
  'channel_health_setting.rebounce_protection_minutes': number
  'channel_health_setting.rebounce_protection_threshold': number
  'channel_health_setting.demote_cooldown_sec': number
  'channel_health_setting.recovery_strategy': string
  'channel_health_setting.recovery_probe_minutes': number
  'channel_health_setting.recovery_probe_model': string
  'channel_health_setting.shadow_sample_rate': number
  'channel_health_setting.skip_channel_tags': string
}

const buildFormDefaults = (
  defaults: MonitoringSettingsSectionProps['defaultValues'],
  quotaPerUnit: number
): MonitoringFormInput => ({
  ChannelDisableThreshold: defaults.ChannelDisableThreshold ?? '',
  QuotaRemindThreshold: quotaToUsdString(
    defaults.QuotaRemindThreshold ?? '',
    quotaPerUnit
  ),
  AutomaticDisableChannelEnabled: defaults.AutomaticDisableChannelEnabled,
  AutomaticEnableChannelEnabled: defaults.AutomaticEnableChannelEnabled,
  AutomaticDisableKeywords: normalizeLineEndings(
    defaults.AutomaticDisableKeywords ?? ''
  ),
  AutomaticDisableStatusCodes: defaults.AutomaticDisableStatusCodes ?? '',
  AutomaticRetryStatusCodes: defaults.AutomaticRetryStatusCodes ?? '',
  monitor_setting: {
    auto_test_channel_enabled:
      defaults['monitor_setting.auto_test_channel_enabled'],
    auto_test_channel_minutes:
      defaults['monitor_setting.auto_test_channel_minutes'],
  },
  channel_health_setting: {
    enabled: defaults['channel_health_setting.enabled'],
    base_degrade_threshold: defaults['channel_health_setting.base_degrade_threshold'],
    level_step_threshold: defaults['channel_health_setting.level_step_threshold'],
    max_degrade_level: defaults['channel_health_setting.max_degrade_level'],
    min_weight_factor: defaults['channel_health_setting.min_weight_factor'],
    max_ttft_ms: defaults['channel_health_setting.max_ttft_ms'],
    latency_degrade_base: defaults['channel_health_setting.latency_degrade_base'],
    latency_degrade_step: defaults['channel_health_setting.latency_degrade_step'],
    count_latency_as_error: defaults['channel_health_setting.count_latency_as_error'],
    disable_threshold: defaults['channel_health_setting.disable_threshold'],
    upgrade_threshold: defaults['channel_health_setting.upgrade_threshold'],
    degrade_probe_minutes: defaults['channel_health_setting.degrade_probe_minutes'],
    degrade_probe_count: defaults['channel_health_setting.degrade_probe_count'],
    count_429_as_error: defaults['channel_health_setting.count_429_as_error'],
    countable_status_codes:
      defaults['channel_health_setting.countable_status_codes'] ?? '',
    notify_on_degrade: defaults['channel_health_setting.notify_on_degrade'],
    notify_on_upgrade: defaults['channel_health_setting.notify_on_upgrade'],
    streak_window_sec: defaults['channel_health_setting.streak_window_sec'],
    rebounce_protection_minutes:
      defaults['channel_health_setting.rebounce_protection_minutes'],
    rebounce_protection_threshold:
      defaults['channel_health_setting.rebounce_protection_threshold'],
    demote_cooldown_sec:
      defaults['channel_health_setting.demote_cooldown_sec'],
    recovery_strategy: ((s) => (s === 'probe' || s === 'manual' ? s : 'probe'))(
      defaults['channel_health_setting.recovery_strategy']
    ) as 'probe' | 'manual',
    recovery_probe_minutes:
      defaults['channel_health_setting.recovery_probe_minutes'],
    recovery_probe_model:
      defaults['channel_health_setting.recovery_probe_model'] ?? '',
    shadow_sample_rate:
      defaults['channel_health_setting.shadow_sample_rate'],
    skip_channel_tags:
      parseTagsFromStorage(defaults['channel_health_setting.skip_channel_tags'] ?? ''),
  },
})

const normalizeDefaults = (
  defaults: MonitoringSettingsSectionProps['defaultValues']
): NormalizedMonitoringValues => ({
  ChannelDisableThreshold: (defaults.ChannelDisableThreshold ?? '').trim(),
  QuotaRemindThreshold: (defaults.QuotaRemindThreshold ?? '').trim(),
  AutomaticDisableChannelEnabled: defaults.AutomaticDisableChannelEnabled,
  AutomaticEnableChannelEnabled: defaults.AutomaticEnableChannelEnabled,
  AutomaticDisableKeywords: normalizeLineEndings(
    defaults.AutomaticDisableKeywords ?? ''
  ),
  AutomaticDisableStatusCodes: parseHttpStatusCodeRules(
    defaults.AutomaticDisableStatusCodes ?? ''
  ).normalized,
  AutomaticRetryStatusCodes: parseHttpStatusCodeRules(
    defaults.AutomaticRetryStatusCodes ?? ''
  ).normalized,
  'monitor_setting.auto_test_channel_enabled':
    defaults['monitor_setting.auto_test_channel_enabled'],
  'monitor_setting.auto_test_channel_minutes':
    defaults['monitor_setting.auto_test_channel_minutes'],
  'channel_health_setting.enabled': defaults['channel_health_setting.enabled'],
  'channel_health_setting.base_degrade_threshold':
    defaults['channel_health_setting.base_degrade_threshold'],
  'channel_health_setting.level_step_threshold':
    defaults['channel_health_setting.level_step_threshold'],
  'channel_health_setting.max_degrade_level':
    defaults['channel_health_setting.max_degrade_level'],
  'channel_health_setting.min_weight_factor':
    defaults['channel_health_setting.min_weight_factor'],
  'channel_health_setting.max_ttft_ms':
    defaults['channel_health_setting.max_ttft_ms'],
  'channel_health_setting.latency_degrade_base':
    defaults['channel_health_setting.latency_degrade_base'],
  'channel_health_setting.latency_degrade_step':
    defaults['channel_health_setting.latency_degrade_step'],
  'channel_health_setting.count_latency_as_error':
    defaults['channel_health_setting.count_latency_as_error'],
  'channel_health_setting.disable_threshold':
    defaults['channel_health_setting.disable_threshold'],
  'channel_health_setting.upgrade_threshold':
    defaults['channel_health_setting.upgrade_threshold'],
  'channel_health_setting.degrade_probe_minutes':
    defaults['channel_health_setting.degrade_probe_minutes'],
  'channel_health_setting.degrade_probe_count':
    defaults['channel_health_setting.degrade_probe_count'],
  'channel_health_setting.count_429_as_error':
    defaults['channel_health_setting.count_429_as_error'],
  'channel_health_setting.countable_status_codes': parseHttpStatusCodeRules(
    defaults['channel_health_setting.countable_status_codes'] ?? ''
  ).normalized,
  'channel_health_setting.notify_on_degrade':
    defaults['channel_health_setting.notify_on_degrade'],
  'channel_health_setting.notify_on_upgrade':
    defaults['channel_health_setting.notify_on_upgrade'],
  'channel_health_setting.streak_window_sec':
    defaults['channel_health_setting.streak_window_sec'],
  'channel_health_setting.rebounce_protection_minutes':
    defaults['channel_health_setting.rebounce_protection_minutes'],
  'channel_health_setting.rebounce_protection_threshold':
    defaults['channel_health_setting.rebounce_protection_threshold'],
  'channel_health_setting.demote_cooldown_sec':
    defaults['channel_health_setting.demote_cooldown_sec'],
  'channel_health_setting.recovery_strategy':
    defaults['channel_health_setting.recovery_strategy'],
  'channel_health_setting.recovery_probe_minutes':
    defaults['channel_health_setting.recovery_probe_minutes'],
  'channel_health_setting.recovery_probe_model':
    defaults['channel_health_setting.recovery_probe_model'] ?? '',
  'channel_health_setting.shadow_sample_rate':
    defaults['channel_health_setting.shadow_sample_rate'],
  'channel_health_setting.skip_channel_tags':
    parseTagsFromStorage(defaults['channel_health_setting.skip_channel_tags'] ?? ''),
})

const normalizeFormValues = (
  values: MonitoringFormValues,
  quotaPerUnit: number
): NormalizedMonitoringValues => ({
  ChannelDisableThreshold: values.ChannelDisableThreshold.trim(),
  QuotaRemindThreshold: usdToQuotaString(values.QuotaRemindThreshold, quotaPerUnit),
  AutomaticDisableChannelEnabled: values.AutomaticDisableChannelEnabled,
  AutomaticEnableChannelEnabled: values.AutomaticEnableChannelEnabled,
  AutomaticDisableKeywords: normalizeLineEndings(
    values.AutomaticDisableKeywords
  ),
  AutomaticDisableStatusCodes: parseHttpStatusCodeRules(
    values.AutomaticDisableStatusCodes
  ).normalized,
  AutomaticRetryStatusCodes: parseHttpStatusCodeRules(
    values.AutomaticRetryStatusCodes
  ).normalized,
  'monitor_setting.auto_test_channel_enabled':
    values.monitor_setting.auto_test_channel_enabled,
  'monitor_setting.auto_test_channel_minutes':
    values.monitor_setting.auto_test_channel_minutes,
  'channel_health_setting.enabled': values.channel_health_setting.enabled,
  'channel_health_setting.base_degrade_threshold':
    values.channel_health_setting.base_degrade_threshold,
  'channel_health_setting.level_step_threshold':
    values.channel_health_setting.level_step_threshold,
  'channel_health_setting.max_degrade_level':
    values.channel_health_setting.max_degrade_level,
  'channel_health_setting.min_weight_factor':
    values.channel_health_setting.min_weight_factor,
  'channel_health_setting.max_ttft_ms':
    values.channel_health_setting.max_ttft_ms,
  'channel_health_setting.latency_degrade_base':
    values.channel_health_setting.latency_degrade_base,
  'channel_health_setting.latency_degrade_step':
    values.channel_health_setting.latency_degrade_step,
  'channel_health_setting.count_latency_as_error':
    values.channel_health_setting.count_latency_as_error,
  'channel_health_setting.disable_threshold':
    values.channel_health_setting.disable_threshold,
  'channel_health_setting.upgrade_threshold':
    values.channel_health_setting.upgrade_threshold,
  'channel_health_setting.degrade_probe_minutes':
    values.channel_health_setting.degrade_probe_minutes,
  'channel_health_setting.degrade_probe_count':
    values.channel_health_setting.degrade_probe_count,
  'channel_health_setting.count_429_as_error':
    values.channel_health_setting.count_429_as_error,
  'channel_health_setting.countable_status_codes': parseHttpStatusCodeRules(
    values.channel_health_setting.countable_status_codes
  ).normalized,
  'channel_health_setting.notify_on_degrade':
    values.channel_health_setting.notify_on_degrade,
  'channel_health_setting.notify_on_upgrade':
    values.channel_health_setting.notify_on_upgrade,
  'channel_health_setting.streak_window_sec':
    values.channel_health_setting.streak_window_sec,
  'channel_health_setting.rebounce_protection_minutes':
    values.channel_health_setting.rebounce_protection_minutes,
  'channel_health_setting.rebounce_protection_threshold':
    values.channel_health_setting.rebounce_protection_threshold,
  'channel_health_setting.demote_cooldown_sec':
    values.channel_health_setting.demote_cooldown_sec,
  'channel_health_setting.recovery_strategy':
    values.channel_health_setting.recovery_strategy,
  'channel_health_setting.recovery_probe_minutes':
    values.channel_health_setting.recovery_probe_minutes,
  'channel_health_setting.recovery_probe_model':
    values.channel_health_setting.recovery_probe_model,
  'channel_health_setting.shadow_sample_rate':
    values.channel_health_setting.shadow_sample_rate,
  'channel_health_setting.skip_channel_tags':
    tagsToStorage(values.channel_health_setting.skip_channel_tags),
})

function ThresholdPreview({ form }: { form: ReturnType<typeof useForm<MonitoringFormInput, unknown, MonitoringFormValues>> }) {
  const { t } = useTranslation()
  const base = form.watch('channel_health_setting.base_degrade_threshold')
  const step = form.watch('channel_health_setting.level_step_threshold')
  const maxLevel = form.watch('channel_health_setting.max_degrade_level')
  const minFactor = form.watch('channel_health_setting.min_weight_factor')

  const rows = useMemo(() => {
    const b = Number(base) || 5
    const s = Number(step) || 5
    const m = Math.min(Number(maxLevel) || 10, 20)
    const mf = Number(minFactor) || 0.05
    const result: Array<{ level: number; streak: string; factor: string; offset: number }> = []
    for (let level = 1; level <= m; level++) {
      const streakMin = b + (level - 1) * s
      const streakMax = b + level * s - 1
      const factor = Math.max(mf, 1.0 - level * ((1.0 - mf) / m))
      result.push({
        level,
        streak: level < m ? `${streakMin}–${streakMax}` : `${streakMin}+`,
        factor: `×${factor.toFixed(3)}`,
        offset: -level,
      })
    }
    return result
  }, [base, step, maxLevel, minFactor])

  return (
    <div className='rounded-lg border p-4'>
      <p className='text-sm font-medium mb-2'>{t('Threshold preview')}</p>
      <div className='overflow-x-auto'>
        <table className='text-xs w-full'>
          <thead>
            <tr className='text-muted-foreground border-b'>
              <th className='py-1 pr-3 text-left'>{t('Level')}</th>
              <th className='py-1 pr-3 text-left'>{t('Streak range')}</th>
              <th className='py-1 pr-3 text-left'>{t('Weight factor')}</th>
              <th className='py-1 text-left'>{t('Priority offset')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.level} className='border-b border-border/40'>
                <td className='py-1 pr-3 font-mono'>L{r.level}</td>
                <td className='py-1 pr-3 font-mono'>{r.streak}</td>
                <td className='py-1 pr-3 font-mono'>{r.factor}</td>
                <td className='py-1 font-mono'>{r.offset}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export function MonitoringSettingsSection({
  defaultValues,
}: MonitoringSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const { currency } = useSystemConfig()
  const quotaPerUnit = currency.quotaPerUnit
  const baselineRef = useRef<NormalizedMonitoringValues>(
    normalizeDefaults(defaultValues)
  )

  const formDefaults = useMemo(
    () => buildFormDefaults(defaultValues, quotaPerUnit),
    [defaultValues, quotaPerUnit]
  )

  const form = useForm<MonitoringFormInput, unknown, MonitoringFormValues>({
    resolver: zodResolver(monitoringSchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const autoDisableStatusCodes = form.watch('AutomaticDisableStatusCodes')
  const autoRetryStatusCodes = form.watch('AutomaticRetryStatusCodes')
  const countableStatusCodes = form.watch(
    'channel_health_setting.countable_status_codes'
  )
  const autoDisableParsed = useMemo(
    () => parseHttpStatusCodeRules(autoDisableStatusCodes),
    [autoDisableStatusCodes]
  )
  const autoRetryParsed = useMemo(
    () => parseHttpStatusCodeRules(autoRetryStatusCodes),
    [autoRetryStatusCodes]
  )
  const countableParsed = useMemo(
    () => parseHttpStatusCodeRules(countableStatusCodes ?? ''),
    [countableStatusCodes]
  )

  const onSubmit = async (values: MonitoringFormValues) => {
    const normalized = normalizeFormValues(values, quotaPerUnit)
    const updates = (
      Object.keys(normalized) as Array<keyof NormalizedMonitoringValues>
    ).filter((key) => normalized[key] !== baselineRef.current[key])

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const key of updates) {
      const value = normalized[key]
      await updateOption.mutateAsync({
        key,
        value,
      })
    }

    baselineRef.current = normalized
  }

  return (
    <SettingsSection
      title={t('Monitoring & Alerts')}
      description={t(
        'Automatically test channels and notify users when limits are hit'
      )}
    >
      <Form {...form}>
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='monitor_setting.auto_test_channel_enabled'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5'>
                    <FormLabel className='text-base'>
                      {t('Scheduled channel tests')}
                    </FormLabel>
                    <FormDescription>
                      {t('Automatically probe all channels in the background')}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='monitor_setting.auto_test_channel_minutes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Test interval (minutes)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={1}
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
                      onChange={(event) =>
                        field.onChange(event.target.valueAsNumber)
                      }
                      name={field.name}
                      onBlur={field.onBlur}
                      ref={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('How frequently the system tests all channels')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='ChannelDisableThreshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Disable threshold (seconds)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      step={1}
                      value={field.value}
                      onChange={(event) => field.onChange(event.target.value)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Automatically disable channels exceeding this response time'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='QuotaRemindThreshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Quota reminder ($)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      step={0.01}
                      value={field.value}
                      onChange={(event) => field.onChange(event.target.value)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Send email alerts when a user falls below this quota')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='AutomaticDisableChannelEnabled'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5'>
                    <FormLabel className='text-base'>
                      {t('Disable on failure')}
                    </FormLabel>
                    <FormDescription>
                      {t('Automatically disable channels when tests fail')}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='AutomaticEnableChannelEnabled'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5'>
                    <FormLabel className='text-base'>
                      {t('Re-enable on success')}
                    </FormLabel>
                    <FormDescription>
                      {t('Bring channels back online after successful checks')}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name='AutomaticDisableKeywords'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Failure keywords')}</FormLabel>
                <FormControl>
                  <Textarea
                    rows={6}
                    placeholder={t('one keyword per line')}
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'If an upstream error contains any of these keywords (case insensitive), the channel will be disabled automatically.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='AutomaticDisableStatusCodes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Auto-disable status codes')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('e.g. 401, 403, 429, 500-599')}
                      value={field.value}
                      onChange={(event) => field.onChange(event.target.value)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Accepts comma-separated status codes and inclusive ranges.'
                    )}{' '}
                    {autoDisableParsed.ok &&
                      autoDisableParsed.normalized &&
                      autoDisableParsed.normalized !== field.value.trim() && (
                        <span className='text-muted-foreground'>
                          {t('Normalized:')} {autoDisableParsed.normalized}
                        </span>
                      )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='AutomaticRetryStatusCodes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Auto-retry status codes')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('e.g. 401, 403, 429, 500-599')}
                      value={field.value}
                      onChange={(event) => field.onChange(event.target.value)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Accepts comma-separated status codes and inclusive ranges.'
                    )}{' '}
                    {autoRetryParsed.ok &&
                      autoRetryParsed.normalized &&
                      autoRetryParsed.normalized !== field.value.trim() && (
                        <span className='text-muted-foreground'>
                          {t('Normalized:')} {autoRetryParsed.normalized}
                        </span>
                      )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Separator />

          <div className='space-y-1'>
            <h3 className='text-base font-semibold'>
              {t('Channel health (passive auto-degrade)')}
            </h3>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Detect failing channels via real user traffic only — no extra token cost. Configure thresholds for L1/L2 demote, auto-disable, and auto-upgrade.'
              )}
            </p>
          </div>

          <FormField
            control={form.control}
            name='channel_health_setting.enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5'>
                  <FormLabel className='text-base'>
                    {t('Enable passive channel health')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'When off, no demote/upgrade/auto-disable actions are taken. Independent from scheduled probing.'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <div className='grid gap-6 md:grid-cols-2 lg:grid-cols-4'>
            <FormField
              control={form.control}
              name='channel_health_setting.base_degrade_threshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Base degrade threshold')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Consecutive errors to enter L1 (default 5)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.level_step_threshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Level step')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Additional errors per level (default 5)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.max_degrade_level'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Max degrade level')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      max={20}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Maximum degradation level (default 10)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.min_weight_factor'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Min weight factor')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0.01}
                      max={1}
                      step={0.01}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Weight floor at deepest level (default 0.05)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='channel_health_setting.disable_threshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Disable threshold (errors)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        field.onChange(Number.isFinite(v) ? v : 0)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Consecutive errors to auto-disable; 0 to disable this feature')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.upgrade_threshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Upgrade threshold (successes)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Consecutive successes to upgrade one level')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.degrade_probe_minutes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Probe interval (minutes)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('System probe interval for degraded channels (minutes)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.degrade_probe_count'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Probe count per channel')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Number of probe requests per degraded channel per cycle')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <ThresholdPreview form={form} />

          <div className='grid gap-6 md:grid-cols-3'>
            <FormField
              control={form.control}
              name='channel_health_setting.count_429_as_error'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5'>
                    <FormLabel className='text-base'>
                      {t('Count 429 as error')}
                    </FormLabel>
                    <FormDescription>
                      {t('Disable for free channels that 429 frequently')}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.notify_on_degrade'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5'>
                    <FormLabel className='text-base'>
                      {t('Notify on degrade')}
                    </FormLabel>
                    <FormDescription>
                      {t('Send notification on L1/L2 demote (default off)')}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.notify_on_upgrade'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5'>
                    <FormLabel className='text-base'>
                      {t('Notify on upgrade')}
                    </FormLabel>
                    <FormDescription>
                      {t('Send notification on level upgrade (default off)')}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name='channel_health_setting.countable_status_codes'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Countable status codes')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder={t('e.g. 429, 500-599 (leave empty for default 5xx fallback)')}
                    value={field.value ?? ''}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Whitelist of status codes that count toward channel error streak. When empty, falls back to 5xx + IsChannelError + AutomaticDisableStatusCodes.'
                  )}{' '}
                  {countableParsed.ok &&
                    countableParsed.normalized &&
                    countableParsed.normalized !== (field.value ?? '').trim() && (
                      <span className='text-muted-foreground'>
                        {t('Normalized:')} {countableParsed.normalized}
                      </span>
                    )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className='grid gap-6 md:grid-cols-3'>
            <FormField
              control={form.control}
              name='channel_health_setting.streak_window_sec'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Streak window (seconds)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={60}
                      step={60}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('TTL for error/success counters (default 600 = 10 min; old 86400 is too long)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.rebounce_protection_minutes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Rebounce window (minutes)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        field.onChange(Number.isFinite(v) ? v : 0)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Disable→enable→disable within this window triggers permanent lock; 0 to disable rebounce protection'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.rebounce_protection_threshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Rebounce lock threshold')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Disables within window before locking (default 3)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.demote_cooldown_sec'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Demote cooldown (seconds)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Within this window only one demote (L1/L2) per channel; 0 to disable (default 60)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='channel_health_setting.recovery_strategy'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Recovery strategy')}</FormLabel>
                  <FormControl>
                    <Select value={field.value} onValueChange={field.onChange}>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value='probe'>
                          {t('probe (cheap model, recommended)')}
                        </SelectItem>
                        <SelectItem value='manual'>
                          {t('manual (no probing, admin only)')}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </FormControl>
                  <FormDescription>
                    {t('How to bring auto-disabled channels back online')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.recovery_probe_minutes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Recovery probe interval (minutes)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Probe interval for auto-disabled channels')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

          </div>

          <div className='grid gap-6 md:grid-cols-3'>
            <FormField
              control={form.control}
              name='channel_health_setting.recovery_probe_model'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Recovery probe model')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('e.g. gpt-4o-mini (empty = channel default)')}
                      value={field.value ?? ''}
                      onChange={(event) => field.onChange(event.target.value)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Cheap model used for recovery probing; empty uses channel default')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.shadow_sample_rate'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Shadow sample rate')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      max={1}
                      step={0.001}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Fraction of traffic to shadow-route for recovery validation (default 0.001)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.skip_channel_tags'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Skip channel tags')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('e.g. manual,vip (comma-separated)')}
                      value={field.value ?? ''}
                      onChange={(event) => field.onChange(event.target.value)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Channels with these tags are excluded from passive health checks')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Separator />

          <div className='space-y-1'>
            <h3 className='text-base font-semibold'>{t('TTFT latency degradation')}</h3>
            <p className='text-muted-foreground text-sm'>
              {t('Degrade channels when first-token latency consistently exceeds the threshold. Independent from error-based degradation.')}
            </p>
          </div>

          <div className='grid gap-6 md:grid-cols-2 lg:grid-cols-4'>
            <FormField
              control={form.control}
              name='channel_health_setting.max_ttft_ms'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Max TTFT (ms)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      step={100}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        field.onChange(Number.isFinite(v) ? v : 0)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('0 = disabled; channels exceeding this trigger latency streak')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.latency_degrade_base'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Latency degrade base')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Consecutive violations to enter L1 (default 5)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.latency_degrade_step'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Latency degrade step')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      step={1}
                      value={typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : ''}
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Additional violations per level (default 5)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_health_setting.count_latency_as_error'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5'>
                    <FormLabel className='text-base'>
                      {t('Count as error')}
                    </FormLabel>
                    <FormDescription>
                      {t('Merge latency violations into error streak instead of independent tracking')}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />
          </div>

          <Button type='submit' disabled={updateOption.isPending}>
            {updateOption.isPending
              ? t('Saving...')
              : t('Save monitoring rules')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

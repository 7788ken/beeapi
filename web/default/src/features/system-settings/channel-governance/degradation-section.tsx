import { useMemo, useRef } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { OctagonAlert } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { SettingsSection } from '../components/settings-section'
import { useOptionBatchSave } from '../hooks/use-option-batch-save'
import { useResetForm } from '../hooks/use-reset-form'
import { AdvancedFields } from './advanced-fields'
import { StatusCodeField } from './status-code-field'
import { statusCodeRules } from './status-code-rules'

const degradationSchema = z
  .object({
    channel_health_setting: z.object({
      enabled: z.boolean(),
      countable_status_codes: statusCodeRules,
      count_429_as_error: z.boolean(),
      base_degrade_threshold: z.coerce.number().int().min(1),
      level_step_threshold: z.coerce.number().int().min(1),
      max_degrade_level: z.coerce.number().int().min(1).max(20),
      min_weight_factor: z.coerce.number().min(0.01).max(1.0),
      upgrade_threshold: z.coerce.number().int().min(1),
      streak_window_sec: z.coerce.number().int().min(60),
      demote_cooldown_sec: z.coerce.number().int().min(0),
      // 0=禁用本功能（不再触发自动 disable）
      disable_threshold: z.coerce.number().int().min(0),
      max_ttft_ms: z.coerce.number().int().min(0),
      latency_degrade_base: z.coerce.number().int().min(1),
      latency_degrade_step: z.coerce.number().int().min(1),
      count_latency_as_error: z.boolean(),
      // 0=禁用反弹保护功能
      rebounce_protection_minutes: z.coerce.number().int().min(0),
      rebounce_protection_threshold: z.coerce.number().int().min(0),
      notify_on_degrade: z.boolean(),
      notify_on_upgrade: z.boolean(),
    }),
  })
  .superRefine((values, ctx) => {
    const h = values.channel_health_setting
    // disable_threshold=0 表示禁用本功能
    if (
      h.disable_threshold > 0 &&
      h.disable_threshold <= h.base_degrade_threshold
    ) {
      ctx.addIssue({
        code: 'custom',
        path: ['channel_health_setting', 'disable_threshold'],
        message:
          'Disable threshold must be greater than base degrade threshold (or 0 to disable)',
      })
    }
  })

type DegradationFormValues = z.output<typeof degradationSchema>
type DegradationFormInput = z.input<typeof degradationSchema>

/** 扁平化后即 option key */
export type DegradationOptions = {
  'channel_health_setting.enabled': boolean
  'channel_health_setting.countable_status_codes': string
  'channel_health_setting.count_429_as_error': boolean
  'channel_health_setting.base_degrade_threshold': number
  'channel_health_setting.level_step_threshold': number
  'channel_health_setting.max_degrade_level': number
  'channel_health_setting.min_weight_factor': number
  'channel_health_setting.upgrade_threshold': number
  'channel_health_setting.streak_window_sec': number
  'channel_health_setting.demote_cooldown_sec': number
  'channel_health_setting.disable_threshold': number
  'channel_health_setting.max_ttft_ms': number
  'channel_health_setting.latency_degrade_base': number
  'channel_health_setting.latency_degrade_step': number
  'channel_health_setting.count_latency_as_error': boolean
  'channel_health_setting.rebounce_protection_minutes': number
  'channel_health_setting.rebounce_protection_threshold': number
  'channel_health_setting.notify_on_degrade': boolean
  'channel_health_setting.notify_on_upgrade': boolean
}

export type DegradationSectionProps = {
  defaultValues: DegradationOptions
}

function flatten(values: DegradationFormValues): DegradationOptions {
  const h = values.channel_health_setting
  return {
    'channel_health_setting.enabled': h.enabled,
    'channel_health_setting.countable_status_codes': h.countable_status_codes,
    'channel_health_setting.count_429_as_error': h.count_429_as_error,
    'channel_health_setting.base_degrade_threshold': h.base_degrade_threshold,
    'channel_health_setting.level_step_threshold': h.level_step_threshold,
    'channel_health_setting.max_degrade_level': h.max_degrade_level,
    'channel_health_setting.min_weight_factor': h.min_weight_factor,
    'channel_health_setting.upgrade_threshold': h.upgrade_threshold,
    'channel_health_setting.streak_window_sec': h.streak_window_sec,
    'channel_health_setting.demote_cooldown_sec': h.demote_cooldown_sec,
    'channel_health_setting.disable_threshold': h.disable_threshold,
    'channel_health_setting.max_ttft_ms': h.max_ttft_ms,
    'channel_health_setting.latency_degrade_base': h.latency_degrade_base,
    'channel_health_setting.latency_degrade_step': h.latency_degrade_step,
    'channel_health_setting.count_latency_as_error': h.count_latency_as_error,
    'channel_health_setting.rebounce_protection_minutes':
      h.rebounce_protection_minutes,
    'channel_health_setting.rebounce_protection_threshold':
      h.rebounce_protection_threshold,
    'channel_health_setting.notify_on_degrade': h.notify_on_degrade,
    'channel_health_setting.notify_on_upgrade': h.notify_on_upgrade,
  }
}

type DegradationForm = ReturnType<
  typeof useForm<DegradationFormInput, unknown, DegradationFormValues>
>

function ThresholdPreview({ form }: { form: DegradationForm }) {
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
    const result: Array<{
      level: number
      streak: string
      factor: string
      offset: number
    }> = []
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
      <p className='mb-2 text-sm font-medium'>{t('Threshold preview')}</p>
      <div className='overflow-x-auto'>
        <table className='w-full text-xs'>
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
              <tr key={r.level} className='border-border/40 border-b'>
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

export function DegradationSection({ defaultValues }: DegradationSectionProps) {
  const { t } = useTranslation()
  const { save, isSaving } = useOptionBatchSave()
  const baselineRef = useRef<DegradationOptions>(defaultValues)

  const formDefaults = useMemo<DegradationFormInput>(
    () => ({
      channel_health_setting: {
        enabled: defaultValues['channel_health_setting.enabled'],
        countable_status_codes:
          defaultValues['channel_health_setting.countable_status_codes'],
        count_429_as_error:
          defaultValues['channel_health_setting.count_429_as_error'],
        base_degrade_threshold:
          defaultValues['channel_health_setting.base_degrade_threshold'],
        level_step_threshold:
          defaultValues['channel_health_setting.level_step_threshold'],
        max_degrade_level:
          defaultValues['channel_health_setting.max_degrade_level'],
        min_weight_factor:
          defaultValues['channel_health_setting.min_weight_factor'],
        upgrade_threshold:
          defaultValues['channel_health_setting.upgrade_threshold'],
        streak_window_sec:
          defaultValues['channel_health_setting.streak_window_sec'],
        demote_cooldown_sec:
          defaultValues['channel_health_setting.demote_cooldown_sec'],
        disable_threshold:
          defaultValues['channel_health_setting.disable_threshold'],
        max_ttft_ms: defaultValues['channel_health_setting.max_ttft_ms'],
        latency_degrade_base:
          defaultValues['channel_health_setting.latency_degrade_base'],
        latency_degrade_step:
          defaultValues['channel_health_setting.latency_degrade_step'],
        count_latency_as_error:
          defaultValues['channel_health_setting.count_latency_as_error'],
        rebounce_protection_minutes:
          defaultValues['channel_health_setting.rebounce_protection_minutes'],
        rebounce_protection_threshold:
          defaultValues['channel_health_setting.rebounce_protection_threshold'],
        notify_on_degrade:
          defaultValues['channel_health_setting.notify_on_degrade'],
        notify_on_upgrade:
          defaultValues['channel_health_setting.notify_on_upgrade'],
      },
    }),
    [defaultValues]
  )

  const form = useForm<DegradationFormInput, unknown, DegradationFormValues>({
    resolver: zodResolver(degradationSchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const onSubmit = async (values: DegradationFormValues) => {
    const { saved } = await save(flatten(values), baselineRef.current)
    baselineRef.current = { ...baselineRef.current, ...saved }
  }

  return (
    <SettingsSection
      title={t('Degradation & Stopping')}
      description={t(
        'What live traffic does to a channel: how far it slides down the ladder, and when it is taken out entirely'
      )}
    >
      <Form {...form}>
        {/* eslint-disable-next-line react-hooks/refs */}
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
          <FormField
            control={form.control}
            name='channel_health_setting.enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5 pe-4'>
                  <FormLabel className='text-base'>
                    {t('Channel health (live traffic)')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Counts consecutive errors from real requests and degrades or stops the channel. Turning this off also stops degrade probing and recovery probing, and freezes any channel that is already degraded.'
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

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Live-traffic error streak')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'These two decide which real-traffic responses push a channel error streak up, which is what Degradation & stopping acts on.'
              )}
            </p>
          </div>

          <div className='grid gap-6 md:grid-cols-2'>
            <StatusCodeField
              control={form.control}
              name='channel_health_setting.countable_status_codes'
              label={t('Countable status codes')}
              description={t(
                'Whitelist of status codes that count toward channel error streak. When empty, falls back to 5xx + IsChannelError + AutomaticDisableStatusCodes.'
              )}
            />
            <FormField
              control={form.control}
              name='channel_health_setting.count_429_as_error'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5 pe-4'>
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
          </div>

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>{t('Degrade ladder')}</h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Consecutive errors push a channel down the ladder, lowering its priority and weight; consecutive successes bring it back one level at a time.'
              )}
            </p>
          </div>

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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
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

          <div className='grid gap-6 md:grid-cols-3'>
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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
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
              name='channel_health_setting.streak_window_sec'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Streak window (seconds)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={60}
                      step={60}
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'TTL for error/success counters (default 600 = 10 min; old 86400 is too long)'
                    )}
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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        if (Number.isFinite(v)) field.onChange(v)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Within this window only one demote (L1/L2) per channel; 0 to disable (default 60)'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <ThresholdPreview form={form} />

          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Consecutive-error auto-stop')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'The one setting on this tab that takes a channel out of service based on live traffic.'
              )}
            </p>
          </div>

          <Alert variant='destructive'>
            <OctagonAlert />
            <AlertTitle>
              {t('This path does not read the probe-failure switch')}
            </AlertTitle>
            <AlertDescription>
              {t(
                'Stop on probe failure under Scheduled probes has no effect here. This path only checks the auto-ban flag on the channel itself. Set the threshold to 0 if you want channels to be degraded but never stopped because of consecutive errors.'
              )}
            </AlertDescription>
          </Alert>

          <FormField
            control={form.control}
            name='channel_health_setting.disable_threshold'
            render={({ field }) => (
              <FormItem className='md:max-w-sm'>
                <FormLabel>{t('Disable threshold (errors)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={1}
                    value={
                      typeof field.value === 'number' &&
                      Number.isFinite(field.value)
                        ? field.value
                        : ''
                    }
                    onChange={(event) => {
                      const v = event.target.valueAsNumber
                      field.onChange(Number.isFinite(v) ? v : 0)
                    }}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Consecutive errors to auto-disable; 0 to disable this feature'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <AdvancedFields>
          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Live first-token latency')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Degrade channels when first-token latency on real requests consistently exceeds the threshold. Independent from error-based degradation.'
              )}
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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
                      onChange={(event) => {
                        const v = event.target.valueAsNumber
                        field.onChange(Number.isFinite(v) ? v : 0)
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      '0 = disabled. Milliseconds, measured on live traffic, and it only degrades. Not to be confused with the probe response timeout under Scheduled probes: that one is in seconds, only watches probes, and stops the channel outright.'
                    )}
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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
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
                  <div className='space-y-0.5 pe-4'>
                    <FormLabel className='text-base'>
                      {t('Count as error')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'Merge latency violations into error streak instead of independent tracking'
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
          </div>

          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Rebounce protection & notifications')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Stop a channel that keeps flapping between stopped and enabled, and decide whether level changes are announced.'
              )}
            </p>
          </div>

          <div className='grid gap-6 md:grid-cols-2'>
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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
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
                      value={
                        typeof field.value === 'number' &&
                        Number.isFinite(field.value)
                          ? field.value
                          : ''
                      }
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
              name='channel_health_setting.notify_on_degrade'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5 pe-4'>
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
                  <div className='space-y-0.5 pe-4'>
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
          </AdvancedFields>

          <Separator />

          <Button type='submit' disabled={isSaving}>
            {isSaving ? t('Saving...') : t('Save degradation settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

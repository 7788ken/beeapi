import { useMemo, useRef } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
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
import { SettingsSection } from '../components/settings-section'
import { useOptionBatchSave } from '../hooks/use-option-batch-save'
import { useResetForm } from '../hooks/use-reset-form'
import { AdvancedFields } from './advanced-fields'

// 渠道路由模式（概率 / 容量）docs/2026-05-26-channel-routing-mode-switchable.md
const routingSchema = z.object({
  channel_routing_setting: z.object({
    mode: z.enum(['probabilistic', 'capacity']),
    capacity_window_sec: z.coerce
      .number()
      .int()
      .min(60)
      .max(3600)
      .refine((v) => v % 60 === 0, {
        message: 'Must be a multiple of 60 seconds',
      }),
    full_strategy: z.enum(['fallback', 'reject', 'degraded', 'queue']),
    fail_mode: z.enum(['fail_open', 'fail_closed']),
    dry_run: z.boolean(),
    dry_run_sample_rate: z.coerce.number().min(0).max(1),
    queue_max_wait_ms: z.coerce.number().int().min(1000).max(300000),
    queue_poll_interval_ms: z.coerce.number().int().min(50).max(10000),
  }),
  url_health_setting: z.object({
    fail_threshold: z.coerce.number().int().min(1).max(100),
    cooldown_seconds: z.coerce.number().int().min(1).max(86400),
    ewma_alpha: z.coerce.number().min(0.01).max(1),
    hysteresis_ratio: z.coerce.number().min(0).max(10),
    hysteresis_min_ms: z.coerce.number().min(0).max(600000),
    exploration_gap_seconds: z.coerce.number().int().min(1).max(86400),
  }),
})

type RoutingFormValues = z.output<typeof routingSchema>
type RoutingFormInput = z.input<typeof routingSchema>

/** 扁平化后即 option key */
export type RoutingOptions = {
  'channel_routing_setting.mode': string
  'channel_routing_setting.capacity_window_sec': number
  'channel_routing_setting.full_strategy': string
  'channel_routing_setting.fail_mode': string
  'channel_routing_setting.dry_run': boolean
  'channel_routing_setting.dry_run_sample_rate': number
  'channel_routing_setting.queue_max_wait_ms': number
  'channel_routing_setting.queue_poll_interval_ms': number
  'url_health_setting.fail_threshold': number
  'url_health_setting.cooldown_seconds': number
  'url_health_setting.ewma_alpha': number
  'url_health_setting.hysteresis_ratio': number
  'url_health_setting.hysteresis_min_ms': number
  'url_health_setting.exploration_gap_seconds': number
}

export type RoutingSectionProps = {
  defaultValues: RoutingOptions
}

/** 后端要求 60 倍数，加载历史非法值时规整到合法档位 */
function normalizeCapacityWindow(raw: number): number {
  const clamped = Math.min(3600, Math.max(60, Number(raw) || 60))
  return Math.ceil(clamped / 60) * 60
}

function flatten(values: RoutingFormValues): RoutingOptions {
  const r = values.channel_routing_setting
  const u = values.url_health_setting
  return {
    'channel_routing_setting.mode': r.mode,
    'channel_routing_setting.capacity_window_sec': r.capacity_window_sec,
    'channel_routing_setting.full_strategy': r.full_strategy,
    'channel_routing_setting.fail_mode': r.fail_mode,
    'channel_routing_setting.dry_run': r.dry_run,
    'channel_routing_setting.dry_run_sample_rate': r.dry_run_sample_rate,
    'channel_routing_setting.queue_max_wait_ms': r.queue_max_wait_ms,
    'channel_routing_setting.queue_poll_interval_ms': r.queue_poll_interval_ms,
    'url_health_setting.fail_threshold': u.fail_threshold,
    'url_health_setting.cooldown_seconds': u.cooldown_seconds,
    'url_health_setting.ewma_alpha': u.ewma_alpha,
    'url_health_setting.hysteresis_ratio': u.hysteresis_ratio,
    'url_health_setting.hysteresis_min_ms': u.hysteresis_min_ms,
    'url_health_setting.exploration_gap_seconds': u.exploration_gap_seconds,
  }
}

export function RoutingSection({ defaultValues }: RoutingSectionProps) {
  const { t } = useTranslation()
  const { save, isSaving } = useOptionBatchSave()
  const baselineRef = useRef<RoutingOptions>({
    ...defaultValues,
    'channel_routing_setting.capacity_window_sec': normalizeCapacityWindow(
      defaultValues['channel_routing_setting.capacity_window_sec']
    ),
  })

  const formDefaults = useMemo<RoutingFormInput>(
    () => ({
      channel_routing_setting: {
        mode: ((m) => (m === 'capacity' ? m : 'probabilistic'))(
          defaultValues['channel_routing_setting.mode']
        ),
        capacity_window_sec: normalizeCapacityWindow(
          defaultValues['channel_routing_setting.capacity_window_sec']
        ),
        full_strategy: ((s) =>
          s === 'reject' || s === 'degraded' || s === 'queue' ? s : 'fallback')(
          defaultValues['channel_routing_setting.full_strategy']
        ),
        fail_mode: ((m) => (m === 'fail_closed' ? m : 'fail_open'))(
          defaultValues['channel_routing_setting.fail_mode']
        ),
        dry_run: defaultValues['channel_routing_setting.dry_run'],
        dry_run_sample_rate:
          defaultValues['channel_routing_setting.dry_run_sample_rate'],
        queue_max_wait_ms:
          defaultValues['channel_routing_setting.queue_max_wait_ms'],
        queue_poll_interval_ms:
          defaultValues['channel_routing_setting.queue_poll_interval_ms'],
      },
      url_health_setting: {
        fail_threshold: defaultValues['url_health_setting.fail_threshold'],
        cooldown_seconds: defaultValues['url_health_setting.cooldown_seconds'],
        ewma_alpha: defaultValues['url_health_setting.ewma_alpha'],
        hysteresis_ratio: defaultValues['url_health_setting.hysteresis_ratio'],
        hysteresis_min_ms:
          defaultValues['url_health_setting.hysteresis_min_ms'],
        exploration_gap_seconds:
          defaultValues['url_health_setting.exploration_gap_seconds'],
      },
    }),
    [defaultValues]
  )

  const form = useForm<RoutingFormInput, unknown, RoutingFormValues>({
    resolver: zodResolver(routingSchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const watchMode = form.watch('channel_routing_setting.mode')
  const watchDryRun = form.watch('channel_routing_setting.dry_run')
  const watchFullStrategy = form.watch('channel_routing_setting.full_strategy')

  const onSubmit = async (values: RoutingFormValues) => {
    const { saved } = await save(flatten(values), baselineRef.current)
    baselineRef.current = { ...baselineRef.current, ...saved }
  }

  return (
    <SettingsSection
      title={t('Routing & Capacity')}
      description={t(
        'Which channel a request is handed to: how load is spread across a priority layer, and which base URL of a channel is used'
      )}
    >
      <Form {...form}>
        <form
          autoComplete='off'
          className='space-y-6'
          onSubmit={form.handleSubmit(onSubmit)}
        >
          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Channel Routing Mode')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Switch between probabilistic (weighted random) and capacity (bucket overflow) routing. Default probabilistic is fully backward compatible.'
              )}
            </p>
          </div>

          <FormField
            control={form.control}
            name='channel_routing_setting.mode'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Default routing mode')}</FormLabel>
                <Select value={field.value} onValueChange={field.onChange}>
                  <FormControl>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    <SelectItem value='probabilistic'>
                      {t('Probabilistic (weighted random, default)')}
                    </SelectItem>
                    <SelectItem value='capacity'>
                      {t('Capacity (bucket overflow)')}
                    </SelectItem>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t(
                    'Global default. Channels can override individually. In capacity mode, channels at the same priority share requests in proportion to their bucket size (capacity_limit, falling back to weight).'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className='grid gap-6 sm:grid-cols-3'>
            <FormField
              control={form.control}
              name='channel_routing_setting.capacity_window_sec'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Capacity window (seconds)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={60}
                      step={60}
                      max={3600}
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
                      'Sliding window length. Must be a multiple of 60s (60–3600). Per-channel window only affects write TTL.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_routing_setting.full_strategy'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Full strategy')}</FormLabel>
                  <Select value={field.value} onValueChange={field.onChange}>
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      <SelectItem value='fallback'>
                        {t('Fallback to next priority')}
                      </SelectItem>
                      <SelectItem value='reject'>
                        {t('Reject (429)')}
                      </SelectItem>
                      <SelectItem value='degraded'>
                        {t('Degraded (least overloaded)')}
                      </SelectItem>
                      <SelectItem value='queue'>
                        {t('Queue (wait then 429)')}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {t(
                      'Action when all channels in the priority layer are full.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='channel_routing_setting.fail_mode'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Redis failure mode')}</FormLabel>
                  <Select value={field.value} onValueChange={field.onChange}>
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      <SelectItem value='fail_open'>
                        {t('Fail-open (downgrade to probabilistic)')}
                      </SelectItem>
                      <SelectItem value='fail_closed'>
                        {t('Fail-closed (treat as full)')}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {t('Behavior when Redis counter is unavailable.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='space-y-4 rounded-md border p-4'>
            <FormField
              control={form.control}
              name='channel_routing_setting.dry_run'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between'>
                  <div className='space-y-0.5 pe-4'>
                    <FormLabel>{t('Enable dry-run mode')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Counter still records traffic, but channel selection follows the original weighted-random path (no capacity filtering). Use this to evaluate impact before switching modes.'
                      )}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            {watchDryRun && (
              <FormField
                control={form.control}
                name='channel_routing_setting.dry_run_sample_rate'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Dry-run log sample rate')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        step={0.01}
                        min={0}
                        max={1}
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
                        'Fraction of selected channels that write a counter snapshot to logs. 0.01 = 1/100. Avoid log explosion under high QPS.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            )}
          </div>

          {watchFullStrategy === 'queue' && (
            <div className='grid gap-6 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='channel_routing_setting.queue_max_wait_ms'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Queue max wait (ms)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1000}
                        step={1000}
                        max={300000}
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
                        'When all channels are full, the maximum time a request waits in queue before returning 429. Keep it well below the nginx/relay timeout.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='channel_routing_setting.queue_poll_interval_ms'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Queue poll interval (ms)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={50}
                        step={50}
                        max={10000}
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
                        'How often a queued request re-checks for a free channel slot. Random jitter is added to avoid a thundering herd.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          )}

          {watchMode === 'capacity' && (
            <div className='rounded-md border border-amber-300 bg-amber-50 p-3 text-sm dark:border-amber-700 dark:bg-amber-950'>
              {t(
                '⚠ Capacity mode treats per-channel capacity_limit as the bucket size (falls back to weight if NULL). weight=0 + limit=NULL channels are always treated as full. Enable dry-run to observe impact before switching strategies.'
              )}
            </div>
          )}

          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Multi Base-URL Failover')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Circuit breaker and latency-aware (fastest) routing for channels configured with backup base URLs. Takes effect immediately.'
              )}
            </p>
          </div>

          <div className='grid gap-6 sm:grid-cols-3'>
            <FormField
              control={form.control}
              name='url_health_setting.fail_threshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Failure threshold')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      placeholder='3'
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
                      'Consecutive no-response failures on a URL before it is circuit-broken (temporarily skipped).'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='url_health_setting.cooldown_seconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Cooldown (seconds)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      placeholder='60'
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
                      'How long a circuit-broken URL stays out before a half-open retry probe.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <AdvancedFields>
            <div className='grid gap-6 sm:grid-cols-3'>
            <FormField
              control={form.control}
              name='url_health_setting.ewma_alpha'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('EWMA alpha')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      step='any'
                      min={0.01}
                      max={1}
                      placeholder='0.2'
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
                      'TTFB smoothing factor (0-1]. Higher reacts faster to recent latency; lower is smoother.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='url_health_setting.hysteresis_ratio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Hysteresis ratio')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      step='any'
                      min={0}
                      placeholder='0.2'
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
                      'A candidate URL must be faster than the current one by current*ratio to switch (anti-flapping).'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='url_health_setting.hysteresis_min_ms'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Hysteresis min (ms)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      step='any'
                      min={0}
                      placeholder='50'
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
                      'Absolute floor for the switch margin (ms); the larger of this and current*ratio wins.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='url_health_setting.exploration_gap_seconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Exploration gap (seconds)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      placeholder='30'
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
                      'Force-probe a healthy URL with no fresh sample beyond this gap, so a fallback stays ready if the fastest URL suddenly dies.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <p className='text-muted-foreground text-sm'>
            {t(
              'Network timeouts (connect / TLS handshake / streaming response header) are configured via env vars RELAY_DIAL_TIMEOUT / RELAY_TLS_HANDSHAKE_TIMEOUT / RELAY_STREAM_RESP_HEADER_TIMEOUT and take effect after restart.'
            )}
          </p>
          </AdvancedFields>

          <Separator />

          <Button type='submit' disabled={isSaving}>
            {isSaving ? t('Saving...') : t('Save routing settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

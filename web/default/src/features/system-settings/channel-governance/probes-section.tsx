import { useMemo, useRef } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { useOptionBatchSave } from '../hooks/use-option-batch-save'
import { useResetForm } from '../hooks/use-reset-form'
import { AdvancedFields } from './advanced-fields'
import { StatusCodeField } from './status-code-field'
import { statusCodeRules } from './status-code-rules'

const numericString = z.string().refine((value) => {
  const trimmed = value.trim()
  if (!trimmed) return true
  return !Number.isNaN(Number(trimmed)) && Number(trimmed) >= 0
}, 'Enter a non-negative number or leave empty')

const probesSchema = z.object({
  ChannelDisableThreshold: numericString,
  AutomaticDisableChannelEnabled: z.boolean(),
  AutomaticEnableChannelEnabled: z.boolean(),
  AutomaticDisableStatusCodes: statusCodeRules,
  AutomaticDisableKeywords: z.string(),
  monitor_setting: z.object({
    auto_test_channel_enabled: z.boolean(),
    auto_test_channel_minutes: z.coerce
      .number()
      .int()
      .min(1, 'Interval must be at least 1 minute'),
  }),
  channel_health_setting: z.object({
    degrade_probe_enabled: z.boolean(),
    degrade_probe_min_level: z.coerce.number().int().min(1).max(20),
    degrade_probe_minutes: z.coerce.number().int().min(1),
    degrade_probe_count: z.coerce.number().int().min(1),
    recovery_strategy: z.enum(['probe', 'manual']),
    recovery_probe_minutes: z.coerce.number().int().min(1),
  }),
  channel_verify_setting: z.object({
    auto_verify_enabled: z.boolean(),
    global_interval_minutes: z.coerce.number().int().min(0),
    scheduler_tick_minutes: z.coerce.number().int().min(0),
    score_drop_threshold: z.coerce.number().int().min(0),
    notify_on_failure: z.boolean(),
  }),
})

type ProbesFormValues = z.output<typeof probesSchema>
type ProbesFormInput = z.input<typeof probesSchema>

/** 扁平化后即 option key */
export type ProbesOptions = {
  ChannelDisableThreshold: string
  AutomaticDisableChannelEnabled: boolean
  AutomaticEnableChannelEnabled: boolean
  AutomaticDisableStatusCodes: string
  AutomaticDisableKeywords: string
  'monitor_setting.auto_test_channel_enabled': boolean
  'monitor_setting.auto_test_channel_minutes': number
  'channel_health_setting.degrade_probe_enabled': boolean
  'channel_health_setting.degrade_probe_min_level': number
  'channel_health_setting.degrade_probe_minutes': number
  'channel_health_setting.degrade_probe_count': number
  'channel_health_setting.recovery_strategy': string
  'channel_health_setting.recovery_probe_minutes': number
  'channel_verify_setting.auto_verify_enabled': boolean
  'channel_verify_setting.global_interval_minutes': number
  'channel_verify_setting.scheduler_tick_minutes': number
  'channel_verify_setting.score_drop_threshold': number
  'channel_verify_setting.notify_on_failure': boolean
}

export type ProbesSectionProps = {
  defaultValues: ProbesOptions
}

function flatten(values: ProbesFormValues): ProbesOptions {
  return {
    ChannelDisableThreshold: values.ChannelDisableThreshold.trim(),
    AutomaticDisableChannelEnabled: values.AutomaticDisableChannelEnabled,
    AutomaticEnableChannelEnabled: values.AutomaticEnableChannelEnabled,
    AutomaticDisableStatusCodes: values.AutomaticDisableStatusCodes,
    AutomaticDisableKeywords: values.AutomaticDisableKeywords,
    'monitor_setting.auto_test_channel_enabled':
      values.monitor_setting.auto_test_channel_enabled,
    'monitor_setting.auto_test_channel_minutes':
      values.monitor_setting.auto_test_channel_minutes,
    'channel_health_setting.degrade_probe_enabled':
      values.channel_health_setting.degrade_probe_enabled,
    'channel_health_setting.degrade_probe_min_level':
      values.channel_health_setting.degrade_probe_min_level,
    'channel_health_setting.degrade_probe_minutes':
      values.channel_health_setting.degrade_probe_minutes,
    'channel_health_setting.degrade_probe_count':
      values.channel_health_setting.degrade_probe_count,
    'channel_health_setting.recovery_strategy':
      values.channel_health_setting.recovery_strategy,
    'channel_health_setting.recovery_probe_minutes':
      values.channel_health_setting.recovery_probe_minutes,
    'channel_verify_setting.auto_verify_enabled':
      values.channel_verify_setting.auto_verify_enabled,
    'channel_verify_setting.global_interval_minutes':
      values.channel_verify_setting.global_interval_minutes,
    'channel_verify_setting.scheduler_tick_minutes':
      values.channel_verify_setting.scheduler_tick_minutes,
    'channel_verify_setting.score_drop_threshold':
      values.channel_verify_setting.score_drop_threshold,
    'channel_verify_setting.notify_on_failure':
      values.channel_verify_setting.notify_on_failure,
  }
}

export function ScheduledProbesSection({ defaultValues }: ProbesSectionProps) {
  const { t } = useTranslation()
  const { save, isSaving } = useOptionBatchSave()
  const baselineRef = useRef<ProbesOptions>({
    ...defaultValues,
    ChannelDisableThreshold: (
      defaultValues.ChannelDisableThreshold ?? ''
    ).trim(),
  })

  const formDefaults = useMemo<ProbesFormInput>(
    () => ({
      ChannelDisableThreshold: defaultValues.ChannelDisableThreshold ?? '',
      AutomaticDisableChannelEnabled:
        defaultValues.AutomaticDisableChannelEnabled,
      AutomaticEnableChannelEnabled:
        defaultValues.AutomaticEnableChannelEnabled,
      AutomaticDisableStatusCodes: defaultValues.AutomaticDisableStatusCodes,
      AutomaticDisableKeywords: defaultValues.AutomaticDisableKeywords,
      monitor_setting: {
        auto_test_channel_enabled:
          defaultValues['monitor_setting.auto_test_channel_enabled'],
        auto_test_channel_minutes:
          defaultValues['monitor_setting.auto_test_channel_minutes'],
      },
      channel_health_setting: {
        degrade_probe_enabled:
          defaultValues['channel_health_setting.degrade_probe_enabled'],
        degrade_probe_min_level:
          defaultValues['channel_health_setting.degrade_probe_min_level'],
        degrade_probe_minutes:
          defaultValues['channel_health_setting.degrade_probe_minutes'],
        degrade_probe_count:
          defaultValues['channel_health_setting.degrade_probe_count'],
        recovery_strategy: ((s) =>
          s === 'probe' || s === 'manual' ? s : 'probe')(
          defaultValues['channel_health_setting.recovery_strategy']
        ),
        recovery_probe_minutes:
          defaultValues['channel_health_setting.recovery_probe_minutes'],
      },
      channel_verify_setting: {
        auto_verify_enabled:
          defaultValues['channel_verify_setting.auto_verify_enabled'],
        global_interval_minutes:
          defaultValues['channel_verify_setting.global_interval_minutes'],
        scheduler_tick_minutes:
          defaultValues['channel_verify_setting.scheduler_tick_minutes'],
        score_drop_threshold:
          defaultValues['channel_verify_setting.score_drop_threshold'],
        notify_on_failure:
          defaultValues['channel_verify_setting.notify_on_failure'],
      },
    }),
    [defaultValues]
  )

  const form = useForm<ProbesFormInput, unknown, ProbesFormValues>({
    resolver: zodResolver(probesSchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const onSubmit = async (values: ProbesFormValues) => {
    const { saved } = await save(flatten(values), baselineRef.current)
    baselineRef.current = { ...baselineRef.current, ...saved }
  }

  return (
    <SettingsSection
      title={t('Scheduled Probes')}
      description={t(
        'The four timers that send requests on their own: all-channel testing, degrade probing, recovery probing and channel verification'
      )}
    >
      <Form {...form}>
        {/* eslint-disable-next-line react-hooks/refs */}
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('All-channel testing')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Sends one test request to every channel on a timer, then acts on the verdict.'
              )}
            </p>
          </div>

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='monitor_setting.auto_test_channel_enabled'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5 pe-4'>
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

          <FormField
            control={form.control}
            name='ChannelDisableThreshold'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Probe response timeout (seconds)')}</FormLabel>
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
                    'Upper bound on how long a probe response may take. A probe slower than this counts as a probe failure. This is not a waiting period before stopping a channel — live traffic latency is a separate setting under Degradation & stopping.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='AutomaticDisableChannelEnabled'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5 pe-4'>
                    <FormLabel className='text-base'>
                      {t('Stop on probe failure')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'Governs the probe path only: when a scheduled test fails, stop the channel. Consecutive errors from live traffic stop channels through a different path that does not read this switch — see Degradation & stopping.'
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

            <FormField
              control={form.control}
              name='AutomaticEnableChannelEnabled'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5 pe-4'>
                    <FormLabel className='text-base'>
                      {t('Enable on probe success')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'Brings an automatically stopped channel back after a passing test. Channels stopped by hand stay stopped, and channels managed by channel verification are skipped.'
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

          <StatusCodeField
            control={form.control}
            name='AutomaticDisableStatusCodes'
            label={t('Auto-disable status codes')}
            description={t(
              'When stop-on-failure is on, a response with one of these status codes disables the channel. Accepts codes and inclusive ranges.'
            )}
          />

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

          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>{t('Degrade probing')}</h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'A degraded channel gets a lowered priority, so it may stop receiving traffic entirely and never earn its way back. This timer sends it probe requests so successes can still accumulate. Requires channel health to be on.'
              )}
            </p>
          </div>

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='channel_health_setting.degrade_probe_enabled'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5 pe-4'>
                    <FormLabel className='text-base'>
                      {t('Probe degraded channels')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'On by default. Turning it off means a deeply degraded channel can only recover if real traffic still reaches it.'
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

            <FormField
              control={form.control}
              name='channel_health_setting.degrade_probe_min_level'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Minimum degrade level to probe')}</FormLabel>
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
                    {t(
                      'Only channels at this level or deeper are probed (default 1, i.e. every degraded channel)'
                    )}
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
                      'Number of probe requests per degraded channel per cycle'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>{t('Recovery probing')}</h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'A stopped channel receives no traffic at all, so the only way it can come back on its own is an explicit ping. Requires channel health to be on.'
              )}
            </p>
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
                  <FormLabel>
                    {t('Recovery probe interval (minutes)')}
                  </FormLabel>
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
                    {t('Probe interval for auto-disabled channels')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Alert>
            <Info />
            <AlertDescription>
              {t(
                'Recovery probing uses the channel test model, or the first model on the channel when none is set.'
              )}
            </AlertDescription>
          </Alert>

          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Channel verification schedule')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Re-runs external verification (external gateway) on Anthropic channels on a timer and emails admins when a score drops. Channels this schedule has stopped are explicitly skipped by all-channel testing above — for them, both stopping and coming back are decided by the score, not by the probe paths on this page.'
              )}
            </p>
          </div>

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='channel_verify_setting.auto_verify_enabled'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                  <div className='space-y-0.5 pe-4'>
                    <FormLabel className='text-base'>
                      {t('Enable scheduled verification')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'Master switch. When off, no channel is auto-verified.'
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

          <FormField
            control={form.control}
            name='channel_verify_setting.global_interval_minutes'
            render={({ field }) => (
              <FormItem className='md:max-w-sm'>
                <FormLabel>{t('Global interval (minutes)')}</FormLabel>
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
                    'Default re-test interval; per-channel value overrides this. Default 360 (6h).'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <AdvancedFields>
            <div className='grid gap-6 md:grid-cols-2'>
              <FormField
                control={form.control}
                name='channel_verify_setting.notify_on_failure'
                render={({ field }) => (
                  <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                    <div className='space-y-0.5 pe-4'>
                      <FormLabel className='text-base'>
                        {t('Alert on verification failure')}
                      </FormLabel>
                      <FormDescription>
                        {t(
                          'Also email admins when a scheduled verification cannot complete.'
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

              <FormField
                control={form.control}
                name='channel_verify_setting.scheduler_tick_minutes'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Scheduler scan interval (minutes)')}
                    </FormLabel>
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
                      {t('How often the scheduler scans for due channels.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='channel_verify_setting.score_drop_threshold'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Score drop alert threshold')}</FormLabel>
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
                        'Email admins when the new score is lower than last by at least this many points.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </AdvancedFields>

          <Separator />

          <Button type='submit' disabled={isSaving}>
            {isSaving ? t('Saving...') : t('Save probe settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

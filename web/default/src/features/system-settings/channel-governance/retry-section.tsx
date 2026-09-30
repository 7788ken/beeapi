import { useMemo, useRef } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'
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
import { Textarea } from '@/components/ui/textarea'
import { SettingsSection } from '../components/settings-section'
import { useOptionBatchSave } from '../hooks/use-option-batch-save'
import { useResetForm } from '../hooks/use-reset-form'
import { AdvancedFields } from './advanced-fields'
import { StatusCodeField } from './status-code-field'
import { statusCodeRules } from './status-code-rules'

const modelTimeoutsJson = z.string().refine(
  (value) => {
    if (value.trim() === '') return true
    try {
      const parsed = JSON.parse(value)
      if (
        typeof parsed !== 'object' ||
        Array.isArray(parsed) ||
        parsed === null
      ) {
        return false
      }
      return Object.values(parsed).every(
        (v) => typeof v === 'number' && Number.isFinite(v) && v > 0
      )
    } catch {
      return false
    }
  },
  { message: 'Must be valid JSON object: {"model":seconds,...}' }
)

const retrySchema = z.object({
  RetryTimes: z.coerce.number().int().min(0).max(10),
  AutomaticRetryStatusCodes: statusCodeRules,
  relay_retry_setting: z.object({
    total_timeout_seconds: z.coerce.number().int().min(0).max(86400),
    backoff_base_ms: z.coerce.number().int().min(0).max(60000),
    backoff_max_ms: z.coerce.number().int().min(0).max(600000),
    model_timeouts: modelTimeoutsJson,
  }),
  retry_short_circuit_setting: z.object({
    enabled: z.boolean(),
    min_duration_seconds: z.coerce.number().int().min(1).max(86400),
    ttl_minutes: z.coerce.number().int().min(1).max(1440),
  }),
})

type RetryFormValues = z.output<typeof retrySchema>
type RetryFormInput = z.input<typeof retrySchema>

/** 扁平化后即 option key */
export type RetryOptions = {
  RetryTimes: number
  AutomaticRetryStatusCodes: string
  'relay_retry_setting.total_timeout_seconds': number
  'relay_retry_setting.backoff_base_ms': number
  'relay_retry_setting.backoff_max_ms': number
  'relay_retry_setting.model_timeouts': string
  'retry_short_circuit_setting.enabled': boolean
  'retry_short_circuit_setting.min_duration_seconds': number
  'retry_short_circuit_setting.ttl_minutes': number
}

export type RetrySectionProps = {
  defaultValues: RetryOptions
}

function flatten(values: RetryFormValues): RetryOptions {
  const relay = values.relay_retry_setting
  const shortCircuit = values.retry_short_circuit_setting
  return {
    RetryTimes: values.RetryTimes,
    AutomaticRetryStatusCodes: values.AutomaticRetryStatusCodes,
    'relay_retry_setting.total_timeout_seconds': relay.total_timeout_seconds,
    'relay_retry_setting.backoff_base_ms': relay.backoff_base_ms,
    'relay_retry_setting.backoff_max_ms': relay.backoff_max_ms,
    // 只有空白的输入按空串存，避免 " " 被后端当成非法 JSON
    'relay_retry_setting.model_timeouts':
      relay.model_timeouts.trim() === '' ? '' : relay.model_timeouts,
    'retry_short_circuit_setting.enabled': shortCircuit.enabled,
    'retry_short_circuit_setting.min_duration_seconds':
      shortCircuit.min_duration_seconds,
    'retry_short_circuit_setting.ttl_minutes': shortCircuit.ttl_minutes,
  }
}

export function RetrySection({ defaultValues }: RetrySectionProps) {
  const { t } = useTranslation()
  const { save, isSaving } = useOptionBatchSave()
  const baselineRef = useRef<RetryOptions>({
    ...defaultValues,
    'relay_retry_setting.model_timeouts':
      (defaultValues['relay_retry_setting.model_timeouts'] ?? '').trim() === ''
        ? ''
        : defaultValues['relay_retry_setting.model_timeouts'],
  })

  const formDefaults = useMemo<RetryFormInput>(
    () => ({
      RetryTimes: defaultValues.RetryTimes,
      AutomaticRetryStatusCodes: defaultValues.AutomaticRetryStatusCodes,
      relay_retry_setting: {
        total_timeout_seconds:
          defaultValues['relay_retry_setting.total_timeout_seconds'],
        backoff_base_ms: defaultValues['relay_retry_setting.backoff_base_ms'],
        backoff_max_ms: defaultValues['relay_retry_setting.backoff_max_ms'],
        model_timeouts:
          defaultValues['relay_retry_setting.model_timeouts'] ?? '',
      },
      retry_short_circuit_setting: {
        enabled: defaultValues['retry_short_circuit_setting.enabled'],
        min_duration_seconds:
          defaultValues['retry_short_circuit_setting.min_duration_seconds'],
        ttl_minutes: defaultValues['retry_short_circuit_setting.ttl_minutes'],
      },
    }),
    [defaultValues]
  )

  const form = useForm<RetryFormInput, unknown, RetryFormValues>({
    resolver: zodResolver(retrySchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const onSubmit = async (values: RetryFormValues) => {
    const { saved } = await save(flatten(values), baselineRef.current)
    baselineRef.current = { ...baselineRef.current, ...saved }
  }

  return (
    <SettingsSection
      title={t('Retry & Timeout')}
      description={t(
        'How many times a failed request is retried, how long the whole attempt may take, and when a replay the client already gave up on is refused outright'
      )}
    >
      <Form {...form}>
        <form
          autoComplete='off'
          className='space-y-6'
          // eslint-disable-next-line react-hooks/refs
          onSubmit={form.handleSubmit(onSubmit)}
        >
          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>{t('Retry count')}</h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'How many further channels a failed request may be handed to before the error reaches the client.'
              )}
            </p>
          </div>

          <FormField
            control={form.control}
            name='RetryTimes'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Retry Times')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    max={10}
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
                    name={field.name}
                    onBlur={field.onBlur}
                    ref={field.ref}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Number of times to retry failed requests (0-10). Every retry picks a different channel, so a Claude request loses its upstream prompt cache on each one and the whole input is billed again at full price.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <StatusCodeField
            control={form.control}
            name='AutomaticRetryStatusCodes'
            label={t('Auto-retry status codes')}
            description={t(
              'A failed request is retried on another channel only when its status code matches this list.'
            )}
          />

          <Alert>
            <Info />
            <AlertTitle>
              {t('Some requests never retry, whatever this number says')}
            </AlertTitle>
            <AlertDescription>
              {t(
                'A channel affinity rule with "skip retry on failure" short-circuits the whole retry chain: a request matching such a rule is returned to the client on the first failure and the retry count above does not apply to it. Both built-in rules — codex cli trace and claude cli trace — have that option on. Review them under Routing & Capacity.'
              )}
            </AlertDescription>
          </Alert>

          <AdvancedFields>
          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Timeout and backoff')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'The budget the retry loop spends: one deadline for the whole request, and how long it waits between attempts.'
              )}
            </p>
          </div>

          <div className='grid gap-6 sm:grid-cols-3'>
            <FormField
              control={form.control}
              name='relay_retry_setting.total_timeout_seconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Total deadline (seconds)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      placeholder='120'
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
                      'Whole-request budget across all retries. 0 disables. Recommend < nginx proxy_read_timeout * 0.9.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='relay_retry_setting.backoff_base_ms'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Backoff base (ms)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      placeholder='200'
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
                      'Initial wait before retry. Doubles each attempt. 0 disables backoff.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='relay_retry_setting.backoff_max_ms'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Backoff max (ms)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      placeholder='2000'
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
                    {t('Cap for a single backoff wait. 0 = no cap.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name='relay_retry_setting.model_timeouts'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Per-model timeout overrides')}</FormLabel>
                <FormControl>
                  <Textarea
                    rows={6}
                    placeholder='{"gpt-4o-mini":15,"gpt-5-*":600,"sora-2":900}'
                    className='font-mono text-sm'
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'JSON object mapping model name (or prefix ending with *) to per-call timeout in seconds. Resolution: exact > longest prefix > global RELAY_TIMEOUT.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Retry short-circuit on client disconnect')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'After a client times out and cancels a long-running non-stream request, identical replays within the TTL are rejected with 400 (official SDKs do not auto-retry 400), stopping repeated spend. Streaming requests are unaffected; changes take effect within 60 seconds.'
              )}
            </p>
          </div>

          <FormField
            control={form.control}
            name='retry_short_circuit_setting.enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5 pe-4'>
                  <FormLabel className='text-base'>
                    {t('Enable retry short-circuit')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Reject identical replays of non-stream requests the client just canceled'
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

          <div className='grid gap-6 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='retry_short_circuit_setting.min_duration_seconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Minimum runtime (seconds)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      placeholder='300'
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
                      'Only requests canceled by the client after running longer than this are recorded'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='retry_short_circuit_setting.ttl_minutes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Fingerprint TTL (minutes)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      placeholder='15'
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
                      'Maximum wait after the client adjusts its configuration'
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
            {isSaving ? t('Saving...') : t('Save retry settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

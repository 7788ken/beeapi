import { z } from 'zod'
import { useForm, type Resolver } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const schema = z.object({
  failThreshold: z.coerce.number().int().min(1).max(100),
  cooldownSeconds: z.coerce.number().int().min(1).max(86400),
  ewmaAlpha: z.coerce.number().min(0.01).max(1),
  hysteresisRatio: z.coerce.number().min(0).max(10),
  hysteresisMinMs: z.coerce.number().min(0).max(600000),
  explorationGapSeconds: z.coerce.number().int().min(1).max(86400),
})

type Values = z.infer<typeof schema>

interface Props {
  defaultValues: {
    'url_health_setting.fail_threshold': number
    'url_health_setting.cooldown_seconds': number
    'url_health_setting.ewma_alpha': number
    'url_health_setting.hysteresis_ratio': number
    'url_health_setting.hysteresis_min_ms': number
    'url_health_setting.exploration_gap_seconds': number
  }
}

export function UrlHealthSection({ defaultValues }: Props) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const initial: Values = {
    failThreshold: defaultValues['url_health_setting.fail_threshold'] ?? 3,
    cooldownSeconds: defaultValues['url_health_setting.cooldown_seconds'] ?? 60,
    ewmaAlpha: defaultValues['url_health_setting.ewma_alpha'] ?? 0.2,
    hysteresisRatio: defaultValues['url_health_setting.hysteresis_ratio'] ?? 0.2,
    hysteresisMinMs: defaultValues['url_health_setting.hysteresis_min_ms'] ?? 50,
    explorationGapSeconds:
      defaultValues['url_health_setting.exploration_gap_seconds'] ?? 30,
  }

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: initial,
  })

  const { isDirty, isSubmitting } = form.formState

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = []

    if (values.failThreshold !== initial.failThreshold) {
      updates.push({
        key: 'url_health_setting.fail_threshold',
        value: String(values.failThreshold),
      })
    }
    if (values.cooldownSeconds !== initial.cooldownSeconds) {
      updates.push({
        key: 'url_health_setting.cooldown_seconds',
        value: String(values.cooldownSeconds),
      })
    }
    if (values.ewmaAlpha !== initial.ewmaAlpha) {
      updates.push({
        key: 'url_health_setting.ewma_alpha',
        value: String(values.ewmaAlpha),
      })
    }
    if (values.hysteresisRatio !== initial.hysteresisRatio) {
      updates.push({
        key: 'url_health_setting.hysteresis_ratio',
        value: String(values.hysteresisRatio),
      })
    }
    if (values.hysteresisMinMs !== initial.hysteresisMinMs) {
      updates.push({
        key: 'url_health_setting.hysteresis_min_ms',
        value: String(values.hysteresisMinMs),
      })
    }
    if (values.explorationGapSeconds !== initial.explorationGapSeconds) {
      updates.push({
        key: 'url_health_setting.exploration_gap_seconds',
        value: String(values.explorationGapSeconds),
      })
    }

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }

    form.reset(values)
  }

  return (
    <SettingsSection
      title={t('Multi Base-URL Failover')}
      description={t(
        'Circuit breaker and latency-aware (fastest) routing for channels configured with backup base URLs. Takes effect immediately.'
      )}
    >
      <Form {...form}>
        <form
          onSubmit={form.handleSubmit(onSubmit)}
          autoComplete='off'
          className='space-y-6'
        >
          <div className='grid gap-6 sm:grid-cols-3'>
            <FormField
              control={form.control}
              name='failThreshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Failure threshold')}</FormLabel>
                  <FormControl>
                    <Input type='number' min={1} placeholder='3' {...field} />
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
              name='cooldownSeconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Cooldown (seconds)')}</FormLabel>
                  <FormControl>
                    <Input type='number' min={1} placeholder='60' {...field} />
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

            <FormField
              control={form.control}
              name='ewmaAlpha'
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
                      {...field}
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
              name='hysteresisRatio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Hysteresis ratio')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      step='any'
                      min={0}
                      placeholder='0.2'
                      {...field}
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
              name='hysteresisMinMs'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Hysteresis min (ms)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      step='any'
                      min={0}
                      placeholder='50'
                      {...field}
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
              name='explorationGapSeconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Exploration gap (seconds)')}</FormLabel>
                  <FormControl>
                    <Input type='number' min={1} placeholder='30' {...field} />
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

          <Button
            type='submit'
            disabled={!isDirty || updateOption.isPending || isSubmitting}
          >
            {updateOption.isPending || isSubmitting
              ? t('Saving...')
              : t('Save failover settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

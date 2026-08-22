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
import { Textarea } from '@/components/ui/textarea'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const schema = z.object({
  totalTimeoutSeconds: z.coerce.number().int().min(0).max(86400),
  backoffBaseMs: z.coerce.number().int().min(0).max(60000),
  backoffMaxMs: z.coerce.number().int().min(0).max(600000),
  modelTimeouts: z
    .string()
    .refine(
      (val) => {
        if (val.trim() === '') return true
        try {
          const parsed = JSON.parse(val)
          if (typeof parsed !== 'object' || Array.isArray(parsed) || parsed === null) {
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
    ),
})

type Values = z.infer<typeof schema>

interface Props {
  defaultValues: {
    'relay_retry_setting.total_timeout_seconds': number
    'relay_retry_setting.backoff_base_ms': number
    'relay_retry_setting.backoff_max_ms': number
    'relay_retry_setting.model_timeouts': string
  }
}

export function RelayRetrySection({ defaultValues }: Props) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const initial: Values = {
    totalTimeoutSeconds: defaultValues['relay_retry_setting.total_timeout_seconds'] ?? 0,
    backoffBaseMs: defaultValues['relay_retry_setting.backoff_base_ms'] ?? 0,
    backoffMaxMs: defaultValues['relay_retry_setting.backoff_max_ms'] ?? 2000,
    modelTimeouts: defaultValues['relay_retry_setting.model_timeouts'] ?? '',
  }

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: initial,
  })

  const { isDirty, isSubmitting } = form.formState

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = []

    if (values.totalTimeoutSeconds !== initial.totalTimeoutSeconds) {
      updates.push({
        key: 'relay_retry_setting.total_timeout_seconds',
        value: String(values.totalTimeoutSeconds),
      })
    }
    if (values.backoffBaseMs !== initial.backoffBaseMs) {
      updates.push({
        key: 'relay_retry_setting.backoff_base_ms',
        value: String(values.backoffBaseMs),
      })
    }
    if (values.backoffMaxMs !== initial.backoffMaxMs) {
      updates.push({
        key: 'relay_retry_setting.backoff_max_ms',
        value: String(values.backoffMaxMs),
      })
    }
    if (values.modelTimeouts !== initial.modelTimeouts) {
      // normalize whitespace-only to empty
      const normalized = values.modelTimeouts.trim() === '' ? '' : values.modelTimeouts
      updates.push({
        key: 'relay_retry_setting.model_timeouts',
        value: normalized,
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
      title={t('Relay Retry & Timeout')}
      description={t(
        'Per-request total deadline, exponential backoff, and per-model timeout overrides for the retry loop'
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
              name='totalTimeoutSeconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Total deadline (seconds)')}</FormLabel>
                  <FormControl>
                    <Input type='number' min={0} placeholder='120' {...field} />
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
              name='backoffBaseMs'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Backoff base (ms)')}</FormLabel>
                  <FormControl>
                    <Input type='number' min={0} placeholder='200' {...field} />
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
              name='backoffMaxMs'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Backoff max (ms)')}</FormLabel>
                  <FormControl>
                    <Input type='number' min={0} placeholder='2000' {...field} />
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
            name='modelTimeouts'
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

          <Button
            type='submit'
            disabled={!isDirty || updateOption.isPending || isSubmitting}
          >
            {updateOption.isPending || isSubmitting
              ? t('Saving...')
              : t('Save relay retry settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

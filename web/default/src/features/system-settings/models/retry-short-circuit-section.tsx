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
import { Switch } from '@/components/ui/switch'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const schema = z.object({
  enabled: z.boolean(),
  minDurationSeconds: z.coerce.number().int().min(1).max(86400),
  ttlMinutes: z.coerce.number().int().min(1).max(1440),
})

type Values = z.infer<typeof schema>

interface Props {
  defaultValues: {
    'retry_short_circuit_setting.enabled': boolean
    'retry_short_circuit_setting.min_duration_seconds': number
    'retry_short_circuit_setting.ttl_minutes': number
  }
}

export function RetryShortCircuitSection({ defaultValues }: Props) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const initial: Values = {
    enabled: defaultValues['retry_short_circuit_setting.enabled'] ?? false,
    minDurationSeconds:
      defaultValues['retry_short_circuit_setting.min_duration_seconds'] ?? 300,
    ttlMinutes: defaultValues['retry_short_circuit_setting.ttl_minutes'] ?? 15,
  }

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: initial,
  })

  const { isDirty, isSubmitting } = form.formState

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = []

    if (values.enabled !== initial.enabled) {
      updates.push({
        key: 'retry_short_circuit_setting.enabled',
        value: String(values.enabled),
      })
    }
    if (values.minDurationSeconds !== initial.minDurationSeconds) {
      updates.push({
        key: 'retry_short_circuit_setting.min_duration_seconds',
        value: String(values.minDurationSeconds),
      })
    }
    if (values.ttlMinutes !== initial.ttlMinutes) {
      updates.push({
        key: 'retry_short_circuit_setting.ttl_minutes',
        value: String(values.ttlMinutes),
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
      title={t('Retry short-circuit for client-canceled non-stream requests')}
      description={t(
        'After a client times out and cancels a long-running non-stream request, identical replays within the TTL are rejected with 400 (official SDKs do not auto-retry 400), stopping repeated spend. Streaming requests are unaffected; changes take effect within 60 seconds.'
      )}
    >
      <Form {...form}>
        <form
          onSubmit={form.handleSubmit(onSubmit)}
          autoComplete='off'
          className='space-y-6'
        >
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5'>
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
                    disabled={updateOption.isPending || isSubmitting}
                  />
                </FormControl>
              </FormItem>
            )}
          />

          <div className='grid gap-6 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='minDurationSeconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Minimum runtime (seconds)')}</FormLabel>
                  <FormControl>
                    <Input type='number' min={1} placeholder='300' {...field} />
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
              name='ttlMinutes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Fingerprint TTL (minutes)')}</FormLabel>
                  <FormControl>
                    <Input type='number' min={1} placeholder='15' {...field} />
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

          <Button
            type='submit'
            disabled={!isDirty || updateOption.isPending || isSubmitting}
          >
            {updateOption.isPending || isSubmitting
              ? t('Saving...')
              : t('Save retry short-circuit settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

import { useEffect, useMemo, useRef } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
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

// react-hook-form 把带点号的 field name 当作嵌套路径，所以表单内部必须用
// 嵌套结构（token_health_setting.xxx），提交时再展平回后端要求的点号 key。
const tokenHealthSchema = z.object({
  token_health_setting: z.object({
    enabled: z.boolean(),
    window_minutes: z.number().min(1),
    min_requests: z.number().min(1),
    error_rate_threshold: z.number().gt(0).max(1),
    cooldown_minutes: z.number().min(1),
    excluded_status_codes: z.string(),
  }),
})

type TokenHealthFormValues = z.infer<typeof tokenHealthSchema>

type FlatTokenHealthValues = {
  'token_health_setting.enabled': boolean
  'token_health_setting.window_minutes': number
  'token_health_setting.min_requests': number
  'token_health_setting.error_rate_threshold': number
  'token_health_setting.cooldown_minutes': number
  'token_health_setting.excluded_status_codes': string
}

type TokenHealthSectionProps = {
  defaultValues: FlatTokenHealthValues
}

const buildFormDefaults = (
  defaults: FlatTokenHealthValues
): TokenHealthFormValues => ({
  token_health_setting: {
    enabled: defaults['token_health_setting.enabled'],
    window_minutes: defaults['token_health_setting.window_minutes'],
    min_requests: defaults['token_health_setting.min_requests'],
    error_rate_threshold: defaults['token_health_setting.error_rate_threshold'],
    cooldown_minutes: defaults['token_health_setting.cooldown_minutes'],
    excluded_status_codes:
      defaults['token_health_setting.excluded_status_codes'],
  },
})

const normalizeFormValues = (
  values: TokenHealthFormValues
): FlatTokenHealthValues => ({
  'token_health_setting.enabled': values.token_health_setting.enabled,
  'token_health_setting.window_minutes':
    values.token_health_setting.window_minutes,
  'token_health_setting.min_requests': values.token_health_setting.min_requests,
  'token_health_setting.error_rate_threshold':
    values.token_health_setting.error_rate_threshold,
  'token_health_setting.cooldown_minutes':
    values.token_health_setting.cooldown_minutes,
  'token_health_setting.excluded_status_codes':
    values.token_health_setting.excluded_status_codes,
})

export function TokenHealthSection({ defaultValues }: TokenHealthSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const baselineRef = useRef<FlatTokenHealthValues>(defaultValues)

  const formDefaults = useMemo(
    () => buildFormDefaults(defaultValues),
    [defaultValues]
  )

  const form = useForm<TokenHealthFormValues>({
    resolver: zodResolver(tokenHealthSchema),
    mode: 'onChange',
    defaultValues: formDefaults,
  })

  useEffect(() => {
    baselineRef.current = defaultValues
    form.reset(buildFormDefaults(defaultValues))
  }, [defaultValues, form])

  const onSubmit = async (values: TokenHealthFormValues) => {
    const normalized = normalizeFormValues(values)
    const updates = (
      Object.keys(normalized) as Array<keyof FlatTokenHealthValues>
    ).filter((key) => normalized[key] !== baselineRef.current[key])

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const key of updates) {
      await updateOption.mutateAsync({ key, value: normalized[key] })
    }

    baselineRef.current = normalized
  }

  return (
    <SettingsSection
      title={t('Token Error-Rate Circuit Breaker')}
      description={t(
        'Temporarily throttle an API key when its client-side error rate is too high'
      )}
    >
      <Form {...form}>
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
          <FormField
            control={form.control}
            name='token_health_setting.enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5'>
                  <FormLabel className='text-base'>
                    {t('Enable error-rate circuit breaker')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Temporarily disable an API key for a cooldown period when its client-side error rate (4xx, excluding 429) is too high, preventing a faulty key from dragging down the system'
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

          <div className='grid gap-4 md:grid-cols-3'>
            <FormField
              control={form.control}
              name='token_health_setting.window_minutes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Statistics window (minutes)')}</FormLabel>
                  <FormControl>
                    <div className='flex items-center gap-2'>
                      <Input
                        type='number'
                        min={1}
                        step={1}
                        {...field}
                        onChange={(e) =>
                          field.onChange(parseInt(e.target.value) || 1)
                        }
                      />
                      <span className='text-muted-foreground text-sm'>
                        {t('minutes')}
                      </span>
                    </div>
                  </FormControl>
                  <FormDescription>
                    {t('Sliding window used to compute the error rate')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='token_health_setting.min_requests'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t('Minimum requests (within window)')}
                  </FormLabel>
                  <FormControl>
                    <div className='flex items-center gap-2'>
                      <Input
                        type='number'
                        min={1}
                        step={1}
                        {...field}
                        onChange={(e) =>
                          field.onChange(parseInt(e.target.value) || 1)
                        }
                      />
                      <span className='text-muted-foreground text-sm'>
                        {t('times')}
                      </span>
                    </div>
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Minimum number of requests in the window before the key is evaluated'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='token_health_setting.cooldown_minutes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Cooldown duration (minutes)')}</FormLabel>
                  <FormControl>
                    <div className='flex items-center gap-2'>
                      <Input
                        type='number'
                        min={1}
                        step={1}
                        {...field}
                        onChange={(e) =>
                          field.onChange(parseInt(e.target.value) || 1)
                        }
                      />
                      <span className='text-muted-foreground text-sm'>
                        {t('minutes')}
                      </span>
                    </div>
                  </FormControl>
                  <FormDescription>
                    {t('How long the key stays disabled after a trip')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name='token_health_setting.error_rate_threshold'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Error-rate threshold (0~1)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    max={1}
                    step={0.05}
                    {...field}
                    onChange={(e) =>
                      field.onChange(parseFloat(e.target.value) || 0)
                    }
                  />
                </FormControl>
                <FormDescription>
                  {t('A value between 0 and 1, e.g. 0.9 means 90%')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='token_health_setting.excluded_status_codes'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Additional excluded status codes')}</FormLabel>
                <FormControl>
                  <Input placeholder={t('401,403')} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    'Comma-separated, e.g. 401,403; 429 is always excluded'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <Button type='submit' disabled={updateOption.isPending}>
            {updateOption.isPending
              ? t('Saving...')
              : t('Save circuit breaker settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

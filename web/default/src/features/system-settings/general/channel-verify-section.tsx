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
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

// 点号 GlobalConfig key（channel_verify_setting.*）必须用「嵌套 zod + 提交时展平」，
// 否则 react-hook-form 把点号当嵌套路径，提交退化成顶层 key、后端静默不写。照抄 ssrf-section。
const channelVerifySchema = z.object({
  channel_verify_setting: z.object({
    auto_verify_enabled: z.boolean(),
    global_interval_minutes: z.string(),
    score_drop_threshold: z.string(),
    notify_on_failure: z.boolean(),
    scheduler_tick_minutes: z.string(),
  }),
})

type ChannelVerifyFormValues = z.output<typeof channelVerifySchema>
type ChannelVerifyFormInput = z.input<typeof channelVerifySchema>

type NormalizedValues = {
  'channel_verify_setting.auto_verify_enabled': boolean
  'channel_verify_setting.global_interval_minutes': number
  'channel_verify_setting.score_drop_threshold': number
  'channel_verify_setting.notify_on_failure': boolean
  'channel_verify_setting.scheduler_tick_minutes': number
}

type ChannelVerifySectionProps = {
  defaultValues: NormalizedValues
}

const toInt = (value: string, fallback = 0) => {
  const n = Number.parseInt(value.trim(), 10)
  return Number.isFinite(n) ? n : fallback
}

const buildFormDefaults = (
  d: NormalizedValues
): ChannelVerifyFormInput => ({
  channel_verify_setting: {
    auto_verify_enabled: d['channel_verify_setting.auto_verify_enabled'],
    global_interval_minutes: String(
      d['channel_verify_setting.global_interval_minutes']
    ),
    score_drop_threshold: String(
      d['channel_verify_setting.score_drop_threshold']
    ),
    notify_on_failure: d['channel_verify_setting.notify_on_failure'],
    scheduler_tick_minutes: String(
      d['channel_verify_setting.scheduler_tick_minutes']
    ),
  },
})

const normalizeFormValues = (
  values: ChannelVerifyFormValues
): NormalizedValues => ({
  'channel_verify_setting.auto_verify_enabled':
    values.channel_verify_setting.auto_verify_enabled,
  'channel_verify_setting.global_interval_minutes': toInt(
    values.channel_verify_setting.global_interval_minutes
  ),
  'channel_verify_setting.score_drop_threshold': toInt(
    values.channel_verify_setting.score_drop_threshold
  ),
  'channel_verify_setting.notify_on_failure':
    values.channel_verify_setting.notify_on_failure,
  'channel_verify_setting.scheduler_tick_minutes': toInt(
    values.channel_verify_setting.scheduler_tick_minutes
  ),
})

export function ChannelVerifySection({
  defaultValues,
}: ChannelVerifySectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const baselineRef = useRef<NormalizedValues>(defaultValues)

  const formDefaults = useMemo(
    () => buildFormDefaults(defaultValues),
    [defaultValues]
  )

  const form = useForm<
    ChannelVerifyFormInput,
    unknown,
    ChannelVerifyFormValues
  >({
    resolver: zodResolver(channelVerifySchema),
    defaultValues: formDefaults,
  })

  useEffect(() => {
    baselineRef.current = defaultValues
    form.reset(buildFormDefaults(defaultValues))
  }, [defaultValues, form])

  const onSubmit = async (data: ChannelVerifyFormValues) => {
    const normalized = normalizeFormValues(data)
    const updates = (
      Object.keys(normalized) as Array<keyof NormalizedValues>
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
      title={t('Channel Verify Schedule')}
      description={t(
        'Periodically re-run external verification (external gateway) on Anthropic channels and alert admins when scores drop.'
      )}
    >
      <Form {...form}>
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
          <FormField
            control={form.control}
            name='channel_verify_setting.auto_verify_enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5'>
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

          <FormField
            control={form.control}
            name='channel_verify_setting.global_interval_minutes'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Global interval (minutes)')}</FormLabel>
                <FormControl>
                  <Input type='number' min={0} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    'Default re-test interval; per-channel value overrides this. Default 360 (6h).'
                  )}
                </FormDescription>
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
                  <Input type='number' min={0} {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    'Email admins when the new score is lower than last by at least this many points.'
                  )}
                </FormDescription>
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='channel_verify_setting.scheduler_tick_minutes'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Scheduler scan interval (minutes)')}</FormLabel>
                <FormControl>
                  <Input type='number' min={0} {...field} />
                </FormControl>
                <FormDescription>
                  {t('How often the scheduler scans for due channels.')}
                </FormDescription>
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='channel_verify_setting.notify_on_failure'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5'>
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

          <Button type='submit' disabled={updateOption.isPending}>
            {updateOption.isPending
              ? t('Saving...')
              : t('Save verify settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

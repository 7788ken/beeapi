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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

// 渠道路由模式（概率 / 容量）
// docs/2026-05-26-channel-routing-mode-switchable.md
const schema = z.object({
  mode: z.enum(['probabilistic', 'capacity']),
  capacityWindowSec: z.coerce
    .number()
    .int()
    .min(60)
    .max(3600)
    .refine((v) => v % 60 === 0, {
      message: 'Must be a multiple of 60 seconds',
    }),
  fullStrategy: z.enum(['fallback', 'reject', 'degraded', 'queue']),
  failMode: z.enum(['fail_open', 'fail_closed']),
  dryRun: z.boolean(),
  dryRunSampleRate: z.coerce.number().min(0).max(1),
  queueMaxWaitMs: z.coerce.number().int().min(1000).max(300000),
  queuePollIntervalMs: z.coerce.number().int().min(50).max(10000),
})

type Values = z.infer<typeof schema>

interface Props {
  defaultValues: {
    'channel_routing_setting.mode': string
    'channel_routing_setting.capacity_window_sec': number
    'channel_routing_setting.full_strategy': string
    'channel_routing_setting.fail_mode': string
    'channel_routing_setting.dry_run': boolean
    'channel_routing_setting.dry_run_sample_rate': number
    'channel_routing_setting.queue_max_wait_ms': number
    'channel_routing_setting.queue_poll_interval_ms': number
  }
}

export function ChannelRoutingSection({ defaultValues }: Props) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const initial: Values = {
    mode:
      (defaultValues['channel_routing_setting.mode'] as
        | 'probabilistic'
        | 'capacity') ?? 'probabilistic',
    capacityWindowSec: (() => {
      const raw =
        defaultValues['channel_routing_setting.capacity_window_sec'] ?? 60
      // 后端要求 60 倍数，UI 上加载时把历史非法值规整到合法档位
      const clamped = Math.min(3600, Math.max(60, Number(raw) || 60))
      return Math.ceil(clamped / 60) * 60
    })(),
    fullStrategy:
      (defaultValues['channel_routing_setting.full_strategy'] as
        | 'fallback'
        | 'reject'
        | 'degraded'
        | 'queue') ?? 'fallback',
    failMode:
      (defaultValues['channel_routing_setting.fail_mode'] as
        | 'fail_open'
        | 'fail_closed') ?? 'fail_open',
    dryRun: defaultValues['channel_routing_setting.dry_run'] ?? false,
    dryRunSampleRate:
      defaultValues['channel_routing_setting.dry_run_sample_rate'] ?? 0.01,
    queueMaxWaitMs:
      defaultValues['channel_routing_setting.queue_max_wait_ms'] ?? 30000,
    queuePollIntervalMs:
      defaultValues['channel_routing_setting.queue_poll_interval_ms'] ?? 500,
  }

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: initial,
  })

  const { isDirty, isSubmitting } = form.formState
  const watchMode = form.watch('mode')
  const watchDryRun = form.watch('dryRun')
  const watchFullStrategy = form.watch('fullStrategy')

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = []
    if (values.mode !== initial.mode) {
      updates.push({ key: 'channel_routing_setting.mode', value: values.mode })
    }
    if (values.capacityWindowSec !== initial.capacityWindowSec) {
      updates.push({
        key: 'channel_routing_setting.capacity_window_sec',
        value: String(values.capacityWindowSec),
      })
    }
    if (values.fullStrategy !== initial.fullStrategy) {
      updates.push({
        key: 'channel_routing_setting.full_strategy',
        value: values.fullStrategy,
      })
    }
    if (values.failMode !== initial.failMode) {
      updates.push({
        key: 'channel_routing_setting.fail_mode',
        value: values.failMode,
      })
    }
    if (values.dryRun !== initial.dryRun) {
      updates.push({
        key: 'channel_routing_setting.dry_run',
        value: String(values.dryRun),
      })
    }
    if (values.dryRunSampleRate !== initial.dryRunSampleRate) {
      updates.push({
        key: 'channel_routing_setting.dry_run_sample_rate',
        value: String(values.dryRunSampleRate),
      })
    }
    if (values.queueMaxWaitMs !== initial.queueMaxWaitMs) {
      updates.push({
        key: 'channel_routing_setting.queue_max_wait_ms',
        value: String(values.queueMaxWaitMs),
      })
    }
    if (values.queuePollIntervalMs !== initial.queuePollIntervalMs) {
      updates.push({
        key: 'channel_routing_setting.queue_poll_interval_ms',
        value: String(values.queuePollIntervalMs),
      })
    }
    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    for (const u of updates) {
      await updateOption.mutateAsync(u)
    }
    form.reset(values)
  }

  return (
    <SettingsSection
      title={t('Channel Routing Mode')}
      description={t(
        'Switch between probabilistic (weighted random) and capacity (bucket overflow) routing. Default probabilistic is fully backward compatible.'
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
            name='mode'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Default routing mode')}</FormLabel>
                <Select
                  value={field.value}
                  onValueChange={field.onChange}
                >
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
              name='capacityWindowSec'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Capacity window (seconds)')}</FormLabel>
                  <FormControl>
                    <Input type='number' min={60} step={60} max={3600} {...field} />
                  </FormControl>
                  <FormDescription>
                    {t('Sliding window length. Must be a multiple of 60s (60–3600). Per-channel window only affects write TTL.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='fullStrategy'
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
                    {t('Action when all channels in the priority layer are full.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='failMode'
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
              name='dryRun'
              render={({ field }) => (
                <FormItem className='flex flex-row items-center justify-between'>
                  <div className='space-y-0.5'>
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
                name='dryRunSampleRate'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Dry-run log sample rate')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        step={0.01}
                        min={0}
                        max={1}
                        {...field}
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
                name='queueMaxWaitMs'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Queue max wait (ms)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1000}
                        step={1000}
                        max={300000}
                        {...field}
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
                name='queuePollIntervalMs'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Queue poll interval (ms)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={50}
                        step={50}
                        max={10000}
                        {...field}
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

          <Button
            type='submit'
            disabled={!isDirty || updateOption.isPending || isSubmitting}
          >
            {updateOption.isPending || isSubmitting
              ? t('Saving...')
              : t('Save channel routing settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

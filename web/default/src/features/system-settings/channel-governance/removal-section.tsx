import { useMemo, useRef } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'
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
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { TriangleAlert } from 'lucide-react'
import { SettingsSection } from '../components/settings-section'
import { useOptionBatchSave } from '../hooks/use-option-batch-save'
import { useResetForm } from '../hooks/use-reset-form'
import { AdvancedFields } from './advanced-fields'
import { findOverlappingKeywords } from './conflict-detection'
import { StatusCodeField } from './status-code-field'
import { statusCodeRules } from './status-code-rules'

const removalSchema = z.object({
  ModelMissingRemovalEnabled: z.boolean(),
  ModelMissingKeywords: z.string(),
  ModelMissingRemovalCooldownSeconds: z.coerce.number().int().min(1),
  ModelMissingRecheckIntervalSeconds: z.coerce.number().int().min(60),
  ModelRateLimitRemovalEnabled: z.boolean(),
  ModelRateLimitRecheckIntervalSeconds: z.coerce.number().int().min(60),
  ModelForbiddenRemovalEnabled: z.boolean(),
  ModelForbiddenStatusCodes: statusCodeRules,
  ModelForbiddenKeywords: z.string(),
  ModelForbiddenRecheckIntervalSeconds: z.coerce.number().int().min(60),
  ModelRemovalMaxRemovedPerChannel: z.coerce.number().int().min(1),
  ModelRemovalConsecutiveThreshold: z.coerce.number().int().min(1),
  ModelRemovalCapAction: z.enum(['alert_only', 'disable_channel']),
  ModelMissingRemovalCapEnabled: z.boolean(),
  ModelRemovalNotifyEnabled: z.boolean(),
})

type RemovalFormValues = z.output<typeof removalSchema>
type RemovalFormInput = z.input<typeof removalSchema>

/** 本页字段全是扁平 option key，无嵌套 */
export type RemovalOptions = {
  ModelMissingRemovalEnabled: boolean
  ModelMissingKeywords: string
  ModelMissingRemovalCooldownSeconds: number
  ModelMissingRecheckIntervalSeconds: number
  ModelRateLimitRemovalEnabled: boolean
  ModelRateLimitRecheckIntervalSeconds: number
  ModelForbiddenRemovalEnabled: boolean
  ModelForbiddenStatusCodes: string
  ModelForbiddenKeywords: string
  ModelForbiddenRecheckIntervalSeconds: number
  ModelRemovalMaxRemovedPerChannel: number
  ModelRemovalConsecutiveThreshold: number
  ModelRemovalCapAction: string
  ModelMissingRemovalCapEnabled: boolean
  ModelRemovalNotifyEnabled: boolean
}

export type RemovalSectionProps = {
  defaultValues: RemovalOptions
  /** 停渠道关键词，只用来提示和权限关键词的重叠，不在本页保存 */
  disableKeywords: string
}

const CAP_ACTIONS = ['alert_only', 'disable_channel'] as const
type CapAction = (typeof CAP_ACTIONS)[number]

/** 后端只接受白名单取值，非法值会被 /api/option/ 拒掉；这里先归一化到默认档 */
function normalizeCapAction(value: string): CapAction {
  return (CAP_ACTIONS as readonly string[]).includes(value)
    ? (value as CapAction)
    : 'alert_only'
}

function flatten(values: RemovalFormValues): RemovalOptions {
  return {
    ModelMissingRemovalEnabled: values.ModelMissingRemovalEnabled,
    ModelMissingKeywords: values.ModelMissingKeywords,
    ModelMissingRemovalCooldownSeconds:
      values.ModelMissingRemovalCooldownSeconds,
    ModelMissingRecheckIntervalSeconds:
      values.ModelMissingRecheckIntervalSeconds,
    ModelRateLimitRemovalEnabled: values.ModelRateLimitRemovalEnabled,
    ModelRateLimitRecheckIntervalSeconds:
      values.ModelRateLimitRecheckIntervalSeconds,
    ModelForbiddenRemovalEnabled: values.ModelForbiddenRemovalEnabled,
    ModelForbiddenStatusCodes: values.ModelForbiddenStatusCodes,
    ModelForbiddenKeywords: values.ModelForbiddenKeywords,
    ModelForbiddenRecheckIntervalSeconds:
      values.ModelForbiddenRecheckIntervalSeconds,
    ModelRemovalMaxRemovedPerChannel: values.ModelRemovalMaxRemovedPerChannel,
    ModelRemovalConsecutiveThreshold: values.ModelRemovalConsecutiveThreshold,
    ModelRemovalCapAction: values.ModelRemovalCapAction,
    ModelMissingRemovalCapEnabled: values.ModelMissingRemovalCapEnabled,
    ModelRemovalNotifyEnabled: values.ModelRemovalNotifyEnabled,
  }
}

export function ModelRemovalSection({
  defaultValues,
  disableKeywords,
}: RemovalSectionProps) {
  const { t } = useTranslation()
  const { save, isSaving } = useOptionBatchSave()
  // baseline 与表单都用归一化后的枚举值：否则服务端存了个陌生取值时，差异计算会
  // 每次保存都白发一遍这个键。
  const baselineRef = useRef<RemovalOptions>({
    ...defaultValues,
    ModelRemovalCapAction: normalizeCapAction(defaultValues.ModelRemovalCapAction),
  })

  const formDefaults = useMemo<RemovalFormInput>(
    () => ({
      ...defaultValues,
      ModelRemovalCapAction: normalizeCapAction(
        defaultValues.ModelRemovalCapAction
      ),
    }),
    [defaultValues]
  )

  const form = useForm<RemovalFormInput, unknown, RemovalFormValues>({
    resolver: zodResolver(removalSchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const forbiddenKeywords = form.watch('ModelForbiddenKeywords')
  const forbiddenRemovalEnabled = form.watch('ModelForbiddenRemovalEnabled')
  const overlappingKeywords = useMemo(
    () => findOverlappingKeywords(forbiddenKeywords ?? '', disableKeywords ?? ''),
    [forbiddenKeywords, disableKeywords]
  )

  const onSubmit = async (values: RemovalFormValues) => {
    const { saved } = await save(flatten(values), baselineRef.current)
    baselineRef.current = { ...baselineRef.current, ...saved }
  }

  return (
    <SettingsSection
      title={t('Model-Level Removal')}
      description={t(
        'Take one model out of a channel instead of stopping the whole channel'
      )}
    >
      <Form {...form}>
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Model missing auto-removal')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'When upstream reports that a model does not exist, sync the upstream model list and remove only that model from the channel, instead of disabling the whole channel. Removed models are rechecked later and auto-restored if they come back.'
              )}
            </p>
          </div>

          <FormField
            control={form.control}
            name='ModelMissingRemovalEnabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5 pe-4'>
                  <FormLabel className='text-base'>
                    {t('Enable model missing auto-removal')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'When off, model-missing errors fall back to the regular auto-disable rules.'
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
            name='ModelMissingKeywords'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Model missing keywords')}</FormLabel>
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
                    'If an upstream error contains any of these keywords (case insensitive), it is treated as a model-missing error. Removal still requires upstream model-list verification.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='ModelMissingRemovalCooldownSeconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t('Removal trigger cooldown (seconds)')}
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
                      name={field.name}
                      onBlur={field.onBlur}
                      ref={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Same channel+model triggers upstream verification only once within this window (default 60)'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='ModelMissingRecheckIntervalSeconds'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Recheck interval (seconds)')}</FormLabel>
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
                      name={field.name}
                      onBlur={field.onBlur}
                      ref={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Delay before rechecking upstream after removal; restored models are auto-added back (default 600)'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Rate limit (429) auto-removal')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Reuses the removal/recheck machinery above, but skips the existence check: when upstream returns 429 the model is still listed, so the model is removed right away and added back at the next recheck. Rate limiting therefore removes the model for one recheck cycle and restores it automatically.'
              )}
            </p>
          </div>

          <FormField
            control={form.control}
            name='ModelRateLimitRemovalEnabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5 pe-4'>
                  <FormLabel className='text-base'>
                    {t('Enable rate limit (429) auto-removal')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Off by default. Removal still requires the upstream model list to be reachable — otherwise the model could never be added back. At most 5 models are removed per channel this way, so a channel-wide rate limit cannot empty it.'
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
            name='ModelRateLimitRecheckIntervalSeconds'
            render={({ field }) => (
              <FormItem className='md:max-w-sm'>
                <FormLabel>
                  {t('Rate limit recheck interval (seconds)')}
                </FormLabel>
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
                    name={field.name}
                    onBlur={field.onBlur}
                    ref={field.ref}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'How long a rate-limited model stays removed before being added back (default 180). This is the cooldown window: shorter recovers capacity sooner, longer reduces churn.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Permission and unsupported-model removal')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'For upstreams that refuse one specific model while the rest of the channel works: an Azure deployment without that model enabled, or a Bedrock region where it was never turned on. Without this, those errors hit the failure keywords and stop the entire channel. The status codes and keywords for this verdict are set just below.'
              )}
            </p>
          </div>

          <FormField
            control={form.control}
            name='ModelForbiddenRemovalEnabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5 pe-4'>
                  <FormLabel className='text-base'>
                    {t('Enable permission and unsupported-model removal')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Off by default, so these errors keep stopping the whole channel exactly as before. Like the 429 path, this skips the existence check — a refused model is still listed upstream — but it still requires the upstream model list to be reachable, otherwise the model could never be added back.'
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

          <StatusCodeField
            control={form.control}
            name='ModelForbiddenStatusCodes'
            label={t('Permission and unsupported-model status codes')}
            description={t(
              'A response only counts as a per-model refusal when its status code is listed here and its text matches a keyword below (default 400, 403).'
            )}
          />

          <FormField
            control={form.control}
            name='ModelForbiddenKeywords'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Permission and unsupported-model keywords')}
                </FormLabel>
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
                    'Matched case insensitively against the upstream error text. Unlike model-missing, there is no upstream model-list verification here: a refused model is still listed upstream, so checking would never let the removal through.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          {overlappingKeywords.length > 0 && forbiddenRemovalEnabled && (
            <Alert>
              <TriangleAlert />
              <AlertTitle>
                {t(
                  'These keywords now remove the model instead of stopping the channel'
                )}
              </AlertTitle>
              <AlertDescription>
                {t(
                  'They appear in both keyword lists: {{keywords}}. A request error is offered to model-level removal first, and stopping the channel only happens if the removal cannot go through — so the lower-severity verdict wins. Leave both lists as they are if that is what you want; remove the entry from one list to make the choice explicit.',
                  { keywords: overlappingKeywords.join(', ') }
                )}
              </AlertDescription>
            </Alert>
          )}

          <FormField
            control={form.control}
            name='ModelForbiddenRecheckIntervalSeconds'
            render={({ field }) => (
              <FormItem className='md:max-w-sm'>
                <FormLabel>
                  {t('Permission recheck interval (seconds)')}
                </FormLabel>
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
                    name={field.name}
                    onBlur={field.onBlur}
                    ref={field.ref}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'How long a refused model stays removed before being added back (default 1800). Permissions change when someone edits them upstream, so this is much longer than the rate-limit window.'
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
              {t('Removal count and escalation')}
            </h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Rate limits and refusals are removed on inference, not on evidence: when the whole channel is limited or the key has no access at all, every model reports the same error and would be taken out one by one. Hitting the cap is the signal that the problem is the channel, not the model.'
              )}
            </p>
          </div>

          <div className='grid gap-6 md:grid-cols-2'>
            <FormField
              control={form.control}
              name='ModelRemovalConsecutiveThreshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t('Consecutive failures before removal')}
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
                      name={field.name}
                      onBlur={field.onBlur}
                      ref={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Only the 429 and permission paths: remove a model after this many consecutive failures for the same channel+model (default 10). Any success in between resets the count, so a transient throttle or one account in a pooling upstream being refused does not remove the model. Set to 1 to remove on the first hit. The model-missing path is exempt — it verifies against the upstream model list.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='ModelRemovalMaxRemovedPerChannel'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Removal cap per channel')}</FormLabel>
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
                      name={field.name}
                      onBlur={field.onBlur}
                      ref={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Stop removing models from a channel once this many are already out (default 5). Counts every model currently awaiting recheck, whatever took it out.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='ModelRemovalCapAction'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Action when the cap is reached')}</FormLabel>
                  <Select value={field.value} onValueChange={field.onChange}>
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      <SelectItem value='alert_only'>
                        {t('Alert only (default)')}
                      </SelectItem>
                      <SelectItem value='disable_channel'>
                        {t('Stop the whole channel')}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {t(
                      'Alert only keeps the pre-existing behaviour: log a line and hand the error back to the regular auto-disable rules. Stopping the channel is an escalation on the cap itself, and still respects the global auto-disable switch and the channel auto-ban flag.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <FormField
            control={form.control}
            name='ModelMissingRemovalCapEnabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5 pe-4'>
                  <FormLabel className='text-base'>
                    {t('Count model-missing removals toward the cap')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Off by default, matching the pre-existing behaviour. Model-missing removals are backed by upstream model-list verification, so hitting the cap means the upstream really did retire that many models — stopping the channel would also cut off the models that still work. Turn this on only if a channel losing many models at once should be taken out entirely.'
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

          <Separator />

          <div className='space-y-1'>
            <h4 className='text-sm font-semibold'>{t('Removal visibility')}</h4>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Removing a model is quieter than stopping a channel, and that is the cost of the lower severity: a stopped channel shows up red in the channel list and sends a notification, while a channel that lost one model still reads as enabled. The channel list carries a badge with the number of removed models either way; this switch adds the notification.'
              )}
            </p>
          </div>

          <FormField
            control={form.control}
            name='ModelRemovalNotifyEnabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5 pe-4'>
                  <FormLabel className='text-base'>
                    {t('Notify on model removal')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Off by default, matching the pre-existing behaviour: before this, only stopping a channel ever sent a notification and removals sent nothing. Each notification names the channel, the model, why it was taken out and when it will be rechecked. On a large fleet removals are far more frequent than stops, so turn this on deliberately.'
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
          </AdvancedFields>

          <Separator />

          <Button type='submit' disabled={isSaving}>
            {isSaving ? t('Saving...') : t('Save removal settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

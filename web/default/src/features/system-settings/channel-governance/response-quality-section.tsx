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
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { SettingsSection } from '../components/settings-section'
import { useOptionBatchSave } from '../hooks/use-option-batch-save'
import { useResetForm } from '../hooks/use-reset-form'

const qualitySchema = z.object({
  response_quality_setting: z.object({
    block_apology_enabled: z.boolean(),
    apology_status_code: z.coerce.number().int().min(100).max(599),
    apology_message: z.string().max(500),
    apology_keywords: z.string().max(20000),
    apply_all_channels: z.boolean(),
    block_low_token_enabled: z.boolean(),
    low_token_threshold: z.coerce.number().int().min(1).max(100000),
    low_token_status_code: z.coerce.number().int().min(100).max(599),
    low_token_message: z.string().max(500),
    retry_on_block: z.boolean(),
  }),
})

type QualityFormValues = z.output<typeof qualitySchema>
type QualityFormInput = z.input<typeof qualitySchema>

export type ResponseQualityOptions = {
  'response_quality_setting.block_apology_enabled': boolean
  'response_quality_setting.apology_status_code': number
  'response_quality_setting.apology_message': string
  'response_quality_setting.apology_keywords': string
  'response_quality_setting.apply_all_channels': boolean
  'response_quality_setting.block_low_token_enabled': boolean
  'response_quality_setting.low_token_threshold': number
  'response_quality_setting.low_token_status_code': number
  'response_quality_setting.low_token_message': string
  'response_quality_setting.retry_on_block': boolean
}

export type ResponseQualitySectionProps = {
  defaultValues: ResponseQualityOptions
}

function normalizeLineEndings(value: string) {
  return value.replace(/\r\n/g, '\n')
}

function qualityStatus(code: number | undefined): number {
  return typeof code === 'number' && code >= 100 && code <= 599 ? code : 503
}

function flatten(values: QualityFormValues): ResponseQualityOptions {
  const quality = values.response_quality_setting
  return {
    'response_quality_setting.block_apology_enabled':
      quality.block_apology_enabled,
    'response_quality_setting.apology_status_code': quality.apology_status_code,
    'response_quality_setting.apology_message': quality.apology_message,
    'response_quality_setting.apology_keywords': normalizeLineEndings(
      quality.apology_keywords
    ),
    'response_quality_setting.apply_all_channels': quality.apply_all_channels,
    'response_quality_setting.block_low_token_enabled':
      quality.block_low_token_enabled,
    'response_quality_setting.low_token_threshold': quality.low_token_threshold,
    'response_quality_setting.low_token_status_code':
      quality.low_token_status_code,
    'response_quality_setting.low_token_message': quality.low_token_message,
    'response_quality_setting.retry_on_block': quality.retry_on_block,
  }
}

function StatusCodeInput({
  value,
  onChange,
  placeholder,
}: {
  value: number | undefined
  onChange: (value: number) => void
  placeholder: string
}) {
  return (
    <Input
      type='number'
      min={100}
      max={599}
      placeholder={placeholder}
      value={
        typeof value === 'number' && Number.isFinite(value) ? value : ''
      }
      onChange={(event) => {
        const next = event.target.valueAsNumber
        if (Number.isFinite(next)) onChange(next)
      }}
    />
  )
}

export function ResponseQualitySection({
  defaultValues,
}: ResponseQualitySectionProps) {
  const { t } = useTranslation()
  const { save, isSaving } = useOptionBatchSave()
  const baselineRef = useRef<ResponseQualityOptions>({
    ...defaultValues,
    'response_quality_setting.apology_keywords': normalizeLineEndings(
      defaultValues['response_quality_setting.apology_keywords'] ?? ''
    ),
  })

  const formDefaults = useMemo<QualityFormInput>(
    () => ({
      response_quality_setting: {
        block_apology_enabled:
          defaultValues['response_quality_setting.block_apology_enabled'],
        apology_status_code: qualityStatus(
          defaultValues['response_quality_setting.apology_status_code']
        ),
        apology_message:
          defaultValues['response_quality_setting.apology_message'] ||
          'upstream apology reply blocked',
        apology_keywords: normalizeLineEndings(
          defaultValues['response_quality_setting.apology_keywords'] ?? ''
        ),
        apply_all_channels:
          defaultValues['response_quality_setting.apply_all_channels'] !== false,
        block_low_token_enabled:
          defaultValues['response_quality_setting.block_low_token_enabled'],
        low_token_threshold:
          defaultValues['response_quality_setting.low_token_threshold'] || 300,
        low_token_status_code: qualityStatus(
          defaultValues['response_quality_setting.low_token_status_code']
        ),
        low_token_message:
          defaultValues['response_quality_setting.low_token_message'] ||
          'upstream completion tokens {tokens} below {threshold}',
        retry_on_block:
          defaultValues['response_quality_setting.retry_on_block'] === true,
      },
    }),
    [defaultValues]
  )

  const form = useForm<QualityFormInput, unknown, QualityFormValues>({
    resolver: zodResolver(qualitySchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)

  const onSubmit = async (values: QualityFormValues) => {
    const { saved } = await save(flatten(values), baselineRef.current)
    baselineRef.current = { ...baselineRef.current, ...saved }
  }

  return (
    <SettingsSection
      title={t('Response Quality')}
      description={t(
        'Intercept apology or very short replies and return the HTTP status and message configured here. With apply-all on, the global switch is enough; turn it off to require the matching channel switch as well.'
      )}
    >
      <Form {...form}>
        <form
          autoComplete='off'
          className='space-y-6'
          onSubmit={form.handleSubmit(onSubmit)}
        >
          <Alert>
            <Info />
            <AlertTitle>{t('Global master switches')}</AlertTitle>
            <AlertDescription>
              {t(
                'A hit discards the upstream reply and returns the status and message below to the client immediately. Channel status-code mapping does not rewrite this response. The current attempt is not billed unless the channel has “0 output no refund” on — then the user still pays the upstream usage for apology and low-token intercepts. Streaming replies are held until the full answer finishes, otherwise the configured error cannot replace it. Apply-all is on by default so you do not have to enable each channel. A user message of hi, and this site’s scheduled channel tests, are never filtered.'
              )}
            </AlertDescription>
          </Alert>

          <FormField
            control={form.control}
            name='response_quality_setting.apply_all_channels'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5'>
                  <FormLabel>{t('Apply to all channels')}</FormLabel>
                  <FormDescription>
                    {t(
                      'When on, the global switches apply to every channel. Turn this off if you only want channels that also have the matching switch enabled.'
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
            name='response_quality_setting.retry_on_block'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between rounded-lg border p-4'>
                <div className='space-y-0.5'>
                  <FormLabel>{t('Retry after intercept')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Off by default. When on, apology and low-token intercepts follow the retry count and status-code list, and may switch channels. A channel with 0-output-no-refund is charged only when the request still ends on an intercept.'
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

          <div className='space-y-4 rounded-lg border p-4'>
            <FormField
              control={form.control}
              name='response_quality_setting.block_apology_enabled'
              render={({ field }) => (
                <FormItem className='flex items-center justify-between'>
                  <div className='space-y-0.5'>
                    <FormLabel>{t('Block apology replies')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Intercept replies whose opening window matches any refusal keyword below.'
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

            <div className='grid gap-4 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='response_quality_setting.apology_status_code'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Apology HTTP status')}</FormLabel>
                    <FormControl>
                      <StatusCodeInput
                        value={
                          typeof field.value === 'number'
                            ? field.value
                            : undefined
                        }
                        onChange={field.onChange}
                        placeholder='503'
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Returned to the client as-is. Channel status-code mapping is ignored.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <FormField
              control={form.control}
              name='response_quality_setting.apology_message'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Apology error message')}</FormLabel>
                  <FormControl>
                    <Textarea
                      rows={3}
                      placeholder='upstream apology reply blocked'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Shown in the client error body. Leave empty to use the default English copy.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='response_quality_setting.apology_keywords'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Refusal keywords')}</FormLabel>
                  <FormControl>
                    <Textarea
                      rows={12}
                      placeholder={t('one keyword per line')}
                      {...field}
                      onChange={(event) => field.onChange(event.target.value)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'One phrase per line. Matching is case-insensitive and only looks at the visible reply up to the low-token threshold. Leave empty to use the built-in list.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='space-y-4 rounded-lg border p-4'>
            <FormField
              control={form.control}
              name='response_quality_setting.block_low_token_enabled'
              render={({ field }) => (
                <FormItem className='flex items-center justify-between'>
                  <div className='space-y-0.5'>
                    <FormLabel>{t('Block low-token replies')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Intercept completions shorter than the threshold below. Skipped when the client asked for fewer tokens than the threshold, or when the reply is a tool call / image / audio.'
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

            <div className='grid gap-4 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='response_quality_setting.low_token_threshold'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Low-token threshold')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={100000}
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
                        'Completion tokens below this number are intercepted (default 300). Streaming replies start forwarding once visible text reaches this number, including when only apology blocking is on.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='response_quality_setting.low_token_status_code'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Low-token HTTP status')}</FormLabel>
                    <FormControl>
                      <StatusCodeInput
                        value={
                          typeof field.value === 'number'
                            ? field.value
                            : undefined
                        }
                        onChange={field.onChange}
                        placeholder='503'
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Returned to the client as-is. Channel status-code mapping is ignored.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <FormField
              control={form.control}
              name='response_quality_setting.low_token_message'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Low-token error message')}</FormLabel>
                  <FormControl>
                    <Textarea
                      rows={3}
                      placeholder='upstream completion tokens {tokens} below {threshold}'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Shown in the client error body. {tokens} and {threshold} are replaced at runtime. Leave empty to use the default English copy.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Button type='submit' disabled={isSaving}>
            {isSaving ? t('Saving...') : t('Save response quality settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

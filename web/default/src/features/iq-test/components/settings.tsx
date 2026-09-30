import { z } from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Save } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { getIQSetting, iqKeys, saveIQSetting, type IQSetting } from '../api'
import { iqErrorMessage } from '../lib'
import { IQQueryState } from './shared'

const schema = z.object({
  enabled: z.boolean(),
  enforcement_mode: z.enum(['enforce', 'report_only']),
  interval_minutes: z.number().int().min(5).max(10080),
  concurrency: z.number().int().min(1).max(16),
  disable_below_baseline: z.boolean(),
  priority_step: z.number().int().min(1).max(100),
  questions_per_round: z.number().int().min(1).max(15),
  per_question_timeout_seconds: z.number().int().min(5).max(120),
  notify_on_action: z.boolean(),
  retention_days: z.number().int().min(1).max(3650),
  version: z.number().int(),
})

function SettingsForm(props: { setting: IQSetting; editable: boolean }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const form = useForm<IQSetting>({
    resolver: zodResolver(schema),
    defaultValues: props.setting,
  })
  const save = useMutation({
    mutationFn: saveIQSetting,
    onSuccess: (setting) => {
      form.reset(setting)
      client.setQueryData(iqKeys.setting, setting)
      toast.success(t('Saved successfully'))
    },
  })
  const numeric = [
    {
      key: 'interval_minutes',
      label: t('IQ Interval minutes'),
      min: 5,
      max: 10080,
    },
    { key: 'concurrency', label: t('IQ Concurrency'), min: 1, max: 16 },
    {
      key: 'questions_per_round',
      label: t('IQ Questions per round'),
      min: 1,
      max: 15,
    },
    {
      key: 'per_question_timeout_seconds',
      label: t('IQ Question timeout seconds'),
      min: 5,
      max: 120,
    },
    { key: 'priority_step', label: t('IQ Priority step'), min: 1, max: 100 },
    { key: 'retention_days', label: t('IQ Retention days'), min: 1, max: 3650 },
  ] as const
  const switches = [
    { key: 'enabled', label: t('IQ Enabled') },
    { key: 'disable_below_baseline', label: t('IQ Disable below baseline') },
    { key: 'notify_on_action', label: t('IQ Notify on action') },
  ] as const
  return (
    <form
      className='max-w-3xl space-y-6'
      onSubmit={form.handleSubmit((values) => save.mutate(values))}
    >
      <fieldset
        disabled={!props.editable || save.isPending}
        className='space-y-6'
      >
        <div className='space-y-2'>
          <Label htmlFor='iq-enforcement-mode'>
            {t('IQ Enforcement mode')}
          </Label>
          <Select
            value={form.watch('enforcement_mode')}
            onValueChange={(value) =>
              form.setValue(
                'enforcement_mode',
                value as IQSetting['enforcement_mode'],
                {
                  shouldDirty: true,
                }
              )
            }
          >
            <SelectTrigger id='iq-enforcement-mode' className='max-w-sm'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='enforce'>{t('IQ Enforce routing')}</SelectItem>
              <SelectItem value='report_only'>{t('IQ Report only')}</SelectItem>
            </SelectContent>
          </Select>
          <p className='text-muted-foreground text-xs'>
            {form.watch('enforcement_mode') === 'report_only'
              ? t('IQ Report only hint')
              : t('IQ Enforce routing hint')}
          </p>
        </div>
        <div className='divide-y'>
          {switches.map((field) => (
            <div
              key={field.key}
              className='flex items-center justify-between gap-4 py-4'
            >
              <Label htmlFor={`iq-${field.key}`}>{field.label}</Label>
              <Switch
                id={`iq-${field.key}`}
                checked={form.watch(field.key)}
                onCheckedChange={(value) =>
                  form.setValue(field.key, value, { shouldDirty: true })
                }
              />
            </div>
          ))}
        </div>
        <div className='grid gap-x-6 gap-y-5 sm:grid-cols-2'>
          {numeric.map((field) => (
            <div key={field.key} className='space-y-2'>
              <Label htmlFor={`iq-${field.key}`}>{field.label}</Label>
              <Input
                id={`iq-${field.key}`}
                type='number'
                min={field.min}
                max={field.max}
                step={1}
                {...form.register(field.key, { valueAsNumber: true })}
              />
              <p className='text-destructive text-xs' role='alert'>
                {form.formState.errors[field.key]
                  ? t('IQ Value range', { min: field.min, max: field.max })
                  : null}
              </p>
            </div>
          ))}
        </div>
        {save.error && (
          <p role='alert' className='text-destructive text-sm break-words'>
            {iqErrorMessage(save.error)}
          </p>
        )}
        {props.editable && (
          <Button
            type='submit'
            disabled={!form.formState.isDirty || save.isPending}
          >
            <Save className='size-4' />
            {t('Save')}
          </Button>
        )}
      </fieldset>
    </form>
  )
}

export function IQSettings(props: { editable: boolean }) {
  const query = useQuery({ queryKey: iqKeys.setting, queryFn: getIQSetting })
  if (!query.data || query.isError)
    return (
      <IQQueryState
        loading={query.isPending}
        error={query.error}
        empty={false}
        retry={() => void query.refetch()}
      />
    )
  return (
    <SettingsForm
      key={query.data.version}
      setting={query.data}
      editable={props.editable}
    />
  )
}

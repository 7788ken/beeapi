import { useEffect } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import type { UserRpmRule } from './api'

// 注意：input 用 number（不走 coerce），onChange 时手动转换，避免 zod v4 + RHF 的
// resolver input/output 类型推断不一致问题。
const schema = z
  .object({
    user_id: z.number().int().positive(),
    base_rpm: z.number().int().min(1),
    jitter_pct: z.number().min(0).max(1),
    min_delay_ms: z.number().int().min(0),
    max_delay_ms: z.number().int().min(0),
    enabled: z.boolean(),
  })
  .refine((v) => v.max_delay_ms >= v.min_delay_ms, {
    message: 'max_delay_ms_lt_min',
    path: ['max_delay_ms'],
  })

export type UserRpmFormValues = z.infer<typeof schema>

type Props = {
  initial?: UserRpmRule | null
  submitting?: boolean
  // editing 已存在的规则时，user_id 锁定
  lockUserId?: boolean
  onSubmit: (values: UserRpmFormValues) => void
  onCancel: () => void
}

function defaultsFromInitial(initial?: UserRpmRule | null): UserRpmFormValues {
  return {
    user_id: initial?.user_id ?? 1,
    base_rpm: initial?.base_rpm ?? 5,
    jitter_pct: initial?.jitter_pct ?? 0.2,
    min_delay_ms: initial?.min_delay_ms ?? 50,
    max_delay_ms: initial?.max_delay_ms ?? 500,
    enabled: initial?.enabled ?? true,
  }
}

export function UserRpmForm({
  initial,
  submitting,
  lockUserId,
  onSubmit,
  onCancel,
}: Props) {
  const { t } = useTranslation()

  const form = useForm<UserRpmFormValues>({
    resolver: zodResolver(schema),
    defaultValues: defaultsFromInitial(initial),
  })

  useEffect(() => {
    form.reset(defaultsFromInitial(initial))
  }, [initial, form])

  // 把 input string → number，空串保留为 NaN 让 zod 报错
  const numberHandler =
    (onChange: (v: number) => void) =>
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const raw = e.target.value
      if (raw === '') {
        onChange(NaN)
        return
      }
      const n = Number(raw)
      onChange(Number.isNaN(n) ? NaN : n)
    }

  return (
    <Form {...form}>
      <form
        onSubmit={form.handleSubmit(onSubmit)}
        className='space-y-3 rounded-lg border bg-card p-3'
      >
        <div className='grid grid-cols-2 gap-3 md:grid-cols-3'>
          <FormField
            control={form.control}
            name='user_id'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('User ID')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    step={1}
                    disabled={lockUserId}
                    value={Number.isFinite(field.value) ? field.value : ''}
                    onChange={numberHandler(field.onChange)}
                    onBlur={field.onBlur}
                    ref={field.ref}
                    name={field.name}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='base_rpm'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Base RPM')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    step={1}
                    value={Number.isFinite(field.value) ? field.value : ''}
                    onChange={numberHandler(field.onChange)}
                    onBlur={field.onBlur}
                    ref={field.ref}
                    name={field.name}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='jitter_pct'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Jitter %')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    max={1}
                    step={0.05}
                    value={Number.isFinite(field.value) ? field.value : ''}
                    onChange={numberHandler(field.onChange)}
                    onBlur={field.onBlur}
                    ref={field.ref}
                    name={field.name}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='min_delay_ms'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Min delay (ms)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={10}
                    value={Number.isFinite(field.value) ? field.value : ''}
                    onChange={numberHandler(field.onChange)}
                    onBlur={field.onBlur}
                    ref={field.ref}
                    name={field.name}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='max_delay_ms'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Max delay (ms)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    step={10}
                    value={Number.isFinite(field.value) ? field.value : ''}
                    onChange={numberHandler(field.onChange)}
                    onBlur={field.onBlur}
                    ref={field.ref}
                    name={field.name}
                  />
                </FormControl>
                <FormMessage>
                  {form.formState.errors.max_delay_ms?.message ===
                    'max_delay_ms_lt_min' && t('Max delay must be >= min delay')}
                </FormMessage>
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <FormItem className='flex flex-col gap-2'>
                <FormLabel>{t('Enabled')}</FormLabel>
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
        </div>
        <div className='flex justify-end gap-2 pt-1'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={onCancel}
            disabled={submitting}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' size='sm' disabled={submitting}>
            {t('Save')}
          </Button>
        </div>
      </form>
    </Form>
  )
}

// 对账上游账单数据源：balance 面板地址 + 只读服务令牌。
// 两项都配置后，dashboard 对账 tab 才会显示"上游账单"卡片与账号明细；清空即整体关闭。
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
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
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { removeTrailingSlash } from './utils'

const createReconcileBillSchema = (t: (key: string) => string) =>
  z.object({
    ReconcileBalancePanelBaseURL: z.string().refine((value) => {
      const trimmed = value.trim()
      if (!trimmed) return true
      return /^https?:\/\//.test(trimmed)
    }, t('Provide a valid URL starting with http:// or https://')),
    ReconcileBalancePanelToken: z.string(),
  })

type ReconcileBillFormValues = z.infer<
  ReturnType<typeof createReconcileBillSchema>
>

type ReconcileBillSettingsSectionProps = {
  defaultValues: ReconcileBillFormValues
}

export function ReconcileBillSettingsSection({
  defaultValues,
}: ReconcileBillSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const schema = createReconcileBillSchema(t)

  const form = useForm<ReconcileBillFormValues>({
    resolver: zodResolver(schema),
    defaultValues,
  })

  useResetForm(form, defaultValues)

  const onSubmit = async (values: ReconcileBillFormValues) => {
    const sanitizedUrl = removeTrailingSlash(values.ReconcileBalancePanelBaseURL)
    const sanitizedToken = values.ReconcileBalancePanelToken.trim()
    const initialUrl = removeTrailingSlash(
      defaultValues.ReconcileBalancePanelBaseURL
    )

    const updates: Array<{ key: string; value: string }> = []
    if (sanitizedUrl !== initialUrl) {
      updates.push({ key: 'ReconcileBalancePanelBaseURL', value: sanitizedUrl })
    }
    // 令牌不回显（Token 后缀在 /api/option/ 被脱敏）：填了才更新；清空地址时同步清令牌。
    if (sanitizedToken !== '' || sanitizedUrl === '') {
      updates.push({ key: 'ReconcileBalancePanelToken', value: sanitizedToken })
    }

    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }
    form.setValue('ReconcileBalancePanelToken', '')
  }

  return (
    <SettingsSection
      title={t('Reconcile Upstream Bill')}
      description={t(
        'Pull per-account upstream spend for the dashboard reconcile tab from the balance monitor panel'
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
            name='ReconcileBalancePanelBaseURL'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Balance panel URL')}</FormLabel>
                <FormControl>
                  <Input
                    type='url'
                    inputMode='url'
                    placeholder='https://balance.example.com'
                    autoComplete='off'
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Leave empty to disable the upstream bill column entirely.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='ReconcileBalancePanelToken'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Balance panel service token')}</FormLabel>
                <FormControl>
                  <Input
                    type='password'
                    placeholder={t('Enter new token to update')}
                    autoComplete='new-password'
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Read-only service account token (recon.view only). Stored server-side and never returned to the browser. Leave blank to keep the existing token.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <Button type='submit' disabled={updateOption.isPending}>
            {updateOption.isPending
              ? t('Saving...')
              : t('Save upstream bill settings')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

import { useEffect } from 'react'
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
import { Textarea } from '@/components/ui/textarea'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const OPTION_KEY = 'console_setting.support_widget_code'
// 与后端 SupportWidgetCodeMaxLen 一致
const MAX_LENGTH = 10000

type SupportWidgetSectionProps = {
  defaultValue: string
}

export function SupportWidgetSection({
  defaultValue,
}: SupportWidgetSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const schema = z.object({
    code: z.string().trim().max(MAX_LENGTH, t('Maximum 10000 characters')),
  })
  type FormValues = z.infer<typeof schema>

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { code: defaultValue },
  })

  useEffect(() => {
    form.reset({ code: defaultValue })
  }, [defaultValue, form])

  const onSubmit = async (values: FormValues) => {
    if (values.code === defaultValue) return
    await updateOption.mutateAsync({ key: OPTION_KEY, value: values.code })
  }

  return (
    <SettingsSection
      title={t('Live Chat')}
      description={t('Show a live chat entry on every page')}
    >
      <Form {...form}>
        <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-6'>
          <FormField
            control={form.control}
            name='code'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Embed Code')}</FormLabel>
                <FormControl>
                  <Textarea
                    placeholder='<script src="https://desk.9manager.com/widget.js" data-site="your-site-id" async></script>'
                    rows={5}
                    spellCheck={false}
                    autoComplete='off'
                    className='font-mono text-xs md:text-xs'
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    "Paste the embed code from your live chat service (HTML and scripts are supported). It is added to every page as-is, so only use code you trust. A 9desk embed picks up this site's colors and follows dark mode automatically; theme attributes written in the code take precedence. Leave empty to hide the live chat entry. Changes apply after the page is refreshed."
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <Button type='submit' disabled={updateOption.isPending}>
            {updateOption.isPending ? t('Saving...') : t('Save Changes')}
          </Button>
        </form>
      </Form>
    </SettingsSection>
  )
}

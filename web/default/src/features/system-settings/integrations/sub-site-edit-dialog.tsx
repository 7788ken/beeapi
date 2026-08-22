import { useEffect } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
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
import type { SubSite, SubSiteUpsertRequest } from '../types'

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSave: (payload: SubSiteUpsertRequest) => void
  saving?: boolean
  editing?: SubSite | null
}

const schemaFactory = (t: (k: string) => string) =>
  z.object({
    name: z
      .string()
      .min(1, t('Name is required'))
      .max(64, t('Name too long')),
    base_url: z
      .string()
      .min(1, t('Base URL is required'))
      .refine(
        (v) => /^https?:\/\//i.test(v),
        t('Base URL must start with http(s)://')
      ),
    upstream_user_id: z
      .union([z.string(), z.number()])
      .transform((v) => (typeof v === 'string' ? parseInt(v, 10) || 0 : v))
      .pipe(z.number().int().min(1, t('Upstream user id must be >= 1'))),
    token: z.string().optional(),
    enabled: z.boolean(),
    note: z.string().max(255).optional(),
  })

export function SubSiteEditDialog({
  open,
  onOpenChange,
  onSave,
  saving,
  editing,
}: Props) {
  const { t } = useTranslation()
  const isEdit = !!editing
  const schema = schemaFactory(t)
  type FormValues = z.infer<typeof schema>

  const form = useForm<FormValues>({
    resolver: zodResolver(schema) as never,
    defaultValues: {
      name: '',
      base_url: '',
      upstream_user_id: 1,
      token: '',
      enabled: true,
      note: '',
    },
  })

  useEffect(() => {
    if (open) {
      form.reset({
        name: editing?.name ?? '',
        base_url: editing?.base_url ?? '',
        upstream_user_id: editing?.upstream_user_id ?? 1,
        token: '',
        enabled: editing?.enabled ?? true,
        note: editing?.note ?? '',
      })
    }
  }, [open, editing, form])

  const onSubmit = (data: FormValues) => {
    const trimmedToken = (data.token ?? '').trim()
    if (!isEdit && !trimmedToken) {
      form.setError('token', { message: t('Token is required when creating') })
      return
    }
    const payload: SubSiteUpsertRequest = {
      id: editing?.id,
      name: data.name.trim(),
      base_url: data.base_url.trim().replace(/\/+$/, ''),
      upstream_user_id: data.upstream_user_id,
      token: trimmedToken === '' ? undefined : trimmedToken,
      enabled: data.enabled,
      note: (data.note ?? '').trim(),
    }
    onSave(payload)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>
            {isEdit ? t('Edit Sub-Site') : t('Add Sub-Site')}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Configure an upstream new-api site for group/pricing synchronization.'
            )}
          </DialogDescription>
        </DialogHeader>
        <Form {...form}>
          <form
            onSubmit={form.handleSubmit(onSubmit)}
            className='space-y-4'
          >
            <FormField
              control={form.control}
              name='name'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Name')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('e.g. partner-a')}
                      autoComplete='off'
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='base_url'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Base URL')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder='https://api.partner.com'
                      autoComplete='off'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Private/loopback addresses are rejected unless SubSiteAllowIntranet=true.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='upstream_user_id'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Upstream user id')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      placeholder='1'
                      autoComplete='off'
                      {...field}
                      value={field.value ?? ''}
                      onChange={(e) => field.onChange(e.target.value)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('The id of the user that owns this token on the upstream site (required by New-Api-User header).')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='token'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Access Token')}</FormLabel>
                  <FormControl>
                    <Input
                      type='password'
                      placeholder={
                        isEdit
                          ? t('Leave blank to keep current')
                          : t('Required (root-level token)')
                      }
                      autoComplete='off'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Stored encrypted (AES-GCM); never returned via API.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='note'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Note')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('Optional')}
                      autoComplete='off'
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <FormItem className='flex items-center justify-between rounded-md border p-3'>
                  <div className='space-y-0.5'>
                    <FormLabel>{t('Enabled')}</FormLabel>
                    <FormDescription>
                      {t('Disabled sites are hidden from Channels page picker.')}
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
            <DialogFooter>
              <Button
                type='button'
                variant='outline'
                onClick={() => onOpenChange(false)}
                disabled={saving}
              >
                {t('Cancel')}
              </Button>
              <Button type='submit' disabled={saving}>
                {saving ? t('Saving...') : t('Save')}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}

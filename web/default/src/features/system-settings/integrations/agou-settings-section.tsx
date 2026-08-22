import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  PayChannelsEditor,
  parsePayChannels,
  type PayChannelItem,
} from './pay-channels-editor'
import { LogoField } from './logo-field'

export interface AgouSettingsValues {
  SfpayEnabled: boolean
  SfpayBaseURL: string
  SfpayAppId: string
  SfpayAppSecret: string
  SfpayGroupCode: string
  SfpayNotifyUrl: string
  SfpayReturnUrl: string
  SfpayUnitPrice: number
  SfpayMinTopUp: number
  SfpayMaxTopUp: number
  SfpayAllowedCallbackIPs: string
  SfpayAlipayPayType: string
  SfpayWechatEnabled: boolean
  SfpayWechatPayType: string
  SfpayAllowedGroups: string
  SfpayPayChannels: string
  SfpayLogo: string
}

interface Props {
  defaultValues: AgouSettingsValues
}

export function AgouSettingsSection(props: Props) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [loading, setLoading] = useState(false)

  const form = useForm<AgouSettingsValues>({
    defaultValues: props.defaultValues,
  })
  const [payChannels, setPayChannels] = useState<PayChannelItem[]>(() =>
    parsePayChannels(props.defaultValues.SfpayPayChannels)
  )
  const [logo, setLogo] = useState(props.defaultValues.SfpayLogo || '')

  useEffect(() => {
    form.reset(props.defaultValues)
    setPayChannels(parsePayChannels(props.defaultValues.SfpayPayChannels))
    setLogo(props.defaultValues.SfpayLogo || '')
  }, [props.defaultValues, form])

  const handleSave = async () => {
    setLoading(true)
    try {
      const values = form.getValues()
      const options: { key: string; value: string }[] = [
        { key: 'SfpayEnabled', value: String(values.SfpayEnabled) },
        { key: 'SfpayBaseURL', value: values.SfpayBaseURL || '' },
        { key: 'SfpayAppId', value: values.SfpayAppId || '' },
        { key: 'SfpayGroupCode', value: values.SfpayGroupCode || '' },
        { key: 'SfpayNotifyUrl', value: values.SfpayNotifyUrl || '' },
        { key: 'SfpayReturnUrl', value: values.SfpayReturnUrl || '' },
        { key: 'SfpayUnitPrice', value: String(values.SfpayUnitPrice || 7.3) },
        { key: 'SfpayMinTopUp', value: String(values.SfpayMinTopUp || 1) },
        { key: 'SfpayMaxTopUp', value: String(values.SfpayMaxTopUp || 0) },
        {
          key: 'SfpayAllowedCallbackIPs',
          value: values.SfpayAllowedCallbackIPs || '',
        },
        {
          key: 'SfpayAlipayPayType',
          value: values.SfpayAlipayPayType || 'ZFBPAY',
        },
        { key: 'SfpayWechatEnabled', value: String(values.SfpayWechatEnabled) },
        { key: 'SfpayWechatPayType', value: values.SfpayWechatPayType || '' },
        { key: 'SfpayAllowedGroups', value: values.SfpayAllowedGroups || '' },
        { key: 'SfpayPayChannels', value: JSON.stringify(payChannels) },
        { key: 'SfpayLogo', value: logo },
      ]
      // SfpayAppSecret is sensitive: only push when non-empty so leaving it
      // blank keeps the stored secret untouched.
      if (values.SfpayAppSecret)
        options.push({ key: 'SfpayAppSecret', value: values.SfpayAppSecret })

      for (const opt of options) {
        await updateOption.mutateAsync(opt)
      }
      toast.success(t('Updated successfully'))
    } catch {
      toast.error(t('Update failed'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <SettingsSection
      title={t('Sfpay Payment Gateway')}
      description={t('Configure sfpay payment gateway integration')}
    >
      <Alert>
        <AlertDescription className='text-xs'>
          {t(
            'Obtain the App ID, App Secret, and group code from the sfpay dashboard, and configure the callback URL.'
          )}
        </AlertDescription>
      </Alert>

      <div className='flex items-center gap-2'>
        <Switch
          checked={form.watch('SfpayEnabled')}
          onCheckedChange={(v) => form.setValue('SfpayEnabled', v)}
        />
        <Label>{t('Enable Sfpay')}</Label>
      </div>

      <div className='grid gap-1.5'>
        <Label>{t('Base URL')}</Label>
        <Input
          placeholder='https://aihef.sfpay.pro'
          {...form.register('SfpayBaseURL')}
        />
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('App ID')}</Label>
          <Input {...form.register('SfpayAppId')} />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('App Secret')}</Label>
          <Input
            type='password'
            placeholder={t('Leave blank to keep the existing secret')}
            {...form.register('SfpayAppSecret')}
          />
        </div>
      </div>

      <div className='grid gap-1.5'>
        <Label>{t('Group Code')}</Label>
        <Input placeholder='RD0C01' {...form.register('SfpayGroupCode')} />
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('Callback notification URL')}</Label>
          <Input
            placeholder='https://example.com/api/sfpay/notify'
            {...form.register('SfpayNotifyUrl')}
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Payment return URL')}</Label>
          <Input
            placeholder='https://example.com/console/topup'
            {...form.register('SfpayReturnUrl')}
          />
        </div>
      </div>

      <div className='grid grid-cols-3 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('Unit price (CNY)')}</Label>
          <Input
            type='number'
            step={0.1}
            min={0}
            {...form.register('SfpayUnitPrice')}
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Minimum top-up quantity')}</Label>
          <Input type='number' min={0} {...form.register('SfpayMinTopUp')} />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Maximum top-up quantity')}</Label>
          <Input type='number' min={0} {...form.register('SfpayMaxTopUp')} />
        </div>
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>{t('Allowed callback IPs')}</Label>
          <Input
            placeholder={t('Leave empty to allow any source IP')}
            {...form.register('SfpayAllowedCallbackIPs')}
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>{t('Alipay pay type')}</Label>
          <Input
            placeholder='ZFBPAY'
            {...form.register('SfpayAlipayPayType')}
          />
        </div>
      </div>

      <Separator />

      <div className='flex items-center gap-2'>
        <Switch
          checked={form.watch('SfpayWechatEnabled')}
          onCheckedChange={(v) => form.setValue('SfpayWechatEnabled', v)}
        />
        <Label>{t('Enable WeChat Pay')}</Label>
      </div>

      <div className='grid gap-1.5'>
        <Label>{t('WeChat pay type')}</Label>
        <Input {...form.register('SfpayWechatPayType')} />
        <p className='text-muted-foreground text-xs'>
          {t(
            'You must first bind a WeChat channel group in the sfpay dashboard and fill in the real payType code.'
          )}
        </p>
      </div>

      <Separator />

      <div className='grid gap-1.5'>
        <Label>{t('Allowed groups (optional)')}</Label>
        <Input
          placeholder={t('e.g., vip;premium')}
          {...form.register('SfpayAllowedGroups')}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Leave empty to allow all groups. Separate multiple groups with a semicolon (;).'
          )}
        </p>
      </div>

      <Separator />

      <LogoField
        value={logo}
        onChange={setLogo}
        label={t('Payment method logo')}
      />

      <PayChannelsEditor
        channels={payChannels}
        onChange={setPayChannels}
        paramFields={[
          { key: 'pay_type', label: t('Sfpay PayType'), placeholder: 'ZFBPAY' },
        ]}
        iconHint={t('React-icons key (alipay/wechat) or an image URL')}
      />

      <Button onClick={handleSave} disabled={loading}>
        {loading ? t('Saving...') : t('Save Changes')}
      </Button>
    </SettingsSection>
  )
}

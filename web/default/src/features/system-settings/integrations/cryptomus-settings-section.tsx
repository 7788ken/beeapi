import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  PayChannelsEditor,
  parsePayChannels,
  type PayChannelItem,
} from './pay-channels-editor'
import { LogoField } from './logo-field'
import { removeTrailingSlash } from './utils'

export interface CryptomusSettingsValues {
  CryptomusEnabled: boolean
  CryptomusMerchantID: string
  CryptomusPaymentApiKey: string
  CryptomusWebhookApiKey: string
  CryptomusDefaultCurrency: string
  CryptomusDefaultNetwork: string
  CryptomusAllowedCurrencies: string
  CryptomusUnitPrice: number
  CryptomusMinTopUp: number
  CryptomusReturnURL: string
  CryptomusAllowedGroups: string
  CryptomusPayChannels: string
  CryptomusLogo: string
}

interface Props {
  defaultValues: CryptomusSettingsValues
}

export function CryptomusSettingsSection(props: Props) {
  const updateOption = useUpdateOption()
  const [loading, setLoading] = useState(false)
  const form = useForm<CryptomusSettingsValues>({
    defaultValues: props.defaultValues,
  })
  const [payChannels, setPayChannels] = useState<PayChannelItem[]>(() =>
    parsePayChannels(props.defaultValues.CryptomusPayChannels)
  )
  const [logo, setLogo] = useState(props.defaultValues.CryptomusLogo || '')

  useEffect(() => {
    form.reset(props.defaultValues)
    setPayChannels(parsePayChannels(props.defaultValues.CryptomusPayChannels))
    setLogo(props.defaultValues.CryptomusLogo || '')
  }, [props.defaultValues, form])

  const handleSave = async () => {
    const values = form.getValues()
    const enabled = !!values.CryptomusEnabled

    if (enabled && !values.CryptomusMerchantID.trim()) {
      toast.error('商户 UUID 为必填项')
      return
    }
    if (enabled && !values.CryptomusPaymentApiKey.trim() && !props.defaultValues.CryptomusMerchantID) {
      toast.error('支付 API Key 为必填项')
      return
    }
    if (enabled && !values.CryptomusDefaultCurrency.trim() && !values.CryptomusAllowedCurrencies.trim()) {
      toast.error('默认结算币种和币种白名单至少填一项')
      return
    }
    if (enabled && Number(values.CryptomusUnitPrice) <= 0) {
      toast.error('单价必须大于 0')
      return
    }
    if (enabled && Number(values.CryptomusMinTopUp) < 1) {
      toast.error('最小充值金额必须 ≥ 1')
      return
    }

    setLoading(true)
    try {
      const options: { key: string; value: string }[] = [
        { key: 'CryptomusEnabled', value: enabled ? 'true' : 'false' },
        {
          key: 'CryptomusMerchantID',
          value: values.CryptomusMerchantID || '',
        },
        {
          key: 'CryptomusDefaultCurrency',
          value: (values.CryptomusDefaultCurrency || 'USDT').toUpperCase(),
        },
        {
          key: 'CryptomusDefaultNetwork',
          value: (values.CryptomusDefaultNetwork || '').toUpperCase(),
        },
        {
          key: 'CryptomusAllowedCurrencies',
          value: values.CryptomusAllowedCurrencies || '',
        },
        {
          key: 'CryptomusUnitPrice',
          value: String(values.CryptomusUnitPrice ?? 1),
        },
        {
          key: 'CryptomusMinTopUp',
          value: String(values.CryptomusMinTopUp ?? 1),
        },
        {
          key: 'CryptomusReturnURL',
          value: removeTrailingSlash(values.CryptomusReturnURL || ''),
        },
        {
          key: 'CryptomusAllowedGroups',
          value: values.CryptomusAllowedGroups || '',
        },
        { key: 'CryptomusPayChannels', value: JSON.stringify(payChannels) },
        { key: 'CryptomusLogo', value: logo },
      ]

      // 仅在用户实际填写时才覆盖密钥，避免清空已存值
      if ((values.CryptomusPaymentApiKey || '').trim()) {
        options.push({
          key: 'CryptomusPaymentApiKey',
          value: values.CryptomusPaymentApiKey,
        })
      }
      if ((values.CryptomusWebhookApiKey || '').trim()) {
        options.push({
          key: 'CryptomusWebhookApiKey',
          value: values.CryptomusWebhookApiKey,
        })
      }

      for (const option of options) {
        await updateOption.mutateAsync(option)
      }
      toast.success('保存成功')
    } catch {
      toast.error('保存失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <SettingsSection
      title='Cryptomus 数字货币支付'
      description='通过 Cryptomus 托管收银台接入 USDT / BTC / ETH 等数字货币充值'
    >
      <Alert>
        <AlertDescription className='text-xs'>
          在 cryptomus.com → Business 后台注册商户，拿到商户 UUID 和支付 API Key。
          Webhook 回调地址：<code>&lt;ServerAddress&gt;/api/cryptomus/webhook</code>，
          需在 Cryptomus 后台 IP/URL 白名单加上本服务地址。
        </AlertDescription>
      </Alert>

      <div className='grid grid-cols-3 gap-4'>
        <div className='flex items-center gap-2'>
          <Switch
            checked={form.watch('CryptomusEnabled')}
            onCheckedChange={(value) =>
              form.setValue('CryptomusEnabled', value)
            }
          />
          <Label>启用 Cryptomus</Label>
        </div>
        <div className='grid gap-1.5'>
          <Label>默认结算币种</Label>
          <Input
            placeholder='USDT'
            {...form.register('CryptomusDefaultCurrency')}
          />
          <p className='text-muted-foreground text-xs'>
            最终到账商户钱包的币种（USDT / BTC / ETH ...）
          </p>
        </div>
        <div className='grid gap-1.5'>
          <Label>默认结算网络</Label>
          <Input
            placeholder='TRON'
            {...form.register('CryptomusDefaultNetwork')}
          />
          <p className='text-muted-foreground text-xs'>
            TRON / ETH / BSC / SOL / ARBITRUM ...（留空则由收银台自选）
          </p>
        </div>
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>商户 UUID</Label>
          <Input
            placeholder='8b03432e-385b-...'
            {...form.register('CryptomusMerchantID')}
          />
        </div>
        <div className='grid gap-1.5'>
          <Label>支付完成跳转 URL</Label>
          <Input
            placeholder='https://example.com/wallet?show_history=true'
            {...form.register('CryptomusReturnURL')}
          />
          <p className='text-muted-foreground text-xs'>
            留空则默认回到钱包页面
          </p>
        </div>
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>支付 API Key</Label>
          <Textarea
            rows={3}
            placeholder='留空则保留已存的旧值'
            {...form.register('CryptomusPaymentApiKey')}
            className='font-mono text-xs'
          />
          <p className='text-muted-foreground text-xs'>
            出于安全考虑，已存的密钥不会回显
          </p>
        </div>
        <div className='grid gap-1.5'>
          <Label>Webhook API Key（可选）</Label>
          <Textarea
            rows={3}
            placeholder='留空时使用支付 API Key 做 webhook 验签'
            {...form.register('CryptomusWebhookApiKey')}
            className='font-mono text-xs'
          />
        </div>
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>币种白名单（CSV，可选）</Label>
          <Input
            placeholder='USDT:TRON,USDT:BSC,BTC,ETH'
            {...form.register('CryptomusAllowedCurrencies')}
          />
          <p className='text-muted-foreground text-xs'>
            格式 <code>币种[:网络]</code>；留空表示让用户在 Cryptomus 收银台
            自由选择全部支持币种
          </p>
        </div>
        <div className='grid grid-cols-2 gap-4'>
          <div className='grid gap-1.5'>
            <Label>单价（USD）</Label>
            <Input
              type='number'
              step={0.01}
              min={0}
              {...form.register('CryptomusUnitPrice', { valueAsNumber: true })}
            />
          </div>
          <div className='grid gap-1.5'>
            <Label>最小充值（USD）</Label>
            <Input
              type='number'
              min={1}
              {...form.register('CryptomusMinTopUp', { valueAsNumber: true })}
            />
          </div>
        </div>
      </div>

      <div className='grid gap-1.5'>
        <Label>允许使用的组别（可选）</Label>
        <Input
          placeholder='vip;premium'
          {...form.register('CryptomusAllowedGroups')}
        />
        <p className='text-muted-foreground text-xs'>
          留空对所有分组开放；多个分组用 ; 分隔
        </p>
      </div>

      <Separator />

      <LogoField value={logo} onChange={setLogo} label='支付方式 Logo' />

      <PayChannelsEditor
        channels={payChannels}
        onChange={setPayChannels}
        paramFields={[
          {
            key: 'to_currency',
            label: '币种 (to_currency)',
            placeholder: 'USDT',
          },
          { key: 'network', label: '网络 (network)', placeholder: 'TRX' },
        ]}
        iconHint='react-icons 键（tether/bitcoin/ethereum）或图片 URL；留空 network 由收银台按币种自选默认链'
      />

      <Button onClick={handleSave} disabled={loading}>
        {loading ? '保存中...' : '保存 Cryptomus 设置'}
      </Button>
    </SettingsSection>
  )
}

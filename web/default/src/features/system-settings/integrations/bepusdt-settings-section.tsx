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
import { LogoField } from './logo-field'
import {
  PayChannelsEditor,
  parsePayChannels,
  type PayChannelItem,
} from './pay-channels-editor'
import { removeTrailingSlash } from './utils'

export interface BepusdtSettingsValues {
  BepusdtEnabled: boolean
  BepusdtBaseURL: string
  BepusdtApiToken: string
  BepusdtCurrencies: string
  BepusdtLifetimeSec: number
  BepusdtUnitPrice: number
  BepusdtMinTopUp: number
  BepusdtReturnURL: string
  BepusdtAllowedGroups: string
  BepusdtPayChannels: string
  BepusdtLogo: string
}

interface Props {
  defaultValues: BepusdtSettingsValues
}

// 网关 create-order 接受的最小订单有效期（秒）
const MIN_LIFETIME_SEC = 180

export function BepusdtSettingsSection(props: Props) {
  const updateOption = useUpdateOption()
  const [loading, setLoading] = useState(false)
  const form = useForm<BepusdtSettingsValues>({
    defaultValues: props.defaultValues,
  })
  const [payChannels, setPayChannels] = useState<PayChannelItem[]>(() =>
    parsePayChannels(props.defaultValues.BepusdtPayChannels)
  )
  const [logo, setLogo] = useState(props.defaultValues.BepusdtLogo || '')

  useEffect(() => {
    form.reset(props.defaultValues)
    setPayChannels(parsePayChannels(props.defaultValues.BepusdtPayChannels))
    setLogo(props.defaultValues.BepusdtLogo || '')
  }, [props.defaultValues, form])

  const handleSave = async () => {
    const values = form.getValues()
    const enabled = !!values.BepusdtEnabled
    const baseUrl = removeTrailingSlash((values.BepusdtBaseURL || '').trim())

    if (enabled && !baseUrl) {
      toast.error('网关地址为必填项')
      return
    }
    if (baseUrl && !/^https?:\/\//.test(baseUrl)) {
      toast.error('网关地址必须以 http:// 或 https:// 开头')
      return
    }
    if (
      enabled &&
      !(values.BepusdtApiToken || '').trim() &&
      !props.defaultValues.BepusdtApiToken
    ) {
      toast.error('对接令牌为必填项')
      return
    }
    if (enabled && Number(values.BepusdtLifetimeSec) < MIN_LIFETIME_SEC) {
      toast.error(`订单有效期不能低于 ${MIN_LIFETIME_SEC} 秒（网关下限）`)
      return
    }
    if (enabled && Number(values.BepusdtUnitPrice) <= 0) {
      toast.error('单价必须大于 0')
      return
    }
    if (enabled && Number(values.BepusdtMinTopUp) < 1) {
      toast.error('最小充值金额必须 ≥ 1')
      return
    }

    setLoading(true)
    try {
      const options: { key: string; value: string }[] = [
        { key: 'BepusdtEnabled', value: enabled ? 'true' : 'false' },
        { key: 'BepusdtBaseURL', value: baseUrl },
        {
          key: 'BepusdtCurrencies',
          value: (values.BepusdtCurrencies || 'USDT').trim().toUpperCase(),
        },
        {
          key: 'BepusdtLifetimeSec',
          value: String(values.BepusdtLifetimeSec ?? 1200),
        },
        {
          key: 'BepusdtUnitPrice',
          value: String(values.BepusdtUnitPrice ?? 1),
        },
        {
          key: 'BepusdtMinTopUp',
          value: String(values.BepusdtMinTopUp ?? 1),
        },
        {
          key: 'BepusdtReturnURL',
          value: removeTrailingSlash(values.BepusdtReturnURL || ''),
        },
        {
          key: 'BepusdtAllowedGroups',
          value: values.BepusdtAllowedGroups || '',
        },
        { key: 'BepusdtPayChannels', value: JSON.stringify(payChannels) },
        { key: 'BepusdtLogo', value: logo },
      ]

      // 仅在用户实际填写时才覆盖令牌，避免清空已存值
      if ((values.BepusdtApiToken || '').trim()) {
        options.push({
          key: 'BepusdtApiToken',
          value: values.BepusdtApiToken.trim(),
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
      title='USDT 自建网关（BEpusdt）'
      description='自部署的 BEpusdt 加密币收款网关，用户在网关收银台自选 USDT 链付款，链上到账后自动入账'
      surface={false}
    >
      <Alert>
        <AlertDescription className='text-xs'>
          对接令牌在网关后台「基本设置 → API设置」查看 /
          重置（重置后这里必须同步）。 回调地址固定为：
          <code className='break-all'>
            &lt;ServerAddress&gt;/api/bepusdt/webhook
          </code>
          ，网关需能从公网访问本站。
          <br />
          启用后本网关顶替 Cryptomus 成为充值页唯一的加密货币入口（Cryptomus
          配置保留；关掉本开关即切回）。
        </AlertDescription>
      </Alert>

      <div className='grid grid-cols-3 gap-4'>
        <div className='flex items-center gap-2'>
          <Switch
            checked={form.watch('BepusdtEnabled')}
            onCheckedChange={(value) => form.setValue('BepusdtEnabled', value)}
          />
          <Label>启用 BEpusdt</Label>
        </div>
        <div className='grid gap-1.5'>
          <Label>收银台限定币种</Label>
          <Input placeholder='USDT' {...form.register('BepusdtCurrencies')} />
          <p className='text-muted-foreground text-xs'>
            网关 currencies 参数：USDT / USDT,USDC；短横线开头为黑名单（-ETH）
          </p>
        </div>
        <div className='grid gap-1.5'>
          <Label>订单有效期（秒）</Label>
          <Input
            type='number'
            min={MIN_LIFETIME_SEC}
            {...form.register('BepusdtLifetimeSec', { valueAsNumber: true })}
          />
          <p className='text-muted-foreground text-xs'>
            建议与网关「订单默认超时」一致（出厂 1200），下限 180
          </p>
        </div>
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>网关地址</Label>
          <Input
            placeholder='https://pay.example.com'
            {...form.register('BepusdtBaseURL')}
          />
          <p className='text-muted-foreground text-xs'>
            网关公网根地址，不带路径；需与网关后台「应用 URI」一致
          </p>
        </div>
        <div className='grid gap-1.5'>
          <Label>支付完成跳转 URL</Label>
          <Input
            placeholder='https://example.com/wallet?show_history=true'
            {...form.register('BepusdtReturnURL')}
          />
          <p className='text-muted-foreground text-xs'>
            留空则默认回到钱包页面
          </p>
        </div>
      </div>

      <div className='grid grid-cols-2 gap-4'>
        <div className='grid gap-1.5'>
          <Label>对接令牌（API Token）</Label>
          <Textarea
            rows={3}
            placeholder='留空则保留已存的旧值'
            {...form.register('BepusdtApiToken')}
            className='font-mono text-xs'
          />
          <p className='text-muted-foreground text-xs'>
            建单与回调验签共用；出于安全考虑，已存的令牌不会回显
          </p>
        </div>
        <div className='grid grid-cols-2 gap-4'>
          <div className='grid gap-1.5'>
            <Label>单价（USD）</Label>
            <Input
              type='number'
              step={0.01}
              min={0}
              {...form.register('BepusdtUnitPrice', { valueAsNumber: true })}
            />
          </div>
          <div className='grid gap-1.5'>
            <Label>最小充值（USD）</Label>
            <Input
              type='number'
              min={1}
              {...form.register('BepusdtMinTopUp', { valueAsNumber: true })}
            />
          </div>
        </div>
      </div>

      <div className='grid gap-1.5'>
        <Label>允许使用的组别（可选）</Label>
        <Input
          placeholder='vip;premium'
          {...form.register('BepusdtAllowedGroups')}
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
        paramFields={[]}
        iconHint='仅用于充值页展示支持的链（react-icons 键 tether 或图片 URL）；实际选链在网关收银台完成'
      />

      <Button onClick={handleSave} disabled={loading}>
        {loading ? '保存中...' : '保存 BEpusdt 设置'}
      </Button>
    </SettingsSection>
  )
}

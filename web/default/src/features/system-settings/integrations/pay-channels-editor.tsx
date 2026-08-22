import { useState } from 'react'
import { Plus, Pencil, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

// 与后端 constant.PayChannel 对齐：充值页每个支付平台下展示的可选渠道。
export interface PayChannelItem {
  key: string
  name: string
  icon: string
  enabled: boolean
  params: Record<string, string>
}

interface ParamField {
  key: string
  label: string
  placeholder?: string
}

interface Props {
  channels: PayChannelItem[]
  onChange: (channels: PayChannelItem[]) => void
  /** 该平台发起支付时透传的网关参数字段（agou: pay_type；cryptomus: to_currency/network；waffo_pancake: 无） */
  paramFields: ParamField[]
  /** 图标字段说明（可选） */
  iconHint?: string
}

// 解析已存 JSON；容错返回空数组。供 section 初始化 state 用。
export function parsePayChannels(raw: string | undefined): PayChannelItem[] {
  try {
    const parsed = JSON.parse(raw || '[]')
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}

const emptyChannel = (): PayChannelItem => ({
  key: '',
  name: '',
  icon: '',
  enabled: true,
  params: {},
})

// 支付渠道编辑表格：复用 Waffo 支付方式表格范式，按 paramFields 渲染各平台专属网关参数列。
export function PayChannelsEditor({
  channels,
  onChange,
  paramFields,
  iconHint,
}: Props) {
  const { t } = useTranslation()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingIdx, setEditingIdx] = useState(-1)
  const [draft, setDraft] = useState<PayChannelItem>(emptyChannel())

  const openAdd = () => {
    setEditingIdx(-1)
    setDraft(emptyChannel())
    setDialogOpen(true)
  }
  const openEdit = (idx: number) => {
    setEditingIdx(idx)
    setDraft({ ...channels[idx], params: { ...channels[idx].params } })
    setDialogOpen(true)
  }
  const save = () => {
    if (!draft.key.trim()) return toast.error(t('Channel key is required'))
    if (!draft.name.trim()) return toast.error(t('Channel name is required'))
    const next =
      editingIdx === -1
        ? [...channels, draft]
        : channels.map((c, i) => (i === editingIdx ? draft : c))
    onChange(next)
    setDialogOpen(false)
  }
  const remove = (idx: number) => onChange(channels.filter((_, i) => i !== idx))
  const toggle = (idx: number, v: boolean) =>
    onChange(channels.map((c, i) => (i === idx ? { ...c, enabled: v } : c)))
  const setParam = (k: string, v: string) =>
    setDraft((p) => ({ ...p, params: { ...p.params, [k]: v } }))

  const colSpan = 5 + paramFields.length

  return (
    <>
      <div className='flex items-center justify-between'>
        <h4 className='font-medium'>{t('Payment Channels')}</h4>
        <Button variant='outline' size='sm' onClick={openAdd}>
          <Plus className='mr-1 h-3 w-3' />
          {t('Add Channel')}
        </Button>
      </div>
      <div className='rounded-md border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className='w-16'>{t('Enabled')}</TableHead>
              <TableHead>{t('Channel Name')}</TableHead>
              <TableHead>{t('Key')}</TableHead>
              <TableHead>{t('Icon')}</TableHead>
              {paramFields.map((f) => (
                <TableHead key={f.key}>{f.label}</TableHead>
              ))}
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {channels.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={colSpan}
                  className='text-muted-foreground py-8 text-center'
                >
                  {t('No payment channels configured')}
                </TableCell>
              </TableRow>
            ) : (
              channels.map((c, idx) => (
                <TableRow key={idx}>
                  <TableCell>
                    <Switch
                      checked={c.enabled}
                      onCheckedChange={(v) => toggle(idx, v)}
                    />
                  </TableCell>
                  <TableCell>{c.name}</TableCell>
                  <TableCell className='font-mono text-xs'>{c.key}</TableCell>
                  <TableCell className='text-muted-foreground text-xs'>
                    {c.icon || '-'}
                  </TableCell>
                  {paramFields.map((f) => (
                    <TableCell key={f.key} className='font-mono text-xs'>
                      {c.params?.[f.key] || '-'}
                    </TableCell>
                  ))}
                  <TableCell className='text-right'>
                    <div className='flex justify-end gap-1'>
                      <Button
                        variant='ghost'
                        size='icon'
                        className='h-7 w-7'
                        onClick={() => openEdit(idx)}
                      >
                        <Pencil className='h-3 w-3' />
                      </Button>
                      <Button
                        variant='ghost'
                        size='icon'
                        className='h-7 w-7'
                        onClick={() => remove(idx)}
                      >
                        <Trash2 className='h-3 w-3' />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editingIdx === -1 ? t('Add Channel') : t('Edit Channel')}
            </DialogTitle>
          </DialogHeader>
          <div className='space-y-3'>
            <div className='flex items-center gap-2'>
              <Switch
                checked={draft.enabled}
                onCheckedChange={(v) => setDraft((p) => ({ ...p, enabled: v }))}
              />
              <Label>{t('Enabled')}</Label>
            </div>
            <div className='grid gap-1.5'>
              <Label>{t('Key')} *</Label>
              <Input
                value={draft.key}
                onChange={(e) =>
                  setDraft((p) => ({ ...p, key: e.target.value }))
                }
                placeholder='alipay / usdt_trc20'
              />
            </div>
            <div className='grid gap-1.5'>
              <Label>{t('Channel Name')} *</Label>
              <Input
                value={draft.name}
                onChange={(e) =>
                  setDraft((p) => ({ ...p, name: e.target.value }))
                }
              />
            </div>
            <div className='grid gap-1.5'>
              <Label>{t('Icon')}</Label>
              <Input
                value={draft.icon}
                onChange={(e) =>
                  setDraft((p) => ({ ...p, icon: e.target.value }))
                }
                placeholder='alipay / tether / /pay-card.png'
              />
              {iconHint ? (
                <p className='text-muted-foreground text-xs'>{iconHint}</p>
              ) : null}
            </div>
            {paramFields.map((f) => (
              <div key={f.key} className='grid gap-1.5'>
                <Label>{f.label}</Label>
                <Input
                  value={draft.params?.[f.key] || ''}
                  onChange={(e) => setParam(f.key, e.target.value)}
                  placeholder={f.placeholder}
                />
              </div>
            ))}
          </div>
          <DialogFooter>
            <Button variant='outline' onClick={() => setDialogOpen(false)}>
              {t('Cancel')}
            </Button>
            <Button onClick={save}>{t('Confirm')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

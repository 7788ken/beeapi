import { useEffect, useState } from 'react'
import { Loader2, RefreshCw, TrendingUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  type ChannelRatioBaseline,
  type ChannelRatioChange,
  getChannelRatioChanges,
} from '../../api'
import { RATIO_BADGE_WINDOW_DAYS } from '../../constants'
import { useChannels } from '../channels-provider'

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

// 倍率口径：group=上游分组倍率原值，resolved=含专属折扣的稳定值（比对基准），
// effective=resolved×高峰系数，api_rate=donehub 的接口倍率，peak=高峰配置本身。
const ratioKindLabel: Record<string, string> = {
  group: '分组倍率',
  resolved: '实际倍率',
  effective: '当前生效',
  api_rate: '接口倍率',
  peak: '高峰配置',
}

function formatRatio(value: number): string {
  if (!Number.isFinite(value)) return '—'
  return String(Number(value.toFixed(6)))
}

export function ChannelRatioChangesDialog({ open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const { currentRow } = useChannels()

  const [loading, setLoading] = useState(false)
  const [changes, setChanges] = useState<ChannelRatioChange[]>([])
  const [current, setCurrent] = useState<ChannelRatioBaseline[]>([])

  const channelId = currentRow?.id
  const channelName = currentRow?.name

  // 与列表口径一致：优先 resolved（sub2api 实付倍率），否则 group
  const currentRatios = (() => {
    const resolved = current.filter((c) => c.ratio_kind === 'resolved')
    const base = resolved.length > 0 ? resolved : current.filter((c) => c.ratio_kind === 'group')
    return [...base].sort((a, b) => a.ratio - b.ratio)
  })()

  const fetchData = async () => {
    if (!channelId) return
    setLoading(true)
    try {
      const res = await getChannelRatioChanges(
        channelId,
        RATIO_BADGE_WINDOW_DAYS
      )
      if (!res.success) {
        toast.error(res.message || t('Failed to load ratio changes'))
        return
      }
      setChanges(res.data ?? [])
      setCurrent(res.current ?? [])
    } catch (err: unknown) {
      toast.error(
        err instanceof Error ? err.message : t('Failed to load ratio changes')
      )
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (open && channelId) {
      setChanges([])
      setCurrent([])
      fetchData()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, channelId])

  if (!currentRow) return null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-w-2xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <TrendingUp className='h-5 w-5' />
            {t('Upstream group ratio changes')}
          </DialogTitle>
          <DialogDescription>
            {t('Channel:')} <strong>{channelName}</strong> (#{channelId})
          </DialogDescription>
        </DialogHeader>

        <div className='py-2'>
          <div className='mb-2 flex items-center justify-between'>
            <h4 className='text-sm font-semibold'>
              {t('Changes in the last {{days}} days', {
                days: RATIO_BADGE_WINDOW_DAYS,
              })}
            </h4>
            <Button
              variant='ghost'
              size='sm'
              onClick={fetchData}
              disabled={loading}
            >
              {loading ? (
                <Loader2 className='h-4 w-4 animate-spin' />
              ) : (
                <RefreshCw className='h-4 w-4' />
              )}
            </Button>
          </div>

          {/* 首要回答"此刻各分组倍率是多少"，变更历史其次 */}
          <div className='mb-3'>
            <h4 className='mb-2 text-sm font-medium'>
              {t('Current group ratios')}
            </h4>
            <ScrollArea className='h-40 rounded border'>
              {currentRatios.length === 0 ? (
                <div className='text-muted-foreground p-4 text-center text-sm'>
                  {loading ? t('Loading...') : t('Not fetched')}
                </div>
              ) : (
                <div className='divide-y'>
                  {currentRatios.map((item) => (
                    <div
                      key={`${item.group_name}-${item.ratio_kind}`}
                      className='flex items-center justify-between gap-3 px-3 py-1.5 text-xs'
                    >
                      <span className='break-all'>{item.group_name}</span>
                      <span className='shrink-0 font-medium tabular-nums'>
                        {item.ratio}
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </ScrollArea>
          </div>

          <ScrollArea className='h-72 rounded border'>
            {loading && changes.length === 0 ? (
              <div className='text-muted-foreground p-4 text-center text-sm'>
                {t('Loading...')}
              </div>
            ) : changes.length === 0 ? (
              <div className='text-muted-foreground p-4 text-center text-sm'>
                {t('No ratio changes')}
              </div>
            ) : (
              <div className='divide-y'>
                {changes.map((chg, idx) => (
                  <div
                    key={`${chg.group_name}-${chg.ratio_kind}-${chg.batch_at}-${idx}`}
                    className='flex items-start gap-3 px-3 py-2 text-xs'
                  >
                    <Badge
                      variant={chg.direction > 0 ? 'destructive' : 'secondary'}
                      className='shrink-0'
                    >
                      {chg.direction > 0 ? '↑' : '↓'}
                    </Badge>
                    <div className='flex-1 space-y-1'>
                      <div className='font-medium break-all'>
                        {chg.group_name}
                        <span className='text-muted-foreground ml-2 font-normal'>
                          {ratioKindLabel[chg.ratio_kind] ?? chg.ratio_kind}
                        </span>
                      </div>
                      <div className='tabular-nums'>
                        <span className='text-muted-foreground'>
                          {formatRatio(chg.old_value)}
                        </span>
                        <span className='mx-1.5'>→</span>
                        <span
                          className={cn(
                            'font-semibold',
                            chg.direction > 0
                              ? 'text-rose-600'
                              : 'text-emerald-600'
                          )}
                        >
                          {formatRatio(chg.new_value)}
                        </span>
                      </div>
                    </div>
                    <div className='text-muted-foreground shrink-0 font-mono'>
                      {formatTimestampToDate(chg.batch_at)}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </ScrollArea>
        </div>

        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

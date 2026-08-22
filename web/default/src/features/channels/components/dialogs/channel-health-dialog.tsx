import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Activity, Loader2, RefreshCw, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestampToDate } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Separator } from '@/components/ui/separator'
import {
  type ChannelHealthEvent,
  type ChannelHealthSnapshot,
  getChannelHealth,
  recoverChannelHealth,
} from '../../api'
import { channelsQueryKeys } from '../../lib'
import { useChannels } from '../channels-provider'

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const eventTypeColor: Record<
  ChannelHealthEvent['event_type'],
  'default' | 'destructive' | 'secondary' | 'outline'
> = {
  demote: 'destructive',
  disable: 'destructive',
  upgrade: 'secondary',
  enable: 'secondary',
  snapshot: 'outline',
}

function formatLevel(level: number, t: (k: string) => string): string {
  if (level === -1) return t('Disabled')
  if (level === 0) return 'L0'
  if (level === 1) return 'L1'
  if (level === 2) return 'L2'
  return `L${level}`
}

export function ChannelHealthDialog({ open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const { currentRow } = useChannels()
  const queryClient = useQueryClient()

  const [loading, setLoading] = useState(false)
  const [recovering, setRecovering] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [snapshot, setSnapshot] = useState<ChannelHealthSnapshot | null>(null)
  const [events, setEvents] = useState<ChannelHealthEvent[]>([])

  const channelId = currentRow?.id
  const channelName = currentRow?.name

  const fetchData = async () => {
    if (!channelId) return
    setLoading(true)
    try {
      const res = await getChannelHealth(channelId, { days: 30, limit: 200 })
      if (!res.success) {
        toast.error(res.message || t('Failed to load health events'))
        return
      }
      setSnapshot(res.data.snapshot)
      setEvents(res.data.events ?? [])
    } catch (err: unknown) {
      toast.error(
        err instanceof Error ? err.message : t('Failed to load health events')
      )
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (open && channelId) {
      // 切换 channelId 或重新打开时先清空旧数据，避免显示上一个渠道的快照/事件造成误读
      setSnapshot(null)
      setEvents([])
      fetchData()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, channelId])

  const handleRecover = async () => {
    if (!channelId) return
    setRecovering(true)
    try {
      const res = await recoverChannelHealth(channelId)
      if (!res.success) {
        toast.error(res.message || t('Recovery failed'))
        return
      }
      toast.success(t('Channel recovered to L0'))
      await fetchData()
      await queryClient.invalidateQueries({
        queryKey: channelsQueryKeys.lists(),
      })
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('Recovery failed'))
    } finally {
      setRecovering(false)
      setConfirmOpen(false)
    }
  }

  const handleClose = () => {
    setSnapshot(null)
    setEvents([])
    onOpenChange(false)
  }

  if (!currentRow) return null

  const level = snapshot?.degrade_level ?? 0
  const permLocked = (snapshot?.permanent_disabled ?? 0) === 1

  return (
    <>
      <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
        <DialogContent className='max-w-2xl'>
          <DialogHeader>
            <DialogTitle className='flex items-center gap-2'>
              <Activity className='h-5 w-5' />
              {t('Channel health')}
            </DialogTitle>
            <DialogDescription>
              {t('Channel:')} <strong>{channelName}</strong> (#{channelId})
            </DialogDescription>
          </DialogHeader>

          <div className='space-y-4 py-2'>
            {/* Snapshot */}
            <div className='bg-muted/50 rounded-lg border p-4'>
              <div className='mb-3 flex items-center justify-between'>
                <h4 className='text-sm font-semibold'>{t('Snapshot')}</h4>
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
              {loading && !snapshot ? (
                <div className='text-muted-foreground text-sm'>
                  {t('Loading...')}
                </div>
              ) : snapshot ? (
                <div className='grid grid-cols-2 gap-2 text-xs'>
                  <div>
                    {t('Current level:')}{' '}
                    <strong>{formatLevel(level, t)}</strong>
                  </div>
                  <div>
                    {t('Status:')}{' '}
                    <Badge variant='outline'>
                      {permLocked
                        ? t('Locked')
                        : snapshot.status === 1
                          ? t('Enabled')
                          : snapshot.status === 2
                            ? t('Manually disabled')
                            : snapshot.status === 3
                              ? t('Auto disabled')
                              : t('Disabled')}
                    </Badge>
                  </div>
                  <div>
                    {t('Current priority:')} {snapshot.current_priority}
                  </div>
                  <div>
                    {t('Current weight:')} {snapshot.current_weight}
                  </div>
                  <div>
                    {t('Original priority:')} {snapshot.original_priority}
                  </div>
                  <div>
                    {t('Original weight:')} {snapshot.original_weight}
                  </div>
                  <div className='col-span-2'>
                    {t('Last demote:')}{' '}
                    {snapshot.last_demote_at > 0
                      ? formatTimestampToDate(snapshot.last_demote_at)
                      : '-'}
                  </div>
                  {snapshot.last_demote_reason && (
                    <div className='col-span-2 break-all'>
                      {t('Reason:')} {snapshot.last_demote_reason}
                    </div>
                  )}
                  <div>
                    {t('Last upgrade:')}{' '}
                    {snapshot.last_upgrade_at > 0
                      ? formatTimestampToDate(snapshot.last_upgrade_at)
                      : '-'}
                  </div>
                  <div>
                    {t('Rebounce count:')} {snapshot.rebounce_count}
                  </div>
                  {permLocked && (
                    <div className='col-span-2 text-red-500'>
                      {t('Permanently locked — manual recovery required')}
                    </div>
                  )}
                </div>
              ) : (
                <div className='text-muted-foreground text-sm'>
                  {t('No data')}
                </div>
              )}
            </div>

            <Separator />

            {/* Events */}
            <div>
              <h4 className='mb-2 text-sm font-semibold'>
                {t('Recent events (30 days)')}
              </h4>
              <ScrollArea className='h-64 rounded border'>
                {events.length === 0 ? (
                  <div className='text-muted-foreground p-4 text-center text-sm'>
                    {t('No events')}
                  </div>
                ) : (
                  <div className='divide-y'>
                    {events.map((evt) => (
                      <div
                        key={evt.id}
                        className='flex items-start gap-3 px-3 py-2 text-xs'
                      >
                        <Badge
                          variant={eventTypeColor[evt.event_type] ?? 'outline'}
                          className='shrink-0'
                        >
                          {evt.event_type}
                        </Badge>
                        <div className='flex-1 space-y-1'>
                          <div>
                            {formatLevel(evt.from_level, t)} →{' '}
                            {formatLevel(evt.to_level, t)}
                            <span className='text-muted-foreground ml-2'>
                              [{evt.operator}]
                            </span>
                          </div>
                          {evt.reason && (
                            <div className='text-muted-foreground break-all'>
                              {evt.reason}
                            </div>
                          )}
                        </div>
                        <div className='text-muted-foreground shrink-0 font-mono'>
                          {formatTimestampToDate(evt.created_at)}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </ScrollArea>
            </div>
          </div>

          <DialogFooter>
            <Button
              variant='outline'
              onClick={handleClose}
              disabled={recovering}
            >
              {t('Close')}
            </Button>
            <Button
              variant='destructive'
              onClick={() => setConfirmOpen(true)}
              disabled={
                recovering ||
                (level === 0 && !permLocked && snapshot?.status === 1)
              }
            >
              {recovering ? (
                <Loader2 className='mr-2 h-4 w-4 animate-spin' />
              ) : (
                <RotateCcw className='mr-2 h-4 w-4' />
              )}
              {t('Recover to L0')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Recover channel health')}
        desc={t(
          'Reset degrade level to 0, restore original priority/weight, and re-enable channel if disabled. This is a manual override.'
        )}
        confirmText={t('Recover')}
        destructive
        handleConfirm={handleRecover}
        isLoading={recovering}
      />
    </>
  )
}

import { useLayoutEffect, useRef, useState, type RefObject } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatDateTimeObject } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useStatus } from '@/hooks/use-status'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useAnnouncements } from '@/features/dashboard/hooks/use-status-data'
import { getPreviewText } from '@/features/dashboard/lib'
import type { AnnouncementItem } from '@/features/dashboard/types'
import { AnnouncementDetailModal } from './announcement-detail-dialog'
import {
  DashBody,
  DashCard,
  DashSkeleton,
  DashTitle,
  dashSecondaryBtn,
} from './dash-style'

// 卡片固定露出的条数，由它们撑出卡片的最小高度；
// 其余公告只填同一行面板已经撑出的空间，不再把整行拉长
const MIN_VISIBLE = 3

function announcementKind(
  type: string | undefined,
  t: (key: string) => string
) {
  switch (type) {
    case 'ongoing':
      return t('In progress')
    case 'success':
      return t('Recovered')
    case 'warning':
      return t('Please note')
    case 'error':
      return t('Outage')
    default:
      return t('Notice')
  }
}

function announcementTone(type: string | undefined) {
  if (type === 'success') return 'text-primary'
  if (type === 'error') return 'font-semibold text-foreground'
  return 'text-muted-foreground'
}

function rowKey(item: AnnouncementItem, idx: number) {
  return item.id ?? `announcement-${idx}`
}

/** 容器不参与撑高（contain-size），返回其中能完整放下的行数 */
function useFittedCount(
  boxRef: RefObject<HTMLDivElement | null>,
  items: AnnouncementItem[]
) {
  const [count, setCount] = useState(0)

  useLayoutEffect(() => {
    const box = boxRef.current
    if (!box) return
    const measure = () => {
      const rows = Array.from(box.children) as HTMLElement[]
      const overflowAt = rows.findIndex(
        (row) => row.offsetTop + row.offsetHeight > box.clientHeight
      )
      setCount(overflowAt === -1 ? rows.length : overflowAt)
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(box)
    return () => observer.disconnect()
  }, [boxRef, items])

  return count
}

function AnnouncementRow(props: {
  item: AnnouncementItem
  hidden?: boolean
  className?: string
  onOpen: () => void
}) {
  const { t } = useTranslation()
  const { item } = props
  return (
    <div
      role='listitem'
      className={cn('border-border/70 border-t', props.hidden && 'invisible')}
    >
      <button
        type='button'
        onClick={props.onOpen}
        className={cn(
          'hover:bg-foreground/[0.04] flex w-full items-center gap-3 py-3 text-left transition-colors duration-[300ms] ease-out focus-visible:shadow-[inset_0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none motion-reduce:transition-none',
          props.className
        )}
      >
        <span className='min-w-0 flex-1'>
          <span
            className={cn(
              'block font-sans text-xs',
              announcementTone(item.type)
            )}
          >
            {announcementKind(item.type, t)}
            {item.publishDate
              ? ` · ${formatDateTimeObject(new Date(item.publishDate))}`
              : ''}
          </span>
          <span className='text-foreground mt-1 block truncate text-sm'>
            {getPreviewText(item.content)}
          </span>
        </span>
        <ChevronRight
          aria-hidden
          className='text-muted-foreground size-4 shrink-0'
        />
      </button>
    </div>
  )
}

export function AnnouncementsPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { items: list, loading } = useAnnouncements()
  const { error } = useStatus()
  const restRef = useRef<HTMLDivElement>(null)
  const restVisible = useFittedCount(restRef, list)
  const [isAllOpen, setIsAllOpen] = useState(false)
  const [selectedAnnouncement, setSelectedAnnouncement] =
    useState<AnnouncementItem | null>(null)
  const [isDialogOpen, setIsDialogOpen] = useState(false)

  const head = list.slice(0, MIN_VISIBLE)
  const rest = list.slice(MIN_VISIBLE)

  const openDetail = (item: AnnouncementItem) => {
    setSelectedAnnouncement(item)
    setIsDialogOpen(true)
  }

  return (
    <DashCard className='h-full'>
      <DashTitle
        title={t('Announcements')}
        actions={
          error || rest.length > 0 ? (
            <>
              {error ? (
                <button
                  type='button'
                  className={dashSecondaryBtn}
                  onClick={() => {
                    void queryClient.invalidateQueries({ queryKey: ['status'] })
                  }}
                >
                  {t('Retry')}
                </button>
              ) : null}
              {rest.length > 0 ? (
                <button
                  type='button'
                  className={dashSecondaryBtn}
                  onClick={() => setIsAllOpen(true)}
                >
                  {t('View all {{count}}', { count: list.length })}
                </button>
              ) : null}
            </>
          ) : undefined
        }
      />
      <DashBody>
        {loading ? (
          <DashSkeleton className='h-24 w-full' />
        ) : error && list.length === 0 ? (
          <p className='font-sans text-sm text-pretty'>
            {t('This section could not be refreshed.')}
          </p>
        ) : list.length === 0 ? (
          <p className='text-muted-foreground font-sans text-sm text-pretty'>
            {t('Nothing you need to handle right now.')}
          </p>
        ) : (
          <div role='list' className='-mx-6 flex flex-1 flex-col md:-mx-8'>
            {head.map((item, idx) => (
              <AnnouncementRow
                key={rowKey(item, idx)}
                item={item}
                className='px-6 md:px-8'
                onOpen={() => openDetail(item)}
              />
            ))}
            {rest.length > 0 ? (
              <div
                ref={restRef}
                className='relative flex-1 overflow-hidden contain-size'
              >
                {rest.map((item, idx) => (
                  <AnnouncementRow
                    key={rowKey(item, MIN_VISIBLE + idx)}
                    item={item}
                    hidden={idx >= restVisible}
                    className='px-6 md:px-8'
                    onOpen={() => openDetail(item)}
                  />
                ))}
              </div>
            ) : null}
          </div>
        )}
      </DashBody>
      <Dialog open={isAllOpen} onOpenChange={setIsAllOpen}>
        <DialogContent aria-describedby={undefined}>
          <DialogHeader>
            <DialogTitle>{t('Announcements')}</DialogTitle>
          </DialogHeader>
          <div role='list' className='-mx-6 max-h-[60vh] overflow-y-auto'>
            {list.map((item, idx) => (
              <AnnouncementRow
                key={rowKey(item, idx)}
                item={item}
                className='px-6'
                onOpen={() => openDetail(item)}
              />
            ))}
          </div>
        </DialogContent>
      </Dialog>
      <AnnouncementDetailModal
        open={isDialogOpen}
        onOpenChange={setIsDialogOpen}
        announcement={selectedAnnouncement}
      />
    </DashCard>
  )
}

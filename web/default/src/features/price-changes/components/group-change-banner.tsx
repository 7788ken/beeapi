import { useEffect, useRef, useState } from 'react'
import type { CSSProperties } from 'react'
import { Megaphone, TrendingDown, TrendingUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatDate } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useMediaQuery } from '@/hooks/use-media-query'
import { formatRatioValue, parseGroupGroupRatioName } from '../lib'
import type { RecentGroupChange } from '../lib'

interface GroupChangeBannerProps {
  changes: RecentGroupChange[]
  /** Called when a marquee item is clicked (opens the price changes drawer) */
  onItemClick?: () => void
  className?: string
}

const MARQUEE_PIXELS_PER_SECOND = 60
const MARQUEE_MIN_DURATION_SECONDS = 20

function changeKey(change: RecentGroupChange): string {
  return `${change.item.group_name}:${change.item.price_type}`
}

function GroupChangeItem({
  change,
  onClick,
  duplicate,
}: {
  change: RecentGroupChange
  onClick?: () => void
  /** Second copy of the track: same markup (so widths match) but not focusable */
  duplicate?: boolean
}) {
  const { t } = useTranslation()
  const { item } = change
  const isUp = item.new_value > item.old_value
  const Icon = isUp ? TrendingUp : TrendingDown
  const parts =
    item.price_type === 'group_group_ratio'
      ? parseGroupGroupRatioName(item.group_name)
      : null

  const text = parts
    ? t(
        'Special ratio for {{userGroup}} → {{targetGroup}} adjusted from {{old}} to {{new}} ({{time}})',
        {
          userGroup: parts.userGroup,
          targetGroup: parts.targetGroup,
          old: formatRatioValue(item.old_value),
          new: formatRatioValue(item.new_value),
          time: formatDate(change.publishedAt),
        }
      )
    : t(
        'Ratio of group "{{group}}" adjusted from {{old}} to {{new}} ({{time}})',
        {
          group: item.group_name,
          old: formatRatioValue(item.old_value),
          new: formatRatioValue(item.new_value),
          time: formatDate(change.publishedAt),
        }
      )

  const content = (
    <>
      <Icon
        className={cn(
          'size-3.5 shrink-0',
          isUp
            ? 'text-red-600 dark:text-red-400'
            : 'text-emerald-600 dark:text-emerald-400'
        )}
      />
      {text}
    </>
  )

  const classes = 'inline-flex items-center gap-1.5 pe-8 whitespace-nowrap'

  if (!onClick) {
    return <span className={classes}>{content}</span>
  }

  return (
    <button
      type='button'
      onClick={onClick}
      tabIndex={duplicate ? -1 : undefined}
      className={cn(classes, 'hover:underline')}
    >
      {content}
    </button>
  )
}

/**
 * Pricing page marquee announcing recent group-level ratio changes. Scrolls
 * horizontally in a seamless loop (duplicated track), pauses on hover, and
 * degrades to a static scrollable row when the content fits or the viewer
 * prefers reduced motion.
 */
export function GroupChangeBanner({
  changes,
  onItemClick,
  className,
}: GroupChangeBannerProps) {
  const reduceMotion = useMediaQuery('(prefers-reduced-motion: reduce)')
  const viewportRef = useRef<HTMLDivElement>(null)
  const copyRef = useRef<HTMLDivElement>(null)
  const [contentWidth, setContentWidth] = useState(0)
  const [viewportWidth, setViewportWidth] = useState(0)

  useEffect(() => {
    const viewport = viewportRef.current
    const copy = copyRef.current
    if (!viewport || !copy) return
    const measure = () => {
      setViewportWidth(viewport.clientWidth)
      setContentWidth(copy.scrollWidth)
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(viewport)
    observer.observe(copy)
    return () => observer.disconnect()
  }, [changes])

  if (changes.length === 0) return null

  const scrolling = contentWidth > viewportWidth && !reduceMotion
  // The track holds two copies, so one full cycle travels exactly one copy width
  const duration = Math.max(
    MARQUEE_MIN_DURATION_SECONDS,
    Math.round(contentWidth / MARQUEE_PIXELS_PER_SECOND)
  )

  return (
    <div
      className={cn(
        'marquee-container rounded-lg border border-amber-300/60 bg-amber-50 px-3 py-2.5 text-sm text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200',
        className
      )}
    >
      <div className='flex items-center gap-2'>
        <Megaphone className='size-4 shrink-0' />
        <div
          ref={viewportRef}
          className={cn(
            'min-w-0 flex-1',
            scrolling ? 'marquee-mask overflow-hidden' : 'overflow-x-auto'
          )}
        >
          <div
            className={cn('flex w-max', scrolling && 'animate-marquee-left')}
            style={
              scrolling
                ? ({ '--marquee-duration': `${duration}s` } as CSSProperties)
                : undefined
            }
          >
            <div ref={copyRef} className='flex w-max items-center'>
              {changes.map((change) => (
                <GroupChangeItem
                  key={changeKey(change)}
                  change={change}
                  onClick={onItemClick}
                />
              ))}
            </div>
            {scrolling && (
              <div aria-hidden className='flex w-max items-center'>
                {changes.map((change) => (
                  <GroupChangeItem
                    key={changeKey(change)}
                    change={change}
                    onClick={onItemClick}
                    duplicate
                  />
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

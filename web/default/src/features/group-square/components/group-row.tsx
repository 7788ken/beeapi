import { Fragment } from 'react'
import { useNavigate } from '@tanstack/react-router'
import {
  ArrowDown,
  ArrowRight,
  ArrowUp,
  Boxes,
  CircleSlash,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { UptimeSparkline } from '@/components/uptime-sparkline'
import type { GroupEntry } from '../hooks/use-group-directory'
import { OTHER_PLATFORM, PLATFORMS, renderPlatformIcon } from '../lib/classify'
import {
  highlightSegments,
  type GroupChange,
  type GroupSortKey,
} from '../lib/directory'
import { secondaryButtonClass } from './styles'

/** 桌面端各行共用的列宽：行各自是一个 grid，列宽必须写死才能上下对齐 */
const GROUP_ROW_GRID =
  '@3xl:grid-cols-[minmax(0,1fr)_11rem_6rem_7.5rem] @3xl:gap-x-5'

function formatRatio(ratio: number): string {
  if (!Number.isFinite(ratio)) return '—'
  return `×${Number(ratio.toFixed(3))}`
}

function formatDay(unixSec: number): string {
  const d = new Date(unixSec * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function Highlight(props: { text: string; tokens: readonly string[] }) {
  if (props.tokens.length === 0) return props.text
  return highlightSegments(props.text, props.tokens).map((seg, i) =>
    seg.hit ? (
      <mark
        key={i}
        className='bg-primary/15 text-foreground rounded-[3px] px-px'
      >
        {seg.text}
      </mark>
    ) : (
      <Fragment key={i}>{seg.text}</Fragment>
    )
  )
}

function PlatformTile(props: { platform: string; dimmed: boolean }) {
  const { t } = useTranslation()
  const platform = PLATFORMS.find((p) => p.key === props.platform)
  const label = t(platform?.labelKey ?? OTHER_PLATFORM.labelKey)
  return (
    <span
      className={cn(
        'bg-background text-foreground flex size-9 shrink-0 items-center justify-center rounded-lg border',
        props.dimmed && 'opacity-60'
      )}
      title={label}
    >
      {platform ? (
        renderPlatformIcon(platform)
      ) : (
        <Boxes className='text-muted-foreground size-4' aria-hidden />
      )}
      <span className='sr-only'>{label}</span>
    </span>
  )
}

function NewBadge(props: { change: GroupChange }) {
  const { t } = useTranslation()
  const { kind, from, to } = props.change
  const detail =
    kind === 'added'
      ? t('New group')
      : kind === 'dedicated'
        ? t('Dedicated ratio {{ratio}}', { ratio: formatRatio(to) })
        : t('Ratio {{from}} → {{to}}', {
            from: formatRatio(from),
            to: formatRatio(to),
          })
  const text = `${detail} · ${formatDay(props.change.at)}`
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          tabIndex={0}
          className='bg-primary text-primary-foreground inline-flex h-4 shrink-0 cursor-default items-center rounded px-1 text-[10px] leading-none font-semibold focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none'
        >
          NEW
          <span className='sr-only'>
            {t('Recently changed')}: {text}
          </span>
        </span>
      </TooltipTrigger>
      <TooltipContent>{text}</TooltipContent>
    </Tooltip>
  )
}

function AvailabilityCell(props: { entry: GroupEntry; pending: boolean }) {
  const { t } = useTranslation()
  const { status } = props.entry.availability

  if (status === 'unknown') {
    return props.pending ? (
      <Skeleton className='h-3.5 w-28' />
    ) : (
      <span className='text-muted-foreground text-sm'>—</span>
    )
  }
  if (status === 'no_supply') {
    return (
      <span className='text-muted-foreground inline-flex items-center gap-1.5 text-xs'>
        <CircleSlash className='size-3.5 shrink-0' aria-hidden />
        {t('No enabled channels')}
      </span>
    )
  }
  const idleText = (
    <span className='text-muted-foreground text-xs'>
      {t('No requests in the last hour')}
    </span>
  )
  if (status === 'idle' && props.entry.series.length === 0) return idleText
  if (status === 'idle') {
    // 柱子是图形，压暗不影响可读；尾部数字必然是「暂无数据」，由下方原因文字代替
    return (
      <div className='flex min-w-0 flex-col gap-1'>
        <UptimeSparkline
          series={props.entry.series}
          size='sm'
          showLatest={false}
          className='opacity-60'
        />
        {idleText}
      </div>
    )
  }
  return (
    <UptimeSparkline
      series={props.entry.series}
      size='sm'
      emptyLabel={t('No data')}
    />
  )
}

export function GroupRow(props: {
  entry: GroupEntry
  tokens: readonly string[]
  availabilityPending: boolean
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { entry } = props
  const dimmed =
    entry.availability.status === 'idle' ||
    entry.availability.status === 'no_supply'

  return (
    <li
      className={cn(
        'group/row hover:bg-foreground/[0.025] grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-2 px-4 py-3.5 transition-colors duration-[300ms] ease-out motion-reduce:transition-none @3xl:items-center @3xl:px-5',
        GROUP_ROW_GRID
      )}
    >
      <div className='col-start-1 row-start-1 flex min-w-0 items-start gap-3'>
        <PlatformTile platform={entry.platform} dimmed={dimmed} />
        <div className='min-w-0 flex-1'>
          <div className='flex min-w-0 items-center gap-2'>
            <h3
              className={cn(
                'truncate text-[15px] leading-6 font-semibold',
                dimmed ? 'text-muted-foreground' : 'text-foreground'
              )}
              title={entry.name}
            >
              <Highlight text={entry.name} tokens={props.tokens} />
            </h3>
            {entry.change && <NewBadge change={entry.change} />}
          </div>
          <p
            className='text-muted-foreground mt-0.5 line-clamp-2 text-sm leading-5 break-words'
            title={entry.desc || undefined}
          >
            {entry.desc ? (
              <Highlight text={entry.desc} tokens={props.tokens} />
            ) : (
              t('No description')
            )}
          </p>
        </div>
      </div>

      <div className='col-start-2 row-start-1 self-start @3xl:col-start-4 @3xl:self-center @3xl:justify-self-end'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() =>
            void navigate({ to: '/keys', search: { group: entry.name } })
          }
          // 读屏名称要包含可见文字（WCAG 2.5.3）并带上分组名，否则每一行都叫同一个名字
          aria-label={`${t('Create key')} · ${entry.name}`}
          className={cn(
            secondaryButtonClass,
            // 悬停整行（或按钮本身）时变实心紫，同一时刻只有一个紫按钮。outline 变体自带的 dark:hover 优先级更高，
            // 必须同名覆盖；阴影与 dash-emphasis 的 dashPrimaryShadow 同值，类名要写全 Tailwind 才扫得到
            'hover:border-primary hover:bg-primary hover:text-primary-foreground dark:hover:bg-primary',
            'group-hover/row:border-primary group-hover/row:bg-primary group-hover/row:text-primary-foreground dark:group-hover/row:border-primary group-hover/row:shadow-[0_2px_5px_rgba(99,91,255,0.4),inset_0_1px_0_rgba(255,255,255,0.2)]'
          )}
        >
          <span className='hidden @md:inline'>{t('Create key')}</span>
          <ArrowRight className='size-4' />
        </Button>
      </div>

      {/* 窄屏：可用率与倍率并成一行，容器够宽（@md）才缩进到文字下，放不下就折行；
          @3xl（容器 ≥768px）起 contents 让两格直接进行内 grid 的列 */}
      <div className='col-span-2 row-start-2 flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1 @md:ps-12 @3xl:contents'>
        <div className='min-w-0 @3xl:col-start-2 @3xl:row-start-1'>
          <AvailabilityCell entry={entry} pending={props.availabilityPending} />
        </div>
        <div
          className={cn(
            'flex items-baseline gap-1 whitespace-nowrap @3xl:col-start-3 @3xl:row-start-1 @3xl:justify-end',
            dimmed ? 'text-muted-foreground' : 'text-foreground'
          )}
        >
          <span className='text-muted-foreground text-xs @3xl:hidden'>
            {t('Ratio')}
          </span>
          <span className='font-mono text-base font-semibold tabular-nums'>
            {formatRatio(entry.ratio)}
          </span>
        </div>
      </div>
    </li>
  )
}

function SortHeader(props: {
  label: string
  active: boolean
  direction: 'asc' | 'desc'
  onClick: () => void
  className?: string
}) {
  const Arrow = props.direction === 'asc' ? ArrowUp : ArrowDown
  return (
    <button
      type='button'
      onClick={props.onClick}
      aria-pressed={props.active}
      className={cn(
        'hover:text-foreground inline-flex items-center gap-1 rounded-sm text-start transition-all duration-[300ms] ease-out focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none active:scale-[0.98] motion-reduce:transition-none motion-reduce:active:scale-100',
        props.active && 'text-primary hover:text-primary',
        props.className
      )}
    >
      {props.label}
      <Arrow
        className={cn('size-3.5 shrink-0', !props.active && 'opacity-0')}
        aria-hidden
      />
    </button>
  )
}

/** 桌面端列头：可用率、倍率可点排序，当前排序列变紫并带方向箭头 */
export function GroupListHeader(props: {
  sort: GroupSortKey
  onSortChange: (value: GroupSortKey) => void
}) {
  const { t } = useTranslation()
  const ratioActive = props.sort === 'ratio_asc' || props.sort === 'ratio_desc'
  return (
    <div
      className={cn(
        'text-muted-foreground hidden border-b px-5 py-2.5 text-xs font-medium @3xl:grid @3xl:items-center',
        GROUP_ROW_GRID
      )}
    >
      <div>{t('Group')}</div>
      <SortHeader
        label={t('Availability (last 24h)')}
        active={props.sort === 'availability'}
        direction='desc'
        onClick={() => props.onSortChange('availability')}
      />
      <SortHeader
        label={t('Ratio')}
        active={ratioActive}
        direction={props.sort === 'ratio_desc' ? 'desc' : 'asc'}
        onClick={() =>
          props.onSortChange(
            props.sort === 'ratio_asc' ? 'ratio_desc' : 'ratio_asc'
          )
        }
        className='justify-self-end'
      />
      <div aria-hidden />
    </div>
  )
}

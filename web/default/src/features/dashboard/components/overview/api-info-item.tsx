import {
  Check,
  CircleAlert,
  Copy,
  Ellipsis,
  ExternalLink,
  Gauge,
  LoaderCircle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  openExternalSpeedTest,
  urlOrigin,
} from '@/features/dashboard/lib/api-info'
import type { ApiInfoItem, PingStatus } from '@/features/dashboard/types'
import { dashRankTone } from './dash-emphasis'
import { dashSecondaryBtn } from './dash-style'

interface ApiInfoItemProps {
  item: ApiInfoItem
  status: PingStatus
  fastest: boolean
}

/** 深色地址条本身就是复制按钮，一行只有这一个主操作。 */
const addressBtn =
  'inline-flex h-10 max-w-full min-w-0 items-center gap-3 justify-self-start rounded-lg bg-[#0a2540] ps-3 pe-2.5 font-mono text-sm text-white shadow-[0_1px_2px_rgba(10,37,64,0.2),inset_0_1px_0_rgba(255,255,255,0.12)] transition-all duration-[300ms] ease-out hover:-translate-y-0.5 hover:bg-[#10345a] focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none active:translate-y-0 active:scale-[0.98] active:shadow-[inset_0_2px_4px_rgba(0,0,0,0.35)] motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100 dark:bg-[#06182c] dark:hover:bg-[#0a2540]'

function LatencyValue(props: {
  status: PingStatus
  fastest: boolean
  className?: string
}) {
  const { t } = useTranslation()
  const status = props.status

  // 没测过时整列留空：没有表头，一列“—”读不出含义
  let content = <span className='sr-only'>{t('Not tested yet')}</span>
  if (status.testing) {
    content = (
      <span className='text-muted-foreground inline-flex items-center gap-1.5 text-xs'>
        <LoaderCircle
          className='size-3.5 animate-spin motion-reduce:animate-none'
          aria-hidden
        />
        {t('Testing...')}
      </span>
    )
  } else if (status.error) {
    content = (
      <span className='text-foreground inline-flex items-center gap-1.5 text-xs font-semibold'>
        <CircleAlert className='size-3.5' aria-hidden />
        {t('Unreachable')}
      </span>
    )
  } else if (status.latency !== null) {
    content = (
      <span className='inline-flex items-center gap-2'>
        {props.fastest ? (
          <span
            className={cn(
              'inline-flex h-5 items-center rounded-md px-1.5 text-xs font-semibold',
              dashRankTone(1)
            )}
          >
            {t('Fastest')}
          </span>
        ) : null}
        <span className='text-foreground text-sm font-medium tabular-nums'>
          {t('{{latency}} ms', { latency: status.latency })}
        </span>
      </span>
    )
  }

  return (
    <div className={cn('flex min-w-0 justify-end', props.className)}>
      {content}
    </div>
  )
}

function RowMenu(props: { url: string; className?: string }) {
  const { t } = useTranslation()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type='button'
          aria-label={t('More actions')}
          className={cn(dashSecondaryBtn, 'w-10 px-0', props.className)}
        >
          <Ellipsis className='size-4' aria-hidden />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end' className='min-w-56'>
        <DropdownMenuItem asChild>
          <a href={props.url} target='_blank' rel='noreferrer'>
            <ExternalLink />
            {t('Open in new tab')}
          </a>
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => openExternalSpeedTest(props.url)}>
          <Gauge />
          {t('External Speed Test')}
          <DropdownMenuShortcut className='tracking-normal'>
            tcptest.cn
          </DropdownMenuShortcut>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * 窄容器两行：线路名 + 延迟 + 菜单 / 地址条；容器够宽（@3xl）并成一行四列，
 * 各行列宽一致，线路名、地址、延迟上下对齐，方便横向比较。
 */
export function ApiInfoItemComponent(props: ApiInfoItemProps) {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })
  const item = props.item
  const copied = copiedText === item.url
  const origin = urlOrigin(item.url)
  // 高亮域名之后的路径：带不带 /v1 是最容易填错的地方
  const path = item.url.startsWith(origin) ? item.url.slice(origin.length) : ''
  const base = item.url.slice(0, item.url.length - path.length)
  // 手机宽度放不下完整地址，省掉协议头，免得结尾的 /v1 被截掉；复制的仍是完整地址
  const scheme = /^https?:\/\//.exec(base)?.[0] ?? ''

  return (
    <li className='hover:bg-foreground/[0.04] grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-x-3 gap-y-3 px-6 py-4 transition-colors duration-[300ms] ease-out motion-reduce:transition-none md:px-8 @3xl:grid-cols-[minmax(0,15rem)_minmax(0,1fr)_7rem_auto] @3xl:gap-x-6'>
      <div className='col-start-1 row-start-1 min-w-0'>
        <p className='text-foreground text-sm font-semibold text-pretty'>
          {item.route}
        </p>
        {item.description ? (
          <p className='text-muted-foreground mt-1 text-xs text-pretty'>
            {item.description}
          </p>
        ) : null}
      </div>
      <button
        type='button'
        title={item.url}
        onClick={() => {
          void copyToClipboard(item.url)
        }}
        className={cn(
          addressBtn,
          'col-span-3 row-start-2 @3xl:col-span-1 @3xl:col-start-2 @3xl:row-start-1'
        )}
      >
        <span className='min-w-0 truncate'>
          <span className='hidden @sm:inline'>{scheme}</span>
          {base.slice(scheme.length)}
          {path.length > 1 ? (
            <span className='text-[#b4afff]'>{path}</span>
          ) : (
            path
          )}
        </span>
        <span className='flex shrink-0 items-center gap-1.5 border-s border-white/15 ps-2.5 font-sans text-xs font-medium'>
          {copied ? (
            <Check className='size-3.5' aria-hidden />
          ) : (
            <Copy className='size-3.5' aria-hidden />
          )}
          {copied ? t('Copied') : t('Copy')}
        </span>
      </button>
      <span className='sr-only' aria-live='polite'>
        {copied ? t('Copied') : ''}
      </span>
      <LatencyValue
        status={props.status}
        fastest={props.fastest}
        className='col-start-2 row-start-1 @3xl:col-start-3'
      />
      <RowMenu
        url={item.url}
        className='col-start-3 row-start-1 @3xl:col-start-4'
      />
    </li>
  )
}

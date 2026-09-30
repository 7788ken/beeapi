import { useEffect, useState, useSyncExternalStore } from 'react'
import {
  Bell,
  Boxes,
  KeyRound,
  LayoutDashboard,
  Lock,
  ScrollText,
  Search,
  type LucideIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { useSystemConfig } from '@/hooks/use-system-config'
import { AnimateInView } from '@/components/animate-in-view'
import { Eyebrow, SectionHeading, homeAccent, homeMuted } from '../home-links'

type PreviewTab = 'dashboard' | 'tokens' | 'logs' | 'models'

const TABS: {
  id: PreviewTab
  label: string
  path: string
  icon: LucideIcon
}[] = [
  {
    id: 'dashboard',
    label: 'Dashboard',
    path: '/console/dashboard',
    icon: LayoutDashboard,
  },
  { id: 'tokens', label: 'Tokens', path: '/console/token', icon: KeyRound },
  { id: 'logs', label: 'Logs', path: '/console/log', icon: ScrollText },
  { id: 'models', label: 'Models', path: '/console/pricing', icon: Boxes },
]

const ROTATE_MS = 3500
const REDUCE_MOTION_QUERY = '(prefers-reduced-motion: reduce)'

function subscribeReduceMotion(onChange: () => void) {
  const mq = window.matchMedia(REDUCE_MOTION_QUERY)
  mq.addEventListener('change', onChange)
  return () => mq.removeEventListener('change', onChange)
}

/** 尊重系统的「减少动态效果」设置：匹配时不自动轮播 */
function usePrefersReducedMotion() {
  return useSyncExternalStore(
    subscribeReduceMotion,
    () => window.matchMedia(REDUCE_MOTION_QUERY).matches,
    () => false
  )
}

export function ConsolePreview() {
  const { t } = useTranslation()
  const { systemName } = useSystemConfig()
  const [tab, setTab] = useState<PreviewTab>('dashboard')
  const [hovered, setHovered] = useState(false)
  const [focused, setFocused] = useState(false)
  const reduceMotion = usePrefersReducedMotion()

  const paused = hovered || focused
  const autoPlaying = !reduceMotion && !paused

  useEffect(() => {
    if (!autoPlaying) return
    const timer = setInterval(() => {
      const current = TABS.findIndex((item) => item.id === tab)
      setTab(TABS[(current + 1) % TABS.length].id)
    }, ROTATE_MS)
    return () => clearInterval(timer)
  }, [autoPlaying, tab])

  const host = `${systemName.toLowerCase().replace(/[^a-z0-9]/g, '')}.com`
  const active = TABS.find((item) => item.id === tab) ?? TABS[0]

  return (
    <section id='home-console' className='scroll-mt-24 px-6 py-20 md:py-28'>
      <div className='mx-auto max-w-5xl'>
        <AnimateInView className='mx-auto mb-12 max-w-2xl text-center'>
          <Eyebrow>{t('Console preview')}</Eyebrow>
          <SectionHeading className='mt-4'>
            <span className='inline-block'>{t('One console,')}</span>{' '}
            <span className={`${homeAccent} inline-block`}>
              {t('every call in view')}
            </span>
          </SectionHeading>
          <p
            className={`mt-4 text-base leading-relaxed text-pretty ${homeMuted}`}
          >
            {t('Keys, usage, billing, and routing stay in one place.')}
          </p>
        </AnimateInView>

        <AnimateInView delay={80}>
          <div
            onMouseEnter={() => setHovered(true)}
            onMouseLeave={() => setHovered(false)}
            onFocusCapture={() => setFocused(true)}
            onBlurCapture={(event) => {
              if (!event.currentTarget.contains(event.relatedTarget)) {
                setFocused(false)
              }
            }}
            className='overflow-hidden rounded-xl bg-white shadow-[0_18px_50px_rgba(11,28,71,0.08)] ring-1 ring-black/6 dark:bg-[#111726] dark:ring-white/8'
          >
            {/* 浏览器外壳 */}
            <div className='flex items-center gap-3 border-b border-black/6 px-3 py-2.5 sm:px-4 dark:border-white/8'>
              <div className='flex shrink-0 gap-1.5' aria-hidden>
                <span className='size-2.5 rounded-full bg-[#ff5f57]' />
                <span className='size-2.5 rounded-full bg-[#febc2e]' />
                <span className='size-2.5 rounded-full bg-[#28c840]' />
              </div>
              <div className='mx-auto flex h-7 min-w-0 flex-1 items-center gap-1.5 rounded-full bg-black/4 px-3 ring-1 ring-black/6 sm:max-w-80 dark:bg-white/6 dark:ring-white/8'>
                <Lock className={`size-3 shrink-0 ${homeMuted}`} aria-hidden />
                <span className={`truncate font-mono text-[11px] ${homeMuted}`}>
                  {host}
                  {active.path}
                </span>
              </div>
              <div className='flex shrink-0 items-center gap-2.5' aria-hidden>
                <Search className={`size-3.5 ${homeMuted}`} />
                <Bell className={`size-3.5 ${homeMuted}`} />
              </div>
            </div>

            <div className='grid md:grid-cols-[12.5rem_1fr]'>
              {/* 侧边栏：窄屏改成顶部横向滚动 */}
              <div className='min-w-0 border-b border-black/6 p-2.5 md:border-r md:border-b-0 md:p-3 dark:border-white/8'>
                <div className='mb-3 hidden items-baseline gap-2 px-2 md:flex'>
                  <span className='truncate text-sm font-semibold text-[#0b1c47] dark:text-[#eef1f6]'>
                    {systemName}
                  </span>
                  <span className={`shrink-0 text-[11px] ${homeMuted}`}>
                    {t('Console')}
                  </span>
                </div>

                <div
                  role='tablist'
                  aria-label={t('Console preview')}
                  className='flex gap-1 overflow-x-auto md:flex-col md:overflow-x-visible'
                >
                  {TABS.map((item) => {
                    const selected = tab === item.id
                    const Icon = item.icon
                    return (
                      <button
                        key={item.id}
                        type='button'
                        role='tab'
                        id={`home-tab-${item.id}`}
                        aria-selected={selected}
                        aria-controls={`home-panel-${item.id}`}
                        onClick={() => setTab(item.id)}
                        className={cn(
                          'flex h-10 shrink-0 items-center gap-2 rounded-xl px-3 text-sm whitespace-nowrap transition-colors duration-200 ease-out focus-visible:shadow-[0_0_0_3px_rgba(11,28,71,0.24)] focus-visible:outline-none dark:focus-visible:shadow-[0_0_0_3px_rgba(238,241,246,0.24)]',
                          selected
                            ? 'bg-[#0b1c47] font-medium text-white dark:bg-[#eef1f6] dark:text-[#0b1c47]'
                            : 'text-[#5a6072] hover:bg-black/4 dark:text-[#9aa1b2] dark:hover:bg-white/6'
                        )}
                      >
                        <Icon className='size-4 shrink-0' aria-hidden />
                        {t(item.label)}
                      </button>
                    )
                  })}
                </div>
              </div>

              {/* 主区域 */}
              <div className='flex min-w-0 flex-col'>
                <div className='min-h-72 p-4 sm:p-5 md:p-6'>
                  {tab === 'dashboard' ? <DashboardPanel /> : null}
                  {tab === 'tokens' ? <TokensPanel /> : null}
                  {tab === 'logs' ? <LogsPanel /> : null}
                  {tab === 'models' ? <ModelsPanel /> : null}
                </div>

                <div className='mt-auto flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5 border-t border-black/6 px-4 py-2.5 sm:px-5 md:px-6 dark:border-white/8'>
                  <p className={`text-[11px] ${homeMuted}`}>
                    {t('Preview only. Live numbers appear after you sign in.')}
                  </p>
                  {reduceMotion ? null : (
                    <span
                      className={cn(
                        'inline-flex h-6 shrink-0 items-center gap-1.5 rounded-full bg-black/4 px-2.5 text-[11px] ring-1 ring-black/6 dark:bg-white/6 dark:ring-white/8',
                        homeMuted
                      )}
                    >
                      {paused ? (
                        t('Paused · move away to resume')
                      ) : (
                        <>
                          <span
                            className={cn(
                              'size-1.5 animate-pulse rounded-full bg-current motion-reduce:animate-none',
                              homeAccent
                            )}
                            aria-hidden
                          />
                          {t('Auto demo')}
                        </>
                      )}
                    </span>
                  )}
                </div>
              </div>
            </div>
          </div>
        </AnimateInView>
      </div>
    </section>
  )
}

function DashboardPanel() {
  const { t } = useTranslation()
  const cards = [
    { label: 'Requests today', value: '12,847', delta: '+12.4%', up: true },
    { label: 'Active keys', value: '6', delta: '+1', up: true },
    { label: 'Ready channels', value: '23', delta: '-2', up: false },
  ]

  return (
    <div
      role='tabpanel'
      id='home-panel-dashboard'
      aria-labelledby='home-tab-dashboard'
    >
      {/* 主卡比其余卡多一行余量条；两列排布时各行等高，第二行不会矮一截 */}
      <div className='grid auto-rows-fr grid-cols-2 gap-2.5 lg:grid-cols-4'>
        <BalanceCard />
        {cards.map((card) => (
          <div
            key={card.label}
            className='flex min-w-0 flex-col rounded-xl bg-[#f4f6fa] px-3 py-3 ring-1 ring-black/6 sm:px-4 dark:bg-[#0a0c12] dark:ring-white/8'
          >
            <p className={`truncate text-[11px] leading-4.5 ${homeMuted}`}>
              {t(card.label)}
            </p>
            <p className='mt-1.5 font-mono text-xl font-semibold tracking-tight text-[#0b1c47] tabular-nums sm:text-2xl dark:text-[#eef1f6]'>
              {card.value}
            </p>
            {/* 被主卡撑高后，底行贴底，和主卡的底行对齐 */}
            <p className={`mt-auto truncate pt-1 text-[11px] ${homeMuted}`}>
              {t('vs yesterday')}{' '}
              <span className={card.up ? homeAccent : homeMuted}>
                {card.delta}
              </span>
            </p>
          </div>
        ))}
      </div>

      <div className='mt-3 rounded-xl bg-[#f4f6fa] p-4 ring-1 ring-black/6 dark:bg-[#0a0c12] dark:ring-white/8'>
        <div className='flex items-baseline justify-between gap-3'>
          <p className={`text-[11px] ${homeMuted}`}>
            {t('Requests · last 7 days')}
          </p>
          <p className={`font-mono text-[11px] tabular-nums ${homeMuted}`}>
            68,204
          </p>
        </div>
        <svg
          viewBox='0 0 320 64'
          preserveAspectRatio='none'
          className={cn('mt-3 h-14 w-full', homeAccent)}
          aria-hidden
        >
          <polygon
            points='0,64 0,48 46,40 91,44 137,30 183,34 229,22 274,26 320,10 320,64'
            fill='currentColor'
            fillOpacity='0.1'
          />
          <polyline
            points='0,48 46,40 91,44 137,30 183,34 229,22 274,26 320,10'
            fill='none'
            stroke='currentColor'
            strokeWidth='2'
            strokeLinecap='round'
            strokeLinejoin='round'
            vectorEffect='non-scaling-stroke'
          />
        </svg>
      </div>
    </div>
  )
}

const BALANCE_SEGMENTS = 10
const BALANCE_FILLED = 8

/**
 * 主卡：与控制台概览页的余额卡同构 —— 实心底 + 投影和内高光、角上半透明圆、状态胶囊、分段余量条。
 * 配色按落地页反色卡：亮色深藏蓝底，暗色翻成近白底。一组卡片只有这一张。
 */
function BalanceCard() {
  const { t } = useTranslation()

  return (
    <div className='relative flex min-w-0 flex-col overflow-hidden rounded-xl bg-[#0b1c47] px-3 py-3 text-[#eef1f6] shadow-[0_10px_22px_-12px_rgba(11,28,71,0.65),inset_0_1px_0_rgba(255,255,255,0.14)] sm:px-4 dark:bg-[#eef1f6] dark:text-[#0b1c47] dark:shadow-[0_10px_22px_-12px_rgba(0,0,0,0.9)]'>
      <svg
        className='pointer-events-none absolute -end-7 -top-9 size-28'
        viewBox='0 0 160 160'
        aria-hidden
      >
        <circle
          cx='108'
          cy='52'
          r='58'
          fill='currentColor'
          fillOpacity='0.08'
        />
        <circle
          cx='124'
          cy='36'
          r='32'
          fill='currentColor'
          fillOpacity='0.12'
        />
        <circle cx='78' cy='18' r='16' fill='currentColor' fillOpacity='0.1' />
      </svg>
      <svg
        className='pointer-events-none absolute -start-5 -bottom-7 size-16'
        viewBox='0 0 96 96'
        aria-hidden
      >
        <circle cx='28' cy='70' r='36' fill='currentColor' fillOpacity='0.08' />
      </svg>

      {/* 内容整体定位，才能压在装饰圆上面 */}
      <div className='relative flex flex-1 flex-col'>
        <div className='flex items-center justify-between gap-2'>
          <p className='truncate text-[11px] leading-4.5'>{t('Balance')}</p>
          <span className='inline-flex h-4.5 shrink-0 items-center rounded-full bg-white/12 px-1.5 text-[10px] font-medium dark:bg-black/8'>
            {t('Healthy')}
          </span>
        </div>
        <p className='mt-1.5 font-mono text-xl font-semibold tracking-tight tabular-nums sm:text-2xl'>
          $1,286.50
        </p>
        <div className='mt-2 flex gap-0.5' aria-hidden>
          {Array.from({ length: BALANCE_SEGMENTS }, (_, index) => (
            <span
              key={index}
              className={cn(
                'h-1.5 flex-1 rounded-full',
                index < BALANCE_FILLED
                  ? 'bg-[#eef1f6] dark:bg-[#0b1c47]'
                  : 'bg-[#eef1f6]/25 dark:bg-[#0b1c47]/35'
              )}
            />
          ))}
        </div>
        <p className='mt-auto truncate pt-2 text-[11px]'>
          {t('Used today')}{' '}
          <span className='font-mono tabular-nums'>$18.20</span>
        </p>
      </div>
    </div>
  )
}

function TokensPanel() {
  const { t } = useTranslation()
  const rows: {
    name: string
    group: string
    quota: string | null
    enabled: boolean
  }[] = [
    {
      name: 'Production service',
      group: 'Default',
      quota: null,
      enabled: true,
    },
    {
      name: 'Data analytics',
      group: 'High speed',
      quota: '$240.00',
      enabled: true,
    },
    { name: 'Mobile client', group: 'Default', quota: '$80.00', enabled: true },
    { name: 'Test sandbox', group: 'Default', quota: '$0.00', enabled: false },
  ]

  return (
    <div
      role='tabpanel'
      id='home-panel-tokens'
      aria-labelledby='home-tab-tokens'
      className='overflow-x-auto'
    >
      <table className='w-full min-w-96 border-collapse text-left'>
        <thead>
          <tr className={`text-[11px] ${homeMuted}`}>
            <th scope='col' className='pb-2 font-normal'>
              {t('Name')}
            </th>
            <th scope='col' className='pb-2 font-normal'>
              {t('Group')}
            </th>
            <th scope='col' className='pb-2 text-right font-normal'>
              {t('Quota')}
            </th>
            <th scope='col' className='pb-2 text-right font-normal'>
              {t('Status')}
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={row.name}
              className='border-t border-black/6 dark:border-white/8'
            >
              <td className='py-2.5 text-sm text-[#0b1c47] dark:text-[#eef1f6]'>
                {t(row.name)}
              </td>
              <td className={`py-2.5 text-sm ${homeMuted}`}>{t(row.group)}</td>
              <td className='py-2.5 text-right font-mono text-sm text-[#0b1c47] tabular-nums dark:text-[#eef1f6]'>
                {row.quota ?? t('Unlimited')}
              </td>
              <td className='py-2.5 text-right'>
                <StatusBadge enabled={row.enabled} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function StatusBadge(props: { enabled: boolean }) {
  const { t } = useTranslation()

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 text-sm',
        props.enabled ? homeAccent : homeMuted
      )}
    >
      <span
        className={cn(
          'size-1.5 rounded-full bg-current',
          props.enabled ? '' : 'opacity-50'
        )}
        aria-hidden
      />
      {props.enabled ? t('Enabled') : t('Exhausted')}
    </span>
  )
}

function LogsPanel() {
  const { t } = useTranslation()
  const rows = [
    {
      time: '14:32:21',
      model: 'claude-opus-5-5',
      token: 'Production service',
      cost: '$0.0321',
      latency: '1.23s',
    },
    {
      time: '14:30:11',
      model: 'gpt-6-sol',
      token: 'Data analytics',
      cost: '$0.0012',
      latency: '0.56s',
    },
    {
      time: '14:22:09',
      model: 'deepseek-v4-pro',
      token: 'Test sandbox',
      cost: '$0.0030',
      latency: '0.41s',
    },
    {
      time: '14:18:33',
      model: 'gemini-3.8-flash',
      token: 'Production service',
      cost: '$0.0454',
      latency: '2.34s',
    },
  ]

  return (
    <div
      role='tabpanel'
      id='home-panel-logs'
      aria-labelledby='home-tab-logs'
      className='overflow-x-auto'
    >
      <table className='w-full min-w-[34rem] border-collapse text-left'>
        <thead>
          <tr className={`text-[11px] ${homeMuted}`}>
            <th scope='col' className='pb-2 font-normal'>
              {t('Time')}
            </th>
            <th scope='col' className='pb-2 font-normal'>
              {t('Model')}
            </th>
            <th scope='col' className='pb-2 font-normal'>
              {t('Token')}
            </th>
            <th scope='col' className='pb-2 text-right font-normal'>
              {t('Consume')}
            </th>
            <th scope='col' className='pb-2 text-right font-normal'>
              {t('Duration')}
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={row.time}
              className='border-t border-black/6 dark:border-white/8'
            >
              <td className={`py-2.5 font-mono text-sm ${homeMuted}`}>
                {row.time}
              </td>
              <td className='py-2.5 text-sm text-[#0b1c47] dark:text-[#eef1f6]'>
                {row.model}
              </td>
              <td className={`py-2.5 text-sm ${homeMuted}`}>{t(row.token)}</td>
              <td className='py-2.5 text-right font-mono text-sm text-[#0b1c47] tabular-nums dark:text-[#eef1f6]'>
                {row.cost}
              </td>
              <td
                className={`py-2.5 text-right font-mono text-sm ${homeMuted}`}
              >
                {row.latency}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ModelsPanel() {
  const { t } = useTranslation()
  // 各家官方标准输入价（每 1M tokens，2026-09 核对）；实际扣费由分组倍率决定。
  // gemini-3.8-flash 的 $0.75 是首发价，2027-01-01 起官方价为 $1.50。
  // 窄屏只显示前 8 个、lg 起排 4 列，使模型栏不高于数据看板，自动轮播时预览不跳动。
  const models = [
    { name: 'claude-opus-5-5', price: '$4.00/1M' },
    { name: 'gpt-6-astra', price: '$10.00/1M' },
    { name: 'gemini-3.8-flash', price: '$0.75/1M' },
    { name: 'deepseek-v4-pro', price: '$9.00/1M' },
    { name: 'claude-fable-5-1', price: '$10.00/1M' },
    { name: 'gpt-6-sol', price: '$2.00/1M' },
    { name: 'grok-4.7', price: '$2.00/1M' },
    { name: 'qwen3.8-max', price: '$12.00/1M' },
    { name: 'claude-sonnet-5', price: '$2.00/1M' },
    { name: 'glm-5.3', price: '$8.00/1M' },
    { name: 'kimi-k3', price: '$20.00/1M' },
    { name: 'MiniMax-M3', price: '$2.10/1M' },
  ]

  return (
    <div
      role='tabpanel'
      id='home-panel-models'
      aria-labelledby='home-tab-models'
      className='grid grid-cols-2 gap-2.5 sm:grid-cols-3 lg:grid-cols-4 max-sm:[&>:nth-child(n+9)]:hidden'
    >
      {models.map((model) => (
        <div
          key={model.name}
          className='rounded-xl bg-[#f4f6fa] px-3 py-3 ring-1 ring-black/6 dark:bg-[#0a0c12] dark:ring-white/8'
        >
          <p className='truncate font-mono text-[12px] font-medium text-[#0b1c47] lg:text-[13px] dark:text-[#eef1f6]'>
            {model.name}
          </p>
          <p className={`mt-1 truncate text-[11px] ${homeMuted}`}>
            {t('Input')} {model.price}
          </p>
        </div>
      ))}
    </div>
  )
}

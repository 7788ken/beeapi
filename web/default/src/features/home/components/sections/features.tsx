import { Code2, Receipt, ShieldCheck, Zap, type LucideIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { AnimateInView } from '@/components/animate-in-view'
import { Eyebrow, SectionHeading, homeAccent, homeMuted } from '../home-links'

interface FeatureCard {
  icon: LucideIcon
  title: string
  tags: string[]
  desc: string
  /** 反色卡：一组里留一张深色，避免四张白卡平铺 */
  invert?: boolean
  /** 宽卡跨两列，构成非对称的 bento 排布 */
  wide?: boolean
  art?: 'latency' | 'routes'
}

export function Features() {
  const { t } = useTranslation()

  const cards: FeatureCard[] = [
    {
      icon: Zap,
      title: t('Lightning Fast'),
      tags: [
        t('Edge entry'),
        t('Connection reuse'),
        t('Streaming passthrough'),
        t('Millisecond scheduling'),
      ],
      desc: t(
        'Optimized network architecture ensures millisecond response times'
      ),
      wide: true,
      art: 'latency',
    },
    {
      icon: ShieldCheck,
      title: t('Secure & Reliable'),
      tags: [
        t('Permissioned keys'),
        t('Quota limits'),
        t('Usage you can audit'),
        t('Key isolation'),
      ],
      desc: t(
        'Enterprise-grade security with comprehensive permission management'
      ),
      invert: true,
    },
    {
      icon: Receipt,
      title: t('Transparent Billing'),
      tags: [
        t('Pay as you go'),
        t('Live usage'),
        t('Exportable detail'),
        t('Auditable ledger'),
      ],
      desc: t('Pay-as-you-go with real-time usage monitoring'),
    },
    {
      icon: Code2,
      title: t('Developer Friendly'),
      tags: [
        t('One endpoint'),
        t('OpenAI compatible'),
        t('Hot standby upstreams'),
        t('Automatic retry'),
      ],
      desc: t('Compatible API routes for common AI application workflows'),
      wide: true,
      art: 'routes',
    },
  ]

  return (
    <section className='px-6 py-16 md:py-24'>
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='mb-10'>
          <Eyebrow>{t('Core capability')}</Eyebrow>
          <div className='mt-5 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between'>
            <SectionHeading className='max-w-xl'>
              <span className='inline-block'>{t('Four jobs,')}</span>{' '}
              <span className={`${homeAccent} inline-block`}>
                {t('wired once')}
              </span>
            </SectionHeading>
            <p
              className={`max-w-xs text-sm leading-relaxed text-pretty ${homeMuted}`}
            >
              {t('Stable · Controllable · Secure')}
            </p>
          </div>
        </AnimateInView>

        <div className='grid gap-4 md:grid-cols-3'>
          {cards.map((card, index) => (
            <FeatureTile key={card.title} card={card} delay={index * 70} />
          ))}
        </div>
      </div>
    </section>
  )
}

function FeatureTile(props: { card: FeatureCard; delay: number }) {
  const { card } = props
  const Icon = card.icon
  const art =
    card.art === 'latency' ? (
      <LatencyArt />
    ) : card.art === 'routes' ? (
      <RoutesArt />
    ) : null

  return (
    <AnimateInView
      delay={props.delay}
      className={cn(
        'rounded-xl p-7 md:p-8',
        card.wide && 'md:col-span-2',
        card.invert
          ? 'bg-[#0b1c47] text-[#eef1f6] dark:bg-[#eef1f6] dark:text-[#0b1c47]'
          : 'bg-white ring-1 ring-black/6 dark:bg-[#111726] dark:ring-white/8'
      )}
    >
      {/* 宽卡把配图挪到右列，窄卡只有文字 */}
      <div
        className={cn(
          'flex h-full flex-col',
          card.wide &&
            'md:grid md:grid-cols-[minmax(0,1fr)_minmax(0,0.85fr)] md:items-center md:gap-8'
        )}
      >
        <div className='flex min-w-0 flex-col'>
          <span
            className={cn(
              'inline-flex size-9 shrink-0 items-center justify-center rounded-[10px]',
              card.invert
                ? 'bg-white/12 text-[#eef1f6] dark:bg-black/8 dark:text-[#0b1c47]'
                : 'bg-[#0b1c47] text-white dark:bg-[#eef1f6] dark:text-[#0b1c47]'
            )}
          >
            <Icon className='size-[18px]' aria-hidden />
          </span>

          <h3
            className={cn(
              'mt-5 text-xl font-bold tracking-[-0.02em]',
              !card.invert && 'text-[#0b1c47] dark:text-[#eef1f6]'
            )}
          >
            {card.title}
          </h3>

          <ul className='mt-4 flex flex-wrap gap-1.5'>
            {card.tags.map((tag) => (
              <li
                key={tag}
                className={cn(
                  'inline-flex h-7 items-center rounded-full px-2.5 text-[11px]',
                  card.invert
                    ? 'bg-white/10 text-[#eef1f6]/85 dark:bg-black/8 dark:text-[#0b1c47]/75'
                    : 'bg-black/4 text-[#5a6072] dark:bg-white/8 dark:text-[#9aa1b2]'
                )}
              >
                {tag}
              </li>
            ))}
          </ul>

          <p
            className={cn(
              'mt-5 max-w-md text-sm leading-relaxed text-pretty',
              card.invert
                ? 'text-[#eef1f6]/75 dark:text-[#0b1c47]/70'
                : 'text-[#5a6072] dark:text-[#9aa1b2]'
            )}
          >
            {card.desc}
          </p>
        </div>

        {art ? <div className='mt-8 md:mt-0'>{art}</div> : null}
      </div>
    </AnimateInView>
  )
}

/** 延迟走势的意象图，不是真实数据 */
function LatencyArt() {
  return (
    <svg
      viewBox='0 0 320 120'
      className='h-28 w-full text-[#1b55e2] dark:text-[#78a0f0]'
      fill='none'
      aria-hidden
    >
      <polyline
        points='4,92 32,86 64,95 96,74 128,80 160,30 176,100 208,58 240,63 272,44 304,50 316,46'
        stroke='currentColor'
        strokeWidth='2'
        strokeLinecap='round'
        strokeLinejoin='round'
      />
    </svg>
  )
}

/** 一个端点扇出到多个上游 */
function RoutesArt() {
  const targets = [14, 48, 82, 116]

  return (
    <svg
      viewBox='0 0 320 130'
      className='h-28 w-full text-[#1b55e2] dark:text-[#78a0f0]'
      fill='none'
      aria-hidden
    >
      {targets.map((y) => (
        <path
          key={y}
          d={`M22 65 C 150 65, 190 ${y}, 298 ${y}`}
          stroke='currentColor'
          strokeOpacity='0.45'
          strokeWidth='1.5'
        />
      ))}
      <circle cx='22' cy='65' r='7' fill='currentColor' />
      {targets.map((y) => (
        <circle key={y} cx='298' cy={y} r='3.5' fill='currentColor' />
      ))}
    </svg>
  )
}

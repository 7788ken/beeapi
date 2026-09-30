import { Link } from '@tanstack/react-router'
import { ArrowUpRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { AnimateInView } from '@/components/animate-in-view'
import {
  Eyebrow,
  SectionAside,
  SectionHeading,
  homeAccent,
  homeMuted,
} from '../home-links'

export function Solutions() {
  const { t } = useTranslation()

  const cards = [
    {
      num: '01',
      title: t('Private deployment'),
      desc: t(
        'Data never leaves your boundary, so the compliance review passes in one go.'
      ),
      audience: t('Finance · Government · Healthcare'),
      badge: t('Core plan'),
      inverted: true,
    },
    {
      num: '02',
      title: t('Smart fallback routing'),
      desc: t('Lower price and higher availability. We absorb the difference.'),
      audience: t('AI coding · Long-running agents'),
    },
    {
      num: '03',
      title: t('Async generation'),
      desc: t(
        'Batch image and video jobs keep their results even when the connection drops.'
      ),
      audience: t('Comics · E-commerce imagery · Short video'),
    },
    {
      num: '04',
      title: t('Cost-first tier'),
      desc: t(
        'Squeeze the price on non-critical load so the whole team can afford it.'
      ),
      audience: t('Internal tools · Daily operations'),
    },
  ]

  return (
    <section className='px-6 py-16 md:py-24'>
      <div className='mx-auto max-w-6xl'>
        <div className='mb-10 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between'>
          <AnimateInView className='max-w-xl'>
            <Eyebrow>{t('Solutions')}</Eyebrow>
            <SectionHeading className='mt-5'>
              <span className='inline-block'>{t('Four scenarios,')}</span>{' '}
              <span className={`${homeAccent} inline-block`}>
                {t('four engineered answers')}
              </span>
            </SectionHeading>
          </AnimateInView>

          <AnimateInView delay={80} className='shrink-0 sm:pb-2'>
            <SectionAside to='/pricing'>
              {t('See all plans')}
              <ArrowUpRight className='size-4' aria-hidden />
            </SectionAside>
          </AnimateInView>
        </div>

        <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-4'>
          {cards.map((card, index) => (
            <SolutionCard
              key={card.num}
              delay={index * 60}
              num={card.num}
              title={card.title}
              desc={card.desc}
              audience={card.audience}
              badge={card.badge}
              inverted={card.inverted}
            />
          ))}
        </div>
      </div>
    </section>
  )
}

function SolutionCard(props: {
  num: string
  title: string
  desc: string
  audience: string
  badge?: string
  inverted?: boolean
  delay?: number
}) {
  const { t } = useTranslation()
  const inverted = props.inverted === true

  const muted = inverted
    ? 'text-[#eef1f6]/70 dark:text-[#0b1c47]/65'
    : homeMuted

  return (
    <AnimateInView
      delay={props.delay}
      className={cn(
        'flex flex-col rounded-xl p-6',
        inverted
          ? 'bg-[#0b1c47] text-[#eef1f6] dark:bg-[#eef1f6] dark:text-[#0b1c47]'
          : 'bg-white text-[#0b1c47] ring-1 ring-black/6 dark:bg-[#111726] dark:text-[#eef1f6] dark:ring-white/8'
      )}
    >
      <div className='flex items-center justify-between gap-3'>
        <span className={cn('text-xs font-medium tracking-[0.18em]', muted)}>
          {props.num}
        </span>
        {props.badge ? (
          <span className='inline-flex h-6 shrink-0 items-center rounded-full bg-white/12 px-2.5 text-[11px] font-medium dark:bg-black/8'>
            {props.badge}
          </span>
        ) : null}
      </div>

      <h3 className='mt-4 text-lg font-bold text-pretty'>{props.title}</h3>

      <p className={cn('mt-3 text-sm leading-relaxed text-pretty', muted)}>
        {props.desc}
      </p>

      <p
        className={cn(
          'mt-5 border-t pt-4 text-xs leading-relaxed text-pretty',
          muted,
          inverted
            ? 'border-white/12 dark:border-black/10'
            : 'border-black/6 dark:border-white/8'
        )}
      >
        {props.audience}
      </p>

      <Link
        to='/pricing'
        className={cn(
          'mt-auto inline-flex items-center gap-1 pt-6 text-sm font-medium transition-opacity duration-200 ease-out hover:opacity-70 focus-visible:outline-none motion-reduce:transition-none',
          inverted ? undefined : homeAccent
        )}
      >
        {t('View the plan')}
        <ArrowUpRight className='size-4' aria-hidden />
      </Link>
    </AnimateInView>
  )
}

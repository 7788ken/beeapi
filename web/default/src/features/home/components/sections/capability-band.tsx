import type { CSSProperties } from 'react'
import { useTranslation } from 'react-i18next'
import { AnimateInView } from '@/components/animate-in-view'
import { Eyebrow, SectionHeading, homeAccent, homeMuted } from '../home-links'
import { Counter } from './stats'

const MODELS = [
  'OpenAI',
  'Claude',
  'Gemini',
  'DeepSeek',
  'Qwen',
  'Llama',
  'Mistral',
  'Groq',
]

export function CapabilityBand() {
  const { t } = useTranslation()

  const stats = [
    {
      end: 50,
      suffix: '+',
      label: t('upstream services integrated'),
      note: t('One adapter per vendor, kept current.'),
    },
    {
      end: 100,
      suffix: '+',
      label: t('model billing support'),
      note: t('Per-model rates, settled to the token.'),
    },
    {
      end: 50,
      suffix: '+',
      label: t('compatible API routes'),
      note: t('Chat, responses, images, audio, video.'),
    },
    {
      end: 10,
      suffix: '+',
      label: t('scheduling controls'),
      note: t('Priority, weight, retry, rate limit.'),
    },
  ]

  return (
    <section className='px-6 pt-16 pb-10 md:pt-24 md:pb-14'>
      <div className='mx-auto max-w-6xl'>
        <AnimateInView>
          <Eyebrow>{t('Capabilities')}</Eyebrow>
          <div className='mt-5 grid items-end gap-6 md:grid-cols-[1.4fr_0.8fr]'>
            <SectionHeading>
              <span className='inline-block'>
                {t('The work a gateway should do,')}
              </span>{' '}
              <span className={`${homeAccent} inline-block`}>
                {t('kept in one place')}
              </span>
            </SectionHeading>
            <p
              className={`max-w-sm text-sm leading-relaxed text-pretty md:justify-self-end ${homeMuted}`}
            >
              {t(
                'Upstreams, billing, routes, and scheduling live in the same console.'
              )}
            </p>
          </div>
        </AnimateInView>

        <div className='mt-14 grid grid-cols-2 border-t border-black/8 md:grid-cols-4 dark:border-white/10'>
          {stats.map((item, index) => (
            <AnimateInView
              key={item.label}
              delay={index * 70}
              className={`px-0 py-8 md:px-7 md:py-10 ${
                index % 2 === 1 ? 'pl-6 md:pl-7' : 'pr-6 md:pr-7'
              } ${
                index > 0
                  ? 'md:border-l md:border-black/8 md:dark:border-white/10'
                  : ''
              } ${
                index === 1
                  ? 'border-l border-black/8 dark:border-white/10'
                  : ''
              } ${
                index === 3
                  ? 'border-l border-black/8 dark:border-white/10'
                  : ''
              }`}
            >
              <p className='home-display text-[clamp(2.5rem,5.5vw,4rem)] leading-none font-extrabold tracking-[-0.04em] text-[#0b1c47] tabular-nums dark:text-[#eef1f6]'>
                <Counter end={item.end} suffix={item.suffix} />
              </p>
              <p className='mt-3 text-sm font-medium text-[#0b1c47] dark:text-[#eef1f6]'>
                {item.label}
              </p>
              <p className={`mt-1.5 text-xs leading-relaxed ${homeMuted}`}>
                {item.note}
              </p>
            </AnimateInView>
          ))}
        </div>
      </div>

      <div className='marquee-mask mt-16 overflow-hidden'>
        <div
          className='animate-marquee-left flex w-max'
          style={{ '--marquee-duration': '42s' } as CSSProperties}
        >
          {[0, 1].map((copy) => (
            <div key={copy} className='flex items-center'>
              {MODELS.map((name) => (
                <span
                  key={`${copy}-${name}`}
                  className='home-display flex items-center px-8 text-2xl font-bold tracking-[-0.02em] text-[#0b1c47] md:text-[2rem] dark:text-[#eef1f6]'
                >
                  {name}
                  <span
                    aria-hidden
                    className='ml-8 h-7 w-px bg-black/12 dark:bg-white/15'
                  />
                </span>
              ))}
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}

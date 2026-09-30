import { ArrowUpRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { AnimateInView } from '@/components/animate-in-view'
import { Eyebrow, SectionAside, SectionHeading, homeMuted } from '../home-links'

export function Team() {
  const { t } = useTranslation()

  return (
    <section className='px-6 py-16 md:py-24'>
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='grid items-center gap-8 rounded-xl bg-white p-6 ring-1 ring-black/6 md:grid-cols-[0.8fr_1fr] md:p-10 dark:bg-[#111726] dark:ring-white/8'>
          <TeamGlyph />

          <div>
            <Eyebrow>{t('Team')}</Eyebrow>
            <SectionHeading className='mt-5'>
              {t('The people who build the infrastructure')}
            </SectionHeading>
            <p
              className={`mt-4 max-w-md text-sm leading-relaxed text-pretty md:text-base ${homeMuted}`}
            >
              {t(
                'A small, engineering-led team that builds and keeps this model supply network running.'
              )}
            </p>
            <div className='mt-6'>
              <SectionAside to='/about'>
                {t('About us')}
                <ArrowUpRight className='size-4' aria-hidden />
              </SectionAside>
            </div>
          </div>
        </AnimateInView>
      </div>
    </section>
  )
}

/** 抽象几何占位：同心圆 + 节点连线，纯 SVG，跟随 currentColor 适配亮暗 */
function TeamGlyph() {
  const nodes = [
    { cx: 238, cy: 84 },
    { cx: 124, cy: 150 },
    { cx: 290, cy: 213 },
    { cx: 110, cy: 87 },
    { cx: 200, cy: 192 },
    { cx: 342, cy: 175 },
  ]

  return (
    <div className='relative aspect-[4/3] w-full overflow-hidden rounded-xl bg-gradient-to-br from-black/6 via-black/2 to-transparent text-[#0b1c47] ring-1 ring-black/6 dark:from-white/10 dark:via-white/4 dark:to-transparent dark:text-[#eef1f6] dark:ring-white/8'>
      <svg
        viewBox='0 0 400 300'
        preserveAspectRatio='xMidYMid meet'
        className='size-full'
        aria-hidden
        focusable='false'
      >
        <defs>
          <radialGradient id='home-team-glow' cx='50%' cy='50%' r='55%'>
            <stop offset='0%' stopColor='currentColor' stopOpacity='0.1' />
            <stop offset='100%' stopColor='currentColor' stopOpacity='0' />
          </radialGradient>
        </defs>

        <rect width='400' height='300' fill='url(#home-team-glow)' />

        <g fill='none' stroke='currentColor' strokeWidth='1'>
          <circle cx='200' cy='150' r='42' strokeOpacity='0.22' />
          <circle cx='200' cy='150' r='76' strokeOpacity='0.16' />
          <circle cx='200' cy='150' r='110' strokeOpacity='0.11' />
          <circle cx='200' cy='150' r='144' strokeOpacity='0.07' />
          <path
            d='M 200 40 A 110 110 0 0 1 310 150'
            strokeWidth='1.5'
            strokeOpacity='0.38'
            strokeLinecap='round'
          />
        </g>

        <g
          stroke='currentColor'
          strokeWidth='1'
          strokeOpacity='0.2'
          strokeLinecap='round'
        >
          {nodes.map((node) => (
            <line
              key={`${node.cx}-${node.cy}`}
              x1='200'
              y1='150'
              x2={node.cx}
              y2={node.cy}
            />
          ))}
        </g>

        <g fill='currentColor'>
          {nodes.map((node) => (
            <circle
              key={`${node.cx}-${node.cy}`}
              cx={node.cx}
              cy={node.cy}
              r='3.5'
              fillOpacity='0.45'
            />
          ))}
          <circle cx='200' cy='150' r='6' fillOpacity='0.7' />
        </g>
      </svg>
    </div>
  )
}

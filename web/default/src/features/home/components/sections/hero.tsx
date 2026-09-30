import { ArrowUpRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useSystemConfig } from '@/hooks/use-system-config'
import {
  HomePrimaryLink,
  HomeSecondaryLink,
  homeAccent,
  homeMuted,
} from '../home-links'
import { ParticleField } from '../particle-field'

interface HeroProps {
  className?: string
  isAuthenticated?: boolean
}

export function Hero(props: HeroProps) {
  const { t } = useTranslation()
  const { systemName } = useSystemConfig()
  const wordmark = systemName.toUpperCase()

  return (
    <section className='relative flex min-h-svh flex-col overflow-hidden'>
      <ParticleField />

      <div className='relative z-10 flex flex-1 flex-col items-center justify-center px-6 pt-24 pb-16 text-center'>
        <div
          aria-hidden
          className='pointer-events-none absolute top-[44%] left-1/2 h-80 w-[min(56rem,98vw)] -translate-x-1/2 -translate-y-1/2 bg-[radial-gradient(ellipse,rgba(244,246,250,0.92)_0%,rgba(244,246,250,0.55)_46%,transparent_74%)] dark:bg-[radial-gradient(ellipse,rgba(10,12,18,0.92)_0%,rgba(10,12,18,0.55)_46%,transparent_74%)]'
        />
        <p
          className={`landing-animate-fade-up text-[11px] font-medium tracking-[0.1em] uppercase ${homeMuted}`}
        >
          {t('One API · 100+ models')}
        </p>
        <h1
          className='landing-animate-fade-up mt-5 text-[clamp(2.5rem,11vw,8.5rem)] leading-[0.94] font-extrabold tracking-[-0.05em] text-balance text-[#0b1c47]/80 opacity-0 dark:text-[#eef1f6]/80'
          style={{ animationDelay: '80ms' }}
        >
          {wordmark}
        </h1>
        <p
          className={`landing-animate-fade-up mt-6 max-w-xl text-base leading-relaxed text-pretty opacity-0 md:text-lg ${homeMuted}`}
          style={{ animationDelay: '160ms' }}
        >
          {t('Different calls go through')}
          <span className={`${homeAccent} font-medium`}>
            {t('one endpoint')}
          </span>
          {t(
            '. {{name}} forwards the models you already run, instead of a pile of separate keys.',
            { name: systemName }
          )}
        </p>
        <div
          className='landing-animate-fade-up mt-8 flex flex-wrap items-center justify-center gap-3 opacity-0'
          style={{ animationDelay: '240ms' }}
        >
          {props.isAuthenticated ? (
            <HomePrimaryLink to='/dashboard'>
              {t('Go to Dashboard')}
              <ArrowUpRight className='size-4' aria-hidden />
            </HomePrimaryLink>
          ) : (
            <HomePrimaryLink to='/sign-up'>
              {t('Get Started')}
              <ArrowUpRight className='size-4' aria-hidden />
            </HomePrimaryLink>
          )}
          <HomeSecondaryLink to='/pricing'>
            {t('View Pricing')}
          </HomeSecondaryLink>
        </div>
      </div>

      <a
        href='#home-console'
        className={`relative z-10 mx-auto mb-8 hidden min-h-11 flex-col items-center justify-end px-6 text-[10px] font-medium tracking-[0.1em] uppercase sm:flex ${homeMuted}`}
      >
        {t('Scroll')}
        <span
          aria-hidden
          className='mt-3 h-8 w-px bg-[#0b1c47]/35 dark:bg-[#eef1f6]/35'
        />
      </a>
    </section>
  )
}

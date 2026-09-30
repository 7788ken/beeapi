import { useTranslation } from 'react-i18next'
import { useSystemConfig } from '@/hooks/use-system-config'
import {
  Eyebrow,
  homeAccent,
  homeMuted,
} from '@/features/home/components/home-links'

export function AboutHero() {
  const { t } = useTranslation()
  const { systemName } = useSystemConfig()
  const name = systemName || 'beeapi'

  return (
    <section className='relative overflow-hidden px-6 pt-36 pb-14 text-center md:pt-44 md:pb-16'>
      <div
        aria-hidden
        className='pointer-events-none absolute inset-x-0 top-0 h-[28rem] bg-[radial-gradient(ellipse_at_top,rgba(27,85,226,0.07)_0%,transparent_64%)] dark:bg-[radial-gradient(ellipse_at_top,rgba(120,160,240,0.09)_0%,transparent_64%)]'
      />
      <div className='relative mx-auto flex max-w-4xl flex-col items-center'>
        <Eyebrow className='landing-animate-fade-up'>
          {t('About {{name}}', { name })}
        </Eyebrow>
        <h1
          className='landing-animate-fade-up mt-6 text-[clamp(2.125rem,5.2vw,3.875rem)] leading-[1.06] font-extrabold tracking-[-0.035em] text-balance text-[#0b1c47] opacity-0 dark:text-[#eef1f6]'
          style={{ animationDelay: '80ms' }}
        >
          {t("We'll catch")}
          <br />
          <span className={homeAccent}>{t('every request, gently.')}</span>
        </h1>
        <p
          className={`landing-animate-fade-up mt-6 max-w-[40rem] text-base leading-[1.66] text-pretty opacity-0 md:text-lg ${homeMuted}`}
          style={{ animationDelay: '160ms' }}
        >
          {t(
            '{{name}} is a unified AI model access platform that provides stable, transparent and efficient model calling services for enterprise teams and individual developers.',
            { name }
          )}
        </p>
      </div>
    </section>
  )
}

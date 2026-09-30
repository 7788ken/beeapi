import { useTranslation } from 'react-i18next'
import { useSystemConfig } from '@/hooks/use-system-config'
import { AnimateInView } from '@/components/animate-in-view'
import { homeAccent, homeMuted } from '@/features/home/components/home-links'

export function Mission() {
  const { t } = useTranslation()
  const { systemName } = useSystemConfig()
  const name = systemName || 'beeapi'

  return (
    <section className='px-6 pt-6 pb-20 md:pt-10 md:pb-24'>
      <AnimateInView className='mx-auto grid max-w-6xl items-center gap-7 md:grid-cols-[1.05fr_1fr] md:gap-x-[5.4rem]'>
        <div>
          <p
            className={`flex items-center gap-3 text-[11px] font-medium tracking-[0.08em] ${homeMuted}`}
          >
            {t('Who we are')}
            <span
              aria-hidden
              className='h-px w-9 bg-[#0b1c47]/15 dark:bg-[#eef1f6]/15'
            />
          </p>
          <h2 className='mt-5 text-[clamp(1.625rem,3.6vw,2.625rem)] leading-[1.22] font-extrabold tracking-[-0.03em] text-[#0b1c47] dark:text-[#eef1f6]'>
            {t('Every model call,')}
            <br />
            {t('stable, transparent')}
            <br />
            {t('and efficient.')}
          </h2>
        </div>

        <div
          className={`space-y-4 text-[15px] leading-[1.75] text-pretty md:text-[15.5px] ${homeMuted}`}
        >
          <p>
            {t(
              'We integrate leading model providers including OpenAI, Anthropic, Google and DeepSeek, offering '
            )}
            <b className={`font-extrabold ${homeAccent}`}>
              {t('a single API entry point and management console')}
            </b>
            {t(
              ' so users can quickly access and manage multiple AI models without integrating with each provider separately.'
            )}
          </p>
          <p>
            {t(
              "Whether you're an enterprise customer requiring large-scale model calls, or an independent developer or individual user, {{name}} delivers a consistent access experience with clear cost management.",
              { name }
            )}
          </p>
        </div>
      </AnimateInView>
    </section>
  )
}

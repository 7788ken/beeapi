import { Check, Mail, Send } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { AnimateInView } from '@/components/animate-in-view'
import { homeAccent, homeMuted } from '@/features/home/components/home-links'

/** 部署时改成自己的支持邮箱和社群链接 */
const SUPPORT_EMAIL = 'support@example.com'
const TELEGRAM_COMMUNITY_URL = 'https://t.me/example'

const pill =
  'inline-flex h-[50px] items-center gap-2 rounded-full bg-[#f4f6fa] px-6 text-[15px] font-medium text-[#0b1c47] ring-1 ring-black/8 transition-transform duration-300 ease-out hover:-translate-y-0.5 focus-visible:shadow-[0_0_0_3px_rgba(11,28,71,0.18)] focus-visible:outline-none active:translate-y-0 motion-reduce:transition-none motion-reduce:hover:translate-y-0 dark:bg-[#161c2b] dark:text-[#eef1f6] dark:ring-white/10 dark:focus-visible:shadow-[0_0_0_3px_rgba(238,241,246,0.24)]'

export function Contact() {
  const { t } = useTranslation()
  const { copiedText, copyToClipboard } = useCopyToClipboard()
  const copied = copiedText === SUPPORT_EMAIL

  return (
    <section id='contact' className='px-6 pb-16 md:pb-20'>
      <AnimateInView className='mx-auto max-w-6xl'>
        <div className='flex flex-wrap items-center justify-between gap-x-9 gap-y-6 rounded-3xl bg-white p-8 ring-1 ring-black/6 md:p-[3.25rem] dark:bg-[#111726] dark:ring-white/8'>
          <div className='max-w-md'>
            <h2 className='text-[clamp(1.5rem,3vw,2.125rem)] leading-[1.3] font-extrabold tracking-[-0.03em] text-[#0b1c47] dark:text-[#eef1f6]'>
              {t('Contact us')}
            </h2>
            <p
              className={`mt-2.5 text-[15px] leading-relaxed text-pretty ${homeMuted}`}
            >
              {t(
                'For any questions, business inquiries or technical support, feel free to reach out.'
              )}
            </p>
          </div>

          <div className='flex flex-wrap gap-3'>
            <button
              type='button'
              onClick={() => copyToClipboard(SUPPORT_EMAIL)}
              aria-label={t('Copy email address')}
              className={`${pill} font-mono`}
            >
              {copied ? (
                <Check className={`size-4 ${homeAccent}`} aria-hidden />
              ) : (
                <Mail className='size-4' aria-hidden />
              )}
              {SUPPORT_EMAIL}
            </button>
            <a
              href={TELEGRAM_COMMUNITY_URL}
              target='_blank'
              rel='noopener noreferrer'
              className={pill}
            >
              <Send className='size-4' aria-hidden />
              {t('Telegram community')}
            </a>
          </div>
        </div>
      </AnimateInView>
    </section>
  )
}

import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { ArrowUpRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { AnimateInView } from '@/components/animate-in-view'

interface CTAProps {
  className?: string
  isAuthenticated?: boolean
}

const motion =
  'transition-transform duration-300 ease-out hover:-translate-y-0.5 active:translate-y-0 active:scale-[0.98] focus-visible:outline-none motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100'

/** 收尾卡是整页最后一次发声，用反色让它站住 */
function CTAButton(props: {
  to: string
  children: ReactNode
  variant: 'solid' | 'outline'
}) {
  return (
    <Link
      to={props.to}
      className={cn(
        motion,
        'inline-flex h-11 items-center justify-center gap-1.5 rounded-full px-5 text-sm font-medium',
        props.variant === 'solid'
          ? 'bg-white text-[#0b1c47] shadow-[0_1px_2px_rgba(0,0,0,0.2)] focus-visible:shadow-[0_0_0_3px_rgba(255,255,255,0.3)] dark:bg-[#111726] dark:text-[#eef1f6]'
          : 'text-white ring-1 ring-white/25 hover:bg-white/8 focus-visible:shadow-[0_0_0_3px_rgba(255,255,255,0.22)] dark:text-[#0b1c47] dark:ring-black/20 dark:hover:bg-black/6'
      )}
    >
      {props.children}
    </Link>
  )
}

export function CTA(props: CTAProps) {
  const { t } = useTranslation()

  return (
    <section className='px-6 pt-8 pb-20 md:pb-28'>
      <AnimateInView className='mx-auto max-w-6xl'>
        <div className='relative overflow-hidden rounded-xl bg-[#0b1c47] px-6 py-16 text-center md:px-12 md:py-24 dark:bg-[#eef1f6]'>
          <div
            aria-hidden
            className='pointer-events-none absolute inset-x-0 top-0 h-64 bg-[radial-gradient(ellipse_at_top,rgba(255,255,255,0.09)_0%,transparent_62%)] dark:bg-[radial-gradient(ellipse_at_top,rgba(0,0,0,0.05)_0%,transparent_62%)]'
          />
          <div className='relative'>
            <h2 className='text-[clamp(2rem,5vw,3.5rem)] leading-[1.08] font-extrabold tracking-[-0.035em] text-balance text-[#eef1f6] dark:text-[#0b1c47]'>
              {t('Focus on the product.')}
              <br />
              {t('Leave the calls to the gateway.')}
            </h2>
            <p className='mx-auto mt-5 max-w-md text-sm leading-relaxed text-pretty text-[#eef1f6]/70 md:text-base dark:text-[#0b1c47]/65'>
              {t(
                'Replace the base URL and use the channels you already configured.'
              )}
            </p>
            <div className='mt-8 flex flex-wrap items-center justify-center gap-3'>
              {props.isAuthenticated ? (
                <CTAButton to='/dashboard' variant='solid'>
                  {t('Go to Dashboard')}
                  <ArrowUpRight className='size-4' aria-hidden />
                </CTAButton>
              ) : (
                <CTAButton to='/sign-up' variant='solid'>
                  {t('Get Started')}
                  <ArrowUpRight className='size-4' aria-hidden />
                </CTAButton>
              )}
              <CTAButton to='/pricing' variant='outline'>
                {t('View Pricing')}
              </CTAButton>
            </div>
          </div>
        </div>
      </AnimateInView>
    </section>
  )
}

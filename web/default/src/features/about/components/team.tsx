import { Activity, Headset, Layers, Route, type LucideIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { AnimateInView } from '@/components/animate-in-view'
import {
  Eyebrow,
  SectionHeading,
  homeAccent,
  homeMuted,
} from '@/features/home/components/home-links'

/** 暂无成员名单与头像，先按职能展示；拿到真实名单后换成头像卡 */
const TEAM_FUNCTIONS: { icon: LucideIcon; title: string; detail: string }[] = [
  {
    icon: Route,
    title: 'Gateway engineering',
    detail: 'Routing · retries · billing engine',
  },
  {
    icon: Activity,
    title: 'Reliability on call',
    detail: 'Nodes · monitoring · alerts',
  },
  {
    icon: Layers,
    title: 'Upstreams & models',
    detail: 'Onboarding · quality checks',
  },
  {
    icon: Headset,
    title: 'Customer support',
    detail: 'Solution reviews · billing help',
  },
]

export function Team() {
  const { t } = useTranslation()

  return (
    <section className='px-6 pb-20 md:pb-24'>
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='mb-10'>
          <Eyebrow>{t('Team')}</Eyebrow>
          <SectionHeading className='mt-5'>
            {t('The people who build the infrastructure')}
          </SectionHeading>
          <p
            className={`mt-3.5 max-w-[460px] text-[15px] leading-relaxed text-pretty ${homeMuted}`}
          >
            {t(
              'A small, engineering-led team that builds and keeps this model supply network running.'
            )}
          </p>
        </AnimateInView>

        <div className='grid grid-cols-2 gap-3.5 md:grid-cols-4'>
          {TEAM_FUNCTIONS.map((item, index) => (
            <AnimateInView key={item.title} as='div' delay={index * 70}>
              <figure>
                <div className='relative aspect-[4/3] overflow-hidden rounded-[14px] bg-white ring-1 ring-black/6 dark:bg-[#111726] dark:ring-white/8'>
                  <div
                    aria-hidden
                    className='absolute inset-0 bg-[radial-gradient(ellipse_at_center,rgba(27,85,226,0.10)_0%,transparent_68%)] dark:bg-[radial-gradient(ellipse_at_center,rgba(120,160,240,0.14)_0%,transparent_68%)]'
                  />
                  <div
                    aria-hidden
                    className='absolute inset-0 bg-[radial-gradient(currentColor_1px,transparent_1px)] bg-size-[14px_14px] text-[#0b1c47]/8 dark:text-[#eef1f6]/7'
                  />
                  <span
                    className={`absolute top-3 left-3.5 font-mono text-[10.5px] tracking-[0.04em] ${homeMuted}`}
                  >
                    {String(index + 1).padStart(2, '0')}
                  </span>
                  <div className='absolute inset-0 grid place-items-center'>
                    <span className='grid size-14 place-items-center rounded-2xl bg-white shadow-[0_1px_2px_rgba(11,28,71,0.08)] ring-1 ring-black/8 dark:bg-[#161c2b] dark:ring-white/10'>
                      <item.icon
                        className={`size-6 ${homeAccent}`}
                        strokeWidth={1.75}
                        aria-hidden
                      />
                    </span>
                  </div>
                </div>
                <figcaption className='pt-3'>
                  <b className='block text-[14.5px] font-bold tracking-[-0.01em] text-[#0b1c47] dark:text-[#eef1f6]'>
                    {t(item.title)}
                  </b>
                  <span
                    className={`mt-1 block font-mono text-[10.5px] tracking-[0.04em] ${homeMuted}`}
                  >
                    {t(item.detail)}
                  </span>
                </figcaption>
              </figure>
            </AnimateInView>
          ))}
        </div>
      </div>
    </section>
  )
}

import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { Markdown } from '@/components/ui/markdown'
import { PublicLayout } from '@/components/layout'
import { Footer } from '@/components/layout/components/footer'
import {
  CapabilityBand,
  ConsolePreview,
  CTA,
  Features,
  Hero,
  HowItWorks,
  Solutions,
  Team,
} from './components'
import { FOOTER_COLUMNS } from './footer-columns'
import './home-type.css'
import { useHomePageContent } from './hooks'

export function Home() {
  const { t } = useTranslation()
  const { auth } = useAuthStore()
  const isAuthenticated = !!auth.user
  const { content, isLoaded, isUrl } = useHomePageContent()

  if (!isLoaded) {
    return (
      <PublicLayout showMainContainer={false}>
        <main className='flex min-h-screen items-center justify-center'>
          <div className='text-muted-foreground'>{t('Loading...')}</div>
        </main>
      </PublicLayout>
    )
  }

  if (content) {
    return (
      <PublicLayout showMainContainer={false}>
        <main className='overflow-x-hidden'>
          {isUrl ? (
            <iframe
              src={content}
              className='h-screen w-full border-none'
              title={t('Custom Home Page')}
            />
          ) : (
            <div className='container mx-auto py-8'>
              <Markdown className='custom-home-content'>{content}</Markdown>
            </div>
          )}
        </main>
      </PublicLayout>
    )
  }

  return (
    <PublicLayout
      showMainContainer={false}
      headerProps={{ className: 'home-type' }}
    >
      <div className='home-type bg-[#f4f6fa] text-[#0b1c47] dark:bg-[#0a0c12] dark:text-[#eef1f6]'>
        <Hero isAuthenticated={isAuthenticated} />
        <ConsolePreview />
        <CapabilityBand />
        <Features />
        <Solutions />
        <HowItWorks />
        <Team />
        <CTA isAuthenticated={isAuthenticated} />
        <Footer columns={FOOTER_COLUMNS} />
      </div>
    </PublicLayout>
  )
}

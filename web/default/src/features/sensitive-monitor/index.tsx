import { useTranslation } from 'react-i18next'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SectionPageLayout } from '@/components/layout'
import { BlocksTab } from './components/blocks-tab'
import { ConfigBar } from './components/config-bar'
import { WordsTab } from './components/words-tab'

export function SensitiveMonitor() {
  const { t } = useTranslation()
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Sensitive Monitor')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Description>
        {t(
          'Manage sensitive keywords and review hit records (async sampling pipeline).'
        )}
      </SectionPageLayout.Description>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <ConfigBar />
          <Tabs defaultValue='words'>
            <TabsList>
              <TabsTrigger value='words'>
                {t('Keyword management')}
              </TabsTrigger>
              <TabsTrigger value='blocks'>{t('Hit records')}</TabsTrigger>
            </TabsList>
            <TabsContent value='words' className='mt-3'>
              <WordsTab />
            </TabsContent>
            <TabsContent value='blocks' className='mt-3'>
              <BlocksTab />
            </TabsContent>
          </Tabs>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { useStatus } from '@/hooks/use-status'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Markdown } from '@/components/ui/markdown'
import { useFAQ } from '@/features/dashboard/hooks/use-status-data'
import type { FAQItem } from '@/features/dashboard/types'
import {
  DashBody,
  DashCard,
  DashSkeleton,
  DashTitle,
  dashSecondaryBtn,
} from './dash-style'

export function FAQPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { items: list, loading } = useFAQ()
  const { error } = useStatus()

  return (
    <DashCard className='h-full'>
      <DashTitle
        title={t('FAQ')}
        description={
          list.length > 0
            ? t('If a call fails, start with the usage logs.')
            : undefined
        }
        actions={
          error ? (
            <button
              type='button'
              className={dashSecondaryBtn}
              onClick={() => {
                void queryClient.invalidateQueries({ queryKey: ['status'] })
              }}
            >
              {t('Retry')}
            </button>
          ) : undefined
        }
      />
      <DashBody>
        {loading ? (
          <DashSkeleton className='h-16 w-full' />
        ) : error && list.length === 0 ? (
          <p className='font-sans text-sm text-pretty'>
            {t('This section could not be refreshed.')}
          </p>
        ) : list.length === 0 ? (
          <p className='text-muted-foreground font-sans text-sm text-pretty'>
            {t('If a call fails, start with the usage logs.')}{' '}
            <Link
              to='/usage-logs/$section'
              params={{ section: 'common' }}
              className='text-primary font-medium underline-offset-4 hover:underline'
            >
              {t('Usage Logs')}
            </Link>
          </p>
        ) : (
          <Accordion type='single' collapsible className='w-full'>
            {list.map((item: FAQItem, idx: number) => {
              const key = item.id ?? `faq-${idx}`
              return (
                <AccordionItem
                  key={key}
                  value={`item-${key}`}
                  className='border-border/70'
                >
                  <AccordionTrigger className='text-foreground text-base font-semibold hover:no-underline'>
                    <Markdown className='text-sm leading-relaxed'>
                      {item.question}
                    </Markdown>
                  </AccordionTrigger>
                  <AccordionContent>
                    <Markdown className='text-muted-foreground font-sans text-sm'>
                      {item.answer}
                    </Markdown>
                  </AccordionContent>
                </AccordionItem>
              )
            })}
          </Accordion>
        )}
      </DashBody>
    </DashCard>
  )
}

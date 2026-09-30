import { Link } from '@tanstack/react-router'
import { AlertTriangle, ArrowDown, ArrowUp, Minus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { iqTime } from '@/features/iq-test/lib'
import type { Channel } from '../types'

export function IQScoreCell(props: { channel: Channel }) {
  const { t } = useTranslation()
  const channel = props.channel
  const score = channel.iq_score
  const delta = channel.iq_score_delta
  const trend = channel.iq_score_trend
  const labels: Record<string, string> = {
    success: t('IQ Valid'),
    invalid: t('IQ Invalid'),
    error: t('IQ Error'),
    running: t('IQ Running'),
    never: t('IQ Not tested'),
  }
  const status =
    labels[channel.iq_score_status || 'never'] ?? channel.iq_score_status
  const comparable = delta != null && trend !== 'unknown'
  let tone = 'text-muted-foreground'
  let Marker = Minus
  if (trend === 'up') {
    tone = 'text-emerald-600'
    Marker = ArrowUp
  }
  if (trend === 'down') {
    tone = 'text-rose-600'
    Marker = ArrowDown
  }
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Link
          to='/iq-test'
          search={{ channel_id: channel.id }}
          className='inline-flex min-w-20 items-center gap-1.5 text-xs tabular-nums'
          aria-label={t('IQ score')}
        >
          {score == null ? (
            <span className='text-muted-foreground'>{status}</span>
          ) : (
            <>
              <span className='font-medium'>{score}</span>
              {channel.iq_score_error && (
                <AlertTriangle
                  className='size-3 text-amber-600'
                  aria-label={t('IQ Latest attempt failed')}
                />
              )}
              <span className={cn('inline-flex items-center gap-0.5', tone)}>
                <Marker className='size-3' />
                {comparable && (
                  <span>
                    {delta > 0 ? '+' : ''}
                    {delta}
                  </span>
                )}
              </span>
            </>
          )}
        </Link>
      </TooltipTrigger>
      <TooltipContent>
        <dl className='grid grid-cols-[auto_minmax(0,1fr)] gap-x-5 gap-y-1 text-xs'>
          <dt>{t('IQ score')}</dt>
          <dd className='text-right tabular-nums'>{score ?? '-'}</dd>
          <dt>{t('IQ Previous score')}</dt>
          <dd className='text-right tabular-nums'>
            {channel.iq_score_previous ?? '-'}
          </dd>
          <dt>{t('IQ Baseline')}</dt>
          <dd className='text-right tabular-nums'>
            {channel.iq_score_baseline ?? '-'}
          </dd>
          <dt>{t('Change')}</dt>
          <dd className='text-right'>
            {comparable
              ? `${delta > 0 ? '+' : ''}${delta}`
              : t('IQ Not comparable')}
          </dd>
          <dt>{t('Model')}</dt>
          <dd className='max-w-56 text-right break-all'>
            {channel.iq_score_model || '-'}
          </dd>
          <dt>{t('IQ Finished at')}</dt>
          <dd className='text-right'>{iqTime(channel.iq_score_at)}</dd>
          <dt>{t('Status')}</dt>
          <dd className='text-right'>{status}</dd>
          {channel.iq_score_error && (
            <>
              <dt>{t('IQ Latest attempt')}</dt>
              <dd className='text-right'>{iqTime(channel.iq_attempt_at)}</dd>
              <dt>{t('IQ Error class')}</dt>
              <dd className='max-w-56 text-right break-words'>
                {channel.iq_score_error}
              </dd>
            </>
          )}
        </dl>
      </TooltipContent>
    </Tooltip>
  )
}

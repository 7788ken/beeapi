/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import {
  formatLatency,
  formatThroughput,
  formatUptimePct,
} from '@/features/performance-metrics/lib/format'
import {
  DashBody,
  DashCard,
  DashSkeleton,
  DashTitle,
  dashSecondaryBtn,
} from './dash-style'
import {
  PERFORMANCE_WINDOW_HOURS,
  rateLevel,
  usePerformanceSummary,
} from './use-performance-summary'

function levelLabel(
  level: ReturnType<typeof rateLevel>,
  t: (key: string) => string
) {
  if (level === 'normal') return t('Operating normally')
  if (level === 'attention') return t('Needs attention')
  if (level === 'failing') return t('Calls are failing')
  return t('No sample')
}

function barClass(level: ReturnType<typeof rateLevel>) {
  if (level === 'normal') return 'bg-primary'
  if (level === 'failing') return 'bg-foreground'
  return 'bg-[#7a73ff]'
}

function textClass(level: ReturnType<typeof rateLevel>) {
  if (level === 'normal') return 'text-primary'
  if (level === 'failing') return 'font-semibold text-foreground'
  return 'text-muted-foreground'
}

export function PerformanceHealthPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const performance = usePerformanceSummary(true)
  const { summary, topModels, loading, hasData, level } = performance
  const latencyText =
    Number.isFinite(summary.avgLatencyMs) && summary.avgLatencyMs > 0
      ? formatLatency(summary.avgLatencyMs)
      : t('No sample')
  const throughputText =
    Number.isFinite(summary.avgTps) && summary.avgTps > 0
      ? formatThroughput(summary.avgTps)
      : t('No sample')

  return (
    <DashCard className='h-full'>
      <DashTitle
        title={t('Service status')}
        description={
          hasData
            ? t(
                'In the last 24 hours the success rate is {{rate}} ({{level}}), average latency is {{latency}}, and throughput is {{throughput}}.',
                {
                  rate: formatUptimePct(summary.successRate),
                  level: levelLabel(level, t),
                  latency: latencyText,
                  throughput: throughputText,
                }
              )
            : t('Performance metrics for the last 24 hours')
        }
        actions={
          performance.metricsQuery.isError ? (
            <button
              type='button'
              className={dashSecondaryBtn}
              onClick={() => {
                void queryClient.invalidateQueries({
                  queryKey: ['perf-metrics-summary', PERFORMANCE_WINDOW_HOURS],
                })
              }}
            >
              {t('Retry')}
            </button>
          ) : (
            <Link
              to='/dashboard/$section'
              params={{ section: 'models' }}
              className={dashSecondaryBtn}
            >
              {t('Dashboard')}
            </Link>
          )
        }
      />
      <DashBody className='gap-3'>
        {loading ? (
          Array.from({ length: 4 }).map((_, index) => (
            <DashSkeleton key={index} className='h-8 w-full' />
          ))
        ) : performance.metricsQuery.isError ? (
          <p className='text-foreground text-base text-pretty'>
            {t('Could not load performance data.')}
          </p>
        ) : !hasData ? (
          <p className='text-muted-foreground font-sans text-sm text-pretty'>
            {t(
              'No calls in the last 24 hours, so success rate and latency are not available yet.'
            )}{' '}
            <Link
              to='/playground'
              className='text-primary font-medium underline-offset-4 hover:underline'
            >
              {t('Try a call in the playground')}
            </Link>
          </p>
        ) : (
          topModels.map((model) => {
            const modelLevel = rateLevel(model.success_rate)
            const width = Number.isFinite(model.success_rate)
              ? Math.max(0, Math.min(100, model.success_rate))
              : 0
            return (
              <div key={model.model_name} className='min-w-0'>
                <div className='mb-1 flex items-center justify-between gap-3'>
                  <span className='text-foreground truncate font-mono text-sm'>
                    {model.model_name}
                  </span>
                  <span
                    className={cn(
                      'shrink-0 font-sans text-xs tabular-nums',
                      textClass(modelLevel)
                    )}
                  >
                    {levelLabel(modelLevel, t)}{' '}
                    {formatUptimePct(model.success_rate)}
                  </span>
                </div>
                <div className='bg-foreground/10 h-2 rounded-lg'>
                  <div
                    className={cn('h-2 rounded-lg', barClass(modelLevel))}
                    style={{ width: `${width}%` }}
                  />
                </div>
              </div>
            )
          })
        )}
      </DashBody>
    </DashCard>
  )
}

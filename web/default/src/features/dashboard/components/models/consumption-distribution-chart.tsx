import { useEffect, useMemo, useRef, useState } from 'react'
import { VChart } from '@visactor/react-vchart'
import { AreaChart, BarChart3, WalletCards } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { TimeGranularity } from '@/lib/time'
import { cn } from '@/lib/utils'
import { VCHART_OPTION } from '@/lib/vchart'
import { useTheme } from '@/context/theme-provider'
import { surfaceClass } from '@/components/ui/card'
import {
  CONSUMPTION_DISTRIBUTION_CHART_OPTIONS,
  DEFAULT_TIME_GRANULARITY,
} from '@/features/dashboard/constants'
import { processChartData } from '@/features/dashboard/lib'
import type {
  ConsumptionDistributionChartType,
  QuotaDataItem,
} from '@/features/dashboard/types'
import { dashSegItem, dashSegTrack } from '../overview/dash-emphasis'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

interface ConsumptionDistributionChartProps {
  data: QuotaDataItem[]
  loading?: boolean
  timeGranularity?: TimeGranularity
  defaultChartType?: ConsumptionDistributionChartType
}

const CHART_TYPE_ICONS: Record<
  ConsumptionDistributionChartType,
  typeof BarChart3
> = {
  bar: BarChart3,
  area: AreaChart,
}

export function ConsumptionDistributionChart(
  props: ConsumptionDistributionChartProps
) {
  const { t } = useTranslation()
  const { resolvedTheme } = useTheme()
  const [chartType, setChartType] = useState<ConsumptionDistributionChartType>(
    props.defaultChartType ?? 'bar'
  )
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)
  const timeGranularity = props.timeGranularity ?? DEFAULT_TIME_GRANULARITY

  useEffect(() => {
    if (props.defaultChartType) setChartType(props.defaultChartType)
  }, [props.defaultChartType])

  useEffect(() => {
    const updateTheme = async () => {
      setThemeReady(false)

      if (!themeManagerPromise) {
        themeManagerPromise = import('@visactor/vchart').then(
          (m) => m.ThemeManager
        )
      }

      const ThemeManager = await themeManagerPromise
      themeManagerRef.current = ThemeManager
      ThemeManager.setCurrentTheme(resolvedTheme === 'dark' ? 'dark' : 'light')
      setThemeReady(true)
    }

    updateTheme()
  }, [resolvedTheme])

  const chartData = useMemo(
    () => processChartData(props.loading ? [] : props.data, timeGranularity, t),
    [props.data, props.loading, timeGranularity, t]
  )
  const spec = chartType === 'bar' ? chartData.spec_line : chartData.spec_area
  const specType = typeof spec?.type === 'string' ? spec.type : chartType
  const chartKey = [
    chartType,
    specType,
    props.loading ? 'loading' : 'ready',
    props.data.length,
    resolvedTheme,
  ].join('-')

  const isEmpty = !props.loading && props.data.length === 0

  return (
    <div className={cn(surfaceClass, 'overflow-hidden')}>
      <div className='flex w-full flex-col gap-1.5 border-b px-3 py-2 sm:gap-3 sm:px-5 sm:py-3 lg:flex-row lg:items-center lg:justify-between'>
        <div className='flex items-center gap-2'>
          <WalletCards className='text-muted-foreground/60 size-4' />
          <div className='text-sm font-semibold'>{t('Quota Distribution')}</div>
          <span className='text-muted-foreground text-xs'>
            {t('Total:')} {chartData.totalQuotaDisplay}
          </span>
        </div>

        <div className={cn(dashSegTrack, 'w-full overflow-x-auto sm:w-auto')}>
          {CONSUMPTION_DISTRIBUTION_CHART_OPTIONS.map((item) => {
            const Icon = CHART_TYPE_ICONS[item.value]
            return (
              <button
                key={item.value}
                type='button'
                onClick={() => setChartType(item.value)}
                aria-pressed={chartType === item.value}
                className={dashSegItem(chartType === item.value)}
              >
                <Icon className='size-3.5' />
                {t(item.labelKey)}
              </button>
            )
          })}
        </div>
      </div>

      <div
        className={cn(
          'p-1.5 sm:p-2',
          // 空态不需要撑满图表高度
          isEmpty ? 'h-40' : 'h-[300px] sm:h-96'
        )}
      >
        {isEmpty ? (
          <div className='text-muted-foreground flex h-full items-center justify-center text-sm'>
            {t('No data in selected range')}
          </div>
        ) : themeReady && spec ? (
          <VChart
            key={chartKey}
            spec={{
              ...spec,
              theme: resolvedTheme === 'dark' ? 'dark' : 'light',
              background: 'transparent',
            }}
            option={VCHART_OPTION}
          />
        ) : null}
      </div>
    </div>
  )
}

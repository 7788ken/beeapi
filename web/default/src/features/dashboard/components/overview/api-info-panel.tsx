import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Gauge, LoaderCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { useStatus } from '@/hooks/use-status'
import { useApiInfo } from '@/features/dashboard/hooks/use-status-data'
import {
  getDefaultPingStatus,
  testUrlLatency,
  urlOrigin,
} from '@/features/dashboard/lib/api-info'
import type { PingStatusMap } from '@/features/dashboard/types'
import { ApiInfoItemComponent } from './api-info-item'
import {
  DashCard,
  DashSkeleton,
  DashTitle,
  dashPrimaryBtn,
  dashSecondaryBtn,
} from './dash-style'

/** 最快的一台服务器；并列时取配置里靠前的那台。 */
function pickFastest(origins: string[], statuses: PingStatusMap) {
  let best: { origin: string; latency: number } | null = null
  for (const origin of origins) {
    const latency = statuses[origin]?.latency ?? null
    if (latency === null) continue
    if (!best || latency < best.latency) best = { origin, latency }
  }
  return best
}

export function ApiInfoPanel() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { items: list, loading } = useApiInfo()
  const { error } = useStatus()
  // 按服务器记结果：每台服务器测一次，用配置里它的第一条地址
  const [pingStatus, setPingStatus] = useState<PingStatusMap>({})
  // 按钮文案看有没有测完过一轮，重新测速的过程中不会跳回“全部测速”
  const [roundsDone, setRoundsDone] = useState(0)
  const targets = useMemo(() => {
    const map = new Map<string, string>()
    for (const item of list) {
      const origin = urlOrigin(item.url)
      if (!map.has(origin)) map.set(origin, item.url)
    }
    return map
  }, [list])
  const origins = [...targets.keys()]

  const testing = origins.some((origin) => pingStatus[origin]?.testing)
  const finished =
    !testing && origins.some((origin) => pingStatus[origin] !== undefined)
  const fastest = finished ? pickFastest(origins, pingStatus) : null
  // 只有一台服务器时没有“最快”可言
  const fastestOrigin = origins.length > 1 ? fastest?.origin : undefined

  const handleTestAll = () => {
    setPingStatus(
      Object.fromEntries(
        origins.map((origin) => [
          origin,
          { latency: null, testing: true, error: false },
        ])
      )
    )
    const runs = [...targets].map(([origin, url]) =>
      testUrlLatency(url).then((result) => {
        setPingStatus((prev) => ({ ...prev, [origin]: result }))
      })
    )
    void Promise.all(runs).then(() => setRoundsDone((n) => n + 1))
  }

  let summary = t(
    'Not sure which line to use? Test them all to see which is fastest.'
  )
  let summaryTone = 'text-muted-foreground'
  if (testing) {
    summary = t('Testing every line...')
  } else if (finished && !fastest) {
    summary = t(
      'No address responded. Check your network or proxy, then test again.'
    )
    summaryTone = 'text-foreground font-semibold'
  } else if (fastest) {
    const host = fastest.origin.replace(/^https?:\/\//, '')
    summary = fastestOrigin
      ? t('From your network, {{host}} is the fastest ({{latency}} ms).', {
          host,
          latency: fastest.latency,
        })
      : t('{{host}} responded in {{latency}} ms.', {
          host,
          latency: fastest.latency,
        })
    summaryTone = 'text-foreground'
  }

  return (
    <DashCard>
      <DashTitle
        title={t('Choose an API address')}
        description={t(
          'Pick the line that suits your network, then click its address to copy it.'
        )}
        actions={
          <>
            <Link to='/wallet' className={dashPrimaryBtn}>
              {t('Recharge')}
            </Link>
            <Link
              to='/usage-logs/$section'
              params={{ section: 'common' }}
              className={dashSecondaryBtn}
            >
              {t('Usage Logs')}
            </Link>
            <Link to='/keys' className={dashSecondaryBtn}>
              {t('API Keys')}
            </Link>
            <Link to='/playground' className={dashSecondaryBtn}>
              {t('Playground')}
            </Link>
            {error ? (
              <button
                type='button'
                className={dashSecondaryBtn}
                onClick={() => {
                  void queryClient.invalidateQueries({ queryKey: ['status'] })
                }}
              >
                {t('Retry')}
              </button>
            ) : null}
          </>
        }
      />
      {loading ? (
        <div className='flex flex-col gap-3'>
          <DashSkeleton className='h-14 w-full' />
          <DashSkeleton className='h-14 w-full' />
          <DashSkeleton className='h-14 w-full' />
        </div>
      ) : error && list.length === 0 ? (
        <p className='font-sans text-sm text-pretty'>
          {t('This section could not be refreshed.')}
        </p>
      ) : list.length === 0 ? (
        <p className='text-muted-foreground font-sans text-sm text-pretty'>
          {t(
            'No API address is configured yet. Ask an admin to add one before you copy it.'
          )}
        </p>
      ) : (
        <>
          {error ? (
            <p className='mb-3 font-sans text-sm text-pretty'>
              {t('This section could not be refreshed.')}
            </p>
          ) : null}
          <div className='mb-4 flex flex-wrap items-center gap-x-4 gap-y-2'>
            <button
              type='button'
              className={dashSecondaryBtn}
              onClick={handleTestAll}
              disabled={testing}
              aria-busy={testing}
            >
              {testing ? (
                <LoaderCircle
                  className='size-4 animate-spin motion-reduce:animate-none'
                  aria-hidden
                />
              ) : (
                <Gauge className='size-4' aria-hidden />
              )}
              {roundsDone > 0 ? t('Retest all lines') : t('Test all lines')}
            </button>
            <p
              className={cn('min-w-0 text-sm text-pretty', summaryTone)}
              aria-live='polite'
            >
              {summary}
            </p>
          </div>
          <ul className='border-border divide-border @container -mx-6 divide-y border-y md:-mx-8'>
            {list.map((item) => {
              const origin = urlOrigin(item.url)
              return (
                <ApiInfoItemComponent
                  key={item.url}
                  item={item}
                  status={pingStatus[origin] ?? getDefaultPingStatus()}
                  fastest={origin === fastestOrigin}
                />
              )
            })}
          </ul>
          <p className='text-muted-foreground mt-4 text-xs text-pretty'>
            {t(
              'A latency check only tells you the address responds. It does not guarantee a model call will succeed.'
            )}
          </p>
        </>
      )}
    </DashCard>
  )
}

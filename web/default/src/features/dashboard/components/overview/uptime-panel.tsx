import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { getUptimeStatus } from '@/features/dashboard/api'
import type {
  UptimeGroupResult,
  UptimeMonitor,
} from '@/features/dashboard/types'
import {
  DashBody,
  DashCard,
  DashSkeleton,
  DashTitle,
  dashSecondaryBtn,
} from './dash-style'

function monitorLabel(status: number, t: (key: string) => string) {
  switch (status) {
    case 1:
      return t('Monitor is up')
    case 0:
      return t('Monitor is down')
    case 2:
      return t('Monitor is checking')
    case 3:
      return t('Maintenance')
    default:
      return t('No sample')
  }
}

function monitorTone(status: number) {
  if (status === 1) return 'text-primary'
  if (status === 0) return 'font-semibold text-foreground'
  return 'text-muted-foreground'
}

export function UptimePanel() {
  const { t } = useTranslation()
  const [groups, setGroups] = useState<UptimeGroupResult[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const abortController = new AbortController()
    getUptimeStatus()
      .then((res) => {
        if (abortController.signal.aborted) return
        setGroups(res?.data || [])
        setFailed(false)
      })
      .catch(() => {
        if (abortController.signal.aborted) return
        setGroups([])
        setFailed(true)
      })
      .finally(() => {
        if (!abortController.signal.aborted) setLoading(false)
      })
    return () => {
      abortController.abort()
    }
  }, [])

  const handleRefresh = () => {
    setRefreshing(true)
    getUptimeStatus()
      .then((res) => {
        setGroups(res?.data || [])
        setFailed(false)
      })
      .catch(() => {
        setFailed(true)
      })
      .finally(() => {
        setRefreshing(false)
      })
  }

  const hasMonitors = groups.some((group) => (group.monitors?.length ?? 0) > 0)

  return (
    <DashCard className='h-full'>
      <DashTitle
        title={t('Uptime')}
        actions={
          hasMonitors || failed ? (
            <button
              type='button'
              className={dashSecondaryBtn}
              onClick={handleRefresh}
              disabled={refreshing}
              aria-busy={refreshing}
            >
              {t('Refresh')}
            </button>
          ) : undefined
        }
      />
      <DashBody>
        {loading ? (
          <DashSkeleton className='h-24 w-full' />
        ) : failed && !hasMonitors ? (
          <p className='font-sans text-sm text-pretty'>
            {t('Could not load service monitors.')}
          </p>
        ) : !hasMonitors ? (
          <p className='text-muted-foreground font-sans text-sm text-pretty'>
            {t('Service monitoring is not set up.')}
          </p>
        ) : (
          <div className='flex flex-col gap-4 md:gap-6'>
            {failed ? (
              <p className='font-sans text-sm text-pretty'>
                {t('Could not load service monitors.')}
              </p>
            ) : null}
            {groups.map((group) => (
              <div key={group.categoryName}>
                <h4 className='text-muted-foreground mb-2 font-sans text-sm'>
                  {group.categoryName}
                  <span className='ml-2 tabular-nums'>
                    {group.monitors?.length || 0}
                  </span>
                </h4>
                <ul>
                  {group.monitors?.map((monitor: UptimeMonitor) => (
                    <li
                      key={monitor.name}
                      className='border-border hover:bg-foreground/[0.04] flex items-center justify-between gap-3 border-t py-3 transition-colors duration-[300ms] ease-out motion-reduce:transition-none'
                    >
                      <span className='min-w-0 truncate font-sans text-sm'>
                        {monitor.name}
                        {monitor.group ? (
                          <span className='text-muted-foreground'>
                            {' '}
                            · {monitor.group}
                          </span>
                        ) : null}
                      </span>
                      <span
                        className={cn(
                          'shrink-0 font-sans text-xs tabular-nums',
                          monitorTone(monitor.status)
                        )}
                      >
                        {monitorLabel(monitor.status, t)} ·{' '}
                        {((monitor.uptime ?? 0) * 100).toFixed(2)}%
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}
      </DashBody>
    </DashCard>
  )
}

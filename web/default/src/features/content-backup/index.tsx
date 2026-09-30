import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { SectionPageLayout } from '@/components/layout'

import {
  useContentBackupStatus,
} from './api'
import { ArchivesView } from './components/archives'
import { QueueView } from './components/queue'
import { NodesView } from './components/nodes'
import { ContentBackupDetailDrawer } from './components/detail-drawer'
import {
  emptyStateLabel,
  formatBytes,
  resolveContentBackupPerms,
  statusLabel,
} from './status'
import type {
  ContentBackupJob,
  ContentBackupListFilter,
  ContentBackupTab,
} from './types'
import { surfaceClass } from '@/components/ui/card'

export type { ContentBackupTab } from './types'

export interface ContentBackupProps {
  /** 日志详情入口直达：带上 request_id 时直接打开归档并定位抽屉 */
  requestId?: string
}

export function ContentBackup(props: ContentBackupProps) {
  const { t } = useTranslation()
  const [initialFilter] = useState<ContentBackupListFilter>(
    props.requestId ? { requestId: props.requestId } : {}
  )
  const auth = useAuthStore((s) => s.auth)
  const [tab, setTab] = useState<ContentBackupTab>('archives')
  const [filter, setFilter] = useState<ContentBackupListFilter>(initialFilter)
  const [drawerJob, setDrawerJob] = useState<ContentBackupJob | null>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)

  const perms = useMemo(
    () =>
      resolveContentBackupPerms(
        auth?.user?.role,
        auth?.user?.permissions?.admin,
        auth?.user?.admin_perms
      ),
    [auth]
  )
  const isRoot = (auth?.user?.role ?? 0) >= ROLE.SUPER_ADMIN

  const statusQuery = useContentBackupStatus({ enabled: perms.view })
  const status = statusQuery.data

  const openDrawer = (job: ContentBackupJob) => {
    setDrawerJob(job)
    setDrawerOpen(true)
  }

  const showJobs = (
    nextTab: ContentBackupTab,
    nextFilter: ContentBackupListFilter
  ) => {
    setTab(nextTab)
    setFilter(nextFilter)
  }

  const tabs: { key: ContentBackupTab; label: string }[] = [
    { key: 'archives', label: t('Archives') },
    { key: 'queue', label: t('Queue') },
    { key: 'nodes', label: t('Storage nodes') },
  ]

  const bodyEmpty = !perms.view
  const emptyLabel = emptyStateLabel({
    canView: perms.view,
    isError: statusQuery.isError,
    itemCount: 0,
  })

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Content backup')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='flex flex-wrap items-center gap-2 text-sm text-muted-foreground'>
          <span
            className={cn(
              'bg-card inline-flex items-center gap-1 rounded-full border px-2 py-0.5',
              status?.enabled
                ? 'border-emerald-300 text-emerald-700 dark:text-emerald-400'
                : 'border-border'
            )}
          >
            {status?.enabled ? t('Capture on') : t('Capture off')}
          </span>
          <span
            className={cn(
              'bg-card inline-flex items-center gap-1 rounded-full border px-2 py-0.5',
              status?.upload_paused
                ? 'border-amber-300 text-amber-700 dark:text-amber-400'
                : 'border-border'
            )}
          >
            {status?.upload_paused ? t('Uploads paused') : t('Uploads running')}
          </span>
          {status ? (
            <span className='tabular-nums'>
              {t('Today')}: {status.today_uploaded_count ?? 0} ·{' '}
              {formatBytes(status.today_uploaded_bytes ?? 0)}
            </span>
          ) : null}
          {status && (status.pending_count ?? 0) > 0 ? (
            <button
              type='button'
              className='bg-card rounded-full border border-amber-300 px-2 py-0.5 text-amber-700 dark:text-amber-400'
              onClick={() => showJobs('queue', {})}
            >
              {t(statusLabel('pending'))}{' '}
              <span className='tabular-nums'>{status.pending_count}</span>
            </button>
          ) : null}
          {status && (status.failed_count ?? 0) > 0 ? (
            <button
              type='button'
              className='bg-card rounded-full border border-destructive/40 px-2 py-0.5 text-destructive'
              onClick={() => showJobs('queue', { status: 'failed' })}
            >
              {t(statusLabel('failed'))}{' '}
              <span className='tabular-nums'>{status.failed_count}</span>
            </button>
          ) : null}
        </div>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          {bodyEmpty ? (
            <div className={cn(surfaceClass, 'p-8 text-center text-muted-foreground')}>
              {emptyLabel ? t(emptyLabel) : null}
            </div>
          ) : (
            <>
              <nav className='flex gap-1 border-b'>
                {tabs.map((item) => (
                  <button
                    key={item.key}
                    type='button'
                    className={cn(
                      'rounded-t-md px-3 py-1.5 text-sm',
                      tab === item.key
                        ? 'border border-b-0 bg-background font-medium'
                        : 'text-muted-foreground hover:text-foreground'
                    )}
                    onClick={() => setTab(item.key)}
                  >
                    {item.label}
                  </button>
                ))}
              </nav>

              {tab === 'archives' ? (
                <ArchivesView
                  canView={perms.view}
                  isRoot={isRoot}
                  drawerOpen={drawerOpen}
                  filter={filter}
                  onFilterChange={setFilter}
                  onOpenJob={openDrawer}
                />
              ) : null}
              {tab === 'queue' ? (
                <QueueView
                  canView={perms.view}
                  canManage={perms.manage}
                  drawerOpen={drawerOpen}
                  filter={filter}
                  onFilterChange={setFilter}
                  onOpenJob={openDrawer}
                />
              ) : null}
              {tab === 'nodes' ? (
                <NodesView canView={perms.view} onShowJobs={showJobs} />
              ) : null}

              <ContentBackupDetailDrawer
                jobId={drawerJob?.job_id ?? null}
                job={drawerJob ?? undefined}
                open={drawerOpen}
                onOpenChange={setDrawerOpen}
                canManage={perms.manage}
                isRoot={isRoot}
              />
            </>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export default ContentBackup

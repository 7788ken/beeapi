import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ArrowRight, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { surfaceClass } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import {
  Table,
  TableHeader,
  TableHead,
  TableBody,
  TableRow,
  TableCell,
} from '@/components/ui/table'
import { getIQRuns, iqKeys } from '../api'
import { iqTime } from '../lib'
import { IQIconButton, IQPagination, IQQueryState, IQStatus } from './shared'

export function IQRuns(props: { onSelect: (runID: string) => void }) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: [...iqKeys.runs, page],
    queryFn: () => getIQRuns({ page, page_size: 20 }),
    refetchInterval: 4000,
  })
  const rows = query.data?.items ?? []
  return (
    <div className='space-y-3'>
      <div className='flex justify-end'>
        <IQIconButton
          label={t('Refresh')}
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          <RefreshCw className='size-4' />
        </IQIconButton>
      </div>
      <IQQueryState
        loading={query.isPending}
        error={query.error}
        empty={!rows.length}
        retry={() => void query.refetch()}
      />
      {!query.isError && rows.length > 0 && (
        <div className={cn(surfaceClass, 'overflow-hidden')}>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('IQ Run')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead>{t('IQ Scope')}</TableHead>
                <TableHead>{t('IQ Progress')}</TableHead>
                <TableHead>{t('IQ Started at')}</TableHead>
                <TableHead>{t('IQ Finished at')}</TableHead>
                <TableHead>{t('Actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((run) => {
                const completed =
                  run.success_count + run.invalid_count + run.error_count
                return (
                  <TableRow key={run.run_id}>
                    <TableCell>
                      <span
                        className='block max-w-40 truncate font-mono text-xs'
                        title={run.run_id}
                      >
                        {run.run_id}
                      </span>
                      <span className='text-muted-foreground text-xs'>
                        {run.trigger === 'schedule'
                          ? t('IQ Scheduled')
                          : t('IQ Manual')}
                      </span>
                    </TableCell>
                    <TableCell>
                      <IQStatus status={run.status} />
                      {run.error_message && (
                        <p className='text-destructive mt-1 max-w-56 text-xs break-words whitespace-normal'>
                          {run.error_message}
                        </p>
                      )}
                    </TableCell>
                    <TableCell>
                      {run.channel_id > 0
                        ? `#${run.channel_id}`
                        : t('All Channels')}
                    </TableCell>
                    <TableCell>
                      <div className='min-w-28 space-y-1'>
                        <span className='text-xs tabular-nums'>
                          {completed} / {run.candidate_count}
                        </span>
                        <Progress
                          value={
                            run.candidate_count
                              ? (completed / run.candidate_count) * 100
                              : 0
                          }
                          className='h-1'
                        />
                        <span className='text-muted-foreground text-xs'>
                          {t('IQ Run counts', {
                            success: run.success_count,
                            invalid: run.invalid_count,
                            error: run.error_count,
                          })}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className='text-xs'>
                      {iqTime(run.started_at)}
                    </TableCell>
                    <TableCell className='text-xs'>
                      {iqTime(run.finished_at)}
                    </TableCell>
                    <TableCell>
                      <IQIconButton
                        label={t('IQ View results')}
                        onClick={() => props.onSelect(run.run_id)}
                      >
                        <ArrowRight className='size-4' />
                      </IQIconButton>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      )}
      <IQPagination
        page={page}
        total={query.data?.total ?? 0}
        setPage={setPage}
        fetching={query.isFetching}
      />
    </div>
  )
}

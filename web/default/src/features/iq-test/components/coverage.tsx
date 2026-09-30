import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { surfaceClass } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import type { IQCoverage } from '../api'

const REASONS: Record<string, string> = {
  protocol_unsupported: 'IQ Skip protocol unsupported',
  channel_disabled: 'IQ Skip channel disabled',
  channel_auto_disabled: 'IQ Skip channel auto disabled',
  channel_not_enabled: 'IQ Skip channel not enabled',
}

// IQCoverageSummary states what a round will actually reach before any upstream
// quota is spent, so "all channels" never quietly means "a subset".
export function IQCoverageSummary(props: { coverage?: IQCoverage }) {
  const { t } = useTranslation()
  const coverage = props.coverage
  if (!coverage) return null
  const skipped = coverage.skipped ?? []
  const hidden = coverage.skipped_total - skipped.length
  return (
    <section className={cn(surfaceClass, 'space-y-3 p-4')}>
      <div className='flex flex-wrap items-center gap-x-6 gap-y-2 text-sm'>
        <span>
          {t('IQ Coverage', {
            channels: coverage.eligible_channels,
            pairs: coverage.eligible_pairs,
          })}
        </span>
        {coverage.skipped_total > 0 && (
          <span className='text-xs text-amber-600'>
            {t('IQ Coverage gaps', { count: coverage.skipped_total })}
          </span>
        )}
        {coverage.enforcement_mode === 'report_only' && (
          <span className='text-muted-foreground text-xs'>
            {t('IQ Report only mode')}
          </span>
        )}
      </div>
      {skipped.length > 0 && (
        <div className='max-h-64 overflow-auto'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('ID')}</TableHead>
                <TableHead>{t('Name')}</TableHead>
                <TableHead>{t('IQ Model')}</TableHead>
                <TableHead>{t('IQ Skip reason')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {skipped.map((skip) => (
                <TableRow key={`${skip.channel_id}:${skip.model_name}`}>
                  <TableCell className='font-mono text-xs'>
                    {skip.channel_id}
                  </TableCell>
                  <TableCell className='max-w-56 truncate text-xs'>
                    {skip.name}
                  </TableCell>
                  <TableCell className='text-xs'>{skip.model_name}</TableCell>
                  <TableCell className='text-xs'>
                    {t(REASONS[skip.reason] ?? 'IQ Skip channel not enabled')}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {hidden > 0 && (
            <p className='text-muted-foreground mt-2 text-xs'>
              {t('IQ More skipped', { count: hidden })}
            </p>
          )}
        </div>
      )}
    </section>
  )
}

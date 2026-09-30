import { Fragment, useState } from 'react'
import { z } from 'zod'
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, Search, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { surfaceClass } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableHeader,
  TableHead,
  TableBody,
  TableRow,
  TableCell,
} from '@/components/ui/table'
import { getIQResults, iqKeys, type IQResult } from '../api'
import { iqTime } from '../lib'
import { IQIconButton, IQPagination, IQQueryState, IQStatus } from './shared'

const questionDetails = z.array(
  z.object({
    qid: z.string(),
    category: z.string(),
    passed: z.boolean(),
    answer_excerpt: z.string().optional(),
    error_class: z.string().optional(),
    failure_class: z.string().optional(),
    http_status: z.number().optional(),
    attempts: z.number().optional(),
    error_snippet: z.string().optional(),
  })
)

// A probe failure is not a wrong answer: it means the upstream never gave us
// something to judge, so it must not be presented as an incorrect response.
function isProbeFailure(item: z.infer<typeof questionDetails>[number]) {
  return item.error_class === 'probe_failed'
}

// Newer records wrap the question array in an object that also carries the
// preference-sampling and model-echo blocks; legacy records are the bare array.
const detailEnvelope = z.object({
  questions: questionDetails,
  preference_samples: z
    .array(
      z.object({
        qid: z.string(),
        samples: z.array(z.string()).optional(),
        verdict: z.string(),
        dominance: z.number().optional(),
      })
    )
    .optional(),
  model_echo: z
    .object({
      requested: z.string(),
      echoed: z.string(),
      verdict: z.string(),
    })
    .optional(),
})

function unwrapDetail(parsed: unknown): z.infer<typeof questionDetails> {
  const envelope = detailEnvelope.safeParse(parsed)
  if (envelope.success) return envelope.data.questions
  return questionDetails.parse(parsed)
}

function PreferenceEchoSummary(props: { parsed: unknown }) {
  const { t } = useTranslation()
  const envelope = detailEnvelope.safeParse(props.parsed)
  if (!envelope.success) return null
  const { preference_samples: preferenceSamples, model_echo: modelEcho } =
    envelope.data
  return (
    <>
      {preferenceSamples?.map((block) => {
        if (block.verdict === 'collapse')
          return (
            <li key={block.qid} className='py-2 text-xs text-amber-600'>
              {t('IQ Preference collapse', { count: block.samples?.length ?? 0 })}
              {block.samples && block.samples.length > 0
                ? `: ${block.samples.join(' / ')}`
                : ''}
            </li>
          )
        return null
      })}
      {modelEcho?.verdict === 'mismatch' && (
        <li className='py-2 text-xs text-amber-600'>
          {t('IQ Echo mismatch', {
            echoed: modelEcho.echoed,
            requested: modelEcho.requested,
          })}
        </li>
      )}
    </>
  )
}

function QuestionDetails(props: { detail: string }) {
  const { t } = useTranslation()
  let parsed: unknown
  try {
    parsed = JSON.parse(props.detail)
  } catch {
    return (
      <p role='alert' className='text-destructive text-xs'>
        {t('IQ Detail invalid')}
      </p>
    )
  }
  const details = unwrapDetail(parsed)
  const categories: Record<string, string> = {
    math: t('IQ Math'),
    math_hard: t('IQ Hard Math'),
    logic: t('IQ Logic'),
    logic_hard: t('IQ Hard Logic'),
    common: t('IQ Knowledge'),
    instruction: t('IQ Instructions'),
  }
  const skipped = details.filter(isProbeFailure).length
  return (
    <ul className='max-h-80 divide-y overflow-auto'>
      <PreferenceEchoSummary parsed={parsed} />
      {skipped > 0 && (
        <li className='text-muted-foreground py-2 text-xs'>
          {t('IQ Skipped questions', { count: skipped })}
        </li>
      )}
      {details.map((item) => {
        const failed = isProbeFailure(item)
        return (
          <li key={item.qid} className='space-y-1 py-2 text-xs'>
            <div className='flex flex-wrap items-center gap-2'>
              <span className='font-mono'>{item.qid}</span>
              <span className='text-muted-foreground'>
                {categories[item.category] ?? item.category}
              </span>
              {failed ? (
                <span className='text-amber-600'>
                  {t('IQ Not answered')}
                  {item.attempts && item.attempts > 1
                    ? ` ×${item.attempts}`
                    : ''}
                </span>
              ) : (
                <span
                  className={item.passed ? 'text-emerald-600' : 'text-rose-600'}
                >
                  {item.passed ? t('IQ Correct') : t('IQ Incorrect')}
                </span>
              )}
            </div>
            {!failed && (
              <p className='break-words whitespace-pre-wrap'>
                {item.answer_excerpt || '-'}
              </p>
            )}
            {failed && (
              <p className='text-muted-foreground break-words'>
                {item.failure_class || item.error_class}
                {item.http_status ? ` (${item.http_status})` : ''}
                {item.error_snippet ? ` · ${item.error_snippet}` : ''}
              </p>
            )}
          </li>
        )
      })}
    </ul>
  )
}

function ResultDetail(props: { result: IQResult }) {
  const { t } = useTranslation()
  return (
    <div className='grid gap-4 p-3 sm:grid-cols-2'>
      <dl className='grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2 text-xs'>
        <dt className='text-muted-foreground'>{t('IQ Run')}</dt>
        <dd className='font-mono break-all'>{props.result.run_id}</dd>
        <dt className='text-muted-foreground'>{t('IQ Upstream model')}</dt>
        <dd className='break-all'>{props.result.upstream_model || '-'}</dd>
        <dt className='text-muted-foreground'>{t('IQ Correct answers')}</dt>
        <dd>
          {props.result.correct_count} / {props.result.total_questions}
        </dd>
        <dt className='text-muted-foreground'>{t('IQ Duration')}</dt>
        <dd>{(props.result.duration_ms / 1000).toFixed(1)} s</dd>
        <dt className='text-muted-foreground'>{t('IQ Action reason')}</dt>
        <dd className='break-words whitespace-pre-wrap'>
          {props.result.action_reason || '-'}
        </dd>
        <dt className='text-muted-foreground'>{t('Priority')}</dt>
        <dd>
          {props.result.priority_before ?? '-'} &rarr;{' '}
          {props.result.priority_after ?? '-'}
        </dd>
        <dt className='text-muted-foreground'>{t('IQ Error class')}</dt>
        <dd className='break-words'>{props.result.error_class || '-'}</dd>
      </dl>
      {props.result.detail && (
        <div className='min-w-0'>
          <p className='text-muted-foreground mb-2 text-xs'>
            {t('IQ Question details')}
          </p>
          <QuestionDetails detail={props.result.detail} />
        </div>
      )}
    </div>
  )
}

export function IQResults(props: {
  runID: string
  initialChannelID?: number
  clearRun: () => void
}) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [channel, setChannel] = useState(
    props.initialChannelID ? String(props.initialChannelID) : ''
  )
  const [model, setModel] = useState('')
  const [status, setStatus] = useState('all')
  const [filters, setFilters] = useState<{
    channel_id?: number
    model_name?: string
    status?: string
  }>({ channel_id: props.initialChannelID })
  const [expanded, setExpanded] = useState<number | null>(null)
  const query = useQuery({
    queryKey: [...iqKeys.results, props.runID, page, filters],
    queryFn: () =>
      getIQResults({
        page,
        page_size: 20,
        run_id: props.runID || undefined,
        ...filters,
      }),
    refetchInterval: 4000,
  })
  const rows = query.data?.items ?? []
  const actions: Record<string, string> = {
    none: t('IQ No action'),
    priority_down: t('IQ Priority down'),
    priority_up: t('IQ Priority up'),
    disabled: t('Disabled'),
    enabled: t('Enabled'),
    breaker_skip: t('IQ Breaker skipped'),
    skipped_conflict: t('IQ Conflict skipped'),
    reconciled: t('IQ Reconciled'),
  }
  return (
    <div className='space-y-4'>
      {props.runID && (
        <div className='flex min-w-0 items-center gap-2 text-xs'>
          <span className='text-muted-foreground shrink-0'>{t('IQ Run')}</span>
          <span className='truncate font-mono' title={props.runID}>
            {props.runID}
          </span>
          <IQIconButton label={t('Clear')} onClick={props.clearRun}>
            <X className='size-4' />
          </IQIconButton>
        </div>
      )}
      <form
        className='flex flex-wrap items-end gap-3'
        onSubmit={(event) => {
          event.preventDefault()
          setPage(1)
          setFilters({
            channel_id: channel ? Number(channel) : undefined,
            model_name: model.trim() || undefined,
            status: status === 'all' ? undefined : status,
          })
          setExpanded(null)
        }}
      >
        <div className='w-32 space-y-1.5'>
          <Label htmlFor='iq-filter-channel'>{t('Channel ID')}</Label>
          <Input
            id='iq-filter-channel'
            type='number'
            min={1}
            step={1}
            value={channel}
            onChange={(event) => setChannel(event.target.value)}
          />
        </div>
        <div className='min-w-36 flex-1 space-y-1.5 sm:max-w-64'>
          <Label htmlFor='iq-filter-model'>{t('Model')}</Label>
          <Input
            id='iq-filter-model'
            value={model}
            onChange={(event) => setModel(event.target.value)}
          />
        </div>
        <div className='w-36 space-y-1.5'>
          <Label htmlFor='iq-filter-status'>{t('Status')}</Label>
          <Select value={status} onValueChange={setStatus}>
            <SelectTrigger id='iq-filter-status'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>{t('All')}</SelectItem>
              <SelectItem value='success'>{t('IQ Valid')}</SelectItem>
              <SelectItem value='invalid'>{t('IQ Invalid')}</SelectItem>
              <SelectItem value='error'>{t('IQ Error')}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <Button type='submit' variant='outline'>
          <Search className='size-4' />
          {t('Search')}
        </Button>
      </form>
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
                <TableHead className='w-10' />
                <TableHead>{t('Channel ID')}</TableHead>
                <TableHead>{t('Model')}</TableHead>
                <TableHead>{t('IQ score')}</TableHead>
                <TableHead>{t('IQ Baseline')}</TableHead>
                <TableHead>{t('IQ Margin')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead>{t('IQ Action')}</TableHead>
                <TableHead>{t('IQ Finished at')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((result) => (
                <Fragment key={result.id}>
                  <TableRow>
                    <TableCell>
                      <IQIconButton
                        label={
                          expanded === result.id
                            ? t('Collapse')
                            : t('IQ View details')
                        }
                        onClick={() =>
                          setExpanded(expanded === result.id ? null : result.id)
                        }
                      >
                        {expanded === result.id ? (
                          <ChevronDown className='size-4' />
                        ) : (
                          <ChevronRight className='size-4' />
                        )}
                      </IQIconButton>
                    </TableCell>
                    <TableCell>#{result.channel_id}</TableCell>
                    <TableCell
                      className='max-w-60 truncate font-mono text-xs'
                      title={result.requested_model}
                    >
                      {result.requested_model}
                    </TableCell>
                    <TableCell className='font-medium tabular-nums'>
                      {result.score ?? '-'}
                    </TableCell>
                    <TableCell className='tabular-nums'>
                      {result.baseline_score_snapshot}
                    </TableCell>
                    <TableCell className='tabular-nums'>
                      {result.margin ?? '-'}
                    </TableCell>
                    <TableCell>
                      <IQStatus status={result.status} />
                    </TableCell>
                    <TableCell className='text-xs'>
                      {actions[result.action] ?? result.action}
                    </TableCell>
                    <TableCell className='text-xs'>
                      {iqTime(result.finished_at)}
                    </TableCell>
                  </TableRow>
                  {expanded === result.id && (
                    <TableRow className='bg-muted/20 hover:bg-muted/20'>
                      <TableCell colSpan={9} className='whitespace-normal'>
                        <ResultDetail result={result} />
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              ))}
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

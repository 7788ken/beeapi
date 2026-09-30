import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { CircleCheckBig, CircleSlash, TriangleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { postGovernanceDryRun, type GovernanceDryRunResult } from './api'

/**
 * 判定试算：输入状态码 + 错误文案，问后端「这条错误现在会触发什么」。
 *
 * 判定一律由后端 /api/channel/governance/dry_run 做，前端**不重新实现一套规则**——
 * 这整轮重构就是为了消除判定语义分裂，前端自己算一遍必然与后端漂移。
 * 该接口纯判定、零副作用：不写库、不改渠道状态、不触发处置、不发通知。
 */

type Verdict = {
  key: string
  label: string
  hit: boolean
  detail?: string
}

function VerdictRow({ verdict }: { verdict: Verdict }) {
  const Icon = verdict.hit ? CircleCheckBig : CircleSlash
  return (
    <li className='flex items-start gap-2'>
      <Icon
        className={
          verdict.hit
            ? 'text-primary mt-0.5 size-4 shrink-0'
            : 'text-muted-foreground mt-0.5 size-4 shrink-0'
        }
        aria-hidden='true'
      />
      <span className='space-y-0.5'>
        <span
          className={
            verdict.hit ? 'font-medium' : 'text-muted-foreground line-through'
          }
        >
          {verdict.label}
        </span>
        {verdict.detail && (
          <span className='text-muted-foreground block text-xs'>
            {verdict.detail}
          </span>
        )}
      </span>
    </li>
  )
}

export function ClassificationDryRun() {
  const { t } = useTranslation()
  const [statusCode, setStatusCode] = useState('404')
  const [errorMessage, setErrorMessage] = useState('')
  const [modelName, setModelName] = useState('')

  const mutation = useMutation({
    mutationFn: () =>
      postGovernanceDryRun({
        status_code: Number(statusCode),
        error_message: errorMessage,
        model: modelName.trim() || undefined,
      }),
  })

  const parsedStatus = Number(statusCode)
  const statusValid =
    Number.isInteger(parsedStatus) && parsedStatus >= 100 && parsedStatus <= 599

  const result: GovernanceDryRunResult | undefined = mutation.data?.success
    ? mutation.data.data
    : undefined
  const errorText = mutation.data && !mutation.data.success
    ? (mutation.data.message ?? t('Dry run failed'))
    : mutation.isError
      ? t('Dry run failed')
      : undefined

  const reasonLabels: Record<string, string> = {
    model_missing: t('Not offered by upstream'),
    rate_limit: t('Rate limited'),
    forbidden: t('Access forbidden'),
  }

  const immediateVerdicts: Verdict[] = result
    ? [
        {
          key: 'removal',
          label: t('Remove just this model from the channel'),
          hit: result.removal.matched,
          detail: result.removal.matched
            ? [
                t('Reason: {{reason}}', {
                  reason:
                    reasonLabels[result.removal.reason] ??
                    result.removal.reason,
                }),
                result.removal.requires_upstream_verify
                  ? t(
                      'Still needs the upstream model list to confirm the model is really gone'
                    )
                  : t('No upstream existence check on this path'),
                t('Recheck in {{seconds}}s', {
                  seconds: result.removal.recheck_interval_seconds,
                }),
                result.removal.cap_applies
                  ? t('Counts toward the per-channel cap of {{cap}}', {
                      cap: result.removal.cap_value,
                    })
                  : t('Not bound by the per-channel removal cap'),
              ].join(' · ')
            : !result.removal.model_provided
              ? t(
                  'No model name given, so the model-missing path cannot match at all — the check needs the error text to mention the model.'
                )
              : t('No removal path matches, or its switch is off'),
        },
        {
          key: 'disable',
          label: t('Stop the whole channel'),
          hit:
            result.channel_disable.would_disable_channel &&
            !result.channel_disable.short_circuited_by_removal,
          detail: result.channel_disable.short_circuited_by_removal
            ? t(
                'Model-level removal takes this error first, so the channel is not stopped now. If the removal cannot go through, this same verdict applies as a fallback — and it currently says {{verdict}}.',
                {
                  verdict: result.channel_disable.would_disable_channel
                    ? t('it would stop the channel')
                    : t('it would not stop the channel'),
                }
              )
            : !result.channel_disable.automatic_disable_channel_enabled
              ? t('The global auto-disable switch is off')
              : result.channel_disable.would_disable_channel
                ? t(
                    'The per-channel auto-ban flag is not evaluated here — a dry run has no channel to look at, and a channel with auto-ban off is never stopped.'
                  )
                : t('Neither a status code nor a failure keyword matches'),
        },
      ]
    : []

  const pipelineVerdicts: Verdict[] = result
    ? [
        {
          key: 'streak',
          label: t('Counts toward the degrade error streak'),
          hit: result.degrade_streak.effective,
          detail: !result.degrade_streak.channel_health_enabled
            ? result.degrade_streak.countable
              ? t(
                  'This error is classified as countable, but channel health is off so nothing is counted at all.'
                )
              : t('Channel health is off, and this error is not countable anyway')
            : t('Channel health is on and this error is countable'),
        },
        {
          key: 'retry',
          label: t('Retryable by status code'),
          hit: result.retry.status_code_retryable,
          detail: t(
            'Only the status-code gate is evaluated. Retry budget, channel affinity and retry scope depend on the request, not on the code and text.'
          ),
        },
      ]
    : []

  return (
    <div className='space-y-4'>
      <div className='space-y-1'>
        <h4 className='text-sm font-semibold'>{t('Try a verdict')}</h4>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Paste an upstream error and see what the rules above would do with it right now. The verdict is computed by the same functions the live request path uses, so it cannot drift from the real behaviour. Nothing is written and no channel is touched.'
          )}
        </p>
      </div>

      <div className='grid gap-4 md:grid-cols-3'>
        <div className='space-y-2'>
          <Label htmlFor='dry-run-status'>{t('Status code')}</Label>
          <Input
            id='dry-run-status'
            type='number'
            min={100}
            max={599}
            value={statusCode}
            onChange={(event) => setStatusCode(event.target.value)}
          />
          {!statusValid && statusCode !== '' && (
            <p className='text-destructive text-xs'>
              {t('Enter a status code between 100 and 599')}
            </p>
          )}
        </div>
        <div className='space-y-2 md:col-span-2'>
          <Label htmlFor='dry-run-message'>{t('Error text')}</Label>
          <Input
            id='dry-run-message'
            placeholder={t('e.g. The model `gpt-5` does not exist')}
            value={errorMessage}
            onChange={(event) => setErrorMessage(event.target.value)}
          />
        </div>
      </div>

      <div className='space-y-2 md:max-w-sm'>
        <Label htmlFor='dry-run-model'>{t('Model name (optional)')}</Label>
        <Input
          id='dry-run-model'
          placeholder='gpt-5'
          value={modelName}
          onChange={(event) => setModelName(event.target.value)}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'The model-missing verdict compares the error text against this name, so leave it out and that path can never match.'
          )}
        </p>
      </div>

      <Button
        type='button'
        variant='secondary'
        disabled={!statusValid || mutation.isPending}
        onClick={() => mutation.mutate()}
      >
        {mutation.isPending ? t('Running...') : t('Run dry run')}
      </Button>

      {errorText && (
        <p className='text-destructive flex items-start gap-2 text-sm'>
          <TriangleAlert className='mt-0.5 size-4 shrink-0' aria-hidden='true' />
          {errorText}
        </p>
      )}

      {result && (
        <div className='grid gap-4 rounded-lg border p-4 md:grid-cols-2'>
          <div className='space-y-2'>
            <p className='text-sm font-medium'>{t('Immediate disposition')}</p>
            <ul className='space-y-2 text-sm'>
              {immediateVerdicts.map((verdict) => (
                <VerdictRow key={verdict.key} verdict={verdict} />
              ))}
            </ul>
          </div>
          <div className='space-y-2'>
            <p className='text-sm font-medium'>{t('Knock-on effects')}</p>
            <ul className='space-y-2 text-sm'>
              {pipelineVerdicts.map((verdict) => (
                <VerdictRow key={verdict.key} verdict={verdict} />
              ))}
            </ul>
          </div>
        </div>
      )}
    </div>
  )
}

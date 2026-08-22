import { useEffect, useMemo, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  Clock,
  Database,
  Download,
  Fingerprint,
  Gauge,
  Loader2,
  PlayCircle,
  Plug,
  ShieldCheck,
  StopCircle,
  XCircle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { cancelVerifyReport, getVerifyReport, listVerifyReports, verifyChannel } from '../../api'
import { channelsQueryKeys, parseModelsList } from '../../lib'
import { useChannels } from '../channels-provider'
import type {
  VerifyCache,
  VerifyCompat,
  VerifyDimension,
  VerifyFingerprint,
  VerifyHealth,
  VerifyReportDetail,
  VerifyReportSummary,
  VerifySSEEvent,
} from '../../types'

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

// ───────────────────────── helpers ─────────────────────────

function gradeBg(grade: string | undefined): string {
  switch ((grade || '').toUpperCase()) {
    case 'A+':
    case 'A':
      return 'bg-emerald-500 text-white'
    case 'B':
      return 'bg-blue-500 text-white'
    case 'C':
      return 'bg-amber-500 text-white'
    case 'D':
      return 'bg-orange-500 text-white'
    case 'F':
      return 'bg-rose-600 text-white'
    default:
      return 'bg-zinc-400 text-white'
  }
}

function dimStatusClass(status: string | undefined): string {
  switch (status) {
    case 'pass':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300'
    case 'warning':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300'
    case 'fail':
      return 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'
    default:
      return 'bg-muted text-muted-foreground'
  }
}

function historyStatusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  switch (status) {
    case 'success':
      return 'default'
    case 'running':
      return 'secondary'
    case 'failed':
      return 'destructive'
    default:
      return 'outline'
  }
}

type VerdictMeta = { key: string; tone: string }
function verdictMeta(verdict: string | undefined): VerdictMeta {
  switch (verdict) {
    case 'official_anthropic':
      return { key: 'Official direct', tone: 'text-emerald-600 dark:text-emerald-400' }
    case 'aws_bedrock':
      return { key: 'AWS Bedrock', tone: 'text-blue-600 dark:text-blue-400' }
    case 'max_subscription':
      return { key: 'Max subscription', tone: 'text-blue-600 dark:text-blue-400' }
    case 'ide_reverse_proxy':
      return { key: 'IDE reverse proxy', tone: 'text-amber-600 dark:text-amber-400' }
    // OpenAI 端 verdict
    case 'official_openai':
      return { key: 'Official OpenAI', tone: 'text-emerald-600 dark:text-emerald-400' }
    case 'azure_openai':
      return { key: 'Azure OpenAI', tone: 'text-blue-600 dark:text-blue-400' }
    case 'chatgpt_reverse':
      return { key: 'ChatGPT reverse proxy', tone: 'text-amber-600 dark:text-amber-400' }
    case 'thirdparty_proxy':
      return { key: 'Third-party proxy', tone: 'text-rose-600 dark:text-rose-400' }
    case 'unknown_proxy':
      return { key: 'Unknown proxy', tone: 'text-rose-600 dark:text-rose-400' }
    default:
      return { key: 'Unknown proxy', tone: 'text-muted-foreground' }
  }
}

// 可测评渠道类型 → model 前缀（与后端 service verifyProviderWhitelist 对齐）
const VERIFY_MODEL_PREFIXES: Record<number, string[]> = {
  14: ['claude-'], // Anthropic → /api/verify/claude
  1: ['gpt-', 'codex-', 'o1', 'o3', 'o4', 'openai/'], // OpenAI → /api/verify/openai
}

const pct = (x: number | undefined): string =>
  x === undefined || x === null ? '—' : `${Math.round(x * 100)}%`

const fmtDuration = (ms: number | undefined): string =>
  ms && ms > 0 ? `${(ms / 1000).toFixed(1)}s` : '—'

// ───────────────────────── small presentational pieces ─────────────────────────

function MetricCard({
  label,
  value,
  tone,
}: {
  label: string
  value: React.ReactNode
  tone?: string
}) {
  return (
    <div className='rounded-md bg-muted/60 px-3 py-2'>
      <div className='text-[11px] text-muted-foreground truncate'>{label}</div>
      <div className={cn('text-lg font-medium tabular-nums', tone)}>{value}</div>
    </div>
  )
}

function KV({ k, v }: { k: string; v: React.ReactNode }) {
  return (
    <div className='flex items-start justify-between gap-3 border-b py-1.5 text-xs last:border-b-0'>
      <span className='text-muted-foreground shrink-0'>{k}</span>
      <span className='text-right font-mono break-all'>{v}</span>
    </div>
  )
}

// ───────────────────────── tab panels ─────────────────────────

function OverviewPanel({
  report,
  health,
  cache,
  fp,
}: {
  report: VerifyReportDetail
  health?: VerifyHealth
  cache?: VerifyCache | null
  fp?: VerifyFingerprint | null
}) {
  const { t } = useTranslation()
  const dims = health?.dimensions || []
  const passed = dims.filter((d) => d.status === 'pass').length
  const issues = dims.filter((d) => d.status && d.status !== 'pass')
  const grade = health?.grade || report.grade
  const conclusion = fp?.findings?.conclusion
  const recommend =
    grade === 'A+' || grade === 'A'
      ? 'Recommended use'
      : grade === 'B'
        ? 'Usable'
        : grade === 'C'
          ? 'Use with caution'
          : grade === 'D'
            ? 'Not recommended'
            : 'Unavailable'

  return (
    <div className='space-y-4'>
      <div className='grid grid-cols-2 gap-2 sm:grid-cols-3'>
        {dims.length > 0 && (
          <MetricCard label={t('Dimensions passed')} value={`${passed} / ${dims.length}`} />
        )}
        {cache && (
          <>
            <MetricCard
              label={t('Cache hit (identical)')}
              value={pct(cache.hit_rate_identical)}
              tone={
                (cache.hit_rate_identical ?? 0) >= 0.9
                  ? 'text-emerald-600 dark:text-emerald-400'
                  : 'text-amber-600 dark:text-amber-400'
              }
            />
            <MetricCard
              label={t('Cache hit (vary-tail)')}
              value={pct(cache.hit_rate_vary_tail)}
              tone={
                (cache.hit_rate_vary_tail ?? 0) < 0.5
                  ? 'text-rose-600 dark:text-rose-400'
                  : 'text-emerald-600 dark:text-emerald-400'
              }
            />
          </>
        )}
        {fp?.timing && (
          <>
            <MetricCard label={t('Output rate')} value={`${fp.timing.tokens_per_second ?? '—'} tok/s`} />
            <MetricCard label={t('First token (TTFT)')} value={`${fp.timing.ttft_ms ?? '—'} ms`} />
            <MetricCard label={t('Output tokens')} value={fp.timing.output_tokens ?? health?.output_token_sum ?? '—'} />
          </>
        )}
      </div>

      {conclusion && (
        <div className='rounded-md border border-l-[3px] border-l-primary p-3 text-sm leading-relaxed'>
          <div className='mb-1 flex items-center gap-2 font-medium'>
            <Badge className={cn('rounded-md', gradeBg(grade))}>{t(recommend)}</Badge>
            {t('Conclusion')}
          </div>
          {conclusion}
          {fp?.findings?.relay_info && (
            <div className='mt-1 text-xs text-muted-foreground'>
              {t('Relay chain')}: {fp.findings.relay_info}
            </div>
          )}
        </div>
      )}

      {fp?.injection?.detected_injection && (
        <div className='flex items-start gap-2 rounded-md bg-rose-50 p-3 text-xs text-rose-700 dark:bg-rose-950 dark:text-rose-300'>
          <AlertTriangle className='mt-0.5 size-4 shrink-0' />
          <div className='min-w-0'>
            <div className='font-medium'>
              {t('System prompt injection detected (confidence {{level}})', {
                level: fp.injection.confidence || '?',
              })}
            </div>
            {fp.injection.injected_content && (
              <div className='mt-1 break-all font-mono opacity-80'>{fp.injection.injected_content}</div>
            )}
          </div>
        </div>
      )}

      {issues.length > 0 && (
        <div>
          <div className='mb-1 text-xs text-muted-foreground'>{t('Issues to note')}</div>
          <div className='space-y-1'>
            {issues.map((d) => (
              <div
                key={d.dimension_id}
                className={cn(
                  'text-xs leading-relaxed',
                  d.status === 'fail'
                    ? 'text-rose-600 dark:text-rose-400'
                    : 'text-amber-600 dark:text-amber-400'
                )}
              >
                · {d.name || `#${d.dimension_id}`}
                {d.detail ? `：${d.detail}` : ''}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function DimensionsPanel({ dims }: { dims: VerifyDimension[] }) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState<Set<number>>(new Set())
  const toggle = (id: number) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })

  const counts = {
    pass: dims.filter((d) => d.status === 'pass').length,
    warning: dims.filter((d) => d.status === 'warning').length,
    fail: dims.filter((d) => d.status === 'fail').length,
  }

  return (
    <div className='space-y-2'>
      <div className='flex flex-wrap gap-3 text-[11px] text-muted-foreground'>
        <span className='text-emerald-600 dark:text-emerald-400'>
          {t('Pass')} {counts.pass}
        </span>
        <span className='text-amber-600 dark:text-amber-400'>
          {t('Warning')} {counts.warning}
        </span>
        <span className='text-rose-600 dark:text-rose-400'>
          {t('Fail')} {counts.fail}
        </span>
      </div>
      {dims.map((d) => {
        const open = expanded.has(d.dimension_id)
        const hasMore = !!(d.detail || d.evidence)
        return (
          <div key={d.dimension_id} className='overflow-hidden rounded-md border'>
            <button
              type='button'
              onClick={() => hasMore && toggle(d.dimension_id)}
              className={cn(
                'flex w-full items-center gap-3 px-3 py-2 text-left text-sm',
                hasMore && 'hover:bg-muted'
              )}
            >
              <span className='w-6 shrink-0 text-xs tabular-nums text-muted-foreground'>
                #{d.dimension_id}
              </span>
              <span className='min-w-0 flex-1 break-words'>{d.name || `Dim ${d.dimension_id}`}</span>
              {d.weight !== undefined && (
                <span className='shrink-0 text-[11px] text-muted-foreground'>
                  {t('Weight')} {d.weight}
                </span>
              )}
              <span
                className={cn(
                  'shrink-0 rounded-md px-2 py-0.5 text-[11px] font-medium',
                  dimStatusClass(d.status)
                )}
              >
                {d.status === 'pass'
                  ? t('Pass')
                  : d.status === 'warning'
                    ? t('Warning')
                    : d.status === 'fail'
                      ? t('Fail')
                      : '-'}
              </span>
              {hasMore && (
                <ChevronDown
                  className={cn(
                    'size-4 shrink-0 text-muted-foreground transition-transform',
                    open && 'rotate-180'
                  )}
                />
              )}
            </button>
            {open && (
              <div className='space-y-2 px-3 pb-3 pl-11 text-xs leading-relaxed text-muted-foreground'>
                {d.detail && <div className='whitespace-pre-wrap break-words'>{d.detail}</div>}
                {d.evidence && (
                  <div className='rounded-md bg-muted/60 px-3 py-2'>
                    <div className='mb-1 font-medium text-foreground'>{t('Evidence')}</div>
                    {Object.entries(d.evidence).map(([key, val]) => (
                      <KV
                        key={key}
                        k={key}
                        v={Array.isArray(val) ? val.join(' · ') : String(val)}
                      />
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

function CachePanel({ cache }: { cache: VerifyCache }) {
  const { t } = useTranslation()
  const low = (cache.hit_rate_vary_tail ?? 1) < 0.5
  return (
    <div className='space-y-3'>
      <div className='grid grid-cols-2 gap-2'>
        <MetricCard
          label={t('Identical hit rate')}
          value={pct(cache.hit_rate_identical)}
          tone={
            (cache.hit_rate_identical ?? 0) >= 0.9
              ? 'text-emerald-600 dark:text-emerald-400'
              : 'text-amber-600 dark:text-amber-400'
          }
        />
        <MetricCard
          label={t('Vary-tail hit rate')}
          value={pct(cache.hit_rate_vary_tail)}
          tone={low ? 'text-rose-600 dark:text-rose-400' : 'text-emerald-600 dark:text-emerald-400'}
        />
        <MetricCard label={t('Warm identical')} value={pct(cache.warm_hit_rate_identical)} />
        <MetricCard
          label={t('Warm vary-tail')}
          value={pct(cache.warm_hit_rate_vary_tail)}
          tone={
            (cache.warm_hit_rate_vary_tail ?? 1) < 0.5
              ? 'text-rose-600 dark:text-rose-400'
              : undefined
          }
        />
      </div>
      <div className='text-xs leading-relaxed text-muted-foreground'>
        {low
          ? t(
              'Cache vary-tail hit rate is low; long-prefix requests cannot reuse cache, raising cost — common on relays that rewrite the request body.'
            )
          : t('Cache path is healthy; both prefix and vary-tail hit effectively.')}
      </div>
    </div>
  )
}

function CompatPanel({ compat }: { compat: VerifyCompat }) {
  const { t } = useTranslation()
  return (
    <div className='space-y-4'>
      {!!compat.probes?.length && (
        <div>
          <div className='mb-1.5 text-xs text-muted-foreground'>{t('Protocol probes')}</div>
          <div className='space-y-1.5'>
            {compat.probes.map((p) => (
              <div key={p.name} className='flex items-start justify-between gap-3 border-b pb-1.5 text-xs'>
                <span className='min-w-0 font-mono break-all'>
                  {p.name}{' '}
                  <span className='rounded-md bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground'>
                    {p.type === 'required' ? t('Required') : t('Optional')}
                  </span>
                </span>
                <span className='max-w-[55%] shrink-0 text-right'>
                  <span
                    className={cn(
                      'rounded-md px-2 py-0.5 text-[11px] font-medium',
                      p.pass
                        ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300'
                        : 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'
                    )}
                  >
                    {p.pass ? 'PASS' : 'FAIL'}
                  </span>
                  {p.reason && (
                    <div className='mt-1 text-[11px] text-muted-foreground break-words'>{p.reason}</div>
                  )}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
      {!!compat.recommendations?.length && (
        <div>
          <div className='mb-1.5 text-xs text-muted-foreground'>{t('Recommended channel config')}</div>
          <div className='space-y-1'>
            {compat.recommendations.map((r) => (
              <div key={r.field_name} className='border-b py-1.5'>
                <div className='flex items-center justify-between gap-3'>
                  <span className='font-mono text-xs'>{r.field_name}</span>
                  <span className='text-xs font-medium'>{String(r.value)}</span>
                </div>
                {r.reason_zh && (
                  <div className='mt-0.5 text-[11px] text-muted-foreground break-words'>{r.reason_zh}</div>
                )}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function SourcePanel({ fp }: { fp: VerifyFingerprint }) {
  const { t } = useTranslation()
  const v = verdictMeta(fp.verdict)
  const bool = (b: boolean | undefined) => (b ? t('Yes') : t('No'))
  return (
    <div className='space-y-4'>
      <div className='flex items-center gap-2'>
        <Fingerprint className={cn('size-5', v.tone)} />
        <span className={cn('text-sm font-medium', v.tone)}>{t(v.key)}</span>
        {fp.confidence !== undefined && (
          <span className='text-xs text-muted-foreground'>
            {t('Confidence')} {pct(fp.confidence)}
          </span>
        )}
      </div>

      {!!fp.signals?.length && (
        <div className='flex flex-wrap gap-1.5'>
          {fp.signals.map((s, i) => (
            <span key={i} className='rounded-md bg-muted px-2 py-0.5 font-mono text-[11px] text-muted-foreground'>
              {s}
            </span>
          ))}
        </div>
      )}

      {!!fp.baseline_matches?.length && (
        <div>
          <div className='mb-1.5 text-xs text-muted-foreground'>{t('Baseline matches')}</div>
          <div className='space-y-1.5'>
            {fp.baseline_matches.map((b, i) => (
              <div key={b.id || i} className='rounded-md border px-3 py-2'>
                <div className='flex items-center justify-between text-xs'>
                  <span className='font-medium'>{b.label || b.source}</span>
                  <span
                    className={cn(
                      (b.match_rate ?? 0) >= 70
                        ? 'text-emerald-600 dark:text-emerald-400'
                        : (b.match_rate ?? 0) >= 40
                          ? 'text-amber-600 dark:text-amber-400'
                          : 'text-muted-foreground'
                    )}
                  >
                    {b.match_rate ?? 0}% · {b.score ?? 0}/{b.max_score ?? 0}
                  </span>
                </div>
                {!!b.matched_signals?.length && (
                  <div className='mt-1 text-[11px] text-emerald-600 break-words dark:text-emerald-400'>
                    ✓ {b.matched_signals.join(' · ')}
                  </div>
                )}
                {!!b.missed_signals?.length && (
                  <div className='text-[11px] text-rose-600 break-words dark:text-rose-400'>
                    ✗ {b.missed_signals.join(' · ')}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {!!fp.findings?.key_findings?.length && (
        <div>
          <div className='mb-1.5 text-xs text-muted-foreground'>{t('Key findings')}</div>
          <div className='space-y-2'>
            {fp.findings.key_findings.map((f, i) => (
              <div key={i} className='text-xs'>
                <div className='font-medium'>
                  {i + 1}. {f.title}
                </div>
                {f.detail && <div className='leading-relaxed text-muted-foreground break-words'>{f.detail}</div>}
                {f.evidence && (
                  <div className='mt-1 inline-block break-all rounded-md bg-muted px-2 py-0.5 font-mono text-[11px] text-muted-foreground'>
                    {f.evidence}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      <div>
        <div className='mb-1.5 text-xs text-muted-foreground'>{t('Signal details')}</div>
        {fp.model_echo && (
          <KV
            k={t('Model echo')}
            v={`${fp.model_echo.sent_model || '?'} → ${fp.model_echo.received_model || '?'} ${
              fp.model_echo.matches ? t('Consistent') : t('Inconsistent')
            }`}
          />
        )}
        {fp.headers && (
          <>
            <KV k={t('Anthropic rate-limit headers')} v={bool(fp.headers.has_anthropic_ratelimit)} />
            <KV k={t('request-id format')} v={fp.headers.request_id_format || '—'} />
            <KV k={t('Server header')} v={fp.headers.server_header || '—'} />
            <KV
              k={t('Custom headers')}
              v={fp.headers.custom_headers?.length ? fp.headers.custom_headers.join(', ') : t('None')}
            />
          </>
        )}
        {fp.usage_shape && (
          <>
            <KV
              k={t('usage shape')}
              v={fp.usage_shape.uses_camel_case ? t('camelCase (non-official)') : t('snake_case (official)')}
            />
            <KV k={t('cache_creation field')} v={fp.usage_shape.has_cache_creation ? t('Present') : t('Missing')} />
          </>
        )}
        {fp.beta_support && (
          <KV
            k={t('Thinking / interleaved thinking')}
            v={`${bool(fp.beta_support.thinking)} / ${bool(fp.beta_support.interleaved_thinking)}`}
          />
        )}
        {fp.findings?.relay_info && <KV k={t('Relay chain')} v={fp.findings.relay_info} />}
      </div>
    </div>
  )
}

// ───────────────────────── main dialog ─────────────────────────

export function ChannelVerifyDialog({ open, onOpenChange }: Props) {
  const { t } = useTranslation()
  const { currentRow } = useChannels()
  const queryClient = useQueryClient()

  const channel = currentRow
  // 可测评模型前缀（与后端 verifyProviderWhitelist 对齐）：Anthropic(14)→claude-；OpenAI(1)→gpt-/codex-/o*
  const verifiableModels = useMemo(() => {
    if (!channel) return []
    const prefixes = VERIFY_MODEL_PREFIXES[channel.type] || []
    if (prefixes.length === 0) return []
    return parseModelsList(channel.models).filter((m) =>
      prefixes.some((p) => m.startsWith(p))
    )
  }, [channel])

  const [selectedModel, setSelectedModel] = useState<string>('')
  const [running, setRunning] = useState(false)
  const [currentReport, setCurrentReport] = useState<VerifyReportDetail | null>(null)
  const [history, setHistory] = useState<VerifyReportSummary[]>([])
  const [queueInfo, setQueueInfo] = useState<{ waiting: boolean; position: number }>({
    waiting: false,
    position: 0,
  })
  const [error, setError] = useState<string>('')
  const [backgroundRunning, setBackgroundRunning] = useState(false)
  const [tab, setTab] = useState<string>('overview')
  const abortRef = useRef<(() => void) | null>(null)

  // 初始化：选默认模型 + 加载历史 + 默认展示最新报告
  useEffect(() => {
    if (!open || !channel) return
    setSelectedModel((prev) => {
      if (prev && verifiableModels.includes(prev)) return prev
      return verifiableModels[0] || ''
    })
    setError('')
    setQueueInfo({ waiting: false, position: 0 })
    setBackgroundRunning(false)
    setTab('overview')
    listVerifyReports(channel.id, 1, 50)
      .then((res) => {
        if (res.success && res.data) {
          setHistory(res.data.items)
          const latest = res.data.items[0]
          if (latest && latest.status === 'running' && Date.now() / 1000 - latest.tested_at < 300) {
            setBackgroundRunning(true)
          }
        }
      })
      .catch(() => {})
    if (channel.verify_report_id && channel.verify_report_id > 0) {
      getVerifyReport(channel.verify_report_id)
        .then((res) => {
          if (res.success && res.data) setCurrentReport(res.data)
        })
        .catch(() => {})
    } else {
      setCurrentReport(null)
    }
  }, [open, channel, verifiableModels])

  // 关闭时取消客户端 fetch（释放浏览器连接），后端测评继续在后台跑直到完成。
  useEffect(() => {
    if (!open && abortRef.current) {
      abortRef.current()
      abortRef.current = null
      if (running) {
        toast.info(t('Verification continues in background. Result will appear in the list when ready.'))
        setTimeout(() => {
          queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
        }, 30000)
      }
      setRunning(false)
    }
  }, [open, running, queryClient, t])

  const refreshHistory = () => {
    if (!channel) return
    listVerifyReports(channel.id, 1, 50)
      .then((res) => {
        if (res.success && res.data) {
          setHistory(res.data.items)
          const latest = res.data.items[0]
          setBackgroundRunning(
            !!(latest && latest.status === 'running' && Date.now() / 1000 - latest.tested_at < 300)
          )
        }
      })
      .catch(() => {})
  }

  const handleStart = () => {
    if (!channel || !selectedModel) return
    setRunning(true)
    setError('')
    setTab('overview')
    setCurrentReport({
      id: 0,
      channel_id: channel.id,
      model: selectedModel,
      score: 0,
      grade: '',
      status: 'running',
      duration_ms: 0,
      tested_at: Math.floor(Date.now() / 1000),
      operator_id: 0,
      report: {},
    })
    setQueueInfo({ waiting: true, position: 0 })

    const merge = (patch: (prev: VerifyReportDetail) => VerifyReportDetail) =>
      setCurrentReport((prev) =>
        patch(
          prev || {
            id: 0,
            channel_id: channel.id,
            model: selectedModel,
            score: 0,
            grade: '',
            status: 'running',
            duration_ms: 0,
            tested_at: Math.floor(Date.now() / 1000),
            operator_id: 0,
            report: {},
          }
        )
      )

    const abort = verifyChannel(
      channel.id,
      selectedModel,
      (evt: VerifySSEEvent) => {
        if (evt.type === 'queued' || evt.type === 'acquired') {
          setQueueInfo({ waiting: evt.type === 'queued', position: evt.data?.position ?? 0 })
        }
        if (evt.type === 'report_created') {
          merge((prev) => ({ ...prev, id: evt.data.report_id }))
        }
        if (evt.type === 'health_score' || evt.type === 'early_stop') {
          merge((prev) => ({
            ...prev,
            score: evt.data?.total_score ?? prev.score,
            grade: evt.data?.grade ?? prev.grade,
            status: 'running',
            report: { ...prev.report, health: evt.data },
          }))
        }
        if (evt.type === 'cache_result') {
          merge((prev) => ({ ...prev, report: { ...prev.report, cache: evt.data } }))
        }
        if (evt.type === 'compat_result') {
          merge((prev) => ({ ...prev, report: { ...prev.report, compat: evt.data } }))
        }
        if (evt.type === 'fingerprint_result') {
          merge((prev) => ({ ...prev, report: { ...prev.report, fingerprint: evt.data } }))
        }
        if (evt.type === 'done') {
          merge((prev) => ({
            ...prev,
            score: evt.data?.health?.total_score ?? prev.score,
            grade: evt.data?.health?.grade ?? prev.grade,
            status: 'success',
            report: { ...prev.report, ...evt.data },
          }))
        }
        if (evt.type === 'error') {
          setError(evt.data?.message || 'unknown error')
        }
      },
      (err) => {
        setError(err.message)
        setRunning(false)
        abortRef.current = null
      },
      () => {
        setRunning(false)
        abortRef.current = null
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
        refreshHistory()
      }
    )
    abortRef.current = abort
  }

  const handleLoadHistory = async (reportId: number) => {
    if (running) return
    try {
      const res = await getVerifyReport(reportId)
      if (res.success && res.data) {
        setCurrentReport(res.data)
        setTab('overview')
      } else {
        toast.error(res.message || t('Failed to load report'))
      }
    } catch (err) {
      toast.error((err as Error).message)
    }
  }

  const handleCancelReport = async (reportId: number) => {
    if (!confirm(t('Stop this running verification?'))) return
    try {
      const res = await cancelVerifyReport(reportId)
      if (!res.success) {
        toast.error(res.message || t('Failed to stop verification'))
        return
      }
      toast.success(t('Verification stopped'))
      setTimeout(() => {
        refreshHistory()
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      }, 500)
    } catch (err) {
      toast.error((err as Error).message)
    }
  }

  const handleExport = () => {
    if (!currentReport) return
    const blob = new Blob([JSON.stringify(currentReport, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `verify-report-${channel?.id ?? 0}-${currentReport.id || 'live'}.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  // 历史栏：后端已按 tested_at DESC（最新置顶）；运行中的实时报告再置顶并去重。
  const displayHistory = useMemo<VerifyReportSummary[]>(() => {
    if (!running || !currentReport) return history
    const live: VerifyReportSummary = {
      id: currentReport.id,
      channel_id: currentReport.channel_id,
      model: currentReport.model,
      score: currentReport.score,
      grade: currentReport.grade,
      status: 'running',
      duration_ms: 0,
      tested_at: currentReport.tested_at,
      operator_id: 0,
    }
    return [live, ...history.filter((h) => h.id !== live.id)]
  }, [running, currentReport, history])

  const report = currentReport?.report
  const health = report?.health
  const cache = report?.cache
  const compat = report?.compat
  const fp = report?.fingerprint
  const dims = health?.dimensions || []
  const grade = health?.grade || currentReport?.grade
  const score = health?.total_score ?? currentReport?.score
  const hasReport = !!currentReport && (!!health || currentReport.status === 'success')

  const availableTabs = useMemo(() => {
    const tabs: Array<{ key: string; label: string; badge?: string }> = []
    if (hasReport) tabs.push({ key: 'overview', label: t('Overview') })
    if (dims.length) tabs.push({ key: 'dimensions', label: t('Dimensions'), badge: String(dims.length) })
    if (cache) tabs.push({ key: 'cache', label: t('Cache') })
    if (compat?.probes?.length)
      tabs.push({
        key: 'compat',
        label: t('Compatibility'),
        badge: `${compat.probes.filter((p) => p.pass).length}/${compat.probes.length}`,
      })
    if (fp) tabs.push({ key: 'source', label: t('Source') })
    return tabs
  }, [hasReport, dims.length, cache, compat, fp, t])

  // 当前 tab 不在可用列表时回退到第一个
  useEffect(() => {
    if (availableTabs.length && !availableTabs.some((x) => x.key === tab)) {
      setTab(availableTabs[0].key)
    }
  }, [availableTabs, tab])

  const verdict = fp ? verdictMeta(fp.verdict) : null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-5xl'>
        <DialogHeader className='px-5 pt-5'>
          <DialogTitle className='flex items-center gap-2'>
            <ShieldCheck className='size-5' />
            {t('Channel Verify')} — {channel?.name}
          </DialogTitle>
          <DialogDescription>
            {t('Trigger external health verification via the external verification gateway. Result will be saved to history.')}
          </DialogDescription>
        </DialogHeader>

        {/* 控制条 */}
        <div className='flex flex-wrap items-end gap-3 border-b px-5 pb-3'>
          <div className='min-w-[200px] flex-1'>
            <label className='mb-1 block text-xs text-muted-foreground'>{t('Test model')}</label>
            {verifiableModels.length === 0 ? (
              <div className='flex items-center gap-1 text-sm text-rose-600'>
                <AlertTriangle className='size-4' />
                {t('No verifiable model available on this channel.')}
              </div>
            ) : (
              <Select value={selectedModel} onValueChange={setSelectedModel} disabled={running}>
                <SelectTrigger>
                  <SelectValue placeholder={t('Select model')} />
                </SelectTrigger>
                <SelectContent>
                  {verifiableModels.map((m) => (
                    <SelectItem key={m} value={m}>
                      {m}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>
          <Button
            onClick={handleStart}
            disabled={running || backgroundRunning || !selectedModel || verifiableModels.length === 0}
          >
            {running ? (
              <>
                <Loader2 className='mr-1 size-4 animate-spin' />
                {queueInfo.waiting ? t('Queued') : t('Verifying...')}
              </>
            ) : backgroundRunning ? (
              <>
                <Loader2 className='mr-1 size-4 animate-spin' />
                {t('Background running')}
              </>
            ) : (
              <>
                <PlayCircle className='mr-1 size-4' />
                {t('Start verify')}
              </>
            )}
          </Button>
          <Button variant='outline' size='icon' onClick={handleExport} disabled={!hasReport} title={t('Export JSON')}>
            <Download className='size-4' />
          </Button>
        </div>

        {/* 状态横幅 */}
        {(backgroundRunning && !running) || queueInfo.waiting || error ? (
          <div className='space-y-1 px-5 pt-2'>
            {backgroundRunning && !running && (
              <div className='flex items-center gap-2 rounded bg-blue-50 p-2 text-sm text-blue-600 dark:bg-blue-950'>
                <Loader2 className='size-4 animate-spin' />
                {t('A verification is running in background. Close this dialog and check the list later.')}
              </div>
            )}
            {queueInfo.waiting && (
              <div className='flex items-center gap-2 text-sm text-amber-600'>
                <Clock className='size-4' />
                {t('In queue, position:')} {queueInfo.position}
              </div>
            )}
            {error && (
              <div className='flex items-center gap-2 rounded bg-rose-50 p-2 text-sm text-rose-600 dark:bg-rose-950'>
                <XCircle className='size-4' />
                {error}
              </div>
            )}
          </div>
        ) : null}

        {/* 主从双栏 */}
        <div className='flex min-h-0 flex-1'>
          {/* 历史栏（独立滚动） */}
          <aside className='flex w-52 min-w-52 flex-col border-r'>
            <div className='flex items-center justify-between px-3 py-2 text-xs text-muted-foreground'>
              <span>{t('History')}</span>
              <span>{history.length}</span>
            </div>
            <ScrollArea className='flex-1'>
              <div className='space-y-1 px-2 pb-2'>
                {displayHistory.length === 0 && (
                  <div className='px-2 text-xs text-muted-foreground'>{t('No history yet.')}</div>
                )}
                {displayHistory.map((h) => {
                  const isRun = h.status === 'running'
                  const isFail = h.status === 'failed'
                  return (
                    <div
                      key={`${h.id}-${h.tested_at}`}
                      className={cn(
                        'rounded-md border border-transparent p-2 transition',
                        currentReport?.id === h.id && currentReport?.status === h.status
                          ? 'border-border bg-muted'
                          : 'hover:bg-muted'
                      )}
                    >
                      <button
                        type='button'
                        onClick={() => !isRun && handleLoadHistory(h.id)}
                        disabled={running || isRun}
                        className='w-full text-left'
                      >
                        <div className='flex items-center gap-2'>
                          <div
                            className={cn(
                              'flex size-6 shrink-0 items-center justify-center rounded text-[11px] font-medium',
                              isFail ? 'bg-rose-600 text-white' : isRun ? 'bg-muted-foreground/20' : gradeBg(h.grade)
                            )}
                          >
                            {isRun ? '…' : isFail ? 'F' : h.grade || '?'}
                          </div>
                          <div className='min-w-0 flex-1'>
                            <div className='text-[13px] font-medium tabular-nums'>
                              {isRun ? t('Verifying...') : isFail ? t('Fail') : h.score}
                            </div>
                            <div className='truncate text-[10px] text-muted-foreground' title={h.model}>
                              {h.model}
                            </div>
                          </div>
                        </div>
                        <div className='mt-1 flex items-center justify-between'>
                          <span className='text-[10px] text-muted-foreground'>
                            {formatTimestampToDate(h.tested_at)}
                          </span>
                          <Badge variant={historyStatusVariant(h.status)} className='text-[9px]'>
                            {h.status}
                          </Badge>
                        </div>
                      </button>
                      {isRun && h.id > 0 && (
                        <Button
                          type='button'
                          size='sm'
                          variant='ghost'
                          className='mt-1 h-6 w-full px-2 text-rose-600 hover:bg-rose-50 hover:text-rose-700 dark:hover:bg-rose-950'
                          onClick={() => handleCancelReport(h.id)}
                        >
                          <StopCircle className='mr-1 size-3.5' />
                          {t('Stop')}
                        </Button>
                      )}
                    </div>
                  )
                })}
              </div>
            </ScrollArea>
          </aside>

          {/* 详情面板 */}
          <main className='flex min-w-0 flex-1 flex-col'>
            {!currentReport ? (
              <div className='flex flex-1 items-center justify-center px-6 text-center text-sm text-muted-foreground'>
                {verifiableModels.length === 0
                  ? t('No verifiable model available on this channel.')
                  : t('No verification report yet. Pick a model and start.')}
              </div>
            ) : currentReport.status === 'failed' ? (
              <div className='flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center'>
                <XCircle className='size-7 text-rose-600' />
                <div className='text-sm text-rose-600'>
                  {t('Verification failed:')} {currentReport.error_msg}
                </div>
                <div className='text-xs text-muted-foreground'>
                  {t('This run produced no report. Check the channel key or base_url and retry.')}
                </div>
              </div>
            ) : !hasReport ? (
              <div className='flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center'>
                <Loader2 className='size-6 animate-spin text-amber-500' />
                <div className='text-sm text-muted-foreground'>{t('Verification in progress…')}</div>
                <div className='text-xs text-muted-foreground'>
                  {t('Result will be saved when complete. You can close this dialog and check later.')}
                </div>
              </div>
            ) : (
              <>
                {/* 摘要条 */}
                <div className='flex items-center gap-3 border-b px-5 py-3'>
                  <div
                    className={cn(
                      'flex size-12 shrink-0 items-center justify-center rounded-full text-xl font-bold',
                      gradeBg(grade)
                    )}
                  >
                    {grade || '?'}
                  </div>
                  <div className='min-w-0 flex-1'>
                    <div className='flex items-center gap-2'>
                      <span className='text-2xl font-bold tabular-nums'>
                        {score ?? 0}
                        <span className='ml-0.5 text-xs text-muted-foreground'>/100</span>
                      </span>
                      {verdict && (
                        <span className={cn('flex items-center gap-1 text-xs', verdict.tone)}>
                          <Fingerprint className='size-3.5' />
                          {t(verdict.key)}
                          {fp?.confidence !== undefined && ` · ${pct(fp.confidence)}`}
                        </span>
                      )}
                      {currentReport.status === 'running' && (
                        <Loader2 className='size-4 animate-spin text-amber-500' />
                      )}
                      {currentReport.status === 'success' && (
                        <CheckCircle2 className='size-4 text-emerald-500' />
                      )}
                    </div>
                    <div className='mt-0.5 flex flex-wrap gap-x-3 text-[11px] text-muted-foreground'>
                      <span className='inline-flex items-center gap-1'>
                        <Gauge className='size-3' />
                        {currentReport.model}
                      </span>
                      <span>{formatTimestampToDate(currentReport.tested_at)}</span>
                      <span>{fmtDuration(currentReport.duration_ms)}</span>
                      {health?.stage_cap != null && (
                        <span>
                          {t('Stage cap:')} {health.stage_cap}
                        </span>
                      )}
                      {!!health?.token_penalty && (
                        <span>
                          {t('Token penalty:')} -{health.token_penalty}
                        </span>
                      )}
                    </div>
                  </div>
                </div>

                {/* 分页签（单一滚动区） */}
                <Tabs value={tab} onValueChange={setTab} className='flex min-h-0 flex-1 flex-col'>
                  <TabsList className='mx-3 mt-2 flex h-auto w-auto flex-wrap justify-start gap-1 bg-transparent p-0'>
                    {availableTabs.map((x) => (
                      <TabsTrigger key={x.key} value={x.key} className='gap-1 text-xs'>
                        {x.key === 'overview' && <Activity className='size-3.5' />}
                        {x.key === 'cache' && <Database className='size-3.5' />}
                        {x.key === 'compat' && <Plug className='size-3.5' />}
                        {x.key === 'source' && <Fingerprint className='size-3.5' />}
                        {x.label}
                        {x.badge && <span className='text-[10px] text-muted-foreground'>{x.badge}</span>}
                      </TabsTrigger>
                    ))}
                  </TabsList>
                  <ScrollArea className='min-h-0 flex-1'>
                    <div className='p-4'>
                      <TabsContent value='overview' className='mt-0'>
                        <OverviewPanel report={currentReport} health={health} cache={cache} fp={fp} />
                      </TabsContent>
                      <TabsContent value='dimensions' className='mt-0'>
                        <DimensionsPanel dims={dims} />
                      </TabsContent>
                      <TabsContent value='cache' className='mt-0'>
                        {cache && <CachePanel cache={cache} />}
                      </TabsContent>
                      <TabsContent value='compat' className='mt-0'>
                        {compat && <CompatPanel compat={compat} />}
                      </TabsContent>
                      <TabsContent value='source' className='mt-0'>
                        {fp && <SourcePanel fp={fp} />}
                      </TabsContent>
                    </div>
                  </ScrollArea>
                </Tabs>
              </>
            )}
          </main>
        </div>

        <DialogFooter className='border-t px-5 py-3'>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

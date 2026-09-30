import { useState } from 'react'
import { Check, Copy, Download, Eye, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  describeApiError,
  useContentBackupDownload,
  useContentBackupJob,
  useContentBackupPreview,
  useContentBackupRetry,
} from '../api'
import {
  cleanupLabel,
  cleanupTone,
  formatBytes,
  formatTimestamp,
  integrityLabel,
  isRetryableStatus,
  parseRetryReason,
  renderPreviewSide,
  retryBlockedLabel,
  retryOutcomeLabel,
  statusLabel,
  statusTone,
  terminalReasonLabel,
} from '../status'
import {
  CONTENT_BACKUP_PREVIEW_BYTES_PER_SIDE,
  type ContentBackupJob,
  type ContentBackupPreviewSide,
} from '../types'

/** 列表与抽屉共用的复制控件：图标带 tooltip，命中区 40px */
export function CopyValueButton(props: { value?: string; label: string }) {
  const { copiedText, copyToClipboard } = useCopyToClipboard()
  if (!props.value) return null
  const copied = copiedText === props.value
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant='ghost'
          size='icon-lg'
          className='shrink-0'
          aria-label={props.label}
          onClick={() => {
            void copyToClipboard(props.value ?? '')
          }}
        >
          {copied ? (
            <Check className='size-4' aria-hidden='true' />
          ) : (
            <Copy className='size-4' aria-hidden='true' />
          )}
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        <p>{props.label}</p>
      </TooltipContent>
    </Tooltip>
  )
}

function Field(props: {
  label: string
  children?: React.ReactNode
  copyValue?: string
  mono?: boolean
}) {
  const { t } = useTranslation()
  return (
    <div className='flex items-start justify-between gap-3 py-1'>
      <span className='text-muted-foreground shrink-0 text-xs'>
        {props.label}
      </span>
      <span className='flex min-w-0 items-start gap-1 text-right'>
        <span
          className={cn(
            'min-w-0 text-xs',
            props.mono ? 'font-mono break-all' : 'break-words'
          )}
        >
          {props.children ?? '-'}
        </span>
        {props.copyValue ? (
          <CopyValueButton value={props.copyValue} label={t('Copy value')} />
        ) : null}
      </span>
    </div>
  )
}

function Section(props: { title: string; children: React.ReactNode }) {
  return (
    <section className='border-b px-4 py-3 last:border-b-0'>
      <h3 className='text-muted-foreground mb-1.5 text-xs font-semibold tracking-wide'>
        {props.title}
      </h3>
      {props.children}
    </section>
  )
}

function PreviewSidePanel(props: {
  title: string
  side?: ContentBackupPreviewSide
}) {
  const { t } = useTranslation()
  const rendered = renderPreviewSide(props.side)
  return (
    <div className='space-y-1.5'>
      <div className='flex flex-wrap items-center gap-2'>
        <span className='text-xs font-medium'>{props.title}</span>
        <span className='text-muted-foreground font-mono text-xs'>
          {rendered.contentType || t('Unknown type')}
        </span>
        {/* 截断标记常驻：截断内容不承诺可解析或可播放 */}
        {'truncated' in rendered && rendered.truncated ? (
          <Badge variant='destructive'>{t('Truncated')}</Badge>
        ) : null}
        {'complete' in rendered && rendered.complete === false ? (
          <Badge variant='outline'>{t('Incomplete')}</Badge>
        ) : null}
      </div>

      {rendered.kind === 'empty' ? (
        <p className='text-muted-foreground text-xs'>{t('No content captured')}</p>
      ) : null}

      {rendered.kind === 'binary' ? (
        <div className='text-muted-foreground space-y-1 text-xs'>
          <p>{t('Binary content is not decoded in the browser.')}</p>
          <p className='tabular-nums'>
            {t('Size')}: {formatBytes(rendered.bytes)}
          </p>
        </div>
      ) : null}

      {rendered.kind === 'text' || rendered.kind === 'json' ? (
        // 客户正文只作为纯文本渲染，依赖 React 默认转义，绝不解析 HTML/Markdown
        <pre className='bg-muted/50 max-h-72 overflow-auto rounded-md p-2 font-mono text-xs whitespace-pre-wrap break-all'>
          {rendered.text}
        </pre>
      ) : null}

      {rendered.kind === 'sse' ? (
        <div className='bg-muted/50 max-h-72 space-y-1.5 overflow-auto rounded-md p-2'>
          {rendered.events.map((event, index) => (
            <div key={`${index}-${event.slice(0, 16)}`}>
              <div className='text-muted-foreground text-[10px] tabular-nums'>
                {t('Event {{index}}', { index: index + 1 })}
              </div>
              <pre className='font-mono text-xs whitespace-pre-wrap break-all'>
                {event}
              </pre>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  )
}

function DrawerBody(props: {
  jobId: string
  job?: ContentBackupJob
  canManage: boolean
  isRoot: boolean
}) {
  const { t } = useTranslation()
  const [previewRequested, setPreviewRequested] = useState(false)
  const detailQuery = useContentBackupJob(props.jobId, {
    initialJob: props.job,
  })
  const previewQuery = useContentBackupPreview(props.jobId, {
    enabled: previewRequested && props.isRoot,
  })
  const retryMutation = useContentBackupRetry()
  const downloadMutation = useContentBackupDownload()
  const job = detailQuery.data ?? props.job

  const handleRetry = () => {
    retryMutation.mutate([props.jobId], {
      onSuccess: (results) => {
        const outcome = results.find((item) => item.job_id === props.jobId)
        const reason = parseRetryReason(outcome?.reason)
        if (outcome?.result === 'queued') {
          // 入队不等于备份成功，上传完成才算
          toast.success(t('Queued for upload'))
          return
        }
        if (reason.blockedLabel) {
          toast.error(t(reason.blockedLabel))
          return
        }
        toast.warning(
          outcome?.reason
            ? `${t(retryOutcomeLabel(outcome?.result))}: ${outcome.reason}`
            : t(retryOutcomeLabel(outcome?.result))
        )
      },
      onError: () => toast.error(t('Retry failed')),
    })
  }

  const handleDownload = () => {
    downloadMutation.mutate(
      { jobId: props.jobId },
      {
        onError: (error) =>
          toast.error(describeApiError(error) ?? t('Download failed')),
      }
    )
  }

  const blockedLabel = retryBlockedLabel(job?.last_error_code)
  const canRetry = props.canManage && isRetryableStatus(job?.status)
  const preview = previewQuery.data
  const lastUpdatedText = detailQuery.dataUpdatedAt
    ? formatTimestamp(new Date(detailQuery.dataUpdatedAt).toISOString())
    : '-'

  return (
    <>
      <SheetHeader className='border-b'>
        <SheetTitle className='flex flex-wrap items-center gap-2'>
          <span className='font-mono'>{job?.job_id ?? props.jobId}</span>
          <Badge variant={statusTone(job?.status)}>
            {t(statusLabel(job?.status))}
          </Badge>
        </SheetTitle>
        <SheetDescription>
          {t('Archived request metadata. Local paths and raw bodies are never listed here.')}
        </SheetDescription>
        <div className='flex flex-wrap items-center gap-2 pt-1'>
          {/* 「已上传」与「本地空间已释放」是两个独立状态，必须同时展示 */}
          <Badge variant={cleanupTone(job?.status, job?.cleanup_state)}>
            {t(cleanupLabel(job?.status, job?.cleanup_state))}
          </Badge>
          <Badge variant='outline'>{t(integrityLabel(job ?? {}))}</Badge>
          <span className='text-muted-foreground text-xs tabular-nums'>
            {formatTimestamp(job?.created_at)}
          </span>
        </div>
      </SheetHeader>

      <div className='min-h-0 flex-1 overflow-y-auto'>
        <Section title={t('Request identity')}>
          <Field label={t('Request ID')} copyValue={job?.request_id} mono>
            {job?.request_id || '-'}
          </Field>
          <Field label={t('Upstream request ID')} copyValue={job?.upstream_request_id} mono>
            {job?.upstream_request_id || '-'}
          </Field>
          <Field label={t('User')}>{job?.user_id ?? '-'}</Field>
          <Field label={t('Token ID')}>{job?.token_id ?? '-'}</Field>
          <Field label={t('Channel')}>
            {job?.channel_name
              ? `${job.channel_name} (#${job.channel_id ?? '-'})`
              : (job?.channel_id ?? '-')}
          </Field>
          <Field label={t('Model')}>{job?.model || '-'}</Field>
          <Field label={t('Endpoint')} mono>
            {job?.endpoint || '-'}
          </Field>
          <Field label={t('Streaming')}>
            {job?.stream === undefined ? '-' : job.stream ? t('Yes') : t('No')}
          </Field>
          <Field label={t('HTTP status')}>{job?.http_status ?? '-'}</Field>
          <Field label={t('Terminal reason')}>
            {t(terminalReasonLabel(job?.terminal_reason))}
          </Field>
        </Section>

        <Section title={t('Session')}>
          <Field label={t('Source')}>{job?.session_source || t('No session')}</Field>
          {/* 只展示脱敏提示与哈希，原始会话值不在列表或详情里回显 */}
          <Field label={t('Session hint')} mono>
            {job?.session_hint || '-'}
          </Field>
          <Field label={t('Session hash')} copyValue={job?.session_hash} mono>
            {job?.session_hash || '-'}
          </Field>
        </Section>

        <Section title={t('Stored content')}>
          <Field label={t('Storage node')} copyValue={job?.storage_node_id} mono>
            {job?.storage_node_id || '-'}
          </Field>
          <Field label={t('Target')}>{job?.target_id || '-'}</Field>
          <Field label={t('Config version')}>{job?.config_version ?? '-'}</Field>
          <Field label={t('Remote path')} copyValue={job?.remote_path} mono>
            {job?.remote_path || '-'}
          </Field>
          <Field label={t('Compressed size')}>
            {formatBytes(job?.compressed_bytes)}
          </Field>
          <Field label={t('Compressed SHA256')} copyValue={job?.compressed_sha256} mono>
            {job?.compressed_sha256 || '-'}
          </Field>
          <Field label={t('Frame SHA256')} copyValue={job?.frame_sha256} mono>
            {job?.frame_sha256 || '-'}
          </Field>
          <Field label={t('Request bytes')}>
            {job?.request_captured_bytes === undefined
              ? '-'
              : `${formatBytes(job.request_captured_bytes)} / ${formatBytes(job.request_observed_bytes)}`}
          </Field>
          <Field label={t('Request content type')} mono>
            {job?.request_content_type || '-'}
          </Field>
          <Field label={t('Response bytes')}>
            {job?.response_captured_bytes === undefined
              ? '-'
              : `${formatBytes(job.response_captured_bytes)} / ${formatBytes(job.response_observed_bytes)}`}
          </Field>
          <Field label={t('Response content type')} mono>
            {job?.response_content_type || '-'}
          </Field>
        </Section>

        <Section title={t('Upload execution')}>
          <Field label={t('Attempts')}>
            {job?.attempts === undefined
              ? '-'
              : `${job.attempts} / ${job.total_attempts ?? '-'}`}
          </Field>
          <Field label={t('Retry rounds')}>{job?.retry_round ?? '-'}</Field>
          <Field label={t('Next attempt at')}>
            {formatTimestamp(job?.available_at)}
          </Field>
          <Field label={t('Updated at')}>{formatTimestamp(job?.updated_at)}</Field>
          <Field label={t('Last error code')} mono>
            {job?.last_error_code || '-'}
          </Field>
          <Field label={t('Last error message')} mono>
            {job?.last_error_message || '-'}
          </Field>
          {blockedLabel ? (
            <p className='text-destructive pt-1 text-xs'>{t(blockedLabel)}</p>
          ) : null}
        </Section>

        <Section title={t('Upload result and local cleanup')}>
          <Field label={t('Uploaded at')}>{formatTimestamp(job?.uploaded_at)}</Field>
          <Field label={t('Local cleanup')}>
            {t(cleanupLabel(job?.status, job?.cleanup_state))}
          </Field>
          <Field label={t('Cleanup attempts')}>{job?.cleanup_attempts ?? '-'}</Field>
          <Field label={t('Next cleanup at')}>
            {formatTimestamp(job?.cleanup_available_at)}
          </Field>
          <Field label={t('Cleanup error')} mono>
            {job?.cleanup_error || '-'}
          </Field>
          <Field label={t('Space reclaimed at')}>
            {formatTimestamp(job?.cleaned_at)}
          </Field>
          {job?.status === 'uploaded' && job?.cleanup_state === 'pending' ? (
            <p className='text-muted-foreground pt-1 text-xs'>
              {t('Uploaded, but the local copy still occupies disk space until cleanup finishes.')}
            </p>
          ) : null}
        </Section>

        <Section title={t('Content preview')}>
          {props.isRoot ? (
            <div className='space-y-3'>
              <div className='flex flex-wrap items-center gap-2'>
                <Button
                  variant='outline'
                  size='sm'
                  className='min-h-10'
                  disabled={previewQuery.isFetching}
                  onClick={() => {
                    // 失败后必须还能再点：按钮此前只看"点过没有"，拉取失败或已拉完
                    // 都还写着「加载中」且永久禁用，既是假状态，也让运营在修好凭据后
                    // 只能关掉抽屉重开。真实状态只有"此刻在不在拉"。
                    if (previewRequested) previewQuery.refetch()
                    else setPreviewRequested(true)
                  }}
                >
                  <Eye className='size-4' aria-hidden='true' />
                  {previewQuery.isFetching ? t('Loading...') : t('Load preview')}
                </Button>
                <Button
                  variant='outline'
                  size='sm'
                  className='min-h-10'
                  disabled={downloadMutation.isPending}
                  onClick={handleDownload}
                >
                  <Download className='size-4' aria-hidden='true' />
                  {downloadMutation.isPending ? t('Downloading...') : t('Download original package')}
                </Button>
              </div>
              <p className='text-muted-foreground text-xs'>
                {t('Preview shows at most {{size}} per side.', {
                  size: formatBytes(CONTENT_BACKUP_PREVIEW_BYTES_PER_SIDE),
                })}
              </p>
              {job?.status !== 'uploaded' ? (
                <p className='text-muted-foreground text-xs'>
                  {t('Not uploaded yet; the body may only exist on the storage node.')}
                </p>
              ) : null}
              {previewQuery.isError ? (
                <p className='text-destructive text-xs'>
                  {describeApiError(previewQuery.error) ?? t('Preview failed')}
                </p>
              ) : null}
              {preview ? (
                <div className='space-y-3'>
                  <PreviewSidePanel title={t('Request body')} side={preview.request} />
                  <PreviewSidePanel title={t('Response body')} side={preview.response} />
                </div>
              ) : null}
            </div>
          ) : (
            <p className='text-muted-foreground text-xs'>
              {t('Preview and download require root permission.')}
            </p>
          )}
        </Section>
      </div>

      <div className='flex flex-wrap items-center justify-between gap-2 border-t px-4 py-3'>
        <span className='text-muted-foreground text-xs'>
          {detailQuery.isError
            ? (describeApiError(detailQuery.error) ??
              t('Content backup request failed'))
            : t('Last updated {{time}}', { time: lastUpdatedText })}
        </span>
        {canRetry ? (
          <Button
            size='sm'
            className='min-h-10'
            disabled={retryMutation.isPending}
            onClick={handleRetry}
          >
            <RotateCcw className='size-4' aria-hidden='true' />
            {t('Retry upload')}
          </Button>
        ) : null}
      </div>
    </>
  )
}

export interface ContentBackupDetailDrawerProps {
  jobId: string | null
  /** 列表行数据，作为详情请求返回前的占位 */
  job?: ContentBackupJob
  open: boolean
  onOpenChange: (open: boolean) => void
  canManage: boolean
  isRoot: boolean
}

export function ContentBackupDetailDrawer(
  props: ContentBackupDetailDrawerProps
) {
  // 桌面定宽抽屉、手机全屏
  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className='w-full gap-0 p-0 sm:max-w-2xl'>
        {props.jobId ? (
          <DrawerBody
            key={props.jobId}
            jobId={props.jobId}
            job={props.job}
            canManage={props.canManage}
            isRoot={props.isRoot}
          />
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

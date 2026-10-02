// 大图预览：左边看图（多张可左右切换，支持方向键），右边是提示词、参数和下载 / 复制 / 复用。
import {
  ChevronLeft,
  ChevronRight,
  Copy,
  CornerDownLeft,
  Download,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { composerChip, composerPrimary } from '@/components/composer-styles'
import { QUALITY_LABELS, formatElapsed, formatSize } from '../lib/image-params'
import type { ImageJob } from '../types'

interface ImageLightboxProps {
  job: ImageJob | null
  index: number
  onIndexChange: (index: number) => void
  onClose: () => void
  onReuse: (job: ImageJob) => void
}

function downloadName(job: ImageJob, index: number, src: string): string {
  const ext = src.match(/^data:image\/(\w+)/)?.[1] ?? 'png'
  return `create-${job.input.model}-${job.startedAt}-${index + 1}.${ext}`
}

export function ImageLightbox({
  job,
  index,
  onIndexChange,
  onClose,
  onReuse,
}: ImageLightboxProps) {
  const { t } = useTranslation()
  const total = job?.images.length ?? 0
  const src = job?.images[index]

  const step = (delta: number) => {
    if (total > 1) onIndexChange((index + delta + total) % total)
  }

  const copyPrompt = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      toast.success(t('Prompt copied'))
    } catch {
      toast.error(t('Copy failed'))
    }
  }

  return (
    <Dialog open={!!job && !!src} onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className='grid max-h-[92vh] gap-0 overflow-hidden rounded-xl p-0 sm:max-w-5xl md:grid-cols-[minmax(0,1fr)_20rem]'
        onKeyDown={(e) => {
          if (e.key === 'ArrowLeft') step(-1)
          if (e.key === 'ArrowRight') step(1)
        }}
      >
        {job && src && (
          <>
            <div className='relative flex min-h-[40vh] items-center justify-center bg-[#0a2540] p-4 md:min-h-[70vh]'>
              <img
                src={src}
                alt={job.input.prompt}
                className='max-h-[55vh] max-w-full rounded-lg object-contain md:max-h-[84vh]'
              />
              {total > 1 && (
                <>
                  <button
                    type='button'
                    onClick={() => step(-1)}
                    aria-label={t('Previous image')}
                    className={`${composerChip} absolute top-[calc(50%-1rem)] left-3 px-2`}
                  >
                    <ChevronLeft className='size-4' />
                  </button>
                  <button
                    type='button'
                    onClick={() => step(1)}
                    aria-label={t('Next image')}
                    className={`${composerChip} absolute top-[calc(50%-1rem)] right-3 px-2`}
                  >
                    <ChevronRight className='size-4' />
                  </button>
                  <span className='absolute bottom-3 left-1/2 -translate-x-1/2 rounded-lg bg-[#0a2540]/80 px-2 py-0.5 text-[11px] text-white tabular-nums'>
                    {index + 1} / {total}
                  </span>
                </>
              )}
            </div>

            <div className='flex min-h-0 flex-col gap-4 overflow-y-auto p-5'>
              <DialogHeader className='space-y-2 pr-6 text-left'>
                <DialogTitle className='font-mono text-sm'>
                  {job.input.model}
                </DialogTitle>
                <DialogDescription className='text-foreground max-h-56 overflow-y-auto text-sm leading-6 whitespace-pre-wrap'>
                  {job.input.prompt}
                </DialogDescription>
              </DialogHeader>

              <dl className='grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs'>
                <dt className='text-muted-foreground'>{t('Size')}</dt>
                <dd className='tabular-nums'>
                  {job.input.params.size === 'auto'
                    ? t('Auto size')
                    : formatSize(job.input.params.size)}
                </dd>
                {job.input.params.quality &&
                  job.input.params.quality !== 'auto' && (
                    <>
                      <dt className='text-muted-foreground'>{t('Quality')}</dt>
                      <dd>
                        {t(
                          QUALITY_LABELS[job.input.params.quality] ??
                            job.input.params.quality
                        )}
                      </dd>
                    </>
                  )}
                <dt className='text-muted-foreground'>{t('API key')}</dt>
                <dd className='truncate'>{job.keyName}</dd>
                <dt className='text-muted-foreground'>{t('Time taken')}</dt>
                <dd className='tabular-nums'>
                  {formatElapsed(
                    (job.finishedAt ?? job.startedAt) - job.startedAt
                  )}
                </dd>
              </dl>

              <div className='mt-auto flex flex-wrap gap-2'>
                {/* 跨域图片地址上 download 不生效会变成同页跳转、丢掉本页记录，所以非 data: 一律新窗口打开 */}
                <a
                  href={src}
                  download={downloadName(job, index, src)}
                  {...(src.startsWith('data:')
                    ? {}
                    : { target: '_blank', rel: 'noreferrer' })}
                  className={composerPrimary}
                >
                  <Download className='size-3.5' />
                  {t('Download')}
                </a>
                <button
                  type='button'
                  onClick={() => void copyPrompt(job.input.prompt)}
                  className={composerChip}
                >
                  <Copy className='size-3.5' />
                  {t('Copy prompt')}
                </button>
                <button
                  type='button'
                  onClick={() => onReuse(job)}
                  className={composerChip}
                >
                  <CornerDownLeft className='size-3.5' />
                  {t('Reuse')}
                </button>
              </div>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

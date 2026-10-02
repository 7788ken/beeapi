// 一次生图 = 一张卡：上面是提示词与参数、复用/重新生成，下面是结果图（参考 Midjourney / Krea 的「一次生成占一行」）。
import { useState } from 'react'
import {
  AlertCircle,
  Clock,
  CornerDownLeft,
  ImageIcon,
  KeyRound,
  Loader2,
  RotateCw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { composerChip } from '@/components/composer-styles'
import {
  QUALITY_LABELS,
  STYLE_LABELS,
  formatElapsed,
  formatSize,
  sizeToAspect,
} from '../lib/image-params'
import type { ImageJob } from '../types'

interface ImageJobCardProps {
  job: ImageJob
  /** 页面时钟，用来刷新生成中的已等待时间 */
  now: number
  onOpen: (jobId: string, index: number) => void
  onReuse: (job: ImageJob) => void
  onRegenerate: (job: ImageJob) => void
}

export function ImageJobCard({
  job,
  now,
  onOpen,
  onReuse,
  onRegenerate,
}: ImageJobCardProps) {
  const { t } = useTranslation()
  const { prompt, model, params, reference, spec } = job.input
  const aspect = sizeToAspect(params.size)
  const elapsed = formatElapsed((job.finishedAt ?? now) - job.startedAt)
  const showQuality = !!params.quality && params.quality !== 'auto'

  return (
    <article className='bg-card text-card-foreground shadow-surface rounded-xl border p-4 sm:p-5'>
      <div className='flex flex-wrap items-start gap-x-4 gap-y-3'>
        <div className='min-w-0 flex-1 basis-64'>
          <p className='line-clamp-3 text-sm leading-6 break-words whitespace-pre-wrap'>
            {prompt}
          </p>
          <div className='text-muted-foreground mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] tabular-nums'>
            <span className='text-foreground font-mono'>{model}</span>
            <span>
              {params.size === 'auto'
                ? t('Auto size')
                : formatSize(params.size)}
            </span>
            {params.n > 1 && <span>×{params.n}</span>}
            {showQuality && (
              <span>
                {t('Quality')} ·{' '}
                {t(QUALITY_LABELS[params.quality] ?? params.quality)}
              </span>
            )}
            {params.style && (
              <span>{t(STYLE_LABELS[params.style] ?? params.style)}</span>
            )}
            {spec.resolutions && params.resolution && (
              <span>{params.resolution}</span>
            )}
            {reference && (
              <span className='inline-flex items-center gap-1'>
                <ImageIcon className='size-3' />
                {t('With reference image')}
              </span>
            )}
            <span className='inline-flex min-w-0 items-center gap-1'>
              <KeyRound className='size-3 shrink-0' />
              <span className='max-w-[10rem] truncate'>{job.keyName}</span>
            </span>
            <span className='inline-flex items-center gap-1'>
              <Clock className='size-3' />
              {elapsed}
            </span>
          </div>
        </div>
        <div className='flex shrink-0 gap-1.5'>
          <button
            type='button'
            onClick={() => onReuse(job)}
            className={composerChip}
          >
            <CornerDownLeft className='size-3.5' />
            {t('Reuse')}
          </button>
          <button
            type='button'
            onClick={() => onRegenerate(job)}
            disabled={job.status === 'running'}
            className={composerChip}
          >
            <RotateCw className='size-3.5' />
            {job.status === 'error' ? t('Retry') : t('Regenerate')}
          </button>
        </div>
      </div>

      <div className='mt-4'>
        {job.status === 'error' ? (
          <div className='flex items-start gap-3 rounded-lg border border-dashed px-4 py-3'>
            <AlertCircle className='mt-0.5 size-4 shrink-0' />
            <div className='min-w-0 flex-1'>
              <p className='text-sm font-semibold'>{t('Generation failed')}</p>
              <p className='text-muted-foreground mt-0.5 text-xs leading-5 break-words'>
                {job.error}
              </p>
              {job.modelText && (
                <p className='bg-muted mt-2 rounded-lg px-3 py-2 text-xs leading-5 break-words whitespace-pre-wrap'>
                  {job.modelText}
                </p>
              )}
            </div>
          </div>
        ) : (
          <div className='flex flex-wrap gap-2'>
            {job.status === 'running'
              ? Array.from({ length: params.n }, (_, i) => (
                  <div
                    key={i}
                    className='bg-muted/60 flex w-[calc(50%-0.25rem)] items-center justify-center rounded-lg border border-dashed sm:w-56'
                    style={{ aspectRatio: aspect ?? 1 }}
                  >
                    <span className='text-muted-foreground flex flex-col items-center gap-1.5 text-[11px] tabular-nums'>
                      <Loader2 className='text-primary size-5 animate-spin motion-reduce:animate-none' />
                      {t('Generating')} · {elapsed}
                    </span>
                  </div>
                ))
              : job.images.map((src, i) => (
                  <ResultTile
                    key={i}
                    src={src}
                    alt={prompt}
                    aspect={aspect}
                    label={t('Open image {{n}}', { n: i + 1 })}
                    onOpen={() => onOpen(job.id, i)}
                  />
                ))}
          </div>
        )}
      </div>
    </article>
  )
}

function ResultTile({
  src,
  alt,
  aspect,
  label,
  onOpen,
}: {
  src: string
  alt: string
  aspect: number | undefined
  label: string
  onOpen: () => void
}) {
  // 尺寸选 auto 时请求里没有比例，等图片加载后按真实比例摆放
  const [natural, setNatural] = useState<number>()
  return (
    <button
      type='button'
      onClick={onOpen}
      aria-label={label}
      className='bg-muted w-[calc(50%-0.25rem)] cursor-zoom-in overflow-hidden rounded-lg border transition-all duration-[400ms] ease-out hover:-translate-y-1 hover:shadow-[0_12px_30px_rgba(0,0,0,0.08)] focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none active:scale-[0.98] motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100 sm:w-56'
      style={{ aspectRatio: aspect ?? natural ?? 1 }}
    >
      <img
        src={src}
        alt={alt}
        loading='lazy'
        className='size-full object-cover'
        onLoad={(e) => {
          const img = e.currentTarget
          if (!aspect && img.naturalHeight) {
            setNatural(img.naturalWidth / img.naturalHeight)
          }
        }}
      />
    </button>
  )
}

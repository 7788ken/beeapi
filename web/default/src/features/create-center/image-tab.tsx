// Image 工作台：上方 form 发起任务，下方 grid 画廊展示已生成结果（本地 session 内）。

import { useState, useMemo, useCallback, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  ImagePlus,
  Loader2,
  Sparkles,
  Upload,
  X,
  Download,
  RotateCw,
  Copy,
  AlertCircle,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { getUserModels, generateImage } from './api'
import { getImageModelCapabilities } from './model-presets'
import type { ImageJob } from './types'

function newJobId() {
  return `job_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
}

export function ImageTab() {
  const { t } = useTranslation()

  // 拉取用户可用模型并过滤出图像模型
  const { data: allModels = [], isLoading: modelsLoading } = useQuery({
    queryKey: ['create-center', 'user-models'],
    queryFn: getUserModels,
    staleTime: 5 * 60_000,
  })
  const imageModels = useMemo(
    () =>
      allModels
        .map((m) => ({ name: m, cap: getImageModelCapabilities(m) }))
        .filter((x): x is { name: string; cap: NonNullable<ReturnType<typeof getImageModelCapabilities>> } => x.cap !== null),
    [allModels]
  )

  // 表单状态
  const [model, setModel] = useState<string>('')
  const [prompt, setPrompt] = useState('')
  const [size, setSize] = useState<string>('')
  const [n, setN] = useState(1)
  const [quality, setQuality] = useState<string>('standard')
  const [style, setStyle] = useState<string>('vivid')
  const [refImageDataUrl, setRefImageDataUrl] = useState<string>('')

  // 选模型后初始化参数默认值
  const selectedCap = useMemo(() => {
    if (!model) return null
    return getImageModelCapabilities(model)
  }, [model])

  // 模型变化时同步默认值
  const handleModelChange = (next: string) => {
    setModel(next)
    const cap = getImageModelCapabilities(next)
    if (cap) {
      setSize(cap.defaultSize)
      setN(cap.defaultN)
      if (!cap.supportsReferenceImage) setRefImageDataUrl('')
    }
  }

  // 默认选第一个可用图像模型
  if (!model && imageModels.length > 0) {
    queueMicrotask(() => handleModelChange(imageModels[0].name))
  }

  // 任务列表（本地状态，刷新页面会清空）
  const [jobs, setJobs] = useState<ImageJob[]>([])
  const [previewJob, setPreviewJob] = useState<ImageJob | null>(null)
  const [previewImageIdx, setPreviewImageIdx] = useState(0)

  const fileInputRef = useRef<HTMLInputElement>(null)

  const handleRefImageChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    if (file.size > 5 * 1024 * 1024) {
      toast.error(t('Reference image must be ≤ 5MB'))
      return
    }
    const reader = new FileReader()
    reader.onload = () => setRefImageDataUrl(reader.result as string)
    reader.readAsDataURL(file)
  }

  const handleGenerate = useCallback(async () => {
    if (!model) {
      toast.error(t('Select a model first'))
      return
    }
    if (!prompt.trim()) {
      toast.error(t('Prompt cannot be empty'))
      return
    }
    const cap = getImageModelCapabilities(model)
    if (!cap) return

    const job: ImageJob = {
      id: newJobId(),
      prompt: prompt.trim(),
      model,
      size: size || cap.defaultSize,
      n,
      status: 'running',
      createdAt: Date.now(),
    }
    setJobs((prev) => [job, ...prev])

    try {
      const images = await generateImage({
        model: job.model,
        prompt: job.prompt,
        n: job.n,
        size: job.size,
        quality: cap.supportsQuality ? quality : undefined,
        style: cap.supportsStyle ? style : undefined,
        reference_image_b64:
          cap.supportsReferenceImage && refImageDataUrl ? refImageDataUrl : undefined,
      })
      setJobs((prev) =>
        prev.map((j) =>
          j.id === job.id ? { ...j, status: 'done', images } : j
        )
      )
      toast.success(t('Image generated'))
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setJobs((prev) =>
        prev.map((j) =>
          j.id === job.id
            ? { ...j, status: 'error', errorMessage: msg }
            : j
        )
      )
      toast.error(msg || t('Generation failed'))
    }
  }, [model, prompt, size, n, quality, style, refImageDataUrl, t])

  const handleRegenerate = (job: ImageJob) => {
    setPrompt(job.prompt)
    setModel(job.model)
    setSize(job.size)
    setN(job.n)
    setPreviewJob(null)
  }

  const handleCopyPrompt = (text: string) => {
    void navigator.clipboard.writeText(text)
    toast.success(t('Prompt copied'))
  }

  return (
    <div className='space-y-4'>
      {/* ──────────────── 上：表单 ──────────────── */}
      <Card className='border-violet-200/50 bg-gradient-to-br from-violet-50/30 to-transparent p-4 dark:border-violet-500/20 dark:from-violet-950/20'>
        {modelsLoading ? (
          <div className='space-y-3'>
            <Skeleton className='h-10 w-full' />
            <Skeleton className='h-24 w-full' />
          </div>
        ) : imageModels.length === 0 ? (
          <NoModelHint />
        ) : (
          <div className='space-y-3'>
            {/* 顶部一行：模型 + 尺寸 + 数量 + 高级（quality/style） */}
            <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
              <div className='col-span-2 space-y-1.5 sm:col-span-1'>
                <Label className='text-xs'>{t('Model')}</Label>
                <Select value={model} onValueChange={handleModelChange}>
                  <SelectTrigger className='h-9'>
                    <SelectValue placeholder={t('Pick a model')} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectLabel className='text-[11px]'>
                        {t('Image models')}
                      </SelectLabel>
                      {imageModels.map(({ name, cap }) => (
                        <SelectItem key={name} value={name}>
                          <div className='flex items-center gap-2'>
                            <span className='font-medium'>{cap.label}</span>
                            <span className='text-muted-foreground text-[11px]'>
                              {name}
                            </span>
                          </div>
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>

              {selectedCap && (
                <div className='space-y-1.5'>
                  <Label className='text-xs'>{t('Size')}</Label>
                  <Select value={size} onValueChange={setSize}>
                    <SelectTrigger className='h-9'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {selectedCap.sizes.map((s) => (
                        <SelectItem key={s} value={s}>
                          {s}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              )}

              {selectedCap && (
                <div className='space-y-1.5'>
                  <Label className='text-xs'>{t('Quantity')}</Label>
                  <Input
                    type='number'
                    min={1}
                    max={selectedCap.maxN}
                    value={n}
                    onChange={(e) =>
                      setN(Math.max(1, Math.min(selectedCap.maxN, Number(e.target.value) || 1)))
                    }
                    className='h-9'
                  />
                </div>
              )}

              {selectedCap?.supportsQuality && (
                <div className='space-y-1.5'>
                  <Label className='text-xs'>{t('Quality')}</Label>
                  <Select value={quality} onValueChange={setQuality}>
                    <SelectTrigger className='h-9'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value='standard'>{t('Standard')}</SelectItem>
                      <SelectItem value='hd'>HD</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              )}

              {selectedCap?.supportsStyle && (
                <div className='space-y-1.5'>
                  <Label className='text-xs'>{t('Style')}</Label>
                  <Select value={style} onValueChange={setStyle}>
                    <SelectTrigger className='h-9'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value='vivid'>{t('Vivid')}</SelectItem>
                      <SelectItem value='natural'>{t('Natural')}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              )}
            </div>

            {/* Prompt 多行 */}
            <div className='space-y-1.5'>
              <Label className='text-xs'>{t('Prompt')}</Label>
              <Textarea
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                placeholder={t(
                  'Describe what you want to generate, e.g. "A futuristic city at sunset, cyberpunk style, neon lights"'
                )}
                rows={3}
                className='resize-none text-sm'
              />
              <div className='text-muted-foreground flex justify-between text-[11px]'>
                <span>
                  {t('Tip: be specific about subject, style, lighting, mood')}
                </span>
                <span className='tabular-nums'>{prompt.length} / 4000</span>
              </div>
            </div>

            {/* 参考图（仅支持模型显示） + 生成按钮 */}
            <div className='flex flex-wrap items-end gap-2'>
              {selectedCap?.supportsReferenceImage && (
                <div className='flex-1 space-y-1.5'>
                  <Label className='text-xs'>
                    {t('Reference image (optional)')}
                  </Label>
                  {refImageDataUrl ? (
                    <div className='relative inline-block'>
                      <img
                        src={refImageDataUrl}
                        alt='ref'
                        className='h-16 w-16 rounded border object-cover'
                      />
                      <button
                        type='button'
                        onClick={() => setRefImageDataUrl('')}
                        className='bg-background absolute -top-1.5 -right-1.5 rounded-full border p-0.5 shadow-sm hover:bg-rose-50'
                        aria-label={t('Remove')}
                      >
                        <X className='h-3 w-3' />
                      </button>
                    </div>
                  ) : (
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      className='h-9'
                      onClick={() => fileInputRef.current?.click()}
                    >
                      <Upload className='mr-1.5 h-4 w-4' />
                      {t('Upload')}
                    </Button>
                  )}
                  <input
                    ref={fileInputRef}
                    type='file'
                    accept='image/*'
                    className='hidden'
                    onChange={handleRefImageChange}
                  />
                </div>
              )}

              <Button
                onClick={handleGenerate}
                disabled={!model || !prompt.trim() || jobs.some((j) => j.status === 'running')}
                className='ml-auto bg-gradient-to-r from-violet-500 to-pink-500 text-white hover:from-violet-600 hover:to-pink-600'
              >
                {jobs.some((j) => j.status === 'running') ? (
                  <Loader2 className='mr-1.5 h-4 w-4 animate-spin' />
                ) : (
                  <Sparkles className='mr-1.5 h-4 w-4' />
                )}
                {t('Generate')}
              </Button>
            </div>
          </div>
        )}
      </Card>

      {/* ──────────────── 下：画廊 ──────────────── */}
      <div>
        <div className='mb-2 flex items-center justify-between'>
          <h3 className='text-sm font-semibold'>
            {t('Recent generations')}{' '}
            <span className='text-muted-foreground font-normal'>
              ({jobs.length})
            </span>
          </h3>
        </div>

        {jobs.length === 0 ? (
          <EmptyGallery t={t} />
        ) : (
          <div className='grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6'>
            {jobs.map((job) => (
              <GalleryTile
                key={job.id}
                job={job}
                onClick={() => {
                  if (job.status === 'done' && job.images?.length) {
                    setPreviewJob(job)
                    setPreviewImageIdx(0)
                  }
                }}
              />
            ))}
          </div>
        )}
      </div>

      {/* ──────────────── 大图预览 Dialog ──────────────── */}
      <Dialog
        open={!!previewJob}
        onOpenChange={(open) => !open && setPreviewJob(null)}
      >
        <DialogContent className='max-w-3xl p-0 overflow-hidden'>
          {previewJob && (
            <>
              <div className='bg-black/95 flex items-center justify-center p-4'>
                <PreviewImage
                  job={previewJob}
                  idx={previewImageIdx}
                  onChangeIdx={setPreviewImageIdx}
                />
              </div>
              <div className='space-y-2 p-4'>
                <DialogHeader>
                  <DialogTitle className='text-base'>
                    {previewJob.model}
                  </DialogTitle>
                  <DialogDescription className='whitespace-pre-wrap text-xs'>
                    {previewJob.prompt}
                  </DialogDescription>
                </DialogHeader>
                <div className='flex flex-wrap gap-2 pt-2'>
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => handleCopyPrompt(previewJob.prompt)}
                  >
                    <Copy className='mr-1.5 h-3.5 w-3.5' />
                    {t('Copy prompt')}
                  </Button>
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => handleRegenerate(previewJob)}
                  >
                    <RotateCw className='mr-1.5 h-3.5 w-3.5' />
                    {t('Regenerate')}
                  </Button>
                  <DownloadButton
                    job={previewJob}
                    idx={previewImageIdx}
                    t={t}
                  />
                </div>
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ──────────────────────────────────────────────────────────────────────────
// 子组件
// ──────────────────────────────────────────────────────────────────────────

function NoModelHint() {
  const { t } = useTranslation()
  return (
    <div className='flex flex-col items-center gap-2 py-6 text-center'>
      <AlertCircle className='text-muted-foreground h-8 w-8' />
      <p className='text-sm font-medium'>{t('No image model available')}</p>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Ask admin to enable DALL·E / GPT-Image / Gemini Image in your token group.'
        )}
      </p>
    </div>
  )
}

function EmptyGallery({ t }: { t: (k: string) => string }) {
  return (
    <Card className='flex flex-col items-center gap-3 border-dashed py-12 text-center'>
      <div className='bg-gradient-to-br from-violet-500/15 to-pink-500/15 rounded-full p-4'>
        <ImagePlus className='text-violet-500 h-8 w-8' />
      </div>
      <p className='text-sm font-medium'>{t('No generations yet')}</p>
      <p className='text-muted-foreground max-w-sm text-xs'>
        {t(
          'Enter a prompt above and pick a model to create your first image.'
        )}
      </p>
    </Card>
  )
}

interface GalleryTileProps {
  job: ImageJob
  onClick: () => void
}
function GalleryTile({ job, onClick }: GalleryTileProps) {
  const { t } = useTranslation()
  const firstImage = job.images?.[0]
  const src = firstImage?.b64_json
    ? `data:image/png;base64,${firstImage.b64_json}`
    : firstImage?.url

  return (
    <button
      type='button'
      onClick={onClick}
      className={cn(
        'group bg-muted/40 relative aspect-square overflow-hidden rounded-lg border transition-all',
        job.status === 'done' && 'cursor-zoom-in hover:ring-2 hover:ring-violet-400 hover:ring-offset-2',
        job.status === 'error' && 'border-rose-300/50 bg-rose-50/30 dark:bg-rose-950/20',
        job.status !== 'done' && 'cursor-default'
      )}
    >
      {job.status === 'running' || job.status === 'pending' ? (
        <div className='flex h-full w-full flex-col items-center justify-center gap-2'>
          <Loader2 className='text-violet-500 h-6 w-6 animate-spin' />
          <span className='text-muted-foreground text-[10px]'>
            {t('Generating...')}
          </span>
        </div>
      ) : job.status === 'error' ? (
        <Tooltip>
          <TooltipTrigger asChild>
            <div className='flex h-full w-full flex-col items-center justify-center gap-1.5'>
              <AlertCircle className='h-6 w-6 text-rose-500' />
              <span className='text-rose-600 px-2 text-[10px] line-clamp-2'>
                {t('Failed')}
              </span>
            </div>
          </TooltipTrigger>
          <TooltipContent>
            <p className='max-w-xs text-xs'>{job.errorMessage}</p>
          </TooltipContent>
        </Tooltip>
      ) : src ? (
        <>
          <img
            src={src}
            alt={job.prompt}
            className='h-full w-full object-cover transition-transform duration-300 group-hover:scale-105'
            loading='lazy'
          />
          <div className='absolute inset-0 bg-gradient-to-t from-black/70 via-transparent to-transparent opacity-0 transition-opacity group-hover:opacity-100' />
          <div className='absolute right-0 bottom-0 left-0 p-2 text-left text-[10px] text-white opacity-0 transition-opacity group-hover:opacity-100'>
            <p className='line-clamp-2'>{job.prompt}</p>
          </div>
        </>
      ) : (
        <div className='flex h-full w-full items-center justify-center'>
          <ImagePlus className='text-muted-foreground h-6 w-6' />
        </div>
      )}
    </button>
  )
}

function PreviewImage({
  job,
  idx,
  onChangeIdx,
}: {
  job: ImageJob
  idx: number
  onChangeIdx: (i: number) => void
}) {
  const img = job.images?.[idx]
  const src = img?.b64_json
    ? `data:image/png;base64,${img.b64_json}`
    : img?.url
  if (!src) return null
  return (
    <div className='relative flex items-center justify-center'>
      <img src={src} alt={job.prompt} className='max-h-[65vh] max-w-full rounded' />
      {(job.images?.length ?? 0) > 1 && (
        <div className='absolute bottom-2 left-1/2 flex -translate-x-1/2 gap-1.5 rounded-full bg-black/60 px-2 py-1'>
          {job.images!.map((_, i) => (
            <button
              key={i}
              onClick={() => onChangeIdx(i)}
              className={cn(
                'h-1.5 w-1.5 rounded-full transition-all',
                i === idx ? 'bg-white w-4' : 'bg-white/40'
              )}
              aria-label={`Image ${i + 1}`}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function DownloadButton({
  job,
  idx,
  t,
}: {
  job: ImageJob
  idx: number
  t: (k: string) => string
}) {
  const img = job.images?.[idx]
  if (!img) return null
  const src = img.b64_json
    ? `data:image/png;base64,${img.b64_json}`
    : img.url
  if (!src) return null
  return (
    <a
      href={src}
      download={`create-${job.id}-${idx}.png`}
      className='inline-flex items-center'
    >
      <Button size='sm' variant='outline'>
        <Download className='mr-1.5 h-3.5 w-3.5' />
        {t('Download')}
      </Button>
    </a>
  )
}

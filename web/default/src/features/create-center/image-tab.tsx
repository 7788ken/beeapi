// 生图工作区：上方是本页的生成记录（一次生成一张卡，最新的在最下面、紧挨输入卡），下方是吸底输入卡。
// 先选自己的 API Key，模型下拉只列这把 key 能用的生图模型，请求也用这把 key 发出。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getImageModelSpec } from '@/lib/model-capabilities'
import { useRelayKey } from '@/hooks/use-relay-key'
import {
  KeyHasNoModelsNotice,
  KeyUnusableNotice,
  NoUsableKeyNotice,
} from '@/components/key-model-selector'
import {
  NoImageReturnedError,
  generateImages,
  type GenerateImagesInput,
  type ReferenceImage,
} from './api'
import {
  ImageComposer,
  type ImageModelOption,
} from './components/image-composer'
import { ImageEmptyState } from './components/image-empty-state'
import { ImageJobCard } from './components/image-job-card'
import { ImageLightbox } from './components/image-lightbox'
import { useImageJobStore } from './lib/image-job-store'
import { defaultImageParams, fitImageParams } from './lib/image-params'
import type { ImageJob, ImageParams } from './types'

const KEY_STORAGE = 'create_center_image_key'
const MODEL_STORAGE = 'create_center_image_model'
/** 同时在跑的任务上限：gpt-image-2 一次要几分钟，不能让用户干等，但也防止连点多扣费 */
const MAX_RUNNING = 4
/** 这段时间内的再次提交（双击、按住回车）只算一次 */
const RESUBMIT_GUARD_MS = 1000

function loadStored(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function store(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // 写不进去（隐私模式等）只影响下次打开时的默认选择
  }
}

function newJobId() {
  return `job_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
}

/** 有任务在跑时每秒走一次，用来显示已等待时间 */
function useNow(active: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [active])
  return now
}

export function ImageTab() {
  const { t } = useTranslation()

  const [tokenId, setTokenId] = useState<number | null>(() => {
    const saved = Number(loadStored(KEY_STORAGE))
    return Number.isInteger(saved) && saved > 0 ? saved : null
  })
  const relay = useRelayKey(tokenId)

  const imageModels = useMemo<ImageModelOption[]>(
    () =>
      relay.models.flatMap((m) => {
        const spec = getImageModelSpec(m)
        return spec ? [{ id: m.id, spec }] : []
      }),
    [relay.models]
  )
  const [preferredModel, setPreferredModel] = useState(
    () => loadStored(MODEL_STORAGE) ?? ''
  )
  const active =
    imageModels.find((m) => m.id === preferredModel) ?? imageModels[0] ?? null
  const spec = active?.spec ?? null

  // 参数跟着模型走：换模型后，新模型支持的参数原样保留，不支持的退回默认值
  const [params, setParams] = useState<ImageParams | null>(null)
  const effectiveParams = useMemo(
    () =>
      spec ? fitImageParams(spec, params ?? defaultImageParams(spec)) : null,
    [spec, params]
  )

  const [prompt, setPrompt] = useState('')
  // 换到不收参考图的模型时只是不显示、不发送，换回来还在
  const [reference, setReference] = useState<ReferenceImage | null>(null)
  const effectiveReference = spec?.supportsReference ? reference : null

  const jobs = useImageJobStore((s) => s.jobs)
  const [preview, setPreview] = useState<{
    jobId: string
    index: number
  } | null>(null)
  const runningCount = jobs.filter((j) => j.status === 'running').length
  const now = useNow(runningCount > 0)

  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const feedEndRef = useRef<HTMLDivElement>(null)

  // 任务写进页面级 store：组件卸载（切到对话页、去管理 key）后请求照常落结果
  const run = useCallback(
    async (input: GenerateImagesInput, keyId: number, keyName: string) => {
      const id = newJobId()
      const { add, patch: patchJob } = useImageJobStore.getState()
      const patch = (next: Partial<ImageJob>) => patchJob(id, next)
      add({
        id,
        input,
        keyId,
        keyName,
        status: 'running',
        startedAt: Date.now(),
        images: [],
      })
      try {
        const images = await generateImages(input)
        patch({ status: 'done', images, finishedAt: Date.now() })
      } catch (err) {
        const noImage = err instanceof NoImageReturnedError
        patch({
          status: 'error',
          finishedAt: Date.now(),
          error: noImage
            ? t('The model returned no image')
            : err instanceof Error
              ? err.message
              : String(err),
          modelText: noImage ? err.text || undefined : undefined,
        })
      }
    },
    [t]
  )

  const canGenerate =
    !!relay.secret &&
    !!relay.activeKey &&
    !!active &&
    prompt.trim().length > 0 &&
    runningCount < MAX_RUNNING

  // 每次提交都会扣费：生成和重新生成都只从这里发
  const lastSubmitAt = useRef(0)
  /** 返回是否真的发出了任务 */
  const submit = (
    input: GenerateImagesInput,
    keyId: number,
    keyName: string
  ): boolean => {
    const now = Date.now()
    if (now - lastSubmitAt.current < RESUBMIT_GUARD_MS) return false
    if (runningCount >= MAX_RUNNING) {
      toast.info(
        t('Up to {{n}} generations can run at once', { n: MAX_RUNNING })
      )
      return false
    }
    lastSubmitAt.current = now
    void run(input, keyId, keyName)
    return true
  }

  const handleGenerate = () => {
    if (!canGenerate || !relay.secret || !relay.activeKey || !active) return
    if (!effectiveParams) return
    const started = submit(
      {
        secret: relay.secret,
        spec: active.spec,
        model: active.id,
        prompt: prompt.trim(),
        params: effectiveParams,
        reference: effectiveReference,
      },
      relay.activeKey.id,
      relay.activeKey.name
    )
    // 发出后像发消息一样清空输入卡（提示词和参考图）；要再来一张可点记录卡上的「复用」
    if (started) {
      setPrompt('')
      setReference(null)
    }
  }

  const handleRegenerate = (job: ImageJob) => {
    submit(job.input, job.keyId, job.keyName)
  }

  const handleReuse = (job: ImageJob) => {
    setTokenId(job.keyId)
    store(KEY_STORAGE, String(job.keyId))
    setPreferredModel(job.input.model)
    store(MODEL_STORAGE, job.input.model)
    setParams(job.input.params)
    setReference(job.input.reference)
    setPrompt(job.input.prompt)
    setPreview(null)
    requestAnimationFrame(() => textareaRef.current?.focus())
  }

  const jobCount = jobs.length
  useEffect(() => {
    if (jobCount === 0) return
    const reduceMotion = window.matchMedia(
      '(prefers-reduced-motion: reduce)'
    ).matches
    feedEndRef.current?.scrollIntoView({
      block: 'end',
      behavior: reduceMotion ? 'auto' : 'smooth',
    })
  }, [jobCount])

  const previewJob = preview
    ? (jobs.find((j) => j.id === preview.jobId) ?? null)
    : null

  return (
    <div className='flex h-full min-h-0 flex-col'>
      <div className='min-h-0 flex-1 overflow-y-auto [scrollbar-gutter:stable]'>
        <div className='mx-auto flex min-h-full w-full max-w-5xl flex-col gap-4 px-4 py-6 sm:px-6'>
          {jobs.length === 0 ? (
            <ImageEmptyState
              onPick={(text) => {
                setPrompt(text)
                textareaRef.current?.focus()
              }}
            />
          ) : (
            <>
              <p className='text-muted-foreground text-center text-[11px]'>
                {t(
                  'Generations stay on this page only. Download what you want to keep before refreshing.'
                )}
              </p>
              {jobs.map((job) => (
                <ImageJobCard
                  key={job.id}
                  job={job}
                  now={now}
                  onOpen={(jobId, index) => setPreview({ jobId, index })}
                  onReuse={handleReuse}
                  onRegenerate={handleRegenerate}
                />
              ))}
            </>
          )}
          <div ref={feedEndRef} />
        </div>
      </div>

      <div className='shrink-0 px-4 pb-4 sm:px-6'>
        <div className='mx-auto w-full max-w-5xl space-y-2'>
          {!relay.keysLoading && !relay.keysError && !relay.activeKey && (
            <NoUsableKeyNotice hasKeys={relay.keys.length > 0} />
          )}
          {relay.activeKey && relay.activeKeyUnusable && (
            <KeyUnusableNotice
              keyName={relay.activeKey.name}
              reason={relay.activeKeyUnusable}
            />
          )}
          {relay.activeKey &&
            !relay.activeKeyUnusable &&
            !relay.modelsLoading &&
            !relay.modelsError &&
            !relay.secretError &&
            imageModels.length === 0 && (
              <KeyHasNoModelsNotice
                keyName={relay.activeKey.name}
                kind='image'
              />
            )}
          <ImageComposer
            relay={relay}
            onSelectKey={(id) => {
              setTokenId(id)
              store(KEY_STORAGE, String(id))
            }}
            models={imageModels}
            model={active?.id ?? ''}
            onModelChange={(id) => {
              setPreferredModel(id)
              store(MODEL_STORAGE, id)
            }}
            spec={spec}
            params={effectiveParams}
            onParamsChange={setParams}
            prompt={prompt}
            onPromptChange={setPrompt}
            reference={effectiveReference}
            onReferenceChange={setReference}
            runningCount={runningCount}
            canGenerate={canGenerate}
            onGenerate={handleGenerate}
            textareaRef={textareaRef}
          />
          <p className='text-muted-foreground hidden px-1 text-[11px] sm:block'>
            {t('Enter to generate, Shift+Enter for a new line')}
            {spec?.supportsReference &&
              ` · ${t('Paste or drop an image to use it as a reference')}`}
          </p>
        </div>
      </div>

      <ImageLightbox
        job={previewJob}
        index={preview?.index ?? 0}
        onIndexChange={(index) =>
          setPreview((prev) => (prev ? { ...prev, index } : prev))
        }
        onClose={() => setPreview(null)}
        onReuse={handleReuse}
      />
    </div>
  )
}

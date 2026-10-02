// 生图输入卡（吸底）：提示词 + 工具栏 [Key][模型][尺寸][张数][质量…][参考图] … [生成]。
// 版式参考 Google Flow（Webby 2026 AI 创作工具奖）：参数收进输入框内的胶囊，参考图可直接粘贴或拖进来。
import { useRef, type ReactNode, type RefObject } from 'react'
import {
  ChevronDown,
  ImageIcon,
  ImagePlus,
  Images,
  Loader2,
  Sparkles,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { ImageModelSpec } from '@/lib/model-capabilities'
import { cn } from '@/lib/utils'
import { getRelayFailure, type RelayKeyState } from '@/hooks/use-relay-key'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupTextarea,
} from '@/components/ui/input-group'
import {
  composerChip,
  composerFrame,
  composerPrimary,
} from '@/components/composer-styles'
import { KeySelector, ModelSelector } from '@/components/key-model-selector'
import type { ReferenceImage } from '../api'
import {
  QUALITY_LABELS,
  STYLE_LABELS,
  formatSize,
  sizeToAspect,
} from '../lib/image-params'
import type { ImageParams } from '../types'

const MAX_REFERENCE_BYTES = 5 * 1024 * 1024

export interface ImageModelOption {
  id: string
  spec: ImageModelSpec
}

interface ImageComposerProps {
  relay: RelayKeyState
  onSelectKey: (id: number) => void
  models: ImageModelOption[]
  model: string
  onModelChange: (id: string) => void
  spec: ImageModelSpec | null
  /** 还没有可用模型时为 null，工具栏不渲染参数 */
  params: ImageParams | null
  onParamsChange: (params: ImageParams) => void
  prompt: string
  onPromptChange: (prompt: string) => void
  reference: ReferenceImage | null
  onReferenceChange: (reference: ReferenceImage | null) => void
  runningCount: number
  canGenerate: boolean
  onGenerate: () => void
  textareaRef: RefObject<HTMLTextAreaElement | null>
}

function readAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result as string)
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

export function ImageComposer({
  relay,
  onSelectKey,
  models,
  model,
  onModelChange,
  spec,
  params,
  onParamsChange,
  prompt,
  onPromptChange,
  reference,
  onReferenceChange,
  runningCount,
  canGenerate,
  onGenerate,
  textareaRef,
}: ImageComposerProps) {
  const { t } = useTranslation()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const acceptsReference = !!spec?.supportsReference

  const attach = async (file: File | undefined) => {
    if (!file || !file.type.startsWith('image/')) return
    if (!acceptsReference) {
      toast.info(t('This model does not take a reference image'))
      return
    }
    if (file.size > MAX_REFERENCE_BYTES) {
      toast.error(t('Reference image must be ≤ 5MB'))
      return
    }
    onReferenceChange({ file, dataUrl: await readAsDataUrl(file) })
  }

  const set = <K extends keyof ImageParams>(key: K, value: ImageParams[K]) => {
    if (params) onParamsChange({ ...params, [key]: value })
  }

  const failure = getRelayFailure(relay)

  return (
    <div
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes('Files')) e.preventDefault()
      }}
      onDrop={(e) => {
        const file = e.dataTransfer.files?.[0]
        if (!file) return
        e.preventDefault()
        void attach(file)
      }}
    >
      <InputGroup className={composerFrame}>
        {reference && (
          <InputGroupAddon align='block-start' className='cursor-default pb-0'>
            <div className='relative'>
              <img
                src={reference.dataUrl}
                alt={t('Reference image')}
                className='size-14 rounded-lg border object-cover'
              />
              <button
                type='button'
                onClick={() => onReferenceChange(null)}
                aria-label={t('Remove reference image')}
                className='bg-card text-foreground absolute -top-1.5 -right-1.5 flex size-5 items-center justify-center rounded-lg border shadow-[0_1px_2px_rgba(10,37,64,0.12)] transition-all duration-[300ms] ease-out hover:-translate-y-0.5 focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none motion-reduce:transition-none motion-reduce:hover:translate-y-0'
              >
                <X className='size-3' />
              </button>
            </div>
            <span className='text-muted-foreground text-[11px]'>
              {t('The reference image is sent with your prompt')}
            </span>
          </InputGroupAddon>
        )}

        <InputGroupTextarea
          ref={textareaRef}
          value={prompt}
          onChange={(e) => onPromptChange(e.target.value)}
          onKeyDown={(e) => {
            // 按住回车会连发 keydown；Safari 输入法确认候选词的回车 isComposing 为 false、keyCode 为 229
            if (
              e.key !== 'Enter' ||
              e.shiftKey ||
              e.repeat ||
              e.nativeEvent.isComposing ||
              e.nativeEvent.keyCode === 229
            ) {
              return
            }
            e.preventDefault()
            if (canGenerate) onGenerate()
          }}
          onPaste={(e) => {
            const file = Array.from(e.clipboardData?.files ?? []).find((f) =>
              f.type.startsWith('image/')
            )
            if (!file) return
            e.preventDefault()
            void attach(file)
          }}
          placeholder={t(
            'Describe the image: subject, scene, style, lighting, composition'
          )}
          maxLength={4000}
          className='field-sizing-content max-h-48 min-h-16 px-4 text-sm'
        />

        <InputGroupAddon
          align='block-end'
          className='cursor-default flex-wrap gap-1.5 px-2.5 pb-2.5'
        >
          <KeySelector
            keys={relay.keys}
            activeKey={relay.activeKey}
            onSelect={onSelectKey}
            loading={relay.keysLoading}
          />
          <ModelSelector
            models={models.map((m) => ({ id: m.id, hint: m.spec.family }))}
            value={model}
            onChange={onModelChange}
            loading={relay.modelsLoading}
            error={failure.error}
            onRetry={failure.retry}
            disabled={!relay.activeKey && !failure.error}
            emptyText={t(
              'This key has no image models. Pick another key or allow image models in the key settings.'
            )}
          />

          {spec && params && (
            <>
              <ParamMenu
                label={t('Size')}
                icon={<AspectGlyph size={params.size} />}
                value={params.size}
                options={spec.sizes.map((s) => ({
                  value: s,
                  label: s === 'auto' ? t('Auto size') : formatSize(s),
                  icon: <AspectGlyph size={s} />,
                }))}
                onChange={(v) => set('size', v)}
              />
              {spec.maxN > 1 && (
                <ParamMenu
                  label={t('Images per run')}
                  icon={<Images className='text-muted-foreground size-3.5' />}
                  value={String(params.n)}
                  display={`×${params.n}`}
                  options={Array.from({ length: spec.maxN }, (_, i) => ({
                    value: String(i + 1),
                    label: t('{{n}} images', { n: i + 1 }),
                  }))}
                  onChange={(v) => set('n', Number(v))}
                />
              )}
              {spec.qualities && (
                <ParamMenu
                  label={t('Quality')}
                  value={params.quality}
                  display={`${t('Quality')} · ${t(QUALITY_LABELS[params.quality] ?? params.quality)}`}
                  options={spec.qualities.map((q) => ({
                    value: q,
                    label: t(QUALITY_LABELS[q] ?? q),
                  }))}
                  onChange={(v) => set('quality', v)}
                />
              )}
              {spec.styles && (
                <ParamMenu
                  label={t('Style')}
                  value={params.style}
                  display={`${t('Style')} · ${t(STYLE_LABELS[params.style] ?? params.style)}`}
                  options={spec.styles.map((s) => ({
                    value: s,
                    label: t(STYLE_LABELS[s] ?? s),
                  }))}
                  onChange={(v) => set('style', v)}
                />
              )}
              {spec.resolutions && (
                <ParamMenu
                  label={t('Resolution')}
                  value={params.resolution}
                  options={spec.resolutions.map((r) => ({
                    value: r,
                    label: r,
                  }))}
                  onChange={(v) => set('resolution', v)}
                />
              )}
              {acceptsReference && !reference && (
                <button
                  type='button'
                  onClick={() => fileInputRef.current?.click()}
                  aria-label={t('Reference image')}
                  className={composerChip}
                >
                  <ImagePlus className='text-muted-foreground size-3.5' />
                  <span className='hidden sm:inline'>
                    {t('Reference image')}
                  </span>
                </button>
              )}
            </>
          )}

          <div className='ml-auto flex items-center gap-2'>
            {runningCount > 0 && (
              <span className='text-muted-foreground hidden items-center gap-1 text-[11px] sm:inline-flex'>
                <Loader2 className='size-3 animate-spin motion-reduce:animate-none' />
                {t('{{n}} generating', { n: runningCount })}
              </span>
            )}
            <button
              type='button'
              onClick={onGenerate}
              disabled={!canGenerate}
              className={composerPrimary}
            >
              <Sparkles className='size-3.5' />
              {t('Generate')}
            </button>
          </div>
        </InputGroupAddon>
      </InputGroup>

      <input
        ref={fileInputRef}
        type='file'
        accept='image/png,image/jpeg,image/webp'
        className='hidden'
        onChange={(e) => {
          void attach(e.target.files?.[0])
          e.target.value = ''
        }}
      />
    </div>
  )
}

interface ParamOption {
  value: string
  label: string
  icon?: ReactNode
}

function ParamMenu({
  label,
  icon,
  value,
  display,
  options,
  onChange,
}: {
  /** 菜单标题，同时作为按钮的无障碍名称 */
  label: string
  icon?: ReactNode
  value: string
  /** 按钮上的文字，默认用选中项的 label */
  display?: string
  options: ParamOption[]
  onChange: (value: string) => void
}) {
  const current = options.find((o) => o.value === value)
  const text = display ?? current?.label ?? value
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type='button'
          aria-label={`${label}: ${text}`}
          className={composerChip}
        >
          {icon}
          <span className='truncate'>{text}</span>
          <ChevronDown className='text-muted-foreground size-3.5 shrink-0' />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='start' className='min-w-40 rounded-xl'>
        <DropdownMenuLabel className='text-muted-foreground text-[11px] font-medium'>
          {label}
        </DropdownMenuLabel>
        <DropdownMenuRadioGroup value={value} onValueChange={onChange}>
          {options.map((o) => (
            <DropdownMenuRadioItem
              key={o.value}
              value={o.value}
              className='gap-2 text-xs'
            >
              {o.icon}
              {o.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** 按宽高比画的小矩形，让尺寸选项一眼看出横竖 */
function AspectGlyph({ size }: { size: string }) {
  const aspect = sizeToAspect(size)
  if (!aspect) {
    return <ImageIcon className='text-muted-foreground size-3.5 shrink-0' />
  }
  const box = 13
  const width = aspect >= 1 ? box : Math.max(5, box * aspect)
  const height = aspect >= 1 ? Math.max(5, box / aspect) : box
  return (
    <span className='flex size-3.5 shrink-0 items-center justify-center'>
      <span
        className={cn('border-muted-foreground rounded-[2px] border-[1.5px]')}
        style={{ width, height }}
      />
    </span>
  )
}

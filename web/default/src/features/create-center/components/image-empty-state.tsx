// 生图空态：一句话说明 + 三张示例提示词卡，点了只填进输入框不直接生成（避免误扣费）。
import { ImagePlus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

const EXAMPLES = [
  {
    tag: 'Product shot',
    prompt:
      'A minimalist ceramic coffee cup on a linen tablecloth, soft morning light, product photography',
  },
  {
    tag: 'Illustration',
    prompt:
      'An isometric illustration of a cozy reading nook with plants and warm lamps',
  },
  {
    tag: 'Poster',
    prompt:
      'A retro travel poster of a lighthouse at dusk, bold flat colors, space for a large title',
  },
] as const

export function ImageEmptyState({
  onPick,
}: {
  onPick: (prompt: string) => void
}) {
  const { t } = useTranslation()
  return (
    <div className='flex min-h-full flex-col items-center justify-center py-10 text-center'>
      <span className='bg-card text-primary shadow-surface flex size-11 items-center justify-center rounded-xl border'>
        <ImagePlus className='size-5' />
      </span>
      <h2 className='mt-4 text-xl font-semibold'>
        {t('Describe the image you want')}
      </h2>
      <p className='text-muted-foreground mt-1.5 max-w-md text-sm leading-6 text-balance'>
        {t(
          'Pick one of your API keys and an image model below, then describe the scene. Images are billed to that key.'
        )}
      </p>
      <div className='mt-8 grid w-full max-w-3xl gap-3 text-left sm:grid-cols-3'>
        {EXAMPLES.map((example) => (
          <button
            key={example.tag}
            type='button'
            onClick={() => onPick(t(example.prompt))}
            className='bg-card text-card-foreground shadow-surface rounded-xl border p-4 text-left transition-all duration-[400ms] ease-out hover:-translate-y-1 hover:shadow-[0_12px_30px_rgba(0,0,0,0.08)] focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] focus-visible:outline-none active:scale-[0.98] motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100'
          >
            <span className='text-primary text-[11px] font-semibold'>
              {t(example.tag)}
            </span>
            <span className='mt-1.5 line-clamp-3 block text-sm leading-6'>
              {t(example.prompt)}
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}

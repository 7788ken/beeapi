import { Shuffle, type LucideIcon } from 'lucide-react'
import {
  OpenAI,
  Claude,
  Gemini,
  DeepSeek,
  Qwen,
  XAI,
} from '@lobehub/icons'

const ICON_SIZE = 20

export interface Platform {
  key: string
  /** i18n key for the section label */
  labelKey: string
  /** Optional Lucide icon (auto routing). */
  lucide?: LucideIcon
  /**
   * @lobehub/icons brand component, may be a `.Color` variant.
   * Falls back to lucide icon when undefined.
   */
  brand?: React.ComponentType<{ size?: number }>
  toneClass: string
  match: (haystack: string) => boolean
}

export const PLATFORMS: Platform[] = [
  {
    key: 'auto',
    labelKey: 'Smart routing',
    lucide: Shuffle,
    toneClass: 'bg-emerald-50 text-emerald-700',
    match: (s) => /(^|\s)default\b|^auto$|智能/i.test(s),
  },
  {
    key: 'openai',
    labelKey: 'OpenAI',
    brand: OpenAI as React.ComponentType<{ size?: number }>,
    toneClass: 'bg-zinc-100 text-zinc-700',
    match: (s) => /\bgpt\b|openai|codex|chatgpt|o1\b|o3\b|o4\b|azure/i.test(s),
  },
  {
    key: 'claude',
    labelKey: 'Anthropic Claude',
    brand: Claude.Color as React.ComponentType<{ size?: number }>,
    toneClass: 'bg-orange-50 text-orange-700',
    match: (s) =>
      /claude|anthropic|sonnet|opus|haiku|kiro|anit|windsurf|cx2cc/i.test(s),
  },
  {
    key: 'gemini',
    labelKey: 'Google Gemini',
    brand: Gemini.Color as React.ComponentType<{ size?: number }>,
    toneClass: 'bg-blue-50 text-blue-700',
    match: (s) => /gemini|google|bard|vertex|banana|香蕉/i.test(s),
  },
  {
    key: 'deepseek',
    labelKey: 'DeepSeek',
    brand: DeepSeek.Color as React.ComponentType<{ size?: number }>,
    toneClass: 'bg-indigo-50 text-indigo-700',
    match: (s) => /deepseek/i.test(s),
  },
  {
    key: 'xai',
    labelKey: 'xAI Grok',
    brand: XAI as React.ComponentType<{ size?: number }>,
    toneClass: 'bg-neutral-100 text-neutral-700',
    match: (s) => /\bxai\b|grok/i.test(s),
  },
  {
    key: 'cn',
    labelKey: 'Chinese models',
    brand: Qwen.Color as React.ComponentType<{ size?: number }>,
    toneClass: 'bg-violet-50 text-violet-700',
    match: (s) =>
      /made in china|国产|minimax|glm|chatglm|kimi|moonshot|qwen|tongyi|通义|doubao|豆包|hunyuan|混元|wenxin|文心|spark|讯飞|yi-|零一|01ai/i.test(
        s
      ),
  },
]

export const OTHER_PLATFORM: Pick<Platform, 'key' | 'labelKey' | 'toneClass'> =
  {
    key: 'other',
    labelKey: 'Other',
    toneClass: 'bg-gray-100 text-gray-600',
  }

export function classifyGroup(name: string, desc: string): string {
  const haystack = `${name} ${desc}`
  for (const p of PLATFORMS) {
    if (p.match(haystack)) return p.key
  }
  return OTHER_PLATFORM.key
}

export function renderPlatformIcon(p: Platform) {
  if (p.brand) {
    const Brand = p.brand
    return <Brand size={ICON_SIZE} />
  }
  if (p.lucide) {
    const L = p.lucide
    return <L size={ICON_SIZE} strokeWidth={2} />
  }
  return <span className='text-xs font-semibold'>·</span>
}

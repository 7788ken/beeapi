import { OpenAI, Claude, Gemini, DeepSeek, Qwen, XAI } from '@lobehub/icons'
import { Shuffle, type LucideIcon } from 'lucide-react'

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
  match: (haystack: string) => boolean
}

export const PLATFORMS: Platform[] = [
  {
    key: 'auto',
    labelKey: 'Smart routing',
    lucide: Shuffle,
    match: (s) => /(^|\s)default\b|^auto$|智能/i.test(s),
  },
  {
    key: 'openai',
    labelKey: 'OpenAI',
    brand: OpenAI as React.ComponentType<{ size?: number }>,
    match: (s) => /\bgpt\b|openai|codex|chatgpt|o1\b|o3\b|o4\b|azure/i.test(s),
  },
  {
    key: 'claude',
    labelKey: 'Anthropic Claude',
    brand: Claude.Color as React.ComponentType<{ size?: number }>,
    match: (s) =>
      /claude|anthropic|sonnet|opus|haiku|kiro|anit|windsurf|cx2cc/i.test(s),
  },
  {
    key: 'gemini',
    labelKey: 'Google Gemini',
    brand: Gemini.Color as React.ComponentType<{ size?: number }>,
    match: (s) => /gemini|google|bard|vertex|banana|香蕉/i.test(s),
  },
  {
    key: 'deepseek',
    labelKey: 'DeepSeek',
    brand: DeepSeek.Color as React.ComponentType<{ size?: number }>,
    match: (s) => /deepseek/i.test(s),
  },
  {
    key: 'xai',
    labelKey: 'xAI Grok',
    brand: XAI as React.ComponentType<{ size?: number }>,
    match: (s) => /\bxai\b|grok/i.test(s),
  },
  {
    key: 'cn',
    labelKey: 'Chinese models',
    brand: Qwen.Color as React.ComponentType<{ size?: number }>,
    match: (s) =>
      /made in china|国产|minimax|glm|chatglm|kimi|moonshot|qwen|tongyi|通义|doubao|豆包|hunyuan|混元|wenxin|文心|spark|讯飞|yi-|零一|01ai/i.test(
        s
      ),
  },
]

export const OTHER_PLATFORM: Pick<Platform, 'key' | 'labelKey'> = {
  key: 'other',
  labelKey: 'Other',
}

/** skip：要跳过的分类（可用分组页已隐藏 default/auto，不再用「智能路由」兜住简介里带「智能」的普通分组） */
export function classifyGroup(
  name: string,
  desc: string,
  skip?: string
): string {
  const haystack = `${name} ${desc}`
  for (const p of PLATFORMS) {
    if (p.key !== skip && p.match(haystack)) return p.key
  }
  return OTHER_PLATFORM.key
}

export function renderPlatformIcon(p: Platform, size = ICON_SIZE) {
  if (p.brand) {
    const Brand = p.brand
    return <Brand size={size} />
  }
  if (p.lucide) {
    const L = p.lucide
    return <L size={size} strokeWidth={2} />
  }
  return <span className='text-xs font-semibold'>·</span>
}

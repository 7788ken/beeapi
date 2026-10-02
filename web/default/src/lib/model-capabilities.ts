// 按 /v1/models 返回的模型判断：能不能对话、能不能生图、生图走哪条协议、支持哪些参数。
// 端点类型（supported_endpoint_types）来自后台模型元数据，但 gpt-image-2、Gemini 生图这类模型默认没有
// image-generation 标记，所以还要按模型家族名识别；家族名同时决定参数表。
import type { RelayModel } from '@/lib/relay-client'

/**
 * images：/v1/images/generations（带参考图时 /v1/images/edits）。
 * chat：/v1/chat/completions，图片以 markdown data URL 写在回复里——Gemini 原生渠道只支持这条（适配器的生图接口只认 imagen）。
 */
export type ImageProtocol = 'images' | 'chat'

export interface ImageModelSpec {
  /** 家族名，下拉里做副标题 */
  family: string
  protocol: ImageProtocol
  /** images 协议是 size（如 1024x1536），chat 协议是 aspect_ratio（如 3:4） */
  sizes: string[]
  defaultSize: string
  /** auto 表示不传、由上游决定 */
  qualities?: string[]
  defaultQuality?: string
  styles?: string[]
  /** Gemini Pro 生图的 image_size */
  resolutions?: string[]
  maxN: number
  supportsReference: boolean
}

const GPT_IMAGE: ImageModelSpec = {
  family: 'GPT Image',
  protocol: 'images',
  sizes: ['1024x1024', '1536x1024', '1024x1536', 'auto'],
  defaultSize: '1024x1024',
  qualities: ['auto', 'low', 'medium', 'high'],
  defaultQuality: 'auto',
  maxN: 4,
  supportsReference: true,
}

const DALL_E_3: ImageModelSpec = {
  family: 'DALL·E 3',
  protocol: 'images',
  sizes: ['1024x1024', '1792x1024', '1024x1792'],
  defaultSize: '1024x1024',
  qualities: ['standard', 'hd'],
  defaultQuality: 'standard',
  styles: ['vivid', 'natural'],
  maxN: 1,
  supportsReference: false,
}

const DALL_E_2: ImageModelSpec = {
  family: 'DALL·E 2',
  protocol: 'images',
  sizes: ['256x256', '512x512', '1024x1024'],
  defaultSize: '1024x1024',
  maxN: 4,
  supportsReference: false,
}

// Imagen 只收 1:1/3:4/4:3/9:16/16:9；size 带冒号时后端原样当 aspectRatio 用，quality=hd 时出 2K
const IMAGEN: ImageModelSpec = {
  family: 'Imagen',
  protocol: 'images',
  sizes: ['1:1', '3:4', '4:3', '9:16', '16:9'],
  defaultSize: '1:1',
  qualities: ['standard', 'hd'],
  defaultQuality: 'standard',
  maxN: 4,
  supportsReference: false,
}

const GEMINI_ASPECTS = ['1:1', '3:4', '4:3', '9:16', '16:9', '2:3', '3:2']

function geminiSpec(isPro: boolean): ImageModelSpec {
  return {
    family: isPro ? 'Nano Banana Pro' : 'Nano Banana',
    protocol: 'chat',
    sizes: GEMINI_ASPECTS,
    defaultSize: '1:1',
    // Flash 生图会静默忽略 image_size、恒出 1K，只给 Pro 开分辨率
    resolutions: isPro ? ['1K', '2K', '4K'] : undefined,
    maxN: 1,
    supportsReference: true,
  }
}

const GENERIC_IMAGE: ImageModelSpec = {
  family: 'Image',
  protocol: 'images',
  sizes: ['1024x1024', '1536x1024', '1024x1536'],
  defaultSize: '1024x1024',
  maxN: 1,
  supportsReference: false,
}

const GEMINI_IMAGE_RE = /gemini.*image|nano-?banana/
const GENERIC_IMAGE_RE = /seedream|flux|qwen-image|wanx|kolors|cogview/

function hasImageEndpoint(model: RelayModel): boolean {
  return model.endpointTypes.includes('image-generation')
}

/** 返回 null 表示不是生图模型，不在生图页出现。 */
export function getImageModelSpec(model: RelayModel): ImageModelSpec | null {
  const id = model.id.toLowerCase()
  if (id.startsWith('gpt-image') || id.startsWith('chatgpt-image')) {
    return GPT_IMAGE
  }
  if (id.startsWith('dall-e-3')) return DALL_E_3
  if (id.startsWith('dall-e-2')) return DALL_E_2
  if (id.startsWith('imagen')) return IMAGEN
  if (GEMINI_IMAGE_RE.test(id)) {
    const spec = geminiSpec(id.includes('pro'))
    // 后台显式标了 image-generation（如走支持生图接口的 OpenAI 兼容上游）就按通用生图接口发，参数用像素尺寸
    return hasImageEndpoint(model)
      ? { ...GENERIC_IMAGE, family: spec.family }
      : spec
  }
  if (GENERIC_IMAGE_RE.test(id) || hasImageEndpoint(model)) return GENERIC_IMAGE
  return null
}

// 网关会把 /v1/chat/completions 转成 Claude / Gemini 格式；只支持 responses 的模型（如 o3-pro）默认不转，不列
const CHAT_ENDPOINTS = ['openai', 'anthropic', 'gemini']

/** 对话页列出的模型：能走 /v1/chat/completions 的，排除只能走生图接口的模型。 */
export function isChatModel(model: RelayModel): boolean {
  if (getImageModelSpec(model)?.protocol === 'images') return false
  return (
    model.endpointTypes.length === 0 ||
    model.endpointTypes.some((t) => CHAT_ENDPOINTS.includes(t))
  )
}

import { api } from '@/lib/api'
import type { ImageModelSpec } from '@/lib/model-capabilities'
import { relayRequest } from '@/lib/relay-client'
import type { ImageParams, MidjourneyTask } from './types'

/** 模型回了文字却没有图片（例如提示词被拒、模型只做了描述）；text 是模型原话。 */
export class NoImageReturnedError extends Error {
  readonly text: string

  constructor(text: string) {
    super('The model returned no image')
    this.name = 'NoImageReturnedError'
    this.text = text
  }
}

export interface ReferenceImage {
  file: File
  /** data URL，用于预览和 chat 协议的 image_url */
  dataUrl: string
}

export interface GenerateImagesInput {
  secret: string
  spec: ImageModelSpec
  model: string
  prompt: string
  params: ImageParams
  reference: ReferenceImage | null
}

/** 返回可直接放进 <img src> 的地址（data: 或 http(s):）。 */
export function generateImages(input: GenerateImagesInput): Promise<string[]> {
  return input.spec.protocol === 'chat'
    ? generateViaChat(input)
    : generateViaImagesApi(input)
}

interface ImagesApiResponse {
  data?: Array<{ b64_json?: string; url?: string }>
}

async function generateViaImagesApi({
  secret,
  spec,
  model,
  prompt,
  params,
  reference,
}: GenerateImagesInput): Promise<string[]> {
  let body: ImagesApiResponse
  if (reference && spec.supportsReference) {
    const form = new FormData()
    form.set('model', model)
    form.set('prompt', prompt)
    form.set('n', String(params.n))
    form.set('size', params.size)
    if (params.quality && params.quality !== 'auto') {
      form.set('quality', params.quality)
    }
    form.set('image', reference.file, reference.file.name)
    body = await relayRequest<ImagesApiResponse>(secret, '/v1/images/edits', {
      form,
    })
  } else {
    const json: Record<string, unknown> = {
      model,
      prompt,
      n: params.n,
      size: params.size,
    }
    if (params.quality && params.quality !== 'auto') {
      json.quality = params.quality
    }
    if (params.style) json.style = params.style
    body = await relayRequest<ImagesApiResponse>(
      secret,
      '/v1/images/generations',
      { json }
    )
  }
  const images = (body.data ?? [])
    .map((d) => (d.b64_json ? `data:image/png;base64,${d.b64_json}` : d.url))
    .filter((src): src is string => !!src)
  if (images.length === 0) throw new NoImageReturnedError('')
  return images
}

type ChatContentPart = {
  type?: string
  text?: string
  image_url?: { url?: string }
}

interface ChatResponse {
  choices?: Array<{
    message?: {
      content?: string | ChatContentPart[] | null
      images?: ChatContentPart[]
    }
  }>
}

// Gemini 生图经网关转成 OpenAI 格式后，图片写成 markdown：![image](data:image/png;base64,...)
const MARKDOWN_IMAGE_RE =
  /!\[[^\]]*\]\((data:image\/[\w.+-]+;base64,[A-Za-z0-9+/=]+|https?:\/\/[^\s)]+)\)/g

async function generateViaChat({
  secret,
  spec,
  model,
  prompt,
  params,
  reference,
}: GenerateImagesInput): Promise<string[]> {
  const content = reference
    ? [
        { type: 'text', text: prompt },
        { type: 'image_url', image_url: { url: reference.dataUrl } },
      ]
    : prompt
  const json: Record<string, unknown> = {
    model,
    stream: false,
    messages: [{ role: 'user', content }],
  }
  // 默认值不传，避免不认识 extra_body 的上游报错
  const imageConfig: Record<string, string> = {}
  if (params.size !== spec.defaultSize) imageConfig.aspect_ratio = params.size
  if (params.resolution && params.resolution !== spec.resolutions?.[0]) {
    imageConfig.image_size = params.resolution
  }
  if (Object.keys(imageConfig).length > 0) {
    json.extra_body = { google: { image_config: imageConfig } }
  }

  const body = await relayRequest<ChatResponse>(
    secret,
    '/v1/chat/completions',
    { json }
  )
  const message = body.choices?.[0]?.message
  const parts = Array.isArray(message?.content) ? message.content : []
  const text =
    typeof message?.content === 'string'
      ? message.content
      : parts.map((p) => p.text ?? '').join('')

  const images = [
    ...Array.from(text.matchAll(MARKDOWN_IMAGE_RE), (m) => m[1]),
    ...[...parts, ...(message?.images ?? [])]
      .map((p) => p.image_url?.url)
      .filter((src): src is string => !!src),
  ]
  if (images.length === 0) {
    throw new NoImageReturnedError(text.replace(MARKDOWN_IMAGE_RE, '').trim())
  }
  return images
}

/** 拉取当前用户的 MJ 历史任务（按时间倒序，最多 N 条） */
export async function getMidjourneyHistory(
  pageSize = 50
): Promise<MidjourneyTask[]> {
  const res = await api.get('/api/mj/self', {
    params: { p: 1, page_size: pageSize },
  })
  if (!res.data?.success) return []
  const items = res.data.data?.items
  if (!Array.isArray(items)) return []
  return items as MidjourneyTask[]
}

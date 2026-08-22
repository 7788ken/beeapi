import { api } from '@/lib/api'
import type { GenerateImageRequest, GeneratedImage, MidjourneyTask } from './types'

/**
 * 调用图像生成。利用 backend 的 /pg/chat/completions
 * 在检测到 image 模型时自动重写为 /v1/images/generations 的特性，
 * 前端不用关心鉴权差异（统一走 session cookie）。
 *
 * 不同模型支持不同 size / quality / 参考图组合；由 model-presets.ts 描述。
 */
export async function generateImage(req: GenerateImageRequest): Promise<GeneratedImage[]> {
  const payload: Record<string, unknown> = {
    model: req.model,
    prompt: req.prompt,
    n: req.n,
  }
  if (req.size) payload.size = req.size
  if (req.quality) payload.quality = req.quality
  if (req.style) payload.style = req.style
  if (req.reference_image_b64) {
    // GPT-Image-2 / Gemini Image 接受 image 输入；OpenAI 兼容协议没标准化，
    // 这里放在 messages.content 的 multipart 形态，让 backend 适配器处理。
    payload.image = req.reference_image_b64
  }

  const res = await api.post('/pg/chat/completions', payload, {
    skipErrorHandler: true,
  } as Record<string, unknown>)

  // OpenAI 图像生成响应格式：{ created, data: [{ b64_json, url, revised_prompt }] }
  const body = res.data
  if (Array.isArray(body?.data)) {
    return body.data as GeneratedImage[]
  }
  // 兜底：某些模型返回 chat 形态，content 包含 markdown 图片
  // 这里不解析 markdown，直接报错让 UI 提示
  throw new Error('Unexpected response format from image generation')
}

/** 获取当前用户可用模型列表 */
export async function getUserModels(): Promise<string[]> {
  const res = await api.get('/api/user/models')
  if (!res.data?.success || !Array.isArray(res.data.data)) return []
  return res.data.data as string[]
}

/** 拉取当前用户的 MJ 历史任务（按时间倒序，最多 N 条） */
export async function getMidjourneyHistory(pageSize = 50): Promise<MidjourneyTask[]> {
  const res = await api.get('/api/mj/self', {
    params: { p: 1, page_size: pageSize },
  })
  if (!res.data?.success) return []
  const items = res.data.data?.items
  if (!Array.isArray(items)) return []
  return items as MidjourneyTask[]
}

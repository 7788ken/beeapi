// Create Center 类型定义

/** OpenAI 兼容图像生成响应中的单张图 */
export interface GeneratedImage {
  /** base64 编码（如果后端返回 b64_json） */
  b64_json?: string
  /** 图像 URL（如果后端返回 url） */
  url?: string
  /** 修订后的 prompt（部分模型返回） */
  revised_prompt?: string
}

/** 单次图像生成任务的本地状态 */
export interface ImageJob {
  /** 本地唯一 id（前端生成，不依赖后端） */
  id: string
  prompt: string
  model: string
  size: string
  n: number
  /** 'pending' 排队 / 'running' 进行中 / 'done' 完成 / 'error' 失败 */
  status: 'pending' | 'running' | 'done' | 'error'
  /** 创建时间 (unix ms) */
  createdAt: number
  /** 错误信息（status='error' 时） */
  errorMessage?: string
  /** 生成的图像数组（status='done' 时） */
  images?: GeneratedImage[]
}

/** 图像生成请求 payload */
export interface GenerateImageRequest {
  model: string
  prompt: string
  /** 数量；DALL-E 3 仅支持 n=1 */
  n: number
  /** 尺寸 e.g. "1024x1024" */
  size?: string
  /** 质量 (DALL-E 3): standard|hd */
  quality?: string
  /** 风格 (DALL-E 3): vivid|natural */
  style?: string
  /** 参考图（base64 data URL）— GPT-Image-2 / Gemini Image 支持 */
  reference_image_b64?: string
}

/** MJ 历史任务（来自 /api/mj/self，简化字段） */
export interface MidjourneyTask {
  id: number
  user_id: number
  mj_id: string
  action: string
  status: string
  progress: string
  prompt: string
  prompt_en?: string
  image_url?: string
  start_time: number
  finish_time: number
  fail_reason?: string
}

// Create Center 类型定义
import type { GenerateImagesInput } from './api'

export interface ImageParams {
  /** images 协议是 size（如 1024x1536 / auto），chat 协议是 aspect_ratio（如 3:4） */
  size: string
  n: number
  /** auto 或空串表示不传、由上游决定 */
  quality: string
  style: string
  /** Gemini Pro 生图的 image_size（1K/2K/4K），其它模型为空 */
  resolution: string
}

/** 一次生图任务；只存在当前页面内存里，刷新即清空。 */
export interface ImageJob {
  id: string
  /** 重新生成时原样再发一次（含当时的 key） */
  input: GenerateImagesInput
  keyId: number
  keyName: string
  status: 'running' | 'done' | 'error'
  startedAt: number
  finishedAt?: number
  images: string[]
  error?: string
  /** 模型只回了文字没回图时的原话 */
  modelText?: string
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

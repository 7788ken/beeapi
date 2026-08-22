// 图像模型识别 + 默认参数（按模型名前缀匹配）。
// 用户的可用模型从 /api/user/models 拉取，本文件只判断哪些是"图像生成模型"以及它们各自支持的参数 schema。

export interface ImageModelCapabilities {
  /** 用于 UI 展示的简短标签 */
  label: string
  /** 支持的尺寸列表 */
  sizes: string[]
  /** 默认尺寸 */
  defaultSize: string
  /** 是否支持 quality 参数（standard/hd） */
  supportsQuality?: boolean
  /** 是否支持 style 参数（vivid/natural） */
  supportsStyle?: boolean
  /** 是否支持参考图上传 */
  supportsReferenceImage?: boolean
  /** 默认数量 n */
  defaultN: number
  /** 最大 n */
  maxN: number
}

/**
 * 按前缀匹配模型能力。返回 null 表示不是图像生成模型（不在创作中心 Image tab 暴露）。
 */
export function getImageModelCapabilities(modelName: string): ImageModelCapabilities | null {
  const lower = modelName.toLowerCase()

  // OpenAI DALL-E 3
  if (lower.startsWith('dall-e-3') || lower === 'dall-e-3') {
    return {
      label: 'DALL·E 3',
      sizes: ['1024x1024', '1792x1024', '1024x1792'],
      defaultSize: '1024x1024',
      supportsQuality: true,
      supportsStyle: true,
      defaultN: 1,
      maxN: 1, // DALL-E 3 仅支持 n=1
    }
  }

  // OpenAI DALL-E 2
  if (lower.startsWith('dall-e-2') || lower === 'dall-e-2') {
    return {
      label: 'DALL·E 2',
      sizes: ['256x256', '512x512', '1024x1024'],
      defaultSize: '1024x1024',
      defaultN: 1,
      maxN: 4,
    }
  }

  // GPT-Image-2 / GPT-Image / chatgpt-image
  if (lower.startsWith('gpt-image') || lower.startsWith('chatgpt-image')) {
    return {
      label: 'GPT Image',
      sizes: ['1024x1024', '1536x1024', '1024x1536', 'auto'],
      defaultSize: '1024x1024',
      supportsQuality: true,
      supportsReferenceImage: true,
      defaultN: 1,
      maxN: 4,
    }
  }

  // Gemini Image 系列（newapi 已适配为 OpenAI 兼容）
  if (lower.includes('gemini') && (lower.includes('image') || lower.includes('vision'))) {
    return {
      label: 'Gemini Image',
      sizes: ['1024x1024', '1536x1024', '1024x1536'],
      defaultSize: '1024x1024',
      supportsReferenceImage: true,
      defaultN: 1,
      maxN: 1,
    }
  }

  // Midjourney 系列：本期创作中心不发起新任务（需要 API token，前端 session 调不通）；
  // 只在 History tab 展示历史。这里返回 null。
  return null
}

/** 判断模型是否在 Image tab 可发起 */
export function isImageCreatableModel(modelName: string): boolean {
  return getImageModelCapabilities(modelName) !== null
}

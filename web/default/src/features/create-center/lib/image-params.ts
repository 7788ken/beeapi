import type { ImageModelSpec } from '@/lib/model-capabilities'
import type { ImageParams } from '../types'

export function defaultImageParams(spec: ImageModelSpec): ImageParams {
  return {
    size: spec.defaultSize,
    n: 1,
    quality: spec.defaultQuality ?? '',
    style: spec.styles?.[0] ?? '',
    resolution: spec.resolutions?.[0] ?? '',
  }
}

/** 切模型或复用旧记录时：新模型支持的参数原样保留，不支持的退回该模型默认值。 */
export function fitImageParams(
  spec: ImageModelSpec,
  params: ImageParams
): ImageParams {
  const fallback = defaultImageParams(spec)
  return {
    size: spec.sizes.includes(params.size) ? params.size : fallback.size,
    n: Math.min(Math.max(1, params.n), spec.maxN),
    quality: spec.qualities?.includes(params.quality)
      ? params.quality
      : fallback.quality,
    style: spec.styles?.includes(params.style) ? params.style : fallback.style,
    resolution: spec.resolutions?.includes(params.resolution)
      ? params.resolution
      : fallback.resolution,
  }
}

/** 1024x1536 / 3:4 → 宽高比；auto 返回 undefined（按图片自身比例）。 */
export function sizeToAspect(size: string): number | undefined {
  const match = size.match(/^(\d+)\s*[x:]\s*(\d+)$/i)
  return match ? Number(match[1]) / Number(match[2]) : undefined
}

/** 值都是 i18n key，展示时 t(label) */
export const QUALITY_LABELS: Record<string, string> = {
  auto: 'Auto',
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  standard: 'Standard',
  hd: 'HD',
}

export const STYLE_LABELS: Record<string, string> = {
  vivid: 'Vivid',
  natural: 'Natural',
}

export function formatSize(size: string): string {
  return size.replace('x', '×')
}

export function formatElapsed(ms: number): string {
  const sec = Math.max(0, Math.round(ms / 1000))
  return sec < 60 ? `${sec}s` : `${Math.floor(sec / 60)}m ${sec % 60}s`
}

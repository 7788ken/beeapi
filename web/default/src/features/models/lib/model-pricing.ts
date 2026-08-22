import type { ModelSettings } from '@/features/system-settings/types'
import { safeJsonParse } from '@/features/system-settings/utils/json-parser'

export type PricingMode = 'per-token' | 'per-request'

export type PricingFields = {
  price?: string
  ratio?: string
  cacheRatio?: string
  completionRatio?: string
  imageRatio?: string
  audioRatio?: string
  audioCompletionRatio?: string
}

export type PricingConfig = {
  mode: PricingMode
  fields: PricingFields
  promptPrice: string
  completionPrice: string
  advancedOpen: boolean
  managedExternally: boolean
}

export const MODEL_PRICING_OPTION_KEYS = [
  'ModelPrice',
  'ModelRatio',
  'CacheRatio',
  'CreateCacheRatio',
  'CompletionRatio',
  'ImageRatio',
  'AudioRatio',
  'AudioCompletionRatio',
  'billing_setting.billing_mode',
  'billing_setting.billing_expr',
] as const

export type ModelPricingOptionKey = (typeof MODEL_PRICING_OPTION_KEYS)[number]

export type ModelPricingSettings = Pick<ModelSettings, ModelPricingOptionKey>

export type ModelPricingMaps = {
  ModelPrice: Record<string, number>
  ModelRatio: Record<string, number>
  CacheRatio: Record<string, number>
  CreateCacheRatio: Record<string, number>
  CompletionRatio: Record<string, number>
  ImageRatio: Record<string, number>
  AudioRatio: Record<string, number>
  AudioCompletionRatio: Record<string, number>
  'billing_setting.billing_mode': Record<string, string>
  'billing_setting.billing_expr': Record<string, string>
}

export type ModelPricingMutation = {
  isEditing: boolean
  oldModelName: string
  finalModelName: string
  loadedPricingName: string
  pricingMode: PricingMode
  fields: PricingFields
}

const EDITABLE_PRICING_OPTION_KEYS = [
  'ModelPrice',
  'ModelRatio',
  'CacheRatio',
  'CompletionRatio',
  'ImageRatio',
  'AudioRatio',
  'AudioCompletionRatio',
] as const

const PRESERVED_PRICING_OPTION_KEYS = [
  'CreateCacheRatio',
  'billing_setting.billing_mode',
  'billing_setting.billing_expr',
] as const

const emptyPricingFields = (): PricingFields => ({
  price: '',
  ratio: '',
  cacheRatio: '',
  completionRatio: '',
  imageRatio: '',
  audioRatio: '',
  audioCompletionRatio: '',
})

const parseNumberMap = (rawMap: string): Record<string, number> =>
  safeJsonParse<Record<string, number>>(rawMap, {
    fallback: {},
    silent: true,
  })

const parseStringMap = (rawMap: string): Record<string, string> =>
  safeJsonParse<Record<string, string>>(rawMap, {
    fallback: {},
    silent: true,
  })

export function parseModelPricingMaps(
  settings: ModelPricingSettings
): ModelPricingMaps {
  return {
    ModelPrice: parseNumberMap(settings.ModelPrice),
    ModelRatio: parseNumberMap(settings.ModelRatio),
    CacheRatio: parseNumberMap(settings.CacheRatio),
    CreateCacheRatio: parseNumberMap(settings.CreateCacheRatio),
    CompletionRatio: parseNumberMap(settings.CompletionRatio),
    ImageRatio: parseNumberMap(settings.ImageRatio),
    AudioRatio: parseNumberMap(settings.AudioRatio),
    AudioCompletionRatio: parseNumberMap(settings.AudioCompletionRatio),
    'billing_setting.billing_mode': parseStringMap(
      settings['billing_setting.billing_mode']
    ),
    'billing_setting.billing_expr': parseStringMap(
      settings['billing_setting.billing_expr']
    ),
  }
}

export function modelPricingMapEquals(
  left: Record<string, number | string>,
  right: Record<string, number | string>
): boolean {
  const leftKeys = Object.keys(left)
  if (leftKeys.length !== Object.keys(right).length) return false
  return leftKeys.every(
    (key) =>
      Object.prototype.hasOwnProperty.call(right, key) &&
      left[key] === right[key]
  )
}

export function readPricingConfig(
  settings: ModelPricingSettings | null,
  modelName: string
): PricingConfig {
  const emptyConfig: PricingConfig = {
    mode: 'per-token',
    fields: emptyPricingFields(),
    promptPrice: '',
    completionPrice: '',
    advancedOpen: false,
    managedExternally: false,
  }
  if (!settings || !modelName) return emptyConfig

  const maps = parseModelPricingMaps(settings)
  const price = maps.ModelPrice[modelName]
  const ratio = maps.ModelRatio[modelName]
  const cacheRatio = maps.CacheRatio[modelName]
  const completionRatio = maps.CompletionRatio[modelName]
  const imageRatio = maps.ImageRatio[modelName]
  const audioRatio = maps.AudioRatio[modelName]
  const audioCompletionRatio = maps.AudioCompletionRatio[modelName]
  const managedExternally =
    maps['billing_setting.billing_mode'][modelName] === 'tiered_expr'

  let promptPrice = ''
  let completionPrice = ''
  if (ratio !== undefined && ratio !== null) {
    const tokenPrice = ratio * 2
    promptPrice = tokenPrice.toString()
    if (completionRatio !== undefined && completionRatio !== null) {
      completionPrice = (tokenPrice * completionRatio).toString()
    }
  }

  const fields: PricingFields = {
    price: price?.toString() || '',
    ratio: ratio?.toString() || '',
    cacheRatio: cacheRatio?.toString() || '',
    completionRatio: completionRatio?.toString() || '',
    imageRatio: imageRatio?.toString() || '',
    audioRatio: audioRatio?.toString() || '',
    audioCompletionRatio: audioCompletionRatio?.toString() || '',
  }

  if (managedExternally) {
    return {
      mode: price !== undefined && price !== null ? 'per-request' : 'per-token',
      fields,
      promptPrice,
      completionPrice,
      advancedOpen: [
        cacheRatio,
        imageRatio,
        audioRatio,
        audioCompletionRatio,
      ].some((value) => value !== undefined && value !== null),
      managedExternally: true,
    }
  }

  if (price !== undefined && price !== null) {
    return {
      ...emptyConfig,
      mode: 'per-request',
      fields: { ...emptyPricingFields(), price: price.toString() },
    }
  }

  return {
    mode: 'per-token',
    fields,
    promptPrice,
    completionPrice,
    advancedOpen: [
      cacheRatio,
      imageRatio,
      audioRatio,
      audioCompletionRatio,
    ].some((value) => value !== undefined && value !== null),
    managedExternally: false,
  }
}

function cloneModelPricingMaps(maps: ModelPricingMaps): ModelPricingMaps {
  return {
    ModelPrice: { ...maps.ModelPrice },
    ModelRatio: { ...maps.ModelRatio },
    CacheRatio: { ...maps.CacheRatio },
    CreateCacheRatio: { ...maps.CreateCacheRatio },
    CompletionRatio: { ...maps.CompletionRatio },
    ImageRatio: { ...maps.ImageRatio },
    AudioRatio: { ...maps.AudioRatio },
    AudioCompletionRatio: { ...maps.AudioCompletionRatio },
    'billing_setting.billing_mode': {
      ...maps['billing_setting.billing_mode'],
    },
    'billing_setting.billing_expr': {
      ...maps['billing_setting.billing_expr'],
    },
  }
}

function movePricingEntry(
  map: Record<string, number | string>,
  oldModelName: string,
  finalModelName: string
) {
  if (!Object.prototype.hasOwnProperty.call(map, oldModelName)) return
  const value = map[oldModelName]
  delete map[oldModelName]
  map[finalModelName] = value
}

function hasPricingConfig(
  pricingMode: PricingMode,
  fields: PricingFields
): boolean {
  if (pricingMode === 'per-request') {
    return Boolean(fields.price && fields.price !== '')
  }
  return Boolean(
    fields.ratio ||
    fields.cacheRatio ||
    fields.completionRatio ||
    fields.imageRatio ||
    fields.audioRatio ||
    fields.audioCompletionRatio
  )
}

function setNumberIfPresent(
  map: Record<string, number>,
  modelName: string,
  value: string | undefined
) {
  if (!value || value === '') return
  map[modelName] = parseFloat(value)
}

export function mutateModelPricingMaps(
  currentMaps: ModelPricingMaps,
  mutation: ModelPricingMutation
): ModelPricingMaps {
  const maps = cloneModelPricingMaps(currentMaps)
  const {
    isEditing,
    oldModelName,
    finalModelName,
    loadedPricingName,
    pricingMode,
    fields,
  } = mutation
  const isRename =
    isEditing && oldModelName !== '' && oldModelName !== finalModelName
  const loadedTiered =
    loadedPricingName !== '' &&
    maps['billing_setting.billing_mode'][loadedPricingName] === 'tiered_expr'

  if (isRename && loadedTiered) {
    for (const key of MODEL_PRICING_OPTION_KEYS) {
      movePricingEntry(
        maps[key] as Record<string, number | string>,
        oldModelName,
        finalModelName
      )
    }
    return maps
  }

  if (isRename) {
    for (const key of EDITABLE_PRICING_OPTION_KEYS) {
      delete maps[key][oldModelName]
    }
    for (const key of PRESERVED_PRICING_OPTION_KEYS) {
      movePricingEntry(
        maps[key] as Record<string, number | string>,
        oldModelName,
        finalModelName
      )
    }
  }

  if (loadedTiered) return maps

  const shouldWritePricing = hasPricingConfig(pricingMode, fields)
  if (shouldWritePricing || finalModelName === loadedPricingName) {
    for (const key of EDITABLE_PRICING_OPTION_KEYS) {
      delete maps[key][finalModelName]
    }
  }

  if (!shouldWritePricing) return maps

  if (pricingMode === 'per-request') {
    setNumberIfPresent(maps.ModelPrice, finalModelName, fields.price)
    return maps
  }

  setNumberIfPresent(maps.ModelRatio, finalModelName, fields.ratio)
  setNumberIfPresent(maps.CacheRatio, finalModelName, fields.cacheRatio)
  setNumberIfPresent(
    maps.CompletionRatio,
    finalModelName,
    fields.completionRatio
  )
  setNumberIfPresent(maps.ImageRatio, finalModelName, fields.imageRatio)
  setNumberIfPresent(maps.AudioRatio, finalModelName, fields.audioRatio)
  setNumberIfPresent(
    maps.AudioCompletionRatio,
    finalModelName,
    fields.audioCompletionRatio
  )
  return maps
}

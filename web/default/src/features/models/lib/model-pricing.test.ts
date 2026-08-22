import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  MODEL_PRICING_OPTION_KEYS,
  modelPricingMapEquals,
  mutateModelPricingMaps,
  parseModelPricingMaps,
  readPricingConfig,
  type ModelPricingMaps,
  type ModelPricingSettings,
} from './model-pricing'

const settings = (
  overrides: Partial<ModelPricingSettings> = {}
): ModelPricingSettings => ({
  ModelPrice: '{}',
  ModelRatio: '{}',
  CacheRatio: '{}',
  CreateCacheRatio: '{}',
  CompletionRatio: '{}',
  ImageRatio: '{}',
  AudioRatio: '{}',
  AudioCompletionRatio: '{}',
  'billing_setting.billing_mode': '{}',
  'billing_setting.billing_expr': '{}',
  ...overrides,
})

const entry = (
  maps: ModelPricingMaps,
  key: (typeof MODEL_PRICING_OPTION_KEYS)[number],
  modelName: string
) => (maps[key] as Record<string, number | string>)[modelName]

describe('model pricing preservation', () => {
  test('treats map key order as semantically unchanged', () => {
    assert.equal(
      modelPricingMapEquals(
        { 'first-model': 1, 'second-model': 2 },
        { 'second-model': 2, 'first-model': 1 }
      ),
      true
    )
  })

  test('loads existing fixed pricing when creating a prefilled model', () => {
    const config = readPricingConfig(
      settings({
        ModelPrice: '{"priced-model":0.02}',
        ModelRatio: '{"priced-model":3}',
      }),
      'priced-model'
    )

    assert.equal(config.mode, 'per-request')
    assert.equal(config.fields.price, '0.02')
    assert.equal(config.fields.ratio, '')
    assert.equal(config.managedExternally, false)
  })

  test('keeps zero-valued advanced ratios visible', () => {
    const config = readPricingConfig(
      settings({
        ModelRatio: '{"free-cache-model":1}',
        CacheRatio: '{"free-cache-model":0}',
      }),
      'free-cache-model'
    )

    assert.equal(config.mode, 'per-token')
    assert.equal(config.fields.cacheRatio, '0')
    assert.equal(config.advancedOpen, true)
  })

  test('keeps pre-existing pricing unchanged when creating a prefilled model', () => {
    const currentSettings = settings({
      ModelRatio: '{"priced-model":1,"other-model":2}',
      CacheRatio: '{"priced-model":0.1}',
      CreateCacheRatio: '{"priced-model":1.25}',
      CompletionRatio: '{"priced-model":4}',
      'billing_setting.billing_mode': '{"other-model":"tiered_expr"}',
      'billing_setting.billing_expr': '{"other-model":"input * 2"}',
    })
    const current = parseModelPricingMaps(currentSettings)
    const config = readPricingConfig(currentSettings, 'priced-model')

    const next = mutateModelPricingMaps(current, {
      isEditing: false,
      oldModelName: '',
      finalModelName: 'priced-model',
      loadedPricingName: 'priced-model',
      pricingMode: config.mode,
      fields: config.fields,
    })

    assert.deepEqual(next, current)
  })

  test('does not rewrite any pricing map for an unpriced create form', () => {
    const current = parseModelPricingMaps(
      settings({
        ModelPrice: '{"existing-model":0.02}',
        ModelRatio: '{"existing-model":1}',
        CacheRatio: '{"existing-model":0.1}',
        CreateCacheRatio: '{"existing-model":1.25}',
        CompletionRatio: '{"existing-model":4}',
        ImageRatio: '{"existing-model":0.5}',
        AudioRatio: '{"existing-model":0.6}',
        AudioCompletionRatio: '{"existing-model":2}',
        'billing_setting.billing_mode': '{"existing-model":"tiered_expr"}',
        'billing_setting.billing_expr': '{"existing-model":"input * 2"}',
      })
    )

    const next = mutateModelPricingMaps(current, {
      isEditing: false,
      oldModelName: '',
      finalModelName: 'existing-model',
      loadedPricingName: '',
      pricingMode: 'per-token',
      fields: {},
    })

    assert.deepEqual(next, current)
  })

  test('moves standard ratios and BeeAPI-only fields on rename', () => {
    const current = parseModelPricingMaps(
      settings({
        ModelPrice: '{"new-model":9}',
        ModelRatio: '{"old-model":1,"new-model":9}',
        CacheRatio: '{"old-model":0.1,"new-model":9}',
        CreateCacheRatio: '{"old-model":1.25,"new-model":9}',
        CompletionRatio: '{"old-model":4,"new-model":9}',
        ImageRatio: '{"old-model":0.5,"new-model":9}',
        AudioRatio: '{"old-model":0.6,"new-model":9}',
        AudioCompletionRatio: '{"old-model":2,"new-model":9}',
        'billing_setting.billing_mode':
          '{"old-model":"legacy","new-model":"tiered_expr"}',
        'billing_setting.billing_expr':
          '{"old-model":"old-expr","new-model":"new-expr"}',
      })
    )

    const next = mutateModelPricingMaps(current, {
      isEditing: true,
      oldModelName: 'old-model',
      finalModelName: 'new-model',
      loadedPricingName: 'old-model',
      pricingMode: 'per-token',
      fields: {
        ratio: '1',
        cacheRatio: '0.1',
        completionRatio: '4',
        imageRatio: '0.5',
        audioRatio: '0.6',
        audioCompletionRatio: '2',
      },
    })

    for (const key of MODEL_PRICING_OPTION_KEYS) {
      assert.equal(entry(next, key, 'old-model'), undefined)
    }
    assert.equal(entry(next, 'ModelPrice', 'new-model'), undefined)
    assert.equal(entry(next, 'ModelRatio', 'new-model'), 1)
    assert.equal(entry(next, 'CacheRatio', 'new-model'), 0.1)
    assert.equal(entry(next, 'CreateCacheRatio', 'new-model'), 1.25)
    assert.equal(entry(next, 'CompletionRatio', 'new-model'), 4)
    assert.equal(entry(next, 'ImageRatio', 'new-model'), 0.5)
    assert.equal(entry(next, 'AudioRatio', 'new-model'), 0.6)
    assert.equal(entry(next, 'AudioCompletionRatio', 'new-model'), 2)
    assert.equal(
      entry(next, 'billing_setting.billing_mode', 'new-model'),
      'legacy'
    )
    assert.equal(
      entry(next, 'billing_setting.billing_expr', 'new-model'),
      'old-expr'
    )
  })

  test('moves tiered pricing and every fallback field without rebuilding it', () => {
    const current = parseModelPricingMaps(
      settings({
        ModelPrice: '{"old-tiered":0.02,"new-tiered":9}',
        ModelRatio: '{"old-tiered":1,"new-tiered":9}',
        CacheRatio: '{"old-tiered":0.1,"new-tiered":9}',
        CreateCacheRatio: '{"old-tiered":1.25,"new-tiered":9}',
        CompletionRatio: '{"old-tiered":4,"new-tiered":9}',
        ImageRatio: '{"old-tiered":0.5,"new-tiered":9}',
        AudioRatio: '{"old-tiered":0.6,"new-tiered":9}',
        AudioCompletionRatio: '{"old-tiered":2,"new-tiered":9}',
        'billing_setting.billing_mode':
          '{"old-tiered":"tiered_expr","new-tiered":"tiered_expr"}',
        'billing_setting.billing_expr':
          '{"old-tiered":"old-expr","new-tiered":"new-expr"}',
      })
    )
    const oldValues = Object.fromEntries(
      MODEL_PRICING_OPTION_KEYS.map((key) => [
        key,
        entry(current, key, 'old-tiered'),
      ])
    )

    const next = mutateModelPricingMaps(current, {
      isEditing: true,
      oldModelName: 'old-tiered',
      finalModelName: 'new-tiered',
      loadedPricingName: 'old-tiered',
      pricingMode: 'per-request',
      fields: { price: '999' },
    })

    for (const key of MODEL_PRICING_OPTION_KEYS) {
      assert.equal(entry(next, key, 'old-tiered'), undefined)
      assert.equal(entry(next, key, 'new-tiered'), oldValues[key])
    }
  })

  test('leaves tiered pricing unchanged on ordinary model edit', () => {
    const current = parseModelPricingMaps(
      settings({
        ModelPrice: '{"tiered-model":0.02}',
        ModelRatio: '{"tiered-model":1}',
        CacheRatio: '{"tiered-model":0.1}',
        CreateCacheRatio: '{"tiered-model":1.25}',
        CompletionRatio: '{"tiered-model":4}',
        'billing_setting.billing_mode': '{"tiered-model":"tiered_expr"}',
        'billing_setting.billing_expr': '{"tiered-model":"input * 2"}',
      })
    )

    const next = mutateModelPricingMaps(current, {
      isEditing: true,
      oldModelName: 'tiered-model',
      finalModelName: 'tiered-model',
      loadedPricingName: 'tiered-model',
      pricingMode: 'per-request',
      fields: { price: '999' },
    })

    assert.deepEqual(next, current)
  })
})

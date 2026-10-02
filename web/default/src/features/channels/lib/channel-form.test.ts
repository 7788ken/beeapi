// @ts-expect-error -- bun:test 的类型未随仓库安装，测试由 bun 运行器执行
import { describe, expect, test } from 'bun:test'
import type { Channel } from '../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  createChannelFormDefaults,
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
} from './channel-form.ts'

// 渠道页不编辑内容备份开关。保存时基底展开必须原样留下 content_backup_enabled，
// 不能因为表单里没有这个字段就把它写成 false。
describe('channel save leaves content backup ownership to the console', () => {
  test('keeps an enabled backup flag and unrelated setting fields', () => {
    const existingSetting = JSON.stringify({
      proxy: 'socks5://user:pass@10.0.0.1:1080',
      system_prompt: 'You are a helpful assistant.',
      content_backup_enabled: true,
      base_url_strategy: 'fastest',
    })

    const formData = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      setting: existingSetting,
      proxy: 'socks5://user:pass@10.0.0.1:1080',
      system_prompt: 'You are a helpful assistant.',
      block_apology_enabled: true,
      block_low_token_enabled: true,
    }

    const payload = transformFormDataToUpdatePayload(formData, 42)
    const saved = JSON.parse(payload.setting as string)

    expect(saved.proxy).toBe('socks5://user:pass@10.0.0.1:1080')
    expect(saved.system_prompt).toBe('You are a helpful assistant.')
    expect(saved.base_url_strategy).toBe('fastest')
    expect(saved.content_backup_enabled).toBe(true)
    expect(saved.block_apology_enabled).toBe(true)
    expect(saved.block_low_token_enabled).toBe(true)
  })

  test('does not add a backup flag when the stored setting has none', () => {
    const existingSetting = JSON.stringify({
      proxy: 'socks5://user:pass@10.0.0.1:1080',
    })

    const formData = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      setting: existingSetting,
      proxy: 'socks5://user:pass@10.0.0.1:1080',
    }

    const payload = transformFormDataToUpdatePayload(formData, 42)
    const saved = JSON.parse(payload.setting as string)

    expect(saved.proxy).toBe('socks5://user:pass@10.0.0.1:1080')
    expect(saved.content_backup_enabled).toBeUndefined()
  })
})

describe('create channel quality block defaults', () => {
  test('stays off unless this site turns the new-channel options on', () => {
    const off = createChannelFormDefaults()
    expect(off.block_apology_enabled).toBe(false)
    expect(off.block_low_token_enabled).toBe(false)

    const on = createChannelFormDefaults({
      blockApology: true,
      blockLowToken: true,
    })
    expect(on.block_apology_enabled).toBe(true)
    expect(on.block_low_token_enabled).toBe(true)
    expect(on.skip_auto_test).toBe(false)
  })
})

// 「不参与定时测试和可用性测试」存在 setting.skip_auto_test，后端三条自动测试循环按它跳过。
describe('skip_auto_test toggle round-trips through setting JSON', () => {
  test('editing a channel with skip_auto_test on shows it on and keeps other fields', () => {
    const channel = {
      name: 'exempt',
      type: 1,
      status: 1,
      channel_info: {},
      setting: JSON.stringify({
        skip_auto_test: true,
        base_url_strategy: 'fastest',
      }),
    } as unknown as Channel

    const formData = transformChannelToFormDefaults(channel)
    expect(formData.skip_auto_test).toBe(true)

    const saved = JSON.parse(
      transformFormDataToUpdatePayload(formData, 7).setting as string
    )
    expect(saved.skip_auto_test).toBe(true)
    expect(saved.base_url_strategy).toBe('fastest')
  })

  test('turning skip_auto_test off writes false', () => {
    const formData = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      setting: JSON.stringify({ skip_auto_test: true }),
      skip_auto_test: false,
    }

    const saved = JSON.parse(
      transformFormDataToUpdatePayload(formData, 7).setting as string
    )
    expect(saved.skip_auto_test).toBe(false)
  })
})

// @ts-expect-error -- bun:test 的类型未随仓库安装，测试由 bun 运行器执行
import { describe, expect, test } from 'bun:test'
import type { Channel } from '../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
} from './channel-form.ts'

// T12: 渠道 settings 更新走最小 payload，不得覆盖 proxy/system_prompt 等既有字段。
// buildSettingJSON() 以 formData.setting（编辑回显时的原始 JSON）为基底展开，
// 这里验证加入 content_backup_enabled 开关后，该基底展开逻辑仍然成立。
describe('content backup toggle preserves unrelated channel setting fields', () => {
  test('toggling content_backup_enabled keeps proxy/system_prompt and unknown fields intact', () => {
    const existingSetting = JSON.stringify({
      proxy: 'socks5://user:pass@10.0.0.1:1080',
      system_prompt: 'You are a helpful assistant.',
      // 模拟后端已新增、前端尚未适配的字段：必须原样透传，不能被整体覆盖清空
      base_url_strategy: 'fastest',
    })

    const formData = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      setting: existingSetting,
      proxy: 'socks5://user:pass@10.0.0.1:1080',
      system_prompt: 'You are a helpful assistant.',
      content_backup_enabled: true,
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

  test('leaving content_backup_enabled unset defaults to false without touching other fields', () => {
    const existingSetting = JSON.stringify({
      proxy: 'socks5://user:pass@10.0.0.1:1080',
      content_backup_enabled: true,
    })

    const formData = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      setting: existingSetting,
      proxy: 'socks5://user:pass@10.0.0.1:1080',
      content_backup_enabled: false,
    }

    const payload = transformFormDataToUpdatePayload(formData, 42)
    const saved = JSON.parse(payload.setting as string)

    expect(saved.proxy).toBe('socks5://user:pass@10.0.0.1:1080')
    expect(saved.content_backup_enabled).toBe(false)
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

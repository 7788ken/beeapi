// @ts-expect-error -- bun:test 的类型未随仓库安装，测试由 bun 运行器执行
import { expect, test } from 'bun:test'
import {
  DEFAULT_FORM_VALUES,
  isValidRemoteBaseDir,
  mergeFormValues,
  normalizeConfigForForm,
  sanitizeInactiveProtocolFields,
} from './config-form'
import type { ContentBackupConfig } from './types'

const base = {
  enabled: false,
  upload_paused: false,
  target_id: 't',
  ftps_host: 'ftps.example',
  ftps_port: 21,
  cert_sha256: '',
  remote_protocol: '',
  sftp_host: 'sftp.example',
  sftp_port: 22,
  sftp_host_key_sha256: '',
} as unknown as Omit<ContentBackupConfig, 'version'>

test('a stored config from before remote_protocol existed renders as FTPS', () => {
  expect(normalizeConfigForForm(base).remote_protocol).toBe('ftps')
})

test('sftp is kept and the other protocol fields are carried through untouched', () => {
  const out = normalizeConfigForForm({ ...base, remote_protocol: 'sftp' })
  expect(out.remote_protocol).toBe('sftp')
  expect(out.ftps_host).toBe('ftps.example')
  expect(out.sftp_host).toBe('sftp.example')
})

test('remote base dir accepts empty, "/" and clean absolute paths only (mirrors ValidateRemoteBaseDir)', () => {
  for (const ok of ['', '/', '/raid/backup/b_459494', '/a']) {
    expect(isValidRemoteBaseDir(ok)).toBe(true)
  }
  for (const bad of [
    'backup',
    '/raid/backup/',
    '/raid//backup',
    '/raid/../etc',
    '/./raid',
    '/raid/.',
    '/raid/..',
    ' /raid',
  ]) {
    expect(isValidRemoteBaseDir(bad)).toBe(false)
  }
})

test('an unknown protocol value falls back to ftps instead of breaking the form', () => {
  const out = normalizeConfigForForm({
    ...base,
    remote_protocol: 'scp',
  } as unknown as Omit<ContentBackupConfig, 'version'>)
  expect(out.remote_protocol).toBe('ftps')
})

// ai 实发：FTPS 证书字段里残留了一个 base64（切协议前粘错位置），切到 SFTP 后保存按钮点了没反应。
test('an invalid value left in the hidden protocol fields is blanked instead of blocking the save', () => {
  const out = sanitizeInactiveProtocolFields({
    remote_protocol: 'sftp',
    ftps_host: '5.45.76.50',
    cert_sha256: 'sTs5r5HmefHuTg9YBR4ZHWcVSiEAekEdlgdanzGJ37Y',
    sftp_host: '5.45.76.50',
    sftp_host_key_sha256: HEX_PIN,
    sftp_base_dir: '',
  })
  expect(out.cert_sha256).toBe('')
  expect(out.ftps_host).toBe('5.45.76.50') // 合法的保留
  expect(out.sftp_host_key_sha256).toBe(HEX_PIN) // 当前协议的值不动
})

test('switching back to ftps blanks only the invalid sftp leftovers', () => {
  const out = sanitizeInactiveProtocolFields({
    remote_protocol: 'ftps',
    ftps_host: 'ftps.example',
    cert_sha256: HEX_PIN,
    sftp_host: 'sftp://bad host',
    sftp_host_key_sha256: 'nope',
    sftp_base_dir: 'relative',
  })
  expect(out.sftp_host).toBe('')
  expect(out.sftp_host_key_sha256).toBe('')
  expect(out.sftp_base_dir).toBe('')
  expect(out.ftps_host).toBe('ftps.example')
  expect(out.cert_sha256).toBe(HEX_PIN)
})

const HEX_PIN =
  'b13b39af91e679f1ee4e0f58051e191d67154a21007a411d96075a9f3189dfb6'

// ai 实发：切到 SFTP 后 FTPS 的 Controller 被卸掉，RHF+zodResolver 交给 Zod 的对象没有
// ftps_host（undefined），Zod 4 报 "expected string, received undefined"。
test('mergeFormValues fills holes left by unmounted protocol and advanced fields', () => {
  const out = mergeFormValues({
    remote_protocol: 'sftp',
    sftp_host: '5.45.76.50',
    sftp_port: 22,
    sftp_host_key_sha256: HEX_PIN,
    site_label: 'ai',
    target_id: 'backup-primary',
    remote_username: 'u',
  })
  expect(out.ftps_host).toBe('')
  expect(out.ftps_port).toBe(21)
  expect(out.cert_sha256).toBe('')
  expect(out.max_body_bytes).toBe(DEFAULT_FORM_VALUES.max_body_bytes)
  expect(out.sftp_host).toBe('5.45.76.50')
  expect(out.remote_protocol).toBe('sftp')
})

test('mergeFormValues treats explicit undefined as missing instead of clobbering defaults', () => {
  const out = mergeFormValues({
    remote_protocol: 'sftp',
    ftps_host: undefined,
    ftps_port: undefined,
    max_body_bytes: undefined,
    sftp_host: '5.45.76.50',
  })
  expect(out.ftps_host).toBe('')
  expect(out.ftps_port).toBe(21)
  expect(out.max_body_bytes).toBe(DEFAULT_FORM_VALUES.max_body_bytes)
  expect(out.sftp_host).toBe('5.45.76.50')
})

test('normalizeConfigForForm fills keys a stored blob may omit', () => {
  const out = normalizeConfigForForm(base)
  expect(out.site_label).toBe('')
  expect(out.sftp_base_dir).toBe('')
  expect(out.sftp_port).toBe(22)
  expect(out.remote_password).toBe('')
  expect(out.notify_oldest_pending).toBe(true)
  expect(out.notify_node_offline).toBe(true)
})

test('sanitize blanks undefined leftovers instead of treating them as the string "undefined"', () => {
  const out = sanitizeInactiveProtocolFields({
    remote_protocol: 'sftp',
    ftps_host: undefined as unknown as string,
    cert_sha256: undefined as unknown as string,
    sftp_host: '5.45.76.50',
    sftp_host_key_sha256: HEX_PIN,
    sftp_base_dir: '',
  })
  expect(out.ftps_host).toBe('')
  expect(out.cert_sha256).toBe('')
})

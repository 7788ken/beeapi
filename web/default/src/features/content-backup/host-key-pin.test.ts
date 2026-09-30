// @ts-expect-error -- bun:test 的类型未随仓库安装，测试由 bun 运行器执行
import { expect, test } from 'bun:test'
import { normalizeHostKeyPin } from './host-key-pin'

// 32 字节 00..1f 的两种编码：hex 与 OpenSSH 的 SHA256:<base64 无填充>
const HEX =
  '000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f'
const OPENSSH = 'SHA256:AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8'

test('OpenSSH SHA256:<base64> form is translated to the hex the backend stores', () => {
  expect(normalizeHostKeyPin(OPENSSH)).toBe(HEX)
  // 只复制了冒号后面那段（43 字符 base64）同样接受
  expect(normalizeHostKeyPin(OPENSSH.slice(7))).toBe(HEX)
  expect(normalizeHostKeyPin(OPENSSH.slice(7) + '=')).toBe(HEX)
  expect(normalizeHostKeyPin(`  ${OPENSSH}  `)).toBe(HEX)
  expect(normalizeHostKeyPin(OPENSSH + '=')).toBe(HEX)
  expect(normalizeHostKeyPin('sha256:' + OPENSSH.slice(7))).toBe(HEX)
})

test('hex input is lower-cased and otherwise left alone', () => {
  expect(normalizeHostKeyPin(HEX)).toBe(HEX)
  expect(normalizeHostKeyPin(HEX.toUpperCase())).toBe(HEX)
  expect(normalizeHostKeyPin('')).toBe('')
})

test('unrecognisable input is returned unchanged so validation can reject it', () => {
  // base64 解出来不是 32 字节：不是一个 SHA-256
  expect(normalizeHostKeyPin('SHA256:AAEC')).toBe('SHA256:AAEC')
  // MD5 冒号形式不是我们要的
  const md5 = 'aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99'
  expect(normalizeHostKeyPin(md5)).toBe(md5)
  // 63 位 hex 不是 pin
  expect(normalizeHostKeyPin(HEX.slice(1))).toBe(HEX.slice(1))
})

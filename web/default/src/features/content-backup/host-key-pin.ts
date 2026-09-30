/**
 * SFTP 主机公钥 pin 的输入规范化。
 *
 * 后端只接受 64 位小写 hex（pkg/contentbackup/config.go ValidateConfig），而运营手里
 * 最容易拿到的是 OpenSSH 工具链的输出：`ssh-keyscan host | ssh-keygen -lf -` 打印
 * `SHA256:<base64 无填充>`。两者是同一段 32 字节的两种编码，这里在表单层把 OpenSSH
 * 形式翻成 hex，再交给后端校验；不认识的输入原样返回，让 zod 的 hex 规则报错。
 */

const HEX64 = /^[a-f0-9]{64}$/
const OPENSSH_PREFIX = /^SHA256:/i

export function normalizeHostKeyPin(raw: string): string {
  const value = raw.trim()
  if (value === '') return ''
  if (HEX64.test(value.toLowerCase()) && value.length === 64) {
    return value.toLowerCase()
  }
  // 允许省略 "SHA256:" 前缀：ssh-keygen 输出复制时很容易只选中冒号后面那段。
  // 32 字节的 base64 恰好 43 字符（无填充）/44 字符（有填充），与 64 位 hex 不可能混淆。
  const body = value.replace(OPENSSH_PREFIX, '').trim()
  if (!OPENSSH_PREFIX.test(value) && body.length !== 43 && body.length !== 44) {
    return value
  }
  const bytes = decodeBase64(body)
  if (bytes === null || bytes.length !== 32) return value
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

/** 无填充/有填充都接受；非法字符或长度返回 null，不抛错 */
function decodeBase64(input: string): Uint8Array | null {
  const cleaned = input.replace(/=+$/, '')
  if (cleaned === '' || !/^[A-Za-z0-9+/]+$/.test(cleaned)) return null
  const padded = cleaned + '='.repeat((4 - (cleaned.length % 4)) % 4)
  try {
    const binary = atob(padded)
    const out = new Uint8Array(binary.length)
    for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i)
    return out
  } catch {
    return null
  }
}

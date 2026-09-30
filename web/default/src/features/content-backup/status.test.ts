// @ts-expect-error -- bun:test 的类型未随仓库安装，测试由 bun 运行器执行
import { describe, expect, test } from 'bun:test'
import {
  beijingDayRange,
  clampRetryBatch,
  cleanupLabel,
  emptyStateLabel,
  emptyStateDescription,
  formatBytes,
  hasConfigDrift,
  humanizeSeconds,
  integrityLabel,
  isWithinMaxRange,
  nodeHeartbeat,
  normalizeJobTimes,
  normalizeTimestamp,
  renderPreviewSide,
  resolveContentBackupPerms,
  retryBlockedLabel,
  retryOutcomeLabel,
  sessionFingerprint,
  sortQueueJobs,
  statusLabel,
  statusTone,
  terminalReasonLabel,
} from './status.ts'
import type { ContentBackupJob, ContentBackupNodeSnapshot } from './types.ts'

// 文档 6.4：上传成功与本地空间释放是两个独立结果，UI 不得把 uploaded 说成已释放
test('uploaded is not the same as local space reclaimed', () => {
  expect(cleanupLabel('uploaded', 'pending')).toBe('Local cleanup pending')
  expect(cleanupLabel('uploaded', 'done')).toBe('Local space reclaimed')
})

test('cleanup label for not yet uploaded jobs', () => {
  expect(cleanupLabel('pending', 'not_applicable')).toBe('Not uploaded')
  expect(cleanupLabel('processing', 'not_applicable')).toBe('Not uploaded')
  expect(cleanupLabel('failed', 'not_applicable')).toBe('Not uploaded')
  // 未上传的任务即使后端误报 done，也不能声称空间已释放
  expect(cleanupLabel('failed', 'done')).toBe('Not uploaded')
})

test('cleanup label degrades on inconsistent or unknown enums', () => {
  expect(cleanupLabel('uploaded', 'not_applicable')).toBe(
    'Local cleanup state unknown'
  )
  expect(cleanupLabel('uploaded', 'weird_state')).toBe(
    'Local cleanup state unknown'
  )
  expect(cleanupLabel('uploaded', undefined)).toBe('Local cleanup state unknown')
  expect(cleanupLabel('weird_status', 'done')).toBe('Upload state unknown')
  expect(cleanupLabel(undefined, undefined)).toBe('Upload state unknown')
})

test('status label maps the frozen vocabulary and degrades to the raw value', () => {
  expect(statusLabel('pending')).toBe('Pending upload')
  expect(statusLabel('processing')).toBe('Uploading')
  expect(statusLabel('failed')).toBe('Upload failed')
  expect(statusLabel('uploaded')).toBe('Uploaded')
  expect(statusLabel('mystery')).toBe('mystery')
  expect(statusLabel('')).toBe('Unknown')
  expect(statusLabel(undefined)).toBe('Unknown')
})

test('status tone never paints an unknown status as success', () => {
  expect(statusTone('uploaded')).toBe('default')
  expect(statusTone('failed')).toBe('destructive')
  expect(statusTone('processing')).toBe('secondary')
  expect(statusTone('pending')).toBe('outline')
  expect(statusTone('mystery')).toBe('outline')
})

test('terminal reason renders known values and falls back verbatim', () => {
  expect(terminalReasonLabel('complete')).toBe('Terminal complete')
  expect(terminalReasonLabel('client_disconnect')).toBe('Client disconnected')
  expect(terminalReasonLabel('error_response')).toBe('Upstream error response')
  expect(terminalReasonLabel('validation_error')).toBe('Validation error')
  expect(terminalReasonLabel('upstream_error')).toBe('Upstream error')
  expect(terminalReasonLabel('retry_exhausted')).toBe('Retries exhausted')
  expect(terminalReasonLabel('unknown')).toBe('Unknown')
  expect(terminalReasonLabel('brand_new_reason')).toBe('brand_new_reason')
  expect(terminalReasonLabel('')).toBe('Unknown')
})

test('retry outcome labels', () => {
  expect(retryOutcomeLabel('queued')).toBe('Queued')
  expect(retryOutcomeLabel('skipped')).toBe('Skipped')
  expect(retryOutcomeLabel('failed')).toBe('Failed')
  expect(retryOutcomeLabel('whatever')).toBe('whatever')
  expect(retryOutcomeLabel(undefined)).toBe('Unknown')
})

test('unrecoverable error codes ask for offline recovery', () => {
  expect(retryBlockedLabel('local_missing')).toBe(
    'Needs offline recovery before it can be queued'
  )
  expect(retryBlockedLabel('hash_error')).toBe(
    'Needs offline recovery before it can be queued'
  )
  expect(retryBlockedLabel('incomplete_spool')).toBe(
    'Needs offline recovery before it can be queued'
  )
  expect(retryBlockedLabel('remote_timeout')).toBeUndefined()
  expect(retryBlockedLabel(undefined)).toBeUndefined()
})

describe('timestamp normalization', () => {
  test('accepts RFC3339, unix seconds as number or numeric string', () => {
    expect(normalizeTimestamp('2026-09-15T04:34:56Z')).toBe(
      '2026-09-15T04:34:56Z'
    )
    expect(normalizeTimestamp(1757910896)).toBe('2025-09-15T04:34:56.000Z')
    expect(normalizeTimestamp('1757910896')).toBe('2025-09-15T04:34:56.000Z')
  })

  test('treats zero, empty and garbage as absent instead of 1970', () => {
    expect(normalizeTimestamp(0)).toBeUndefined()
    expect(normalizeTimestamp('')).toBeUndefined()
    expect(normalizeTimestamp(null)).toBeUndefined()
    expect(normalizeTimestamp(undefined)).toBeUndefined()
    expect(normalizeTimestamp('not-a-time')).toBeUndefined()
    expect(normalizeTimestamp(Number.NaN)).toBeUndefined()
    expect(normalizeTimestamp({})).toBeUndefined()
  })

  test('normalizes every job timestamp field in one place', () => {
    const raw = {
      job_id: 'job-1',
      created_at: 1757910896,
      uploaded_at: 0,
      cleanup_available_at: '2026-09-15T04:34:56Z',
    } as unknown as ContentBackupJob
    const job = normalizeJobTimes(raw)
    expect(job.created_at).toBe('2025-09-15T04:34:56.000Z')
    expect(job.uploaded_at).toBeUndefined()
    expect(job.cleanup_available_at).toBe('2026-09-15T04:34:56Z')
    expect(job.job_id).toBe('job-1')
  })
})

test('queue order is failed first, then oldest within a status', () => {
  const items: ContentBackupJob[] = [
    { job_id: 'p-old', status: 'pending', created_at: '2026-09-15T01:00:00Z' },
    { job_id: 'f-new', status: 'failed', created_at: '2026-09-15T05:00:00Z' },
    { job_id: 'pr-1', status: 'processing', created_at: '2026-09-15T02:00:00Z' },
    { job_id: 'f-old', status: 'failed', created_at: '2026-09-15T03:00:00Z' },
    { job_id: 'x-1', status: 'mystery', created_at: '2026-09-15T00:30:00Z' },
  ]
  const sorted = sortQueueJobs(items).map((item) => item.job_id)
  expect(sorted).toEqual(['f-old', 'f-new', 'pr-1', 'x-1', 'p-old'])
  // 输入不被就地修改
  expect(items[0].job_id).toBe('p-old')
})

test('retry batch is capped at 100 explicit job ids', () => {
  const ids = Array.from({ length: 150 }, (_, i) => `job-${i}`)
  const clamped = clampRetryBatch(ids)
  expect(clamped.accepted).toHaveLength(100)
  expect(clamped.rejected).toHaveLength(50)
  expect(clamped.accepted[0]).toBe('job-0')
  expect(clamped.rejected[0]).toBe('job-100')
  const dupes = clampRetryBatch(['a', 'a', '', 'b'])
  expect(dupes.accepted).toEqual(['a', 'b'])
})

test('normal list range is capped at 31 days', () => {
  expect(
    isWithinMaxRange('2026-09-01T00:00:00Z', '2026-09-30T00:00:00Z')
  ).toBe(true)
  expect(
    isWithinMaxRange('2026-09-01T00:00:00Z', '2026-10-03T00:00:00Z')
  ).toBe(false)
  expect(isWithinMaxRange(undefined, undefined)).toBe(true)
  expect(isWithinMaxRange('2026-09-30T00:00:00Z', '2026-09-01T00:00:00Z')).toBe(
    false
  )
})

test('permissions follow content_backup.view / manage with root always allowed', () => {
  expect(resolveContentBackupPerms(100, undefined, undefined)).toEqual({
    view: true,
    manage: true,
  })
  expect(resolveContentBackupPerms(10, undefined, ['content_backup.view'])).toEqual(
    { view: true, manage: false }
  )
  expect(
    resolveContentBackupPerms(10, undefined, ['content_backup.manage'])
  ).toEqual({ view: true, manage: true })
  // 新增权限默认不给存量普通管理员
  expect(
    resolveContentBackupPerms(10, undefined, ['channel.view', 'log.view'])
  ).toEqual({ view: false, manage: false })
  expect(resolveContentBackupPerms(10, undefined, undefined)).toEqual({
    view: false,
    manage: false,
  })
  expect(resolveContentBackupPerms(1, undefined, ['content_backup.manage'])).toEqual(
    { view: false, manage: false }
  )
  expect(
    resolveContentBackupPerms(10, { content_backup_view: true }, undefined)
  ).toEqual({ view: true, manage: false })
})

test('node heartbeat derives offline state from last_seen_at', () => {
  const now = Date.parse('2026-09-15T04:35:00Z')
  expect(
    nodeHeartbeat('2026-09-15T04:34:50Z', now, 45)
  ).toEqual({ state: 'online', secondsSince: 10 })
  expect(nodeHeartbeat('2026-09-15T04:33:00Z', now, 45)).toEqual({
    state: 'offline',
    secondsSince: 120,
  })
  expect(nodeHeartbeat(undefined, now, 45)).toEqual({
    state: 'unknown',
    secondsSince: undefined,
  })
  expect(nodeHeartbeat('garbage', now, 45)).toEqual({
    state: 'unknown',
    secondsSince: undefined,
  })
})

test('config drift is explicit when applied version differs', () => {
  expect(
    hasConfigDrift({ config_version: 7, applied_config_version: 7 })
  ).toBe(false)
  expect(
    hasConfigDrift({ config_version: 7, applied_config_version: 6 })
  ).toBe(true)
  expect(hasConfigDrift({ config_version: 7 })).toBe(false)
  expect(hasConfigDrift({})).toBe(false)
})

test('byte and duration formatting stay stable width friendly', () => {
  expect(formatBytes(undefined)).toBe('-')
  expect(formatBytes(0)).toBe('0 B')
  expect(formatBytes(1023)).toBe('1023 B')
  expect(formatBytes(1536)).toBe('1.5 KB')
  expect(formatBytes(5 * 1024 * 1024)).toBe('5 MB')
  expect(formatBytes(3 * 1024 * 1024 * 1024)).toBe('3 GB')
  expect(formatBytes(-1)).toBe('-')
  expect(humanizeSeconds(0)).toEqual({ value: 0, unitKey: 'seconds' })
  expect(humanizeSeconds(45)).toEqual({ value: 45, unitKey: 'seconds' })
  expect(humanizeSeconds(120)).toEqual({ value: 2, unitKey: 'minutes' })
  expect(humanizeSeconds(7200)).toEqual({ value: 2, unitKey: 'hours' })
  expect(humanizeSeconds(172800)).toEqual({ value: 2, unitKey: 'days' })
  expect(humanizeSeconds(-5)).toEqual({ value: 0, unitKey: 'seconds' })
})

describe('preview rendering keeps customer content inert', () => {
  const b64 = (text: string) => btoa(text)

  test('html and markdown stay plain text, never markup', () => {
    const rendered = renderPreviewSide({
      content_type: 'text/html',
      encoding: 'base64',
      body: b64('<script>alert(1)</script>'),
      captured_bytes: 25,
      observed_bytes: 25,
      truncated: false,
      complete: true,
    })
    expect(rendered.kind).toBe('text')
    if (rendered.kind !== 'text') throw new Error('unreachable')
    expect(rendered.text).toBe('<script>alert(1)</script>')
    expect(Object.keys(rendered)).not.toContain('html')
  })

  test('only complete json gets formatted', () => {
    const complete = renderPreviewSide({
      content_type: 'application/json',
      encoding: 'base64',
      body: b64('{"a":1}'),
      truncated: false,
      complete: true,
    })
    expect(complete.kind).toBe('json')
    if (complete.kind !== 'json') throw new Error('unreachable')
    expect(complete.text).toContain('\n')

    const truncated = renderPreviewSide({
      content_type: 'application/json',
      encoding: 'base64',
      body: b64('{"a":1'),
      truncated: true,
      complete: false,
    })
    expect(truncated.kind).toBe('text')
  })

  test('sse is split into event text blocks', () => {
    const rendered = renderPreviewSide({
      content_type: 'text/event-stream',
      encoding: 'base64',
      body: b64('data: {"a":1}\n\ndata: [DONE]\n\n'),
      truncated: false,
      complete: true,
    })
    expect(rendered.kind).toBe('sse')
    if (rendered.kind !== 'sse') throw new Error('unreachable')
    expect(rendered.events).toEqual(['data: {"a":1}', 'data: [DONE]'])
  })

  test('binary is described by type and length, never decoded as text', () => {
    const rendered = renderPreviewSide({
      content_type: 'image/png',
      encoding: 'base64',
      body: b64('not-really-a-png'),
      captured_bytes: 2048,
      observed_bytes: 2048,
      truncated: false,
      complete: true,
    })
    expect(rendered.kind).toBe('binary')
    if (rendered.kind !== 'binary') throw new Error('unreachable')
    expect(rendered.bytes).toBe(2048)
    expect(Object.keys(rendered)).not.toContain('text')
  })

  test('missing or empty bodies report empty', () => {
    expect(renderPreviewSide(undefined).kind).toBe('empty')
    expect(renderPreviewSide({ content_type: 'application/json' }).kind).toBe(
      'empty'
    )
    expect(
      renderPreviewSide({ content_type: 'application/json', body: '' }).kind
    ).toBe('empty')
  })

  test('non base64 encodings are read as utf-8 text', () => {
    const rendered = renderPreviewSide({
      content_type: 'text/plain',
      encoding: 'utf-8',
      body: 'plain body',
      complete: true,
    })
    expect(rendered.kind).toBe('text')
    if (rendered.kind !== 'text') throw new Error('unreachable')
    expect(rendered.text).toBe('plain body')
  })
})

test('empty and error states are distinguishable', () => {
  expect(
    emptyStateLabel({ canView: false, isError: false, itemCount: 0 })
  ).toBe('No permission to view content backups')
  expect(emptyStateLabel({ canView: true, isError: true, itemCount: 0 })).toBe(
    'Content backup request failed'
  )
  expect(
    emptyStateLabel({
      canView: true,
      isError: false,
      itemCount: 0,
      configured: false,
    })
  ).toBe('Content backup is not configured')
  expect(
    emptyStateLabel({
      canView: true,
      isError: false,
      itemCount: 0,
      configured: true,
      enabled: false,
    })
  ).toBe('Content capture is disabled')
  expect(
    emptyStateLabel({
      canView: true,
      isError: false,
      itemCount: 0,
      configured: true,
      enabled: true,
      uploadPaused: true,
    })
  ).toBe('Uploads are paused')
  expect(
    emptyStateLabel({
      canView: true,
      isError: false,
      itemCount: 0,
      configured: true,
      enabled: true,
      nodeOffline: true,
    })
  ).toBe('Storage node is offline')
  expect(
    emptyStateLabel({
      canView: true,
      isError: false,
      itemCount: 0,
      configured: true,
      enabled: true,
    })
  ).toBe('No matching backup records')
  expect(
    emptyStateLabel({ canView: true, isError: true, itemCount: 3 })
  ).toBeUndefined()
  // 采集/上传状态未知时不猜测原因
  expect(
    emptyStateLabel({ canView: true, isError: false, itemCount: 0 })
  ).toBe('No matching backup records')
})

// 空表第二行此前是写死的：接口 500 时表头写着「内容备份请求失败」，下面一行
// 仍然写着「尚无存储节点上报心跳。」。运营据此判断"确实没有节点"，而真相是
// 这次根本没拿到数据 —— 9.3 明令禁止的假 0。
test('empty description never asserts a count we did not fetch', () => {
  const nodesText = 'No storage node has reported a heartbeat yet.'

  expect(
    emptyStateDescription(
      { canView: true, isError: false, itemCount: 0, configured: true, enabled: true },
      nodesText
    )
  ).toBe(nodesText)

  expect(
    emptyStateDescription({ canView: true, isError: true, itemCount: 0 }, nodesText)
  ).toBe('')

  // 无权限时查询根本没发出去，同样不知道远端有没有节点
  expect(
    emptyStateDescription({ canView: false, isError: false, itemCount: 0 }, nodesText)
  ).toBe('')

  // 未配置/未开启是已知事实，不属于"没拿到数据"，描述照常显示
  expect(
    emptyStateDescription(
      { canView: true, isError: false, itemCount: 0, configured: false },
      nodesText
    )
  ).toBe(nodesText)
})

test('session fingerprint is stable and does not leak the raw value', () => {
  const a = sessionFingerprint('user-session-value')
  expect(a).toBe(sessionFingerprint('user-session-value'))
  expect(a).not.toBe(sessionFingerprint('other-session-value'))
  expect(a).not.toContain('user-session-value')
  expect(sessionFingerprint('')).toBe('')
})

test('node snapshot fields stay optional so a narrowed dto cannot crash', () => {
  const node: ContentBackupNodeSnapshot = { storage_node_id: 'node-a' }
  expect(hasConfigDrift(node)).toBe(false)
  expect(nodeHeartbeat(node.last_seen_at, Date.now(), 45).state).toBe('unknown')
})

test('today follows the Beijing day used by the daily stats key', () => {
  const morning = beijingDayRange(Date.parse('2026-09-15T04:00:00Z'))
  expect(morning.dateLabel).toBe('2026-09-15')
  expect(morning.from).toBe('2026-09-14T16:00:00.000Z')
  expect(morning.to).toBe('2026-09-15T15:59:59.000Z')

  const utcEvening = beijingDayRange(Date.parse('2026-09-15T17:00:00Z'))
  expect(utcEvening.dateLabel).toBe('2026-09-16')
  expect(utcEvening.from).toBe('2026-09-15T16:00:00.000Z')
})

test('integrity column never guesses when completeness fields are missing', () => {
  expect(integrityLabel({ response_truncated: true })).toBe('Truncated')
  expect(integrityLabel({ request_complete: false })).toBe('Incomplete')
  expect(
    integrityLabel({ request_complete: true, response_complete: true })
  ).toBe('Complete')
  expect(integrityLabel({})).toBe('Unknown')
})

import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { ADMIN_LOG_LIST_PATH, buildApiPath } from './api-path'
import { formatLogTotal } from './pagination'

describe('usage logs pagination', () => {
  test('marks a capped total with a plus suffix', () => {
    assert.equal(formatLogTotal(10000, true), `${(10000).toLocaleString()}+`)
    assert.equal(formatLogTotal(42, false), '42')
  })

  test('uses the canonical admin list URL without changing the user URL', () => {
    assert.equal(buildApiPath(ADMIN_LOG_LIST_PATH, true), '/api/log/')
    assert.equal(buildApiPath('/api/log', false), '/api/log/self')
  })
})

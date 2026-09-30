// @ts-expect-error -- bun:test 的类型未随仓库安装，测试由 bun 运行器执行
import { describe, expect, test } from 'bun:test'
import {
  collectProductRows,
  normalizeUsableGroups,
  removeProductGroup,
} from './group-ratio-maps.ts'

describe('removeProductGroup', () => {
  test('removes the product from every related map and keeps empty user-tier columns', () => {
    const next = removeProductGroup('drop', {
      groupRatio: { keep: 1, drop: 2 },
      groupGroupRatio: {
        vip: { keep: 0.9, drop: 0.5 },
        emptyTier: { drop: 1 },
      },
      usableGroups: {
        keep: { description: 'keep', user_selectable: true },
        drop: { description: 'drop', user_selectable: false },
      },
      autoGroups: ['keep', 'drop', 'other'],
      topupGroupRatio: { drop: 1.2, keep: 1 },
      specialUsable: {
        drop: { append_1: 'x' },
        vip: { '+:drop': 'hide', keep: 'ok' },
      },
    })

    expect(next.groupRatio).toEqual({ keep: 1 })
    expect(next.groupGroupRatio).toEqual({
      vip: { keep: 0.9 },
      emptyTier: {},
    })
    expect(next.usableGroups).toEqual({
      keep: { description: 'keep', user_selectable: true },
    })
    expect(next.autoGroups).toEqual(['keep', 'other'])
    expect(next.topupGroupRatio).toEqual({ keep: 1 })
    expect(next.specialUsable).toEqual({
      vip: { keep: 'ok' },
    })
  })
})

describe('collectProductRows', () => {
  test('unions GroupRatio, UserUsableGroups and GroupGroupRatio inner keys', () => {
    expect(
      collectProductRows(
        { a: 1 },
        { vip: { b: 0.9 } },
        { c: { description: 'only meta', user_selectable: true } },
        ['c', 'a']
      )
    ).toEqual(['c', 'a', 'b'])
  })
})

describe('normalizeUsableGroups', () => {
  test('accepts legacy string values and new object values', () => {
    expect(
      normalizeUsableGroups({
        legacy: '旧描述',
        modern: { description: '新描述', user_selectable: false },
      })
    ).toEqual({
      legacy: { description: '旧描述', user_selectable: true },
      modern: { description: '新描述', user_selectable: false },
    })
  })
})

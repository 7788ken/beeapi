// @ts-expect-error -- bun:test 的类型未随仓库安装，测试由 bun 运行器执行
import { describe, expect, test } from 'bun:test'
import type {
  PriceChangeBatch,
  PriceChangeItem,
  PriceChangesData,
} from '@/features/price-changes/types'
import type { GroupUptimeBucket } from '../types'
import {
  buildGroupChangeMap,
  buildGroupStates,
  classifyAvailability,
  countAvailable,
  latestChangeAt,
  highlightSegments,
  isRoutingGroup,
  matchesSearch,
  searchTokens,
  sortGroups,
  type SortableGroup,
} from './directory'

const H = 1_758_600_000 - (1_758_600_000 % 3600) // 某个 UTC 整点

function bucket(ts: number, requests: number, rate: number): GroupUptimeBucket {
  return {
    ts,
    request_count: requests,
    success_count: Math.round((requests * rate) / 100),
    success_rate: rate,
  }
}

describe('isRoutingGroup', () => {
  test('忽略 emoji 前缀和大小写识别 default / auto', () => {
    for (const name of [
      'default',
      '♻️ default',
      'Default',
      'auto',
      '🔄 auto ',
      'default ♻️',
      '1️⃣ default',
    ]) {
      expect(isRoutingGroup(name)).toBe(true)
    }
  })

  test('名字里只是包含 default 的普通分组不隐藏', () => {
    for (const name of [
      '🚀 Claude - Max5',
      '♻️ default-plus',
      'autoX',
      '🙋‍♂️ 千问 Qwen',
    ]) {
      expect(isRoutingGroup(name)).toBe(false)
    }
  })
})

describe('classifyAvailability', () => {
  test('整窗 request_count=0 的零序列 = 无可用渠道', () => {
    const zero = Array.from({ length: 24 }, (_, i) =>
      bucket(H - i * 3600, 0, 0)
    )
    expect(classifyAvailability(zero, H + 600)).toEqual({
      status: 'no_supply',
      latestPct: null,
    })
  })

  test('接口里没有这个分组 = 有渠道但近期没流量', () => {
    expect(classifyAvailability(undefined, H + 600).status).toBe('idle')
  })

  test('当前小时有请求：可用，数字取当前小时（成功率 0% 也算可用）', () => {
    expect(classifyAvailability([bucket(H, 3, 0)], H + 600)).toEqual({
      status: 'available',
      latestPct: 0,
    })
    expect(
      classifyAvailability(
        [bucket(H - 3600, 10, 50), bucket(H, 4, 75)],
        H + 600
      )
    ).toEqual({ status: 'available', latestPct: 75 })
  })

  test('整点刚过、后端缓存里还没有当前小时的桶：靠上一小时仍判可用', () => {
    expect(classifyAvailability([bucket(H - 3600, 20, 90)], H + 30)).toEqual({
      status: 'available',
      latestPct: null,
    })
  })

  test('只有两小时以前的请求 = 近 1 小时无请求', () => {
    expect(
      classifyAvailability([bucket(H - 7200, 20, 100)], H + 600).status
    ).toBe('idle')
  })
})

function item(partial: Partial<PriceChangeItem>): PriceChangeItem {
  return {
    scope: 'group',
    group_name: '',
    model_name: '',
    price_type: 'group_ratio',
    old_value: 0,
    new_value: 0,
    direction: 'up',
    affected_groups: [],
    display: {},
    ...partial,
  }
}

function batch(
  id: number,
  publishedAt: number,
  items: PriceChangeItem[]
): PriceChangeBatch {
  return {
    id,
    published_at: publishedAt,
    note: '',
    affected_groups: [],
    summary: { up: 0, down: 0, added: 0, removed: 0 },
    items,
  }
}

describe('buildGroupChangeMap', () => {
  const nowMs = H * 1000
  const nowSec = H
  const feed = (
    batches: PriceChangeBatch[],
    enabled = true
  ): PriceChangesData => ({
    enabled,
    badge_days: 7,
    batches,
  })

  test('功能关闭或窗口外的批次不算变动', () => {
    const ratios = new Map([['A', 2]])
    const b = batch(1, nowSec - 3600, [item({ group_name: 'A', new_value: 2 })])
    expect(buildGroupChangeMap(feed([b], false), ratios, nowMs).size).toBe(0)
    const old = batch(2, nowSec - 8 * 86400, [
      item({ group_name: 'A', new_value: 2 }),
    ])
    expect(buildGroupChangeMap(feed([old]), ratios, nowMs).size).toBe(0)
  })

  test('同一分组取最新一批；removed 和不在列表里的分组忽略', () => {
    const ratios = new Map([
      ['A', 3],
      ['B', 1],
    ])
    const older = batch(1, nowSec - 2 * 86400, [
      item({ group_name: 'A', direction: 'added', old_value: 0, new_value: 4 }),
    ])
    const newer = batch(2, nowSec - 86400, [
      item({ group_name: 'A', direction: 'down', old_value: 4, new_value: 3 }),
      item({
        group_name: 'B',
        direction: 'removed',
        old_value: 1,
        new_value: 0,
      }),
      item({ group_name: 'Gone', direction: 'added', new_value: 1 }),
    ])
    const map = buildGroupChangeMap(feed([older, newer]), ratios, nowMs)
    expect([...map.keys()]).toEqual(['A'])
    expect(map.get('A')).toEqual({
      kind: 'down',
      from: 4,
      to: 3,
      at: nowSec - 86400,
    })
  })

  test('用户有专属倍率时全局调价不算，改看 group_group_ratio 条目', () => {
    const ratios = new Map([['🚀 Max1', 0.85]]) // 用户视角倍率来自 GroupGroupRatio
    const globalChange = batch(2, nowSec - 3600, [
      item({ group_name: '🚀 Max1', old_value: 0.9, new_value: 1 }),
    ])
    const ownChange = batch(1, nowSec - 7200, [
      item({
        price_type: 'group_group_ratio',
        group_name: 'vip->🚀 Max1',
        direction: 'down',
        old_value: 0.9,
        new_value: 0.85,
      }),
    ])
    const map = buildGroupChangeMap(
      feed([globalChange, ownChange]),
      ratios,
      nowMs
    )
    expect(map.get('🚀 Max1')).toEqual({
      kind: 'down',
      from: 0.9,
      to: 0.85,
      at: nowSec - 7200,
    })
  })

  test('给本用户组新设专属倍率 = dedicated，不能显示成「新增分组」；与现价不符的忽略', () => {
    const ratios = new Map([
      ['Max', 0.8],
      ['Other', 1],
    ])
    const b = batch(1, nowSec - 60, [
      item({
        price_type: 'group_group_ratio',
        group_name: 'vip->Max',
        direction: 'added',
        new_value: 0.8,
      }),
      item({
        price_type: 'group_group_ratio',
        group_name: 'vip->Other',
        direction: 'added',
        new_value: 0.5,
      }),
    ])
    const map = buildGroupChangeMap(feed([b]), ratios, nowMs)
    expect(map.get('Max')).toEqual({
      kind: 'dedicated',
      from: 0,
      to: 0.8,
      at: nowSec - 60,
    })
    expect(map.has('Other')).toBe(false)
  })

  test('专属倍率被撤掉 = 回落到全局价的一次调价；价格没变则不算', () => {
    const ratios = new Map([
      ['Max', 1],
      ['Same', 0.9],
    ])
    const b = batch(1, nowSec - 60, [
      item({
        price_type: 'group_group_ratio',
        group_name: 'vip->Max',
        direction: 'removed',
        old_value: 0.8,
        new_value: 0,
      }),
      item({
        price_type: 'group_group_ratio',
        group_name: 'vip->Same',
        direction: 'removed',
        old_value: 0.9,
        new_value: 0,
      }),
    ])
    const map = buildGroupChangeMap(feed([b]), ratios, nowMs)
    expect(map.get('Max')).toEqual({
      kind: 'up',
      from: 0.8,
      to: 1,
      at: nowSec - 60,
    })
    expect(map.has('Same')).toBe(false)
  })

  test('新增分组不要求倍率相等', () => {
    const ratios = new Map([['New', 0.5]])
    const b = batch(1, nowSec - 60, [
      item({ group_name: 'New', direction: 'added', new_value: 1 }),
    ])
    expect(buildGroupChangeMap(feed([b]), ratios, nowMs).get('New')?.kind).toBe(
      'added'
    )
  })
})

describe('buildGroupStates', () => {
  const nowMs = H * 1000 + 600_000
  const groups = {
    '♻️ default': { ratio: 1, desc: '默认分组' },
    A: { ratio: 0.5, desc: 'a' },
    B: { ratio: 2 },
  }

  test('隐藏路由分组；可用率未加载时一律 unknown、不计入可用数', () => {
    const states = buildGroupStates(groups, undefined, null, nowMs)
    expect(states.map((g) => [g.name, g.desc, g.availability.status])).toEqual([
      ['A', 'a', 'unknown'],
      ['B', '', 'unknown'],
    ])
    expect(countAvailable(states)).toBe(0)
  })

  test('可用数与最近变动时间', () => {
    const uptime = { A: [bucket(H, 5, 100)], B: [bucket(H - 7200, 5, 100)] }
    const changes: PriceChangesData = {
      enabled: true,
      badge_days: 7,
      batches: [
        batch(1, H - 60, [
          item({ group_name: 'B', direction: 'added', new_value: 2 }),
        ]),
      ],
    }
    const states = buildGroupStates(groups, uptime, changes, nowMs)
    expect(countAvailable(states)).toBe(1)
    expect(latestChangeAt(states)).toBe(H - 60)
  })
})

describe('search', () => {
  test('空格分词、全部命中才算、不区分大小写和顺序', () => {
    const tokens = searchTokens('  MAX5   claude ')
    expect(tokens).toEqual(['max5', 'claude'])
    expect(matchesSearch('🚀 Claude - Max5', '满血', tokens)).toBe(true)
    expect(matchesSearch('🚀 Claude - Max2', '满血', tokens)).toBe(false)
    expect(
      matchesSearch(
        '🐳 Deepseek',
        '官网直连｜deepseek-v4',
        searchTokens('官网')
      )
    ).toBe(true)
  })

  test('词不会跨越分组名和简介的边界拼出命中', () => {
    expect(matchesSearch('abc', 'def', searchTokens('cd'))).toBe(false)
  })

  test('高亮合并重叠片段、覆盖多次出现', () => {
    expect(highlightSegments('Claude claude', ['cla', 'laude'])).toEqual([
      { text: 'Claude', hit: true },
      { text: ' ', hit: false },
      { text: 'claude', hit: true },
    ])
    expect(highlightSegments('Kiro 企业版', [])).toEqual([
      { text: 'Kiro 企业版', hit: false },
    ])
    expect(highlightSegments('Kiro 企业版', ['企业'])).toEqual([
      { text: 'Kiro ', hit: false },
      { text: '企业', hit: true },
      { text: '版', hit: false },
    ])
  })
})

describe('sortGroups', () => {
  const g = (
    name: string,
    ratio: number,
    platformRank: number,
    status: SortableGroup['availability']['status'],
    latestPct: number | null
  ): SortableGroup => ({
    name,
    ratio,
    platformRank,
    availability: { status, latestPct },
  })

  const list = [
    g('idle', 0.1, 0, 'idle', null),
    g('dead', 0.2, 0, 'no_supply', null),
    g('prev-hour-only', 3, 1, 'available', null),
    g('failing', 1, 1, 'available', 0),
    g('healthy', 2, 2, 'available', 99),
    g('bad-ratio', Number.NaN, 0, 'available', 50),
  ]
  const names = (key: Parameters<typeof sortGroups>[1]) =>
    sortGroups(list, key).map((x) => x.name)

  test('可用率：有数的高到低，其次当前小时无数据，再次近 1 小时无请求，无可用渠道垫底', () => {
    expect(names('availability')).toEqual([
      'healthy',
      'bad-ratio',
      'failing',
      'prev-hour-only',
      'idle',
      'dead',
    ])
  })

  test('倍率升降序，非数字倍率始终排最后', () => {
    expect(names('ratio_asc')).toEqual([
      'idle',
      'dead',
      'failing',
      'healthy',
      'prev-hour-only',
      'bad-ratio',
    ])
    expect(names('ratio_desc')).toEqual([
      'prev-hour-only',
      'healthy',
      'failing',
      'dead',
      'idle',
      'bad-ratio',
    ])
  })

  test('名称按自然数字排序', () => {
    const maxes = [
      g('Max10', 1, 0, 'available', 90),
      g('Max9', 1, 0, 'available', 90),
      g('Max2', 1, 0, 'available', 90),
    ]
    expect(sortGroups(maxes, 'ratio_asc').map((x) => x.name)).toEqual([
      'Max2',
      'Max9',
      'Max10',
    ])
  })

  test('默认：分类顺序 → 倍率低到高 → 名称', () => {
    expect(names('default')).toEqual([
      'idle',
      'dead',
      'bad-ratio',
      'failing',
      'prev-hour-only',
      'healthy',
    ])
  })
})

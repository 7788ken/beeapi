import {
  badgeWindowStart,
  parseGroupGroupRatioName,
} from '@/features/price-changes/lib'
import type { PriceChangesData } from '@/features/price-changes/types'
import type {
  GroupUptimeBucket,
  GroupUptimeResponse,
  SelfGroupsResponse,
} from '../types'

const HOUR_SEC = 3600

/**
 * default / auto 是自动路由分组：自己没有供给和可用率（auto 的倍率还是字符串「自动」），列表不展示。
 * 站点常给分组名加 emoji（ai 站默认分组叫「♻️ default」），先去掉首尾符号和键帽 emoji（1️⃣）再比对。
 * 只在本页过滤——/api/user/self/groups 不动，密钥分组选择、Playground 等处仍可选用它们。
 */
export function isRoutingGroup(name: string): boolean {
  const bare = name
    .replace(/\p{N}\uFE0F?\u20E3/gu, '')
    .replace(/^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$/gu, '')
    .toLowerCase()
  return bare === 'default' || bare === 'auto'
}

/** unknown = 可用率数据还没加载到（或加载失败），此时不参与「只看可用」筛选 */
export type AvailabilityStatus = 'available' | 'idle' | 'no_supply' | 'unknown'

export interface GroupAvailability {
  status: AvailabilityStatus
  /** 当前小时的成功率，与迷你图尾部数字同源；当前小时还没有请求时为 null */
  latestPct: number | null
}

export const UNKNOWN_AVAILABILITY: GroupAvailability = {
  status: 'unknown',
  latestPct: null,
}

/**
 * 「可用」= 最近 1 小时有可用率数据，成功率不限（用户口径「近 1 小时可用率 ≥ 0%」）。
 * 数据与迷你图同源：真实流量 + 启用态测活，站点开了定时测活时有启用渠道的分组基本都会有数据。
 *
 * 桶按 UTC 整点切、当前小时还没过完，后端结果又缓存 5 分钟：只看当前小时的话，每个整点后几分钟
 * 所有分组都会被判成不可用。所以窗口取「当前小时 + 上一小时」，保证始终覆盖最近 60 分钟。
 *
 * 后端口径（service/group_uptime.go）：没有启用渠道的分组返回整窗 request_count=0 的零序列；
 * 有渠道但 24 小时内没有流量的分组不出现在返回里（buckets 为 undefined）；真实桶 request_count 必大于 0。
 */
export function classifyAvailability(
  buckets: GroupUptimeBucket[] | undefined,
  nowSec: number
): GroupAvailability {
  if (buckets?.length && buckets.every((b) => b.request_count === 0)) {
    return { status: 'no_supply', latestPct: null }
  }
  const currentHour = nowSec - (nowSec % HOUR_SEC)
  let recent = false
  let latestPct: number | null = null
  for (const b of buckets ?? []) {
    if (b.request_count === 0 || b.ts < currentHour - HOUR_SEC) continue
    recent = true
    if (b.ts === currentHour) latestPct = b.success_rate
  }
  return { status: recent ? 'available' : 'idle', latestPct }
}

/** added=新增分组；dedicated=给本用户组新设了专属倍率（原价未知，后端 old_value 恒为 0） */
export type GroupChangeKind = 'added' | 'up' | 'down' | 'dedicated'

export interface GroupChange {
  kind: GroupChangeKind
  from: number
  to: number
  /** 发布时间（unix 秒） */
  at: number
}

const sameRatio = (a: number, b: number) => Math.abs(a - b) <= 1e-9

/**
 * 分组「变动」= 价格变动发布里、展示窗口（badge_days）内涉及该分组的最新一条分组条目。
 * 与模型广场涨跌徽标、铃铛通知是同一个数据源。
 *
 * 结果必须与用户现在看到的倍率对得上才算：用户分组有专属倍率（group_group_ratio）时，
 * 全局 group_ratio 的调整并不影响他，照样展示就是错的。专属倍率条目后端只返回本用户组的。
 */
export function buildGroupChangeMap(
  data: PriceChangesData | null | undefined,
  currentRatios: ReadonlyMap<string, number>,
  nowMs: number
): Map<string, GroupChange> {
  const map = new Map<string, GroupChange>()
  if (!data?.enabled || !Array.isArray(data.batches)) return map

  const windowStart = badgeWindowStart(data.badge_days, nowMs)
  const batches = data.batches
    .filter((b) => b.published_at >= windowStart)
    .sort((a, b) => b.published_at - a.published_at)

  for (const batch of batches) {
    for (const item of batch.items ?? []) {
      if (item.scope !== 'group') continue
      const dedicated = item.price_type === 'group_group_ratio'
      const target = dedicated
        ? parseGroupGroupRatioName(item.group_name)?.targetGroup
        : item.group_name
      if (!target || map.has(target)) continue
      const ratio = currentRatios.get(target)
      if (ratio === undefined) continue
      const at = batch.published_at

      if (item.direction === 'removed') {
        // 专属倍率被撤掉：用户回落到全局倍率，按「原专属价 → 现价」算一次调价；分组本身被删则已不在列表里
        if (!dedicated || sameRatio(item.old_value, ratio)) continue
        const kind = ratio > item.old_value ? 'up' : 'down'
        map.set(target, { kind, from: item.old_value, to: ratio, at })
        continue
      }
      if (item.direction === 'added' && !dedicated) {
        map.set(target, { kind: 'added', from: 0, to: item.new_value, at })
        continue
      }
      if (!sameRatio(item.new_value, ratio)) continue
      const kind = item.direction === 'added' ? 'dedicated' : item.direction
      map.set(target, { kind, from: item.old_value, to: item.new_value, at })
    }
  }
  return map
}

/** 不含分类与迷你图的分组状态：侧栏入口只要这些，不必把品牌图标库拉进常驻包 */
export interface GroupState {
  name: string
  desc: string
  ratio: number
  availability: GroupAvailability
  change?: GroupChange
}

/**
 * 合并三份数据。uptime 为 undefined（未加载或加载失败）时可用性一律 unknown，
 * nowMs 由调用方传入数据的拉取时刻，渲染期不读系统时钟。
 */
export function buildGroupStates(
  groups: SelfGroupsResponse,
  uptime: GroupUptimeResponse | undefined,
  priceChanges: PriceChangesData | null | undefined,
  nowMs: number
): GroupState[] {
  const nowSec = Math.floor(nowMs / 1000)
  const list = Object.entries(groups)
    .filter(([name]) => !isRoutingGroup(name))
    .map(([name, info]) => ({
      name,
      desc: info.desc ?? '',
      ratio: Number(info.ratio),
      availability: uptime
        ? classifyAvailability(uptime[name], nowSec)
        : UNKNOWN_AVAILABILITY,
    }))
  const changes = buildGroupChangeMap(
    priceChanges,
    new Map(list.map((g) => [g.name, g.ratio])),
    nowMs
  )
  return list.map((g) => ({ ...g, change: changes.get(g.name) }))
}

export function countAvailable(states: readonly GroupState[]): number {
  return states.filter((g) => g.availability.status === 'available').length
}

export function latestChangeAt(states: readonly GroupState[]): number {
  return states.reduce((max, g) => Math.max(max, g.change?.at ?? 0), 0)
}

export function searchTokens(query: string): string[] {
  return query.trim().toLowerCase().split(/\s+/).filter(Boolean)
}

/** 模糊搜索：空格分隔的每个词都要出现在「分组名 + 简介」里，不区分大小写、不限顺序 */
export function matchesSearch(
  name: string,
  desc: string,
  tokens: readonly string[]
): boolean {
  const haystack = `${name}\n${desc}`.toLowerCase()
  return tokens.every((token) => haystack.includes(token))
}

export interface TextSegment {
  text: string
  hit: boolean
}

/** 切出命中搜索词的片段供高亮；大小写折叠后长度会变的文本（极少见）下标对不上，不高亮 */
export function highlightSegments(
  text: string,
  tokens: readonly string[]
): TextSegment[] {
  const lower = text.toLowerCase()
  const ranges: [number, number][] = []
  if (lower.length === text.length) {
    for (const token of tokens) {
      for (
        let at = lower.indexOf(token);
        at !== -1;
        at = lower.indexOf(token, at + token.length)
      ) {
        ranges.push([at, at + token.length])
      }
    }
  }
  if (ranges.length === 0) return [{ text, hit: false }]

  ranges.sort((a, b) => a[0] - b[0])
  const merged: [number, number][] = [ranges[0]]
  for (const [start, end] of ranges.slice(1)) {
    const last = merged[merged.length - 1]
    if (start <= last[1]) last[1] = Math.max(last[1], end)
    else merged.push([start, end])
  }

  const segments: TextSegment[] = []
  let cursor = 0
  for (const [start, end] of merged) {
    if (start > cursor)
      segments.push({ text: text.slice(cursor, start), hit: false })
    segments.push({ text: text.slice(start, end), hit: true })
    cursor = end
  }
  if (cursor < text.length)
    segments.push({ text: text.slice(cursor), hit: false })
  return segments
}

export type GroupSortKey =
  | 'default'
  | 'availability'
  | 'ratio_asc'
  | 'ratio_desc'

export interface SortableGroup {
  name: string
  ratio: number
  /** 分类在 PLATFORMS 里的顺序，「默认排序」先按它分块 */
  platformRank: number
  availability: GroupAvailability
}

const STATUS_RANK: Record<AvailabilityStatus, number> = {
  available: 0,
  unknown: 0,
  idle: 1,
  no_supply: 2,
}

// 非数字倍率（后端契约外的值）无论升降序都排最后
function compareRatio(a: SortableGroup, b: SortableGroup, dir: 1 | -1) {
  const ka = Number.isFinite(a.ratio) ? a.ratio * dir : Number.POSITIVE_INFINITY
  const kb = Number.isFinite(b.ratio) ? b.ratio * dir : Number.POSITIVE_INFINITY
  return ka === kb ? 0 : ka < kb ? -1 : 1
}

// numeric：「Max10」排在「Max9」之后
function compareName(a: SortableGroup, b: SortableGroup) {
  return a.name.localeCompare(b.name, undefined, { numeric: true })
}

/**
 * 可用率排序看的是行上显示的那个数（当前小时）：有数的从高到低，当前小时还没请求的（—）其次，
 * 近 1 小时无请求、无可用渠道的垫底。
 */
export function sortGroups<T extends SortableGroup>(
  list: readonly T[],
  key: GroupSortKey
): T[] {
  const out = [...list]
  switch (key) {
    case 'availability':
      return out.sort(
        (a, b) =>
          STATUS_RANK[a.availability.status] -
            STATUS_RANK[b.availability.status] ||
          (b.availability.latestPct ?? -1) - (a.availability.latestPct ?? -1) ||
          compareRatio(a, b, 1) ||
          compareName(a, b)
      )
    case 'ratio_asc':
      return out.sort((a, b) => compareRatio(a, b, 1) || compareName(a, b))
    case 'ratio_desc':
      return out.sort((a, b) => compareRatio(a, b, -1) || compareName(a, b))
    case 'default':
      return out.sort(
        (a, b) =>
          a.platformRank - b.platformRank ||
          compareRatio(a, b, 1) ||
          compareName(a, b)
      )
  }
}

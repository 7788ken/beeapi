export type RatioMap = Record<string, number>
export type GroupGroupMap = Record<string, RatioMap>
export type UsableGroupRawValue =
  | string
  | { description?: string; user_selectable?: boolean }
export type UsableGroupsMap = Record<
  string,
  { description: string; user_selectable: boolean }
>
export type SpecialUsableMap = Record<string, Record<string, string>>

export function normalizeUsableGroups(
  raw: Record<string, UsableGroupRawValue> | null | undefined
): UsableGroupsMap {
  const out: UsableGroupsMap = {}
  if (!raw || typeof raw !== 'object') return out
  for (const [k, v] of Object.entries(raw)) {
    if (typeof v === 'string') {
      out[k] = { description: v, user_selectable: true }
    } else {
      out[k] = {
        description: v?.description ?? '',
        user_selectable: v?.user_selectable !== false,
      }
    }
  }
  return out
}

export function collectProductRows(
  groupRatio: RatioMap,
  groupGroupRatio: GroupGroupMap,
  usableGroups: UsableGroupsMap,
  rowOrder: string[] = []
): string[] {
  const allSet = new Set<string>([
    ...Object.keys(groupRatio),
    ...Object.keys(usableGroups),
  ])
  for (const col of Object.values(groupGroupRatio || {})) {
    for (const tg of Object.keys(col || {})) {
      allSet.add(tg)
    }
  }
  const all = Array.from(allSet)
  if (rowOrder.length === 0) return all
  const orderIdx = new Map<string, number>()
  rowOrder.forEach((k, i) => orderIdx.set(k, i))
  const naturalIdx = new Map<string, number>()
  all.forEach((k, i) => naturalIdx.set(k, i))
  return all.slice().sort((a, b) => {
    const ai = orderIdx.has(a) ? (orderIdx.get(a) as number) : Number.POSITIVE_INFINITY
    const bi = orderIdx.has(b) ? (orderIdx.get(b) as number) : Number.POSITIVE_INFINITY
    if (ai !== bi) return ai - bi
    return (naturalIdx.get(a) as number) - (naturalIdx.get(b) as number)
  })
}

export function removeProductGroup(
  row: string,
  input: {
    groupRatio: RatioMap
    groupGroupRatio: GroupGroupMap
    usableGroups: UsableGroupsMap
    autoGroups: string[]
    topupGroupRatio: RatioMap
    specialUsable: SpecialUsableMap
  }
): {
  groupRatio: RatioMap
  groupGroupRatio: GroupGroupMap
  usableGroups: UsableGroupsMap
  autoGroups: string[]
  topupGroupRatio: RatioMap
  specialUsable: SpecialUsableMap
} {
  const groupRatio = { ...input.groupRatio }
  delete groupRatio[row]

  const groupGroupRatio: GroupGroupMap = {}
  for (const [ug, col] of Object.entries(input.groupGroupRatio || {})) {
    const colMap = { ...(col || {}) }
    delete colMap[row]
    // Keep empty user-tier columns so deleting a product does not drop the tier.
    groupGroupRatio[ug] = colMap
  }

  const usableGroups = { ...input.usableGroups }
  delete usableGroups[row]

  const autoGroups = (input.autoGroups || []).filter((g) => g !== row)

  const topupGroupRatio = { ...input.topupGroupRatio }
  delete topupGroupRatio[row]

  const specialUsable: SpecialUsableMap = {}
  for (const [ug, inner] of Object.entries(input.specialUsable || {})) {
    if (ug === row) continue
    const nextInner: Record<string, string> = {}
    for (const [k, v] of Object.entries(inner || {})) {
      const name = k.replace(/^(\+:|-:)/, '')
      if (name === row) continue
      nextInner[k] = v
    }
    specialUsable[ug] = nextInner
  }

  return {
    groupRatio,
    groupGroupRatio,
    usableGroups,
    autoGroups,
    topupGroupRatio,
    specialUsable,
  }
}

/**
 * 治理配置的互相抵消检测。
 *
 * 这些组合都是合法配置，后端不会报错，但语义上互相拆台——例如「巡检失败即禁用」
 * 关掉之后，被动健康度的连错直禁仍会掉渠道（它不经过那个开关）。纯函数，无副作用，
 * 便于单测与后续扩展规则。
 */

export type GovernanceConflictLevel = 'error' | 'warning'

export type GovernanceConflict = {
  id: string
  level: GovernanceConflictLevel
  /** i18n key，英文原文 */
  title: string
  /** i18n key，英文原文；占位符用 {{name}} */
  detail: string
  values?: Record<string, string | number>
}

export type GovernanceConflictInput = {
  autoTestEnabled: boolean
  automaticDisableChannelEnabled: boolean
  automaticEnableChannelEnabled: boolean
  healthEnabled: boolean
  disableThreshold: number
  baseDegradeThreshold: number
  levelStepThreshold: number
  maxDegradeLevel: number
  count429AsError: boolean
  recoveryStrategy: string
  modelRateLimitRemovalEnabled: boolean
}

/** 走完整条降级阶梯（到 maxDegradeLevel）所需的连错数 */
export function streakForDeepestLevel(input: {
  baseDegradeThreshold: number
  levelStepThreshold: number
  maxDegradeLevel: number
}): number {
  const { baseDegradeThreshold, levelStepThreshold, maxDegradeLevel } = input
  if (maxDegradeLevel <= 1) return baseDegradeThreshold
  return baseDegradeThreshold + levelStepThreshold * (maxDegradeLevel - 1)
}

export function detectGovernanceConflicts(
  input: GovernanceConflictInput
): GovernanceConflict[] {
  const conflicts: GovernanceConflict[] = []

  // R1 关掉「巡检失败即禁用」不代表渠道不会被自动停用：连错直禁走另一条路径，
  // 只看渠道自身的 auto_ban，不看这个总开关。
  //
  // 分两档：阈值在可触达范围内是真隐患；被设成远超阶梯的极高值（生产上常见的
  // 「设 999 当关掉」）实际不会触发，报成 error 只是噪音——但语义上它仍是开启状态，
  // 值得提醒改成 0，否则下一个看这个页面的人会误判。
  if (
    !input.automaticDisableChannelEnabled &&
    input.healthEnabled &&
    input.disableThreshold > 0
  ) {
    const reachable = input.disableThreshold <= streakForDeepestLevel(input) * 3
    conflicts.push(
      reachable
        ? {
            id: 'disable-switch-bypassed',
            level: 'error',
            title: 'Channels can still be stopped automatically',
            detail:
              'Stop on probe failure is off, but consecutive-error auto-stop is set to {{threshold}} and takes a different code path that does not check that switch. Set it to 0 if you want no automatic stops at all.',
            values: { threshold: input.disableThreshold },
          }
        : {
            id: 'disable-switch-bypassed-unreachable',
            level: 'warning',
            title: 'Consecutive-error auto-stop is off in practice, on in principle',
            detail:
              'It is set to {{threshold}}, far beyond the {{deepest}} errors needed for the deepest degrade level, so it will realistically never fire. Note that it does not check the stop-on-probe-failure switch either. Set it to 0 to say "never" outright — the next person reading this page will thank you.',
            values: {
              threshold: input.disableThreshold,
              deepest: streakForDeepestLevel(input),
            },
          }
    )
  }

  // R2 被动健康度是降级探测与恢复探活两条循环的前置，且关掉后连成功也不再累计，
  // 已降级渠道的等级与权重冻结在库里。
  if (!input.healthEnabled) {
    conflicts.push({
      id: 'degradation-frozen',
      level: 'warning',
      title: 'Degraded channels are frozen',
      detail:
        'Channel health is off, so degrade probing and recovery probing are both stopped and successful requests no longer count toward upgrades. Any channel already degraded keeps its lowered priority and weight until someone recovers it by hand.',
    })
  }

  // R3 连错直禁的阈值若不高于走完阶梯所需连错数，渠道会在到达最深降级前就被停用，
  // 中间那些降级档位形同虚设。
  if (input.healthEnabled && input.disableThreshold > 0) {
    const deepest = streakForDeepestLevel(input)
    if (input.disableThreshold <= deepest) {
      conflicts.push({
        id: 'ladder-unreachable',
        level: 'warning',
        title: 'Lower degrade levels are unreachable',
        detail:
          'Consecutive-error auto-stop fires at {{threshold}} errors, but reaching the deepest degrade level needs {{deepest}}. Channels get stopped before the ladder finishes, so the lower levels never apply.',
        values: { threshold: input.disableThreshold, deepest },
      })
    }
  }

  // R4 同一次 429 会既摘掉模型又推高渠道连错，两种处置叠加。
  if (
    input.modelRateLimitRemovalEnabled &&
    input.healthEnabled &&
    input.count429AsError
  ) {
    conflicts.push({
      id: 'rate-limit-double-disposition',
      level: 'warning',
      title: 'Rate limits trigger two dispositions at once',
      detail:
        'A single upstream 429 both removes the model and pushes the channel error streak up. If rate limiting is routine here, keep only the model removal.',
    })
  }

  // R5 停用渠道有两条自动恢复路径：巡检测通即启用，或恢复探活。两条都不通时
  // 只能人工启用，且界面上没有任何地方会说这件事。
  const probeRecovery = input.autoTestEnabled && input.automaticEnableChannelEnabled
  const activeRecovery =
    input.healthEnabled && input.recoveryStrategy === 'probe'
  if (!probeRecovery && !activeRecovery) {
    conflicts.push({
      id: 'no-recovery-path',
      level: 'error',
      title: 'Stopped channels will never come back on their own',
      detail:
        'Neither recovery path is active: scheduled probing with auto-enable is off, and health recovery probing is off. Every automatically stopped channel stays stopped until someone enables it by hand.',
    })
  }

  return conflicts
}

/** 每行一个关键词的文本框 → 去空行、去首尾空白后的条目列表 */
function parseKeywordLines(raw: string): string[] {
  return (raw ?? '')
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}

/**
 * 权限关键词表与停渠道关键词表的重叠条目（大小写不敏感比对，返回权限表里的原始写法）。
 *
 * 重叠本身不是错误：relay 报错路径先尝试模型级摘除，接管成功即短路整渠道禁用，
 * 所以命中权限表的错误走摘模型、优先于停渠道。但这会静默降低那几条的处置烈度，
 * 管理员必须看得见是哪几条。
 *
 * 刻意不做自动迁移：各站点在停渠道表里自加了条目（欠费、no available channel 等），
 * 无法自动判断归属，猜错就是改线上行为。
 */
export function findOverlappingKeywords(
  forbiddenKeywords: string,
  disableKeywords: string
): string[] {
  const disableSet = new Set(
    parseKeywordLines(disableKeywords).map((k) => k.toLowerCase())
  )
  const seen = new Set<string>()
  const overlaps: string[] = []
  for (const keyword of parseKeywordLines(forbiddenKeywords)) {
    const normalized = keyword.toLowerCase()
    if (!disableSet.has(normalized) || seen.has(normalized)) continue
    seen.add(normalized)
    overlaps.push(keyword)
  }
  return overlaps
}

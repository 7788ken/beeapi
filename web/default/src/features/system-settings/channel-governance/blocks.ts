/** 渠道治理页内的 tab。一个 tab 对应一块功能、一个保存按钮。 */
export const GOVERNANCE_TABS = [
  'overview',
  'routing',
  'affinity',
  'retry',
  'response-quality',
  'degradation',
  'removal',
  'probes',
] as const

export type GovernanceTabId = (typeof GOVERNANCE_TABS)[number]

const TAB_ALIAS: Record<string, GovernanceTabId> = {
  route: 'routing',
  fail: 'retry',
  classification: 'overview',
  dispose: 'degradation',
  probe: 'probes',
}

export function tabFromSection(section: string | undefined): GovernanceTabId {
  if (!section) return 'overview'
  if ((GOVERNANCE_TABS as readonly string[]).includes(section)) {
    return section as GovernanceTabId
  }
  return TAB_ALIAS[section] ?? 'overview'
}

export function tabFromHash(hash: string): GovernanceTabId | null {
  const id = hash.replace(/^#/, '')
  if (!id) return null
  if (
    !(GOVERNANCE_TABS as readonly string[]).includes(id) &&
    !(id in TAB_ALIAS)
  ) {
    return null
  }
  return tabFromSection(id)
}

export const GOVERNANCE_OPEN_BLOCK = 'governance-open-block'

export type GovernanceOpenDetail = {
  tab: GovernanceTabId
}

/** 切到某个 tab。不走路由，避免表单被卸载后丢掉未保存的修改。 */
export function openGovernanceBlock(tab: GovernanceTabId) {
  const next = `#${tab}`
  if (window.location.hash !== next) {
    window.history.replaceState(
      null,
      '',
      `${window.location.pathname}${window.location.search}${next}`
    )
  }
  window.dispatchEvent(
    new CustomEvent<GovernanceOpenDetail>(GOVERNANCE_OPEN_BLOCK, {
      detail: { tab },
    })
  )
}

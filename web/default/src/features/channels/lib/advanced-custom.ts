import type { AdvancedCustomConfig } from '../types'

export const CHANNEL_TYPE_ADVANCED_CUSTOM = 59
export const ADVANCED_CUSTOM_MODEL_LIST_PATH = '/v1/models'

export function createAdvancedCustomConfig(): AdvancedCustomConfig {
  return {
    advanced_routes: [
      {
        incoming_path: '/v1/chat/completions',
        upstream_path: '/v1/chat/completions',
        converter: 'none',
      },
      {
        incoming_path: ADVANCED_CUSTOM_MODEL_LIST_PATH,
        upstream_path: ADVANCED_CUSTOM_MODEL_LIST_PATH,
        converter: 'none',
      },
    ],
  }
}

export function stringifyAdvancedCustomConfig(
  config: AdvancedCustomConfig
): string {
  return JSON.stringify(config, null, 2)
}

export function parseAdvancedCustomConfig(
  value: string | undefined
): AdvancedCustomConfig | null {
  if (!value?.trim()) return null
  try {
    const parsed = JSON.parse(value)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    return parsed as AdvancedCustomConfig
  } catch {
    return null
  }
}

export function validateAdvancedCustomConfig(
  config: AdvancedCustomConfig | null
): string | null {
  if (!config || !Array.isArray(config.advanced_routes)) {
    return 'Advanced custom configuration is required'
  }
  if (config.advanced_routes.length === 0) {
    return 'Advanced custom configuration requires at least one route'
  }
  for (const route of config.advanced_routes) {
    if (!route.incoming_path?.startsWith('/')) {
      return 'Incoming path must start with /'
    }
    if (
      !route.upstream_path?.startsWith('/') &&
      !/^https?:\/\//.test(route.upstream_path || '')
    ) {
      return 'Upstream path must be a full URL or start with /'
    }
  }
  return null
}

export function advancedCustomConfigUsesRelativeUpstreamPath(
  config: AdvancedCustomConfig | null
): boolean {
  return Boolean(
    config?.advanced_routes?.some((route) =>
      route.upstream_path?.startsWith('/')
    )
  )
}

export function hasAdvancedCustomModelListRoute(
  config: AdvancedCustomConfig | null
): boolean {
  return Boolean(
    config?.advanced_routes?.some(
      (route) =>
        route.incoming_path === ADVANCED_CUSTOM_MODEL_LIST_PATH &&
        route.converter === 'none'
    )
  )
}

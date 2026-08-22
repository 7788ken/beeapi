// Field names match backend `model/sensitive_word.go` / `model/sensitive_block_log.go`
// (introduced by patch 0010-sensitive-monitor.patch).

export interface SensitiveWord {
  id: number
  /** Backend column name is `pattern` (not `word`). */
  pattern: string
  description?: string
  is_regex: boolean
  /** 1 = block (record + freeze token), 2 = monitor only */
  action: number
  enabled: boolean
  hit_count: number
  /** Unix seconds; 0 means never hit. */
  last_hit_at: number
  created_at: number
  updated_at: number
}

export interface SensitiveBlockLog {
  id: number
  request_id?: string
  user_id: number
  username?: string
  token_id: number
  token_name?: string
  channel_id?: number
  channel_name?: string
  model_name?: string
  /** API path that triggered the hit. */
  path?: string
  matched_word_id: number
  matched_pattern: string
  is_regex: boolean
  action: number
  /** Short snippet from the request body around the match. */
  matched_snippet?: string
  /** Deprecated; new pipeline does not write this column anymore. */
  request_body?: string
  dump_path?: string
  body_sha256?: string
  body_size?: number
  dump_exists?: boolean
  ip?: string
  user_agent?: string
  /** True when the associated token has been disabled (matches block action). */
  token_disabled: boolean
  /** Unix seconds when the hit occurred. */
  created_at: number
}

export interface SensitiveAuditStats {
  enqueued: number
  processed: number
  dropped: number
  failed: number
  queue_depth: number
  queue_cap: number
  suspicious_users: number
  async_enabled: boolean
  sample_rate: number
  dump_to_file: boolean
  retention_days: number
  disk_guard_pct: number
}

/**
 * Backend `/api/option/` returns `data: [{ key, value }, ...]`.
 * Components convert it to a plain map by key for lookup.
 */
export interface OptionEntry {
  key: string
  value: string
}

export type SensitiveOptionMap = Record<string, string>

/** Option keys our patch 0010 backend defines (see setting/sensitive.go). */
export const SENSITIVE_OPTION_KEYS = {
  ASYNC_ENABLED: 'SensitiveAsyncEnabled',
  DUMP_TO_FILE: 'SensitiveDumpToFile',
  SAMPLE_RATE: 'SensitiveSampleRate', // integer percent 0-100
  DUMP_RETENTION_DAYS: 'SensitiveDumpRetentionDays',
  DUMP_DISK_GUARD: 'SensitiveDumpDiskGuardPercent',
} as const

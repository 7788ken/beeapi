import { ROLE } from '@/lib/roles'

/**
 * 管理员细粒度权限。key 与后端 model/user_admin_perm.go 中的常量一一对应。
 * 只对管理员生效：超级管理员恒为全部模块权限，普通用户恒无。
 */
export const ADMIN_PERM = {
  CHANNEL_VIEW: 'channel.view',
  CHANNEL_EDIT: 'channel.edit',
  CHANNEL_METRICS: 'channel.metrics',
  MODEL_VIEW: 'model.view',
  REDEMPTION_MANAGE: 'redemption.manage',
  SUBSCRIPTION_MANAGE: 'subscription.manage',
  LOG_VIEW: 'log.view',
  QUOTA_GRANT: 'quota.grant',
  USER_MANAGE: 'user.manage',
  QUOTA_DEDUCT_SELF: 'quota.deduct_self',
  CONTENT_BACKUP_VIEW: 'content_backup.view',
  CONTENT_BACKUP_MANAGE: 'content_backup.manage',
} as const

export type AdminPermKey = (typeof ADMIN_PERM)[keyof typeof ADMIN_PERM]

/** 权限弹窗的展示顺序、文案与说明（值为 i18n key） */
export const ADMIN_PERM_ITEMS: {
  key: AdminPermKey
  labelKey: string
  descKey: string
}[] = [
  {
    key: ADMIN_PERM.CHANNEL_VIEW,
    labelKey: 'View channels',
    descKey: 'Access the channel management page',
  },
  {
    key: ADMIN_PERM.CHANNEL_EDIT,
    labelKey: 'Create and edit channels',
    descKey:
      'Create, edit, delete, copy and batch-modify channels. Off by default: an admin who can edit a channel can point its base URL at their own server and capture the upstream key.',
  },
  {
    key: ADMIN_PERM.CHANNEL_METRICS,
    labelKey: 'View channel ratios and scores',
    descKey:
      'Show upstream ratio and quality/verify score columns on the channels page. Off by default: ratios reveal upstream cost structure, so this must be granted explicitly.',
  },
  {
    key: ADMIN_PERM.MODEL_VIEW,
    labelKey: 'Use models',
    descKey:
      'Access the models page (model metadata and pricing). Off by default: pricing structure is business-sensitive.',
  },
  {
    key: ADMIN_PERM.REDEMPTION_MANAGE,
    labelKey: 'Use redemption codes',
    descKey:
      'Access the redemption codes page (create/edit/delete codes). Off by default: minting codes mints quota (money).',
  },
  {
    key: ADMIN_PERM.SUBSCRIPTION_MANAGE,
    labelKey: 'Use subscription management',
    descKey:
      'Access the subscription management page (plan configuration). Off by default; must be granted explicitly.',
  },
  {
    key: ADMIN_PERM.LOG_VIEW,
    labelKey: 'View logs',
    descKey: 'Access site-wide usage logs; otherwise only their own logs',
  },
  {
    key: ADMIN_PERM.QUOTA_GRANT,
    labelKey: 'Adjust Quota',
    descKey:
      'Add quota to regular users only; never subtract, zero out or override',
  },
  {
    key: ADMIN_PERM.USER_MANAGE,
    labelKey: 'Manage Users',
    descKey: 'Create, edit, enable/disable and delete users',
  },
  {
    key: ADMIN_PERM.QUOTA_DEDUCT_SELF,
    labelKey: 'Deduct own quota on top-up',
    descKey: 'Adding quota to a user is taken out of this admin own balance',
  },
  {
    key: ADMIN_PERM.CONTENT_BACKUP_VIEW,
    labelKey: 'View content backup',
    descKey:
      'Access the content backup pages (archives, queue, storage nodes). Content preview, download and configuration stay root-only.',
  },
  {
    key: ADMIN_PERM.CONTENT_BACKUP_MANAGE,
    labelKey: 'Manage content backup',
    descKey:
      'Retry failed content backup uploads, in addition to viewing archives, queue and storage nodes. Content preview, download and configuration stay root-only.',
  },
]

/**
 * 未配置过的管理员的默认权限。
 * ⚠️ 不含 CHANNEL_EDIT —— 建/改渠道必须超管逐个显式开（与后端 defaultAdminPerms 一致）。
 */
export const DEFAULT_ADMIN_PERMS: AdminPermKey[] = [
  ADMIN_PERM.CHANNEL_VIEW,
  ADMIN_PERM.LOG_VIEW,
  ADMIN_PERM.QUOTA_GRANT,
  ADMIN_PERM.USER_MANAGE,
]

export type AdminPermFlags = {
  channel_view: boolean
  channel_edit: boolean
  channel_metrics: boolean
  model_view: boolean
  redemption_manage: boolean
  subscription_manage: boolean
  log_view: boolean
  quota_grant: boolean
  user_manage: boolean
  quota_deduct_self: boolean
  content_backup_view: boolean
  content_backup_manage: boolean
}

const NO_PERMS: AdminPermFlags = {
  channel_view: false,
  channel_edit: false,
  channel_metrics: false,
  model_view: false,
  redemption_manage: false,
  subscription_manage: false,
  log_view: false,
  quota_grant: false,
  user_manage: false,
  quota_deduct_self: false,
  content_backup_view: false,
  content_backup_manage: false,
}

/**
 * 把后端返回的权限列表摊平成布尔字段。
 * 后端 /api/user/self 已经算好 permissions.admin，这里只在缺字段时兜底
 * （老 localStorage 缓存的 user 对象没有该字段），避免管理员看到空侧边栏。
 */
export function resolveAdminPermFlags(
  role: number | undefined,
  flags: Partial<AdminPermFlags> | undefined,
  permList: string[] | undefined
): AdminPermFlags {
  if ((role ?? 0) < ROLE.ADMIN) return NO_PERMS
  if ((role ?? 0) >= ROLE.SUPER_ADMIN) {
    return {
      channel_view: true,
      channel_edit: true,
      channel_metrics: true,
      model_view: true,
      redemption_manage: true,
      subscription_manage: true,
      log_view: true,
      quota_grant: true,
      user_manage: true,
      quota_deduct_self: false,
      content_backup_view: true,
      content_backup_manage: true,
    }
  }
  if (flags) {
    return { ...NO_PERMS, ...flags }
  }
  const list = permList ?? DEFAULT_ADMIN_PERMS
  return {
    channel_view: list.includes(ADMIN_PERM.CHANNEL_VIEW),
    channel_edit: list.includes(ADMIN_PERM.CHANNEL_EDIT),
    channel_metrics: list.includes(ADMIN_PERM.CHANNEL_METRICS),
    model_view: list.includes(ADMIN_PERM.MODEL_VIEW),
    redemption_manage: list.includes(ADMIN_PERM.REDEMPTION_MANAGE),
    subscription_manage: list.includes(ADMIN_PERM.SUBSCRIPTION_MANAGE),
    log_view: list.includes(ADMIN_PERM.LOG_VIEW),
    quota_grant: list.includes(ADMIN_PERM.QUOTA_GRANT),
    user_manage: list.includes(ADMIN_PERM.USER_MANAGE),
    quota_deduct_self: list.includes(ADMIN_PERM.QUOTA_DEDUCT_SELF),
    content_backup_view: list.includes(ADMIN_PERM.CONTENT_BACKUP_VIEW),
    content_backup_manage: list.includes(ADMIN_PERM.CONTENT_BACKUP_MANAGE),
  }
}

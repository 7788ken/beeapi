import { ROLE } from '@/lib/roles'

/**
 * 管理员细粒度权限。key 与后端 model/user_admin_perm.go 中的常量一一对应。
 * 只对管理员生效：超级管理员恒为全部模块权限，普通用户恒无。
 */
export const ADMIN_PERM = {
  CHANNEL_VIEW: 'channel.view',
  CHANNEL_EDIT: 'channel.edit',
  LOG_VIEW: 'log.view',
  QUOTA_GRANT: 'quota.grant',
  USER_MANAGE: 'user.manage',
  QUOTA_DEDUCT_SELF: 'quota.deduct_self',
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
  log_view: boolean
  quota_grant: boolean
  user_manage: boolean
  quota_deduct_self: boolean
}

const NO_PERMS: AdminPermFlags = {
  channel_view: false,
  channel_edit: false,
  log_view: false,
  quota_grant: false,
  user_manage: false,
  quota_deduct_self: false,
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
      log_view: true,
      quota_grant: true,
      user_manage: true,
      quota_deduct_self: false,
    }
  }
  if (flags) {
    return { ...NO_PERMS, ...flags }
  }
  const list = permList ?? DEFAULT_ADMIN_PERMS
  return {
    channel_view: list.includes(ADMIN_PERM.CHANNEL_VIEW),
    channel_edit: list.includes(ADMIN_PERM.CHANNEL_EDIT),
    log_view: list.includes(ADMIN_PERM.LOG_VIEW),
    quota_grant: list.includes(ADMIN_PERM.QUOTA_GRANT),
    user_manage: list.includes(ADMIN_PERM.USER_MANAGE),
    quota_deduct_self: list.includes(ADMIN_PERM.QUOTA_DEDUCT_SELF),
  }
}

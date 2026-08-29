package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// 管理员细粒度权限。仅对 role = RoleAdminUser 有意义：
//   - root（超级管理员）恒有全部「模块权限」，不需要也不受配置约束；
//   - 普通用户恒无任何权限。
//
// 落库在 users.admin_perms，逗号分隔：
//
//	""                        未配置 —— 按 defaultAdminPerms 处理，等价于本功能上线前的管理员能力
//	"none"                    超级管理员显式收走了全部权限
//	"channel.view,log.view"   显式授予的子集
//
// 用 "none" 而不是空串表示「显式无权限」，是为了让存量管理员在升级后不掉权限，
// 同时又不需要一次性数据回填（多节点同时启动时回填会和 root 的配置抢写）。
const (
	// AdminPermChannelView 渠道管理入口（/api/channel/*）；只读 + 诊断（测试、拉余额、重算、探测更新）
	AdminPermChannelView = "channel.view"
	// AdminPermChannelEdit 新建/修改渠道，以及一切会改渠道配置的写操作（删除、复制、批量、
	// 按 tag 编辑、多 key 管理、应用上游模型更新、Codex 凭据、Ollama 拉删、限流规则、恢复健康）。
	// 默认关：管理员改得动渠道就能把 base_url 指到自己的机器把上游 key 骗出去，
	// 所以这一项必须由超级管理员逐个显式开。
	AdminPermChannelEdit = "channel.edit"
	// AdminPermLogView 全站日志入口（/api/log 管理端；关闭后只能看自己的日志）
	AdminPermLogView = "log.view"
	// AdminPermQuotaGrant 给普通用户增加额度。非 root 只能「增加」，不能扣减/覆盖
	AdminPermQuotaGrant = "quota.grant"
	// AdminPermUserManage 用户管理（新建/编辑/启停/删除/解绑/重置等）
	AdminPermUserManage = "user.manage"
	// AdminPermQuotaDeductSelf 给普通用户增加额度时，从操作者自己的额度里扣（不足则拒绝）。
	// 这是计费行为开关，不是访问权限，因此 root 不豁免。
	AdminPermQuotaDeductSelf = "quota.deduct_self"

	// AdminPermsNone 显式无任何权限，用于和「未配置」区分
	AdminPermsNone = "none"
)

// AllAdminPerms 全部可配置项，顺序即前端展示顺序
var AllAdminPerms = []string{
	AdminPermChannelView,
	AdminPermChannelEdit,
	AdminPermLogView,
	AdminPermQuotaGrant,
	AdminPermUserManage,
	AdminPermQuotaDeductSelf,
}

// defaultAdminPerms 未配置时的默认权限。
// ⚠️ 唯一一处「默认值不等于上线前行为」的地方：AdminPermChannelEdit 不在内。
// 上线前任何管理员都能建/改渠道，而改渠道 = 能把 base_url 指到自己机器把上游 key 骗走，
// 所以按用户要求收成默认关，须由超级管理员逐个显式开。其余四项仍等于上线前行为。
// 同样不含 AdminPermQuotaDeductSelf（计费开关，默认关）。
var defaultAdminPerms = []string{
	AdminPermChannelView,
	AdminPermLogView,
	AdminPermQuotaGrant,
	AdminPermUserManage,
}

// rootAdminPerms root 恒有的全部模块权限（含渠道写；不含扣自己额度这个计费开关）
var rootAdminPerms = []string{
	AdminPermChannelView,
	AdminPermChannelEdit,
	AdminPermLogView,
	AdminPermQuotaGrant,
	AdminPermUserManage,
}

func IsValidAdminPerm(perm string) bool {
	for _, p := range AllAdminPerms {
		if p == perm {
			return true
		}
	}
	return false
}

// NormalizeAdminPerms 把前端提交的权限列表规范化为落库字符串。
// 去重、按 AllAdminPerms 固定顺序排列；空列表落为 AdminPermsNone。
func NormalizeAdminPerms(perms []string) (string, error) {
	granted := make(map[string]bool, len(perms))
	for _, p := range perms {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !IsValidAdminPerm(p) {
			return "", fmt.Errorf("unknown admin permission: %s", p)
		}
		granted[p] = true
	}
	ordered := make([]string, 0, len(granted))
	for _, p := range AllAdminPerms {
		if granted[p] {
			ordered = append(ordered, p)
		}
	}
	if len(ordered) == 0 {
		return AdminPermsNone, nil
	}
	return strings.Join(ordered, ","), nil
}

// parseAdminPerms 解析落库字符串；不认识的 key 直接丢弃
func parseAdminPerms(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return append([]string(nil), defaultAdminPerms...)
	}
	if raw == AdminPermsNone {
		// 返回空切片而不是 nil：这个值会直接进 JSON，null 和 [] 在前端不是一回事
		return []string{}
	}
	perms := make([]string, 0, len(AllAdminPerms))
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if IsValidAdminPerm(p) {
			perms = append(perms, p)
		}
	}
	return perms
}

// EffectiveAdminPerms 返回某个角色 + 存储值下实际生效的权限列表。
// root 恒为全部模块权限且恒不带 AdminPermQuotaDeductSelf（超级管理员充值不扣自己）；
// 普通用户恒为空。
func EffectiveAdminPerms(role int, raw string) []string {
	if role < common.RoleAdminUser {
		return []string{}
	}
	if role >= common.RoleRootUser {
		return append([]string(nil), rootAdminPerms...)
	}
	return parseAdminPerms(raw)
}

// HasAdminPermFor 判定某个角色 + 存储值是否拥有指定权限
func HasAdminPermFor(role int, raw string, perm string) bool {
	for _, p := range EffectiveAdminPerms(role, raw) {
		if p == perm {
			return true
		}
	}
	return false
}

func (user *User) EffectiveAdminPerms() []string {
	return EffectiveAdminPerms(user.Role, user.AdminPerms)
}

func (user *User) HasAdminPerm(perm string) bool {
	return HasAdminPermFor(user.Role, user.AdminPerms, perm)
}

// GetUserAdminPermsRaw 现读一次库拿权限串。
// 刻意不走用户缓存：权限收回必须立刻生效，而管理端接口本身是低频调用。
// 角色不从这里取——鉴权中间件已经算过一次，避免同一请求里出现两个角色来源。
func GetUserAdminPermsRaw(userId int) (string, error) {
	if userId <= 0 {
		return "", fmt.Errorf("invalid user id")
	}
	var raw string
	if err := DB.Model(&User{}).
		Select("admin_perms").
		Where("id = ?", userId).
		Take(&raw).Error; err != nil {
		return "", err
	}
	return raw, nil
}

// UserHasAnyAdminPerm 用鉴权层给出的 role + 库里现读的权限串判定，命中任意一个即放行
func UserHasAnyAdminPerm(userId int, role int, perms ...string) (bool, error) {
	raw, err := GetUserAdminPermsRaw(userId)
	if err != nil {
		return false, err
	}
	for _, perm := range perms {
		if HasAdminPermFor(role, raw, perm) {
			return true, nil
		}
	}
	return false, nil
}

// UpdateUserAdminPerms 仅供 root 调用，写入后 bump auth version 让缓存失效
func UpdateUserAdminPerms(userId int, raw string) error {
	result := DB.Model(&User{}).Where("id = ?", userId).Update("admin_perms", raw)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("update admin perms: user %d not found", userId)
	}
	return nil
}

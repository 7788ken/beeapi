package setting

import (
	"encoding/json"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// UserUsableGroupEntry 描述产品分组在 admin 后台的配置。
//
//   - Description: 展示给用户/管理员看的友好描述
//   - UserSelectable: 用户在创建 API Key 时是否可以选到这个分组；
//     false 表示"管理员专用"——只在 admin 后台和后端逻辑里出现，
//     /api/group/user 接口不会暴露
type UserUsableGroupEntry struct {
	Description    string `json:"description"`
	UserSelectable bool   `json:"user_selectable"`
}

var userUsableGroups = map[string]UserUsableGroupEntry{
	"default": {Description: "默认分组", UserSelectable: true},
	"vip":     {Description: "vip分组", UserSelectable: true},
}
var userUsableGroupsMutex sync.RWMutex

// GetUserUsableGroupsCopy 返回**对用户可选**的分组映射 name→description。
// 调用方（service/group.go）拿到的应当是"用户能看到的产品"，所以这里直接过滤
// UserSelectable=false 的条目。管理员后台读取整张表请走 GetAllUserUsableGroupsCopy。
func GetUserUsableGroupsCopy() map[string]string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	copyUserUsableGroups := make(map[string]string)
	for k, v := range userUsableGroups {
		if !v.UserSelectable {
			continue
		}
		copyUserUsableGroups[k] = v.Description
	}
	return copyUserUsableGroups
}

// GetAllUserUsableGroupsCopy 返回完整产品分组配置（含 UserSelectable=false 的条目），
// 主要给 admin 后台 / 内部审计使用。
func GetAllUserUsableGroupsCopy() map[string]UserUsableGroupEntry {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	copyAll := make(map[string]UserUsableGroupEntry, len(userUsableGroups))
	for k, v := range userUsableGroups {
		copyAll[k] = v
	}
	return copyAll
}

// UserUsableGroups2JSONString 序列化为新版对象格式。
// admin UI 拿到的是带 description / user_selectable 的完整结构。
func UserUsableGroups2JSONString() string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	jsonBytes, err := json.Marshal(userUsableGroups)
	if err != nil {
		common.SysLog("error marshalling user groups: " + err.Error())
	}
	return string(jsonBytes)
}

// UpdateUserUsableGroupsByJSONString 支持两种 JSON 入参格式：
//
//  1. 旧格式 {"default":"默认分组"}：value 是字符串，
//     自动解释成 {Description: value, UserSelectable: true}
//  2. 新格式 {"default":{"description":"默认分组","user_selectable":true}}：
//     直接解析
//
// 兼容设计保证升级时旧数据不丢、admin 重新保存后自动迁移到新格式。
// OnUserUsableGroupsChanged 分组可见性变更回调，由 service 侧注册（setting 不能反向依赖 service）。
// 用于让按「可见分组」缓存的用户侧数据在管理员改分组集合 / user_selectable 后立即失效。
var OnUserUsableGroupsChanged func()

func UpdateUserUsableGroupsByJSONString(jsonStr string) error {
	if jsonStr == "" {
		userUsableGroupsMutex.Lock()
		userUsableGroups = map[string]UserUsableGroupEntry{}
		userUsableGroupsMutex.Unlock()
		if OnUserUsableGroupsChanged != nil {
			OnUserUsableGroupsChanged()
		}
		return nil
	}

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &rawMap); err != nil {
		return err
	}

	parsed := make(map[string]UserUsableGroupEntry, len(rawMap))
	for name, raw := range rawMap {
		// 尝试新格式对象
		var entry UserUsableGroupEntry
		if err := json.Unmarshal(raw, &entry); err == nil && (entry.Description != "" || hasUserSelectableKey(raw)) {
			parsed[name] = entry
			continue
		}
		// 退回旧格式字符串
		var desc string
		if err := json.Unmarshal(raw, &desc); err != nil {
			return err
		}
		parsed[name] = UserUsableGroupEntry{
			Description:    desc,
			UserSelectable: true,
		}
	}

	userUsableGroupsMutex.Lock()
	userUsableGroups = parsed
	userUsableGroupsMutex.Unlock()
	if OnUserUsableGroupsChanged != nil {
		OnUserUsableGroupsChanged()
	}
	return nil
}

// hasUserSelectableKey 用来区分"对象但 description 为空"和"纯字符串"。
// 如果 JSON 是 {"user_selectable": false}（admin 故意空 description）也要按对象解。
func hasUserSelectableKey(raw json.RawMessage) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	_, ok := probe["user_selectable"]
	return ok
}

// GetUsableGroupDescription 返回分组的描述。不区分 UserSelectable——任何代码
// 想知道"这个分组叫啥"都返回友好描述。
func GetUsableGroupDescription(groupName string) string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	if entry, ok := userUsableGroups[groupName]; ok {
		return entry.Description
	}
	return groupName
}

// IsGroupUserSelectable 检查指定分组是否允许用户在 /api/group/user 看到。
// 未配置的分组按 false 处理（保守策略）。
func IsGroupUserSelectable(groupName string) bool {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	entry, ok := userUsableGroups[groupName]
	if !ok {
		return false
	}
	return entry.UserSelectable
}

// IsGroupExplicitlyHidden 检查分组是否被管理员**显式**设为用户不可见
// （已在 UserUsableGroups 注册且 user_selectable=false）。
// 与 IsGroupUserSelectable 的区别：未注册的分组返回 false 而非 true——
// 用户专属分组、特殊规则分组往往不在注册表里，它们不应被当成"已隐藏"。
func IsGroupExplicitlyHidden(groupName string) bool {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	entry, ok := userUsableGroups[groupName]
	return ok && !entry.UserSelectable
}

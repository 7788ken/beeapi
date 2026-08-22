package model

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Ability struct {
	Group     string  `json:"group" gorm:"type:varchar(191);primaryKey;autoIncrement:false"`
	Model     string  `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int     `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool    `json:"enabled"`
	Priority  *int64  `json:"priority" gorm:"bigint;default:0;index"`
	Weight    uint    `json:"weight" gorm:"default:0;index"`
	Tag       *string `json:"tag" gorm:"index"`
}

type AbilityWithChannel struct {
	Ability
	ChannelType int `json:"channel_type"`
}

func GetAllEnableAbilityWithChannels() ([]AbilityWithChannel, error) {
	var abilities []AbilityWithChannel
	err := DB.Table("abilities").
		Select("abilities.*, channels.type as channel_type").
		Joins("left join channels on abilities.channel_id = channels.id").
		Where("abilities.enabled = ?", true).
		Scan(&abilities).Error
	return abilities, err
}

func GetGroupEnabledModels(group string) []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where(commonGroupCol+" = ? and enabled = ?", group, true).Distinct("model").Pluck("model", &models)
	return models
}

func GetEnabledModels() []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where("enabled = ?", true).Distinct("model").Pluck("model", &models)
	return models
}

type GroupChannelPair struct {
	Group     string `gorm:"column:group_name"`
	ChannelId int    `gorm:"column:channel_id"`
	Enabled   bool   `gorm:"column:enabled"`
}

// GetGroupChannelPairs 返回 (分组, 渠道, 是否启用) 去重映射，供分组可用率按渠道日志汇总时归属分组。
// abilities 以 (group, model, channel_id) 为主键，同一渠道在一个分组下有多个模型行，这里按分组+渠道去重；
// 渠道被禁用（手动或自动）时 abilities 行不删除、只把 enabled 置 0（见 UpdateAbilityStatus /
// UpdateAbilityStatusByTag），Enabled 即"该渠道当前在该分组内可被路由到"。
//
// 全量返回而不是只取 enabled=1：启停维度的历史归属不随渠道当前启停改写——按当前状态过滤时，
// 被恢复探活反复禁停/启用的翻转渠道会让分组曲线整窗抖动。当前是否可路由由调用方按 Enabled
// 区分，口径详见 service/group_uptime.go。
// ⚠ abilities 是当前映射表不是历史表：渠道改分组会删旧行重建、删渠道会删行，这两种操作下
// 历史样本的归属仍会被追溯改写；本函数只保证"启停不改写归属"。
// 同一 (分组, 渠道) 若出现 enabled 混排（仅人工改库可造成），会返回两行，调用方按"任一行启用即启用"合并。
func GetGroupChannelPairs(ctx context.Context) ([]GroupChannelPair, error) {
	var pairs []GroupChannelPair
	if err := DB.WithContext(ctx).Table("abilities").
		Select("DISTINCT " + commonGroupCol + " AS group_name, channel_id, enabled").
		Scan(&pairs).Error; err != nil {
		return nil, err
	}
	return pairs, nil
}

func GetAllEnableAbilities() []Ability {
	var abilities []Ability
	DB.Find(&abilities, "enabled = ?", true)
	return abilities
}

func getPriority(group string, model string, retry int) (int, error) {

	var priorities []int
	err := DB.Model(&Ability{}).
		Select("DISTINCT(priority)").
		Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true).
		Order("priority DESC").              // 按优先级降序排序
		Pluck("priority", &priorities).Error // Pluck用于将查询的结果直接扫描到一个切片中

	if err != nil {
		// 处理错误
		return 0, err
	}

	if len(priorities) == 0 {
		// 如果没有查询到优先级，则返回错误
		return 0, errors.New("数据库一致性被破坏")
	}

	// 确定要使用的优先级
	var priorityToUse int
	if retry >= len(priorities) {
		// 如果重试次数大于优先级数，则使用最小的优先级
		priorityToUse = priorities[len(priorities)-1]
	} else {
		priorityToUse = priorities[retry]
	}
	return priorityToUse, nil
}

func getChannelQuery(group string, model string, retry int) (*gorm.DB, error) {
	maxPrioritySubQuery := DB.Model(&Ability{}).Select("MAX(priority)").Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true)
	channelQuery := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and priority = (?)", group, model, true, maxPrioritySubQuery)
	if retry != 0 {
		priority, err := getPriority(group, model, retry)
		if err != nil {
			return nil, err
		} else {
			channelQuery = DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and priority = ?", group, model, true, priority)
		}
	}

	return channelQuery, nil
}

func GetChannel(group string, model string, retry int, excludedIDs []int, cacheDomainFilter string) (*Channel, error) {
	return GetChannelForRequest(group, model, retry, excludedIDs, cacheDomainFilter, "")
}

func GetChannelForRequest(group string, model string, retry int, excludedIDs []int, cacheDomainFilter string, requestPath string) (*Channel, error) {
	var abilities []Ability

	var err error = nil
	var channelQuery *gorm.DB
	if requestPath == "" {
		channelQuery, err = getChannelQuery(group, model, retry)
		if err != nil {
			return nil, err
		}
	} else {
		// Path-aware routing must filter all candidate priorities first. If the
		// highest-priority Advanced Custom channel does not support this path,
		// selection must continue at the next compatible priority, matching the
		// memory-cache path.
		channelQuery = DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true)
	}
	if common.UsingSQLite || common.UsingPostgreSQL {
		err = channelQuery.Order("weight DESC").Find(&abilities).Error
	} else {
		err = channelQuery.Order("weight DESC").Find(&abilities).Error
	}
	if err != nil {
		return nil, err
	}
	abilities, err = filterAbilitiesByRequestPathAndModel(abilities, requestPath, model)
	if err != nil {
		return nil, err
	}
	// same_domain：只保留与 origin 同 cache_domain 的渠道（不外溢到其它账号丢缓存）。
	// 域内无可用即 abilities 清空 → 后续返回 nil；不做 excluded 回退。
	if cacheDomainFilter != "" && len(abilities) > 0 {
		seen := make(map[int]bool, len(abilities))
		ids := make([]int, 0, len(abilities))
		for _, ab := range abilities {
			if !seen[ab.ChannelId] {
				seen[ab.ChannelId] = true
				ids = append(ids, ab.ChannelId)
			}
		}
		var chans []Channel
		if e := DB.Select("id", "cache_domain").Where("id IN ?", ids).Find(&chans).Error; e != nil {
			// 查不到候选渠道的缓存域时，宁可失败也不外溢到其它账号丢缓存
			return nil, e
		}
		domainOf := make(map[int]string, len(chans))
		for i := range chans {
			domainOf[chans[i].Id] = chans[i].GetCacheDomain()
		}
		filtered := make([]Ability, 0, len(abilities))
		for _, ab := range abilities {
			if domainOf[ab.ChannelId] == cacheDomainFilter {
				filtered = append(filtered, ab)
			}
		}
		abilities = filtered
	}
	// 排除已失败渠道。注意：filtered 必须新建底层数组，不能用 abilities[:0]
	// 共享底层数组——否则 append 会就地覆盖 abilities，fallback 取不回原始集合。
	if len(excludedIDs) > 0 && len(abilities) > 0 {
		filtered := make([]Ability, 0, len(abilities))
		for _, ab := range abilities {
			if !isChannelExcluded(ab.ChannelId, excludedIDs) {
				filtered = append(filtered, ab)
			}
		}
		// FALLBACK：全部被排除 → 保留原始 abilities 让 retry 继续；瞬时错误可能已恢复，
		// 宁可重试坏点也不要"无可用渠道"瞬间失败（RetryTimes 已限制总次数）。
		// same_domain（cacheDomainFilter != ""）例外：与内存缓存路径一致，域内候选全部失败即
		// fail-fast 返回 nil，由 relay 层把上一轮真实上游错误返回客户端，不回退重试坏点。
		if len(filtered) == 0 {
			if cacheDomainFilter != "" {
				abilities = nil
			} else {
				common.SysLog(fmt.Sprintf("retry (db path): all %d abilities excluded for group %s model %s, fallback to retry full candidate set", len(abilities), group, model))
				// abilities 保持不变（原始集合）
			}
		} else {
			abilities = filtered
		}
	}
	if requestPath != "" {
		abilities = filterAbilitiesByRetryPriority(abilities, retry)
	}
	channel := Channel{}
	if len(abilities) > 0 {
		// Randomly choose one
		weightSum := uint(0)
		for _, ability_ := range abilities {
			weightSum += ability_.Weight + 10
		}
		// Randomly choose one
		weight := common.GetRandomInt(int(weightSum))
		for _, ability_ := range abilities {
			weight -= int(ability_.Weight) + 10
			//log.Printf("weight: %d, ability weight: %d", weight, *ability_.Weight)
			if weight <= 0 {
				channel.Id = ability_.ChannelId
				break
			}
		}
	} else {
		return nil, nil
	}
	err = DB.First(&channel, "id = ?", channel.Id).Error
	return &channel, err
}

func filterAbilitiesByRetryPriority(abilities []Ability, retry int) []Ability {
	if len(abilities) == 0 {
		return abilities
	}
	prioritySet := make(map[int64]struct{}, len(abilities))
	for _, ability := range abilities {
		prioritySet[common.DerefInt64Or(ability.Priority, 0)] = struct{}{}
	}
	priorities := make([]int64, 0, len(prioritySet))
	for priority := range prioritySet {
		priorities = append(priorities, priority)
	}
	sort.Slice(priorities, func(i, j int) bool { return priorities[i] > priorities[j] })
	if retry >= len(priorities) {
		retry = len(priorities) - 1
	}
	if retry < 0 {
		retry = 0
	}
	targetPriority := priorities[retry]
	filtered := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		if common.DerefInt64Or(ability.Priority, 0) == targetPriority {
			filtered = append(filtered, ability)
		}
	}
	return filtered
}

func filterAbilitiesByRequestPathAndModel(abilities []Ability, requestPath string, modelName string) ([]Ability, error) {
	if requestPath == "" || len(abilities) == 0 {
		return abilities, nil
	}
	channelIDs := make([]int, 0, len(abilities))
	seen := make(map[int]struct{}, len(abilities))
	for _, ability := range abilities {
		if _, ok := seen[ability.ChannelId]; ok {
			continue
		}
		seen[ability.ChannelId] = struct{}{}
		channelIDs = append(channelIDs, ability.ChannelId)
	}
	var channels []Channel
	if err := DB.Select("id", "type", "settings").Where("id IN ?", channelIDs).Find(&channels).Error; err != nil {
		return nil, err
	}
	advancedConfigs := make(map[int]*dto.AdvancedCustomConfig)
	for i := range channels {
		channel := &channels[i]
		if channel.Type != constant.ChannelTypeAdvancedCustom {
			continue
		}
		var settings dto.ChannelOtherSettings
		if err := common.UnmarshalJsonStr(channel.OtherSettings, &settings); err != nil ||
			settings.AdvancedCustom == nil {
			advancedConfigs[channel.Id] = nil
			continue
		}
		advancedConfigs[channel.Id] = settings.AdvancedCustom
	}
	filtered := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		config, isAdvancedCustom := advancedConfigs[ability.ChannelId]
		if !isAdvancedCustom || (config != nil && config.SupportsPathForModel(requestPath, modelName)) {
			filtered = append(filtered, ability)
		}
	}
	return filtered, nil
}

func (channel *Channel) AddAbilities(tx *gorm.DB) error {
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}
	if len(abilities) == 0 {
		return nil
	}
	// choose DB or provided tx
	useDB := DB
	if tx != nil {
		useDB = tx
	}
	for _, chunk := range lo.Chunk(abilities, 50) {
		err := useDB.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) DeleteAbilities() error {
	return deleteChannelAbilities(DB, []int{channel.Id})
}

func deleteChannelAbilities(tx *gorm.DB, channelIds []int) error {
	for _, chunk := range lo.Chunk(channelIds, 200) {
		if err := tx.Where("channel_id IN (?)", chunk).Delete(&Ability{}).Error; err != nil {
			return err
		}
	}
	return nil
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities(tx *gorm.DB) error {
	isNewTx := false
	// 如果没有传入事务，创建新的事务
	if tx == nil {
		tx = DB.Begin()
		if tx.Error != nil {
			return tx.Error
		}
		isNewTx = true
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()
	}

	// First delete all abilities of this channel
	err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
	if err != nil {
		if isNewTx {
			tx.Rollback()
		}
		return err
	}

	// Then add new abilities
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}

	if len(abilities) > 0 {
		for _, chunk := range lo.Chunk(abilities, 50) {
			err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
			if err != nil {
				if isNewTx {
					tx.Rollback()
				}
				return err
			}
		}
	}

	// 如果是新创建的事务，需要提交
	if isNewTx {
		return tx.Commit().Error
	}

	return nil
}

func UpdateAbilityStatus(channelId int, status bool) error {
	return DB.Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityStatusByTag(tag string, status bool) error {
	return DB.Model(&Ability{}).Where("tag = ?", tag).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityByTag(tag string, newTag *string, priority *int64, weight *uint) error {
	ability := Ability{}
	if newTag != nil {
		ability.Tag = newTag
	}
	if priority != nil {
		ability.Priority = priority
	}
	if weight != nil {
		ability.Weight = *weight
	}
	return DB.Model(&Ability{}).Where("tag = ?", tag).Updates(ability).Error
}

var fixLock = sync.Mutex{}

func FixAbility() (int, int, error) {
	lock := fixLock.TryLock()
	if !lock {
		return 0, 0, errors.New("已经有一个修复任务在运行中，请稍后再试")
	}
	defer fixLock.Unlock()

	// truncate abilities table
	if common.UsingSQLite {
		err := DB.Exec("DELETE FROM abilities").Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Delete abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	} else {
		err := DB.Exec("TRUNCATE TABLE abilities").Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Truncate abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	}
	var channels []*Channel
	// Find all channels
	err := DB.Model(&Channel{}).Find(&channels).Error
	if err != nil {
		return 0, 0, err
	}
	if len(channels) == 0 {
		return 0, 0, nil
	}
	successCount := 0
	failCount := 0
	for _, chunk := range lo.Chunk(channels, 50) {
		ids := lo.Map(chunk, func(c *Channel, _ int) int { return c.Id })
		// Delete all abilities of this channel
		err = DB.Where("channel_id IN ?", ids).Delete(&Ability{}).Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Delete abilities failed: %s", err.Error()))
			failCount += len(chunk)
			continue
		}
		// Then add new abilities
		for _, channel := range chunk {
			err = channel.AddAbilities(nil)
			if err != nil {
				common.SysLog(fmt.Sprintf("Add abilities for channel %d failed: %s", channel.Id, err.Error()))
				failCount++
			} else {
				successCount++
			}
		}
	}
	InitChannelCache()
	return successCount, failCount, nil
}

package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

type Token struct {
	Id                 int            `json:"id"`
	UserId             int            `json:"user_id" gorm:"index"`
	Key                string         `json:"key" gorm:"type:varchar(128);uniqueIndex"`
	Status             int            `json:"status" gorm:"default:1"`
	Name               string         `json:"name" gorm:"index" `
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	AccessedTime       int64          `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64          `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota        int            `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota     bool           `json:"unlimited_quota"`
	ModelLimitsEnabled bool           `json:"model_limits_enabled"`
	ModelLimits        string         `json:"model_limits" gorm:"type:text"`
	AllowIps           *string        `json:"allow_ips" gorm:"default:''"`
	UsedQuota          int            `json:"used_quota" gorm:"default:0"` // used quota
	Group              string         `json:"group" gorm:"default:''"`
	CrossGroupRetry    bool           `json:"cross_group_retry"` // 跨分组重试，仅auto分组有效
	RelayRetryPolicy   string         `json:"relay_retry_policy" gorm:"type:varchar(32);default:'system'"`
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

const (
	TokenRelayRetryPolicySystem            = "system"
	TokenRelayRetryPolicyDisabled          = "disabled"
	TokenRelayRetryPolicyCacheDomainOnly   = "cache_domain_only"
	TokenRelayRetryPolicyAllowCrossChannel = "allow_cross_channel"
)

func NormalizeTokenRelayRetryPolicy(policy string) string {
	switch strings.TrimSpace(policy) {
	case TokenRelayRetryPolicyDisabled,
		TokenRelayRetryPolicyCacheDomainOnly,
		TokenRelayRetryPolicyAllowCrossChannel:
		return strings.TrimSpace(policy)
	default:
		return TokenRelayRetryPolicySystem
	}
}

func (token *Token) NormalizeRelayRetryPolicy() {
	token.RelayRetryPolicy = NormalizeTokenRelayRetryPolicy(token.RelayRetryPolicy)
}

func (token *Token) Clean() {
	token.Key = ""
}

func MaskTokenKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	if len(key) <= 8 {
		return key[:2] + "****" + key[len(key)-2:]
	}
	return key[:4] + "**********" + key[len(key)-4:]
}

func (token *Token) GetFullKey() string {
	return token.Key
}

func (token *Token) GetMaskedKey() string {
	return MaskTokenKey(token.Key)
}

func (token *Token) GetIpLimits() []string {
	// delete empty spaces
	//split with \n
	ipLimits := make([]string, 0)
	if token.AllowIps == nil {
		return ipLimits
	}
	cleanIps := strings.ReplaceAll(*token.AllowIps, " ", "")
	if cleanIps == "" {
		return ipLimits
	}
	ips := strings.Split(cleanIps, "\n")
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		ip = strings.ReplaceAll(ip, ",", "")
		if ip != "" {
			ipLimits = append(ipLimits, ip)
		}
	}
	return ipLimits
}

func GetAllUserTokens(userId int, startIdx int, num int) ([]*Token, error) {
	var tokens []*Token
	var err error
	err = DB.Where("user_id = ?", userId).Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, err
}

// sanitizeLikePattern 校验并清洗用户输入的 LIKE 搜索模式。
// 规则：
//  1. 转义 ! 和 _（使用 ! 作为 ESCAPE 字符，兼容 MySQL/PostgreSQL/SQLite）
//  2. 连续的 % 合并为单个 %
//  3. 最多允许 2 个 %
//  4. 含 % 时（模糊搜索），去掉 % 后关键词长度必须 >= 2
//  5. 不含 % 时按精确匹配
func sanitizeLikePattern(input string) (string, error) {
	// 1. 先转义 ESCAPE 字符 ! 自身，再转义 _
	//    使用 ! 而非 \ 作为 ESCAPE 字符，避免 MySQL 中反斜杠的字符串转义问题
	input = strings.ReplaceAll(input, "!", "!!")
	input = strings.ReplaceAll(input, `_`, `!_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}
	return input, nil
}

func validateLikePattern(input string) error {
	// 2. 连续的 % 直接拒绝
	if strings.Contains(input, "%%") {
		return errors.New("搜索模式中不允许包含连续的 % 通配符")
	}

	// 3. 统计 % 数量，不得超过 2
	count := strings.Count(input, "%")
	if count > 2 {
		return errors.New("搜索模式中最多允许包含 2 个 % 通配符")
	}

	// 4. 含 % 时，去掉 % 后关键词长度必须 >= 2
	if count > 0 {
		stripped := strings.ReplaceAll(input, "%", "")
		if len(stripped) < 2 {
			return errors.New("使用模糊搜索时，关键词长度至少为 2 个字符")
		}
	}
	return nil
}

const searchHardLimit = 100

func SearchUserTokens(userId int, keyword string, token string, offset int, limit int) (tokens []*Token, total int64, err error) {
	// model 层强制截断
	if limit <= 0 || limit > searchHardLimit {
		limit = searchHardLimit
	}
	if offset < 0 {
		offset = 0
	}

	if token != "" {
		token = strings.TrimPrefix(token, "sk-")
	}

	// 超量用户（令牌数超过上限）只允许精确搜索，禁止模糊搜索
	maxTokens := operation_setting.GetMaxUserTokens()
	hasFuzzy := strings.Contains(keyword, "%") || strings.Contains(token, "%")
	if hasFuzzy {
		count, err := CountUserTokens(userId)
		if err != nil {
			common.SysLog("failed to count user tokens: " + err.Error())
			return nil, 0, errors.New("获取令牌数量失败")
		}
		if int(count) > maxTokens {
			return nil, 0, errors.New("令牌数量超过上限，仅允许精确搜索，请勿使用 % 通配符")
		}
	}

	baseQuery := DB.Model(&Token{}).Where("user_id = ?", userId)

	// 非空才加 LIKE 条件，空则跳过（不过滤该字段）
	if keyword != "" {
		keywordPattern, err := sanitizeLikePattern(keyword)
		if err != nil {
			return nil, 0, err
		}
		baseQuery = baseQuery.Where("name LIKE ? ESCAPE '!'", keywordPattern)
	}
	if token != "" {
		tokenPattern, err := sanitizeLikePattern(token)
		if err != nil {
			return nil, 0, err
		}
		baseQuery = baseQuery.Where(commonKeyCol+" LIKE ? ESCAPE '!'", tokenPattern)
	}

	// 先查匹配总数（用于分页，受 maxTokens 上限保护，避免全表 COUNT）
	err = baseQuery.Limit(maxTokens).Count(&total).Error
	if err != nil {
		common.SysError("failed to count search tokens: " + err.Error())
		return nil, 0, errors.New("搜索令牌失败")
	}

	// 再分页查数据
	err = baseQuery.Order("id desc").Offset(offset).Limit(limit).Find(&tokens).Error
	if err != nil {
		common.SysError("failed to search tokens: " + err.Error())
		return nil, 0, errors.New("搜索令牌失败")
	}
	return tokens, total, nil
}

func ValidateUserToken(key string) (token *Token, err error) {
	if key == "" {
		return nil, ErrTokenNotProvided
	}
	token, err = GetTokenByKey(key, false)
	if err == nil {
		if token.Status == common.TokenStatusExhausted ||
			token.Status == common.TokenStatusExpired ||
			token.Status != common.TokenStatusEnabled {
			return token, ErrTokenInvalid
		}
		if token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp() {
			token.Status = common.TokenStatusExpired
			err := token.SelectUpdate()
			if err != nil {
				common.SysLog("failed to update token status" + err.Error())
			}
			return token, ErrTokenInvalid
		}
		if !token.UnlimitedQuota && token.RemainQuota <= 0 {
			token.Status = common.TokenStatusExhausted
			err := token.SelectUpdate()
			if err != nil {
				common.SysLog("failed to update token status" + err.Error())
			}
			return token, ErrTokenInvalid
		}
		return token, nil
	}
	common.SysLog("ValidateUserToken: failed to get token: " + err.Error())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTokenInvalid
	}
	return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
}

func GetTokenByIds(id int, userId int) (*Token, error) {
	if id == 0 || userId == 0 {
		return nil, errors.New("id 或 userId 为空！")
	}
	token := Token{Id: id, UserId: userId}
	var err error = nil
	err = DB.First(&token, "id = ? and user_id = ?", id, userId).Error
	return &token, err
}

func GetTokenById(id int) (*Token, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	token := Token{Id: id}
	var err error = nil
	err = DB.First(&token, "id = ?", id).Error
	return &token, err
}

func GetTokenByKey(key string, _ bool) (token *Token, err error) {
	err = DB.Where(commonKeyCol+" = ?", key).First(&token).Error
	return token, err
}

func (token *Token) Insert() error {
	token.NormalizeRelayRetryPolicy()
	var err error
	err = DB.Create(token).Error
	return err
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (token *Token) Update() (err error) {
	token.NormalizeRelayRetryPolicy()
	err = DB.Model(token).Select("name", "status", "expired_time", "remain_quota", "unlimited_quota",
		"model_limits_enabled", "model_limits", "allow_ips", "group", "cross_group_retry", "relay_retry_policy").Updates(token).Error
	return err
}

func (token *Token) SelectUpdate() (err error) {
	// This can update zero values
	return DB.Model(token).Select("accessed_time", "status").Updates(token).Error
}

func (token *Token) Delete() (err error) {
	if token.Id == 0 {
		return errors.New("id 为空！")
	}
	deletedToken, err := deleteTokenWithLedger(token.Id, 0)
	if err != nil {
		return err
	}
	token.Key = deletedToken.Key
	return nil
}

func (token *Token) IsModelLimitsEnabled() bool {
	return token.ModelLimitsEnabled
}

func (token *Token) GetModelLimits() []string {
	if token.ModelLimits == "" {
		return []string{}
	}
	return strings.Split(token.ModelLimits, ",")
}

func (token *Token) GetModelLimitsMap() map[string]bool {
	limits := token.GetModelLimits()
	limitsMap := make(map[string]bool)
	for _, limit := range limits {
		limitsMap[limit] = true
	}
	return limitsMap
}

func DisableModelLimits(tokenId int) error {
	token, err := GetTokenById(tokenId)
	if err != nil {
		return err
	}
	token.ModelLimitsEnabled = false
	token.ModelLimits = ""
	return token.Update()
}

func DeleteTokenById(id int, userId int) (err error) {
	// Why we need userId here? In case user want to delete other's token.
	if id == 0 || userId == 0 {
		return errors.New("id 或 userId 为空！")
	}
	_, err = deleteTokenWithLedger(id, userId)
	if err != nil {
		return err
	}
	return nil
}

func deleteTokenWithLedger(id int, userId int) (Token, error) {
	var token Token
	err := DB.Transaction(func(tx *gorm.DB) error {
		query := batchDeleteRowLock(tx).Where("id = ?", id)
		if userId != 0 {
			query = query.Where("user_id = ?", userId)
		}
		if err := query.First(&token).Error; err != nil {
			return err
		}
		if err := createBatchUpdateDeleteLedgers(tx, token.Id, BatchUpdateTypeTokenQuota); err != nil {
			return err
		}
		return requireSelectedDeleteRows(
			fmt.Sprintf("token id %d", token.Id),
			1,
			tx.Where("id = ?", token.Id).Delete(&Token{}),
		)
	})
	return token, err
}

func IncreaseTokenQuota(tokenId int, quota int) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return adjustTokenQuotaTx(tx, tokenId, 0, -quota)
	})
}

func DecreaseTokenQuota(id int, quota int) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return adjustTokenQuotaTx(tx, id, 0, quota)
	})
}

// TrustedSettleTokenQuota 信任旁路结算时补扣限额令牌的 remain/used。
// 批量模式下进聚合通道（flush 侧 remain_quota+delta/used_quota-delta，
// 消耗传负 delta），避免高频令牌行写；未启用批量时直接同步扣减。
func TrustedSettleTokenQuota(tokenId int, quota int) error {
	if tokenId <= 0 {
		return errors.New("token id must be positive")
	}
	if quota <= 0 {
		return errors.New("settle quota must be positive")
	}
	if common.BatchUpdateEnabled {
		if err := addNewRecord(BatchUpdateTypeTokenQuota, tokenId, -quota); err != nil {
			return recordBatchAdmissionError("trusted settle token quota", err)
		}
		return nil
	}
	return DecreaseTokenQuota(tokenId, quota)
}

func adjustTokenQuotaTx(tx *gorm.DB, id int, userID int, delta int) error {
	if id <= 0 {
		return errors.New("token id must be positive")
	}
	if delta == 0 {
		return nil
	}

	query := tx.Model(&Token{})
	if delta < 0 {
		// A token may be soft-deleted while an in-flight request is awaiting a
		// refund. Deletion must block new consumption, but not rollback of quota
		// already charged before the delete.
		query = query.Unscoped()
	}
	query = query.Where("id = ?", id)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}

	var updates map[string]interface{}
	if delta > 0 {
		query = query.Where("(unlimited_quota = ? OR remain_quota >= ?)", true, delta)
		updates = map[string]interface{}{
			"remain_quota":  gorm.Expr("CASE WHEN unlimited_quota = ? THEN remain_quota ELSE remain_quota - ? END", true, delta),
			"used_quota":    gorm.Expr("CASE WHEN unlimited_quota = ? THEN used_quota ELSE used_quota + ? END", true, delta),
			"accessed_time": common.GetTimestamp(),
		}
	} else {
		refund := -delta
		query = query.Where("(unlimited_quota = ? OR used_quota >= ?)", true, refund)
		updates = map[string]interface{}{
			"remain_quota":  gorm.Expr("CASE WHEN unlimited_quota = ? THEN remain_quota ELSE remain_quota + ? END", true, refund),
			"used_quota":    gorm.Expr("CASE WHEN unlimited_quota = ? THEN used_quota ELSE used_quota - ? END", true, refund),
			"accessed_time": common.GetTimestamp(),
		}
	}

	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return resolveZeroAffectedTokenQuotaUpdate(tx, id, userID, delta)
	}
	if result.RowsAffected != 1 {
		return ErrInsufficientTokenQuota
	}
	return nil
}

// MySQL reports changed rows rather than matched rows by default. An unlimited
// token keeps its quota counters unchanged, and accessed_time has second
// precision, so repeated adjustments in the same second can legitimately
// report zero affected rows. Re-read the locked row to distinguish that case
// from a missing, deleted, mismatched, or genuinely insufficient finite token.
func resolveZeroAffectedTokenQuotaUpdate(tx *gorm.DB, id int, userID int, delta int) error {
	query := withForUpdate(tx).Model(&Token{})
	if delta < 0 {
		query = query.Unscoped()
	}
	query = query.Where("id = ?", id)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}

	var token Token
	err := query.Select("id", "unlimited_quota").Take(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrInsufficientTokenQuota
	}
	if err != nil {
		return err
	}
	if !token.UnlimitedQuota {
		return ErrInsufficientTokenQuota
	}
	return nil
}

// CountUserTokens returns total number of tokens for the given user, used for pagination
func CountUserTokens(userId int) (int64, error) {
	var total int64
	err := DB.Model(&Token{}).Where("user_id = ?", userId).Count(&total).Error
	return total, err
}

// BatchDeleteTokens 删除指定用户的一组令牌，返回成功删除数量
func BatchDeleteTokens(ids []int, userId int) (int, error) {
	if len(ids) == 0 {
		return 0, errors.New("ids 不能为空！")
	}

	requestedIds := uniqueBatchDeleteIDs(ids)
	var tokens []Token
	err := DB.Transaction(func(tx *gorm.DB) error {
		for _, chunk := range batchDeleteIDChunks(requestedIds) {
			var selected []Token
			if err := batchDeleteRowLock(tx).
				Where("user_id = ? AND id IN (?)", userId, chunk).
				Order("id").
				Find(&selected).Error; err != nil {
				return err
			}
			tokens = append(tokens, selected...)
		}
		if len(tokens) == 0 {
			return nil
		}

		tokenIds := make([]int, 0, len(tokens))
		for _, token := range tokens {
			if err := createBatchUpdateDeleteLedgers(tx, token.Id, BatchUpdateTypeTokenQuota); err != nil {
				return err
			}
			tokenIds = append(tokenIds, token.Id)
		}
		for _, chunk := range batchDeleteIDChunks(tokenIds) {
			if err := requireSelectedDeleteRows(
				fmt.Sprintf("tokens for user %d", userId),
				len(chunk),
				tx.Where("user_id = ? AND id IN (?)", userId, chunk).Delete(&Token{}),
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	return len(tokens), nil
}

func GetTokenKeysByIds(ids []int, userId int) ([]Token, error) {
	var tokens []Token
	err := DB.Select("id", commonKeyCol).
		Where("user_id = ? AND id IN (?)", userId, ids).
		Find(&tokens).Error
	return tokens, err
}

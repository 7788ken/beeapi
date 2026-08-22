package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/pkg/billinglifecycle"

	"gorm.io/gorm"
)

const UserNameMaxLength = 20

// User if you add sensitive fields, don't forget to clean them in setupLogin function.
// Otherwise, the sensitive information will be saved on local storage in plain text!
type User struct {
	Id               int            `json:"id"`
	Username         string         `json:"username" gorm:"unique;index" validate:"max=20"`
	Password         string         `json:"password" gorm:"not null;" validate:"min=8,max=20"`
	OriginalPassword string         `json:"original_password" gorm:"-:all"` // this field is only for Password change verification, don't save it to database!
	DisplayName      string         `json:"display_name" gorm:"index" validate:"max=20"`
	Role             int            `json:"role" gorm:"type:int;default:1"`   // admin, common
	Status           int            `json:"status" gorm:"type:int;default:1"` // enabled, disabled
	Email            string         `json:"email" gorm:"index" validate:"max=50"`
	GitHubId         string         `json:"github_id" gorm:"column:github_id;index"`
	DiscordId        string         `json:"discord_id" gorm:"column:discord_id;index"`
	OidcId           string         `json:"oidc_id" gorm:"column:oidc_id;index"`
	WeChatId         string         `json:"wechat_id" gorm:"column:wechat_id;index"`
	TelegramId       string         `json:"telegram_id" gorm:"column:telegram_id;index"`
	VerificationCode string         `json:"verification_code" gorm:"-:all"`                                    // this field is only for Email verification, don't save it to database!
	AccessToken      *string        `json:"access_token" gorm:"type:char(32);column:access_token;uniqueIndex"` // this token is for system management
	Quota            int            `json:"quota" gorm:"type:int;default:0"`
	UsedQuota        int            `json:"used_quota" gorm:"type:int;default:0;column:used_quota"` // used quota
	RequestCount     int            `json:"request_count" gorm:"type:int;default:0;"`               // request number
	Group            string         `json:"group" gorm:"type:varchar(64);default:'♻️ default'"`
	AffCode          string         `json:"aff_code" gorm:"type:varchar(32);column:aff_code;uniqueIndex"`
	AffCount         int            `json:"aff_count" gorm:"type:int;default:0;column:aff_count"`
	AffQuota         int            `json:"aff_quota" gorm:"type:int;default:0;column:aff_quota"`           // 邀请剩余额度
	AffHistoryQuota  int            `json:"aff_history_quota" gorm:"type:int;default:0;column:aff_history"` // 邀请历史额度
	InviterId        int            `json:"inviter_id" gorm:"type:int;column:inviter_id;index"`
	// InviterRewardGranted 邀请奖励是否已结算/无需结算。默认 true（存量及无推荐人场景视为已处理）；
	// 仅当开启 RewardInviterOnEffectiveOnly 的新注册被推荐人会置 false，待登录+消费达标后发放并置回 true。
	InviterRewardGranted bool `json:"inviter_reward_granted" gorm:"default:true;column:inviter_reward_granted"`
	DeletedAt        gorm.DeletedAt `gorm:"index"`
	LinuxDOId        string         `json:"linux_do_id" gorm:"column:linux_do_id;index"`
	Setting          string         `json:"setting" gorm:"type:text;column:setting"`
	Remark           string         `json:"remark,omitempty" gorm:"type:varchar(255)" validate:"max=255"`
	RpmLimit         int            `json:"rpm_limit" gorm:"type:int;default:0;column:rpm_limit"`
	// 过去 24h 平均每分钟请求数；由 service.UserMetricsTask 每 5min 重算（docs/2026-05-12-channel-quality-rpm-list-plan.md）。
	Rpm24h           float64        `json:"rpm_24h" gorm:"column:rpm_24h;type:double precision;not null;default:0"`
	RpmUpdatedAt     int64          `json:"rpm_updated_at" gorm:"column:rpm_updated_at;type:bigint;not null;default:0"`
	Pinned           bool           `json:"pinned" gorm:"default:false;column:pinned"`
	StripeCustomer   string         `json:"stripe_customer" gorm:"type:varchar(64);column:stripe_customer;index"`
	CreatedAt        int64          `json:"created_at" gorm:"autoCreateTime;column:created_at"`
	LastLoginAt      int64          `json:"last_login_at" gorm:"default:0;column:last_login_at"`
	AuthVersion      int64          `json:"-" gorm:"type:bigint;not null;default:1;column:auth_version"`
	// 账号有效期，unix sec；0 = 永不过期（默认）。仅 admin 可设置；登录时若 ExpiresAt>0 且 < now 则拒绝。
	// 用于特殊场景给特定用户设上限（试用、临时账号等）。普通注册不写入。
	ExpiresAt        int64          `json:"expires_at" gorm:"default:0;column:expires_at"`
}

func (user *User) ToBaseUser() *UserBase {
	cache := &UserBase{
		Id:        user.Id,
		Group:     user.Group,
		Quota:     user.Quota,
		Status:    user.Status,
		Role:      user.Role,
		Username:  user.Username,
		Setting:   user.Setting,
		Email:     user.Email,
		RpmLimit:  user.RpmLimit,
		ExpiresAt: user.ExpiresAt,
	}
	return cache
}

func (user *User) GetAccessToken() string {
	if user.AccessToken == nil {
		return ""
	}
	return *user.AccessToken
}

func (user *User) SetAccessToken(token string) {
	user.AccessToken = &token
}

func (user *User) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := json.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

func (user *User) SetSetting(setting dto.UserSetting) {
	settingBytes, err := json.Marshal(setting)
	if err != nil {
		common.SysLog("failed to marshal setting: " + err.Error())
		return
	}
	user.Setting = string(settingBytes)
}

// 根据用户角色生成默认的边栏配置
func generateDefaultSidebarConfigForRole(userRole int) string {
	defaultConfig := map[string]interface{}{}

	// 聊天区域 - 所有用户都可以访问
	defaultConfig["chat"] = map[string]interface{}{
		"enabled":    true,
		"playground": true,
		"chat":       true,
	}

	// 控制台区域 - 所有用户都可以访问
	defaultConfig["console"] = map[string]interface{}{
		"enabled":    true,
		"detail":     true,
		"token":      true,
		"log":        true,
		"midjourney": true,
		"task":       true,
	}

	// 个人中心区域 - 所有用户都可以访问
	defaultConfig["personal"] = map[string]interface{}{
		"enabled":  true,
		"topup":    true,
		"personal": true,
	}

	// 管理员区域 - 根据角色决定
	if userRole == common.RoleAdminUser {
		// 管理员可以访问管理员区域，但不能访问系统设置
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    false, // 管理员不能访问系统设置
		}
	} else if userRole == common.RoleRootUser {
		// 超级管理员可以访问所有功能
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    true,
		}
	}
	// 普通用户不包含admin区域

	// 转换为JSON字符串
	configBytes, err := json.Marshal(defaultConfig)
	if err != nil {
		common.SysLog("生成默认边栏配置失败: " + err.Error())
		return ""
	}

	return string(configBytes)
}

// CheckUserExistOrDeleted check if user exist or deleted, if not exist, return false, nil, if deleted or exist, return true, nil
func CheckUserExistOrDeleted(username string, email string) (bool, error) {
	var user User

	// err := DB.Unscoped().First(&user, "username = ? or email = ?", username, email).Error
	// check email if empty
	var err error
	email = NormalizeEmail(email)
	if email == "" {
		err = DB.Unscoped().First(&user, "username = ?", username).Error
	} else {
		err = DB.Unscoped().First(&user, "username = ? or LOWER(email) = ?", username, email).Error
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// not exist, return false, nil
			return false, nil
		}
		// other error, return false, err
		return false, err
	}
	// exist, return true, nil
	return true, nil
}

// NormalizeEmail trims surrounding whitespace and lowercases an email so that
// case/whitespace variants of the same address collapse to one canonical form.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func emailQuery(tx *gorm.DB, email string) *gorm.DB {
	if tx == nil {
		tx = DB
	}
	return tx.Unscoped().Model(&User{}).Where("LOWER(email) = ?", NormalizeEmail(email))
}

func CountUsersByEmail(email string) (int64, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return 0, nil
	}
	var count int64
	err := emailQuery(DB, email).Count(&count).Error
	return count, err
}

func IsEmailAvailable(email string, excludeUserID int) (bool, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return true, nil
	}
	query := emailQuery(DB, email)
	if excludeUserID > 0 {
		query = query.Where("id <> ?", excludeUserID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

func EnsureEmailAvailable(email string, excludeUserID int) error {
	available, err := IsEmailAvailable(email, excludeUserID)
	if err != nil {
		return err
	}
	if !available {
		return ErrEmailAlreadyTaken
	}
	return nil
}

// withNormalizedEmailLock serializes concurrent writers that target the same
// normalized email inside tx, so a "check then write" sequence cannot be raced
// by two transactions. It must be called inside an active transaction; the lock
// is scoped to that transaction and released on commit/rollback.
//
//   - PostgreSQL: transaction-level advisory lock keyed by the normalized email.
//   - MySQL (default REPEATABLE READ): a locking read that takes a next-key/gap
//     lock on the email index, blocking concurrent inserts of the same value.
//   - SQLite: no explicit lock; the single-writer model already serializes the
//     write, so a racing second write fails instead of duplicating.
//
// An empty email is allowed to repeat and needs no serialization.
func withNormalizedEmailLock(tx *gorm.DB, email string, fn func(tx *gorm.DB) error) error {
	email = NormalizeEmail(email)
	if email == "" {
		return fn(tx)
	}
	switch {
	case common.UsingPostgreSQL:
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", email).Error; err != nil {
			return err
		}
	case common.UsingMySQL:
		var ids []int
		if err := tx.Raw("SELECT id FROM users WHERE email = ? FOR UPDATE", email).Scan(&ids).Error; err != nil {
			return err
		}
	}
	return fn(tx)
}

func ensureEmailAvailableWithTx(tx *gorm.DB, email string, excludeUserID int) error {
	email = NormalizeEmail(email)
	if email == "" {
		return nil
	}
	query := emailQuery(tx, email)
	if excludeUserID > 0 {
		query = query.Where("id <> ?", excludeUserID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrEmailAlreadyTaken
	}
	return nil
}

// BindEmailToUser atomically checks email availability and assigns it to the
// user, serializing concurrent binds of the same email so two accounts cannot
// end up sharing one address. The email is normalized before check and store.
func BindEmailToUser(user *User, email string) error {
	email = NormalizeEmail(email)
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return withNormalizedEmailLock(tx, email, func(tx *gorm.DB) error {
			if err := ensureEmailAvailableWithTx(tx, email, user.Id); err != nil {
				return err
			}
			if err := tx.Model(&User{}).Where("id = ?", user.Id).Update("email", email).Error; err != nil {
				return err
			}
			user.Email = email
			return nil
		})
	}); err != nil {
		return err
	}
	return updateUserCache(*user)
}

func GetMaxUserId() int {
	var user User
	DB.Unscoped().Last(&user)
	return user.Id
}

// allowedUserOrderColumns 白名单：允许作为 ORDER BY 字段的列，防 SQL 注入。
// 任何不在白名单的输入都回退到默认 id desc。
var allowedUserOrderColumns = map[string]bool{
	"id":             true,
	"created_at":     true,
	"last_login_at":  true,
	"used_quota":     true,
	"request_count":  true,
	"rpm_24h":        true,
	"quota":          true,
}

// buildUserOrderClause 根据请求参数生成安全的 ORDER BY 子句。
// orderBy 不在白名单或为空时返回默认 "id desc"。
func buildUserOrderClause(orderBy, order string) string {
	if !allowedUserOrderColumns[orderBy] {
		return "id desc"
	}
	if order != "asc" {
		order = "desc" // 默认降序
	}
	// 二级排序按 id 保稳定（同 RPM 时按 id 分先后，避免 paginated list 错位）
	return fmt.Sprintf("%s %s, id desc", orderBy, order)
}

func GetAllUsers(pageInfo *common.PageInfo) (users []*User, total int64, err error) {
	return GetAllUsersOrdered(pageInfo, "id", "desc")
}

// GetAllUsersOrdered 支持动态 ORDER BY 的列表查询。
// orderBy / order 经 buildUserOrderClause 白名单过滤后才拼到 SQL。
func GetAllUsersOrdered(pageInfo *common.PageInfo, orderBy, order string) (users []*User, total int64, err error) {
	// Start transaction
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Get total count within transaction
	err = tx.Unscoped().Model(&User{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	orderClause := buildUserOrderClause(orderBy, order)
	// Get paginated users within same transaction
	err = tx.Unscoped().Order(orderClause).Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Omit("password", "access_token").Find(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Commit transaction
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

func GetPinnedUsers() ([]*User, error) {
	var users []*User
	err := DB.Where("pinned = ?", true).Select("id, username, display_name, `group`, quota, remark, pinned").Find(&users).Error
	return users, err
}

func SearchUsers(keyword string, group string, startIdx int, num int) ([]*User, int64, error) {
	var users []*User
	var total int64
	var err error

	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 构建基础查询
	query := tx.Unscoped().Model(&User{})

	// 构建搜索条件
	likeCondition := "username LIKE ? OR email LIKE ? OR display_name LIKE ?"

	// 尝试将关键字转换为整数ID
	keywordInt, err := strconv.Atoi(keyword)
	if err == nil {
		// 如果是数字，同时搜索ID和其他字段
		likeCondition = "id = ? OR " + likeCondition
		if group != "" {
			query = query.Where("("+likeCondition+") AND "+commonGroupCol+" = ?",
				keywordInt, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", group)
		} else {
			query = query.Where(likeCondition,
				keywordInt, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
		}
	} else {
		// 非数字关键字，只搜索字符串字段
		if group != "" {
			query = query.Where("("+likeCondition+") AND "+commonGroupCol+" = ?",
				"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", group)
		} else {
			query = query.Where(likeCondition,
				"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
		}
	}

	// 获取总数
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = query.Omit("password", "access_token").Order("id desc").Limit(num).Offset(startIdx).Find(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

func GetUserById(id int, selectAll bool) (*User, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	user := User{Id: id}
	var err error = nil
	if selectAll {
		err = DB.First(&user, "id = ?", id).Error
	} else {
		err = DB.Omit("password", "access_token").First(&user, "id = ?", id).Error
	}
	return &user, err
}

func GetUserIdByAffCode(affCode string) (int, error) {
	if affCode == "" {
		return 0, errors.New("affCode 为空！")
	}
	var user User
	err := DB.Select("id").First(&user, "aff_code = ?", affCode).Error
	return user.Id, err
}

func GetUserIdByUsername(username string) (int, error) {
	if username == "" {
		return 0, errors.New("username 为空！")
	}
	var user User
	err := DB.Select("id").First(&user, "username = ?", username).Error
	return user.Id, err
}

func DeleteUserById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
	}
	return deleteUserWithLedger(id, false)
}

func HardDeleteUserById(id int) error {
	if id == 0 {
		return errors.New("id 为空！")
	}
	return deleteUserWithLedger(id, true)
}

func deleteUserWithLedger(id int, hardDelete bool) error {
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		selectQuery := batchDeleteRowLock(tx)
		if hardDelete {
			selectQuery = selectQuery.Unscoped()
		}
		if err := selectQuery.
			Select("id", "deleted_at").
			Where("id = ?", id).
			First(&user).Error; err != nil {
			return err
		}
		userDeleteKinds := []int{
			BatchUpdateTypeUserQuota,
			BatchUpdateTypeUsedQuota,
			BatchUpdateTypeRequestCount,
		}
		if hardDelete && user.DeletedAt.Valid {
			if err := ensureBatchUpdateDeleteLedgers(
				tx,
				id,
				user.DeletedAt.Time.Unix(),
				userDeleteKinds...,
			); err != nil {
				return err
			}
		} else {
			if err := createBatchUpdateDeleteLedgers(tx, id, userDeleteKinds...); err != nil {
				return err
			}
		}

		deleteQuery := tx
		if hardDelete {
			if err := deleteUserAuthenticationData(tx, id); err != nil {
				return err
			}
			if err := settleUserPendingWorkloads(tx, id); err != nil {
				return err
			}
			// 事务内抬 auth_version 并写围栏，让缓存失效变成确定性动作。
			// 提交后的 invalidateUserCache 是尽力而为：它失败时 user:%d 会存活到 TTL，
			// 期间会话缓存与用户缓存双命中就能让已删用户继续通过后台鉴权。
			// 围栏抬高后 cacheGetUserBase 强制回源，回源查不到人即拒绝。
			// 必须放在删除用户行之前——它内部先查用户，行没了会直接失败。
			if _, err := IncrementUserAuthVersionWithTx(tx, id); err != nil {
				return err
			}
			deleteQuery = deleteQuery.Unscoped()
		}
		return requireSelectedDeleteRows(
			fmt.Sprintf("user id %d", id),
			1,
			deleteQuery.Where("id = ?", id).Delete(&User{}),
		)
	})
	if err != nil {
		return err
	}

	if err := invalidateUserCache(id); err != nil {
		common.SysError(fmt.Sprintf("failed to invalidate deleted user %d cache: %v", id, err))
	}
	return nil
}

// settleUserPendingWorkloads 硬删除用户前把其名下在途工作项置终态。
// 后台 worker 对这些行做资金回补时都要求命中 1 行用户记录（钱包预扣清扫器、
// 异步任务退款均是 RowsAffected != 1 即报错回滚），用户行一旦消失就会每轮失败、
// 记录状态回滚后 updated_at/id 又永远排在队首，既无法收敛还会持续重试上游。
// 账号已被彻底删除，余额随 users 行一并消失，这里只需关闭在途状态，不做退款。
func settleUserPendingWorkloads(tx *gorm.DB, userId int) error {
	if err := tx.Model(&WalletPreConsumeRecord{}).
		Where("user_id = ? AND status = ?", userId, WalletPreConsumeStatusReserved).
		Update("status", WalletPreConsumeStatusRefunded).Error; err != nil {
		return err
	}
	if err := tx.Model(&Task{}).
		Where("user_id = ? AND status NOT IN ?", userId,
			[]TaskStatus{TaskStatusSuccess, TaskStatusFailure}).
		Updates(map[string]any{
			"status":      TaskStatusFailure,
			"progress":    "100%",
			"fail_reason": "user deleted",
		}).Error; err != nil {
		return err
	}
	return tx.Model(&Midjourney{}).
		Where("user_id = ? AND progress != ?", userId, "100%").
		Updates(map[string]any{
			"status":      "FAILURE",
			"progress":    "100%",
			"fail_reason": "user deleted",
		}).Error
}

// deleteUserAuthenticationData 硬删除用户前清空其全部认证凭据，否则残留的 token
// 仍可通过 TokenAuth 鉴权（ValidateUserToken 直查 DB，没有缓存层可兜底）。
// Token 参与批量额度更新，硬删除必须补终结台账，否则后台 flush 会因目标行消失而报错。
func deleteUserAuthenticationData(tx *gorm.DB, userId int) error {
	var tokens []Token
	if err := batchDeleteRowLock(tx.Unscoped()).
		Select("id", "deleted_at").
		Where("user_id = ?", userId).
		Order("id").
		Find(&tokens).Error; err != nil {
		return err
	}
	for _, token := range tokens {
		deletedAt := common.GetTimestamp()
		if token.DeletedAt.Valid {
			deletedAt = token.DeletedAt.Time.Unix()
		}
		if err := ensureBatchUpdateDeleteLedgers(
			tx,
			token.Id,
			deletedAt,
			BatchUpdateTypeTokenQuota,
		); err != nil {
			return err
		}
	}

	for _, authData := range []any{
		&Token{},
		&TwoFABackupCode{},
		&TwoFA{},
		&PasskeyCredential{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
	} {
		if err := tx.Unscoped().Where("user_id = ?", userId).Delete(authData).Error; err != nil {
			return err
		}
	}
	return DeleteUserOAuthBindingsByUserIdWithTx(tx.Unscoped(), userId)
}

func inviteUser(inviterId int) (err error) {
	user, err := GetUserById(inviterId, true)
	if err != nil {
		return err
	}
	user.AffCount++
	user.AffQuota += common.QuotaForInviter
	user.AffHistoryQuota += common.QuotaForInviter
	return DB.Save(user).Error
}

// grantInviteeAndInviterRewards 在新被推荐人创建成功后统一处理邀请相关赠送：
// 1) 给被推荐人发注册邀请额度(QuotaForInvitee)；
// 2) 给邀请人发奖——若开启 RewardInviterOnEffectiveOnly 则只标记待结算(延迟到被推荐人
//    登录+消费达标后由 TryGrantInviterReward 发放)，否则按旧逻辑注册即发。
// 调用前必须保证被推荐人行的 inviter_id 已落库(Insert/InsertWithTx 已统一写入)，
// 否则 TryGrantInviterReward 的 inviter_id<>0 条件无法匹配。inviterId 须 != 0。
func grantInviteeAndInviterRewards(inviteeId int, inviterId int) {
	if common.QuotaForInvitee > 0 {
		_ = IncreaseUserQuota(inviteeId, common.QuotaForInvitee)
		RecordLog(inviteeId, LogTypeSystem, fmt.Sprintf("使用邀请码赠送 %s", logger.LogQuota(common.QuotaForInvitee)))
	}
	if common.QuotaForInviter <= 0 {
		return
	}
	if common.RewardInviterOnEffectiveOnly {
		// 延迟发放：标记为待结算，等被推荐人登录且消费达标后再发奖（见 TryGrantInviterReward）
		if err := DB.Model(&User{}).Where("id = ?", inviteeId).Update("inviter_reward_granted", false).Error; err != nil {
			common.SysLog(fmt.Sprintf("grantInviteeAndInviterRewards: mark invitee %d pending failed: %s", inviteeId, err.Error()))
		}
		return
	}
	RecordLog(inviterId, LogTypeSystem, fmt.Sprintf("邀请用户赠送 %s", logger.LogQuota(common.QuotaForInviter)))
	_ = inviteUser(inviterId)
}

// TryGrantInviterReward 在被推荐人产生消费后调用：当其成为「有效用户」
// (已登录 last_login_at>0 + 消费 used_quota 达标) 时，把待结算的邀请奖励发给推荐人。
// 通过原子条件 UPDATE 置位 inviter_reward_granted，保证并发下只发一次。
func TryGrantInviterReward(userId int) {
	if !common.RewardInviterOnEffectiveOnly || common.QuotaForInviter <= 0 {
		return
	}
	threshold := common.EffectiveInviteeConsumeThreshold
	if threshold <= 0 {
		// 默认口径：消费必须超过赠送给被推荐人的额度
		threshold = common.QuotaForInvitee + 1
	}
	result := DB.Model(&User{}).
		Where("id = ? AND inviter_id <> 0 AND inviter_reward_granted = ? AND last_login_at > 0 AND used_quota >= ?",
			userId, false, threshold).
		Update("inviter_reward_granted", true)
	if result.Error != nil {
		common.SysLog("TryGrantInviterReward update failed: " + result.Error.Error())
		return
	}
	if result.RowsAffected == 0 {
		return // 未达标或已结算，幂等跳过
	}
	invitee, err := GetUserById(userId, false)
	if err != nil || invitee.InviterId <= 0 {
		// 已抢到发放权但拿不到 inviter，回滚置位留待下次消费重试，避免奖励永久丢失
		DB.Model(&User{}).Where("id = ?", userId).Update("inviter_reward_granted", false)
		common.SysLog(fmt.Sprintf("TryGrantInviterReward: load invitee %d failed, rolled back", userId))
		return
	}
	if err := inviteUser(invitee.InviterId); err != nil {
		// 发奖失败同样回滚，下次消费重试
		DB.Model(&User{}).Where("id = ?", userId).Update("inviter_reward_granted", false)
		common.SysLog(fmt.Sprintf("TryGrantInviterReward: inviteUser(%d) failed, rolled back: %s", invitee.InviterId, err.Error()))
		return
	}
	RecordLog(invitee.InviterId, LogTypeSystem,
		fmt.Sprintf("被推荐人 #%d 达标，邀请奖励 %s", userId, logger.LogQuota(common.QuotaForInviter)))
}

// IncreaseAffQuotaForInviter 把分成 quota 累加到 inviter 的 aff_quota / aff_history（原子）。
// 用于推荐分成日结，不动 aff_count。
// 注意 DB 列是 aff_history，struct 字段是 AffHistoryQuota。
func IncreaseAffQuotaForInviter(inviterId int, quota int) error {
	if inviterId <= 0 || quota <= 0 {
		return nil
	}
	return DB.Model(&User{}).Where("id = ?", inviterId).
		Updates(map[string]interface{}{
			"aff_quota":   gorm.Expr("aff_quota + ?", quota),
			"aff_history": gorm.Expr("aff_history + ?", quota),
		}).Error
}

// InviteeRecord 单个被推荐用户的对外展示信息。
type InviteeRecord struct {
	Id          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	CreatedAt   int64  `json:"created_at"`
	UsedQuota   int    `json:"used_quota"`
	Status      int    `json:"status"`
}

// GetInviteesByInviterId 拉取某个 inviter 的所有被邀请人（按注册时间倒序）。
func GetInviteesByInviterId(inviterId int) ([]InviteeRecord, error) {
	if inviterId <= 0 {
		return []InviteeRecord{}, nil
	}
	var records []InviteeRecord
	err := DB.Model(&User{}).
		Select("id, username, display_name, created_at, used_quota, status").
		Where("inviter_id = ?", inviterId).
		Order("created_at DESC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (user *User) TransferAffQuotaToQuota(quota int, callerIp string) error {
	// 检查quota是否小于最小额度
	if float64(quota) < common.QuotaPerUnit {
		return fmt.Errorf("转移额度最小为%s！", logger.LogQuota(int(common.QuotaPerUnit)))
	}

	// 开始数据库事务
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback() // 确保在函数退出时事务能回滚

	// 加锁查询用户以确保数据一致性
	err := withForUpdate(tx).First(&user, user.Id).Error
	if err != nil {
		return err
	}

	// 再次检查用户的AffQuota是否足够
	if user.AffQuota < quota {
		return errors.New("邀请额度不足！")
	}

	// 条件更新同时适用于 SQLite：即使方言忽略 FOR UPDATE，也不会丢失并发转移。
	if err := requireSingleRow(
		tx.Model(&User{}).
			Where("id = ? AND aff_quota >= ?", user.Id, quota).
			Updates(map[string]interface{}{
				"aff_quota": gorm.Expr("aff_quota - ?", quota),
				"quota":     gorm.Expr("quota + ?", quota),
			}),
		errors.New("邀请额度不足！"),
	); err != nil {
		return err
	}
	user.AffQuota -= quota
	user.Quota += quota

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		return err
	}

	// 写入一条 topup 类型的 log（事务外，LOG_DB 可能为独立实例）
	// 带齐 admin_info 审计字段，避免前端提示"旧版本实例"
	adminInfo := map[string]interface{}{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"caller_ip":               callerIp,
		"payment_method":          "affiliate_transfer",
		"callback_payment_method": "",
		"version":                 common.Version,
	}
	other := map[string]interface{}{
		"admin_info": adminInfo,
	}
	logEntry := &Log{
		UserId:    user.Id,
		Username:  user.Username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   fmt.Sprintf("推荐返利转入余额：%s", logger.LogQuota(quota)),
		Quota:     quota,
		Ip:        callerIp,
		Other:     common.MapToJsonStr(other),
	}
	if err := LOG_DB.Create(logEntry).Error; err != nil {
		common.SysLog("failed to record affiliate transfer log: " + err.Error())
	}
	return nil
}

// prepareForInsert normalizes the email, enforces email uniqueness within tx,
// and hashes the password before a user row is created. It must run inside the
// same transaction (and email lock) that performs the Create so the check and
// write cannot be raced by a concurrent registration of the same email.
func (user *User) prepareForInsert(tx *gorm.DB) error {
	user.Email = NormalizeEmail(user.Email)
	if err := ensureEmailAvailableWithTx(tx, user.Email, 0); err != nil {
		return err
	}
	if user.Password == "" {
		return nil
	}
	var err error
	user.Password, err = common.Password2Hash(user.Password)
	return err
}

func (user *User) Insert(inviterId int) error {
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return withNormalizedEmailLock(tx, user.Email, func(tx *gorm.DB) error {
			if err := user.prepareForInsert(tx); err != nil {
				return err
			}
			user.Quota = common.QuotaForNewUser
			//user.SetAccessToken(common.GetUUID())
			user.AffCode = common.GetRandomString(4)
			// 落库邀请人 id，供延迟发奖(TryGrantInviterReward)按 inviter_id 匹配；统一覆盖所有注册入口
			if inviterId != 0 {
				user.InviterId = inviterId
			}

			// 初始化用户设置，包括默认的边栏配置
			if user.Setting == "" {
				defaultSetting := dto.UserSetting{}
				// 这里暂时不设置SidebarModules，因为需要在用户创建后根据角色设置
				user.SetSetting(defaultSetting)
			}

			return tx.Create(user).Error
		})
	}); err != nil {
		return err
	}

	// 用户创建成功后，根据角色初始化边栏配置
	// 需要重新获取用户以确保有正确的ID和Role
	var createdUser User
	if err := DB.Where("username = ?", user.Username).First(&createdUser).Error; err == nil {
		// 生成基于角色的默认边栏配置
		defaultSidebarConfig := generateDefaultSidebarConfigForRole(createdUser.Role)
		if defaultSidebarConfig != "" {
			currentSetting := createdUser.GetSetting()
			currentSetting.SidebarModules = defaultSidebarConfig
			createdUser.SetSetting(currentSetting)
			createdUser.Update(false)
			common.SysLog(fmt.Sprintf("为新用户 %s (角色: %d) 初始化边栏配置", createdUser.Username, createdUser.Role))
		}
	}

	if common.QuotaForNewUser > 0 {
		RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("新用户注册赠送 %s", logger.LogQuota(common.QuotaForNewUser)))
	}
	if inviterId != 0 {
		grantInviteeAndInviterRewards(user.Id, inviterId)
	}
	return nil
}

// InsertWithTx inserts a new user within an existing transaction.
// This is used for OAuth registration where user creation and binding need to be atomic.
// Post-creation tasks (sidebar config, logs, inviter rewards) are handled after the transaction commits.
func (user *User) InsertWithTx(tx *gorm.DB, inviterId int) error {
	return withNormalizedEmailLock(tx, user.Email, func(tx *gorm.DB) error {
		if err := user.prepareForInsert(tx); err != nil {
			return err
		}
		user.Quota = common.QuotaForNewUser
		user.AffCode = common.GetRandomString(4)
		// 落库邀请人 id，供延迟发奖(TryGrantInviterReward)按 inviter_id 匹配；覆盖 OAuth 注册入口
		if inviterId != 0 {
			user.InviterId = inviterId
		}

		// 初始化用户设置
		if user.Setting == "" {
			defaultSetting := dto.UserSetting{}
			user.SetSetting(defaultSetting)
		}

		return tx.Create(user).Error
	})
}

// FinalizeOAuthUserCreation performs post-transaction tasks for OAuth user creation.
// This should be called after the transaction commits successfully.
func (user *User) FinalizeOAuthUserCreation(inviterId int) {
	// 用户创建成功后，根据角色初始化边栏配置
	var createdUser User
	if err := DB.Where("id = ?", user.Id).First(&createdUser).Error; err == nil {
		defaultSidebarConfig := generateDefaultSidebarConfigForRole(createdUser.Role)
		if defaultSidebarConfig != "" {
			currentSetting := createdUser.GetSetting()
			currentSetting.SidebarModules = defaultSidebarConfig
			createdUser.SetSetting(currentSetting)
			createdUser.Update(false)
			common.SysLog(fmt.Sprintf("为新用户 %s (角色: %d) 初始化边栏配置", createdUser.Username, createdUser.Role))
		}
	}

	if common.QuotaForNewUser > 0 {
		RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("新用户注册赠送 %s", logger.LogQuota(common.QuotaForNewUser)))
	}
	if inviterId != 0 {
		grantInviteeAndInviterRewards(user.Id, inviterId)
	}
}

func (user *User) Update(updatePassword bool) error {
	var err error
	if updatePassword {
		user.Password, err = common.Password2Hash(user.Password)
		if err != nil {
			return err
		}
	}
	newUser := *user
	DB.First(&user, user.Id)
	if updatePassword {
		if err = DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(user).Updates(newUser).Error; err != nil {
				return err
			}
			_, err := IncrementUserAuthVersionWithTx(tx, user.Id)
			return err
		}); err != nil {
			return err
		}
		return PublishUserAuthCache(user.Id)
	}
	if err = DB.Model(user).Updates(newUser).Error; err != nil {
		return err
	}

	// Update cache
	return updateUserCache(*user)
}

// updateUserSingleColumn 只更新单一列，绝不触碰 quota/used_quota/request_count 等计费字段，
// 从根本上消除 UpdateSelf/UpdateUserSetting 等资料接口全行写回覆盖计费原子自增的丢失更新竞态。
// 写库后失效用户缓存即可：quota 由 GetUserQuota 直读 DB、计费准入不信任缓存，故失效对计费链路无影响。
func updateUserSingleColumn(userId int, column string, value interface{}) error {
	if userId <= 0 {
		return errors.New("invalid user id")
	}
	result := DB.Model(&User{}).Where("id = ?", userId).Update(column, value)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("update user %s: user %d not found", column, userId)
	}
	return invalidateUserCache(userId)
}

// UpdateUserSettingColumn 仅写 setting 列（侧边栏 / 语言 / 通知 / 账单偏好等资料设置），
// 供 UpdateSelf、UpdateUserSetting 等 self 类接口使用，替代原本的全行写回。
func UpdateUserSettingColumn(userId int, setting string) error {
	return updateUserSingleColumn(userId, "setting", setting)
}

// UpdateUserAccessTokenColumn 仅写 access_token 列（系统管理令牌），供 GenerateAccessToken 使用。
func UpdateUserAccessTokenColumn(userId int, token string) error {
	return updateUserSingleColumn(userId, "access_token", token)
}

// UpdateUserAffCodeColumn 仅写 aff_code 列，供 GetAffCode 首次生成邀请码使用。
func UpdateUserAffCodeColumn(userId int, affCode string) error {
	return updateUserSingleColumn(userId, "aff_code", affCode)
}

func (user *User) Edit(updatePassword bool) error {
	var err error
	if updatePassword {
		user.Password, err = common.Password2Hash(user.Password)
		if err != nil {
			return err
		}
	}

	newUser := *user
	updates := map[string]interface{}{
		"username":     newUser.Username,
		"display_name": newUser.DisplayName,
		"group":        newUser.Group,
		"remark":       newUser.Remark,
		"rpm_limit":    newUser.RpmLimit,
		"pinned":       newUser.Pinned,
		"inviter_id":   newUser.InviterId,
		"expires_at":   newUser.ExpiresAt,
	}
	if updatePassword {
		updates["password"] = newUser.Password
	}

	var oldUser User
	DB.First(&oldUser, user.Id)

	// Update inviter aff_count when inviter_id changes
	if oldUser.InviterId != newUser.InviterId {
		if oldUser.InviterId > 0 {
			DB.Model(&User{}).Where("id = ?", oldUser.InviterId).UpdateColumn("aff_count", gorm.Expr("aff_count - 1"))
		}
		if newUser.InviterId > 0 {
			DB.Model(&User{}).Where("id = ?", newUser.InviterId).UpdateColumn("aff_count", gorm.Expr("aff_count + 1"))
		}
	}

	if updatePassword {
		if err = DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&oldUser).Updates(updates).Error; err != nil {
				return err
			}
			_, err := IncrementUserAuthVersionWithTx(tx, user.Id)
			return err
		}); err != nil {
			return err
		}
		return PublishUserAuthCache(user.Id)
	}
	if err = DB.Model(&oldUser).Updates(updates).Error; err != nil {
		return err
	}

	// Update cache —— 必须从 DB 重新读，不能直接用 *user。
	// *user 来源是 admin PUT /api/user/ 的 JSON.Decode 结果，前端 body 不会带 status / role /
	// quota 等字段，对应 Go 字段是零值。如果直接 ToBaseUser() 写 Redis，会把 Status=0/Role=0
	// 等无意义零值灌进缓存，下次 TokenAuth 见到 Status != UserStatusEnabled(1) 直接 403
	// "User has been banned"，直到 ~60s 缓存 TTL 过期才自愈（prod 上已造成 391 次/7min 的误封）。
	var fresh User
	if err := DB.First(&fresh, user.Id).Error; err != nil {
		// 极端情况：刚更新完用户被并发删除。不能写 *user（会复现原 bug 把零值灌进缓存），
		// 改为清缓存让下次访问被迫从 DB 重新读取。
		common.SysLog(fmt.Sprintf("Edit: failed to reload user %d before cache, invalidating: %v", user.Id, err))
		return invalidateUserCache(user.Id)
	}
	return updateUserCache(fresh)
}

func (user *User) ClearBinding(bindingType string) error {
	if user.Id == 0 {
		return errors.New("user id is empty")
	}

	bindingColumnMap := map[string]string{
		"email":    "email",
		"github":   "github_id",
		"discord":  "discord_id",
		"oidc":     "oidc_id",
		"wechat":   "wechat_id",
		"telegram": "telegram_id",
		"linuxdo":  "linux_do_id",
	}

	column, ok := bindingColumnMap[bindingType]
	if !ok {
		return errors.New("invalid binding type")
	}

	if err := DB.Model(&User{}).Where("id = ?", user.Id).Update(column, "").Error; err != nil {
		return err
	}

	if err := DB.Where("id = ?", user.Id).First(user).Error; err != nil {
		return err
	}

	return updateUserCache(*user)
}

func (user *User) Delete() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	return deleteUserWithLedger(user.Id, false)
}

func (user *User) HardDelete() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	return deleteUserWithLedger(user.Id, true)
}

// ValidateAndFill check password & user status
func (user *User) ValidateAndFill() (err error) {
	// When querying with struct, GORM will only query with non-zero fields,
	// that means if your field's value is 0, '', false or other zero values,
	// it won't be used to build query conditions
	password := user.Password
	username := strings.TrimSpace(user.Username)
	if username == "" || password == "" {
		return ErrUserEmptyCredentials
	}
	// find by username or email
	err = DB.Where("username = ? OR email = ?", username, username).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInvalidCredentials
		}
		return fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	// 账号无可用密码（如仅 OAuth 注册）时拒绝密码登录，避免空密码被绕过校验。
	if user.Password == "" {
		return ErrInvalidCredentials
	}
	okay := common.ValidatePasswordAndHash(password, user.Password)
	if !okay || user.Status != common.UserStatusEnabled {
		return ErrInvalidCredentials
	}
	return nil
}

func (user *User) FillUserById() error {
	if user.Id == 0 {
		return errors.New("id 为空！")
	}
	DB.Where(User{Id: user.Id}).First(user)
	return nil
}

func (user *User) FillUserByEmail() error {
	if user.Email == "" {
		return errors.New("email 为空！")
	}
	DB.Where(User{Email: user.Email}).First(user)
	return nil
}

func (user *User) FillUserByGitHubId() error {
	if user.GitHubId == "" {
		return errors.New("GitHub id 为空！")
	}
	DB.Where(User{GitHubId: user.GitHubId}).First(user)
	return nil
}

// UpdateGitHubId updates the user's GitHub ID (used for migration from login to numeric ID)
func (user *User) UpdateGitHubId(newGitHubId string) error {
	if user.Id == 0 {
		return errors.New("user id is empty")
	}
	return DB.Model(user).Update("github_id", newGitHubId).Error
}

func (user *User) FillUserByDiscordId() error {
	if user.DiscordId == "" {
		return errors.New("discord id 为空！")
	}
	DB.Where(User{DiscordId: user.DiscordId}).First(user)
	return nil
}

func (user *User) FillUserByOidcId() error {
	if user.OidcId == "" {
		return errors.New("oidc id 为空！")
	}
	DB.Where(User{OidcId: user.OidcId}).First(user)
	return nil
}

func (user *User) FillUserByWeChatId() error {
	if user.WeChatId == "" {
		return errors.New("WeChat id 为空！")
	}
	DB.Where(User{WeChatId: user.WeChatId}).First(user)
	return nil
}

func (user *User) FillUserByTelegramId() error {
	if user.TelegramId == "" {
		return errors.New("Telegram id 为空！")
	}
	err := DB.Where(User{TelegramId: user.TelegramId}).First(user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("该 Telegram 账户未绑定")
	}
	return nil
}

func IsEmailAlreadyTaken(email string) bool {
	count, err := CountUsersByEmail(email)
	return err == nil && count > 0
}

// GetUniqueUserByEmail returns the single user matching the normalized email.
// It returns ErrEmailNotFound when no account matches and ErrEmailAmbiguous
// when more than one account shares the address, so callers can refuse to act
// on an ambiguous match instead of touching multiple rows.
func GetUniqueUserByEmail(email string) (*User, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return nil, ErrEmailNotFound
	}
	var users []User
	if err := DB.Where("LOWER(email) = ?", email).Limit(2).Find(&users).Error; err != nil {
		return nil, err
	}
	switch len(users) {
	case 0:
		return nil, ErrEmailNotFound
	case 1:
		return &users[0], nil
	default:
		return nil, ErrEmailAmbiguous
	}
}

func IsWeChatIdAlreadyTaken(wechatId string) bool {
	return DB.Unscoped().Where("wechat_id = ?", wechatId).Find(&User{}).RowsAffected == 1
}

func IsGitHubIdAlreadyTaken(githubId string) bool {
	return DB.Unscoped().Where("github_id = ?", githubId).Find(&User{}).RowsAffected == 1
}

func IsDiscordIdAlreadyTaken(discordId string) bool {
	return DB.Unscoped().Where("discord_id = ?", discordId).Find(&User{}).RowsAffected == 1
}

func IsOidcIdAlreadyTaken(oidcId string) bool {
	return DB.Where("oidc_id = ?", oidcId).Find(&User{}).RowsAffected == 1
}

func IsTelegramIdAlreadyTaken(telegramId string) bool {
	return DB.Unscoped().Where("telegram_id = ?", telegramId).Find(&User{}).RowsAffected == 1
}

func ResetUserPasswordByEmail(email string, password string) error {
	if email == "" || password == "" {
		return errors.New("邮箱地址或密码为空！")
	}
	user, err := GetUniqueUserByEmail(email)
	if err != nil {
		return err
	}
	hashedPassword, err := common.Password2Hash(password)
	if err != nil {
		return err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", user.Id).
			Update("password", hashedPassword).Error; err != nil {
			return err
		}
		_, err := IncrementUserAuthVersionWithTx(tx, user.Id)
		return err
	}); err != nil {
		return err
	}
	return PublishUserAuthCache(user.Id)
}

func IsAdmin(userId int) bool {
	if userId == 0 {
		return false
	}
	var user User
	err := DB.Where("id = ?", userId).Select("role").Find(&user).Error
	if err != nil {
		common.SysLog("no such user " + err.Error())
		return false
	}
	return user.Role >= common.RoleAdminUser
}

//// IsUserEnabled checks user status from Redis first, falls back to DB if needed
//func IsUserEnabled(id int, fromDB bool) (status bool, err error) {
//	defer func() {
//		// Update Redis cache asynchronously on successful DB read
//		if shouldUpdateRedis(fromDB, err) {
//			gopool.Go(func() {
//				if err := updateUserStatusCache(id, status); err != nil {
//					common.SysError("failed to update user status cache: " + err.Error())
//				}
//			})
//		}
//	}()
//	if !fromDB && common.RedisEnabled {
//		// Try Redis first
//		status, err := getUserStatusCache(id)
//		if err == nil {
//			return status == common.UserStatusEnabled, nil
//		}
//		// Don't return error - fall through to DB
//	}
//	fromDB = true
//	var user User
//	err = DB.Where("id = ?", id).Select("status").Find(&user).Error
//	if err != nil {
//		return false, err
//	}
//
//	return user.Status == common.UserStatusEnabled, nil
//}

func ValidateAccessToken(token string) (*User, error) {
	if token == "" {
		return nil, nil
	}
	token = strings.Replace(token, "Bearer ", "", 1)
	user := &User{}
	err := DB.Where("access_token = ?", token).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	return user, nil
}

// GetUserQuota reads consumable quota from the main database. Redis is not a
// quota authority because an asynchronous cache delta can be observed in a
// different order on another node.
func GetUserQuota(id int) (quota int, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("quota").Find(&quota).Error
	if err != nil {
		return 0, err
	}

	return quota, nil
}

func GetUserUsedQuota(id int) (quota int, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("used_quota").Find(&quota).Error
	return quota, err
}

var (
	ErrInsufficientUserQuota  = errors.New("insufficient user quota")
	ErrInsufficientTokenQuota = errors.New("insufficient token quota")
)

// ReserveUserTokenQuota atomically reserves wallet and token quota in the main
// database. Redis and the batch updater are deliberately excluded: neither is
// allowed to decide admission or hold consumable quota. The only carve-out is
// TrustedSettleUserQuota, which records already-incurred debt (admission was
// decided against the trust threshold at request time) and may batch.
func ReserveUserTokenQuota(userID int, tokenID int, quota int) error {
	return ReserveUserTokenQuotaWithRecord("", userID, tokenID, quota)
}

// ReserveUserTokenQuotaWithRecord 预扣费并在同一事务内落地凭据。
// requestId 为空时退化为无凭据预扣（仅供不经过 relay 的内部调用）。
func ReserveUserTokenQuotaWithRecord(requestId string, userID int, tokenID int, quota int) error {
	if userID <= 0 || tokenID <= 0 {
		return errors.New("user id and token id must be positive")
	}
	if quota <= 0 {
		return errors.New("reservation quota must be positive")
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		// 锁序统一为 凭据 → users → tokens，与 FinalizeUserTokenQuota 的
		// claim-first 一致；先锁 users 再写凭据会与结算路径构成 AB-BA 死锁。
		if strings.TrimSpace(requestId) != "" {
			if err := createWalletPreConsumeRecordTx(tx, requestId, userID, tokenID, quota); err != nil {
				return err
			}
		}
		userResult := tx.Model(&User{}).
			Where("id = ? AND quota >= ?", userID, quota).
			Update("quota", gorm.Expr("quota - ?", quota))
		if userResult.Error != nil {
			return fmt.Errorf("reserve user quota: %w", userResult.Error)
		}
		if userResult.RowsAffected != 1 {
			return ErrInsufficientUserQuota
		}

		return adjustTokenQuotaTx(tx, tokenID, userID, quota)
	})
}

// ExtendUserTokenReservation 追加预扣额度，并同步累加到已有凭据上。
func ExtendUserTokenReservation(requestId string, userID int, tokenID int, quota int) error {
	if userID <= 0 || tokenID <= 0 {
		return errors.New("user id and token id must be positive")
	}
	if quota <= 0 {
		return errors.New("reservation quota must be positive")
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		// 同 ReserveUserTokenQuotaWithRecord：凭据 → users → tokens。
		if strings.TrimSpace(requestId) != "" {
			if err := addWalletPreConsumeTx(tx, requestId, userID, tokenID, quota); err != nil {
				return err
			}
		}
		userResult := tx.Model(&User{}).
			Where("id = ? AND quota >= ?", userID, quota).
			Update("quota", gorm.Expr("quota - ?", quota))
		if userResult.Error != nil {
			return fmt.Errorf("reserve user quota: %w", userResult.Error)
		}
		if userResult.RowsAffected != 1 {
			return ErrInsufficientUserQuota
		}

		return adjustTokenQuotaTx(tx, tokenID, userID, quota)
	})
}

// FinalizeUserTokenQuota 结算/退款并把预扣凭据推进到终态，两者同一事务。
// 这保证"余额已回补"与"凭据已关闭"不会只成功一半，避免清扫任务重复退款。
func FinalizeUserTokenQuota(requestId string, userID int, tokenID int, delta int, status string) error {
	if userID <= 0 || tokenID <= 0 {
		return errors.New("user id and token id must be positive")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		// 先抢占凭据再动余额：若凭据已被清扫任务推进到终态，说明这笔预扣的钱
		// 已经退回去了，此处必须直接返回，不能再结算/退款第二次。
		claimed, err := claimWalletPreConsumeTx(tx, requestId, status)
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
		if delta != 0 {
			return adjustUserTokenQuotaTx(tx, userID, tokenID, delta)
		}
		return nil
	})
}

// TrustedSettleUserQuota 记账信任旁路请求的实际消耗。请求未做预扣（准入时
// 已确认余额高于信任阈值），这里只是把已发生的债务落账，因此不做 quota >= X
// 守卫——即使并发把余额打穿阈值，债务也必须记下而不能丢。批量聚合把同一
// 用户的高频扣减合并为周期性单条 UPDATE，消除 users.quota 单行热点；批量器
// 未启用时退化为直接扣减。
func TrustedSettleUserQuota(userID int, quota int) error {
	if userID <= 0 {
		return errors.New("user id must be positive")
	}
	if quota <= 0 {
		return errors.New("settle quota must be positive")
	}
	if common.BatchUpdateEnabled {
		if err := addNewRecord(BatchUpdateTypeUserQuota, userID, -quota); err != nil {
			return recordBatchAdmissionError("trusted settle user quota", err)
		}
		return nil
	}
	// Unscoped 穿透软删，与退款/sweeper 同口径：请求进行中用户被软删，
	// 已发生的债务仍必须落账，否则软删成为逃单口。
	result := DB.Unscoped().Model(&User{}).
		Where("id = ?", userID).
		Update("quota", gorm.Expr("quota - ?", quota))
	if result.Error != nil {
		return fmt.Errorf("trusted settle user quota: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("trusted settle user quota: user %d not found", userID)
	}
	return nil
}

// adjustUserTokenQuotaTx 是 AdjustUserTokenQuota 的事务内版本。
func adjustUserTokenQuotaTx(tx *gorm.DB, userID int, tokenID int, delta int) error {
	if delta > 0 {
		userResult := tx.Model(&User{}).
			Where("id = ? AND quota >= ?", userID, delta).
			Update("quota", gorm.Expr("quota - ?", delta))
		if userResult.Error != nil {
			return fmt.Errorf("reserve user quota: %w", userResult.Error)
		}
		if userResult.RowsAffected != 1 {
			return ErrInsufficientUserQuota
		}
		return adjustTokenQuotaTx(tx, tokenID, userID, delta)
	}

	refund := -delta
	userResult := tx.Unscoped().Model(&User{}).
		Where("id = ?", userID).
		Update("quota", gorm.Expr("quota + ?", refund))
	if userResult.Error != nil {
		return fmt.Errorf("return user quota: %w", userResult.Error)
	}
	if userResult.RowsAffected != 1 {
		return fmt.Errorf("return user quota: user %d not found", userID)
	}
	return adjustTokenQuotaTx(tx, tokenID, userID, delta)
}

// AdjustUserTokenQuota settles a wallet reservation in one main-database
// transaction. A positive delta consumes more quota; a negative delta returns
// previously consumed quota.
func AdjustUserTokenQuota(userID int, tokenID int, delta int) error {
	if userID <= 0 || tokenID <= 0 {
		return errors.New("user id and token id must be positive")
	}
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		return ReserveUserTokenQuota(userID, tokenID, delta)
	}

	refund := -delta
	return DB.Transaction(func(tx *gorm.DB) error {
		userResult := tx.Unscoped().Model(&User{}).
			Where("id = ?", userID).
			Update("quota", gorm.Expr("quota + ?", refund))
		if userResult.Error != nil {
			return fmt.Errorf("return user quota: %w", userResult.Error)
		}
		if userResult.RowsAffected != 1 {
			return fmt.Errorf("return user quota: user %d not found", userID)
		}

		return adjustTokenQuotaTx(tx, tokenID, userID, delta)
	})
}

func GetUserEmail(id int) (email string, err error) {
	err = DB.Model(&User{}).Where("id = ?", id).Select("email").Find(&email).Error
	return email, err
}

// GetUserGroup gets group from Redis first, falls back to DB if needed
func GetUserGroup(id int, fromDB bool) (group string, err error) {
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) {
			_ = backgroundtask.Submit("user-group-cache-refill", func(context.Context) {
				if err := updateUserGroupCache(id, group); err != nil {
					common.SysLog("failed to update user group cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		group, err := getUserGroupCache(id)
		if err == nil {
			return group, nil
		}
		// Don't return error - fall through to DB
	}
	fromDB = true
	err = DB.Model(&User{}).Where("id = ?", id).Select(commonGroupCol).Find(&group).Error
	if err != nil {
		return "", err
	}

	return group, nil
}

// GetUserSetting gets setting from Redis first, falls back to DB if needed
func GetUserSetting(id int, fromDB bool) (settingMap dto.UserSetting, err error) {
	var setting string
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) {
			_ = backgroundtask.Submit("user-setting-cache-refill", func(context.Context) {
				if err := updateUserSettingCache(id, setting); err != nil {
					common.SysLog("failed to update user setting cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		setting, err := getUserSettingCache(id)
		if err == nil {
			return setting, nil
		}
		// Don't return error - fall through to DB
	}
	fromDB = true
	// can be nil setting
	var safeSetting sql.NullString
	err = DB.Model(&User{}).Where("id = ?", id).Select("setting").Find(&safeSetting).Error
	if err != nil {
		return settingMap, err
	}
	if safeSetting.Valid {
		setting = safeSetting.String
	} else {
		setting = ""
	}
	userBase := &UserBase{
		Setting: setting,
	}
	return userBase.GetSetting(), nil
}

func IncreaseUserQuota(id int, quota int) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if quota == 0 {
		return nil
	}
	return increaseUserQuota(id, quota)
}

func increaseUserQuota(id int, quota int) error {
	result := DB.Model(&User{}).
		Where("id = ?", id).
		Update("quota", gorm.Expr("quota + ?", quota))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("increase user quota: user %d not found", id)
	}
	return nil
}

func DecreaseUserQuota(id int, quota int) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if quota == 0 {
		return nil
	}
	return decreaseUserQuota(id, quota)
}

func decreaseUserQuota(id int, quota int) error {
	result := DB.Model(&User{}).
		Where("id = ? AND quota >= ?", id, quota).
		Update("quota", gorm.Expr("quota - ?", quota))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInsufficientUserQuota
	}
	return nil
}

func DeltaUpdateUserQuota(id int, delta int) (err error) {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		return IncreaseUserQuota(id, delta)
	} else {
		return DecreaseUserQuota(id, -delta)
	}
}

//func GetRootUserEmail() (email string) {
//	DB.Model(&User{}).Where("role = ?", common.RoleRootUser).Select("email").Find(&email)
//	return email
//}

func GetRootUser() (user *User) {
	DB.Where("role = ?", common.RoleRootUser).First(&user)
	return user
}

func UpdateUserLastLoginAt(id int) {
	if err := DB.Model(&User{}).Where("id = ?", id).Update("last_login_at", common.GetTimestamp()).Error; err != nil {
		common.SysLog("failed to update user last_login_at: " + err.Error())
	}
}

// UpdateUserAndChannelUsedQuota atomically admits one consumption event into
// the batch updater. Database persistence remains grouped by entity so a failed
// channel update can be retried without replaying a successful user update.
func UpdateUserAndChannelUsedQuota(userID int, channelID int, quota int) error {
	return UpdateUserAndChannelUsedQuotaWithContext(context.Background(), userID, channelID, quota)
}

// UpdateUserAndChannelUsedQuotaWithContext preserves billing lifecycle
// authority carried by tracked producers.
func UpdateUserAndChannelUsedQuotaWithContext(ctx context.Context, userID int, channelID int, quota int) error {
	if common.BatchUpdateEnabled {
		err := addNewRecords([]BatchUpdate{
			{Kind: BatchUpdateTypeUsedQuota, ID: userID, Delta: quota},
			{Kind: BatchUpdateTypeRequestCount, ID: userID, Delta: 1},
			{Kind: BatchUpdateTypeChannelUsedQuota, ID: channelID, Delta: quota},
		})
		if err != nil {
			return recordBatchAdmissionError("update usage statistics", err)
		}
		return nil
	}
	if err := updateUserUsedQuotaAndRequestCount(ctx, userID, quota, 1); err != nil {
		return err
	}
	return updateChannelUsedQuota(channelID, quota)
}

func UpdateUserUsedQuotaAndRequestCount(id int, quota int) error {
	return UpdateUserUsedQuotaAndRequestCountWithContext(context.Background(), id, quota)
}

func UpdateUserUsedQuotaAndRequestCountWithContext(ctx context.Context, id int, quota int) error {
	if common.BatchUpdateEnabled {
		err := addNewRecords([]BatchUpdate{
			{Kind: BatchUpdateTypeUsedQuota, ID: id, Delta: quota},
			{Kind: BatchUpdateTypeRequestCount, ID: id, Delta: 1},
		})
		if err != nil {
			return recordBatchAdmissionError("update user used quota and request count", err)
		}
		return nil
	}
	return updateUserUsedQuotaAndRequestCount(ctx, id, quota, 1)
}

var (
	reserveInviterRewardTicket = billinglifecycle.ReserveFromContext
	runInviterReward           = TryGrantInviterReward
)

func inviterRewardAfterUsedQuotaEnabled() bool {
	return common.RewardInviterOnEffectiveOnly && common.QuotaForInviter > 0
}

func reserveInviterRewardAfterUsedQuota(ctx context.Context) (*billinglifecycle.Ticket, error) {
	if !inviterRewardAfterUsedQuotaEnabled() {
		return nil, nil
	}
	return reserveInviterRewardTicket(ctx, "inviter-reward-after-used-quota")
}

func submitInviterRewardAfterUsedQuota(ticket *billinglifecycle.Ticket, id int) {
	if ticket == nil {
		return
	}
	if err := ticket.Submit(func(*billinglifecycle.Ticket) {
		runInviterReward(id)
	}); err != nil {
		// A freshly reserved ticket is private to this call and is submitted
		// exactly once. Returning an error after the database commit would make
		// the batch updater replay the committed quota delta, so treat any
		// coordinator rejection here as an internal invariant violation.
		panic(fmt.Sprintf("submit reserved inviter reward ticket for user %d: %v", id, err))
	}
}

func updateUserUsedQuotaAndRequestCount(ctx context.Context, id int, quota int, count int) error {
	ticket, err := reserveInviterRewardAfterUsedQuota(ctx)
	if err != nil {
		return fmt.Errorf("reserve inviter reward before updating user %d usage: %w", id, err)
	}
	submitted := false
	defer func() {
		if ticket != nil && !submitted {
			if releaseErr := ticket.Release(); releaseErr != nil {
				common.SysError(fmt.Sprintf("failed to release inviter reward ticket for user %d: %v", id, releaseErr))
			}
		}
	}()

	result := DB.WithContext(ctx).Model(&User{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"request_count": gorm.Expr("request_count + ?", count),
		},
	)
	if result.Error != nil {
		common.SysLog("failed to update user used quota and request count: " + result.Error.Error())
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("update user id %d usage affected %d rows, want 1", id, result.RowsAffected)
	}

	submitInviterRewardAfterUsedQuota(ticket, id)
	submitted = ticket != nil
	return nil

	//// 更新缓存
	//if err := invalidateUserCache(id); err != nil {
	//	common.SysError("failed to invalidate user cache: " + err.Error())
	//}
}

// GetUsernameById gets username from Redis first, falls back to DB if needed
func GetUsernameById(id int, fromDB bool) (username string, err error) {
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) {
			_ = backgroundtask.Submit("user-name-cache-refill", func(context.Context) {
				if err := updateUserNameCache(id, username); err != nil {
					common.SysLog("failed to update user name cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		username, err := getUserNameCache(id)
		if err == nil {
			return username, nil
		}
		// Don't return error - fall through to DB
	}
	fromDB = true
	err = DB.Model(&User{}).Where("id = ?", id).Select("username").Find(&username).Error
	if err != nil {
		return "", err
	}

	return username, nil
}

func IsLinuxDOIdAlreadyTaken(linuxDOId string) bool {
	var user User
	err := DB.Unscoped().Where("linux_do_id = ?", linuxDOId).First(&user).Error
	return !errors.Is(err, gorm.ErrRecordNotFound)
}

func (user *User) FillUserByLinuxDOId() error {
	if user.LinuxDOId == "" {
		return errors.New("linux do id is empty")
	}
	err := DB.Where("linux_do_id = ?", user.LinuxDOId).First(user).Error
	return err
}

func RootUserExists() bool {
	var user User
	err := DB.Where("role = ?", common.RoleRootUser).First(&user).Error
	if err != nil {
		return false
	}
	return true
}

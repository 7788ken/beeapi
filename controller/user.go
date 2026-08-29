package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"

	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

var (
	errUserPasswordUnset    = errors.New("user password is not set")
	errOriginalPasswordFail = errors.New("original password is incorrect")
)

func Login(c *gin.Context) {
	if !common.PasswordLoginEnabled {
		common.ApiErrorI18n(c, i18n.MsgUserPasswordLoginDisabled)
		return
	}
	var loginRequest LoginRequest
	err := json.NewDecoder(c.Request.Body).Decode(&loginRequest)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	username := loginRequest.Username
	password := loginRequest.Password
	if username == "" || password == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	user := model.User{
		Username: username,
		Password: password,
	}
	err = user.ValidateAndFill()
	if err != nil {
		switch {
		case errors.Is(err, model.ErrDatabase):
			common.SysLog(fmt.Sprintf("Login database error for user %s: %v", username, err))
			common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		case errors.Is(err, model.ErrUserEmptyCredentials):
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		default:
			common.ApiErrorI18n(c, i18n.MsgUserUsernameOrPasswordError)
		}
		return
	}

	// 检查是否启用2FA
	if model.IsTwoFAEnabled(user.Id) {
		flowToken, _, err := service.CreateAuthFlow(service.AuthFlowSpec{
			Purpose:  service.AuthFlowPurposeTwoFA,
			Provider: "password",
			Intent:   "login",
			UserID:   user.Id,
		})
		if err != nil {
			common.ApiErrorI18n(c, i18n.MsgUserSessionSaveFailed)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message": i18n.T(c, i18n.MsgUserRequire2FA),
			"success": true,
			"data": map[string]interface{}{
				"require_2fa": true,
				"flow_token":  flowToken,
			},
		})
		return
	}

	setupLogin(&user, c, "password")
}

// setupLogin persists and consumes the single-step AuthFlow before issuing the
// target dashboard access/refresh identity.
func setupLogin(user *model.User, c *gin.Context, loginMethod string) {
	// 账号有效期校验：ExpiresAt>0 且已过期则拒绝登录（所有登录路径汇聚于此：密码 / OAuth /
	// Passkey / Telegram / 2FA 都经过本函数）。0 = 永不过期（默认，普通注册）。
	if user.ExpiresAt > 0 && user.ExpiresAt < common.GetTimestamp() {
		common.ApiErrorI18n(c, i18n.MsgUserExpired)
		return
	}
	model.UpdateUserLastLoginAt(user.Id)
	flowToken, _, err := service.CreateAuthFlow(service.AuthFlowSpec{
		Purpose:  service.AuthFlowPurposeLogin,
		Provider: loginMethod,
		Intent:   "login",
		UserID:   user.Id,
	})
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserSessionSaveFailed)
		return
	}
	bundle, err := service.ConsumeLoginAuthFlow(
		flowToken,
		service.AuthFlowPurposeLogin,
		loginMethod,
		c.ClientIP(),
		c.Request.UserAgent(),
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	writeLoginSuccess(c, user, bundle)
}

func writeLoginSuccess(c *gin.Context, user *model.User, bundle *service.AuthBundle) {
	writeRefreshCookie(c, bundle)
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
		"data": map[string]any{
			"id":                user.Id,
			"username":          user.Username,
			"display_name":      user.DisplayName,
			"role":              user.Role,
			"status":            user.Status,
			"group":             user.Group,
			"access_token":      bundle.AccessToken,
			"token_type":        bundle.TokenType,
			"access_expires_at": bundle.AccessExpiresAt,
			"session":           bundle.Session,
		},
	})
}

func RefreshDashboardSession(c *gin.Context) {
	refreshToken, _ := c.Cookie("refresh_token")
	if refreshToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgAuthNotLoggedIn),
		})
		return
	}
	bundle, err := service.RefreshLoginSession(refreshToken)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, service.ErrRefreshTokenInvalid),
			errors.Is(err, service.ErrLoginSessionRevoked):
			status = http.StatusUnauthorized
		case errors.Is(err, service.ErrRefreshRace):
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	writeAuthBundle(c, bundle)
}

func Logout(c *gin.Context) {
	refreshToken, _ := c.Cookie("refresh_token")
	if refreshToken != "" {
		if err := service.RevokeByRefreshToken(refreshToken, "logout"); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("refresh_token", "", -1, "/api/user", "", common.SessionCookieSecure, true)
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
	})
}

func writeAuthBundle(c *gin.Context, bundle *service.AuthBundle) {
	writeRefreshCookie(c, bundle)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"access_token":      bundle.AccessToken,
			"token_type":        bundle.TokenType,
			"access_expires_at": bundle.AccessExpiresAt,
			"session":           bundle.Session,
		},
	})
}

func writeRefreshCookie(c *gin.Context, bundle *service.AuthBundle) {
	maxAge := int(bundle.Session.ExpiresAt - time.Now().Unix())
	if maxAge < 1 {
		maxAge = 1
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("refresh_token", bundle.RefreshToken, maxAge, "/api/user", "", common.SessionCookieSecure, true)
}

func Register(c *gin.Context) {
	if !common.RegisterEnabled {
		common.ApiErrorI18n(c, i18n.MsgUserRegisterDisabled)
		return
	}
	if !common.PasswordRegisterEnabled {
		common.ApiErrorI18n(c, i18n.MsgUserPasswordRegisterDisabled)
		return
	}
	var user model.User
	err := json.NewDecoder(c.Request.Body).Decode(&user)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserInputInvalid, map[string]any{"Error": err.Error()})
		return
	}
	user.Email = model.NormalizeEmail(user.Email)
	if common.EmailVerificationEnabled {
		if user.Email == "" || user.VerificationCode == "" {
			common.ApiErrorI18n(c, i18n.MsgUserEmailVerificationRequired)
			return
		}
		if !common.VerifyCodeWithKey(user.Email, user.VerificationCode, common.EmailVerificationPurpose) {
			common.ApiErrorI18n(c, i18n.MsgUserVerificationCodeError)
			return
		}
		if err := model.EnsureEmailAvailable(user.Email, 0); err != nil {
			if errors.Is(err, model.ErrEmailAlreadyTaken) {
				common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
				return
			}
			common.ApiErrorI18n(c, i18n.MsgDatabaseError)
			return
		}
	}
	emailForExistCheck := ""
	if common.EmailVerificationEnabled {
		emailForExistCheck = user.Email
	}
	exist, err := model.CheckUserExistOrDeleted(user.Username, emailForExistCheck)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		common.SysLog(fmt.Sprintf("CheckUserExistOrDeleted error: %v", err))
		return
	}
	if exist {
		common.ApiErrorI18n(c, i18n.MsgUserExists)
		return
	}
	affCode := user.AffCode // this code is the inviter's code, not the user's own code
	inviterId, _ := model.GetUserIdByAffCode(affCode)
	cleanUser := model.User{
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.Username,
		InviterId:   inviterId,
		Role:        common.RoleCommonUser, // 明确设置角色为普通用户
	}
	if common.EmailVerificationEnabled {
		cleanUser.Email = user.Email
	}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if insertErr := cleanUser.InsertWithTx(tx, inviterId); insertErr != nil {
			return insertErr
		}
		registrationToken, _, createErr := service.CreateAuthFlowWithTx(tx, service.AuthFlowSpec{
			Purpose:  service.AuthFlowPurposeRegistration,
			Provider: "password",
			Intent:   "register",
			UserID:   cleanUser.Id,
			Payload: map[string]any{
				"inviter_id": inviterId,
				"aff_code":   affCode,
			},
		})
		if createErr != nil {
			return createErr
		}
		_, consumeErr := service.ConsumeBoundAuthFlowWithTx(
			tx,
			registrationToken,
			service.AuthFlowPurposeRegistration,
			"password",
			"register",
			cleanUser.Id,
			time.Now(),
			nil,
		)
		if consumeErr != nil {
			return consumeErr
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, model.ErrEmailAlreadyTaken) {
			common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
			return
		}
		common.ApiError(c, err)
		return
	}

	cleanUser.FinalizeOAuthUserCreation(inviterId)
	insertedUser := cleanUser
	// 生成默认令牌
	if constant.GenerateDefaultToken {
		key, err := common.GenerateKey()
		if err != nil {
			common.ApiErrorI18n(c, i18n.MsgUserDefaultTokenFailed)
			common.SysLog("failed to generate token key: " + err.Error())
			return
		}
		// 生成默认令牌
		token := model.Token{
			UserId:             insertedUser.Id, // 使用插入后的用户ID
			Name:               cleanUser.Username + "的初始令牌",
			Key:                key,
			CreatedTime:        common.GetTimestamp(),
			AccessedTime:       common.GetTimestamp(),
			ExpiredTime:        -1,     // 永不过期
			RemainQuota:        500000, // 示例额度
			UnlimitedQuota:     true,
			ModelLimitsEnabled: false,
		}
		if setting.DefaultUseAutoGroup {
			token.Group = "auto"
		}
		if err := token.Insert(); err != nil {
			common.ApiErrorI18n(c, i18n.MsgCreateDefaultTokenErr)
			return
		}
	}

	bundle, err := service.CreateLoginSession(
		insertedUser.Id,
		"registration",
		c.ClientIP(),
		c.Request.UserAgent(),
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	writeLoginSuccess(c, &insertedUser, bundle)
}

func GetAllUsers(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	// 接口级排序：?order_by=rpm_24h&order=desc。白名单见 model.buildUserOrderClause。
	orderBy := c.Query("order_by")
	order := c.Query("order")

	// RPM 排序特殊路径：rpm_24h 字段在 DB 已废（UserMetricsTask 已禁用），值始终为 0/陈旧；
	// 显示值由 overlayRealtimeUserRPM 用 Redis 60s 滑动窗口实时覆盖。如果仍走 DB ORDER BY，
	// 会出现"排序按陈旧 DB 值、显示按 Redis 值"的不一致。改用 Go 层按 Redis RPM 排序再翻页。
	// （prod 用户总数 ~238，全量加载 ID + RPM map 内存代价可忽略。）
	if orderBy == "rpm_24h" {
		users, total, err := listUsersOrderedByRealtimeRPM(c.Request.Context(), pageInfo, order)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		pageInfo.SetTotal(int(total))
		pageInfo.SetItems(toSafeUserResponses(users))
		common.ApiSuccess(c, pageInfo)
		return
	}

	users, total, err := model.GetAllUsersOrdered(pageInfo, orderBy, order)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	overlayRealtimeUserRPM(users)

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(toSafeUserResponses(users))

	common.ApiSuccess(c, pageInfo)
	return
}

// listUsersOrderedByRealtimeRPM 按 Redis 实时 RPM 排序后翻页。
// 流程：DB 拿全部活跃 user ID → Redis BatchGetUserRPMs → 排序 → 切页 → DB 加载该页用户。
// 适合 admin 用户量小的场景（<10k）。order=desc 时 RPM 高的在前。
func listUsersOrderedByRealtimeRPM(ctx context.Context, pageInfo *common.PageInfo, order string) ([]*model.User, int64, error) {
	var ids []int
	if err := model.DB.WithContext(ctx).Model(&model.User{}).Pluck("id", &ids).Error; err != nil {
		return nil, 0, err
	}
	total := int64(len(ids))
	if total == 0 {
		return []*model.User{}, 0, nil
	}

	rpmMap := common.BatchGetUserRPMs(ids)
	desc := order != "asc"
	sort.SliceStable(ids, func(i, j int) bool {
		ri, rj := rpmMap[ids[i]], rpmMap[ids[j]]
		if ri == rj {
			// 二级排序：id desc 保稳定（同 buildUserOrderClause 默认）
			return ids[i] > ids[j]
		}
		if desc {
			return ri > rj
		}
		return ri < rj
	})

	start := pageInfo.GetStartIdx()
	end := start + pageInfo.GetPageSize()
	if start >= len(ids) {
		return []*model.User{}, total, nil
	}
	if end > len(ids) {
		end = len(ids)
	}
	pageIds := ids[start:end]

	var users []*model.User
	if err := model.DB.WithContext(ctx).Where("id IN ?", pageIds).Omit("password", "access_token").Find(&users).Error; err != nil {
		return nil, 0, err
	}

	// DB IN 查询返回顺序不一定与 pageIds 一致，重新按 pageIds 顺序排列
	userByID := make(map[int]*model.User, len(users))
	for _, u := range users {
		userByID[u.Id] = u
	}
	ordered := make([]*model.User, 0, len(pageIds))
	for _, id := range pageIds {
		if u, ok := userByID[id]; ok {
			ordered = append(ordered, u)
		}
	}

	// 把 Redis RPM 值覆盖到 Rpm24h 字段（与显示值一致）
	now := common.GetTimestamp()
	for _, u := range ordered {
		u.Rpm24h = rpmMap[u.Id]
		u.RpmUpdatedAt = now
	}
	return ordered, total, nil
}

// overlayRealtimeUserRPM 用 Redis 实时桶覆盖 Rpm24h 字段（"最近 1 分钟实际请求数"）。
// 失败/为零保持原 DB 值，避免无 Redis 时整列归零。
func overlayRealtimeUserRPM(users []*model.User) {
	if len(users) == 0 {
		return
	}
	ids := make([]int, 0, len(users))
	for _, u := range users {
		if u != nil {
			ids = append(ids, u.Id)
		}
	}
	rpmMap := common.BatchGetUserRPMs(ids)
	now := common.GetTimestamp()
	for _, u := range users {
		if u == nil {
			continue
		}
		u.Rpm24h = rpmMap[u.Id]
		u.RpmUpdatedAt = now
	}
}

// RecomputeUserMetrics 手动触发 RPM 重算（admin-only，CriticalRateLimit 防刷）。
func RecomputeUserMetrics(c *gin.Context) {
	err := backgroundtask.Submit("manual-user-metrics", func(context.Context) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		_ = service.RecomputeUserMetricsOnce(ctx)
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"triggered": true, "started_at": time.Now().Unix()})
}

func SearchUsers(c *gin.Context) {
	keyword := c.Query("keyword")
	group := c.Query("group")
	pageInfo := common.GetPageQuery(c)
	users, total, err := model.SearchUsers(keyword, group, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}

	overlayRealtimeUserRPM(users)

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(toSafeUserResponses(users))
	common.ApiSuccess(c, pageInfo)
	return
}

type safeUserResponse struct {
	*model.User
	Password         string  `json:"password,omitempty"`
	OriginalPassword string  `json:"original_password,omitempty"`
	VerificationCode string  `json:"verification_code,omitempty"`
	AccessToken      *string `json:"access_token,omitempty"`
	// AdminPerms 是「实际生效」的管理员权限（未配置的管理员会展开成默认全开，
	// root 展开成全部模块权限），前端权限弹窗直接按它渲染勾选状态。
	AdminPerms []string `json:"admin_perms"`
}

func toSafeUserResponse(user *model.User) safeUserResponse {
	return safeUserResponse{User: user, AdminPerms: user.EffectiveAdminPerms()}
}

func toSafeUserResponses(users []*model.User) []safeUserResponse {
	responses := make([]safeUserResponse, 0, len(users))
	for _, user := range users {
		if user != nil {
			responses = append(responses, toSafeUserResponse(user))
		}
	}
	return responses
}

func GetUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if myRole <= user.Role && myRole != common.RoleRootUser {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    toSafeUserResponse(user),
	})
	return
}

func GenerateAccessToken(c *gin.Context) {
	id := c.GetInt("id")
	user, err := model.GetUserById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// get rand int 28-32
	randI := common.GetRandomInt(4)
	key, err := common.GenerateRandomKey(29 + randI)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgGenerateFailed)
		common.SysLog("failed to generate key: " + err.Error())
		return
	}
	user.SetAccessToken(key)

	if model.DB.Where("access_token = ?", user.AccessToken).First(user).RowsAffected != 0 {
		common.ApiErrorI18n(c, i18n.MsgUuidDuplicate)
		return
	}

	// 单列写入 access_token，避免全行写回覆盖计费字段（丢失更新竞态）
	if err := model.UpdateUserAccessTokenColumn(id, key); err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    key,
	})
	return
}

type TransferAffQuotaRequest struct {
	Quota int `json:"quota" binding:"required"`
}

func TransferAffQuota(c *gin.Context) {
	id := c.GetInt("id")
	user, err := model.GetUserById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	tran := TransferAffQuotaRequest{}
	if err := c.ShouldBindJSON(&tran); err != nil {
		common.ApiError(c, err)
		return
	}
	err = user.TransferAffQuotaToQuota(tran.Quota, c.ClientIP())
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserTransferFailed, map[string]any{"Error": err.Error()})
		return
	}
	common.ApiSuccessI18n(c, i18n.MsgUserTransferSuccess, nil)
}

// maskUsername 隐私脱敏：邮箱保留首末字符 + 域名；普通用户名保留首末，中间打星。
// 与前端 web/default 的 maskName 规则一致；后端先脱敏避免明文邮箱通过 API 泄露。
func maskUsername(s string) string {
	if s == "" {
		return ""
	}
	if at := indexOfAt(s); at > 0 {
		local := s[:at]
		domain := s[at:]
		switch len(local) {
		case 1:
			return local + "***" + domain
		case 2:
			return string(local[0]) + "*" + domain
		case 3:
			return string(local[0]) + "*" + string(local[2]) + domain
		default:
			return string(local[0]) + repeatStar(len(local)-2) + string(local[len(local)-1]) + domain
		}
	}
	switch len(s) {
	case 1:
		return "*"
	case 2:
		return string(s[0]) + "*"
	case 3:
		return string(s[0]) + "*" + string(s[2])
	default:
		return string(s[0]) + repeatStar(len(s)-2) + string(s[len(s)-1])
	}
}

func indexOfAt(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '@' {
			return i
		}
	}
	return -1
}

func repeatStar(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = '*'
	}
	return string(b)
}

// GetMyInvitees 返回当前用户邀请的所有用户列表 + 累计分成统计。
func GetMyInvitees(c *gin.Context) {
	id := c.GetInt("id")
	invitees, err := model.GetInviteesByInviterId(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	stats, err := model.GetCommissionStatsByInviter(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	statMap := make(map[int]model.AffiliateCommissionStat, len(stats))
	for _, s := range stats {
		statMap[s.InviteeId] = s
	}

	type InviteeView struct {
		Id              int    `json:"id"`
		Username        string `json:"username"`
		DisplayName     string `json:"display_name"`
		CreatedAt       int64  `json:"created_at"`
		UsedQuota       int    `json:"used_quota"`
		Status          int    `json:"status"`
		TotalCommission int    `json:"total_commission"`
		TotalConsume    int    `json:"total_consume"`
	}
	views := make([]InviteeView, 0, len(invitees))
	for _, inv := range invitees {
		s := statMap[inv.Id]
		views = append(views, InviteeView{
			Id:              inv.Id,
			Username:        maskUsername(inv.Username),
			DisplayName:     maskUsername(inv.DisplayName),
			CreatedAt:       inv.CreatedAt,
			UsedQuota:       inv.UsedQuota,
			Status:          inv.Status,
			TotalCommission: s.TotalCommission,
			TotalConsume:    s.TotalConsume,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"invitees":         views,
			"commission_ratio": common.AffiliateCommissionRatio,
			"enabled":          common.AffiliateCommissionEnabled,
		},
	})
}

func GetAffCode(c *gin.Context) {
	id := c.GetInt("id")
	user, err := model.GetUserById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user.AffCode == "" {
		user.AffCode = common.GetRandomString(4)
		if err := model.UpdateUserAffCodeColumn(id, user.AffCode); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    user.AffCode,
	})
	return
}

func GetSelf(c *gin.Context) {
	id := c.GetInt("id")
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// Hide admin remarks: set to empty to trigger omitempty tag, ensuring the remark field is not included in JSON returned to regular users
	user.Remark = ""

	// 计算用户权限信息
	permissions := calculateUserPermissions(user)

	// 获取用户设置并提取sidebar_modules
	userSetting := user.GetSetting()

	// 构建响应数据，包含用户信息和权限
	responseData := map[string]interface{}{
		"id":                user.Id,
		"username":          user.Username,
		"display_name":      user.DisplayName,
		"role":              user.Role,
		"status":            user.Status,
		"email":             user.Email,
		"github_id":         user.GitHubId,
		"discord_id":        user.DiscordId,
		"oidc_id":           user.OidcId,
		"wechat_id":         user.WeChatId,
		"telegram_id":       user.TelegramId,
		"group":             user.Group,
		"quota":             user.Quota,
		"used_quota":        user.UsedQuota,
		"request_count":     user.RequestCount,
		"aff_code":          user.AffCode,
		"aff_count":         user.AffCount,
		"aff_quota":         user.AffQuota,
		"aff_history_quota": user.AffHistoryQuota,
		"inviter_id":        user.InviterId,
		"linux_do_id":       user.LinuxDOId,
		"setting":           user.Setting,
		"stripe_customer":   user.StripeCustomer,
		"sidebar_modules":   userSetting.SidebarModules, // 正确提取sidebar_modules字段
		"permissions":       permissions,                // 新增权限字段
		"admin_perms":       user.EffectiveAdminPerms(), // 管理员细粒度权限（普通用户为空）
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    responseData,
	})
	return
}

// adminPermFlags 把权限 key 列表摊平成前端好判断的布尔字段
func adminPermFlags(user *model.User) map[string]bool {
	return map[string]bool{
		"channel_view":      user.HasAdminPerm(model.AdminPermChannelView),
		"channel_edit":      user.HasAdminPerm(model.AdminPermChannelEdit),
		"log_view":          user.HasAdminPerm(model.AdminPermLogView),
		"quota_grant":       user.HasAdminPerm(model.AdminPermQuotaGrant),
		"user_manage":       user.HasAdminPerm(model.AdminPermUserManage),
		"quota_deduct_self": user.HasAdminPerm(model.AdminPermQuotaDeductSelf),
	}
}

// 计算用户权限的辅助函数
func calculateUserPermissions(user *model.User) map[string]interface{} {
	userRole := user.Role
	permissions := map[string]interface{}{
		// admin 段是超级管理员给该管理员配置的细粒度权限，前端据此隐藏入口。
		// 后端每个接口自己也会再校验一次，前端这层只负责别让人点到 403。
		"admin": adminPermFlags(user),
	}

	// 根据用户角色计算权限
	if userRole == common.RoleRootUser {
		// 超级管理员不需要边栏设置功能
		permissions["sidebar_settings"] = false
		permissions["sidebar_modules"] = map[string]interface{}{}
	} else if userRole == common.RoleAdminUser {
		// 管理员可以设置边栏，但不包含系统设置功能
		permissions["sidebar_settings"] = true
		permissions["sidebar_modules"] = map[string]interface{}{
			"admin": map[string]interface{}{
				"setting": false, // 管理员不能访问系统设置
			},
		}
	} else {
		// 普通用户只能设置个人功能，不包含管理员区域
		permissions["sidebar_settings"] = true
		permissions["sidebar_modules"] = map[string]interface{}{
			"admin": false, // 普通用户不能访问管理员区域
		}
	}

	return permissions
}

// 根据用户角色生成默认的边栏配置
func generateDefaultSidebarConfig(userRole int) string {
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

func GetUserModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		id = c.GetInt("id")
	}
	user, err := model.GetUserCache(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groups := service.GetUserUsableGroups(user.Group)
	var models []string
	for group := range groups {
		for _, g := range model.GetGroupEnabledModels(group) {
			if !common.StringsContains(models, g) {
				models = append(models, g)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    models,
	})
	return
}

func UpdateUser(c *gin.Context) {
	var updatedUser model.User
	err := json.NewDecoder(c.Request.Body).Decode(&updatedUser)
	if err != nil || updatedUser.Id == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if updatedUser.Password == "" {
		updatedUser.Password = "$I_LOVE_U" // make Validator happy :)
	}
	if err := common.Validate.Struct(&updatedUser); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserInputInvalid, map[string]any{"Error": err.Error()})
		return
	}
	originUser, err := model.GetUserById(updatedUser.Id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if myRole <= originUser.Role && myRole != common.RoleRootUser {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionHigherLevel)
		return
	}
	if myRole <= updatedUser.Role && myRole != common.RoleRootUser {
		common.ApiErrorI18n(c, i18n.MsgUserCannotCreateHigherLevel)
		return
	}
	if updatedUser.Password == "$I_LOVE_U" {
		updatedUser.Password = "" // rollback to what it should be
	}
	updatePassword := updatedUser.Password != ""
	if err := updatedUser.Edit(updatePassword); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func AdminClearUserBinding(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	bindingType := strings.ToLower(strings.TrimSpace(c.Param("binding_type")))
	if bindingType == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	myRole := c.GetInt("role")
	if myRole <= user.Role && myRole != common.RoleRootUser {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
		return
	}

	if err := user.ClearBinding(bindingType); err != nil {
		common.ApiError(c, err)
		return
	}

	model.RecordLog(user.Id, model.LogTypeManage, fmt.Sprintf("admin cleared %s binding for user %s", bindingType, user.Username))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "success",
	})
}

func UpdateSelf(c *gin.Context) {
	var requestData map[string]interface{}
	err := json.NewDecoder(c.Request.Body).Decode(&requestData)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	// 检查是否是用户设置更新请求 (sidebar_modules 或 language)
	if sidebarModules, sidebarExists := requestData["sidebar_modules"]; sidebarExists {
		userId := c.GetInt("id")
		user, err := model.GetUserById(userId, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}

		// 获取当前用户设置
		currentSetting := user.GetSetting()

		// 更新sidebar_modules字段
		if sidebarModulesStr, ok := sidebarModules.(string); ok {
			currentSetting.SidebarModules = sidebarModulesStr
		}

		// 保存更新后的设置
		user.SetSetting(currentSetting)
		if err := model.UpdateUserSettingColumn(userId, user.Setting); err != nil {
			common.ApiErrorI18n(c, i18n.MsgUpdateFailed)
			return
		}

		common.ApiSuccessI18n(c, i18n.MsgUpdateSuccess, nil)
		return
	}

	// 检查是否是语言偏好更新请求
	if language, langExists := requestData["language"]; langExists {
		userId := c.GetInt("id")
		user, err := model.GetUserById(userId, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}

		// 获取当前用户设置
		currentSetting := user.GetSetting()

		// 更新language字段
		if langStr, ok := language.(string); ok {
			currentSetting.Language = langStr
		}

		// 保存更新后的设置
		user.SetSetting(currentSetting)
		if err := model.UpdateUserSettingColumn(userId, user.Setting); err != nil {
			common.ApiErrorI18n(c, i18n.MsgUpdateFailed)
			return
		}

		common.ApiSuccessI18n(c, i18n.MsgUpdateSuccess, nil)
		return
	}

	// 原有的用户信息更新逻辑
	var user model.User
	requestDataBytes, err := json.Marshal(requestData)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	err = json.Unmarshal(requestDataBytes, &user)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	if user.Password == "" {
		user.Password = "$I_LOVE_U" // make Validator happy :)
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidInput)
		return
	}

	cleanUser := model.User{
		Id:          c.GetInt("id"),
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.DisplayName,
	}
	if user.Password == "$I_LOVE_U" {
		user.Password = "" // rollback to what it should be
		cleanUser.Password = ""
	}
	updatePassword, err := checkUpdatePassword(user.OriginalPassword, user.Password, cleanUser.Id)
	if err != nil {
		if errors.Is(err, errUserPasswordUnset) {
			common.ApiErrorI18n(c, i18n.MsgUserPasswordUnset)
			return
		}
		if errors.Is(err, errOriginalPasswordFail) {
			common.ApiErrorI18n(c, i18n.MsgUserOriginalPasswordError)
			return
		}
		common.ApiError(c, err)
		return
	}
	if err := cleanUser.Update(updatePassword); err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func checkUpdatePassword(originalPassword string, newPassword string, userId int) (updatePassword bool, err error) {
	if newPassword == "" {
		return
	}
	var currentUser *model.User
	currentUser, err = model.GetUserById(userId, true)
	if err != nil {
		return
	}

	// 修改密码必须校验原密码。账号本身没有密码时（如仅 OAuth 绑定），
	// 不允许无原密码直接设置新密码，避免账号被无凭据接管，需走密码重置或管理员重置。
	if currentUser.Password == "" {
		err = errUserPasswordUnset
		return
	}
	if !common.ValidatePasswordAndHash(originalPassword, currentUser.Password) {
		err = errOriginalPasswordFail
		return
	}
	updatePassword = true
	return
}

func DeleteUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	originUser, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if myRole <= originUser.Role {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionHigherLevel)
		return
	}
	err = model.HardDeleteUserById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func DeleteSelf(c *gin.Context) {
	id := c.GetInt("id")
	user, _ := model.GetUserById(id, false)

	if user.Role == common.RoleRootUser {
		common.ApiErrorI18n(c, i18n.MsgUserCannotDeleteRootUser)
		return
	}

	err := model.DeleteUserById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func GetPinnedUsers(c *gin.Context) {
	users, err := model.GetPinnedUsers()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    users,
	})
}

func CreateUser(c *gin.Context) {
	var user model.User
	err := json.NewDecoder(c.Request.Body).Decode(&user)
	user.Username = strings.TrimSpace(user.Username)
	if err != nil || user.Username == "" || user.Password == "" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUserInputInvalid, map[string]any{"Error": err.Error()})
		return
	}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	myRole := c.GetInt("role")
	if user.Role >= myRole {
		common.ApiErrorI18n(c, i18n.MsgUserCannotCreateHigherLevel)
		return
	}
	// Even for admin users, we cannot fully trust them!
	cleanUser := model.User{
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.DisplayName,
		Role:        user.Role, // 保持管理员设置的角色
	}
	if err := cleanUser.Insert(0); err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

type ManageRequest struct {
	Id     int    `json:"id"`
	Action string `json:"action"`
	Value  int    `json:"value"`
	Mode   string `json:"mode"`
}

// ManageUser Only admin user can do this
func ManageUser(c *gin.Context) {
	var req ManageRequest
	err := json.NewDecoder(c.Request.Body).Decode(&req)

	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	user := model.User{
		Id: req.Id,
	}
	// Fill attributes
	model.DB.Unscoped().Where(&user).First(&user)
	if user.Id == 0 {
		common.ApiErrorI18n(c, i18n.MsgUserNotExists)
		return
	}
	myRole := c.GetInt("role")
	if myRole <= user.Role && myRole != common.RoleRootUser {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionHigherLevel)
		return
	}
	// 细粒度权限：调额度走「调整额度」，其余动作走「管理用户」。
	// root 直接放行，也不用读库——超级管理员恒有全部模块权限。
	operatorPerms := ""
	if myRole < common.RoleRootUser {
		requiredPerm := model.AdminPermUserManage
		if req.Action == "add_quota" {
			requiredPerm = model.AdminPermQuotaGrant
		}
		operatorPerms, err = model.GetUserAdminPermsRaw(c.GetInt("id"))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if !model.HasAdminPermFor(myRole, operatorPerms, requiredPerm) {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": common.TranslateMessage(c, i18n.MsgAuthAdminPermDenied),
			})
			return
		}
	}
	switch req.Action {
	case "disable":
		user.Status = common.UserStatusDisabled
		if user.Role == common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserCannotDisableRootUser)
			return
		}
	case "enable":
		user.Status = common.UserStatusEnabled
	case "delete":
		if user.Role == common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserCannotDeleteRootUser)
			return
		}
		if err := user.Delete(); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data": model.User{
				Role:   user.Role,
				Status: user.Status,
			},
		})
		return
	case "promote":
		if myRole != common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserAdminCannotPromote)
			return
		}
		if user.Role >= common.RoleAdminUser {
			common.ApiErrorI18n(c, i18n.MsgUserAlreadyAdmin)
			return
		}
		user.Role = common.RoleAdminUser
	case "demote":
		if user.Role == common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserCannotDemoteRootUser)
			return
		}
		if user.Role == common.RoleCommonUser {
			common.ApiErrorI18n(c, i18n.MsgUserAlreadyCommon)
			return
		}
		user.Role = common.RoleCommonUser
	case "promote_root":
		if myRole != common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserAdminCannotPromote)
			return
		}
		if user.Role != common.RoleAdminUser {
			common.ApiErrorI18n(c, i18n.MsgUserPromoteRootRequireAdmin)
			return
		}
		user.Role = common.RoleRootUser
	case "demote_root":
		if c.GetInt("id") != 1 {
			common.ApiErrorI18n(c, i18n.MsgUserDemoteRootRequireFounder)
			return
		}
		if user.Id == 1 {
			common.ApiErrorI18n(c, i18n.MsgUserDemoteRootCannotSelf)
			return
		}
		if user.Role != common.RoleRootUser {
			common.ApiErrorI18n(c, i18n.MsgUserDemoteRootRequireRoot)
			return
		}
		user.Role = common.RoleAdminUser
	case "add_quota":
		adminName := c.GetString("username")
		adminId := c.GetInt("id")
		adminInfo := map[string]interface{}{
			"admin_id":       adminId,
			"admin_username": adminName,
		}
		// 非超级管理员只能给普通用户「增加」额度：扣减和覆盖（含归零）一律拒绝
		if myRole != common.RoleRootUser {
			if user.Role != common.RoleCommonUser {
				common.ApiErrorI18n(c, i18n.MsgUserQuotaAdjustCommonOnly)
				return
			}
			if req.Mode != "add" {
				common.ApiErrorI18n(c, i18n.MsgUserQuotaAdjustAddOnly)
				return
			}
		}
		switch req.Mode {
		case "add":
			if req.Value <= 0 {
				common.ApiErrorI18n(c, i18n.MsgUserQuotaChangeZero)
				return
			}
			// 开了「充值扣自己额度」的管理员：本次增加的额度从他自己账户里划走，不足则整笔拒绝
			if model.HasAdminPermFor(myRole, operatorPerms, model.AdminPermQuotaDeductSelf) && adminId != user.Id {
				if err := model.TransferUserQuota(adminId, user.Id, req.Value); err != nil {
					if errors.Is(err, model.ErrInsufficientUserQuota) {
						common.ApiErrorI18n(c, i18n.MsgUserQuotaGrantSelfInsufficent)
						return
					}
					common.ApiError(c, err)
					return
				}
				model.RecordLogWithAdminInfo(user.Id, model.LogTypeManage,
					fmt.Sprintf("管理员增加用户额度 %s（从管理员 %s 的额度中扣除）", logger.LogQuota(req.Value), adminName), adminInfo)
				model.RecordLogWithAdminInfo(adminId, model.LogTypeManage,
					fmt.Sprintf("给用户 %s 充值，扣减自身额度 %s", user.Username, logger.LogQuota(req.Value)), adminInfo)
			} else {
				if err := model.IncreaseUserQuota(user.Id, req.Value); err != nil {
					common.ApiError(c, err)
					return
				}
				model.RecordLogWithAdminInfo(user.Id, model.LogTypeManage,
					fmt.Sprintf("管理员增加用户额度 %s", logger.LogQuota(req.Value)), adminInfo)
			}
		case "subtract":
			if req.Value <= 0 {
				common.ApiErrorI18n(c, i18n.MsgUserQuotaChangeZero)
				return
			}
			if err := model.DecreaseUserQuota(user.Id, req.Value); err != nil {
				common.ApiError(c, err)
				return
			}
			model.RecordLogWithAdminInfo(user.Id, model.LogTypeManage,
				fmt.Sprintf("管理员减少用户额度 %s", logger.LogQuota(req.Value)), adminInfo)
		case "override":
			oldQuota := user.Quota
			if err := model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", req.Value).Error; err != nil {
				common.ApiError(c, err)
				return
			}
			model.RecordLogWithAdminInfo(user.Id, model.LogTypeManage,
				fmt.Sprintf("管理员覆盖用户额度从 %s 为 %s", logger.LogQuota(oldQuota), logger.LogQuota(req.Value)), adminInfo)
		default:
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
		})
		return
	default:
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	if err := model.UpdateUserRoleStatusAndBumpAuthVersion(
		user.Id,
		user.Role,
		user.Status,
	); err != nil {
		common.ApiError(c, err)
		return
	}
	clearUser := model.User{
		Role:   user.Role,
		Status: user.Status,
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    clearUser,
	})
	return
}

type updateAdminPermsRequest struct {
	AdminPerms []string `json:"admin_perms"`
}

// UpdateUserAdminPerms 超级管理员给管理员配置细粒度权限（路由已挂 RootAuth）。
// 权限判定每次现读库，因此这里不需要 bump auth version / 清用户缓存，改完即刻生效。
func UpdateUserAdminPerms(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	var req updateAdminPermsRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 只给管理员配：普通用户没有管理端入口，root 恒为全权限
	if user.Role != common.RoleAdminUser {
		common.ApiErrorI18n(c, i18n.MsgUserAdminPermsTargetInvalid)
		return
	}
	raw, err := model.NormalizeAdminPerms(req.AdminPerms)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err := model.UpdateUserAdminPerms(id, raw); err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordLogWithAdminInfo(id, model.LogTypeManage,
		fmt.Sprintf("超级管理员将该管理员权限设置为 [%s]", raw), map[string]interface{}{
			"admin_id":       c.GetInt("id"),
			"admin_username": c.GetString("username"),
		})
	user.AdminPerms = raw
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    user.EffectiveAdminPerms(),
	})
}

type emailBindRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func EmailBind(c *gin.Context) {
	var req emailBindRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiError(c, errors.New("invalid request body"))
		return
	}
	email := model.NormalizeEmail(req.Email)
	code := req.Code
	if !common.VerifyCodeWithKey(email, code, common.EmailVerificationPurpose) {
		common.ApiErrorI18n(c, i18n.MsgUserVerificationCodeError)
		return
	}
	id := c.GetInt("id")
	if id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgAuthNotLoggedIn)
		return
	}
	user := model.User{
		Id: id,
	}
	err := user.FillUserById()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 绑定邮箱前必须校验该邮箱是否已被他人占用，并在事务内串行化同一邮箱的并发绑定，
	// 避免两个账号共享同一邮箱（否则会破坏「邮箱↔账号」1:1 不变量，进而被密码重置接管）。
	if err := model.BindEmailToUser(&user, email); err != nil {
		if errors.Is(err, model.ErrEmailAlreadyTaken) {
			common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
			return
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

type topUpRequest struct {
	Key string `json:"key"`
}

var topUpLocks sync.Map
var topUpCreateLock sync.Mutex

type topUpTryLock struct {
	ch chan struct{}
}

func newTopUpTryLock() *topUpTryLock {
	return &topUpTryLock{ch: make(chan struct{}, 1)}
}

func (l *topUpTryLock) TryLock() bool {
	select {
	case l.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *topUpTryLock) Unlock() {
	select {
	case <-l.ch:
	default:
	}
}

func getTopUpLock(userID int) *topUpTryLock {
	if v, ok := topUpLocks.Load(userID); ok {
		return v.(*topUpTryLock)
	}
	topUpCreateLock.Lock()
	defer topUpCreateLock.Unlock()
	if v, ok := topUpLocks.Load(userID); ok {
		return v.(*topUpTryLock)
	}
	l := newTopUpTryLock()
	topUpLocks.Store(userID, l)
	return l
}

func TopUp(c *gin.Context) {
	id := c.GetInt("id")
	lock := getTopUpLock(id)
	if !lock.TryLock() {
		common.ApiErrorI18n(c, i18n.MsgUserTopUpProcessing)
		return
	}
	defer lock.Unlock()
	req := topUpRequest{}
	err := c.ShouldBindJSON(&req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	quota, err := model.Redeem(req.Key, id)
	if err != nil {
		if errors.Is(err, model.ErrRedeemFailed) {
			common.ApiErrorI18n(c, i18n.MsgRedeemFailed)
			return
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    quota,
	})
}

type UpdateUserSettingRequest struct {
	QuotaWarningType                 string  `json:"notify_type"`
	QuotaWarningThreshold            float64 `json:"quota_warning_threshold"`
	WebhookUrl                       string  `json:"webhook_url,omitempty"`
	WebhookSecret                    string  `json:"webhook_secret,omitempty"`
	NotificationEmail                string  `json:"notification_email,omitempty"`
	BarkUrl                          string  `json:"bark_url,omitempty"`
	GotifyUrl                        string  `json:"gotify_url,omitempty"`
	GotifyToken                      string  `json:"gotify_token,omitempty"`
	GotifyPriority                   int     `json:"gotify_priority,omitempty"`
	UpstreamModelUpdateNotifyEnabled *bool   `json:"upstream_model_update_notify_enabled,omitempty"`
	AcceptUnsetModelRatioModel       *bool   `json:"accept_unset_model_ratio_model,omitempty"`
	PriceChangeNotifyDisabled        *bool   `json:"price_change_notify_disabled,omitempty"`
	RecordIpLog                      bool    `json:"record_ip_log"`
}

func UpdateUserSetting(c *gin.Context) {
	var req UpdateUserSettingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	// 验证预警类型
	if req.QuotaWarningType != dto.NotifyTypeEmail && req.QuotaWarningType != dto.NotifyTypeWebhook && req.QuotaWarningType != dto.NotifyTypeBark && req.QuotaWarningType != dto.NotifyTypeGotify {
		common.ApiErrorI18n(c, i18n.MsgSettingInvalidType)
		return
	}

	// 验证预警阈值
	if req.QuotaWarningThreshold <= 0 {
		common.ApiErrorI18n(c, i18n.MsgQuotaThresholdGtZero)
		return
	}

	// 如果是webhook类型,验证webhook地址
	if req.QuotaWarningType == dto.NotifyTypeWebhook {
		if req.WebhookUrl == "" {
			common.ApiErrorI18n(c, i18n.MsgSettingWebhookEmpty)
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.WebhookUrl); err != nil {
			common.ApiErrorI18n(c, i18n.MsgSettingWebhookInvalid)
			return
		}
	}

	// 如果是邮件类型，验证邮箱地址
	if req.QuotaWarningType == dto.NotifyTypeEmail && req.NotificationEmail != "" {
		// 验证邮箱格式
		if !strings.Contains(req.NotificationEmail, "@") {
			common.ApiErrorI18n(c, i18n.MsgSettingEmailInvalid)
			return
		}
	}

	// 如果是Bark类型，验证Bark URL
	if req.QuotaWarningType == dto.NotifyTypeBark {
		if req.BarkUrl == "" {
			common.ApiErrorI18n(c, i18n.MsgSettingBarkUrlEmpty)
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.BarkUrl); err != nil {
			common.ApiErrorI18n(c, i18n.MsgSettingBarkUrlInvalid)
			return
		}
		// 检查是否是HTTP或HTTPS
		if !strings.HasPrefix(req.BarkUrl, "https://") && !strings.HasPrefix(req.BarkUrl, "http://") {
			common.ApiErrorI18n(c, i18n.MsgSettingUrlMustHttp)
			return
		}
	}

	// 如果是Gotify类型，验证Gotify URL和Token
	if req.QuotaWarningType == dto.NotifyTypeGotify {
		if req.GotifyUrl == "" {
			common.ApiErrorI18n(c, i18n.MsgSettingGotifyUrlEmpty)
			return
		}
		if req.GotifyToken == "" {
			common.ApiErrorI18n(c, i18n.MsgSettingGotifyTokenEmpty)
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.GotifyUrl); err != nil {
			common.ApiErrorI18n(c, i18n.MsgSettingGotifyUrlInvalid)
			return
		}
		// 检查是否是HTTP或HTTPS
		if !strings.HasPrefix(req.GotifyUrl, "https://") && !strings.HasPrefix(req.GotifyUrl, "http://") {
			common.ApiErrorI18n(c, i18n.MsgSettingUrlMustHttp)
			return
		}
	}

	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	existingSettings := user.GetSetting()
	upstreamModelUpdateNotifyEnabled := existingSettings.UpstreamModelUpdateNotifyEnabled
	if user.Role >= common.RoleAdminUser && req.UpstreamModelUpdateNotifyEnabled != nil {
		upstreamModelUpdateNotifyEnabled = *req.UpstreamModelUpdateNotifyEnabled
	}

	// 仅管理员可启用「接受未定价模型」开关，避免普通用户绕过模型白名单
	acceptUnsetRatioModel := existingSettings.AcceptUnsetRatioModel
	if user.Role >= common.RoleAdminUser && req.AcceptUnsetModelRatioModel != nil {
		acceptUnsetRatioModel = *req.AcceptUnsetModelRatioModel
	}

	// 价格变动通知退订偏好（倒装命名：零值=默认接收）；未传时保留原值，防止其他设置保存把它冲掉
	priceChangeNotifyDisabled := existingSettings.PriceChangeNotifyDisabled
	if req.PriceChangeNotifyDisabled != nil {
		priceChangeNotifyDisabled = *req.PriceChangeNotifyDisabled
	}

	// 构建设置
	settings := dto.UserSetting{
		NotifyType:                       req.QuotaWarningType,
		QuotaWarningThreshold:            req.QuotaWarningThreshold,
		UpstreamModelUpdateNotifyEnabled: upstreamModelUpdateNotifyEnabled,
		AcceptUnsetRatioModel:            acceptUnsetRatioModel,
		PriceChangeNotifyDisabled:        priceChangeNotifyDisabled,
		RecordIpLog:                      req.RecordIpLog,
	}

	// 如果是webhook类型,添加webhook相关设置
	if req.QuotaWarningType == dto.NotifyTypeWebhook {
		settings.WebhookUrl = req.WebhookUrl
		if req.WebhookSecret != "" {
			settings.WebhookSecret = req.WebhookSecret
		}
	}

	// 如果提供了通知邮箱，添加到设置中
	if req.QuotaWarningType == dto.NotifyTypeEmail && req.NotificationEmail != "" {
		settings.NotificationEmail = req.NotificationEmail
	}

	// 如果是Bark类型，添加Bark URL到设置中
	if req.QuotaWarningType == dto.NotifyTypeBark {
		settings.BarkUrl = req.BarkUrl
	}

	// 如果是Gotify类型，添加Gotify配置到设置中
	if req.QuotaWarningType == dto.NotifyTypeGotify {
		settings.GotifyUrl = req.GotifyUrl
		settings.GotifyToken = req.GotifyToken
		// Gotify优先级范围0-10，超出范围则使用默认值5
		if req.GotifyPriority < 0 || req.GotifyPriority > 10 {
			settings.GotifyPriority = 5
		} else {
			settings.GotifyPriority = req.GotifyPriority
		}
	}

	// 更新用户设置
	user.SetSetting(settings)
	if err := model.UpdateUserSettingColumn(userId, user.Setting); err != nil {
		common.ApiErrorI18n(c, i18n.MsgUpdateFailed)
		return
	}

	common.ApiSuccessI18n(c, i18n.MsgSettingSaved, nil)
}

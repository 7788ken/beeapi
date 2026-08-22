package model

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"

	"github.com/gin-gonic/gin"
)

// UserBase struct remains the same as it represents the cached data structure
type UserBase struct {
	Id          int    `json:"id"`
	Group       string `json:"group"`
	Email       string `json:"email"`
	Quota       int    `json:"quota"`
	Status      int    `json:"status"`
	Role        int    `json:"role"`
	Username    string `json:"username"`
	Setting     string `json:"setting"`
	RpmLimit    int    `json:"rpm_limit"`
	ExpiresAt   int64  `json:"expires_at"` // 0=永不过期；>now 时 TokenAuth/authHelper 拒绝
	AuthVersion int64  `json:"-"`
	CacheSchema int    `json:"-"`
}

func (user *UserBase) WriteContext(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
	common.SetContextKey(c, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(c, constant.ContextKeyUserStatus, user.Status)
	common.SetContextKey(c, constant.ContextKeyUserEmail, user.Email)
	common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(c, constant.ContextKeyUserRole, user.Role)
	common.SetContextKey(c, constant.ContextKeyUserSetting, user.GetSetting())
	common.SetContextKey(c, constant.ContextKeyUserRpmLimit, user.RpmLimit)
}

func (user *UserBase) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := common.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

// getUserCacheKey returns the key for user cache
func getUserCacheKey(userId int) string {
	return fmt.Sprintf("user:%d", userId)
}

// invalidateUserCache clears user cache
func invalidateUserCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisDelKey(getUserCacheKey(userId))
}

// InvalidateUserCache is the exported version of invalidateUserCache.
// 供 controller 等上层包在用户状态变更（如禁用、删除、角色变更）后主动清理缓存。
func InvalidateUserCache(userId int) error {
	return invalidateUserCache(userId)
}

// updateUserCache updates all user cache fields using hash
func updateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}

	return writeCacheWithBatchDeleteBarrier(
		BatchUpdateTypeUserQuota,
		user.Id,
		func() error {
			cache := user.ToBaseUser()
			cache.AuthVersion = user.AuthVersion
			cache.CacheSchema = userCacheSchemaVersion
			return writeUserAuthCache(cache)
		},
		func() error {
			return invalidateUserCache(user.Id)
		},
	)
}

// GetUserCache gets complete user cache from hash
func GetUserCache(userId int) (userCache *UserBase, err error) {
	var user *User
	var fromDB bool
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) && user != nil {
			_ = backgroundtask.Submit("user-cache-refill", func(context.Context) {
				if err := updateUserCache(*user); err != nil {
					common.SysLog("failed to update user status cache: " + err.Error())
				}
			})
		}
	}()

	// Try getting from Redis first
	userCache, err = cacheGetUserBase(userId)
	if err == nil {
		userCache.Quota, err = GetUserQuota(userId)
		if err != nil {
			return nil, err
		}
		return userCache, nil
	}

	// If Redis fails, get from DB
	fromDB = true
	user, err = GetUserById(userId, false)
	if err != nil {
		return nil, err // Return nil and error if DB lookup fails
	}
	if common.RedisEnabled {
		floor, floorErr := getUserAuthVersionFloor(userId)
		if floorErr == nil && floor > user.AuthVersion {
			return nil, ErrUserAuthCachePending
		}
	}

	// Create cache object from user data
	userCache = &UserBase{
		Id:          user.Id,
		Group:       user.Group,
		Quota:       user.Quota,
		Status:      user.Status,
		Role:        user.Role,
		Username:    user.Username,
		Setting:     user.Setting,
		Email:       user.Email,
		RpmLimit:    user.RpmLimit,
		ExpiresAt:   user.ExpiresAt,
		AuthVersion: user.AuthVersion,
		CacheSchema: userCacheSchemaVersion,
	}

	return userCache, nil
}

func cacheGetUserBase(userId int) (*UserBase, error) {
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var userCache UserBase
	// Try getting from Redis first
	err := common.RedisHGetObj(getUserCacheKey(userId), &userCache)
	if err != nil {
		return nil, err
	}
	if userCache.Id != userId ||
		userCache.CacheSchema != userCacheSchemaVersion ||
		userCache.AuthVersion <= 0 {
		return nil, fmt.Errorf("user cache schema is stale")
	}
	floor, err := getUserAuthVersionFloor(userId)
	if err != nil {
		return nil, err
	}
	if floor > userCache.AuthVersion {
		return nil, ErrUserAuthCachePending
	}
	return &userCache, nil
}

// Helper functions to get individual fields if needed
func getUserGroupCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Group, nil
}

func getUserStatusCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Status, nil
}

func getUserNameCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Username, nil
}

func getUserSettingCache(userId int) (dto.UserSetting, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return dto.UserSetting{}, err
	}
	return cache.GetSetting(), nil
}

// New functions for individual field updates
func updateUserStatusCache(userId int, _ bool) error {
	if !common.RedisEnabled {
		return nil
	}
	return PublishUserAuthCache(userId)
}

func updateUserGroupCache(userId int, _ string) error {
	if !common.RedisEnabled {
		return nil
	}
	return PublishUserAuthCache(userId)
}

func UpdateUserGroupCache(userId int, group string) error {
	return updateUserGroupCache(userId, group)
}

func updateUserNameCache(userId int, _ string) error {
	if !common.RedisEnabled {
		return nil
	}
	return PublishUserAuthCache(userId)
}

func updateUserSettingCache(userId int, _ string) error {
	if !common.RedisEnabled {
		return nil
	}
	return PublishUserAuthCache(userId)
}

// GetUserLanguage returns the user's language preference from cache
// Uses the existing GetUserCache mechanism for efficiency
func GetUserLanguage(userId int) string {
	userCache, err := GetUserCache(userId)
	if err != nil {
		return ""
	}
	return userCache.GetSetting().Language
}

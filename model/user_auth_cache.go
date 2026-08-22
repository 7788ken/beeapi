package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const userCacheSchemaVersion = 2

var (
	ErrUserAuthCachePending    = errors.New("user authentication state update is pending")
	ErrUserAuthVersionConflict = errors.New("user authentication version update conflicted")
)

func getUserAuthFenceKey(userID int) string {
	return fmt.Sprintf("auth:user:fence:%d", userID)
}

func getUserAuthVersionKey(userID int) string {
	return fmt.Sprintf("auth:user:version:%d", userID)
}

func userCacheTTLSeconds() int {
	ttl := common.RedisKeyCacheSeconds()
	if ttl <= 0 {
		return 60
	}
	return ttl
}

func userAuthFenceTTLSeconds() int {
	cacheTTL := userCacheTTLSeconds()
	extra := cacheTTL
	if extra < 60 {
		extra = 60
	}
	return cacheTTL + extra
}

func writeUserAuthCache(user *UserBase) error {
	if user == nil || user.Id <= 0 || !common.RedisEnabled {
		return nil
	}
	if user.AuthVersion <= 0 {
		return fmt.Errorf("invalid user auth version")
	}
	user.CacheSchema = userCacheSchemaVersion

	const script = `
local incoming = tonumber(ARGV[1])
local pending = tonumber(redis.call('GET', KEYS[2]) or '0')
local committed = tonumber(redis.call('GET', KEYS[3]) or '0')
local current = tonumber(redis.call('HGET', KEYS[1], 'AuthVersion') or '0')
if pending > incoming or committed > incoming or current > incoming then
  return 0
end
if committed < incoming then
  redis.call('SET', KEYS[3], ARGV[1])
end
if pending > 0 and pending <= incoming then
  redis.call('DEL', KEYS[2])
end
redis.call('HSET', KEYS[1],
  'Id', ARGV[2], 'Group', ARGV[3], 'Email', ARGV[4],
  'Quota', ARGV[5], 'Status', ARGV[6], 'Role', ARGV[7],
  'Username', ARGV[8], 'Setting', ARGV[9], 'RpmLimit', ARGV[10],
  'ExpiresAt', ARGV[11], 'AuthVersion', ARGV[1], 'CacheSchema', ARGV[12])
redis.call('EXPIRE', KEYS[1], ARGV[13])
return 1`
	result, err := common.RDB.Eval(
		context.Background(),
		script,
		[]string{
			getUserCacheKey(user.Id),
			getUserAuthFenceKey(user.Id),
			getUserAuthVersionKey(user.Id),
		},
		user.AuthVersion,
		user.Id,
		user.Group,
		user.Email,
		user.Quota,
		user.Status,
		user.Role,
		user.Username,
		user.Setting,
		user.RpmLimit,
		user.ExpiresAt,
		user.CacheSchema,
		userCacheTTLSeconds(),
	).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrUserAuthCachePending
	}
	return nil
}

func getUserAuthVersionFloor(userID int) (int64, error) {
	if !common.RedisEnabled {
		return 0, nil
	}
	values, err := common.RDB.MGet(
		context.Background(),
		getUserAuthFenceKey(userID),
		getUserAuthVersionKey(userID),
	).Result()
	if err != nil {
		return 0, err
	}
	var floor int64
	for _, value := range values {
		if value == nil {
			continue
		}
		parsed, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		if err != nil {
			return 0, err
		}
		if parsed > floor {
			floor = parsed
		}
	}
	return floor, nil
}

func SetUserAuthVersionFence(userID int, authVersion int64) error {
	if !common.RedisEnabled {
		return nil
	}
	if userID <= 0 || authVersion <= 0 {
		return fmt.Errorf("invalid user auth fence")
	}
	const script = `
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local incoming = tonumber(ARGV[1])
if current < incoming then
  redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
elseif current == incoming then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
elseif redis.call('TTL', KEYS[1]) < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return 1`
	return common.RDB.Eval(
		context.Background(),
		script,
		[]string{getUserAuthFenceKey(userID)},
		authVersion,
		userAuthFenceTTLSeconds(),
	).Err()
}

func publishCommittedUserAuthVersion(userID int, authVersion int64) error {
	if !common.RedisEnabled {
		return nil
	}
	if userID <= 0 || authVersion <= 0 {
		return fmt.Errorf("invalid committed user auth version")
	}
	const script = `
local incoming = tonumber(ARGV[1])
local committed = tonumber(redis.call('GET', KEYS[1]) or '0')
local pending = tonumber(redis.call('GET', KEYS[2]) or '0')
if committed < incoming then
  redis.call('SET', KEYS[1], ARGV[1])
end
if pending > 0 and pending <= incoming then
  redis.call('DEL', KEYS[2])
end
return 1`
	return common.RDB.Eval(
		context.Background(),
		script,
		[]string{getUserAuthVersionKey(userID), getUserAuthFenceKey(userID)},
		authVersion,
	).Err()
}

func IncrementUserAuthVersionWithTx(tx *gorm.DB, userID int) (int64, error) {
	if tx == nil || userID <= 0 {
		return 0, fmt.Errorf("invalid user auth version update")
	}
	for range 3 {
		var user User
		if err := withForUpdate(tx.Unscoped()).
			Select("id", "auth_version").
			Where("id = ?", userID).
			First(&user).Error; err != nil {
			return 0, err
		}
		current := user.AuthVersion
		if current < 1 {
			current = 1
		}
		next := current + 1
		if err := SetUserAuthVersionFence(userID, next); err != nil {
			return 0, err
		}
		result := tx.Unscoped().Model(&User{}).
			Where("id = ? AND auth_version = ?", userID, user.AuthVersion).
			Update("auth_version", next)
		if result.Error != nil {
			return 0, result.Error
		}
		if result.RowsAffected == 1 {
			return next, nil
		}
	}
	return 0, ErrUserAuthVersionConflict
}

func BumpUserAuthVersion(userID int) (int64, error) {
	var next int64
	if err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		next, err = IncrementUserAuthVersionWithTx(tx, userID)
		return err
	}); err != nil {
		return 0, err
	}
	if err := PublishUserAuthCache(userID); err != nil {
		return next, err
	}
	return next, nil
}

func UpdateUserRoleStatusAndBumpAuthVersion(userID, role, status int) error {
	if userID <= 0 {
		return fmt.Errorf("invalid user authorization update")
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
			"role":   role,
			"status": status,
		}).Error; err != nil {
			return err
		}
		_, err := IncrementUserAuthVersionWithTx(tx, userID)
		return err
	}); err != nil {
		return err
	}
	return PublishUserAuthCache(userID)
}

func PublishUserAuthCache(userID int) error {
	user, err := GetUserById(userID, false)
	if err != nil {
		return err
	}
	if err := publishCommittedUserAuthVersion(userID, user.AuthVersion); err != nil {
		return err
	}
	return updateUserCache(*user)
}

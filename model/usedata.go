package model

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"gorm.io/gorm"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id            int    `json:"id"`
	UserID        int    `json:"user_id" gorm:"index"`
	Username      string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName     string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	Group         string `json:"group" gorm:"index:idx_qdt_group_time,priority:1;size:64;default:''"`
	BillingSource string `json:"billing_source" gorm:"size:64;default:''"`
	CreatedAt     int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2;index:idx_qdt_group_time,priority:2"`
	TokenUsed     int    `json:"token_used" gorm:"default:0"`
	Count         int    `json:"count" gorm:"default:0"`
	Quota         int    `json:"quota" gorm:"default:0"`
}

func UpdateQuotaData(ctx context.Context) {
	backgroundtask.RunPeriodic(ctx, time.Duration(common.DataExportInterval)*time.Minute, true, func() {
		if common.DataExportEnabled {
			common.SysLog("正在更新数据看板数据...")
			if err := SaveQuotaDataCache(); err != nil {
				common.SysError("保存数据看板数据失败: " + err.Error())
			}
		}
	})
}

func StartQuotaDataUpdater() error {
	if err := CheckFlushOperationSchemaReady(); err != nil {
		return err
	}
	return backgroundtask.Start("quota-data-updater", UpdateQuotaData)
}

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock = sync.Mutex{}
var quotaDataFlushLock sync.Mutex

type quotaDataFlushEntry struct {
	OperationID string
	Data        QuotaData
}

type quotaDataSaveFunc func(context.Context, string, *QuotaData) error

var quotaDataInFlight = make(map[string]*quotaDataFlushEntry)

func logQuotaDataCache(userId int, username string, modelName string, group string, billingSource string, quota int, createdAt int64, tokenUsed int) {
	key := quotaDataCacheKey(userId, username, modelName, group, billingSource, createdAt)
	mergeQuotaDataCacheEntry(key, &QuotaData{
		UserID:        userId,
		Username:      username,
		ModelName:     modelName,
		Group:         group,
		BillingSource: billingSource,
		CreatedAt:     createdAt,
		Count:         1,
		Quota:         quota,
		TokenUsed:     tokenUsed,
	})
}

func quotaDataCacheKey(userId int, username string, modelName string, group string, billingSource string, createdAt int64) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s\x00%d", userId, username, modelName, group, billingSource, createdAt)
}

func mergeQuotaDataCacheEntry(key string, incoming *QuotaData) {
	quotaData, ok := CacheQuotaData[key]
	if ok {
		quotaData.Count += incoming.Count
		quotaData.Quota += incoming.Quota
		quotaData.TokenUsed += incoming.TokenUsed
		return
	}
	CacheQuotaData[key] = incoming
}

func LogQuotaData(userId int, username string, modelName string, group string, billingSource string, quota int, createdAt int64, tokenUsed int) {
	// 只精确到小时
	createdAt = createdAt - (createdAt % 3600)

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(userId, username, modelName, group, billingSource, quota, createdAt, tokenUsed)
}

func SaveQuotaDataCache() error {
	return saveQuotaDataCache(context.Background(), saveQuotaData)
}

func saveQuotaDataCache(ctx context.Context, saver quotaDataSaveFunc) error {
	if ctx == nil {
		return fmt.Errorf("quota data flush context is nil")
	}
	if saver == nil {
		return fmt.Errorf("quota data saver is nil")
	}

	quotaDataFlushLock.Lock()
	defer quotaDataFlushLock.Unlock()

	CacheQuotaDataLock.Lock()
	for key, quotaData := range CacheQuotaData {
		if quotaDataInFlight[key] != nil {
			continue
		}
		quotaDataInFlight[key] = &quotaDataFlushEntry{
			OperationID: newFlushOperationID("quota"),
			Data:        *quotaData,
		}
		delete(CacheQuotaData, key)
	}
	keys := make([]string, 0, len(quotaDataInFlight))
	for key := range quotaDataInFlight {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	CacheQuotaDataLock.Unlock()

	size := len(keys)
	failedCount := 0
	var firstFlushError error
	confirmedOperationIDs := make([]string, 0, size)
	for _, key := range keys {
		CacheQuotaDataLock.Lock()
		entry := quotaDataInFlight[key]
		CacheQuotaDataLock.Unlock()
		if entry == nil {
			failedCount++
			if firstFlushError == nil {
				firstFlushError = fmt.Errorf("quota data in-flight entry disappeared")
			}
			continue
		}
		if err := saver(ctx, entry.OperationID, &entry.Data); err != nil {
			failedCount++
			if firstFlushError == nil {
				firstFlushError = err
			}
			continue
		}

		CacheQuotaDataLock.Lock()
		current := quotaDataInFlight[key]
		if current == nil || current.OperationID != entry.OperationID {
			CacheQuotaDataLock.Unlock()
			failedCount++
			if firstFlushError == nil {
				firstFlushError = fmt.Errorf("quota data operation identity changed during flush")
			}
			continue
		}
		delete(quotaDataInFlight, key)
		CacheQuotaDataLock.Unlock()
		confirmedOperationIDs = append(confirmedOperationIDs, entry.OperationID)
	}
	if cleanupErr := deleteFlushOperationLedgers(ctx, confirmedOperationIDs); cleanupErr != nil {
		common.SysError(cleanupErr.Error())
	}

	if failedCount > 0 {
		return fmt.Errorf("failed to save %d of %d quota cache entries; first error: %w", failedCount, size, firstFlushError)
	}

	common.SysLog(fmt.Sprintf("保存数据看板数据成功，共保存%d条数据", size))
	return nil
}

func saveQuotaData(ctx context.Context, operationID string, quotaData *QuotaData) error {
	payloadHash, err := flushOperationPayloadHash(quotaData)
	if err != nil {
		return err
	}
	_, err = applyFlushOperation(
		ctx,
		operationID,
		"quota-data",
		payloadHash,
		func(tx *gorm.DB) (string, error) {
			quotaDataDB := &QuotaData{}
			err := tx.Table("quota_data").
				Select("id").
				Where("user_id = ?", quotaData.UserID).
				Where("username = ?", quotaData.Username).
				Where("model_name = ?", quotaData.ModelName).
				Where(commonGroupCol+" = ?", quotaData.Group).
				Where("billing_source = ?", quotaData.BillingSource).
				Where("created_at = ?", quotaData.CreatedAt).
				Order("id").
				Limit(1).
				Find(quotaDataDB).Error
			if err != nil {
				return "", fmt.Errorf("query existing quota data: %w", err)
			}
			if quotaDataDB.Id > 0 {
				if err := increaseQuotaData(tx, quotaDataDB.Id, quotaData.Count, quotaData.Quota, quotaData.TokenUsed); err != nil {
					return "", fmt.Errorf("increase existing quota data: %w", err)
				}
				return flushOperationResultQuotaDataWritten, nil
			}

			quotaDataToCreate := *quotaData
			quotaDataToCreate.Id = 0
			result := tx.Table("quota_data").Create(&quotaDataToCreate)
			if result.Error != nil {
				return "", fmt.Errorf("create quota data: %w", result.Error)
			}
			if result.RowsAffected != 1 {
				return "", fmt.Errorf("create quota data affected %d rows, want 1", result.RowsAffected)
			}
			return flushOperationResultQuotaDataWritten, nil
		},
	)
	return err
}

func increaseQuotaData(db *gorm.DB, id int, count int, quota int, tokenUsed int) error {
	result := db.Table("quota_data").
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"count":      gorm.Expr("count + ?", count),
			"quota":      gorm.Expr("quota + ?", quota),
			"token_used": gorm.Expr("token_used + ?", tokenUsed),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("update quota data id %d affected %d rows, want 1", id, result.RowsAffected)
	}
	return nil
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime).Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataGroupByUser(startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").
		Select("username, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Group("username, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

// GetQuotaDataGroupByGroup 按用户分组(group)聚合统计
// username 为空时返回所有用户的数据；billingSource 为空时返回所有计费源（subscription + wallet）
func GetQuotaDataGroupByGroup(startTime int64, endTime int64, username string, billingSource string) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	query := DB.Table("quota_data").
		Select(commonGroupCol+", created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Where(commonGroupCol + " <> ''").
		Group(commonGroupCol + ", created_at")
	if username != "" {
		query = query.Where("username = ?", username)
	}
	if billingSource != "" {
		query = query.Where("billing_source = ?", billingSource)
	}
	err = query.Find(&quotaDatas).Error
	return quotaDatas, err
}

// GetQuotaDataByGroupSumByUser 按分组下的用户聚合，按 quota 倒序返回 top N
// billingSource 为空时返回所有计费源
func GetQuotaDataByGroupSumByUser(group string, startTime int64, endTime int64, billingSource string, limit int) (quotaData []*QuotaData, err error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	var quotaDatas []*QuotaData
	query := DB.Table("quota_data").
		Select("user_id, username, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Where(commonGroupCol+" = ?", group).
		Group("user_id, username").
		Order("quota DESC").
		Limit(limit)
	if billingSource != "" {
		query = query.Where("billing_source = ?", billingSource)
	}
	err = query.Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserSumByGroup(userId int, fallbackUsername string, startTime, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	query := DB.Table("quota_data").
		Select(commonGroupCol+", sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Group(commonGroupCol).
		Order("quota DESC").
		Limit(50)
	if userId > 0 {
		query = query.Where("user_id = ?", userId)
	} else {
		query = query.Where("username = ?", fallbackUsername)
	}
	err = query.Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetAllQuotaDates(startTime int64, endTime int64, username string) (quotaData []*QuotaData, err error) {
	if username != "" {
		return GetQuotaDataByUsername(username, startTime, endTime)
	}
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, created_at").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}

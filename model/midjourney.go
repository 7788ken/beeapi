package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type Midjourney struct {
	Id             int    `json:"id"`
	Code           int    `json:"code"`
	UserId         int    `json:"user_id" gorm:"index"`
	TokenId        int    `json:"token_id" gorm:"index;default:0"`
	SubscriptionId int    `json:"subscription_id" gorm:"index;default:0"`
	BillingSource  string `json:"billing_source" gorm:"type:varchar(32);not null;default:''"`
	Action         string `json:"action" gorm:"type:varchar(40);index"`
	MjId           string `json:"mj_id" gorm:"index"`
	Prompt         string `json:"prompt"`
	PromptEn       string `json:"prompt_en"`
	Description    string `json:"description"`
	State          string `json:"state"`
	SubmitTime     int64  `json:"submit_time" gorm:"index"`
	StartTime      int64  `json:"start_time" gorm:"index"`
	FinishTime     int64  `json:"finish_time" gorm:"index"`
	ImageUrl       string `json:"image_url"`
	VideoUrl       string `json:"video_url"`
	VideoUrls      string `json:"video_urls"`
	Status         string `json:"status" gorm:"type:varchar(20);index"`
	Progress       string `json:"progress" gorm:"type:varchar(30);index"`
	FailReason     string `json:"fail_reason"`
	ChannelId      int    `json:"channel_id"`
	Quota          int    `json:"quota"`
	Buttons        string `json:"buttons"`
	Properties     string `json:"properties"`
}

// TaskQueryParams 用于包含所有搜索条件的结构体，可以根据需求添加更多字段
type TaskQueryParams struct {
	ChannelID      string
	MjID           string
	StartTimestamp string
	EndTimestamp   string
}

func GetAllUserTask(userId int, startIdx int, num int, queryParams TaskQueryParams) []*Midjourney {
	var tasks []*Midjourney
	var err error

	// 初始化查询构建器
	query := DB.Where("user_id = ?", userId)

	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		// 假设您已将前端传来的时间戳转换为数据库所需的时间格式，并处理了时间戳的验证和解析
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func GetAllTasks(startIdx int, num int, queryParams TaskQueryParams) []*Midjourney {
	var tasks []*Midjourney
	var err error

	// 初始化查询构建器
	query := DB

	// 添加过滤条件
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func GetAllUnFinishTasks() []*Midjourney {
	var tasks []*Midjourney
	var err error
	// get all tasks progress is not 100%
	err = DB.Where("progress != ?", "100%").Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetByOnlyMJId(mjId string) *Midjourney {
	var mj *Midjourney
	var err error
	err = DB.Where("mj_id = ?", mjId).First(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func GetByMJId(userId int, mjId string) *Midjourney {
	var mj *Midjourney
	var err error
	err = DB.Where("user_id = ? and mj_id = ?", userId, mjId).First(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func GetByMJIds(userId int, mjIds []string) []*Midjourney {
	var mj []*Midjourney
	var err error
	err = DB.Where("user_id = ? and mj_id in (?)", userId, mjIds).Find(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func GetMjByuId(id int) *Midjourney {
	var mj *Midjourney
	var err error
	err = DB.Where("id = ?", id).First(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func UpdateProgress(id int, progress string) error {
	return DB.Model(&Midjourney{}).Where("id = ?", id).Update("progress", progress).Error
}

func (midjourney *Midjourney) Insert() error {
	var err error
	err = DB.Create(midjourney).Error
	return err
}

// ConsumeQuota records that an accepted task was charged and mutates its
// funding source/token in the same main-database transaction.
func (midjourney *Midjourney) ConsumeQuota(userId int, tokenId int, userSubscriptionId int, quota int) error {
	if midjourney.Id <= 0 || userId <= 0 || quota <= 0 {
		return errors.New("midjourney task, user and charged quota must be positive")
	}
	billingSource := "wallet"
	if userSubscriptionId > 0 {
		billingSource = "subscription"
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		taskResult := tx.Model(&Midjourney{}).
			Where("id = ? AND user_id = ? AND quota = 0", midjourney.Id, userId).
			Updates(map[string]interface{}{
				"quota":           quota,
				"token_id":        tokenId,
				"subscription_id": userSubscriptionId,
				"billing_source":  billingSource,
			})
		if taskResult.Error != nil {
			return taskResult.Error
		}
		if taskResult.RowsAffected != 1 {
			return errors.New("midjourney charged quota was not recorded")
		}

		if userSubscriptionId > 0 {
			subscriptionResult := tx.Model(&UserSubscription{}).
				Where(
					"id = ? AND user_id = ? AND (amount_total <= 0 OR amount_used + ? <= amount_total)",
					userSubscriptionId, userId, quota,
				).
				Update("amount_used", gorm.Expr("amount_used + ?", quota))
			if subscriptionResult.Error != nil {
				return subscriptionResult.Error
			}
			if subscriptionResult.RowsAffected != 1 {
				return errors.New("midjourney subscription quota adjustment rejected")
			}
		} else {
			userResult := tx.Model(&User{}).
				Where("id = ? AND quota >= ?", userId, quota).
				Update("quota", gorm.Expr("quota - ?", quota))
			if userResult.Error != nil {
				return userResult.Error
			}
			if userResult.RowsAffected != 1 {
				return ErrInsufficientUserQuota
			}
		}
		if tokenId > 0 {
			return adjustTokenQuotaTx(tx, tokenId, userId, quota)
		}
		return nil
	})
	if err != nil {
		return err
	}
	midjourney.Quota = quota
	midjourney.TokenId = tokenId
	midjourney.SubscriptionId = userSubscriptionId
	midjourney.BillingSource = billingSource
	return nil
}

func (midjourney *Midjourney) Update() error {
	var err error
	err = DB.Save(midjourney).Error
	return err
}

// UpdateWithStatus performs a conditional UPDATE guarded by fromStatus (CAS).
// Uses Model().Select("*").Updates() to avoid GORM Save()'s INSERT fallback.
func (midjourney *Midjourney) UpdateWithStatus(fromStatus string) (bool, error) {
	result := DB.Model(midjourney).Where("status = ?", fromStatus).Select("*").Updates(midjourney)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// UpdateWithStatusAndRefund atomically wins the task terminal transition and
// returns the charged wallet/token quota. TokenId=0 is reserved for historical
// tasks created before token ownership was persisted.
func (midjourney *Midjourney) UpdateWithStatusAndRefund(fromStatus string) (bool, error) {
	if midjourney.UserId <= 0 {
		return false, errors.New("midjourney user id must be positive")
	}
	if midjourney.Quota <= 0 {
		return midjourney.UpdateWithStatus(fromStatus)
	}
	won := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(midjourney).Where("status = ?", fromStatus).Select("*").Updates(midjourney)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		won = true

		taskId := midjourney.MjId
		if taskId == "" {
			taskId = fmt.Sprintf("midjourney-db-%d", midjourney.Id)
		}
		refundRecord := &AsyncTaskRefundRecord{
			TaskKind:           AsyncTaskRefundKindMidjourney,
			TaskDbId:           int64(midjourney.Id),
			TaskId:             taskId,
			UserId:             midjourney.UserId,
			ChannelId:          midjourney.ChannelId,
			TokenId:            midjourney.TokenId,
			UserSubscriptionId: midjourney.SubscriptionId,
			BillingSource: normalizeAsyncTaskBillingSource(
				midjourney.BillingSource,
				midjourney.SubscriptionId,
			),
			Quota:  midjourney.Quota,
			Reason: midjourney.FailReason,
			Status: AsyncTaskRefundStatusRefunded,
		}
		if err := createAsyncTaskRefundRecordTx(tx, refundRecord); err != nil {
			return err
		}
		return applyAsyncTaskRefundFundingTx(tx, refundRecord)
	})
	if err != nil {
		return false, err
	}
	return won, nil
}

func MjBulkUpdate(mjIds []string, params map[string]any) error {
	return DB.Model(&Midjourney{}).
		Where("mj_id in (?)", mjIds).
		Updates(params).Error
}

func MjBulkUpdateByTaskIds(taskIDs []int, params map[string]any) error {
	return DB.Model(&Midjourney{}).
		Where("id in (?)", taskIDs).
		Updates(params).Error
}

// CountAllTasks returns total midjourney tasks for admin query
func CountAllTasks(queryParams TaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Midjourney{})
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}

// CountAllUserTask returns total midjourney tasks for user
func CountAllUserTask(userId int, queryParams TaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Midjourney{}).Where("user_id = ?", userId)
	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}

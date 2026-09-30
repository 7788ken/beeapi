package service

import (
	"context"
	"fmt"
	"html"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm/clause"
)

func queueIQNotice(key, subject, body string) error {
	row := model.IQTestNotice{Key: key, Subject: subject, Body: body, Pending: true}
	created := model.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if created.Error != nil {
		return created.Error
	}
	if created.RowsAffected == 1 {
		return nil
	}
	return model.DB.Model(&model.IQTestNotice{}).Where(map[string]any{"key": key}).Where("sent_at <= ? AND claim_until <= ?", time.Now().Add(-24*time.Hour).Unix(), time.Now().Unix()).Updates(map[string]any{"subject": subject, "body": body, "pending": true}).Error
}

func IQTestMaintenance(ctx context.Context) error {
	cfg, err := GetIQTestSetting()
	if err != nil {
		return err
	}
	// Keep scores, run summaries and action audit; only expire response excerpts.
	cutoff := time.Now().Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour).Unix()
	completed := model.DB.Model(&model.IQTestRun{}).Select("run_id").Where("finished_at > ? AND finished_at < ?", 0, cutoff)
	if err := model.DB.WithContext(ctx).Model(&model.IQTestResult{}).Where("run_id IN (?) AND detail <> ?", completed, "").Update("detail", "").Error; err != nil {
		return err
	}
	var pending []model.IQTestNotice
	now := time.Now().Unix()
	if err := model.DB.WithContext(ctx).Where("pending = ? AND retry_at <= ? AND claim_until <= ?", true, now, now).Limit(20).Find(&pending).Error; err != nil {
		return err
	}
	for _, notice := range pending {
		claimed := model.DB.Model(&model.IQTestNotice{}).Where(map[string]any{"key": notice.Key, "pending": true}).Where("claim_until <= ?", now).Update("claim_until", now+300)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected == 0 {
			continue
		}
		var root model.User
		sendErr := model.DB.Where("role = ?", common.RoleRootUser).First(&root).Error
		if sendErr == nil {
			address := root.GetSetting().NotificationEmail
			if address == "" {
				address = root.Email
			}
			if address == "" {
				sendErr = fmt.Errorf("root notification email is not configured")
			} else {
				sendErr = common.SendEmail(notice.Subject, address, "<p>"+html.EscapeString(notice.Body)+"</p>")
			}
		}
		updates := map[string]any{"claim_until": 0, "pending": false, "sent_at": time.Now().Unix(), "last_error": ""}
		if sendErr != nil {
			updates = map[string]any{"claim_until": 0, "pending": true, "retry_at": time.Now().Add(5 * time.Minute).Unix(), "last_error": sendErr.Error()}
		}
		if err := model.DB.Model(&model.IQTestNotice{}).Where(map[string]any{"key": notice.Key}).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

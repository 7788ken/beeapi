package model

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func TestRecordConsumeLogUpdatesQuotaCacheBeforeReturn(t *testing.T) {
	if err := LOG_DB.Exec("DELETE FROM logs").Error; err != nil {
		t.Fatalf("clear logs before test: %v", err)
	}
	t.Cleanup(func() {
		_ = LOG_DB.Exec("DELETE FROM logs").Error
	})
	isolateQuotaDataCache(t)

	oldDataExportEnabled := common.DataExportEnabled
	oldLogConsumeEnabled := common.LogConsumeEnabled
	common.DataExportEnabled = true
	common.LogConsumeEnabled = true
	t.Cleanup(func() {
		common.DataExportEnabled = oldDataExportEnabled
		common.LogConsumeEnabled = oldLogConsumeEnabled
	})

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	c.Set("username", "cache-user")

	CacheQuotaDataLock.Lock()
	cacheLocked := true
	t.Cleanup(func() {
		if cacheLocked {
			CacheQuotaDataLock.Unlock()
		}
	})

	recordDone := make(chan struct{})
	go func() {
		RecordConsumeLog(c, 0, RecordConsumeLogParams{
			ChannelId:        0,
			ModelName:        "image-model",
			PromptTokens:     11,
			CompletionTokens: 7,
			Quota:            321,
			Group:            "image-group",
			Other: map[string]interface{}{
				"billing_source": "subscription",
			},
		})
		close(recordDone)
	}()

	logPersisted := false
	persistDeadline := time.NewTimer(2 * time.Second)
	persistPoll := time.NewTicker(5 * time.Millisecond)
	for !logPersisted {
		var count int64
		if err := LOG_DB.Model(&Log{}).Count(&count).Error; err != nil {
			persistPoll.Stop()
			persistDeadline.Stop()
			CacheQuotaDataLock.Unlock()
			cacheLocked = false
			if !waitForSignal(recordDone, 2*time.Second) {
				t.Fatalf("count consume logs: %v; RecordConsumeLog did not stop after cache unlock", err)
			}
			t.Fatalf("count consume logs: %v", err)
		}
		logPersisted = count == 1
		if logPersisted {
			break
		}
		select {
		case <-persistPoll.C:
		case <-persistDeadline.C:
			persistPoll.Stop()
			CacheQuotaDataLock.Unlock()
			cacheLocked = false
			if !waitForSignal(recordDone, 2*time.Second) {
				t.Fatal("consume log was not persisted and RecordConsumeLog did not stop after cache unlock")
			}
			t.Fatal("consume log was not persisted while quota cache lock was held")
		}
	}
	persistPoll.Stop()
	if !persistDeadline.Stop() {
		select {
		case <-persistDeadline.C:
		default:
		}
	}

	returnedWhileLocked := waitForSignal(recordDone, 200*time.Millisecond)

	CacheQuotaDataLock.Unlock()
	cacheLocked = false
	if !waitForSignal(recordDone, 2*time.Second) {
		t.Fatal("RecordConsumeLog did not return after releasing quota cache lock")
	}
	if returnedWhileLocked {
		t.Fatal("RecordConsumeLog returned before its quota data was added to the cache")
	}

	var persisted Log
	if err := LOG_DB.First(&persisted).Error; err != nil {
		t.Fatalf("read consume log: %v", err)
	}

	entries := quotaDataCacheEntries()
	if len(entries) != 1 {
		t.Fatalf("CacheQuotaData entries = %d, want 1 when RecordConsumeLog returns", len(entries))
	}
	cached := entries[0]
	if cached.BillingSource != "subscription" {
		t.Fatalf("billing source = %q, want subscription", cached.BillingSource)
	}
	if cached.Quota != 321 {
		t.Fatalf("quota = %d, want 321", cached.Quota)
	}
	if cached.TokenUsed != 18 {
		t.Fatalf("token used = %d, want 18", cached.TokenUsed)
	}
	wantCreatedAt := persisted.CreatedAt - persisted.CreatedAt%3600
	if cached.CreatedAt != wantCreatedAt {
		t.Fatalf("cache created_at = %d, want log hour %d from log created_at %d", cached.CreatedAt, wantCreatedAt, persisted.CreatedAt)
	}
}

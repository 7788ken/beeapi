package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type logFastAPIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Quota int64 `json:"quota"`
		Rpm   *int  `json:"rpm"`
		Tpm   *int  `json:"tpm"`
	} `json:"data"`
}

type reconcileFastAPIResponse struct {
	Success bool                     `json:"success"`
	Message string                   `json:"message"`
	Data    ChannelReconcileResponse `json:"data"`
}

func setupFastLogControllerDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:controller-fast-"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE logs (
			id INTEGER PRIMARY KEY,
			channel_id INTEGER NOT NULL,
			type INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			quota INTEGER NOT NULL
		)`).Error; err != nil {
		t.Fatalf("create minimal logs table: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE channels (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			status INTEGER NOT NULL
		)`).Error; err != nil {
		t.Fatalf("create channels: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO channels (id, name, status)
		VALUES (4, 'four', 1), (5, 'five', 2), (6, 'six', 1)
	`).Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO logs (id, channel_id, type, created_at, quota)
		VALUES
			(1, 4, ?, 100, 10),
			(2, 4, ?, 200, 20),
			(3, 4, ?, 150, 1000),
			(4, 5, ?, 150, 7),
			(5, 6, ?, 99, 1000)
	`, model.LogTypeConsume, model.LogTypeConsume, model.LogTypeError, model.LogTypeConsume, model.LogTypeConsume).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	oldDB, oldLogDB := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
	})
}

func newFastLogContext(target string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", target, nil)
	return ctx, recorder
}

func TestGetLogsStatQuotaOnlyReturnsQuotaWithoutRPMTPM(t *testing.T) {
	setupFastLogControllerDB(t)
	ctx, recorder := newFastLogContext("/api/log/stat?type=2&channel=4&start_timestamp=100&end_timestamp=200&quota_only=true")

	GetLogsStat(ctx)

	var response logFastAPIResponse
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.Data.Quota != 30 {
		t.Fatalf("response = %s", recorder.Body.String())
	}
	if response.Data.Rpm != nil || response.Data.Tpm != nil {
		t.Fatalf("quota_only response must omit rpm/tpm: %s", recorder.Body.String())
	}
}

func TestGetLogsStatQuotaOnlyRejectsUnsupportedShapes(t *testing.T) {
	tests := []string{
		"/api/log/stat?type=2&channel=4&start_timestamp=100&end_timestamp=200&quota_only=1",
		"/api/log/stat?type=2&start_timestamp=100&end_timestamp=200&quota_only=true",
		"/api/log/stat?type=2&channel=4&start_timestamp=100&end_timestamp=200&group=g&quota_only=true",
		"/api/log/stat?type=2&channel=4&start_timestamp=100&end_timestamp=86501&quota_only=true",
	}
	for _, target := range tests {
		ctx, recorder := newFastLogContext(target)
		GetLogsStat(ctx)

		var response logFastAPIResponse
		if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode %s: %v", target, err)
		}
		if response.Success || response.Message == "" {
			t.Fatalf("target %s unexpectedly accepted: %s", target, recorder.Body.String())
		}
	}
}

func TestGetChannelReconcileSummaryOnlyReturnsChannelQuota(t *testing.T) {
	setupFastLogControllerDB(t)
	ctx, recorder := newFastLogContext("/api/channel/reconcile?start_ts=100&end_ts=200&summary_only=true")

	GetChannelReconcile(ctx)

	var response reconcileFastAPIResponse
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.Data.Total.Quota != 37 || len(response.Data.Channels) != 2 {
		t.Fatalf("response = %s", recorder.Body.String())
	}
	if response.Data.Channels[0].ChannelId != 4 || response.Data.Channels[0].Quota != 30 {
		t.Fatalf("first channel = %+v", response.Data.Channels[0])
	}
	if response.Data.Channels[1].ChannelId != 5 || response.Data.Channels[1].Quota != 7 {
		t.Fatalf("second channel = %+v", response.Data.Channels[1])
	}
	for _, channel := range response.Data.Channels {
		if channel.SuccessCount != 0 || channel.ErrorCount != 0 || channel.TimeoutCount != 0 || len(channel.Models) != 0 {
			t.Fatalf("summary channel contains detailed metrics: %+v", channel)
		}
	}
	if !strings.Contains(recorder.Body.String(), `"models":[]`) {
		t.Fatalf("summary models must encode as []: %s", recorder.Body.String())
	}
}

func TestGetChannelReconcileRejectsInvalidSummaryOnly(t *testing.T) {
	ctx, recorder := newFastLogContext("/api/channel/reconcile?start_ts=100&end_ts=200&summary_only=1")

	GetChannelReconcile(ctx)

	var response reconcileFastAPIResponse
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Success || response.Message == "" {
		t.Fatalf("invalid summary_only accepted: %s", recorder.Body.String())
	}
}

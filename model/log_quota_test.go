package model

import (
	"context"
	"sort"
	"testing"
)

func setupLogQuotaTestDB(t *testing.T) {
	t.Helper()
	db := openLogMigrationTestDB(t)
	if err := migrateLogSchema(db); err != nil {
		t.Fatalf("migrate log schema: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE channels (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			status INTEGER NOT NULL
		)`).Error; err != nil {
		t.Fatalf("create channels table: %v", err)
	}

	oldDB, oldLogDB := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() {
		DB, LOG_DB = oldDB, oldLogDB
	})
}

func seedQuotaLogs(t *testing.T) {
	t.Helper()
	logs := []Log{
		{ChannelId: 4, Type: LogTypeConsume, CreatedAt: 100, Quota: 10},
		{ChannelId: 4, Type: LogTypeConsume, CreatedAt: 200, Quota: 20},
		{ChannelId: 4, Type: LogTypeConsume, CreatedAt: 99, Quota: 1000},
		{ChannelId: 4, Type: LogTypeConsume, CreatedAt: 201, Quota: 1000},
		{ChannelId: 4, Type: LogTypeError, CreatedAt: 150, Quota: 1000},
		{ChannelId: 5, Type: LogTypeConsume, CreatedAt: 150, Quota: 7},
		{ChannelId: 6, Type: LogTypeError, CreatedAt: 150, Quota: 0},
	}
	if err := LOG_DB.Create(&logs).Error; err != nil {
		t.Fatalf("seed quota logs: %v", err)
	}
}

func TestSumChannelQuotaUsesChannelTypeAndInclusiveWindow(t *testing.T) {
	setupLogQuotaTestDB(t)
	seedQuotaLogs(t)

	quota, err := SumChannelQuota(context.Background(), 4, 100, 200)
	if err != nil {
		t.Fatalf("sum channel quota: %v", err)
	}
	if quota != 30 {
		t.Fatalf("quota = %d, want 30", quota)
	}

	quota, err = SumChannelQuota(context.Background(), 9, 100, 200)
	if err != nil {
		t.Fatalf("sum empty channel quota: %v", err)
	}
	if quota != 0 {
		t.Fatalf("empty channel quota = %d, want 0", quota)
	}
}

func TestGetChannelQuotaSummaryRowsOmitsChannelsWithoutConsumeLogs(t *testing.T) {
	setupLogQuotaTestDB(t)
	seedQuotaLogs(t)
	if err := DB.Exec(`
		INSERT INTO channels (id, name, status)
		VALUES (4, 'four', 1), (5, 'five', 2), (6, 'six', 1), (7, 'seven', 1)
	`).Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}

	rows, err := GetChannelQuotaSummaryRows(context.Background(), 100, 200)
	if err != nil {
		t.Fatalf("get channel quota summary: %v", err)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ChannelId < rows[j].ChannelId })
	if len(rows) != 2 {
		t.Fatalf("summary rows = %v, want two consume channels", rows)
	}
	if rows[0].ChannelId != 4 || rows[0].ChannelName != "four" || rows[0].Status != 1 || rows[0].Quota != 30 {
		t.Fatalf("channel 4 summary = %+v", rows[0])
	}
	if rows[1].ChannelId != 5 || rows[1].ChannelName != "five" || rows[1].Status != 2 || rows[1].Quota != 7 {
		t.Fatalf("channel 5 summary = %+v", rows[1])
	}
}

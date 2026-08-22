package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/driver/clickhouse"
	"gorm.io/gorm"
)

func withLogDatabaseType(t *testing.T, databaseType string) {
	t.Helper()
	previous := common.LogSqlType
	common.LogSqlType = databaseType
	t.Cleanup(func() {
		common.LogSqlType = previous
		initCol()
	})
	initCol()
}

func TestClickHouseLogUsesIndependentEventCursor(t *testing.T) {
	withLogDatabaseType(t, common.DatabaseTypeClickHouse)

	if got := clickHouseLogOrder("logs."); got != "logs.created_at desc, logs.event_id desc" {
		t.Fatalf("ClickHouse log order = %q", got)
	}
	db := openLogMigrationTestDB(t).Session(&gorm.Session{DryRun: true})
	var total int64
	result := buildBoundedLogCountQuery(db.Where("logs.user_id = ?", 7), 100).Count(&total)
	if result.Error != nil {
		t.Fatalf("build bounded query: %v", result.Error)
	}
	sql := result.Statement.SQL.String()
	if !strings.Contains(sql, "logs.event_id") || !strings.Contains(sql, "logs.created_at desc") {
		t.Fatalf("bounded query does not use event cursor: %s", sql)
	}
}

func TestClickHouseLikeEscaping(t *testing.T) {
	withLogDatabaseType(t, common.DatabaseTypeClickHouse)

	condition, pattern, err := buildLogLikeCondition("logs.model_name", `gpt_4\mini%`)
	if err != nil {
		t.Fatalf("build ClickHouse LIKE: %v", err)
	}
	if condition != "logs.model_name LIKE ?" {
		t.Fatalf("condition = %q", condition)
	}
	if pattern != `gpt\_4\\mini%` {
		t.Fatalf("pattern = %q", pattern)
	}
}

func TestClickHouseLogColumnsDoNotInheritMainPostgresQuoting(t *testing.T) {
	previousPostgres := common.UsingPostgreSQL
	common.UsingPostgreSQL = true
	t.Cleanup(func() {
		common.UsingPostgreSQL = previousPostgres
		initCol()
	})
	withLogDatabaseType(t, common.DatabaseTypeClickHouse)

	if commonGroupCol != `"group"` {
		t.Fatalf("main group column = %q", commonGroupCol)
	}
	if logGroupCol != "`group`" {
		t.Fatalf("ClickHouse group column = %q", logGroupCol)
	}
}

func TestEnsureLogRequestId(t *testing.T) {
	log := &Log{}
	ensureLogRequestId(log)
	if strings.TrimSpace(log.RequestId) == "" {
		t.Fatal("request ID was not generated")
	}
	existing := &Log{RequestId: "fixed-request"}
	ensureLogRequestId(existing)
	if existing.RequestId != "fixed-request" {
		t.Fatalf("existing request ID changed to %q", existing.RequestId)
	}
}

func TestClickHouseAnomalyUsesDisplayId(t *testing.T) {
	withLogDatabaseType(t, common.DatabaseTypeClickHouse)
	if got := anomalyLogID(Log{Id: 0}, 20, 2); got != 23 {
		t.Fatalf("anomaly display ID = %d, want 23", got)
	}
}

func TestChooseDBRejectsClickHouseAsMainDatabase(t *testing.T) {
	t.Setenv("SQL_DSN", "clickhouse://localhost:9000/default")
	db, err := chooseDB("SQL_DSN", false)
	if err == nil || db != nil {
		t.Fatalf("main ClickHouse must be rejected: db=%v err=%v", db, err)
	}
	if !strings.Contains(err.Error(), "use LOG_SQL_DSN") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClickHouseRuntimeSchemaLeavesEventIdToDatabaseDefault(t *testing.T) {
	withLogDatabaseType(t, common.DatabaseTypeClickHouse)
	db, err := gorm.Open(clickhouse.New(clickhouse.Config{
		DSN:                       "clickhouse://localhost:9000/default",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DisableAutomaticPing: true,
		DryRun:               true,
	})
	if err != nil {
		t.Fatalf("open dry-run ClickHouse: %v", err)
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(&Log{}); err != nil {
		t.Fatalf("parse Log schema: %v", err)
	}
	eventId := statement.Schema.LookUpField("EventId")
	if eventId == nil || eventId.Creatable {
		t.Fatalf("event_id must be read-only so ClickHouse can generate its default: %+v", eventId)
	}
	requestId := statement.Schema.LookUpField("RequestId")
	if requestId == nil || !requestId.Creatable {
		t.Fatalf("request_id must remain writable: %+v", requestId)
	}
}

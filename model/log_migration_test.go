package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openLogMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:log-migration-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

type legacyLog struct {
	Id        int
	CreatedAt int64 `gorm:"bigint"`
	Type      int
}

func (legacyLog) TableName() string {
	return "logs"
}

func TestMigrateLogSchemaCreatesChannelWindowIndexForFreshTable(t *testing.T) {
	db := openLogMigrationTestDB(t)

	if err := migrateLogSchema(db); err != nil {
		t.Fatalf("migrate fresh log schema: %v", err)
	}
	if db.Migrator().HasColumn(&Log{}, "event_id") {
		t.Fatal("relational logs table must not receive ClickHouse-only event_id")
	}
	if !db.Migrator().HasIndex(&logChannelWindowIndex{}, logChannelWindowIndexName) {
		t.Fatalf("fresh logs table missing %s", logChannelWindowIndexName)
	}

	var columns []struct {
		Name string `gorm:"column:name"`
	}
	if err := db.Raw("PRAGMA index_info(" + logChannelWindowIndexName + ")").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect index columns: %v", err)
	}
	got := make([]string, 0, len(columns))
	for _, column := range columns {
		got = append(got, column.Name)
	}
	want := []string{"channel_id", "type", "created_at", "quota"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("index columns = %v, want %v", got, want)
	}
}

func TestMigrateLogSchemaSkipsNewIndexForExistingNonEmptyTable(t *testing.T) {
	db := openLogMigrationTestDB(t)
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatalf("create existing log schema: %v", err)
	}
	if err := db.Create(&Log{
		CreatedAt: 1,
		Type:      LogTypeConsume,
		ChannelId: 1,
		Quota:     1,
	}).Error; err != nil {
		t.Fatalf("seed existing log row: %v", err)
	}
	if db.Migrator().HasIndex(&logChannelWindowIndex{}, logChannelWindowIndexName) {
		t.Fatalf("%s unexpectedly declared on Log", logChannelWindowIndexName)
	}

	if err := migrateLogSchema(db); err != nil {
		t.Fatalf("migrate existing log schema: %v", err)
	}
	if db.Migrator().HasIndex(&logChannelWindowIndex{}, logChannelWindowIndexName) {
		t.Fatalf("non-empty logs table must not build %s during startup", logChannelWindowIndexName)
	}
}

func TestMigrateLogSchemaAddsUpstreamRequestIdWithoutIndexToExistingTable(t *testing.T) {
	db := openLogMigrationTestDB(t)
	if err := db.AutoMigrate(&legacyLog{}); err != nil {
		t.Fatalf("create legacy logs table: %v", err)
	}
	if err := db.Create(&legacyLog{Id: 1, CreatedAt: 123, Type: LogTypeConsume}).Error; err != nil {
		t.Fatalf("seed legacy log: %v", err)
	}

	if err := migrateLogSchema(db); err != nil {
		t.Fatalf("migrate legacy logs table: %v", err)
	}
	if !db.Migrator().HasColumn(&Log{}, "upstream_request_id") {
		t.Fatal("legacy logs table missing upstream_request_id after migration")
	}
	if db.Migrator().HasIndex(&Log{}, "idx_logs_upstream_request_id") {
		t.Fatal("startup migration must not create upstream request ID index")
	}

	var log Log
	if err := db.First(&log, 1).Error; err != nil {
		t.Fatalf("read legacy log after migration: %v", err)
	}
	if log.CreatedAt != 123 || log.Type != LogTypeConsume || log.UpstreamRequestId != "" {
		t.Fatalf("legacy log changed during migration: %+v", log)
	}
}

func TestMigrateLogSchemaCreatesIndexForExistingEmptyTable(t *testing.T) {
	db := openLogMigrationTestDB(t)
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatalf("create existing empty log schema: %v", err)
	}
	if db.Migrator().HasIndex(&logChannelWindowIndex{}, logChannelWindowIndexName) {
		t.Fatalf("%s unexpectedly declared on Log", logChannelWindowIndexName)
	}

	if err := migrateLogSchema(db); err != nil {
		t.Fatalf("retry migration for existing empty table: %v", err)
	}
	if !db.Migrator().HasIndex(&logChannelWindowIndex{}, logChannelWindowIndexName) {
		t.Fatalf("existing empty logs table missing %s", logChannelWindowIndexName)
	}

	if err := migrateLogSchema(db); err != nil {
		t.Fatalf("repeat completed migration: %v", err)
	}
}

func TestChannelWindowQueriesCanUseCompositeIndex(t *testing.T) {
	db := openLogMigrationTestDB(t)
	if err := migrateLogSchema(db); err != nil {
		t.Fatalf("migrate log schema: %v", err)
	}

	queries := []string{
		`EXPLAIN QUERY PLAN
		 SELECT SUM(quota)
		 FROM logs
		 WHERE channel_id = 1 AND type = 2
		   AND created_at >= 100 AND created_at <= 200`,
		`EXPLAIN QUERY PLAN
		 SELECT channel_id, SUM(quota)
		 FROM logs
		 WHERE channel_id = 1 AND type = 2
		   AND created_at >= 100 AND created_at <= 200
		 GROUP BY channel_id`,
	}
	for _, query := range queries {
		var plan []struct {
			Detail string `gorm:"column:detail"`
		}
		if err := db.Raw(query).Scan(&plan).Error; err != nil {
			t.Fatalf("explain channel-window query: %v", err)
		}
		details := make([]string, 0, len(plan))
		for _, row := range plan {
			details = append(details, row.Detail)
		}
		joined := strings.Join(details, "\n")
		if !strings.Contains(joined, logChannelWindowIndexName) {
			t.Fatalf("query plan does not use %s: %v", logChannelWindowIndexName, details)
		}
		if !strings.Contains(joined, "COVERING INDEX") {
			t.Fatalf("query plan is not covering: %v", details)
		}
	}
}

type namedLogQuotaDialector struct {
	gorm.Dialector
	name string
}

func (d namedLogQuotaDialector) Name() string {
	return d.name
}

func logQuotaTestDBWithDialectName(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db := openLogMigrationTestDB(t)
	clone := *db
	config := *db.Config
	config.Dialector = namedLogQuotaDialector{
		Dialector: db.Dialector,
		name:      name,
	}
	clone.Config = &config
	return &clone
}

func TestChannelQuotaTableUsesForceIndexOnlyForMySQL(t *testing.T) {
	tests := []struct {
		dialect string
		want    string
	}{
		{dialect: "mysql", want: "logs FORCE INDEX (`" + logChannelWindowIndexName + "`)"},
		{dialect: "sqlite", want: "logs"},
		{dialect: "postgres", want: "logs"},
	}
	for _, test := range tests {
		t.Run(test.dialect, func(t *testing.T) {
			db := logQuotaTestDBWithDialectName(t, test.dialect)
			if got := channelQuotaTable(db); got != test.want {
				t.Fatalf("channelQuotaTable(%s) = %q, want %q", test.dialect, got, test.want)
			}
		})
	}
}

func TestUserLogsTableUsesTimeTypeIndexOnlyForBoundedMySQLRefunds(t *testing.T) {
	forced := "logs FORCE INDEX (`" + logCreatedAtTypeIndexName + "`)"
	tests := []struct {
		name       string
		dialect    string
		logType    int
		startTime  int64
		endTime    int64
		additional bool
		want       string
	}{
		{name: "mysql refund bounded", dialect: "mysql", logType: LogTypeRefund, startTime: 100, endTime: 200, want: forced},
		{name: "mysql manage bounded", dialect: "mysql", logType: LogTypeManage, startTime: 100, endTime: 200, want: "logs"},
		{name: "mysql consume bounded", dialect: "mysql", logType: LogTypeConsume, startTime: 100, endTime: 200, want: "logs"},
		{name: "mysql error bounded", dialect: "mysql", logType: LogTypeError, startTime: 100, endTime: 200, want: "logs"},
		{name: "mysql refund open ended", dialect: "mysql", logType: LogTypeRefund, startTime: 100, want: "logs"},
		{name: "mysql refund with request id", dialect: "mysql", logType: LogTypeRefund, startTime: 100, endTime: 200, additional: true, want: "logs"},
		{name: "sqlite refund bounded", dialect: "sqlite", logType: LogTypeRefund, startTime: 100, endTime: 200, want: "logs"},
		{name: "postgres refund bounded", dialect: "postgres", logType: LogTypeRefund, startTime: 100, endTime: 200, want: "logs"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := logQuotaTestDBWithDialectName(t, test.dialect)
			if got := userLogsTable(db, test.logType, test.startTime, test.endTime, test.additional); got != test.want {
				t.Fatalf("userLogsTable() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestUserLogsSQLUsesMySQL57CompatibleForceIndexForBoundedRefunds(t *testing.T) {
	db := logQuotaTestDBWithDialectName(t, "mysql")
	stmt := db.Session(&gorm.Session{DryRun: true}).
		Table(userLogsTable(db, LogTypeRefund, 100, 200, false)).
		Where("logs.user_id = ? AND logs.type = ?", 7, LogTypeRefund).
		Where("logs.created_at >= ? AND logs.created_at <= ?", 100, 200).
		Order("logs.id DESC").
		Limit(100).
		Find(&[]Log{}).
		Statement

	want := "FROM logs FORCE INDEX (`" + logCreatedAtTypeIndexName + "`)"
	if !strings.Contains(stmt.SQL.String(), want) {
		t.Fatalf("user logs SQL missing FORCE INDEX %q: %s", want, stmt.SQL.String())
	}
	if strings.Contains(stmt.SQL.String(), "/*+") {
		t.Fatalf("user logs SQL must not use MySQL 8 optimizer hints: %s", stmt.SQL.String())
	}
}

func TestChannelQuotaSQLUsesMySQL57CompatibleForceIndex(t *testing.T) {
	db := logQuotaTestDBWithDialectName(t, "mysql")
	stmt := db.Session(&gorm.Session{DryRun: true}).Table(channelQuotaTable(db)).
		Select(channelQuotaSelect).
		Where("channel_id = ? AND type = ? AND created_at >= ? AND created_at <= ?", 1, LogTypeConsume, 100, 200).
		Scan(&struct{}{}).Statement

	want := "FROM logs FORCE INDEX (`" + logChannelWindowIndexName + "`)"
	if !strings.Contains(stmt.SQL.String(), want) {
		t.Fatalf("quota SQL missing FORCE INDEX %q: %s", want, stmt.SQL.String())
	}
	if strings.Contains(stmt.SQL.String(), "/*+") {
		t.Fatalf("quota SQL must not use MySQL 8 optimizer hints: %s", stmt.SQL.String())
	}
}

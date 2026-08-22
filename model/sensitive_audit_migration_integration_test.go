package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const sensitiveAuditMigrationTestDatabase = "newapi_sensitive_audit_test"

type legacySensitiveBlockLogMigration struct {
	Id            int `gorm:"primaryKey;autoIncrement"`
	MatchedWordId int `gorm:"index;default:0"`
	RequestBody   string
	DumpPath      string `gorm:"type:varchar(512);default:''"`
	DumpExists    bool   `gorm:"default:true"`
}

func (legacySensitiveBlockLogMigration) TableName() string {
	return "sensitive_block_logs"
}

type legacySensitiveBlockLogSQLite struct {
	Id            int    `gorm:"primaryKey;autoIncrement"`
	MatchedWordId int    `gorm:"index;default:0"`
	RequestBody   string `gorm:"type:longtext"`
	DumpPath      string `gorm:"type:varchar(512);default:''"`
	DumpExists    bool   `gorm:"default:true"`
}

func (legacySensitiveBlockLogSQLite) TableName() string {
	return "sensitive_block_logs"
}

func TestSensitiveAuditMigrationFromLegacySQLiteLongText(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "sensitive-audit-migration.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	require.NoError(t, db.AutoMigrate(&legacySensitiveBlockLogSQLite{}))
	legacyBody := strings.Repeat("x", 70*1024)
	legacy := legacySensitiveBlockLogSQLite{
		MatchedWordId: 1,
		RequestBody:   legacyBody,
		DumpPath:      "/legacy/sqlite-request.json.gz",
		DumpExists:    true,
	}
	require.NoError(t, db.Create(&legacy).Error)

	require.NoError(t, db.AutoMigrate(&SensitiveBlockLog{}, &SensitiveAuditJobRecord{}))
	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
	})
	require.NoError(t, CheckSensitiveAuditSchemaReady())

	var migrated SensitiveBlockLog
	require.NoError(t, db.First(&migrated, legacy.Id).Error)
	require.Equal(t, legacyBody, migrated.RequestBody)

	require.NoError(t, db.Create(&SensitiveBlockLog{MatchedWordId: 10, DumpExists: false}).Error)
	require.NoError(t, db.Create(&SensitiveBlockLog{MatchedWordId: 10, DumpExists: false}).Error)

	jobID := "sqlite-migration-unique-job"
	require.NoError(t, db.Create(&SensitiveBlockLog{
		AuditJobId:    &jobID,
		MatchedWordId: 11,
		DumpExists:    false,
	}).Error)
	require.Error(t, db.Create(&SensitiveBlockLog{
		AuditJobId:    &jobID,
		MatchedWordId: 11,
		DumpExists:    false,
	}).Error)

	sharedPath := "/legacy/shared-cleanup.json.gz"
	require.NoError(t, db.Create(&SensitiveBlockLog{
		MatchedWordId: 20,
		DumpPath:      sharedPath,
		DumpExists:    true,
	}).Error)
	require.NoError(t, db.Create(&SensitiveBlockLog{
		MatchedWordId: 21,
		DumpPath:      sharedPath,
		DumpExists:    true,
	}).Error)
	require.NoError(t, MarkSensitiveDumpsCleaned([]string{sharedPath}))
	var cleaned []SensitiveBlockLog
	require.NoError(t, db.Where("dump_path = ?", sharedPath).Order("id asc").Find(&cleaned).Error)
	require.Len(t, cleaned, 2)
	require.False(t, cleaned[0].DumpExists)
	require.False(t, cleaned[1].DumpExists)
	require.NoError(t, MarkSensitiveDumpsCleaned([]string{sharedPath}))
	require.NoError(t, MarkSensitiveDumpsCleaned([]string{"/legacy/orphan.json.gz"}))
}

func TestSensitiveAuditMigrationOnRealDatabase(t *testing.T) {
	dialect := os.Getenv("SENSITIVE_AUDIT_MIGRATION_TEST_DIALECT")
	dsn := os.Getenv("SENSITIVE_AUDIT_MIGRATION_TEST_DSN")
	if dialect == "" || dsn == "" {
		t.Skip("real sensitive audit migration database is not configured")
	}

	var dialector gorm.Dialector
	switch dialect {
	case "mysql":
		dialector = mysql.Open(dsn)
	case "postgres":
		dialector = postgres.New(postgres.Config{
			DSN:                  dsn,
			PreferSimpleProtocol: true,
		})
	default:
		t.Fatalf("unsupported SENSITIVE_AUDIT_MIGRATION_TEST_DIALECT %q", dialect)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	var databaseName string
	switch dialect {
	case "mysql":
		require.NoError(t, db.Raw("SELECT DATABASE()").Scan(&databaseName).Error)
	case "postgres":
		require.NoError(t, db.Raw("SELECT current_database()").Scan(&databaseName).Error)
	}
	require.Equal(t, sensitiveAuditMigrationTestDatabase, databaseName,
		"integration migration may only run against its dedicated disposable database")

	require.NoError(t, db.Migrator().DropTable(&SensitiveAuditJobRecord{}, &SensitiveBlockLog{}))
	t.Cleanup(func() {
		_ = db.Migrator().DropTable(&SensitiveAuditJobRecord{}, &SensitiveBlockLog{})
	})

	require.NoError(t, db.AutoMigrate(&legacySensitiveBlockLogMigration{}))
	legacyBody := strings.Repeat("x", 70*1024)
	legacy := legacySensitiveBlockLogMigration{
		MatchedWordId: 1,
		RequestBody:   legacyBody,
		DumpPath:      "/legacy/request.json.gz",
		DumpExists:    true,
	}
	require.NoError(t, db.Create(&legacy).Error)

	require.NoError(t, db.AutoMigrate(&SensitiveBlockLog{}, &SensitiveAuditJobRecord{}))
	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
	})
	require.NoError(t, CheckSensitiveAuditSchemaReady())
	require.True(t, db.Migrator().HasColumn(&SensitiveBlockLog{}, "audit_job_id"))
	require.True(t, db.Migrator().HasIndex(&SensitiveBlockLog{}, "idx_sensitive_audit_job_word"))
	require.True(t, db.Migrator().HasIndex(&SensitiveAuditJobRecord{}, "idx_sensitive_audit_claim"))

	columnTypes, err := db.Migrator().ColumnTypes(&SensitiveBlockLog{})
	require.NoError(t, err)
	var auditJobNullable bool
	var requestBodyType string
	for _, columnType := range columnTypes {
		switch columnType.Name() {
		case "audit_job_id":
			nullable, ok := columnType.Nullable()
			require.True(t, ok)
			auditJobNullable = nullable
		case "request_body":
			requestBodyType = strings.ToLower(columnType.DatabaseTypeName())
		}
	}
	require.True(t, auditJobNullable)
	if dialect == "mysql" {
		require.Equal(t, "longtext", requestBodyType)
	} else {
		require.Equal(t, "text", requestBodyType)
	}

	var migrated SensitiveBlockLog
	require.NoError(t, db.First(&migrated, legacy.Id).Error)
	require.Equal(t, legacyBody, migrated.RequestBody)

	require.NoError(t, db.Create(&SensitiveBlockLog{MatchedWordId: 10, DumpExists: false}).Error)
	require.NoError(t, db.Create(&SensitiveBlockLog{MatchedWordId: 10, DumpExists: false}).Error)

	jobID := "migration-unique-job"
	require.NoError(t, db.Create(&SensitiveBlockLog{
		AuditJobId:    &jobID,
		MatchedWordId: 11,
		DumpExists:    false,
	}).Error)
	require.Error(t, db.Create(&SensitiveBlockLog{
		AuditJobId:    &jobID,
		MatchedWordId: 11,
		DumpExists:    false,
	}).Error)

	sharedPath := "/legacy/shared-cleanup.json.gz"
	require.NoError(t, db.Create(&SensitiveBlockLog{
		MatchedWordId: 20,
		DumpPath:      sharedPath,
		DumpExists:    true,
	}).Error)
	require.NoError(t, db.Create(&SensitiveBlockLog{
		MatchedWordId: 21,
		DumpPath:      sharedPath,
		DumpExists:    true,
	}).Error)
	require.NoError(t, MarkSensitiveDumpsCleaned([]string{sharedPath}))
	var cleaned []SensitiveBlockLog
	require.NoError(t, db.Where("dump_path = ?", sharedPath).Order("id asc").Find(&cleaned).Error)
	require.Len(t, cleaned, 2)
	require.False(t, cleaned[0].DumpExists)
	require.False(t, cleaned[1].DumpExists)
	require.NoError(t, MarkSensitiveDumpsCleaned([]string{sharedPath}))
	require.NoError(t, MarkSensitiveDumpsCleaned([]string{"/legacy/orphan.json.gz"}))
}

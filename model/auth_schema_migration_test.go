package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const authSchemaIntegrationDatabase = "newapi_auth_schema_test"

func authSchemaModels() []interface{} {
	return []interface{}{
		&Channel{},
		&User{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
	}
}

func resetAuthSchemaTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Migrator().DropTable(
		&ExternalIdentityClaim{},
		&AuthFlow{},
		&UserSession{},
		&User{},
		&Channel{},
	))
}

type legacyAuthUser struct {
	Id       int     `gorm:"primaryKey"`
	Username string  `gorm:"unique"`
	Password string  `gorm:"not null"`
	AffCode  string  `gorm:"type:varchar(32);uniqueIndex"`
	Rpm24h   float64 `gorm:"column:rpm_24h"`
}

func (legacyAuthUser) TableName() string {
	return "users"
}

func prepareLegacyAuthUserSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Dialector.Name() == "postgres" {
		// The production migration is incremental over an existing users table.
		// Keep rpm_24h in this fixture because the local User model's historical
		// MySQL-specific "double" type cannot bootstrap a fresh PostgreSQL table.
		require.NoError(t, db.Exec(`
			CREATE TABLE users (
				id BIGSERIAL PRIMARY KEY,
				username TEXT UNIQUE,
				password TEXT NOT NULL,
				aff_code VARCHAR(32) UNIQUE,
				rpm_24h DOUBLE PRECISION NOT NULL DEFAULT 0
			)
		`).Error)
		return
	}

	require.NoError(t, db.AutoMigrate(&User{}))
	require.NoError(t, db.Migrator().DropColumn(&User{}, "AuthVersion"))
}

func exerciseAuthSchemaMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	resetAuthSchemaTables(t, db)
	t.Cleanup(func() {
		resetAuthSchemaTables(t, db)
	})

	prepareLegacyAuthUserSchema(t, db)

	legacy := legacyAuthUser{
		Username: "legacy-auth-user",
		Password: "legacy-password",
		AffCode:  "legacy-auth-user",
	}
	require.NoError(t, db.Create(&legacy).Error)

	require.NoError(t, db.AutoMigrate(authSchemaModels()...))
	require.NoError(t, db.AutoMigrate(authSchemaModels()...), "schema migration must be idempotent")

	for _, table := range []interface{}{&UserSession{}, &AuthFlow{}, &ExternalIdentityClaim{}} {
		require.True(t, db.Migrator().HasTable(table))
		var count int64
		require.NoError(t, db.Model(table).Count(&count).Error)
		require.Zero(t, count, "schema-only migration must not create runtime auth records")
	}

	var authVersion int64
	require.NoError(t, db.Model(&User{}).Where("id = ?", legacy.Id).
		Pluck("auth_version", &authVersion).Error)
	require.Equal(t, int64(1), authVersion, "existing users must receive the initial auth fence")

	for _, check := range []struct {
		model interface{}
		index string
	}{
		{&UserSession{}, "idx_user_sessions_user_status_expiry"},
		{&UserSession{}, "idx_user_sessions_user_created"},
		{&UserSession{}, "idx_user_sessions_status_revoked"},
		{&UserSession{}, "idx_user_sessions_expires_at"},
		{&AuthFlow{}, "idx_auth_flows_token_hash"},
		{&AuthFlow{}, "idx_auth_flow_purpose_expiry"},
		{&ExternalIdentityClaim{}, "idx_external_identity_subject"},
		{&ExternalIdentityClaim{}, "idx_external_identity_user"},
	} {
		require.Truef(t, db.Migrator().HasIndex(check.model, check.index), "missing index %s", check.index)
	}

	exerciseUserSessionRefreshRotation(t, db)
	exerciseUserAuthVersionBump(t, db, legacy.Id)
}

func exerciseUserAuthVersionBump(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	previousDB := DB
	previousRedisEnabled := common.RedisEnabled
	DB = db
	common.RedisEnabled = false
	t.Cleanup(func() {
		DB = previousDB
		common.RedisEnabled = previousRedisEnabled
	})

	next, err := BumpUserAuthVersion(userID)
	require.NoError(t, err)
	require.Equal(t, int64(2), next)

	var stored int64
	require.NoError(t, db.Model(&User{}).
		Where("id = ?", userID).
		Pluck("auth_version", &stored).Error)
	require.Equal(t, next, stored)
}

func exerciseUserSessionRefreshRotation(t *testing.T, db *gorm.DB) {
	t.Helper()
	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
	})

	now := time.Now().Unix()
	session := &UserSession{
		SID:             "auth-schema-refresh-session",
		UserID:          9001,
		Version:         1,
		UserAuthVersion: 1,
		Status:          UserSessionStatusActive,
		RefreshHash:     strings.Repeat("a", 64),
		LoginMethod:     "test",
		CreatedAt:       now,
		LastActiveAt:    now,
		ExpiresAt:       now + 3600,
	}
	require.NoError(t, CreateUserSession(session))

	type rotationResult struct {
		session *UserSession
		err     error
	}
	start := make(chan struct{})
	results := make(chan rotationResult, 2)
	for range 2 {
		go func() {
			<-start
			rotated, err := RotateUserSessionRefresh(
				session.UserID,
				session.SID,
				strings.Repeat("a", 64),
				strings.Repeat("b", 64),
				now,
				30*time.Second,
			)
			results <- rotationResult{session: rotated, err: err}
		}()
	}
	close(start)

	var successes, recoveries int
	for range 2 {
		result := <-results
		require.NotNil(t, result.session)
		require.Equal(t, strings.Repeat("b", 64), result.session.RefreshHash)
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, ErrUserSessionRefreshRace):
			recoveries++
		default:
			require.NoError(t, result.err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, recoveries)

	_, err := RotateUserSessionRefresh(
		session.UserID,
		session.SID,
		strings.Repeat("c", 64),
		strings.Repeat("d", 64),
		now,
		30*time.Second,
	)
	require.ErrorIs(t, err, ErrUserSessionRefreshInvalid)
	current, err := GetUserSessionBySID(session.SID)
	require.NoError(t, err)
	require.Equal(t, UserSessionStatusActive, current.Status)

	_, err = RotateUserSessionRefresh(
		session.UserID,
		session.SID,
		strings.Repeat("a", 64),
		strings.Repeat("b", 64),
		now+31,
		30*time.Second,
	)
	require.True(t, errors.Is(err, ErrUserSessionRefreshReuse))
	current, err = GetUserSessionBySID(session.SID)
	require.NoError(t, err)
	require.Equal(t, UserSessionStatusRevoked, current.Status)
	require.Equal(t, "refresh_reuse", current.RevokedReason)
}

func TestAuthSchemaFieldContracts(t *testing.T) {
	statement := &gorm.Statement{DB: DB}
	require.NoError(t, statement.Parse(&UserSession{}))

	previousHash := statement.Schema.LookUpField("PreviousRefreshHash")
	require.NotNil(t, previousHash)
	require.Equal(t, "varchar(64)", previousHash.TagSettings["TYPE"])
	require.False(t, previousHash.NotNull)

	refreshHash := statement.Schema.LookUpField("RefreshHash")
	require.NotNil(t, refreshHash)
	require.Equal(t, "char(64)", refreshHash.TagSettings["TYPE"])
	require.True(t, refreshHash.NotNull)

	require.NoError(t, statement.Parse(&User{}))
	authVersion := statement.Schema.LookUpField("AuthVersion")
	require.NotNil(t, authVersion)
	require.Equal(t, "1", authVersion.TagSettings["DEFAULT"])
	require.True(t, authVersion.NotNull)

	require.NoError(t, statement.Parse(&Channel{}))
	for _, fieldName := range []string{"RpmLast24h", "PeakRpm"} {
		field := statement.Schema.LookUpField(fieldName)
		require.NotNil(t, field)
		require.Equal(t, "double precision", field.TagSettings["TYPE"])
	}
}

func TestAuthSchemaMigrationSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth-schema.db")
	db, err := gorm.Open(
		sqlite.Open(path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	exerciseAuthSchemaMigration(t, db)
}

func requireDedicatedAuthSchemaDatabase(t *testing.T, db *gorm.DB, dialect string) {
	t.Helper()
	var current string
	query := "SELECT current_database()"
	if dialect == "mysql" {
		query = "SELECT DATABASE()"
	}
	require.NoError(t, db.Raw(query).Scan(&current).Error)
	require.Equal(t, authSchemaIntegrationDatabase, current,
		"refusing destructive auth schema test outside the dedicated database")
}

func TestAuthSchemaMigrationConfiguredDatabases(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		dialector func(string) gorm.Dialector
	}{
		{
			name: "mysql",
			env:  "AUTH_SCHEMA_TEST_MYSQL_DSN",
			dialector: func(dsn string) gorm.Dialector {
				return mysql.Open(dsn)
			},
		},
		{
			name: "postgres",
			env:  "AUTH_SCHEMA_TEST_POSTGRES_DSN",
			dialector: func(dsn string) gorm.Dialector {
				return postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(test.env))
			if dsn == "" {
				t.Skip(fmt.Sprintf("%s is not configured", test.env))
			}
			db, err := gorm.Open(test.dialector(dsn), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, sqlDB.Close())
			})
			requireDedicatedAuthSchemaDatabase(t, db, test.name)
			exerciseAuthSchemaMigration(t, db)
		})
	}
}

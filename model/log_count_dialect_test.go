package model

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestBoundedLogCountQuerySupportsAllDatabaseDialects(t *testing.T) {
	tests := []struct {
		name      string
		dialector gorm.Dialector
	}{
		{
			name:      "sqlite",
			dialector: sqlite.Open(":memory:"),
		},
		{
			name: "mysql",
			dialector: mysql.New(mysql.Config{
				DSN:                       "gorm:gorm@tcp(localhost:9910)/gorm?charset=utf8",
				SkipInitializeWithVersion: true,
			}),
		},
		{
			name: "postgres",
			dialector: postgres.New(postgres.Config{
				DSN:                  "host=localhost user=gorm dbname=gorm sslmode=disable",
				PreferSimpleProtocol: true,
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(test.dialector, &gorm.Config{
				DisableAutomaticPing: true,
				DryRun:               true,
			})
			require.NoError(t, err)

			query := db.Where("logs.user_id = ?", 7)
			var total int64
			result := buildBoundedLogCountQuery(query, logSearchCountLimit+1).
				Count(&total)
			require.NoError(t, result.Error)

			sql := strings.ToUpper(result.Statement.SQL.String())
			require.Contains(t, sql, "FROM (SELECT")
			require.Contains(t, sql, "ORDER BY LOGS.ID DESC")
			require.Contains(t, sql, "LIMIT")
			require.Contains(t, sql, "BOUNDED_LOGS")
		})
	}
}

func TestBoundedSparseUserLogQueryKeepsMySQLForceIndex(t *testing.T) {
	db, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "gorm:gorm@tcp(localhost:9910)/gorm?charset=utf8",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DisableAutomaticPing: true,
		DryRun:               true,
	})
	require.NoError(t, err)

	query := db.Table(userLogsTable(db, LogTypeRefund, 100, 200, false)).
		Where("logs.user_id = ? AND logs.type = ?", 7, LogTypeRefund).
		Where("logs.created_at >= ? AND logs.created_at <= ?", 100, 200)

	var logs []*Log
	listResult := query.Order("logs.id DESC").Limit(100).Find(&logs)
	require.NoError(t, listResult.Error)
	require.Contains(
		t,
		listResult.Statement.SQL.String(),
		"FROM logs FORCE INDEX (`"+logCreatedAtTypeIndexName+"`)",
	)

	var total int64
	countResult := buildBoundedLogCountQuery(query, logSearchCountLimit+1).Count(&total)
	require.NoError(t, countResult.Error)
	require.Contains(
		t,
		countResult.Statement.SQL.String(),
		"FROM logs FORCE INDEX (`"+logCreatedAtTypeIndexName+"`)",
	)
}

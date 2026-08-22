package model

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type lockingProbe struct {
	ID int
}

func TestWithForUpdateUsesDialectLockingClause(t *testing.T) {
	tests := []struct {
		name          string
		dialector     gorm.Dialector
		wantForUpdate bool
	}{
		{
			name:          "mysql",
			dialector:     mysql.New(mysql.Config{DSN: "root:pass@tcp(127.0.0.1:3306)/test", SkipInitializeWithVersion: true}),
			wantForUpdate: true,
		},
		{
			name:          "postgres",
			dialector:     postgres.New(postgres.Config{DSN: "host=127.0.0.1 user=test password=test dbname=test sslmode=disable"}),
			wantForUpdate: true,
		},
		{
			name:          "sqlite",
			dialector:     sqlite.Open(":memory:"),
			wantForUpdate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := gorm.Open(tt.dialector, &gorm.Config{
				DryRun:               true,
				DisableAutomaticPing: true,
			})
			if err != nil {
				t.Fatalf("open %s dry-run database: %v", tt.name, err)
			}

			statement := withForUpdate(db).
				Where("id = ?", 1).
				Find(&lockingProbe{}).
				Statement
			sql := strings.ToUpper(statement.SQL.String())
			hasForUpdate := strings.Contains(sql, "FOR UPDATE")
			if hasForUpdate != tt.wantForUpdate {
				t.Fatalf("%s SQL = %q, has FOR UPDATE = %v, want %v", tt.name, sql, hasForUpdate, tt.wantForUpdate)
			}
		})
	}
}

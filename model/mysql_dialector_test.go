package model

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestSameColumnDefaultComparesValuesNotSpelling(t *testing.T) {
	boolField := &schema.Field{GORMDataType: schema.Bool, HasDefaultValue: true, DefaultValue: "false"}
	trueField := &schema.Field{GORMDataType: schema.Bool, HasDefaultValue: true, DefaultValue: "true"}
	decimalField := &schema.Field{GORMDataType: schema.Float, HasDefaultValue: true, DefaultValue: "0"}
	intField := &schema.Field{GORMDataType: schema.Int, HasDefaultValue: true, DefaultValue: "1"}
	stringField := &schema.Field{GORMDataType: schema.String, HasDefaultValue: true, DefaultValue: "0"}
	untagged := &schema.Field{GORMDataType: schema.Bool}

	tests := []struct {
		name   string
		field  *schema.Field
		stored string
		want   bool
	}{
		{"MySQL reads boolean false back as 0", boolField, "0", true},
		{"MySQL reads boolean true back as 1", trueField, "1", true},
		{"a different boolean is still a change", boolField, "1", false},
		{"decimal(10,6) reads 0 back as 0.000000", decimalField, "0.000000", true},
		{"a different number is still a change", decimalField, "0.000001", false},
		{"integers compare by value", intField, "1", true},
		{"strings keep exact comparison", stringField, "0.0", false},
		{"no tag, nothing to reconcile", untagged, "0", false},
		{"unparsable stored value is left to GORM", boolField, "b'0'", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sameColumnDefault(test.field, test.stored); got != test.want {
				t.Fatalf("sameColumnDefault(%q vs tag %q) = %v, want %v", test.stored, test.field.DefaultValue, got, test.want)
			}
		})
	}
}

type automigrateDefaultProbe struct {
	ID          int64   `gorm:"primaryKey"`
	Enabled     bool    `gorm:"default:true"`
	Hidden      bool    `gorm:"not null;default:false"`
	PriceAmount float64 `gorm:"type:decimal(10,6);not null;default:0"`
	Name        string  `gorm:"type:varchar(32);not null;default:''"`
}

func (automigrateDefaultProbe) TableName() string { return "automigrate_default_probe" }

type ddlRecorder struct {
	mu  sync.Mutex
	ddl []string
}

var ddlStatement = regexp.MustCompile(`(?i)^\s*(ALTER|CREATE|DROP|RENAME)\b`)

func (r *ddlRecorder) LogMode(logger.LogLevel) logger.Interface      { return r }
func (r *ddlRecorder) Info(context.Context, string, ...interface{})  {}
func (r *ddlRecorder) Warn(context.Context, string, ...interface{})  {}
func (r *ddlRecorder) Error(context.Context, string, ...interface{}) {}
func (r *ddlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	if ddlStatement.MatchString(sql) {
		r.mu.Lock()
		r.ddl = append(r.ddl, sql)
		r.mu.Unlock()
	}
}

func (r *ddlRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.ddl
	r.ddl = nil
	return out
}

func openAutomigrateMySQL(t *testing.T, dialector gorm.Dialector, rec *ddlRecorder) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(dialector, &gorm.Config{Logger: rec})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Migrator().DropTable(&automigrateDefaultProbe{})
		_ = sqlDB.Close()
	})
	_ = db.Migrator().DropTable(&automigrateDefaultProbe{})
	rec.take()
	return db
}

// BE-70: every master start re-ran ALTER TABLE on 13 tables because MySQL spells boolean and
// decimal defaults differently from the struct tags. The dialector chooseDB opens must make a
// second AutoMigrate a no-op; the stock dialector is the control that shows the test bites.
func TestMySQLAutoMigrateLeavesMatchingDefaultsAlone(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("AUTOMIGRATE_TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("AUTOMIGRATE_TEST_MYSQL_DSN is not set")
	}

	stockRec := &ddlRecorder{}
	stock := openAutomigrateMySQL(t, mysql.Open(dsn), stockRec)
	if err := stock.AutoMigrate(&automigrateDefaultProbe{}); err != nil {
		t.Fatalf("stock first AutoMigrate: %v", err)
	}
	stockRec.take()
	if err := stock.AutoMigrate(&automigrateDefaultProbe{}); err != nil {
		t.Fatalf("stock second AutoMigrate: %v", err)
	}
	if ddl := stockRec.take(); len(ddl) == 0 {
		t.Fatal("control: the stock MySQL dialector no longer re-alters these columns, so this test proves nothing")
	}

	rec := &ddlRecorder{}
	db := openAutomigrateMySQL(t, newMySQLDialector(dsn), rec)
	if err := db.AutoMigrate(&automigrateDefaultProbe{}); err != nil {
		t.Fatalf("first AutoMigrate: %v", err)
	}
	if ddl := rec.take(); len(ddl) != 1 || !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(ddl[0])), "CREATE TABLE") {
		t.Fatalf("first AutoMigrate DDL = %q, want exactly the CREATE TABLE", ddl)
	}
	if err := db.AutoMigrate(&automigrateDefaultProbe{}); err != nil {
		t.Fatalf("second AutoMigrate: %v", err)
	}
	if ddl := rec.take(); len(ddl) != 0 {
		t.Fatalf("second AutoMigrate must issue no DDL, got %q", ddl)
	}

	// A real default change must still be migrated.
	if err := db.Exec("ALTER TABLE automigrate_default_probe MODIFY COLUMN enabled boolean DEFAULT false").Error; err != nil {
		t.Fatalf("drift default: %v", err)
	}
	rec.take()
	if err := db.AutoMigrate(&automigrateDefaultProbe{}); err != nil {
		t.Fatalf("AutoMigrate after drift: %v", err)
	}
	if ddl := rec.take(); len(ddl) != 1 || !strings.Contains(ddl[0], "`enabled`") {
		t.Fatalf("a changed default must be migrated back, got %q", ddl)
	}

	// Wrapping must keep the dialector's savepoints, or every nested transaction would fail.
	err := db.Transaction(func(tx *gorm.DB) error {
		return tx.Transaction(func(inner *gorm.DB) error {
			return inner.Create(&automigrateDefaultProbe{Name: "nested"}).Error
		})
	})
	if err != nil {
		t.Fatalf("nested transaction through the wrapped dialector: %v", err)
	}
}

// The fix only counts if production opens MySQL through it.
func TestChooseDBOpensMySQLThroughTheDefaultAwareDialector(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("AUTOMIGRATE_TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("AUTOMIGRATE_TEST_MYSQL_DSN is not set")
	}
	t.Setenv("AUTOMIGRATE_TEST_CHOOSE_DB_DSN", dsn)
	previous := common.UsingMySQL
	t.Cleanup(func() {
		common.UsingMySQL = previous
		initCol()
	})

	db, err := chooseDB("AUTOMIGRATE_TEST_CHOOSE_DB_DSN", false)
	if err != nil {
		t.Fatalf("chooseDB: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if _, ok := db.Dialector.(mysqlDialector); !ok {
		t.Fatalf("chooseDB opened MySQL with %T, want mysqlDialector", db.Dialector)
	}
}

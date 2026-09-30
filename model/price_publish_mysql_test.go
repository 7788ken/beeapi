package model

import (
	"os"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 生产 MySQL 开着 STRICT_TRANS_TABLES。snapshot 列升级成 mediumtext 之后，只要结构体
// 期望的类型还是 text，每次启动 AutoMigrate 都会把它 MODIFY 回 text；一旦有快照超过
// 64KB，这条 ALTER 报 Data too long，InitDB 返回错误，进程 FatalLog 退出起不来。
// 需要真实 MySQL：CB_MYSQL_DSN='root:pw@tcp(127.0.0.1:33307)/repro'
func TestPricePublishSnapshotSurvivesRestartAutoMigrateOnStrictMySQL(t *testing.T) {
	dsn := os.Getenv("CB_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CB_MYSQL_DSN not set; this regression only reproduces on a real strict-mode MySQL")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	_ = db.Migrator().DropTable(&PricePublishBatch{})
	t.Cleanup(func() { _ = db.Migrator().DropTable(&PricePublishBatch{}) })

	if err := db.AutoMigrate(&PricePublishBatch{}); err != nil {
		t.Fatalf("first AutoMigrate: %v", err)
	}
	// 线上现状：早先的版本建出 text，随后被一次性迁移升级成 mediumtext。
	if err := db.Exec("ALTER TABLE price_publish_batches MODIFY COLUMN snapshot MEDIUMTEXT").Error; err != nil {
		t.Fatalf("widen snapshot: %v", err)
	}
	big := strings.Repeat("x", 70*1024)
	if err := db.Exec("INSERT INTO price_publish_batches (created_at, snapshot) VALUES (?, ?)", 1, big).Error; err != nil {
		t.Fatalf("insert 70KB snapshot: %v", err)
	}

	// 下一次进程启动。
	if err := db.AutoMigrate(&PricePublishBatch{}); err != nil {
		t.Fatalf("a restart must not narrow the snapshot column back to text: %v", err)
	}
	var colType string
	if err := db.Raw(`SELECT DATA_TYPE FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = 'price_publish_batches' AND column_name = 'snapshot'`).
		Scan(&colType).Error; err != nil {
		t.Fatalf("read column type: %v", err)
	}
	if !strings.EqualFold(colType, "mediumtext") {
		t.Fatalf("snapshot column = %q after restart, want mediumtext", colType)
	}
	var n int
	if err := db.Raw("SELECT LENGTH(snapshot) FROM price_publish_batches LIMIT 1").Scan(&n).Error; err != nil {
		t.Fatalf("read snapshot length: %v", err)
	}
	if n != len(big) {
		t.Fatalf("snapshot length = %d after restart, want %d", n, len(big))
	}
}

// 删掉了那段一次性加宽迁移以后，从没升级过的老库（列还是 text）必须由 AutoMigrate
// 自己升到 mediumtext，否则这类站点会永远卡在 64KB 上限。
func TestPricePublishSnapshotLegacyTextColumnIsWidenedByAutoMigrate(t *testing.T) {
	dsn := os.Getenv("CB_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CB_MYSQL_DSN not set; this regression only reproduces on a real strict-mode MySQL")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	_ = db.Migrator().DropTable(&PricePublishBatch{})
	t.Cleanup(func() { _ = db.Migrator().DropTable(&PricePublishBatch{}) })

	if err := db.AutoMigrate(&PricePublishBatch{}); err != nil {
		t.Fatalf("first AutoMigrate: %v", err)
	}
	if err := db.Exec("ALTER TABLE price_publish_batches MODIFY COLUMN snapshot TEXT").Error; err != nil {
		t.Fatalf("simulate legacy text column: %v", err)
	}
	if err := db.AutoMigrate(&PricePublishBatch{}); err != nil {
		t.Fatalf("AutoMigrate over legacy text column: %v", err)
	}
	var colType string
	if err := db.Raw(`SELECT DATA_TYPE FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = 'price_publish_batches' AND column_name = 'snapshot'`).
		Scan(&colType).Error; err != nil {
		t.Fatalf("read column type: %v", err)
	}
	if !strings.EqualFold(colType, "mediumtext") {
		t.Fatalf("legacy snapshot column = %q after AutoMigrate, want mediumtext", colType)
	}
	big := strings.Repeat("y", 70*1024)
	if err := db.Create(&PricePublishBatch{CreatedAt: 2, Snapshot: PricePublishSnapshot(big)}).Error; err != nil {
		t.Fatalf("write a 70KB snapshot through GORM: %v", err)
	}
	var got PricePublishBatch
	if err := db.Where("created_at = ?", 2).First(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got.Snapshot) != big {
		t.Fatalf("snapshot round trip lost data: got %d bytes, want %d", len(got.Snapshot), len(big))
	}
}

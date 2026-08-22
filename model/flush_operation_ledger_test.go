package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const flushOperationIntegrationTestDatabase = "newapi_flush_operation_test"

type flushOperationIntegrationCounter struct {
	ID    int `gorm:"primaryKey"`
	Value int
}

func (flushOperationIntegrationCounter) TableName() string {
	return "flush_operation_integration_counters"
}

func TestFlushOperationSchemaReadyAfterMigration(t *testing.T) {
	if err := CheckFlushOperationSchemaReady(); err != nil {
		t.Fatalf("check flush operation schema: %v", err)
	}
}

func TestApplyFlushOperationRollsBackLedgerWithBusinessFailure(t *testing.T) {
	truncateTables(t)
	payloadHash, err := flushOperationPayloadHash(struct {
		Delta int
	}{Delta: 7})
	if err != nil {
		t.Fatalf("hash payload: %v", err)
	}
	forcedError := errors.New("forced business mutation failure")
	_, err = applyFlushOperation(
		context.Background(),
		"batch-rollback-test",
		"batch-user",
		payloadHash,
		func(*gorm.DB) (string, error) {
			return "", forcedError
		},
	)
	if !errors.Is(err, forcedError) {
		t.Fatalf("apply error = %v, want forced failure", err)
	}

	var count int64
	if err := DB.Model(&FlushOperationLedger{}).
		Where("operation_id = ?", "batch-rollback-test").
		Count(&count).Error; err != nil {
		t.Fatalf("count rolled-back ledger: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled-back ledger count = %d, want 0", count)
	}
}

func TestApplyFlushOperationRejectsOperationIDPayloadMutation(t *testing.T) {
	truncateTables(t)
	firstHash, err := flushOperationPayloadHash(struct {
		Delta int
	}{Delta: 7})
	if err != nil {
		t.Fatalf("hash first payload: %v", err)
	}
	secondHash, err := flushOperationPayloadHash(struct {
		Delta int
	}{Delta: 8})
	if err != nil {
		t.Fatalf("hash second payload: %v", err)
	}

	result, err := applyFlushOperation(
		context.Background(),
		"batch-immutable-test",
		"batch-user",
		firstHash,
		func(*gorm.DB) (string, error) {
			return flushOperationResultBatchUpdated, nil
		},
	)
	if err != nil {
		t.Fatalf("apply first payload: %v", err)
	}
	if result != flushOperationResultBatchUpdated {
		t.Fatalf("first result = %q", result)
	}

	replayedApplyCalled := false
	_, err = applyFlushOperation(
		context.Background(),
		"batch-immutable-test",
		"batch-user",
		secondHash,
		func(*gorm.DB) (string, error) {
			replayedApplyCalled = true
			return flushOperationResultBatchUpdated, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("mutated replay error = %v, want identity mismatch", err)
	}
	if replayedApplyCalled {
		t.Fatal("mutated replay executed the business mutation")
	}
}

func TestDeleteFlushOperationLedgersChunksConfirmedOperations(t *testing.T) {
	truncateTables(t)
	const operationCount = flushOperationCleanupChunkSize + 5
	operationIDs := make([]string, 0, operationCount)
	ledgers := make([]FlushOperationLedger, 0, operationCount)
	for index := range operationCount {
		operationID := fmt.Sprintf("cleanup-%03d", index)
		operationIDs = append(operationIDs, operationID)
		ledgers = append(ledgers, FlushOperationLedger{
			OperationID: operationID,
			Scope:       "batch-token",
			PayloadHash: strings.Repeat("a", 64),
			ResultCode:  flushOperationResultBatchUpdated,
			AppliedAt:   1,
		})
	}
	if err := DB.Create(&ledgers).Error; err != nil {
		t.Fatalf("seed confirmed flush operations: %v", err)
	}
	if err := deleteFlushOperationLedgers(context.Background(), operationIDs); err != nil {
		t.Fatalf("delete confirmed flush operations: %v", err)
	}
	var count int64
	if err := DB.Model(&FlushOperationLedger{}).Count(&count).Error; err != nil {
		t.Fatalf("count confirmed flush operations after cleanup: %v", err)
	}
	if count != 0 {
		t.Fatalf("confirmed flush operation ledgers after cleanup = %d, want 0", count)
	}
}

func TestFlushOperationLedgerOnRealDatabase(t *testing.T) {
	dialect := os.Getenv("FLUSH_OPERATION_TEST_DIALECT")
	dsn := os.Getenv("FLUSH_OPERATION_TEST_DSN")
	if dialect == "" || dsn == "" {
		t.Skip("real flush operation database is not configured")
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
		t.Fatalf("unsupported FLUSH_OPERATION_TEST_DIALECT %q", dialect)
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open %s integration database: %v", dialect, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get %s sql database: %v", dialect, err)
	}
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	var databaseName string
	switch dialect {
	case "mysql":
		err = db.Raw("SELECT DATABASE()").Scan(&databaseName).Error
	case "postgres":
		err = db.Raw("SELECT current_database()").Scan(&databaseName).Error
	}
	if err != nil {
		t.Fatalf("read %s database name: %v", dialect, err)
	}
	if databaseName != flushOperationIntegrationTestDatabase {
		t.Fatalf(
			"integration test database = %q, want dedicated disposable database %q",
			databaseName,
			flushOperationIntegrationTestDatabase,
		)
	}

	if err := db.Migrator().DropTable(&FlushOperationLedger{}, &flushOperationIntegrationCounter{}); err != nil {
		t.Fatalf("drop old integration tables: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Migrator().DropTable(&FlushOperationLedger{}, &flushOperationIntegrationCounter{})
	})
	if err := db.AutoMigrate(&FlushOperationLedger{}, &flushOperationIntegrationCounter{}); err != nil {
		t.Fatalf("migrate integration tables: %v", err)
	}

	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
	})
	if err := CheckFlushOperationSchemaReady(); err != nil {
		t.Fatalf("check %s flush operation schema: %v", dialect, err)
	}

	payloadHash, err := flushOperationPayloadHash(struct {
		Delta int
	}{Delta: 1})
	if err != nil {
		t.Fatalf("hash integration payload: %v", err)
	}
	const operationID = "batch-real-database-test"
	applyCounter := func(tx *gorm.DB) (string, error) {
		result := tx.Model(&flushOperationIntegrationCounter{}).
			Where("id = ?", 1).
			Update("value", gorm.Expr("value + ?", 1))
		if result.Error != nil {
			return "", result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Create(&flushOperationIntegrationCounter{ID: 1, Value: 1}).Error; err != nil {
				return "", err
			}
		}
		return flushOperationResultBatchUpdated, nil
	}

	const callers = 2
	results := make(chan error, callers)
	var start sync.WaitGroup
	start.Add(callers)
	release := make(chan struct{})
	for range callers {
		go func() {
			start.Done()
			<-release
			_, err := applyFlushOperation(
				context.Background(),
				operationID,
				"batch-user",
				payloadHash,
				applyCounter,
			)
			results <- err
		}()
	}
	start.Wait()
	close(release)
	for range callers {
		if err := <-results; err != nil {
			t.Fatalf("concurrent %s replay: %v", dialect, err)
		}
	}

	var counter flushOperationIntegrationCounter
	if err := db.First(&counter, 1).Error; err != nil {
		t.Fatalf("read %s integration counter: %v", dialect, err)
	}
	if counter.Value != 1 {
		t.Fatalf("%s integration counter = %d, want exactly once", dialect, counter.Value)
	}
	var ledger FlushOperationLedger
	if err := db.First(&ledger, "operation_id = ?", operationID).Error; err != nil {
		t.Fatalf("read %s integration ledger: %v", dialect, err)
	}
	if ledger.ResultCode != flushOperationResultBatchUpdated {
		t.Fatalf("%s integration result code = %q", dialect, ledger.ResultCode)
	}
}

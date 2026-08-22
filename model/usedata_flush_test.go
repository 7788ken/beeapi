package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSaveQuotaDataCacheDoesNotBlockLoggingAndKeepsFailedGenerationSeparate(t *testing.T) {
	db := setupQuotaDataTestDB(t)
	isolateQuotaDataCache(t)

	const createdAt = int64(7205)
	LogQuotaData(0, "flush-user", "image-model", "image-group", "subscription", 100, createdAt, 4)
	LogQuotaData(0, "flush-user", "text-model", "image-group", "subscription", 50, createdAt, 2)

	queryStarted := make(chan struct{})
	releaseQuery := make(chan struct{})
	var releaseQueryOnce sync.Once
	releaseBlockedQuery := func() {
		releaseQueryOnce.Do(func() {
			close(releaseQuery)
		})
	}
	defer releaseBlockedQuery()
	forcedError := errors.New("forced quota flush failure")
	var queryOnce sync.Once
	if err := db.Callback().Query().Before("gorm:query").Register("test:block_quota_flush", func(tx *gorm.DB) {
		queryOnce.Do(func() {
			close(queryStarted)
			select {
			case <-releaseQuery:
			case <-time.After(5 * time.Second):
			}
		})
		tx.AddError(forcedError)
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	flushDone := make(chan error, 1)
	go func() {
		flushDone <- SaveQuotaDataCache()
	}()

	if !waitForSignal(queryStarted, 2*time.Second) {
		releaseBlockedQuery()
		if _, completed := waitForError(flushDone, 2*time.Second); !completed {
			t.Fatal("quota flush did not reach the blocked query or stop after release")
		}
		t.Fatal("quota flush did not reach the blocked database query")
	}

	logDone := make(chan struct{})
	go func() {
		LogQuotaData(0, "flush-user", "image-model", "image-group", "subscription", 200, createdAt, 6)
		close(logDone)
	}()

	logCompletedWhileDBBlocked := waitForSignal(logDone, 2*time.Second)

	releaseBlockedQuery()
	flushErr, flushCompleted := waitForError(flushDone, 2*time.Second)
	if !logCompletedWhileDBBlocked {
		if !waitForSignal(logDone, 2*time.Second) {
			t.Fatal("LogQuotaData remained blocked after quota flush database I/O was released")
		}
		t.Fatal("LogQuotaData was blocked by quota flush database I/O")
	}
	if !flushCompleted {
		t.Fatal("SaveQuotaDataCache did not return after releasing blocked database I/O")
	}
	if !errors.Is(flushErr, forcedError) {
		t.Fatalf("SaveQuotaDataCache error = %v, want forced database error", flushErr)
	}
	if !strings.Contains(flushErr.Error(), "failed to save 2 of 2 quota cache entries") {
		t.Fatalf("SaveQuotaDataCache error = %q, want bounded failure count", flushErr)
	}
	if strings.Count(flushErr.Error(), forcedError.Error()) != 1 {
		t.Fatalf("SaveQuotaDataCache error repeated first database error: %q", flushErr)
	}
	if strings.Contains(flushErr.Error(), "flush-user") {
		t.Fatalf("SaveQuotaDataCache error leaked cache keys: %q", flushErr)
	}

	liveEntries := quotaDataCacheEntries()
	if len(liveEntries) != 1 {
		t.Fatalf("CacheQuotaData entries = %d, want one new live entry", len(liveEntries))
	}
	if liveEntries[0].Count != 1 || liveEntries[0].Quota != 200 || liveEntries[0].TokenUsed != 6 {
		t.Fatalf("new live image-model entry = %+v", liveEntries[0])
	}

	inFlightEntries := quotaDataInFlightEntries()
	if len(inFlightEntries) != 2 {
		t.Fatalf("quotaDataInFlight entries = %d, want two failed snapshot entries", len(inFlightEntries))
	}
	var failedImage *QuotaData
	for i := range inFlightEntries {
		if inFlightEntries[i].Data.ModelName == "image-model" {
			failedImage = &inFlightEntries[i].Data
			break
		}
	}
	if failedImage == nil {
		t.Fatal("failed image-model in-flight entry not found")
	}
	if failedImage.Count != 1 || failedImage.Quota != 100 || failedImage.TokenUsed != 4 {
		t.Fatalf("failed image-model in-flight entry = %+v", *failedImage)
	}
}

func TestSaveQuotaDataCacheUpdatesOnlyOneDuplicateBusinessKeyRow(t *testing.T) {
	db := setupQuotaDataTestDB(t)
	isolateQuotaDataCache(t)

	const createdAt = int64(14400)
	duplicates := []QuotaData{
		{
			UserID:        0,
			Username:      "duplicate-user",
			ModelName:     "image-model",
			Group:         "image-group",
			BillingSource: "wallet",
			CreatedAt:     createdAt,
			Count:         2,
			Quota:         20,
			TokenUsed:     3,
		},
		{
			UserID:        0,
			Username:      "duplicate-user",
			ModelName:     "image-model",
			Group:         "image-group",
			BillingSource: "wallet",
			CreatedAt:     createdAt,
			Count:         5,
			Quota:         50,
			TokenUsed:     7,
		},
	}
	if result := db.Create(&duplicates); result.Error != nil || result.RowsAffected != 2 {
		t.Fatalf("seed duplicate quota rows: rows=%d err=%v", result.RowsAffected, result.Error)
	}

	LogQuotaData(0, "duplicate-user", "image-model", "image-group", "wallet", 100, createdAt, 4)
	if err := SaveQuotaDataCache(); err != nil {
		t.Fatalf("flush duplicate quota rows: %v", err)
	}

	var totals struct {
		Count     int
		Quota     int
		TokenUsed int
	}
	if err := db.Model(&QuotaData{}).
		Select("SUM(count) AS count, SUM(quota) AS quota, SUM(token_used) AS token_used").
		Scan(&totals).Error; err != nil {
		t.Fatalf("sum duplicate quota rows: %v", err)
	}
	if totals.Count != 8 || totals.Quota != 170 || totals.TokenUsed != 14 {
		t.Fatalf("duplicate totals = %+v, want count=8 quota=170 token_used=14", totals)
	}
}

func TestSaveQuotaDataCacheCreatesThenIncrements(t *testing.T) {
	db := setupQuotaDataTestDB(t)
	isolateQuotaDataCache(t)

	const createdAt = int64(10805)
	LogQuotaData(0, "flush-user", "image-model", "image-group", "wallet", 100, createdAt, 4)
	if err := SaveQuotaDataCache(); err != nil {
		t.Fatalf("create quota data: %v", err)
	}
	if entries := quotaDataCacheEntries(); len(entries) != 0 {
		t.Fatalf("CacheQuotaData entries after create flush = %d, want 0", len(entries))
	}

	var persisted QuotaData
	if err := db.First(&persisted).Error; err != nil {
		t.Fatalf("read created quota data: %v", err)
	}
	if persisted.Count != 1 || persisted.Quota != 100 || persisted.TokenUsed != 4 {
		t.Fatalf("created quota data = %+v", persisted)
	}

	LogQuotaData(0, "flush-user", "image-model", "image-group", "wallet", 200, createdAt, 6)
	if err := SaveQuotaDataCache(); err != nil {
		t.Fatalf("increment quota data: %v", err)
	}
	if entries := quotaDataCacheEntries(); len(entries) != 0 {
		t.Fatalf("CacheQuotaData entries after update flush = %d, want 0", len(entries))
	}

	if err := db.First(&persisted, persisted.Id).Error; err != nil {
		t.Fatalf("read incremented quota data: %v", err)
	}
	if persisted.Count != 2 || persisted.Quota != 300 || persisted.TokenUsed != 10 {
		t.Fatalf("incremented quota data = %+v", persisted)
	}
}

func TestSaveQuotaDataCacheReplaysAmbiguousCommitBeforeNewGeneration(t *testing.T) {
	db := setupQuotaDataTestDB(t)
	isolateQuotaDataCache(t)

	const createdAt = int64(21605)
	LogQuotaData(0, "ambiguous-user", "image-model", "image-group", "wallet", 100, createdAt, 4)

	forcedError := errors.New("forced ambiguous quota result after commit")
	var (
		operationIDs []string
		attempts     int
	)
	saver := func(ctx context.Context, operationID string, quotaData *QuotaData) error {
		operationIDs = append(operationIDs, operationID)
		attempts++
		if err := saveQuotaData(ctx, operationID, quotaData); err != nil {
			return err
		}
		if attempts == 1 {
			return forcedError
		}
		return nil
	}

	if err := saveQuotaDataCache(context.Background(), saver); !errors.Is(err, forcedError) {
		t.Fatalf("first quota flush error = %v, want ambiguous result", err)
	}
	LogQuotaData(0, "ambiguous-user", "image-model", "image-group", "wallet", 200, createdAt, 6)

	if err := saveQuotaDataCache(context.Background(), saver); err != nil {
		t.Fatalf("retry ambiguous quota operation: %v", err)
	}
	var afterRetry QuotaData
	if err := db.First(&afterRetry).Error; err != nil {
		t.Fatalf("read quota data after retry: %v", err)
	}
	if afterRetry.Count != 1 || afterRetry.Quota != 100 || afterRetry.TokenUsed != 4 {
		t.Fatalf("quota data after ambiguous retry = %+v, want first generation exactly once", afterRetry)
	}
	if entries := quotaDataCacheEntries(); len(entries) != 1 {
		t.Fatalf("live quota entries after old generation retry = %d, want 1", len(entries))
	}

	if err := saveQuotaDataCache(context.Background(), saver); err != nil {
		t.Fatalf("flush new quota generation: %v", err)
	}
	if err := db.First(&afterRetry, afterRetry.Id).Error; err != nil {
		t.Fatalf("read quota data after new generation: %v", err)
	}
	if afterRetry.Count != 2 || afterRetry.Quota != 300 || afterRetry.TokenUsed != 10 {
		t.Fatalf("quota data after new generation = %+v, want both generations once", afterRetry)
	}
	if len(operationIDs) != 3 {
		t.Fatalf("quota saver operation ids = %#v, want three attempts", operationIDs)
	}
	if operationIDs[0] != operationIDs[1] {
		t.Fatalf("failed quota operation id changed across retry: %q != %q", operationIDs[0], operationIDs[1])
	}
	if operationIDs[1] == operationIDs[2] {
		t.Fatalf("new quota generation reused operation id %q", operationIDs[2])
	}
}

func TestSaveQuotaDataCacheFailedEntryDoesNotBlockUnrelatedNewKey(t *testing.T) {
	setupQuotaDataTestDB(t)
	isolateQuotaDataCache(t)

	const createdAt = int64(25205)
	LogQuotaData(0, "poison-user", "poison-model", "image-group", "wallet", 100, createdAt, 4)

	forcedError := errors.New("forced poison quota entry")
	type saveCall struct {
		modelName   string
		operationID string
	}
	var (
		calls         []saveCall
		allowRecovery bool
	)
	saver := func(_ context.Context, operationID string, quotaData *QuotaData) error {
		calls = append(calls, saveCall{
			modelName:   quotaData.ModelName,
			operationID: operationID,
		})
		if quotaData.ModelName == "poison-model" && !allowRecovery {
			return forcedError
		}
		return nil
	}

	if err := saveQuotaDataCache(context.Background(), saver); !errors.Is(err, forcedError) {
		t.Fatalf("first poison flush error = %v", err)
	}
	LogQuotaData(0, "poison-user", "poison-model", "image-group", "wallet", 200, createdAt, 6)
	LogQuotaData(0, "healthy-user", "healthy-model", "image-group", "wallet", 50, createdAt, 2)

	if err := saveQuotaDataCache(context.Background(), saver); !errors.Is(err, forcedError) {
		t.Fatalf("second poison flush error = %v", err)
	}
	healthyCalls := 0
	var poisonOperationIDs []string
	for _, call := range calls {
		if call.modelName == "healthy-model" {
			healthyCalls++
		}
		if call.modelName == "poison-model" {
			poisonOperationIDs = append(poisonOperationIDs, call.operationID)
		}
	}
	if healthyCalls != 1 {
		t.Fatalf("healthy saver calls = %d, want 1 despite poison retry", healthyCalls)
	}
	if len(poisonOperationIDs) != 2 || poisonOperationIDs[0] != poisonOperationIDs[1] {
		t.Fatalf("poison operation ids before recovery = %#v, want one stable id", poisonOperationIDs)
	}
	liveEntries := quotaDataCacheEntries()
	if len(liveEntries) != 1 || liveEntries[0].ModelName != "poison-model" || liveEntries[0].Quota != 200 {
		t.Fatalf("live entries while poison generation is in flight = %+v", liveEntries)
	}

	allowRecovery = true
	if err := saveQuotaDataCache(context.Background(), saver); err != nil {
		t.Fatalf("recover poison generation: %v", err)
	}
	if entries := quotaDataCacheEntries(); len(entries) != 1 {
		t.Fatalf("new poison generation after old recovery = %d, want 1 pending", len(entries))
	}
	if err := saveQuotaDataCache(context.Background(), saver); err != nil {
		t.Fatalf("flush new poison generation: %v", err)
	}

	var allPoisonOperationIDs []string
	for _, call := range calls {
		if call.modelName == "poison-model" {
			allPoisonOperationIDs = append(allPoisonOperationIDs, call.operationID)
		}
	}
	if len(allPoisonOperationIDs) != 4 {
		t.Fatalf("poison operation ids = %#v, want fail, retry, recovery, new generation", allPoisonOperationIDs)
	}
	if allPoisonOperationIDs[0] != allPoisonOperationIDs[1] ||
		allPoisonOperationIDs[1] != allPoisonOperationIDs[2] {
		t.Fatalf("old poison generation changed operation id: %#v", allPoisonOperationIDs[:3])
	}
	if allPoisonOperationIDs[2] == allPoisonOperationIDs[3] {
		t.Fatalf("new poison generation reused operation id %q", allPoisonOperationIDs[3])
	}
}

func TestQuotaDataCacheKeySeparatesHyphenAmbiguity(t *testing.T) {
	isolateQuotaDataCache(t)

	const createdAt = int64(18000)
	oldFirstKey := fmt.Sprintf("%d-%s-%s-%s-%s-%d", 0, "a-b", "c", "group", "wallet", createdAt)
	oldSecondKey := fmt.Sprintf("%d-%s-%s-%s-%s-%d", 0, "a", "b-c", "group", "wallet", createdAt)
	if oldFirstKey != oldSecondKey {
		t.Fatalf("test inputs do not reproduce the legacy key collision: %q != %q", oldFirstKey, oldSecondKey)
	}

	LogQuotaData(0, "a-b", "c", "group", "wallet", 10, createdAt, 1)
	LogQuotaData(0, "a", "b-c", "group", "wallet", 20, createdAt, 2)

	entries := quotaDataCacheEntries()
	if len(entries) != 2 {
		t.Fatalf("CacheQuotaData entries = %d, want 2 distinct NUL-delimited keys", len(entries))
	}
}

func setupQuotaDataTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open quota data database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get quota data sql database: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&QuotaData{}, &FlushOperationLedger{}); err != nil {
		t.Fatalf("migrate quota data: %v", err)
	}
	oldDB := DB
	oldCommonGroupCol := commonGroupCol
	DB = db
	commonGroupCol = "`group`"
	t.Cleanup(func() {
		DB = oldDB
		commonGroupCol = oldCommonGroupCol
		_ = sqlDB.Close()
	})
	return db
}

func isolateQuotaDataCache(t *testing.T) {
	t.Helper()
	CacheQuotaDataLock.Lock()
	oldCacheQuotaData := CacheQuotaData
	oldQuotaDataInFlight := quotaDataInFlight
	CacheQuotaData = make(map[string]*QuotaData)
	quotaDataInFlight = make(map[string]*quotaDataFlushEntry)
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		CacheQuotaDataLock.Lock()
		CacheQuotaData = oldCacheQuotaData
		quotaDataInFlight = oldQuotaDataInFlight
		CacheQuotaDataLock.Unlock()
	})
}

func quotaDataCacheEntries() []QuotaData {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()

	entries := make([]QuotaData, 0, len(CacheQuotaData))
	for _, quotaData := range CacheQuotaData {
		entries = append(entries, *quotaData)
	}
	return entries
}

func quotaDataInFlightEntries() []quotaDataFlushEntry {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()

	entries := make([]quotaDataFlushEntry, 0, len(quotaDataInFlight))
	for _, entry := range quotaDataInFlight {
		entries = append(entries, *entry)
	}
	return entries
}

func waitForSignal(signal <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-signal:
		return true
	case <-timer.C:
		return false
	}
}

func waitForError(result <-chan error, timeout time.Duration) (error, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err, true
	case <-timer.C:
		return nil, false
	}
}

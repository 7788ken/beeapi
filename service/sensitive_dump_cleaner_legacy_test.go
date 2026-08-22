package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupLegacySensitiveDumpCleanerTest(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := model.DB
	previousUsingSQLite := common.UsingSQLite
	previousUsingMySQL := common.UsingMySQL
	previousUsingPostgreSQL := common.UsingPostgreSQL
	previousMark := markLegacySensitiveDumpsCleaned
	previousLookup := hasDurableSensitiveDump
	previousRemove := removeLegacySensitiveDump

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy-cleaner.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	require.NoError(t, db.AutoMigrate(
		&model.SensitiveAuditJobRecord{},
		&model.SensitiveBlockLog{},
	))

	t.Cleanup(func() {
		model.DB = previousDB
		common.UsingSQLite = previousUsingSQLite
		common.UsingMySQL = previousUsingMySQL
		common.UsingPostgreSQL = previousUsingPostgreSQL
		markLegacySensitiveDumpsCleaned = previousMark
		hasDurableSensitiveDump = previousLookup
		removeLegacySensitiveDump = previousRemove
		_ = sqlDB.Close()
	})

	return db
}

func createStaleLegacyDump(t *testing.T, root string, name string) string {
	t.Helper()
	path := filepath.Join(root, name+".json.gz")
	require.NoError(t, os.WriteFile(path, []byte("legacy"), 0o600))
	staleTime := time.Unix(100, 0)
	require.NoError(t, os.Chtimes(path, staleTime, staleTime))
	return path
}

func createLegacyDumpLog(t *testing.T, db *gorm.DB, path string, dumpExists bool) model.SensitiveBlockLog {
	t.Helper()
	entry := model.SensitiveBlockLog{
		RequestId:      "legacy-request",
		MatchedWordId:  1,
		MatchedPattern: "legacy",
		Action:         model.SensitiveActionMonitor,
		DumpPath:       path,
		DumpExists:     dumpExists,
	}
	require.NoError(t, db.Create(&entry).Error)
	if !dumpExists {
		require.NoError(t, db.Model(&entry).Update("dump_exists", false).Error)
		entry.DumpExists = false
	}
	return entry
}

func TestLegacyDumpCleanerMarksEveryLogBeforeDeletingSharedFile(t *testing.T) {
	db := setupLegacySensitiveDumpCleanerTest(t)
	root := t.TempDir()
	path := createStaleLegacyDump(t, root, "shared")
	first := createLegacyDumpLog(t, db, path, true)
	second := createLegacyDumpLog(t, db, path, true)

	deleted, scanned, errs := walkAndCleanDumps(root, time.Unix(200, 0))
	require.Equal(t, 1, deleted)
	require.Equal(t, 1, scanned)
	require.Zero(t, errs)
	_, statErr := os.Stat(path)
	require.ErrorIs(t, statErr, fs.ErrNotExist)

	var stored []model.SensitiveBlockLog
	require.NoError(t, db.Where("id IN ?", []int{first.Id, second.Id}).Order("id asc").Find(&stored).Error)
	require.Len(t, stored, 2)
	require.False(t, stored[0].DumpExists)
	require.False(t, stored[1].DumpExists)
}

func TestLegacyDumpCleanerDeletesStaleOrphanWhenDBMarkAffectsNoRows(t *testing.T) {
	db := setupLegacySensitiveDumpCleanerTest(t)
	root := t.TempDir()
	path := createStaleLegacyDump(t, root, "orphan")

	var logCount int64
	require.NoError(t, db.Model(&model.SensitiveBlockLog{}).Where("dump_path = ?", path).Count(&logCount).Error)
	require.Zero(t, logCount)

	deleted, scanned, errs := walkAndCleanDumps(root, time.Unix(200, 0))
	require.Equal(t, 1, deleted)
	require.Equal(t, 1, scanned)
	require.Zero(t, errs)
	_, statErr := os.Stat(path)
	require.ErrorIs(t, statErr, fs.ErrNotExist)
}

func TestLegacyDumpCleanerConcurrentScansConvergeThroughENOENT(t *testing.T) {
	db := setupLegacySensitiveDumpCleanerTest(t)
	root := t.TempDir()
	path := createStaleLegacyDump(t, root, "concurrent")
	entry := createLegacyDumpLog(t, db, path, true)

	removeArrivals := make(chan struct{}, 2)
	releaseRemoves := make(chan struct{})
	removeLegacySensitiveDump = func(path string) error {
		removeArrivals <- struct{}{}
		<-releaseRemoves
		return os.Remove(path)
	}

	type cleanResult struct {
		deleted int
		scanned int
		errs    int
	}
	start := make(chan struct{})
	results := make(chan cleanResult, 2)
	var cleaners sync.WaitGroup
	for range 2 {
		cleaners.Add(1)
		go func() {
			defer cleaners.Done()
			<-start
			deleted, scanned, errs := walkAndCleanDumps(root, time.Unix(200, 0))
			results <- cleanResult{deleted: deleted, scanned: scanned, errs: errs}
		}()
	}
	close(start)
	<-removeArrivals
	<-removeArrivals
	close(releaseRemoves)
	cleaners.Wait()
	close(results)

	for result := range results {
		require.Equal(t, 1, result.deleted)
		require.Equal(t, 1, result.scanned)
		require.Zero(t, result.errs)
	}
	_, statErr := os.Stat(path)
	require.ErrorIs(t, statErr, fs.ErrNotExist)

	var stored model.SensitiveBlockLog
	require.NoError(t, db.First(&stored, entry.Id).Error)
	require.False(t, stored.DumpExists)
}

func TestLegacyDumpCleanerKeepsWholeBatchWhenDBMarkFails(t *testing.T) {
	db := setupLegacySensitiveDumpCleanerTest(t)
	root := t.TempDir()
	path := createStaleLegacyDump(t, root, "db-failure")
	entry := createLegacyDumpLog(t, db, path, true)
	markLegacySensitiveDumpsCleaned = func([]string) error {
		return errors.New("forced DB failure")
	}

	deleted, scanned, errs := walkAndCleanDumps(root, time.Unix(200, 0))
	require.Zero(t, deleted)
	require.Equal(t, 1, scanned)
	require.Equal(t, 1, errs)
	_, statErr := os.Stat(path)
	require.NoError(t, statErr)

	var stored model.SensitiveBlockLog
	require.NoError(t, db.First(&stored, entry.Id).Error)
	require.True(t, stored.DumpExists)
}

func TestLegacyDumpCleanerRetriesFileWhoseDBFlagIsAlreadyFalse(t *testing.T) {
	db := setupLegacySensitiveDumpCleanerTest(t)
	root := t.TempDir()
	path := createStaleLegacyDump(t, root, "already-marked")
	createLegacyDumpLog(t, db, path, false)

	deleted, scanned, errs := walkAndCleanDumps(root, time.Unix(200, 0))
	require.Equal(t, 1, deleted)
	require.Equal(t, 1, scanned)
	require.Zero(t, errs)
	_, statErr := os.Stat(path)
	require.ErrorIs(t, statErr, fs.ErrNotExist)
}

func TestLegacyDumpCleanerRetriesRemoveFailureAfterDBMark(t *testing.T) {
	db := setupLegacySensitiveDumpCleanerTest(t)
	root := t.TempDir()
	path := createStaleLegacyDump(t, root, "remove-retry")
	entry := createLegacyDumpLog(t, db, path, true)
	removeLegacySensitiveDump = func(string) error {
		return errors.New("forced remove failure")
	}

	deleted, scanned, errs := walkAndCleanDumps(root, time.Unix(200, 0))
	require.Zero(t, deleted)
	require.Equal(t, 1, scanned)
	require.Equal(t, 1, errs)
	_, statErr := os.Stat(path)
	require.NoError(t, statErr)

	var stored model.SensitiveBlockLog
	require.NoError(t, db.First(&stored, entry.Id).Error)
	require.False(t, stored.DumpExists)

	removeLegacySensitiveDump = os.Remove
	deleted, scanned, errs = walkAndCleanDumps(root, time.Unix(200, 0))
	require.Equal(t, 1, deleted)
	require.Equal(t, 1, scanned)
	require.Zero(t, errs)
	_, statErr = os.Stat(path)
	require.ErrorIs(t, statErr, fs.ErrNotExist)
}

func TestLegacyDumpCleanerTreatsENOENTAsSuccessfulDeletion(t *testing.T) {
	db := setupLegacySensitiveDumpCleanerTest(t)
	root := t.TempDir()
	path := createStaleLegacyDump(t, root, "already-removed")
	createLegacyDumpLog(t, db, path, true)
	removeLegacySensitiveDump = func(path string) error {
		require.NoError(t, os.Remove(path))
		return fs.ErrNotExist
	}

	deleted, scanned, errs := walkAndCleanDumps(root, time.Unix(200, 0))
	require.Equal(t, 1, deleted)
	require.Equal(t, 1, scanned)
	require.Zero(t, errs)
}

func TestLegacyDumpCleanerSkipsDurableJobPath(t *testing.T) {
	db := setupLegacySensitiveDumpCleanerTest(t)
	root := t.TempDir()
	path := createStaleLegacyDump(t, root, "durable")
	entry := createLegacyDumpLog(t, db, path, true)
	require.NoError(t, db.Create(&model.SensitiveAuditJobRecord{
		JobID:       "durable-cleaner-job",
		StorageNode: "node-a",
		DumpPath:    path,
		BodySHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BodySize:    6,
		CreatedAt:   100,
	}).Error)

	deleted, scanned, errs := walkAndCleanDumps(root, time.Unix(200, 0))
	require.Zero(t, deleted)
	require.Equal(t, 1, scanned)
	require.Zero(t, errs)
	_, statErr := os.Stat(path)
	require.NoError(t, statErr)

	var stored model.SensitiveBlockLog
	require.NoError(t, db.First(&stored, entry.Id).Error)
	require.True(t, stored.DumpExists)
}

func TestLegacyDumpCleanerLimitsDBFirstBatchesToTwoHundredPaths(t *testing.T) {
	setupLegacySensitiveDumpCleanerTest(t)
	root := t.TempDir()
	const fileCount = legacySensitiveDumpBatch + 1
	for index := 0; index < fileCount; index++ {
		createStaleLegacyDump(t, root, "batch-"+common.GetUUID())
	}
	hasDurableSensitiveDump = func(string) (bool, error) {
		return false, nil
	}
	var batchSizes []int
	markLegacySensitiveDumpsCleaned = func(paths []string) error {
		batchSizes = append(batchSizes, len(paths))
		return nil
	}

	deleted, scanned, errs := walkAndCleanDumps(root, time.Unix(200, 0))
	require.Equal(t, fileCount, deleted)
	require.Equal(t, fileCount, scanned)
	require.Zero(t, errs)
	require.Equal(t, []int{legacySensitiveDumpBatch, 1}, batchSizes)
}

func TestLegacyDumpCleanerKeepsOnlyTheBatchWhoseDBMarkFails(t *testing.T) {
	setupLegacySensitiveDumpCleanerTest(t)
	root := t.TempDir()
	const fileCount = legacySensitiveDumpBatch + 1
	paths := make([]string, 0, fileCount)
	for index := 0; index < fileCount; index++ {
		paths = append(paths, createStaleLegacyDump(t, root, fmt.Sprintf("batch-%03d", index)))
	}
	hasDurableSensitiveDump = func(string) (bool, error) {
		return false, nil
	}
	var batchSizes []int
	markLegacySensitiveDumpsCleaned = func(paths []string) error {
		batchSizes = append(batchSizes, len(paths))
		if len(batchSizes) == 2 {
			return errors.New("forced second-batch DB failure")
		}
		return nil
	}

	deleted, scanned, errs := walkAndCleanDumps(root, time.Unix(200, 0))
	require.Equal(t, legacySensitiveDumpBatch, deleted)
	require.Equal(t, fileCount, scanned)
	require.Equal(t, 1, errs)
	require.Equal(t, []int{legacySensitiveDumpBatch, 1}, batchSizes)

	for _, path := range paths[:legacySensitiveDumpBatch] {
		_, statErr := os.Stat(path)
		require.ErrorIs(t, statErr, fs.ErrNotExist)
	}
	_, statErr := os.Stat(paths[legacySensitiveDumpBatch])
	require.NoError(t, statErr)
}

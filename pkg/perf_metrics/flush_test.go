package perfmetrics

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func clearHotBuckets() {
	hotBuckets.Range(func(key, _ any) bool {
		hotBuckets.Delete(key)
		return true
	})
}

func TestFlushAllPersistsActiveBucketExactlyOnce(t *testing.T) {
	previousDB := model.DB
	db, err := gorm.Open(sqlite.Open("file:perf-metrics-flush?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.PerfMetric{}); err != nil {
		t.Fatalf("migrate perf metrics: %v", err)
	}
	model.DB = db
	clearHotBuckets()
	t.Cleanup(func() {
		clearHotBuckets()
		model.DB = previousDB
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	key := bucketKey{model: "test-model", group: "default", bucketTs: bucketStart(time.Now().Unix())}
	bucket := &atomicBucket{}
	bucket.add(Sample{
		Success:      true,
		LatencyMs:    120,
		TtftMs:       30,
		HasTtft:      true,
		OutputTokens: 12,
		GenerationMs: 60,
	})
	hotBuckets.Store(key, bucket)

	if err := FlushAll(); err != nil {
		t.Fatalf("FlushAll() error = %v", err)
	}
	if err := FlushAll(); err != nil {
		t.Fatalf("second FlushAll() error = %v", err)
	}

	var metric model.PerfMetric
	if err := db.Where("model_name = ? AND `group` = ? AND bucket_ts = ?", key.model, key.group, key.bucketTs).
		First(&metric).Error; err != nil {
		t.Fatalf("load metric: %v", err)
	}
	if metric.RequestCount != 1 || metric.SuccessCount != 1 || metric.OutputTokens != 12 {
		t.Fatalf("persisted metric = %+v, want one sample", metric)
	}
	if _, exists := hotBuckets.Load(key); exists {
		t.Fatal("active bucket still present after successful FlushAll")
	}
}

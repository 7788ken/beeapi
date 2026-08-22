package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
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

func TestDurableSensitiveAuditStopDrainsAProducerAcceptedBeforeAdmissionClosed(t *testing.T) {
	db := setupSensitiveAuditDurableTestDB(t)
	common.NodeName = "node-stop"

	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	pipeline := newDurableSensitiveAuditPipeline(1)
	pipeline.asyncEnabled = func() bool { return true }
	pipeline.dumpEnabled = func() bool { return true }
	pipeline.writeDump = func(job SensitiveAuditJob, _ []byte) (string, error) {
		close(writeStarted)
		<-releaseWrite
		return filepath.Join(t.TempDir(), job.JobID+".json.gz"), nil
	}
	pipeline.process = func(job SensitiveAuditJob) error {
		return model.CompleteSensitiveAuditJob(job.JobID, job.LeaseToken, 0, nil, nil, time.Now().Unix())
	}
	require.NoError(t, pipeline.Start(1))

	submitResult := make(chan error, 1)
	go func() {
		submitResult <- pipeline.Submit(SensitiveAuditJob{RequestID: "stop-race"}, []byte(`{"prompt":"test"}`))
	}()
	<-writeStarted

	stopResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		stopResult <- pipeline.Stop(ctx)
	}()
	require.Eventually(t, func() bool {
		pipeline.mu.Lock()
		defer pipeline.mu.Unlock()
		return pipeline.state == sensitiveLifecycleStopping
	}, time.Second, time.Millisecond)
	close(releaseWrite)

	require.NoError(t, <-submitResult)
	require.NoError(t, <-stopResult)
	var job model.SensitiveAuditJobRecord
	require.NoError(t, db.Where("request_id = ?", "stop-race").First(&job).Error)
	require.Equal(t, model.SensitiveAuditJobSucceeded, job.Status)
}

func setupSensitiveAuditDurableTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousUsingSQLite := common.UsingSQLite
	previousUsingMySQL := common.UsingMySQL
	previousUsingPostgreSQL := common.UsingPostgreSQL
	previousRedisEnabled := common.RedisEnabled
	previousNodeName := common.NodeName

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	dsn := filepath.Join(t.TempDir(), "sensitive-audit.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(
		&model.SensitiveAuditJobRecord{},
		&model.SensitiveWord{},
		&model.SensitiveBlockLog{},
		&model.Token{},
	))

	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.UsingSQLite = previousUsingSQLite
		common.UsingMySQL = previousUsingMySQL
		common.UsingPostgreSQL = previousUsingPostgreSQL
		common.RedisEnabled = previousRedisEnabled
		common.NodeName = previousNodeName
		_ = sqlDB.Close()
	})

	return db
}

func newSensitiveAuditDurableRecord(jobID string) *model.SensitiveAuditJobRecord {
	return &model.SensitiveAuditJobRecord{
		JobID:       jobID,
		StorageNode: "node-a",
		DumpPath:    "/tmp/" + jobID + ".json.gz",
		BodySHA256:  strings.Repeat("a", 64),
		BodySize:    4,
		CreatedAt:   100,
	}
}

func TestInsertSensitiveAuditJobIsIdempotentOnlyForTheSameImmutableIdentity(t *testing.T) {
	db := setupSensitiveAuditDurableTestDB(t)

	job := newSensitiveAuditDurableRecord("job-insert")
	require.NoError(t, model.InsertSensitiveAuditJob(job))

	replay := *job
	require.NoError(t, model.InsertSensitiveAuditJob(&replay))

	var count int64
	require.NoError(t, db.Model(&model.SensitiveAuditJobRecord{}).Count(&count).Error)
	require.Equal(t, int64(1), count)

	tests := []struct {
		name   string
		mutate func(*model.SensitiveAuditJobRecord)
	}{
		{
			name: "storage node",
			mutate: func(candidate *model.SensitiveAuditJobRecord) {
				candidate.StorageNode = "node-b"
			},
		},
		{
			name: "dump path",
			mutate: func(candidate *model.SensitiveAuditJobRecord) {
				candidate.DumpPath = "/tmp/different.json.gz"
			},
		},
		{
			name: "body hash",
			mutate: func(candidate *model.SensitiveAuditJobRecord) {
				candidate.BodySHA256 = strings.Repeat("b", 64)
			},
		},
		{
			name: "body size",
			mutate: func(candidate *model.SensitiveAuditJobRecord) {
				candidate.BodySize++
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := *job
			test.mutate(&candidate)
			require.ErrorContains(t, model.InsertSensitiveAuditJob(&candidate), "immutable identity mismatch")
		})
	}

	require.NoError(t, db.Model(&model.SensitiveAuditJobRecord{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestTryClaimSensitiveAuditJobAllowsOnlyOneConcurrentOwner(t *testing.T) {
	db := setupSensitiveAuditDurableTestDB(t)

	job := newSensitiveAuditDurableRecord("job-dual-claim")
	require.NoError(t, model.InsertSensitiveAuditJob(job))

	type claimResult struct {
		token   string
		claimed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	var callers sync.WaitGroup
	for index := 0; index < 2; index++ {
		token := fmt.Sprintf("lease-%d", index)
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			claimed, err := model.TryClaimSensitiveAuditJob(
				job.JobID,
				job.StorageNode,
				"worker-"+token,
				token,
				100,
				200,
			)
			results <- claimResult{token: token, claimed: claimed, err: err}
		}()
	}
	close(start)
	callers.Wait()
	close(results)

	claimedTokens := make([]string, 0, 1)
	for result := range results {
		require.NoError(t, result.err)
		if result.claimed {
			claimedTokens = append(claimedTokens, result.token)
		}
	}
	require.Len(t, claimedTokens, 1)

	var stored model.SensitiveAuditJobRecord
	require.NoError(t, db.First(&stored, "job_id = ?", job.JobID).Error)
	require.Equal(t, model.SensitiveAuditJobProcessing, stored.Status)
	require.Equal(t, claimedTokens[0], stored.LeaseToken)
	require.Equal(t, int64(1), stored.LeaseGeneration)
	require.Equal(t, 1, stored.Attempts)
}

func TestCompleteSensitiveAuditJobRejectsAnExpiredLeaseOwner(t *testing.T) {
	db := setupSensitiveAuditDurableTestDB(t)

	job := newSensitiveAuditDurableRecord("job-lease-fencing")
	require.NoError(t, model.InsertSensitiveAuditJob(job))

	claimed, err := model.TryClaimSensitiveAuditJob(job.JobID, job.StorageNode, "worker-old", "lease-old", 100, 110)
	require.NoError(t, err)
	require.True(t, claimed)

	claimed, err = model.TryClaimSensitiveAuditJob(job.JobID, job.StorageNode, "worker-new", "lease-new", 111, 200)
	require.NoError(t, err)
	require.True(t, claimed)

	require.Error(t, model.CompleteSensitiveAuditJob(job.JobID, "lease-old", 0, nil, nil, 120))

	var stored model.SensitiveAuditJobRecord
	require.NoError(t, db.First(&stored, "job_id = ?", job.JobID).Error)
	require.Equal(t, model.SensitiveAuditJobProcessing, stored.Status)
	require.Equal(t, "lease-new", stored.LeaseToken)
	require.Equal(t, int64(2), stored.LeaseGeneration)
	require.Equal(t, 2, stored.Attempts)

	require.NoError(t, model.CompleteSensitiveAuditJob(job.JobID, "lease-new", 0, nil, nil, 121))
	require.NoError(t, db.First(&stored, "job_id = ?", job.JobID).Error)
	require.Equal(t, model.SensitiveAuditJobSucceeded, stored.Status)
	require.Equal(t, int64(121), stored.CompletedAt)
}

func TestProcessSensitiveAuditJobCommitsEveryRuleSharingTheSamePatternOnce(t *testing.T) {
	db := setupSensitiveAuditDurableTestDB(t)
	t.Setenv(envSensitiveDumpRoot, t.TempDir())

	rules := []model.SensitiveWord{
		{Pattern: "secret", Enabled: true, Action: model.SensitiveActionBlock},
		{Pattern: "secret", Enabled: true, Action: model.SensitiveActionMonitor},
	}
	for index := range rules {
		require.NoError(t, db.Create(&rules[index]).Error)
	}
	token := model.Token{
		Id:     41,
		UserId: 7,
		Key:    "durable-sensitive-token",
		Name:   "durable-sensitive-token",
		Status: common.TokenStatusEnabled,
	}
	require.NoError(t, db.Create(&token).Error)

	body := []byte(`{"prompt":"SECRET appears twice: secret"}`)
	sum := sha256.Sum256(body)
	job := SensitiveAuditJob{
		JobID:       "job-same-pattern",
		StorageNode: "node-a",
		RequestID:   "request-same-pattern",
		UserID:      token.UserId,
		Username:    "sensitive-user",
		TokenID:     token.Id,
		TokenName:   token.Name,
		ChannelID:   9,
		ChannelName: "channel-a",
		ModelName:   "model-a",
		Path:        "/v1/chat/completions",
		IP:          "127.0.0.1",
		UserAgent:   "durable-test",
		BodySha256:  hex.EncodeToString(sum[:]),
		BodySize:    int64(len(body)),
		CreatedAt:   1_700_000_000,
	}
	dumpPath, err := writeSensitiveDump(job, body)
	require.NoError(t, err)
	job.DumpPath = dumpPath

	require.NoError(t, persistSensitiveAuditJob(job))
	claimed, err := model.TryClaimSensitiveAuditJob(job.JobID, job.StorageNode, "worker-a", "lease-a", job.CreatedAt, job.CreatedAt+60)
	require.NoError(t, err)
	require.True(t, claimed)
	job.LeaseToken = "lease-a"

	require.NoError(t, processSensitiveAuditJob(job))

	var storedJob model.SensitiveAuditJobRecord
	require.NoError(t, db.First(&storedJob, "job_id = ?", job.JobID).Error)
	require.Equal(t, model.SensitiveAuditJobSucceeded, storedJob.Status)

	var logs []model.SensitiveBlockLog
	require.NoError(t, db.Where("audit_job_id = ?", job.JobID).Order("matched_word_id asc").Find(&logs).Error)
	require.Len(t, logs, 2)
	require.Equal(t, []int{rules[0].Id, rules[1].Id}, []int{logs[0].MatchedWordId, logs[1].MatchedWordId})
	require.True(t, logs[0].TokenDisabled)
	require.False(t, logs[1].TokenDisabled)

	var storedRules []model.SensitiveWord
	require.NoError(t, db.Order("id asc").Find(&storedRules).Error)
	require.Len(t, storedRules, 2)
	require.Equal(t, int64(1), storedRules[0].HitCount)
	require.Equal(t, int64(1), storedRules[1].HitCount)

	var storedToken model.Token
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	require.Equal(t, common.TokenStatusDisabled, storedToken.Status)

	// A replay after an ambiguous completion result sees the durable terminal
	// and must not duplicate logs, counters, or token side effects.
	require.NoError(t, processSensitiveAuditJob(job))
	require.NoError(t, db.Where("audit_job_id = ?", job.JobID).Order("matched_word_id asc").Find(&logs).Error)
	require.Len(t, logs, 2)
	require.NoError(t, db.Order("id asc").Find(&storedRules).Error)
	require.Equal(t, int64(1), storedRules[0].HitCount)
	require.Equal(t, int64(1), storedRules[1].HitCount)
}

func TestCompleteSensitiveAuditJobRollsBackAllRuleEffectsAtomically(t *testing.T) {
	db := setupSensitiveAuditDurableTestDB(t)

	rules := []model.SensitiveWord{
		{Pattern: "secret", Enabled: true, Action: model.SensitiveActionBlock},
		{Pattern: "secret", Enabled: true, Action: model.SensitiveActionMonitor},
	}
	for index := range rules {
		require.NoError(t, db.Create(&rules[index]).Error)
	}
	token := model.Token{
		Id:     42,
		UserId: 8,
		Key:    "durable-sensitive-rollback-token",
		Name:   "durable-sensitive-rollback-token",
		Status: common.TokenStatusEnabled,
	}
	require.NoError(t, db.Create(&token).Error)

	job := newSensitiveAuditDurableRecord("job-atomic-rollback")
	job.TokenID = token.Id
	require.NoError(t, model.InsertSensitiveAuditJob(job))
	claimed, err := model.TryClaimSensitiveAuditJob(job.JobID, job.StorageNode, "worker-a", "lease-a", 100, 200)
	require.NoError(t, err)
	require.True(t, claimed)

	jobID := job.JobID
	hits := []*model.SensitiveBlockLog{
		{
			AuditJobId:     &jobID,
			MatchedWordId:  rules[0].Id,
			MatchedPattern: rules[0].Pattern,
			Action:         rules[0].Action,
			DumpExists:     true,
		},
		{
			AuditJobId:     &jobID,
			MatchedWordId:  rules[1].Id,
			MatchedPattern: rules[1].Pattern,
			Action:         rules[1].Action,
			DumpExists:     true,
		},
	}
	versions := []model.SensitiveAuditRuleVersion{
		{
			ID:        rules[0].Id,
			Pattern:   rules[0].Pattern,
			Action:    rules[0].Action,
			UpdatedAt: rules[0].UpdatedAt,
		},
		{
			ID:        rules[1].Id,
			Pattern:   rules[1].Pattern,
			Action:    rules[1].Action,
			UpdatedAt: rules[1].UpdatedAt + 1,
		},
	}

	require.ErrorContains(
		t,
		model.CompleteSensitiveAuditJob(job.JobID, "lease-a", token.Id, hits, versions, 150),
		"changed before completion",
	)

	var storedJob model.SensitiveAuditJobRecord
	require.NoError(t, db.First(&storedJob, "job_id = ?", job.JobID).Error)
	require.Equal(t, model.SensitiveAuditJobProcessing, storedJob.Status)

	var logCount int64
	require.NoError(t, db.Model(&model.SensitiveBlockLog{}).Where("audit_job_id = ?", job.JobID).Count(&logCount).Error)
	require.Zero(t, logCount)

	var storedRules []model.SensitiveWord
	require.NoError(t, db.Order("id asc").Find(&storedRules).Error)
	require.Equal(t, int64(0), storedRules[0].HitCount)
	require.Equal(t, int64(0), storedRules[1].HitCount)

	var storedToken model.Token
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	require.Equal(t, common.TokenStatusEnabled, storedToken.Status)
}

func TestCleanDurableSensitiveDumpsFinalizesAnAlreadyMissingFile(t *testing.T) {
	db := setupSensitiveAuditDurableTestDB(t)
	common.NodeName = "node-cleaner"

	job := newSensitiveAuditDurableRecord("job-missing-dump")
	job.StorageNode = common.NodeName
	job.Status = model.SensitiveAuditJobSucceeded
	job.CompletedAt = 110
	job.DumpPath = filepath.Join(t.TempDir(), "already-missing.json.gz")
	require.NoError(t, model.InsertSensitiveAuditJob(job))

	jobID := job.JobID
	logEntry := model.SensitiveBlockLog{
		AuditJobId:     &jobID,
		MatchedWordId:  1,
		MatchedPattern: "secret",
		Action:         model.SensitiveActionMonitor,
		DumpPath:       job.DumpPath,
		DumpExists:     true,
	}
	require.NoError(t, db.Create(&logEntry).Error)

	deleted, scanned, errs := cleanDurableSensitiveDumps(time.Unix(200, 0))
	require.Equal(t, 1, deleted)
	require.Equal(t, 1, scanned)
	require.Zero(t, errs)

	var storedJob model.SensitiveAuditJobRecord
	require.NoError(t, db.First(&storedJob, "job_id = ?", job.JobID).Error)
	require.Equal(t, model.SensitiveAuditDumpDeleted, storedJob.DumpState)
	require.NotZero(t, storedJob.DumpDeletedAt)
	require.Empty(t, storedJob.DumpLeaseOwner)
	require.Empty(t, storedJob.DumpLeaseToken)
	require.Zero(t, storedJob.DumpLeaseUntil)

	var storedLog model.SensitiveBlockLog
	require.NoError(t, db.First(&storedLog, logEntry.Id).Error)
	require.False(t, storedLog.DumpExists)

	deleted, scanned, errs = cleanDurableSensitiveDumps(time.Unix(200, 0))
	require.Zero(t, deleted)
	require.Zero(t, scanned)
	require.Zero(t, errs)
}

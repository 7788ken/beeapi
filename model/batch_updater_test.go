package model

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func startBatchUpdaterForTest(t *testing.T, applier BatchApplyFunc) *BatchUpdater {
	t.Helper()
	updater := NewBatchUpdater(3600, applier)
	if err := updater.Start(); err != nil {
		t.Fatalf("start batch updater: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := updater.StopAndFlush(ctx); err != nil {
			t.Errorf("stop batch updater: %v", err)
		}
	})
	return updater
}

func TestBatchUpdaterLifecycleAndFiveUpdateKinds(t *testing.T) {
	var (
		mu     sync.Mutex
		groups [][]BatchUpdate
	)
	updater := startBatchUpdaterForTest(t, func(_ context.Context, updates []BatchUpdate) error {
		mu.Lock()
		defer mu.Unlock()
		groups = append(groups, append([]BatchUpdate(nil), updates...))
		return nil
	})

	updates := []BatchUpdate{
		{Kind: BatchUpdateTypeUserQuota, ID: 1, Delta: 10},
		{Kind: BatchUpdateTypeUsedQuota, ID: 1, Delta: 20},
		{Kind: BatchUpdateTypeRequestCount, ID: 1, Delta: 1},
		{Kind: BatchUpdateTypeTokenQuota, ID: 2, Delta: -30},
		{Kind: BatchUpdateTypeChannelUsedQuota, ID: 3, Delta: 40},
	}
	if err := updater.AddMany(updates); err != nil {
		t.Fatalf("add five update kinds: %v", err)
	}
	if got := updater.Pending(); got != len(updates) {
		t.Fatalf("pending entries = %d, want %d", got, len(updates))
	}
	if err := updater.Flush(context.Background()); err != nil {
		t.Fatalf("flush five update kinds: %v", err)
	}
	if got := updater.Pending(); got != 0 {
		t.Fatalf("pending entries after flush = %d, want 0", got)
	}

	wantGroups := [][]BatchUpdate{
		{
			{Kind: BatchUpdateTypeUserQuota, ID: 1, Delta: 10},
			{Kind: BatchUpdateTypeUsedQuota, ID: 1, Delta: 20},
			{Kind: BatchUpdateTypeRequestCount, ID: 1, Delta: 1},
		},
		{{Kind: BatchUpdateTypeTokenQuota, ID: 2, Delta: -30}},
		{{Kind: BatchUpdateTypeChannelUsedQuota, ID: 3, Delta: 40}},
	}
	mu.Lock()
	gotGroups := append([][]BatchUpdate(nil), groups...)
	mu.Unlock()
	if !reflect.DeepEqual(gotGroups, wantGroups) {
		t.Fatalf("applied groups = %#v, want %#v", gotGroups, wantGroups)
	}

	if err := updater.StopAndFlush(context.Background()); err != nil {
		t.Fatalf("stop and flush: %v", err)
	}
	if err := updater.StopAndFlush(context.Background()); err != nil {
		t.Fatalf("repeat stop and flush: %v", err)
	}
	if err := updater.Add(BatchUpdateTypeUserQuota, 1, 1); !errors.Is(err, ErrBatchUpdaterNotAccepting) {
		t.Fatalf("add after stop error = %v, want ErrBatchUpdaterNotAccepting", err)
	}
	if err := updater.Start(); !errors.Is(err, ErrBatchUpdaterAlreadyStarted) {
		t.Fatalf("restart error = %v, want ErrBatchUpdaterAlreadyStarted", err)
	}
}

func TestBatchUpdaterRetriesFailedOperationBeforeNewSameKeyDelta(t *testing.T) {
	forcedError := errors.New("forced token update failure")
	var (
		updater      *BatchUpdater
		mu           sync.Mutex
		calls        [][]BatchUpdate
		operationIDs []string
		tokenAttempt int
		addErr       error
	)
	updater = startBatchUpdaterForTest(t, func(ctx context.Context, updates []BatchUpdate) error {
		copied := append([]BatchUpdate(nil), updates...)
		operationID, ok := flushOperationIDFromContext(ctx)
		if !ok {
			return errors.New("missing flush operation id")
		}
		mu.Lock()
		calls = append(calls, copied)
		operationIDs = append(operationIDs, operationID)
		isFirstTokenAttempt := updates[0].Kind == BatchUpdateTypeTokenQuota && tokenAttempt == 0
		if updates[0].Kind == BatchUpdateTypeTokenQuota {
			tokenAttempt++
		}
		mu.Unlock()

		if isFirstTokenAttempt {
			addErr = updater.Add(BatchUpdateTypeTokenQuota, updates[0].ID, 3)
			return forcedError
		}
		return nil
	})

	if err := updater.AddMany([]BatchUpdate{
		{Kind: BatchUpdateTypeUserQuota, ID: 1, Delta: 10},
		{Kind: BatchUpdateTypeTokenQuota, ID: 2, Delta: 20},
	}); err != nil {
		t.Fatalf("add initial updates: %v", err)
	}
	err := updater.Flush(context.Background())
	if !errors.Is(err, forcedError) {
		t.Fatalf("first flush error = %v, want forced error", err)
	}
	if addErr != nil {
		t.Fatalf("add during failed apply: %v", addErr)
	}
	if got := updater.Pending(); got != 2 {
		t.Fatalf("pending entries after partial failure = %d, want 2 separated generations", got)
	}
	if err := updater.Flush(context.Background()); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
	if got := updater.Pending(); got != 1 {
		t.Fatalf("pending entries after retry = %d, want new generation to remain pending", got)
	}
	if err := updater.Flush(context.Background()); err != nil {
		t.Fatalf("flush new generation: %v", err)
	}

	wantCalls := [][]BatchUpdate{
		{{Kind: BatchUpdateTypeUserQuota, ID: 1, Delta: 10}},
		{{Kind: BatchUpdateTypeTokenQuota, ID: 2, Delta: 20}},
		{{Kind: BatchUpdateTypeTokenQuota, ID: 2, Delta: 20}},
		{{Kind: BatchUpdateTypeTokenQuota, ID: 2, Delta: 3}},
	}
	mu.Lock()
	gotCalls := append([][]BatchUpdate(nil), calls...)
	gotOperationIDs := append([]string(nil), operationIDs...)
	mu.Unlock()
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("apply calls = %#v, want %#v", gotCalls, wantCalls)
	}
	if gotOperationIDs[1] != gotOperationIDs[2] {
		t.Fatalf("failed operation id changed across retry: %q != %q", gotOperationIDs[1], gotOperationIDs[2])
	}
	if gotOperationIDs[2] == gotOperationIDs[3] {
		t.Fatalf("new same-key generation reused operation id %q", gotOperationIDs[3])
	}
}

func TestBatchUpdaterFailedOperationDoesNotBlockUnrelatedNewGroup(t *testing.T) {
	forcedError := errors.New("forced poison group failure")
	var (
		mu            sync.Mutex
		calls         [][]BatchUpdate
		allowRecovery bool
	)
	updater := startBatchUpdaterForTest(t, func(_ context.Context, updates []BatchUpdate) error {
		mu.Lock()
		calls = append(calls, append([]BatchUpdate(nil), updates...))
		recovering := allowRecovery
		mu.Unlock()
		if updates[0].ID == 1 && !recovering {
			return forcedError
		}
		return nil
	})

	if err := updater.Add(BatchUpdateTypeTokenQuota, 1, 10); err != nil {
		t.Fatalf("add poison group: %v", err)
	}
	if err := updater.Flush(context.Background()); !errors.Is(err, forcedError) {
		t.Fatalf("first flush error = %v, want poison failure", err)
	}
	if err := updater.Add(BatchUpdateTypeTokenQuota, 2, 20); err != nil {
		t.Fatalf("add unrelated group: %v", err)
	}
	if err := updater.Flush(context.Background()); !errors.Is(err, forcedError) {
		t.Fatalf("second flush error = %v, want poison failure", err)
	}
	if got := updater.Pending(); got != 1 {
		t.Fatalf("pending after unrelated group succeeded = %d, want only poison group", got)
	}

	mu.Lock()
	gotCalls := append([][]BatchUpdate(nil), calls...)
	allowRecovery = true
	mu.Unlock()
	wantCalls := [][]BatchUpdate{
		{{Kind: BatchUpdateTypeTokenQuota, ID: 1, Delta: 10}},
		{{Kind: BatchUpdateTypeTokenQuota, ID: 1, Delta: 10}},
		{{Kind: BatchUpdateTypeTokenQuota, ID: 2, Delta: 20}},
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("apply calls = %#v, want poison retry plus unrelated group %#v", gotCalls, wantCalls)
	}
	if err := updater.Flush(context.Background()); err != nil {
		t.Fatalf("recover poison group: %v", err)
	}
}

func TestBatchUpdaterDoesNotConsumeSameKeyAddedDuringSuccessfulFlush(t *testing.T) {
	applyStarted := make(chan struct{})
	releaseApply := make(chan struct{})
	var (
		blockFirst sync.Once
		mu         sync.Mutex
		calls      [][]BatchUpdate
	)
	updater := startBatchUpdaterForTest(t, func(_ context.Context, updates []BatchUpdate) error {
		mu.Lock()
		calls = append(calls, append([]BatchUpdate(nil), updates...))
		mu.Unlock()
		blockFirst.Do(func() {
			close(applyStarted)
			<-releaseApply
		})
		return nil
	})

	if err := updater.Add(BatchUpdateTypeUserQuota, 1, 10); err != nil {
		t.Fatalf("add initial delta: %v", err)
	}
	flushDone := make(chan error, 1)
	go func() {
		flushDone <- updater.Flush(context.Background())
	}()

	select {
	case <-applyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not reach applier")
	}
	if err := updater.Add(BatchUpdateTypeUserQuota, 1, 3); err != nil {
		t.Fatalf("add same key during flush: %v", err)
	}
	close(releaseApply)
	select {
	case err := <-flushDone:
		if err != nil {
			t.Fatalf("first flush: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first flush did not finish")
	}
	if got := updater.Pending(); got != 1 {
		t.Fatalf("pending entries after first flush = %d, want 1", got)
	}
	if err := updater.Flush(context.Background()); err != nil {
		t.Fatalf("second flush: %v", err)
	}

	wantCalls := [][]BatchUpdate{
		{{Kind: BatchUpdateTypeUserQuota, ID: 1, Delta: 10}},
		{{Kind: BatchUpdateTypeUserQuota, ID: 1, Delta: 3}},
	}
	mu.Lock()
	gotCalls := append([][]BatchUpdate(nil), calls...)
	mu.Unlock()
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("apply calls = %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestBatchUpdaterStopTimeoutCanBeRetried(t *testing.T) {
	applyStarted := make(chan struct{})
	releaseApply := make(chan struct{})
	var blockFirst sync.Once
	updater := startBatchUpdaterForTest(t, func(_ context.Context, _ []BatchUpdate) error {
		blockFirst.Do(func() {
			close(applyStarted)
			<-releaseApply
		})
		return nil
	})

	if err := updater.Add(BatchUpdateTypeTokenQuota, 1, 5); err != nil {
		t.Fatalf("add token delta: %v", err)
	}
	flushDone := make(chan error, 1)
	go func() {
		flushDone <- updater.Flush(context.Background())
	}()
	select {
	case <-applyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not reach applier")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := updater.StopAndFlush(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop timeout error = %v, want context deadline exceeded", err)
	}
	if err := updater.Add(BatchUpdateTypeTokenQuota, 1, 1); !errors.Is(err, ErrBatchUpdaterNotAccepting) {
		t.Fatalf("add after stop boundary error = %v, want ErrBatchUpdaterNotAccepting", err)
	}

	close(releaseApply)
	select {
	case err := <-flushDone:
		if err != nil {
			t.Fatalf("blocked flush: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked flush did not finish")
	}
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer retryCancel()
	if err := updater.StopAndFlush(retryCtx); err != nil {
		t.Fatalf("retry stop and flush: %v", err)
	}
}

func TestBatchUpdaterConcurrentStopAndFailedFinalFlushRetry(t *testing.T) {
	forcedError := errors.New("forced final flush failure")
	var (
		mu       sync.Mutex
		attempts int
	)
	updater := startBatchUpdaterForTest(t, func(_ context.Context, _ []BatchUpdate) error {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts == 1 {
			return forcedError
		}
		return nil
	})
	if err := updater.Add(BatchUpdateTypeUserQuota, 1, 5); err != nil {
		t.Fatalf("add final flush delta: %v", err)
	}
	if err := updater.StopAndFlush(context.Background()); !errors.Is(err, forcedError) {
		t.Fatalf("first stop error = %v, want forced error", err)
	}
	if got := updater.Pending(); got != 1 {
		t.Fatalf("pending entries after failed final flush = %d, want 1", got)
	}

	const callers = 8
	results := make(chan error, callers)
	for range callers {
		go func() {
			results <- updater.StopAndFlush(context.Background())
		}()
	}
	for range callers {
		if err := <-results; err != nil {
			t.Fatalf("concurrent stop and flush: %v", err)
		}
	}
	if got := updater.Pending(); got != 0 {
		t.Fatalf("pending entries after concurrent retry = %d, want 0", got)
	}
	mu.Lock()
	gotAttempts := attempts
	mu.Unlock()
	if gotAttempts != 2 {
		t.Fatalf("apply attempts = %d, want 2", gotAttempts)
	}
}

func TestBatchUpdaterValidatesAdmissionAtomically(t *testing.T) {
	updater := startBatchUpdaterForTest(t, func(_ context.Context, _ []BatchUpdate) error {
		return nil
	})

	if err := updater.AddMany([]BatchUpdate{
		{Kind: BatchUpdateTypeUserQuota, ID: 1, Delta: 5},
		{Kind: BatchUpdateTypeCount, ID: 2, Delta: 1},
	}); err == nil {
		t.Fatal("AddMany accepted an invalid kind")
	}
	if got := updater.Pending(); got != 0 {
		t.Fatalf("partial AddMany admission left %d pending entries, want 0", got)
	}
	if err := updater.Add(BatchUpdateTypeUserQuota, 0, 1); err == nil {
		t.Fatal("Add accepted a non-positive id")
	}

	maxInt := int(^uint(0) >> 1)
	if err := updater.Add(BatchUpdateTypeUserQuota, 1, maxInt); err != nil {
		t.Fatalf("add max int delta: %v", err)
	}
	if err := updater.Add(BatchUpdateTypeUserQuota, 1, 1); err == nil {
		t.Fatal("Add accepted an overflowing delta")
	}
	if err := updater.Add(BatchUpdateTypeUserQuota, 1, -maxInt); err != nil {
		t.Fatalf("cancel max int delta: %v", err)
	}
	if got := updater.Pending(); got != 0 {
		t.Fatalf("pending entries after net zero = %d, want 0", got)
	}
}

func TestUsageStatisticsAdmissionIsAtomic(t *testing.T) {
	var (
		mu     sync.Mutex
		groups [][]BatchUpdate
	)
	updater := startBatchUpdaterForTest(t, func(_ context.Context, updates []BatchUpdate) error {
		mu.Lock()
		defer mu.Unlock()
		groups = append(groups, append([]BatchUpdate(nil), updates...))
		return nil
	})

	defaultBatchUpdaterMu.Lock()
	oldDefaultUpdater := defaultBatchUpdater
	defaultBatchUpdater = updater
	defaultBatchUpdaterMu.Unlock()
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
		defaultBatchUpdaterMu.Lock()
		defaultBatchUpdater = oldDefaultUpdater
		defaultBatchUpdaterMu.Unlock()
	})

	if err := UpdateUserAndChannelUsedQuota(1, 0, 7); err == nil {
		t.Fatal("usage admission accepted an invalid channel id")
	}
	if got := updater.Pending(); got != 0 {
		t.Fatalf("failed usage admission left %d pending entries, want 0", got)
	}
	if err := UpdateUserAndChannelUsedQuota(1, 2, 7); err != nil {
		t.Fatalf("admit usage statistics: %v", err)
	}
	if got := updater.Pending(); got != 3 {
		t.Fatalf("usage admission pending entries = %d, want 3", got)
	}
	if err := updater.Flush(context.Background()); err != nil {
		t.Fatalf("flush usage statistics: %v", err)
	}

	wantGroups := [][]BatchUpdate{
		{
			{Kind: BatchUpdateTypeUsedQuota, ID: 1, Delta: 7},
			{Kind: BatchUpdateTypeRequestCount, ID: 1, Delta: 1},
		},
		{{Kind: BatchUpdateTypeChannelUsedQuota, ID: 2, Delta: 7}},
	}
	mu.Lock()
	gotGroups := append([][]BatchUpdate(nil), groups...)
	mu.Unlock()
	if !reflect.DeepEqual(gotGroups, wantGroups) {
		t.Fatalf("usage groups = %#v, want %#v", gotGroups, wantGroups)
	}
}

func TestBatchUpdaterRejectsInvalidConfiguration(t *testing.T) {
	if err := NewBatchUpdater(0, func(context.Context, []BatchUpdate) error { return nil }).Start(); err == nil {
		t.Fatal("Start accepted a zero interval")
	}
	if err := NewBatchUpdater(1, nil).Start(); err == nil {
		t.Fatal("Start accepted a nil applier")
	}
}

func TestApplyDefaultBatchUpdatesPersistsAllFiveKinds(t *testing.T) {
	truncateTables(t)
	user := User{
		Username:     "batch-updater-user",
		Password:     "password123",
		Quota:        100,
		UsedQuota:    10,
		RequestCount: 2,
	}
	token := Token{
		UserId:      1,
		Key:         "batch-updater-token",
		Name:        "batch updater token",
		RemainQuota: 200,
		UsedQuota:   50,
	}
	channel := Channel{
		Key:       "batch-updater-channel",
		Name:      "batch updater channel",
		UsedQuota: 70,
	}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token.UserId = user.Id
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := DB.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	updater := startBatchUpdaterForTest(t, applyDefaultBatchUpdates)
	if err := updater.AddMany([]BatchUpdate{
		{Kind: BatchUpdateTypeUserQuota, ID: user.Id, Delta: 11},
		{Kind: BatchUpdateTypeUsedQuota, ID: user.Id, Delta: 12},
		{Kind: BatchUpdateTypeRequestCount, ID: user.Id, Delta: 1},
		{Kind: BatchUpdateTypeTokenQuota, ID: token.Id, Delta: -13},
		{Kind: BatchUpdateTypeChannelUsedQuota, ID: channel.Id, Delta: 14},
	}); err != nil {
		t.Fatalf("add default batch updates: %v", err)
	}
	if err := updater.Flush(context.Background()); err != nil {
		t.Fatalf("flush default batch updates: %v", err)
	}

	var gotUser User
	if err := DB.First(&gotUser, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if gotUser.Quota != 111 || gotUser.UsedQuota != 22 || gotUser.RequestCount != 3 {
		t.Fatalf("user quota fields = quota:%d used:%d requests:%d", gotUser.Quota, gotUser.UsedQuota, gotUser.RequestCount)
	}
	var gotToken Token
	if err := DB.First(&gotToken, token.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if gotToken.RemainQuota != 187 || gotToken.UsedQuota != 63 {
		t.Fatalf("token quota fields = remain:%d used:%d", gotToken.RemainQuota, gotToken.UsedQuota)
	}
	var gotChannel Channel
	if err := DB.First(&gotChannel, channel.Id).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if gotChannel.UsedQuota != 84 {
		t.Fatalf("channel used quota = %d, want 84", gotChannel.UsedQuota)
	}

	err := applyDefaultBatchUpdates(context.Background(), []BatchUpdate{{
		Kind:  BatchUpdateTypeUserQuota,
		ID:    user.Id + 1000000,
		Delta: 1,
	}})
	if err == nil || !strings.Contains(err.Error(), "affected 0 rows, want 1") {
		t.Fatalf("missing-row error = %v, want RowsAffected validation", err)
	}
	if err := requireOneBatchUpdateRow("user", user.Id, &gorm.DB{RowsAffected: 2}); err == nil {
		t.Fatal("RowsAffected > 1 was accepted")
	}
}

func TestBatchUpdaterReplaysAmbiguousCommittedOperationWithoutDoubleApplying(t *testing.T) {
	truncateTables(t)
	user := User{
		Username: "ambiguous-batch-user",
		Password: "password123",
		Quota:    100,
	}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := Token{
		UserId:      user.Id,
		Key:         "ambiguous-batch-token",
		Name:        "ambiguous batch token",
		RemainQuota: 200,
		UsedQuota:   50,
	}
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	forcedError := errors.New("forced ambiguous result after commit")
	var (
		mu           sync.Mutex
		operationIDs []string
		attempts     int
	)
	updater := startBatchUpdaterForTest(t, func(ctx context.Context, updates []BatchUpdate) error {
		operationID, ok := flushOperationIDFromContext(ctx)
		if !ok {
			return errors.New("missing flush operation id")
		}
		mu.Lock()
		operationIDs = append(operationIDs, operationID)
		attempts++
		attempt := attempts
		mu.Unlock()

		if err := applyDefaultBatchUpdates(ctx, updates); err != nil {
			return err
		}
		if attempt == 1 {
			return forcedError
		}
		return nil
	})
	if err := updater.Add(BatchUpdateTypeTokenQuota, token.Id, -13); err != nil {
		t.Fatalf("queue token delta: %v", err)
	}
	if err := updater.Flush(context.Background()); !errors.Is(err, forcedError) {
		t.Fatalf("first flush error = %v, want ambiguous result", err)
	}

	var afterAmbiguous Token
	if err := DB.First(&afterAmbiguous, token.Id).Error; err != nil {
		t.Fatalf("reload token after ambiguous result: %v", err)
	}
	if afterAmbiguous.RemainQuota != 187 || afterAmbiguous.UsedQuota != 63 {
		t.Fatalf(
			"token after ambiguous result = remain:%d used:%d, want remain:187 used:63",
			afterAmbiguous.RemainQuota,
			afterAmbiguous.UsedQuota,
		)
	}

	if err := updater.Flush(context.Background()); err != nil {
		t.Fatalf("retry ambiguous operation: %v", err)
	}
	var afterRetry Token
	if err := DB.First(&afterRetry, token.Id).Error; err != nil {
		t.Fatalf("reload token after replay: %v", err)
	}
	if afterRetry.RemainQuota != 187 || afterRetry.UsedQuota != 63 {
		t.Fatalf(
			"token after replay = remain:%d used:%d, want unchanged remain:187 used:63",
			afterRetry.RemainQuota,
			afterRetry.UsedQuota,
		)
	}

	mu.Lock()
	gotOperationIDs := append([]string(nil), operationIDs...)
	mu.Unlock()
	if len(gotOperationIDs) != 2 || gotOperationIDs[0] != gotOperationIDs[1] {
		t.Fatalf("operation ids across ambiguous replay = %#v, want the same id twice", gotOperationIDs)
	}
	var ledgerCount int64
	if err := DB.Model(&FlushOperationLedger{}).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("count flush operation ledgers: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("flush operation ledgers after confirmed replay = %d, want 0", ledgerCount)
	}
}

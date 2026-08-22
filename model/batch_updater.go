package model

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billinglifecycle"
	"gorm.io/gorm"
)

const (
	BatchUpdateTypeUserQuota = iota
	BatchUpdateTypeTokenQuota
	BatchUpdateTypeUsedQuota
	BatchUpdateTypeChannelUsedQuota
	BatchUpdateTypeRequestCount
	BatchUpdateTypeCount
)

var (
	ErrBatchUpdaterAlreadyStarted = errors.New("batch updater already started")
	ErrBatchUpdaterNotRunning     = errors.New("batch updater is not running")
	ErrBatchUpdaterNotAccepting   = errors.New("batch updater is not accepting updates")
)

type batchUpdaterState uint8

const (
	batchUpdaterStateNew batchUpdaterState = iota
	batchUpdaterStateRunning
	batchUpdaterStateStopping
	batchUpdaterStateStopped
)

type BatchUpdate struct {
	Kind  int
	ID    int
	Delta int
}

type flushOperationIDContextKey struct{}

func contextWithFlushOperationID(ctx context.Context, operationID string) context.Context {
	return context.WithValue(ctx, flushOperationIDContextKey{}, operationID)
}

func flushOperationIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	operationID, ok := ctx.Value(flushOperationIDContextKey{}).(string)
	return operationID, ok && operationID != ""
}

// BatchApplyFunc must apply every update in a group atomically or apply none of
// them. Returning an error after a partial commit would make a retry duplicate
// the committed subset.
type BatchApplyFunc func(ctx context.Context, updates []BatchUpdate) error

type BatchUpdater struct {
	intervalSeconds int
	applier         BatchApplyFunc

	mu           sync.Mutex
	state        batchUpdaterState
	stores       []map[int]int
	inFlight     []map[int]int
	operationIDs map[string]string

	flushGate  chan struct{}
	stopCh     chan struct{}
	workerDone chan struct{}
	runCtx     context.Context
	cancelRun  context.CancelFunc
}

func NewBatchUpdater(intervalSeconds int, applier BatchApplyFunc) *BatchUpdater {
	return &BatchUpdater{
		intervalSeconds: intervalSeconds,
		applier:         applier,
		state:           batchUpdaterStateNew,
		stores:          newBatchUpdateStores(),
		inFlight:        newBatchUpdateStores(),
		operationIDs:    make(map[string]string),
		flushGate:       make(chan struct{}, 1),
	}
}

func newBatchUpdateStores() []map[int]int {
	stores := make([]map[int]int, BatchUpdateTypeCount)
	for kind := range stores {
		stores[kind] = make(map[int]int)
	}
	return stores
}

func batchUpdateInterval(seconds int) (time.Duration, error) {
	if seconds <= 0 {
		return 0, fmt.Errorf("batch update interval must be positive, got %d", seconds)
	}
	maxSeconds := int64((1<<63 - 1) / int64(time.Second))
	if int64(seconds) > maxSeconds {
		return 0, fmt.Errorf("batch update interval %d seconds overflows time.Duration", seconds)
	}
	return time.Duration(seconds) * time.Second, nil
}

func (u *BatchUpdater) Start() error {
	if u == nil {
		return errors.New("batch updater is nil")
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	if u.state != batchUpdaterStateNew {
		return fmt.Errorf("%w: state=%s", ErrBatchUpdaterAlreadyStarted, u.state)
	}
	interval, err := batchUpdateInterval(u.intervalSeconds)
	if err != nil {
		return err
	}
	if u.applier == nil {
		return errors.New("batch updater applier is nil")
	}

	u.stopCh = make(chan struct{})
	u.workerDone = make(chan struct{})
	u.runCtx, u.cancelRun = context.WithCancel(context.Background())
	u.state = batchUpdaterStateRunning
	go u.run(interval)
	return nil
}

func (u *BatchUpdater) run(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer func() {
		ticker.Stop()
		close(u.workerDone)
	}()

	for {
		select {
		case <-ticker.C:
			if err := u.Flush(u.runCtx); err != nil && !errors.Is(err, context.Canceled) {
				common.SysError("periodic batch update failed: " + err.Error())
			}
		case <-u.stopCh:
			return
		}
	}
}

func (u *BatchUpdater) Add(kind int, id int, delta int) error {
	return u.AddMany([]BatchUpdate{{Kind: kind, ID: id, Delta: delta}})
}

func (u *BatchUpdater) AddMany(updates []BatchUpdate) error {
	if u == nil {
		return errors.New("batch updater is nil")
	}
	if len(updates) == 0 {
		return nil
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	if u.state != batchUpdaterStateRunning {
		return fmt.Errorf("%w: state=%s", ErrBatchUpdaterNotAccepting, u.state)
	}

	type updateKey struct {
		kind int
		id   int
	}
	prospective := make(map[updateKey]int, len(updates))
	for _, update := range updates {
		if update.Kind < 0 || update.Kind >= BatchUpdateTypeCount {
			return fmt.Errorf("invalid batch update kind %d", update.Kind)
		}
		if update.ID <= 0 {
			return fmt.Errorf("invalid batch update id %d", update.ID)
		}

		key := updateKey{kind: update.Kind, id: update.ID}
		live, ok := prospective[key]
		if !ok {
			live = u.stores[update.Kind][update.ID]
		}
		nextLive, ok := checkedAddInt(live, update.Delta)
		if !ok {
			return fmt.Errorf("batch update overflow for kind=%d id=%d", update.Kind, update.ID)
		}
		if _, ok := checkedAddInt(u.inFlight[update.Kind][update.ID], nextLive); !ok {
			return fmt.Errorf("batch update overflow with in-flight delta for kind=%d id=%d", update.Kind, update.ID)
		}
		prospective[key] = nextLive
	}

	for key, value := range prospective {
		if value == 0 {
			delete(u.stores[key.kind], key.id)
		} else {
			u.stores[key.kind][key.id] = value
		}
	}
	return nil
}

func checkedAddInt(left int, right int) (int, bool) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	if right > 0 && left > maxInt-right {
		return 0, false
	}
	if right < 0 && left < minInt-right {
		return 0, false
	}
	return left + right, true
}

func (u *BatchUpdater) Flush(ctx context.Context) error {
	if u == nil {
		return errors.New("batch updater is nil")
	}
	if ctx == nil {
		return errors.New("batch flush context is nil")
	}

	if err := u.acquireFlush(ctx); err != nil {
		return err
	}
	defer u.releaseFlush()

	u.mu.Lock()
	switch u.state {
	case batchUpdaterStateNew:
		u.mu.Unlock()
		return ErrBatchUpdaterNotRunning
	case batchUpdaterStateStopped:
		u.mu.Unlock()
		return nil
	}
	snapshot, err := u.prepareInFlightLocked()
	if err != nil {
		u.mu.Unlock()
		return err
	}
	if batchStoresEmpty(snapshot) {
		u.mu.Unlock()
		return nil
	}

	groups := buildBatchUpdateGroups(snapshot)
	operations := make([]batchUpdateOperation, 0, len(groups))
	for _, group := range groups {
		key, err := batchUpdateGroupKey(group)
		if err != nil {
			u.mu.Unlock()
			return err
		}
		operationID := u.operationIDs[key]
		if operationID == "" {
			u.mu.Unlock()
			return fmt.Errorf("batch updater operation identity missing for group=%s", key)
		}
		operations = append(operations, batchUpdateOperation{
			key:         key,
			operationID: operationID,
			updates:     group,
		})
	}
	u.mu.Unlock()

	failedGroups := 0
	var firstError error
	confirmedOperationIDs := make([]string, 0, len(operations))
	for _, operation := range operations {
		err := ctx.Err()
		if err == nil {
			operationCtx := contextWithFlushOperationID(ctx, operation.operationID)
			err = u.applier(operationCtx, operation.updates)
		}
		finalizeErr := u.finalizeGroup(operation, err != nil)
		if err != nil || finalizeErr != nil {
			failedGroups++
			if firstError == nil {
				if err != nil {
					firstError = err
				} else {
					firstError = finalizeErr
				}
			}
			continue
		}
		confirmedOperationIDs = append(confirmedOperationIDs, operation.operationID)
	}
	if cleanupErr := deleteFlushOperationLedgers(ctx, confirmedOperationIDs); cleanupErr != nil {
		common.SysError(cleanupErr.Error())
	}
	if failedGroups > 0 {
		return fmt.Errorf("batch update failed for %d of %d groups; first error: %w", failedGroups, len(operations), firstError)
	}
	return nil
}

func (u *BatchUpdater) acquireFlush(ctx context.Context) error {
	select {
	case u.flushGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (u *BatchUpdater) releaseFlush() {
	<-u.flushGate
}

func (u *BatchUpdater) prepareInFlightLocked() ([]map[int]int, error) {
	for _, group := range buildBatchUpdateGroups(u.stores) {
		key, err := batchUpdateGroupKey(group)
		if err != nil {
			return nil, err
		}
		if u.operationIDs[key] != "" {
			continue
		}
		for _, update := range group {
			u.inFlight[update.Kind][update.ID] = update.Delta
			delete(u.stores[update.Kind], update.ID)
		}
		u.operationIDs[key] = newFlushOperationID("batch")
	}
	return u.inFlight, nil
}

func batchStoresEmpty(stores []map[int]int) bool {
	for _, store := range stores {
		if len(store) > 0 {
			return false
		}
	}
	return true
}

func buildBatchUpdateGroups(snapshot []map[int]int) [][]BatchUpdate {
	userIDs := make(map[int]struct{})
	for _, kind := range []int{BatchUpdateTypeUserQuota, BatchUpdateTypeUsedQuota, BatchUpdateTypeRequestCount} {
		for id := range snapshot[kind] {
			userIDs[id] = struct{}{}
		}
	}

	groups := make([][]BatchUpdate, 0)
	for _, id := range sortedBatchIDs(userIDs) {
		group := make([]BatchUpdate, 0, 3)
		for _, kind := range []int{BatchUpdateTypeUserQuota, BatchUpdateTypeUsedQuota, BatchUpdateTypeRequestCount} {
			if delta, ok := snapshot[kind][id]; ok {
				group = append(group, BatchUpdate{Kind: kind, ID: id, Delta: delta})
			}
		}
		groups = append(groups, group)
	}
	for _, kind := range []int{BatchUpdateTypeTokenQuota, BatchUpdateTypeChannelUsedQuota} {
		ids := make(map[int]struct{}, len(snapshot[kind]))
		for id := range snapshot[kind] {
			ids[id] = struct{}{}
		}
		for _, id := range sortedBatchIDs(ids) {
			groups = append(groups, []BatchUpdate{{Kind: kind, ID: id, Delta: snapshot[kind][id]}})
		}
	}
	return groups
}

type batchUpdateOperation struct {
	key         string
	operationID string
	updates     []BatchUpdate
}

func batchUpdateGroupKey(group []BatchUpdate) (string, error) {
	if len(group) == 0 {
		return "", errors.New("batch update group is empty")
	}
	id := group[0].ID
	for _, update := range group {
		if update.ID != id {
			return "", errors.New("batch update group contains multiple ids")
		}
	}
	switch group[0].Kind {
	case BatchUpdateTypeUserQuota, BatchUpdateTypeUsedQuota, BatchUpdateTypeRequestCount:
		for _, update := range group {
			switch update.Kind {
			case BatchUpdateTypeUserQuota, BatchUpdateTypeUsedQuota, BatchUpdateTypeRequestCount:
			default:
				return "", fmt.Errorf("invalid user batch update kind %d", update.Kind)
			}
		}
		return fmt.Sprintf("user:%d", id), nil
	case BatchUpdateTypeTokenQuota:
		if len(group) != 1 {
			return "", errors.New("token batch update group must contain one delta")
		}
		return fmt.Sprintf("token:%d", id), nil
	case BatchUpdateTypeChannelUsedQuota:
		if len(group) != 1 {
			return "", errors.New("channel batch update group must contain one delta")
		}
		return fmt.Sprintf("channel:%d", id), nil
	default:
		return "", fmt.Errorf("unsupported batch update kind %d", group[0].Kind)
	}
}

func sortedBatchIDs(ids map[int]struct{}) []int {
	sorted := make([]int, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Ints(sorted)
	return sorted
}

func (u *BatchUpdater) finalizeGroup(operation batchUpdateOperation, failed bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.operationIDs[operation.key] != operation.operationID {
		return fmt.Errorf("batch updater operation identity mismatch for group=%s", operation.key)
	}
	for _, update := range operation.updates {
		inFlight, ok := u.inFlight[update.Kind][update.ID]
		if !ok || inFlight != update.Delta {
			return fmt.Errorf("batch updater in-flight state mismatch for kind=%d id=%d", update.Kind, update.ID)
		}
	}
	if failed {
		return nil
	}
	for _, update := range operation.updates {
		delete(u.inFlight[update.Kind], update.ID)
	}
	delete(u.operationIDs, operation.key)
	return nil
}

func (u *BatchUpdater) Pending() int {
	if u == nil {
		return 0
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return batchPendingLocked(u.stores) + batchPendingLocked(u.inFlight)
}

func batchPendingLocked(stores []map[int]int) int {
	pending := 0
	for _, store := range stores {
		pending += len(store)
	}
	return pending
}

func (u *BatchUpdater) StopAndFlush(ctx context.Context) error {
	if u == nil {
		return errors.New("batch updater is nil")
	}
	if ctx == nil {
		return errors.New("batch stop context is nil")
	}

	u.mu.Lock()
	switch u.state {
	case batchUpdaterStateNew:
		u.mu.Unlock()
		return ErrBatchUpdaterNotRunning
	case batchUpdaterStateRunning:
		u.state = batchUpdaterStateStopping
		close(u.stopCh)
		u.cancelRun()
	case batchUpdaterStateStopped:
		u.mu.Unlock()
		return nil
	}
	workerDone := u.workerDone
	u.mu.Unlock()

	select {
	case <-workerDone:
	case <-ctx.Done():
		return ctx.Err()
	}

	for {
		if err := u.Flush(ctx); err != nil {
			return err
		}
		u.mu.Lock()
		if batchPendingLocked(u.stores)+batchPendingLocked(u.inFlight) == 0 {
			u.state = batchUpdaterStateStopped
			u.mu.Unlock()
			return nil
		}
		u.mu.Unlock()
	}
}

func (state batchUpdaterState) String() string {
	switch state {
	case batchUpdaterStateNew:
		return "new"
	case batchUpdaterStateRunning:
		return "running"
	case batchUpdaterStateStopping:
		return "stopping"
	case batchUpdaterStateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

var (
	defaultBatchUpdaterMu sync.RWMutex
	defaultBatchUpdater   *BatchUpdater
)

func InitBatchUpdater() error {
	defaultBatchUpdaterMu.Lock()
	defer defaultBatchUpdaterMu.Unlock()
	if defaultBatchUpdater != nil {
		return ErrBatchUpdaterAlreadyStarted
	}
	if err := CheckFlushOperationSchemaReady(); err != nil {
		return err
	}
	updater := NewBatchUpdater(common.BatchUpdateInterval, applyDefaultBatchUpdates)
	if err := updater.Start(); err != nil {
		return err
	}
	defaultBatchUpdater = updater
	return nil
}

func StopBatchUpdater(ctx context.Context) error {
	defaultBatchUpdaterMu.RLock()
	updater := defaultBatchUpdater
	defaultBatchUpdaterMu.RUnlock()
	if updater == nil {
		return nil
	}
	return updater.StopAndFlush(ctx)
}

func addNewRecord(kind int, id int, value int) error {
	return addNewRecords([]BatchUpdate{{Kind: kind, ID: id, Delta: value}})
}

func addNewRecords(updates []BatchUpdate) error {
	defaultBatchUpdaterMu.RLock()
	updater := defaultBatchUpdater
	defaultBatchUpdaterMu.RUnlock()
	if updater == nil {
		return errors.New("batch updater is not initialized")
	}
	return updater.AddMany(updates)
}

func recordBatchAdmissionError(operation string, err error) error {
	wrapped := fmt.Errorf("%s rejected by batch updater: %w", operation, err)
	common.SysError(wrapped.Error())
	return wrapped
}

func applyDefaultBatchUpdates(ctx context.Context, updates []BatchUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	id := updates[0].ID
	hasUsedQuotaUpdate := false
	for _, update := range updates {
		if update.ID != id {
			return errors.New("default batch update group contains multiple ids")
		}
		if update.Kind == BatchUpdateTypeUsedQuota {
			hasUsedQuotaUpdate = true
		}
	}

	operationID, ok := flushOperationIDFromContext(ctx)
	if !ok {
		operationID = newFlushOperationID("batch")
	}
	scope, err := batchUpdateOperationScope(updates)
	if err != nil {
		return err
	}
	payloadHash, err := flushOperationPayloadHash(updates)
	if err != nil {
		return err
	}

	var rewardTicket *billinglifecycle.Ticket
	if hasUsedQuotaUpdate {
		var err error
		rewardTicket, err = reserveInviterRewardAfterUsedQuota(ctx)
		if err != nil {
			return fmt.Errorf("reserve inviter reward before batch update for user %d: %w", id, err)
		}
	}
	rewardSubmitted := false
	defer func() {
		if rewardTicket != nil && !rewardSubmitted {
			if releaseErr := rewardTicket.Release(); releaseErr != nil {
				common.SysError(fmt.Sprintf("failed to release inviter reward ticket for batch user %d: %v", id, releaseErr))
			}
		}
	}()

	resultCode, err := applyFlushOperation(ctx, operationID, scope, payloadHash, func(tx *gorm.DB) (string, error) {
		switch updates[0].Kind {
		case BatchUpdateTypeUserQuota, BatchUpdateTypeUsedQuota, BatchUpdateTypeRequestCount:
			values := make(map[string]interface{}, len(updates))
			for _, update := range updates {
				switch update.Kind {
				case BatchUpdateTypeUserQuota:
					values["quota"] = gorm.Expr("quota + ?", update.Delta)
				case BatchUpdateTypeUsedQuota:
					values["used_quota"] = gorm.Expr("used_quota + ?", update.Delta)
				case BatchUpdateTypeRequestCount:
					values["request_count"] = gorm.Expr("request_count + ?", update.Delta)
				default:
					return "", fmt.Errorf("invalid user batch update kind %d", update.Kind)
				}
			}
			result := tx.Model(&User{}).Where("id = ?", id).Updates(values)
			if result.Error != nil {
				return "", fmt.Errorf("update user id %d: %w", id, result.Error)
			}
			switch result.RowsAffected {
			case 0:
				if err := discardDeletedBatchUpdates(tx, "user", id, updates); err != nil {
					return "", err
				}
				return flushOperationResultBatchDiscarded, nil
			case 1:
				return flushOperationResultBatchUpdated, nil
			default:
				return "", fmt.Errorf("update user id %d affected %d rows, want 1", id, result.RowsAffected)
			}

		case BatchUpdateTypeTokenQuota:
			if len(updates) != 1 {
				return "", errors.New("token batch update group must contain one delta")
			}
			delta := updates[0].Delta
			result := tx.Model(&Token{}).Where("id = ?", id).Updates(map[string]interface{}{
				"remain_quota":  gorm.Expr("remain_quota + ?", delta),
				"used_quota":    gorm.Expr("used_quota - ?", delta),
				"accessed_time": common.GetTimestamp(),
			})
			if result.Error != nil {
				return "", fmt.Errorf("update token id %d: %w", id, result.Error)
			}
			switch result.RowsAffected {
			case 0:
				if err := discardDeletedBatchUpdates(tx, "token", id, updates); err != nil {
					return "", err
				}
				return flushOperationResultBatchDiscarded, nil
			case 1:
				return flushOperationResultBatchUpdated, nil
			default:
				return "", fmt.Errorf("update token id %d affected %d rows, want 1", id, result.RowsAffected)
			}

		case BatchUpdateTypeChannelUsedQuota:
			if len(updates) != 1 {
				return "", errors.New("channel batch update group must contain one delta")
			}
			result := tx.Model(&Channel{}).Where("id = ?", id).
				Update("used_quota", gorm.Expr("used_quota + ?", updates[0].Delta))
			if result.Error != nil {
				return "", fmt.Errorf("update channel id %d: %w", id, result.Error)
			}
			switch result.RowsAffected {
			case 0:
				if err := discardDeletedBatchUpdates(tx, "channel", id, updates); err != nil {
					return "", err
				}
				return flushOperationResultBatchDiscarded, nil
			case 1:
				return flushOperationResultBatchUpdated, nil
			default:
				return "", fmt.Errorf("update channel id %d affected %d rows, want 1", id, result.RowsAffected)
			}
		default:
			return "", fmt.Errorf("unsupported batch update kind %d", updates[0].Kind)
		}
	})
	if err != nil {
		return err
	}
	if hasUsedQuotaUpdate && resultCode == flushOperationResultBatchUpdated {
		submitInviterRewardAfterUsedQuota(rewardTicket, id)
		rewardSubmitted = rewardTicket != nil
	}
	return nil
}

func batchUpdateOperationScope(updates []BatchUpdate) (string, error) {
	key, err := batchUpdateGroupKey(updates)
	if err != nil {
		return "", err
	}
	scope := key
	if separator := strings.IndexByte(key, ':'); separator >= 0 {
		scope = key[:separator]
	}
	return "batch-" + scope, nil
}

func requireOneBatchUpdateRow(entity string, id int, result *gorm.DB) error {
	if result.Error != nil {
		return fmt.Errorf("update %s id %d: %w", entity, id, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("update %s id %d affected %d rows, want 1", entity, id, result.RowsAffected)
	}
	return nil
}

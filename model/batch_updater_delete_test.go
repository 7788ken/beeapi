package model

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type deleteLedgerExpectation struct {
	delta int64
	count int64
}

type batchDeleteTestTarget struct {
	id     int
	userID int
}

type batchDeleteScenario struct {
	name    string
	seed    func(*testing.T) batchDeleteTestTarget
	delete  func(batchDeleteTestTarget) error
	updates func(batchDeleteTestTarget, int) []BatchUpdate
}

func seedBatchDeleteTestUser(t *testing.T, label string) *User {
	t.Helper()
	suffix := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name() + "-" + label)
	user := &User{
		Username: fmt.Sprintf("delete-user-%s", suffix),
		Password: "password123",
		AffCode:  fmt.Sprintf("delete-aff-%s", suffix),
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Quota:    100,
	}
	if err := DB.Create(user).Error; err != nil {
		t.Fatalf("create delete test user: %v", err)
	}
	return user
}

func seedBatchDeleteTestToken(t *testing.T, label string) (*Token, *User) {
	t.Helper()
	user := seedBatchDeleteTestUser(t, label+"-owner")
	suffix := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name() + "-" + label)
	token := &Token{
		UserId:      user.Id,
		Key:         fmt.Sprintf("delete-token-%s", suffix),
		Name:        fmt.Sprintf("delete token %s", suffix),
		Status:      common.TokenStatusEnabled,
		RemainQuota: 100,
	}
	if err := DB.Create(token).Error; err != nil {
		t.Fatalf("create delete test token: %v", err)
	}
	return token, user
}

func seedBatchDeleteTestChannel(t *testing.T, label string, status int) *Channel {
	t.Helper()
	suffix := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name() + "-" + label)
	channel := &Channel{
		Key:       fmt.Sprintf("delete-channel-%s", suffix),
		Name:      fmt.Sprintf("delete channel %s", suffix),
		Status:    status,
		UsedQuota: 100,
	}
	if err := DB.Create(channel).Error; err != nil {
		t.Fatalf("create delete test channel: %v", err)
	}
	return channel
}

func batchDeleteScenarios() []batchDeleteScenario {
	return []batchDeleteScenario{
		{
			name: "user",
			seed: func(t *testing.T) batchDeleteTestTarget {
				user := seedBatchDeleteTestUser(t, "batch")
				return batchDeleteTestTarget{id: user.Id}
			},
			delete: func(target batchDeleteTestTarget) error {
				return HardDeleteUserById(target.id)
			},
			updates: func(target batchDeleteTestTarget, multiplier int) []BatchUpdate {
				return []BatchUpdate{
					{Kind: BatchUpdateTypeUserQuota, ID: target.id, Delta: 11 * multiplier},
					{Kind: BatchUpdateTypeUsedQuota, ID: target.id, Delta: 12 * multiplier},
					{Kind: BatchUpdateTypeRequestCount, ID: target.id, Delta: multiplier},
				}
			},
		},
		{
			name: "token",
			seed: func(t *testing.T) batchDeleteTestTarget {
				token, user := seedBatchDeleteTestToken(t, "batch")
				return batchDeleteTestTarget{id: token.Id, userID: user.Id}
			},
			delete: func(target batchDeleteTestTarget) error {
				return DeleteTokenById(target.id, target.userID)
			},
			updates: func(target batchDeleteTestTarget, multiplier int) []BatchUpdate {
				return []BatchUpdate{{
					Kind:  BatchUpdateTypeTokenQuota,
					ID:    target.id,
					Delta: -13 * multiplier,
				}}
			},
		},
		{
			name: "channel",
			seed: func(t *testing.T) batchDeleteTestTarget {
				channel := seedBatchDeleteTestChannel(t, "batch", common.ChannelStatusEnabled)
				return batchDeleteTestTarget{id: channel.Id}
			},
			delete: func(target batchDeleteTestTarget) error {
				channel := Channel{Id: target.id}
				return channel.Delete()
			},
			updates: func(target batchDeleteTestTarget, multiplier int) []BatchUpdate {
				return []BatchUpdate{{
					Kind:  BatchUpdateTypeChannelUsedQuota,
					ID:    target.id,
					Delta: 14 * multiplier,
				}}
			},
		},
	}
}

func ledgerExpectations(updates ...[]BatchUpdate) map[int]deleteLedgerExpectation {
	expected := make(map[int]deleteLedgerExpectation)
	for _, group := range updates {
		for _, update := range group {
			value := expected[update.Kind]
			value.delta += int64(update.Delta)
			value.count++
			expected[update.Kind] = value
		}
	}
	return expected
}

func emptyDeleteLedgerExpectations(kinds ...int) map[int]deleteLedgerExpectation {
	expected := make(map[int]deleteLedgerExpectation, len(kinds))
	for _, kind := range kinds {
		expected[kind] = deleteLedgerExpectation{}
	}
	return expected
}

func requireDeleteLedgers(
	t *testing.T,
	entityID int,
	expected map[int]deleteLedgerExpectation,
) {
	t.Helper()
	var ledgers []BatchUpdateDeleteLedger
	if err := DB.Where("entity_id = ?", entityID).Order("kind").Find(&ledgers).Error; err != nil {
		t.Fatalf("load delete ledgers for entity %d: %v", entityID, err)
	}
	if len(ledgers) != len(expected) {
		t.Fatalf("delete ledger count for entity %d = %d, want %d: %#v", entityID, len(ledgers), len(expected), ledgers)
	}
	for _, ledger := range ledgers {
		want, ok := expected[ledger.Kind]
		if !ok {
			t.Fatalf("unexpected delete ledger kind=%d entity_id=%d", ledger.Kind, entityID)
		}
		if ledger.DeletedAt <= 0 {
			t.Fatalf("delete ledger kind=%d entity_id=%d has invalid deleted_at=%d", ledger.Kind, entityID, ledger.DeletedAt)
		}
		if ledger.DiscardedDelta != want.delta || ledger.DiscardedCount != want.count {
			t.Fatalf(
				"delete ledger kind=%d entity_id=%d discarded=(%d,%d), want=(%d,%d)",
				ledger.Kind,
				entityID,
				ledger.DiscardedDelta,
				ledger.DiscardedCount,
				want.delta,
				want.count,
			)
		}
	}
}

func requireNoDeleteLedgers(t *testing.T, entityID int) {
	t.Helper()
	var count int64
	if err := DB.Model(&BatchUpdateDeleteLedger{}).Where("entity_id = ?", entityID).Count(&count).Error; err != nil {
		t.Fatalf("count delete ledgers for entity %d: %v", entityID, err)
	}
	if count != 0 {
		t.Fatalf("delete ledger count for entity %d = %d, want 0", entityID, count)
	}
}

func requireDeleteLedgerTimestamp(t *testing.T, entityID int, expected int64) {
	t.Helper()
	var ledgers []BatchUpdateDeleteLedger
	if err := DB.Where("entity_id = ?", entityID).Find(&ledgers).Error; err != nil {
		t.Fatalf("load delete ledger timestamps for entity %d: %v", entityID, err)
	}
	if len(ledgers) == 0 {
		t.Fatalf("no delete ledgers found for entity %d", entityID)
	}
	for _, ledger := range ledgers {
		if ledger.DeletedAt != expected {
			t.Fatalf(
				"delete ledger kind=%d entity_id=%d deleted_at=%d, want original soft-delete timestamp %d",
				ledger.Kind,
				entityID,
				ledger.DeletedAt,
				expected,
			)
		}
	}
}

func stopDeleteTestUpdater(t *testing.T, updater *BatchUpdater) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := updater.StopAndFlush(ctx); err != nil {
		t.Fatalf("stop delete test updater: %v", err)
	}
	if got := updater.Pending(); got != 0 {
		t.Fatalf("pending after stop = %d, want 0", got)
	}
}

func waitDeleteTestSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("timeout waiting for %s: %v", label, ctx.Err())
	}
}

func waitDeleteTestResult(t *testing.T, result <-chan error, label string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for %s: %v", label, ctx.Err())
	}
}

func TestDeleteEntrypointsCreateDurableBatchUpdateLedgers(t *testing.T) {
	t.Run("DeleteUserById", func(t *testing.T) {
		truncateTables(t)
		user := seedBatchDeleteTestUser(t, "by-id")
		if err := DeleteUserById(user.Id); err != nil {
			t.Fatalf("DeleteUserById: %v", err)
		}
		requireDeleteLedgers(t, user.Id, emptyDeleteLedgerExpectations(
			BatchUpdateTypeUserQuota,
			BatchUpdateTypeUsedQuota,
			BatchUpdateTypeRequestCount,
		))
		var deleted User
		if err := DB.Unscoped().First(&deleted, user.Id).Error; err != nil {
			t.Fatalf("load soft-deleted user: %v", err)
		}
		if !deleted.DeletedAt.Valid {
			t.Fatal("DeleteUserById did not soft-delete user")
		}
	})

	t.Run("UserDelete", func(t *testing.T) {
		truncateTables(t)
		user := seedBatchDeleteTestUser(t, "method")
		if err := user.Delete(); err != nil {
			t.Fatalf("User.Delete: %v", err)
		}
		requireDeleteLedgers(t, user.Id, emptyDeleteLedgerExpectations(
			BatchUpdateTypeUserQuota,
			BatchUpdateTypeUsedQuota,
			BatchUpdateTypeRequestCount,
		))
	})

	t.Run("HardDeleteUserById", func(t *testing.T) {
		truncateTables(t)
		user := seedBatchDeleteTestUser(t, "hard")
		if err := HardDeleteUserById(user.Id); err != nil {
			t.Fatalf("HardDeleteUserById: %v", err)
		}
		requireDeleteLedgers(t, user.Id, emptyDeleteLedgerExpectations(
			BatchUpdateTypeUserQuota,
			BatchUpdateTypeUsedQuota,
			BatchUpdateTypeRequestCount,
		))
		if err := DB.Unscoped().First(&User{}, user.Id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("hard-deleted user lookup error = %v, want record not found", err)
		}
	})

	t.Run("UserHardDelete", func(t *testing.T) {
		truncateTables(t)
		user := seedBatchDeleteTestUser(t, "hard-method")
		if err := user.HardDelete(); err != nil {
			t.Fatalf("User.HardDelete: %v", err)
		}
		requireDeleteLedgers(t, user.Id, emptyDeleteLedgerExpectations(
			BatchUpdateTypeUserQuota,
			BatchUpdateTypeUsedQuota,
			BatchUpdateTypeRequestCount,
		))
		if err := DB.Unscoped().First(&User{}, user.Id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("hard-deleted user lookup error = %v, want record not found", err)
		}
	})

	t.Run("DeleteTokenById", func(t *testing.T) {
		truncateTables(t)
		token, user := seedBatchDeleteTestToken(t, "single")
		if err := DeleteTokenById(token.Id, user.Id); err != nil {
			t.Fatalf("DeleteTokenById: %v", err)
		}
		requireDeleteLedgers(t, token.Id, emptyDeleteLedgerExpectations(
			BatchUpdateTypeTokenQuota,
		))
		var deleted Token
		if err := DB.Unscoped().First(&deleted, token.Id).Error; err != nil {
			t.Fatalf("load soft-deleted token: %v", err)
		}
		if !deleted.DeletedAt.Valid {
			t.Fatal("DeleteTokenById did not soft-delete token")
		}
	})

	t.Run("BatchDeleteTokens", func(t *testing.T) {
		truncateTables(t)
		first, user := seedBatchDeleteTestToken(t, "batch-first")
		second := &Token{
			UserId:      user.Id,
			Key:         fmt.Sprintf("delete-token-%s-second", t.Name()),
			Name:        "batch second",
			Status:      common.TokenStatusEnabled,
			RemainQuota: 100,
		}
		if err := DB.Create(second).Error; err != nil {
			t.Fatalf("create second token: %v", err)
		}
		deleted, err := BatchDeleteTokens([]int{first.Id, second.Id}, user.Id)
		if err != nil {
			t.Fatalf("BatchDeleteTokens: %v", err)
		}
		if deleted != 2 {
			t.Fatalf("BatchDeleteTokens deleted = %d, want 2", deleted)
		}
		for _, token := range []*Token{first, second} {
			requireDeleteLedgers(t, token.Id, emptyDeleteLedgerExpectations(
				BatchUpdateTypeTokenQuota,
			))
		}
	})

	t.Run("ChannelDelete", func(t *testing.T) {
		truncateTables(t)
		channel := seedBatchDeleteTestChannel(t, "single", common.ChannelStatusEnabled)
		ability := &Ability{
			Group:     "default",
			Model:     "delete-channel-model",
			ChannelId: channel.Id,
			Enabled:   true,
		}
		if err := DB.Create(ability).Error; err != nil {
			t.Fatalf("create channel ability: %v", err)
		}
		if err := channel.Delete(); err != nil {
			t.Fatalf("Channel.Delete: %v", err)
		}
		requireDeleteLedgers(t, channel.Id, emptyDeleteLedgerExpectations(
			BatchUpdateTypeChannelUsedQuota,
		))
		if err := DB.First(&Channel{}, channel.Id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("deleted channel lookup error = %v, want record not found", err)
		}
		var abilityCount int64
		if err := DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Count(&abilityCount).Error; err != nil {
			t.Fatalf("count deleted channel abilities: %v", err)
		}
		if abilityCount != 0 {
			t.Fatalf("abilities remaining after channel delete = %d, want 0", abilityCount)
		}
	})

	t.Run("BatchDeleteChannels", func(t *testing.T) {
		truncateTables(t)
		first := seedBatchDeleteTestChannel(t, "batch-first", common.ChannelStatusEnabled)
		second := seedBatchDeleteTestChannel(t, "batch-second", common.ChannelStatusEnabled)
		if err := BatchDeleteChannels([]int{first.Id, second.Id}); err != nil {
			t.Fatalf("BatchDeleteChannels: %v", err)
		}
		for _, channel := range []*Channel{first, second} {
			requireDeleteLedgers(t, channel.Id, emptyDeleteLedgerExpectations(
				BatchUpdateTypeChannelUsedQuota,
			))
		}
	})

	t.Run("DeleteDisabledChannel", func(t *testing.T) {
		truncateTables(t)
		autoDisabled := seedBatchDeleteTestChannel(t, "auto-disabled", common.ChannelStatusAutoDisabled)
		manualDisabled := seedBatchDeleteTestChannel(t, "manual-disabled", common.ChannelStatusManuallyDisabled)
		enabled := seedBatchDeleteTestChannel(t, "enabled", common.ChannelStatusEnabled)
		deleted, err := DeleteDisabledChannel()
		if err != nil {
			t.Fatalf("DeleteDisabledChannel: %v", err)
		}
		if deleted != 2 {
			t.Fatalf("DeleteDisabledChannel deleted = %d, want 2", deleted)
		}
		for _, channel := range []*Channel{autoDisabled, manualDisabled} {
			requireDeleteLedgers(t, channel.Id, emptyDeleteLedgerExpectations(
				BatchUpdateTypeChannelUsedQuota,
			))
		}
		requireNoDeleteLedgers(t, enabled.Id)
		if err := DB.First(&Channel{}, enabled.Id).Error; err != nil {
			t.Fatalf("enabled channel was deleted: %v", err)
		}
	})

	t.Run("DeleteChannelByStatus", func(t *testing.T) {
		truncateTables(t)
		channel := seedBatchDeleteTestChannel(t, "status", common.ChannelStatusAutoDisabled)
		deleted, err := DeleteChannelByStatus(common.ChannelStatusAutoDisabled)
		if err != nil {
			t.Fatalf("DeleteChannelByStatus: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("DeleteChannelByStatus deleted = %d, want 1", deleted)
		}
		requireDeleteLedgers(t, channel.Id, emptyDeleteLedgerExpectations(
			BatchUpdateTypeChannelUsedQuota,
		))
	})
}

func TestDeleteLedgerConflictRollsBackEntityDeletion(t *testing.T) {
	tests := []struct {
		name        string
		seed        func(*testing.T) batchDeleteTestTarget
		kind        int
		delete      func(batchDeleteTestTarget) error
		assertAlive func(*testing.T, batchDeleteTestTarget)
	}{
		{
			name: "user",
			seed: func(t *testing.T) batchDeleteTestTarget {
				user := seedBatchDeleteTestUser(t, "conflict")
				return batchDeleteTestTarget{id: user.Id}
			},
			kind: BatchUpdateTypeUserQuota,
			delete: func(target batchDeleteTestTarget) error {
				return HardDeleteUserById(target.id)
			},
			assertAlive: func(t *testing.T, target batchDeleteTestTarget) {
				if err := DB.First(&User{}, target.id).Error; err != nil {
					t.Fatalf("user was deleted after ledger conflict: %v", err)
				}
			},
		},
		{
			name: "token",
			seed: func(t *testing.T) batchDeleteTestTarget {
				token, user := seedBatchDeleteTestToken(t, "conflict")
				return batchDeleteTestTarget{id: token.Id, userID: user.Id}
			},
			kind: BatchUpdateTypeTokenQuota,
			delete: func(target batchDeleteTestTarget) error {
				return DeleteTokenById(target.id, target.userID)
			},
			assertAlive: func(t *testing.T, target batchDeleteTestTarget) {
				if err := DB.First(&Token{}, target.id).Error; err != nil {
					t.Fatalf("token was deleted after ledger conflict: %v", err)
				}
			},
		},
		{
			name: "channel",
			seed: func(t *testing.T) batchDeleteTestTarget {
				channel := seedBatchDeleteTestChannel(t, "conflict", common.ChannelStatusEnabled)
				return batchDeleteTestTarget{id: channel.Id}
			},
			kind: BatchUpdateTypeChannelUsedQuota,
			delete: func(target batchDeleteTestTarget) error {
				channel := Channel{Id: target.id}
				return channel.Delete()
			},
			assertAlive: func(t *testing.T, target batchDeleteTestTarget) {
				if err := DB.First(&Channel{}, target.id).Error; err != nil {
					t.Fatalf("channel was deleted after ledger conflict: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			target := test.seed(t)
			if err := DB.Create(&BatchUpdateDeleteLedger{
				Kind:      test.kind,
				EntityId:  target.id,
				DeletedAt: 1,
			}).Error; err != nil {
				t.Fatalf("seed conflicting ledger: %v", err)
			}
			if err := test.delete(target); err == nil {
				t.Fatal("delete succeeded despite conflicting ledger")
			}
			test.assertAlive(t, target)
			requireDeleteLedgers(t, target.id, map[int]deleteLedgerExpectation{
				test.kind: {},
			})
		})
	}
}

func TestBatchUpdaterDiscardsQueuedDeltaAfterDeletion(t *testing.T) {
	for _, scenario := range batchDeleteScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			truncateTables(t)
			target := scenario.seed(t)
			updates := scenario.updates(target, 1)
			updater := startBatchUpdaterForTest(t, applyDefaultBatchUpdates)
			if err := updater.AddMany(updates); err != nil {
				t.Fatalf("queue updates: %v", err)
			}
			if err := scenario.delete(target); err != nil {
				t.Fatalf("delete target: %v", err)
			}
			stopDeleteTestUpdater(t, updater)
			requireDeleteLedgers(t, target.id, ledgerExpectations(updates))
		})
	}
}

func TestBatchUpdaterReplaysAmbiguousDeletedEntityAuditWithoutDoubleCounting(t *testing.T) {
	for _, scenario := range batchDeleteScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			truncateTables(t)
			target := scenario.seed(t)
			updates := scenario.updates(target, 1)
			if err := scenario.delete(target); err != nil {
				t.Fatalf("delete target: %v", err)
			}

			forcedError := errors.New("forced ambiguous delete audit result")
			attempts := 0
			updater := startBatchUpdaterForTest(t, func(ctx context.Context, group []BatchUpdate) error {
				attempts++
				if err := applyDefaultBatchUpdates(ctx, group); err != nil {
					return err
				}
				if attempts == 1 {
					return forcedError
				}
				return nil
			})
			if err := updater.AddMany(updates); err != nil {
				t.Fatalf("queue deleted target updates: %v", err)
			}
			if err := updater.Flush(context.Background()); !errors.Is(err, forcedError) {
				t.Fatalf("first flush error = %v, want ambiguous result", err)
			}
			requireDeleteLedgers(t, target.id, ledgerExpectations(updates))

			if err := updater.Flush(context.Background()); err != nil {
				t.Fatalf("replay deleted target audit: %v", err)
			}
			requireDeleteLedgers(t, target.id, ledgerExpectations(updates))
		})
	}
}

func TestBatchUpdaterDiscardsInFlightSnapshotAfterDeletion(t *testing.T) {
	for _, scenario := range batchDeleteScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			truncateTables(t)
			target := scenario.seed(t)
			updates := scenario.updates(target, 1)
			applyStarted := make(chan struct{})
			releaseApply := make(chan struct{})
			var blockFirst sync.Once
			updater := startBatchUpdaterForTest(t, func(ctx context.Context, group []BatchUpdate) error {
				blockFirst.Do(func() {
					close(applyStarted)
					select {
					case <-releaseApply:
					case <-ctx.Done():
					}
				})
				if err := ctx.Err(); err != nil {
					return err
				}
				return applyDefaultBatchUpdates(ctx, group)
			})
			if err := updater.AddMany(updates); err != nil {
				t.Fatalf("queue updates: %v", err)
			}

			flushDone := make(chan error, 1)
			go func() {
				flushDone <- updater.Flush(context.Background())
			}()
			waitDeleteTestSignal(t, applyStarted, "in-flight snapshot")
			if err := scenario.delete(target); err != nil {
				t.Fatalf("delete in-flight target: %v", err)
			}
			close(releaseApply)
			waitDeleteTestResult(t, flushDone, "flush in-flight deleted target")
			stopDeleteTestUpdater(t, updater)
			requireDeleteLedgers(t, target.id, ledgerExpectations(updates))
		})
	}
}

func TestTwoBatchUpdatersDiscardLateAddsUsingSharedDeleteLedger(t *testing.T) {
	for _, scenario := range batchDeleteScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			truncateTables(t)
			target := scenario.seed(t)
			if err := scenario.delete(target); err != nil {
				t.Fatalf("delete target before late adds: %v", err)
			}

			firstUpdates := scenario.updates(target, 1)
			secondUpdates := scenario.updates(target, 2)
			first := startBatchUpdaterForTest(t, applyDefaultBatchUpdates)
			second := startBatchUpdaterForTest(t, applyDefaultBatchUpdates)
			admissionErrors := make(chan error, 2)
			go func() {
				admissionErrors <- first.AddMany(firstUpdates)
			}()
			go func() {
				admissionErrors <- second.AddMany(secondUpdates)
			}()
			for range 2 {
				if err := <-admissionErrors; err != nil {
					t.Fatalf("late add: %v", err)
				}
			}

			stopErrors := make(chan error, 2)
			for _, updater := range []*BatchUpdater{first, second} {
				go func(updater *BatchUpdater) {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					stopErrors <- updater.StopAndFlush(ctx)
				}(updater)
			}
			for range 2 {
				if err := <-stopErrors; err != nil {
					t.Fatalf("stop updater with shared ledger: %v", err)
				}
			}
			if first.Pending() != 0 || second.Pending() != 0 {
				t.Fatalf("pending after shared-ledger stop = (%d,%d), want (0,0)", first.Pending(), second.Pending())
			}
			requireDeleteLedgers(t, target.id, ledgerExpectations(firstUpdates, secondUpdates))
		})
	}
}

func TestDeletedUserGroupAuditRollsBackWhenOneLedgerIsMissing(t *testing.T) {
	for _, missingKind := range []int{
		BatchUpdateTypeUserQuota,
		BatchUpdateTypeUsedQuota,
		BatchUpdateTypeRequestCount,
	} {
		t.Run(fmt.Sprintf("missing-kind-%d", missingKind), func(t *testing.T) {
			truncateTables(t)
			user := seedBatchDeleteTestUser(t, "missing-group-ledger")
			if err := HardDeleteUserById(user.Id); err != nil {
				t.Fatalf("hard delete user: %v", err)
			}
			if err := DB.Where(
				"kind = ? AND entity_id = ?",
				missingKind,
				user.Id,
			).Delete(&BatchUpdateDeleteLedger{}).Error; err != nil {
				t.Fatalf("remove one user delete ledger: %v", err)
			}

			updates := []BatchUpdate{
				{Kind: BatchUpdateTypeUserQuota, ID: user.Id, Delta: 21},
				{Kind: BatchUpdateTypeUsedQuota, ID: user.Id, Delta: 22},
				{Kind: BatchUpdateTypeRequestCount, ID: user.Id, Delta: 1},
			}
			updater := startBatchUpdaterForTest(t, applyDefaultBatchUpdates)
			if err := updater.AddMany(updates); err != nil {
				t.Fatalf("queue deleted user updates: %v", err)
			}
			err := updater.Flush(context.Background())
			if err == nil || !strings.Contains(err.Error(), "affected 0 rows, want 1") {
				t.Fatalf("flush error = %v, want missing-row error", err)
			}
			if got := updater.Pending(); got != len(updates) {
				t.Fatalf("pending after rolled-back audit = %d, want %d", got, len(updates))
			}
			unchangedLedgers := emptyDeleteLedgerExpectations(
				BatchUpdateTypeUserQuota,
				BatchUpdateTypeUsedQuota,
				BatchUpdateTypeRequestCount,
			)
			delete(unchangedLedgers, missingKind)
			requireDeleteLedgers(t, user.Id, unchangedLedgers)

			if err := DB.Create(&BatchUpdateDeleteLedger{
				Kind:      missingKind,
				EntityId:  user.Id,
				DeletedAt: common.GetTimestamp(),
			}).Error; err != nil {
				t.Fatalf("repair missing delete ledger for cleanup: %v", err)
			}
			stopDeleteTestUpdater(t, updater)
			requireDeleteLedgers(t, user.Id, ledgerExpectations(updates))
		})
	}
}

func TestMissingRowsWithoutDeleteLedgerRemainPending(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T) (int, []BatchUpdate)
	}{
		{
			name: "unknown user",
			prepare: func(*testing.T) (int, []BatchUpdate) {
				const id = 900000001
				return id, []BatchUpdate{
					{Kind: BatchUpdateTypeUserQuota, ID: id, Delta: 31},
					{Kind: BatchUpdateTypeUsedQuota, ID: id, Delta: 32},
					{Kind: BatchUpdateTypeRequestCount, ID: id, Delta: 1},
				}
			},
		},
		{
			name: "historical hard-deleted token",
			prepare: func(t *testing.T) (int, []BatchUpdate) {
				token, _ := seedBatchDeleteTestToken(t, "historical-hard")
				if err := DB.Unscoped().Delete(token).Error; err != nil {
					t.Fatalf("raw hard delete token: %v", err)
				}
				return token.Id, []BatchUpdate{{
					Kind:  BatchUpdateTypeTokenQuota,
					ID:    token.Id,
					Delta: -33,
				}}
			},
		},
		{
			name: "historical hard-deleted channel",
			prepare: func(t *testing.T) (int, []BatchUpdate) {
				channel := seedBatchDeleteTestChannel(t, "historical-hard", common.ChannelStatusEnabled)
				if err := DB.Unscoped().Delete(channel).Error; err != nil {
					t.Fatalf("raw hard delete channel: %v", err)
				}
				return channel.Id, []BatchUpdate{{
					Kind:  BatchUpdateTypeChannelUsedQuota,
					ID:    channel.Id,
					Delta: 34,
				}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			entityID, updates := test.prepare(t)
			updater := startBatchUpdaterForTest(t, applyDefaultBatchUpdates)
			if err := updater.AddMany(updates); err != nil {
				t.Fatalf("queue missing-row updates: %v", err)
			}
			err := updater.Flush(context.Background())
			if err == nil || !strings.Contains(err.Error(), "affected 0 rows, want 1") {
				t.Fatalf("flush error = %v, want missing-row error", err)
			}
			if got := updater.Pending(); got != len(updates) {
				t.Fatalf("pending after missing-row error = %d, want %d", got, len(updates))
			}
			requireNoDeleteLedgers(t, entityID)

			kinds := make([]int, 0, len(updates))
			for _, update := range updates {
				kinds = append(kinds, update.Kind)
			}
			if err := createBatchUpdateDeleteLedgers(DB, entityID, kinds...); err != nil {
				t.Fatalf("seed cleanup ledgers: %v", err)
			}
			stopDeleteTestUpdater(t, updater)
			requireDeleteLedgers(t, entityID, ledgerExpectations(updates))
		})
	}
}

func TestHistoricalSoftDeletedRowsCreateLedgerOnDemand(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T) (int, int64, []BatchUpdate)
	}{
		{
			name: "user",
			prepare: func(t *testing.T) (int, int64, []BatchUpdate) {
				user := seedBatchDeleteTestUser(t, "historical-soft")
				if err := DB.Delete(user).Error; err != nil {
					t.Fatalf("raw soft delete user: %v", err)
				}
				historicalDeletedAt := time.Unix(1_700_000_001, 0).UTC()
				if err := DB.Unscoped().Model(&User{}).
					Where("id = ?", user.Id).
					Update("deleted_at", historicalDeletedAt).Error; err != nil {
					t.Fatalf("set historical user deleted_at: %v", err)
				}
				var deleted User
				if err := DB.Unscoped().First(&deleted, user.Id).Error; err != nil {
					t.Fatalf("reload raw soft-deleted user: %v", err)
				}
				if !deleted.DeletedAt.Valid {
					t.Fatal("raw soft-deleted user has no deleted_at")
				}
				return user.Id, deleted.DeletedAt.Time.Unix(), []BatchUpdate{
					{Kind: BatchUpdateTypeUserQuota, ID: user.Id, Delta: 41},
					{Kind: BatchUpdateTypeUsedQuota, ID: user.Id, Delta: 42},
					{Kind: BatchUpdateTypeRequestCount, ID: user.Id, Delta: 1},
				}
			},
		},
		{
			name: "token",
			prepare: func(t *testing.T) (int, int64, []BatchUpdate) {
				token, _ := seedBatchDeleteTestToken(t, "historical-soft")
				if err := DB.Delete(token).Error; err != nil {
					t.Fatalf("raw soft delete token: %v", err)
				}
				historicalDeletedAt := time.Unix(1_700_000_002, 0).UTC()
				if err := DB.Unscoped().Model(&Token{}).
					Where("id = ?", token.Id).
					Update("deleted_at", historicalDeletedAt).Error; err != nil {
					t.Fatalf("set historical token deleted_at: %v", err)
				}
				var deleted Token
				if err := DB.Unscoped().First(&deleted, token.Id).Error; err != nil {
					t.Fatalf("reload raw soft-deleted token: %v", err)
				}
				if !deleted.DeletedAt.Valid {
					t.Fatal("raw soft-deleted token has no deleted_at")
				}
				return token.Id, deleted.DeletedAt.Time.Unix(), []BatchUpdate{{
					Kind:  BatchUpdateTypeTokenQuota,
					ID:    token.Id,
					Delta: -43,
				}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			entityID, deletedAt, updates := test.prepare(t)
			requireNoDeleteLedgers(t, entityID)
			updater := startBatchUpdaterForTest(t, applyDefaultBatchUpdates)
			if err := updater.AddMany(updates); err != nil {
				t.Fatalf("queue historical soft-delete updates: %v", err)
			}
			stopDeleteTestUpdater(t, updater)
			requireDeleteLedgers(t, entityID, ledgerExpectations(updates))
			requireDeleteLedgerTimestamp(t, entityID, deletedAt)
		})
	}
}

func TestUserHardDeleteReusesExistingSoftDeleteLedgers(t *testing.T) {
	truncateTables(t)
	user := seedBatchDeleteTestUser(t, "soft-then-hard")
	if err := user.Delete(); err != nil {
		t.Fatalf("soft delete user: %v", err)
	}

	expected := map[int]BatchUpdateDeleteLedger{}
	for index, kind := range []int{
		BatchUpdateTypeUserQuota,
		BatchUpdateTypeUsedQuota,
		BatchUpdateTypeRequestCount,
	} {
		delta := int64(51 + index)
		count := int64(2 + index)
		result := DB.Model(&BatchUpdateDeleteLedger{}).
			Where("kind = ? AND entity_id = ?", kind, user.Id).
			Updates(map[string]interface{}{
				"discarded_delta": delta,
				"discarded_count": count,
			})
		if result.Error != nil {
			t.Fatalf("seed existing ledger audit kind=%d: %v", kind, result.Error)
		}
		if result.RowsAffected != 1 {
			t.Fatalf("seed existing ledger audit kind=%d affected %d rows, want 1", kind, result.RowsAffected)
		}
		var ledger BatchUpdateDeleteLedger
		if err := DB.First(
			&ledger,
			"kind = ? AND entity_id = ?",
			kind,
			user.Id,
		).Error; err != nil {
			t.Fatalf("load existing soft-delete ledger kind=%d: %v", kind, err)
		}
		expected[kind] = ledger
	}

	if err := user.HardDelete(); err != nil {
		t.Fatalf("hard delete previously soft-deleted user: %v", err)
	}
	if err := DB.Unscoped().First(&User{}, user.Id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("soft-then-hard user lookup error = %v, want record not found", err)
	}

	var ledgers []BatchUpdateDeleteLedger
	if err := DB.Where("entity_id = ?", user.Id).Find(&ledgers).Error; err != nil {
		t.Fatalf("load ledgers after hard delete: %v", err)
	}
	if len(ledgers) != len(expected) {
		t.Fatalf("ledgers after hard delete = %d, want %d", len(ledgers), len(expected))
	}
	for _, ledger := range ledgers {
		if ledger != expected[ledger.Kind] {
			t.Fatalf("ledger kind=%d changed across hard delete: got %#v want %#v", ledger.Kind, ledger, expected[ledger.Kind])
		}
	}
}

func TestUserHardDeleteBackfillsLegacySoftDeleteLedgers(t *testing.T) {
	truncateTables(t)
	user := seedBatchDeleteTestUser(t, "legacy-soft-then-hard")
	if err := DB.Delete(user).Error; err != nil {
		t.Fatalf("raw soft delete legacy user: %v", err)
	}
	historicalDeletedAt := time.Unix(1_700_000_003, 0).UTC()
	if err := DB.Unscoped().Model(&User{}).
		Where("id = ?", user.Id).
		Update("deleted_at", historicalDeletedAt).Error; err != nil {
		t.Fatalf("set historical legacy user deleted_at: %v", err)
	}
	var softDeleted User
	if err := DB.Unscoped().First(&softDeleted, user.Id).Error; err != nil {
		t.Fatalf("reload legacy soft-deleted user: %v", err)
	}
	if !softDeleted.DeletedAt.Valid {
		t.Fatal("legacy soft-deleted user has no deleted_at")
	}
	originalDeletedAt := softDeleted.DeletedAt.Time.Unix()
	requireNoDeleteLedgers(t, user.Id)

	if err := user.HardDelete(); err != nil {
		t.Fatalf("hard delete legacy soft-deleted user: %v", err)
	}
	if err := DB.Unscoped().First(&User{}, user.Id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("legacy soft-then-hard user lookup error = %v, want record not found", err)
	}
	requireDeleteLedgers(t, user.Id, emptyDeleteLedgerExpectations(
		BatchUpdateTypeUserQuota,
		BatchUpdateTypeUsedQuota,
		BatchUpdateTypeRequestCount,
	))
	requireDeleteLedgerTimestamp(t, user.Id, originalDeletedAt)
}

func seedBatchDeleteTokensAboveBindLimit(
	t *testing.T,
	label string,
	count int,
) (*User, []Token, []int) {
	t.Helper()
	user := seedBatchDeleteTestUser(t, label+"-owner")
	tokens := make([]Token, count)
	for index := range tokens {
		tokens[index] = Token{
			UserId:      user.Id,
			Key:         fmt.Sprintf("bulk-delete-%s-%04d", label, index),
			Name:        fmt.Sprintf("bulk delete token %04d", index),
			Status:      common.TokenStatusEnabled,
			RemainQuota: 100,
		}
	}
	if err := DB.CreateInBatches(&tokens, 200).Error; err != nil {
		t.Fatalf("create %d bulk-delete tokens: %v", count, err)
	}
	ids := make([]int, len(tokens))
	for index := range tokens {
		ids[index] = tokens[index].Id
	}
	return user, tokens, ids
}

func maxBoundSliceLength(value reflect.Value) int {
	if !value.IsValid() {
		return 0
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	maximum := 0
	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		maximum = value.Len()
		for index := 0; index < value.Len(); index++ {
			if nested := maxBoundSliceLength(value.Index(index)); nested > maximum {
				maximum = nested
			}
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if nested := maxBoundSliceLength(value.Field(index)); nested > maximum {
				maximum = nested
			}
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			if nested := maxBoundSliceLength(iterator.Value()); nested > maximum {
				maximum = nested
			}
		}
	}
	return maximum
}

func enforceBatchDeleteTokenBindLimit(t *testing.T, limit int) {
	t.Helper()
	check := func(tx *gorm.DB) {
		where, ok := tx.Statement.Clauses["WHERE"]
		if !ok {
			return
		}
		if bound := maxBoundSliceLength(reflect.ValueOf(where.Expression)); bound > limit {
			tx.AddError(fmt.Errorf("test database bind limit exceeded: %d > %d", bound, limit))
		}
	}
	queryCallback := "test:batch-delete-token-query-bind-limit"
	deleteCallback := "test:batch-delete-token-delete-bind-limit"
	if err := DB.Callback().Query().Before("gorm:query").Register(queryCallback, check); err != nil {
		t.Fatalf("register query bind-limit callback: %v", err)
	}
	if err := DB.Callback().Delete().Before("gorm:delete").Register(deleteCallback, check); err != nil {
		t.Fatalf("register delete bind-limit callback: %v", err)
	}
	t.Cleanup(func() {
		_ = DB.Callback().Query().Remove(queryCallback)
		_ = DB.Callback().Delete().Remove(deleteCallback)
	})
}

func TestUniqueBatchDeleteIDsAreStrictlyAscending(t *testing.T) {
	input := []int{9, 3, 7, 3, 1, 9, 2}
	want := []int{1, 2, 3, 7, 9}
	got := uniqueBatchDeleteIDs(input)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueBatchDeleteIDs(%v) = %v, want strictly ascending %v", input, got, want)
	}
}

func TestBatchDeleteTokensChunksAboveDatabaseBindLimit(t *testing.T) {
	const tokenCount = 1105

	t.Run("success", func(t *testing.T) {
		truncateTables(t)
		user, _, ids := seedBatchDeleteTokensAboveBindLimit(t, "bind-success", tokenCount)
		enforceBatchDeleteTokenBindLimit(t, 200)

		deleted, err := BatchDeleteTokens(ids, user.Id)
		if err != nil {
			t.Fatalf("BatchDeleteTokens above bind limit: %v", err)
		}
		if deleted != tokenCount {
			t.Fatalf("BatchDeleteTokens deleted = %d, want %d", deleted, tokenCount)
		}
		var liveCount int64
		if err := DB.Model(&Token{}).Where("user_id = ?", user.Id).Count(&liveCount).Error; err != nil {
			t.Fatalf("count live tokens after large batch delete: %v", err)
		}
		if liveCount != 0 {
			t.Fatalf("live tokens after large batch delete = %d, want 0", liveCount)
		}
		var deletedCount int64
		if err := DB.Unscoped().Model(&Token{}).Where("user_id = ?", user.Id).Count(&deletedCount).Error; err != nil {
			t.Fatalf("count soft-deleted tokens after large batch delete: %v", err)
		}
		if deletedCount != tokenCount {
			t.Fatalf("soft-deleted tokens = %d, want %d", deletedCount, tokenCount)
		}
		var ledgerCount int64
		if err := DB.Model(&BatchUpdateDeleteLedger{}).
			Where("kind = ?", BatchUpdateTypeTokenQuota).
			Count(&ledgerCount).Error; err != nil {
			t.Fatalf("count large batch delete ledgers: %v", err)
		}
		if ledgerCount != tokenCount {
			t.Fatalf("large batch delete ledgers = %d, want %d", ledgerCount, tokenCount)
		}
	})

	t.Run("ledger conflict rolls back every chunk", func(t *testing.T) {
		truncateTables(t)
		user, tokens, ids := seedBatchDeleteTokensAboveBindLimit(t, "bind-rollback", tokenCount)
		conflict := tokens[603]
		if err := DB.Create(&BatchUpdateDeleteLedger{
			Kind:      BatchUpdateTypeTokenQuota,
			EntityId:  conflict.Id,
			DeletedAt: 1,
		}).Error; err != nil {
			t.Fatalf("seed large-batch ledger conflict: %v", err)
		}
		enforceBatchDeleteTokenBindLimit(t, 200)

		deleted, err := BatchDeleteTokens(ids, user.Id)
		if err == nil || !strings.Contains(err.Error(), "ledger invariant violated") {
			t.Fatalf("BatchDeleteTokens conflict error = %v, want ledger invariant violation", err)
		}
		if deleted != 0 {
			t.Fatalf("BatchDeleteTokens conflict deleted = %d, want 0", deleted)
		}
		var liveCount int64
		if err := DB.Model(&Token{}).Where("user_id = ?", user.Id).Count(&liveCount).Error; err != nil {
			t.Fatalf("count tokens after rolled-back large delete: %v", err)
		}
		if liveCount != tokenCount {
			t.Fatalf("live tokens after rolled-back large delete = %d, want %d", liveCount, tokenCount)
		}
		var ledgerCount int64
		if err := DB.Model(&BatchUpdateDeleteLedger{}).
			Where("kind = ?", BatchUpdateTypeTokenQuota).
			Count(&ledgerCount).Error; err != nil {
			t.Fatalf("count ledgers after rolled-back large delete: %v", err)
		}
		if ledgerCount != 1 {
			t.Fatalf("ledgers after rolled-back large delete = %d, want only preexisting conflict", ledgerCount)
		}
	})
}

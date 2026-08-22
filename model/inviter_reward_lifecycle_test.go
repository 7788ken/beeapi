package model

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billinglifecycle"
	"gorm.io/gorm"
)

func enableInviterRewardLifecycleForTest(t *testing.T) {
	t.Helper()
	oldEnabled := common.RewardInviterOnEffectiveOnly
	oldQuota := common.QuotaForInviter
	common.RewardInviterOnEffectiveOnly = true
	common.QuotaForInviter = 10
	t.Cleanup(func() {
		common.RewardInviterOnEffectiveOnly = oldEnabled
		common.QuotaForInviter = oldQuota
	})
}

func useInviterRewardCoordinatorForTest(t *testing.T, coordinator *billinglifecycle.Coordinator) {
	t.Helper()
	oldReserve := reserveInviterRewardTicket
	reserveInviterRewardTicket = coordinator.ReserveFromContext
	t.Cleanup(func() {
		reserveInviterRewardTicket = oldReserve
	})
}

func closeInviterRewardCoordinator(t *testing.T, coordinator *billinglifecycle.Coordinator) {
	t.Helper()
	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := coordinator.CloseAdmissionAndWait(ctx, sentinel); err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}
}

func TestUsedQuotaReservesInviterRewardBeforeDatabaseWrite(t *testing.T) {
	setupUserUpdateTestState(t)
	enableInviterRewardLifecycleForTest(t)
	coordinator := billinglifecycle.NewCoordinator()
	useInviterRewardCoordinatorForTest(t, coordinator)

	user := User{Username: "reserve-before-db", Password: "password", UsedQuota: 0}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	reserved := false
	oldReserve := reserveInviterRewardTicket
	reserveInviterRewardTicket = func(ctx context.Context, name string) (*billinglifecycle.Ticket, error) {
		var usedQuota int
		if err := DB.Model(&User{}).Where("id = ?", user.Id).Select("used_quota").Scan(&usedQuota).Error; err != nil {
			t.Fatalf("read used quota during reservation: %v", err)
		}
		if usedQuota != 0 {
			t.Fatalf("used quota before reservation = %d, want 0", usedQuota)
		}
		reserved = true
		return oldReserve(ctx, name)
	}

	if err := updateUserUsedQuotaAndRequestCount(context.Background(), user.Id, 7, 1); err != nil {
		t.Fatalf("update user usage: %v", err)
	}
	if !reserved {
		t.Fatal("inviter reward ticket was not reserved")
	}

	closeInviterRewardCoordinator(t, coordinator)
}

func TestUsedQuotaDatabaseFailureReleasesInviterRewardTicket(t *testing.T) {
	setupUserUpdateTestState(t)
	enableInviterRewardLifecycleForTest(t)
	coordinator := billinglifecycle.NewCoordinator()
	useInviterRewardCoordinatorForTest(t, coordinator)

	user := User{Username: "db-failure-release", Password: "password"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	forcedErr := errors.New("forced used quota update failure")
	callbackName := "test:fail-used-quota-update"
	if err := DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(forcedErr)
		}
	}); err != nil {
		t.Fatalf("register update callback: %v", err)
	}
	t.Cleanup(func() {
		_ = DB.Callback().Update().Remove(callbackName)
	})

	err := updateUserUsedQuotaAndRequestCount(context.Background(), user.Id, 7, 1)
	if !errors.Is(err, forcedErr) {
		t.Fatalf("update user usage error = %v, want forced error", err)
	}

	closeInviterRewardCoordinator(t, coordinator)
}

func TestBatchUsedQuotaUsesExplicitDrainParent(t *testing.T) {
	setupUserUpdateTestState(t)
	enableInviterRewardLifecycleForTest(t)
	coordinator := billinglifecycle.NewCoordinator()
	useInviterRewardCoordinatorForTest(t, coordinator)

	user := User{Username: "batch-drain-child", Password: "password"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}

	update := []BatchUpdate{{Kind: BatchUpdateTypeUsedQuota, ID: user.Id, Delta: 7}}
	if err := applyDefaultBatchUpdates(context.Background(), update); !errors.Is(err, billinglifecycle.ErrAdmissionClosed) {
		t.Fatalf("batch update without parent error = %v, want ErrAdmissionClosed", err)
	}
	var unchanged User
	if err := DB.First(&unchanged, user.Id).Error; err != nil {
		t.Fatalf("reload unchanged user: %v", err)
	}
	if unchanged.UsedQuota != 0 {
		t.Fatalf("used quota after rejected root = %d, want 0", unchanged.UsedQuota)
	}

	ctx := billinglifecycle.ContextWithParent(context.Background(), sentinel)
	rewardStarted := make(chan struct{})
	releaseReward := make(chan struct{})
	oldRunner := runInviterReward
	runInviterReward = func(int) {
		close(rewardStarted)
		<-releaseReward
	}
	t.Cleanup(func() {
		runInviterReward = oldRunner
	})
	if err := applyDefaultBatchUpdates(ctx, update); err != nil {
		t.Fatalf("batch update with drain parent: %v", err)
	}
	select {
	case <-rewardStarted:
	case <-time.After(time.Second):
		t.Fatal("inviter reward child did not start")
	}

	closeReturned := make(chan error, 1)
	closeCtx, cancelClose := context.WithTimeout(context.Background(), time.Second)
	defer cancelClose()
	go func() {
		closeReturned <- coordinator.CloseAdmissionAndWait(closeCtx, sentinel)
	}()
	select {
	case err := <-closeReturned:
		t.Fatalf("CloseAdmissionAndWait() returned while inviter reward child was running: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseReward)
	if err := <-closeReturned; err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}

	var updated User
	if err := DB.First(&updated, user.Id).Error; err != nil {
		t.Fatalf("reload updated user: %v", err)
	}
	if updated.UsedQuota != 7 {
		t.Fatalf("used quota after child batch update = %d, want 7", updated.UsedQuota)
	}
}

func TestDisabledInviterRewardDoesNotReserveTicket(t *testing.T) {
	setupUserUpdateTestState(t)
	oldEnabled := common.RewardInviterOnEffectiveOnly
	oldQuota := common.QuotaForInviter
	common.RewardInviterOnEffectiveOnly = false
	common.QuotaForInviter = 10
	t.Cleanup(func() {
		common.RewardInviterOnEffectiveOnly = oldEnabled
		common.QuotaForInviter = oldQuota
	})

	called := false
	oldReserve := reserveInviterRewardTicket
	reserveInviterRewardTicket = func(context.Context, string) (*billinglifecycle.Ticket, error) {
		called = true
		return nil, errors.New("unexpected reservation")
	}
	t.Cleanup(func() {
		reserveInviterRewardTicket = oldReserve
	})

	user := User{Username: "disabled-reward", Password: "password"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := updateUserUsedQuotaAndRequestCount(context.Background(), user.Id, 7, 1); err != nil {
		t.Fatalf("update user usage: %v", err)
	}
	if called {
		t.Fatal("disabled inviter reward reserved a lifecycle ticket")
	}
}

func TestTrackedProducerParentAdmitsNonBatchUsageDuringDrain(t *testing.T) {
	setupUserUpdateTestState(t)
	enableInviterRewardLifecycleForTest(t)
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})
	if err := DB.Exec("DELETE FROM channels").Error; err != nil {
		t.Fatalf("clear channels: %v", err)
	}
	t.Cleanup(func() {
		_ = DB.Exec("DELETE FROM channels").Error
	})

	coordinator := billinglifecycle.NewCoordinator()
	useInviterRewardCoordinatorForTest(t, coordinator)
	oldRunner := runInviterReward
	rewardRan := make(chan int, 1)
	runInviterReward = func(userID int) {
		rewardRan <- userID
	}
	t.Cleanup(func() {
		runInviterReward = oldRunner
	})

	user := User{Username: "tracked-producer-drain", Password: "password"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	channel := Channel{Name: "tracked-producer-drain"}
	if err := DB.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	producerStarted := make(chan struct{})
	runRound := make(chan struct{})
	roundResult := make(chan error, 1)
	if err := coordinator.StartProducer("task-or-midjourney-polling", func(ctx context.Context, parent *billinglifecycle.Ticket) {
		close(producerStarted)
		<-runRound
		ctx = billinglifecycle.ContextWithParent(ctx, parent)
		ctx = context.WithoutCancel(ctx)
		roundResult <- UpdateUserAndChannelUsedQuotaWithContext(ctx, user.Id, channel.Id, 7)
	}); err != nil {
		t.Fatalf("StartProducer() error = %v", err)
	}
	<-producerStarted

	sentinel, err := coordinator.BeginDrain()
	if err != nil {
		t.Fatalf("BeginDrain() error = %v", err)
	}
	if err := UpdateUserAndChannelUsedQuota(user.Id, channel.Id, 7); !errors.Is(err, billinglifecycle.ErrAdmissionClosed) {
		t.Fatalf("ordinary usage during drain error = %v, want ErrAdmissionClosed", err)
	}

	close(runRound)
	if err := <-roundResult; err != nil {
		t.Fatalf("tracked producer usage during drain: %v", err)
	}
	select {
	case rewardUserID := <-rewardRan:
		if rewardUserID != user.Id {
			t.Fatalf("reward user id = %d, want %d", rewardUserID, user.Id)
		}
	case <-time.After(time.Second):
		t.Fatal("tracked producer inviter reward did not run")
	}

	closeCtx, cancelClose := context.WithTimeout(context.Background(), time.Second)
	defer cancelClose()
	if err := coordinator.CloseAdmissionAndWait(closeCtx, sentinel); err != nil {
		t.Fatalf("CloseAdmissionAndWait() error = %v", err)
	}

	var updatedUser User
	if err := DB.First(&updatedUser, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if updatedUser.UsedQuota != 7 || updatedUser.RequestCount != 1 {
		t.Fatalf("user usage = (%d, %d), want (7, 1)", updatedUser.UsedQuota, updatedUser.RequestCount)
	}
	var updatedChannel Channel
	if err := DB.First(&updatedChannel, channel.Id).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if updatedChannel.UsedQuota != 7 {
		t.Fatalf("channel used quota = %d, want 7", updatedChannel.UsedQuota)
	}
}

func TestMissingUserUsageReleasesTicketWithoutReward(t *testing.T) {
	setupUserUpdateTestState(t)
	enableInviterRewardLifecycleForTest(t)
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})

	coordinator := billinglifecycle.NewCoordinator()
	useInviterRewardCoordinatorForTest(t, coordinator)
	oldRunner := runInviterReward
	rewardRan := make(chan struct{}, 1)
	runInviterReward = func(int) {
		rewardRan <- struct{}{}
	}
	t.Cleanup(func() {
		runInviterReward = oldRunner
	})

	err := UpdateUserUsedQuotaAndRequestCount(999999, 7)
	wantError := "update user id 999999 usage affected 0 rows, want 1"
	if err == nil || err.Error() != wantError {
		t.Fatalf("missing user usage update error = %v, want %q", err, wantError)
	}
	select {
	case <-rewardRan:
		t.Fatal("missing user usage update submitted inviter reward")
	default:
	}

	closeInviterRewardCoordinator(t, coordinator)
}

package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupIQLeaseRegressionDB targets SQLite by default; setting IQ_TEST_MYSQL_DSN
// reruns the regression against MySQL, whose driver reports changed rows
// instead of matched rows — the exact dialect difference behind the bug.
func setupIQLeaseRegressionDB(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("IQ_TEST_MYSQL_DSN")
	if dsn == "" {
		setupIQRuntimeDB(t)
		return
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	tables := []string{"iq_test_actions", "iq_test_controls", "iq_test_leases", "iq_test_models", "iq_test_notices", "iq_test_results", "iq_test_runs", "abilities", "channels", "options"}
	for _, table := range tables {
		require.NoError(t, db.Exec("DROP TABLE IF EXISTS "+table).Error)
	}
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous })
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.Ability{}, &model.IQTestControl{}, &model.IQTestAction{}, &model.IQTestNotice{}, &model.IQTestModel{}, &model.IQTestRun{}, &model.IQTestLease{}, &model.IQTestResult{}))
}

func TestIQLeaseRenewalToleratesIdenticalSameSecondValue(t *testing.T) {
	setupIQLeaseRegressionDB(t)
	owner := "regression-owner"
	require.NoError(t, model.DB.Create(&model.IQTestLease{Key: "scheduler", OwnerID: owner, ExpiresAt: time.Now().Add(time.Hour).Unix()}).Error)
	for attempt := 0; attempt < 50; attempt++ {
		now := time.Now().Unix()
		require.NoError(t, model.DB.Model(&model.IQTestLease{}).Where(map[string]any{"key": "scheduler", "owner_id": owner}).
			Update("expires_at", now+iqLeaseSeconds).Error)
		ok, err := model.RenewIQTestLease(owner, iqLeaseSeconds)
		require.NoError(t, err)
		if time.Now().Unix() != now {
			continue // crossed a second boundary; retry to hit the same-second window
		}
		require.True(t, ok, "renewal must succeed when a same-second writer already stored the identical expiry")
		var lease model.IQTestLease
		require.NoError(t, model.DB.Where(map[string]any{"key": "scheduler"}).First(&lease).Error)
		require.GreaterOrEqual(t, lease.ExpiresAt, now+iqLeaseSeconds)
		ok, err = model.RenewIQTestLease("another-owner", iqLeaseSeconds)
		require.NoError(t, err)
		require.False(t, ok)
		return
	}
	t.Fatal("could not hit the same-second renewal window")
}

// TestIQEnforcementSurvivesBackToBackSameSecondRenewals mirrors the production
// failure: channel N's transaction renews the lease, and channel N+1's
// transaction runs in the same second and writes the identical expiry. A
// RowsAffected-based ownership assertion reports 0 changed rows on MySQL and
// aborts the whole run after every probe already succeeded.
func TestIQEnforcementSurvivesBackToBackSameSecondRenewals(t *testing.T) {
	setupIQLeaseRegressionDB(t)
	seedIQRuntime(t)
	priority := int64(100)
	for _, id := range []int{1, 2} {
		if id != 1 {
			require.NoError(t, model.DB.Create(&model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: "test-model", Name: "back-to-back", Key: "local"}).Error)
		}
		require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", id).Update("priority", 100).Error)
		require.NoError(t, model.DB.Create(&model.Ability{ChannelId: id, Model: "test-model", Group: "default", Enabled: true, Priority: &priority}).Error)
	}
	run, created, err := PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, 0, "", nil)
	require.NoError(t, err)
	require.True(t, created)
	var snapshot IQRunSnapshot
	require.NoError(t, common.Unmarshal([]byte(run.ConfigSnapshot), &snapshot))
	score, margin := 100, 30
	for _, id := range []int{1, 2} {
		require.NoError(t, model.DB.Create(&model.IQTestResult{RunID: run.RunID, ChannelID: id, RequestedModel: "test-model", Status: "success", Score: &score, Margin: &margin, BaselineScoreSnapshot: 70, BankVersion: IQBankVersion(), CreatedAt: time.Now().Unix()}).Error)
	}
	require.NoError(t, EnforceIQTestRun(context.Background(), run.RunID, snapshot.Setting))
	var controls int64
	require.NoError(t, model.DB.Model(&model.IQTestControl{}).Count(&controls).Error)
	require.EqualValues(t, 2, controls, "both channels must land their control rows when transactions renew the lease back-to-back")
	require.NoError(t, model.ReleaseIQTestLease(run.OwnerID))
}

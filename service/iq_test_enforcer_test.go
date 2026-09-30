package service

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func iqSeedEnforcer(t *testing.T) {
	setupIQRuntimeDB(t)
	seedIQRuntime(t)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", 1).Update("priority", 100).Error)
	priority := int64(100)
	require.NoError(t, model.DB.Create(&model.Ability{ChannelId: 1, Model: "test-model", Group: "default", Enabled: true, Priority: &priority}).Error)
	cfg, err := GetIQTestSetting()
	require.NoError(t, err)
	cfg.NotifyOnAction = false
	_, err = SaveIQTestSetting(cfg, cfg.Version)
	require.NoError(t, err)
}

func iqEnforcerRound(t *testing.T, score int) {
	t.Helper()
	run, created, err := PrepareIQTestRun("run_now", 1, 1, "", nil)
	require.NoError(t, err)
	require.True(t, created)
	var snapshot IQRunSnapshot
	require.NoError(t, common.Unmarshal([]byte(run.ConfigSnapshot), &snapshot))
	margin := score - 70
	require.NoError(t, model.DB.Create(&model.IQTestResult{RunID: run.RunID, ChannelID: 1, RequestedModel: "test-model", Status: "success", Score: &score, Margin: &margin, BaselineScoreSnapshot: 70, BankVersion: IQBankVersion(), CreatedAt: time.Now().Unix()}).Error)
	require.NoError(t, EnforceIQTestRun(context.Background(), run.RunID, snapshot.Setting))
	require.NoError(t, model.FinalizeIQTestRun(run.RunID, "finished", 1, 1, 0, 0, time.Now().Unix()))
	require.NoError(t, model.ReleaseIQTestLease(run.OwnerID))
}

func TestIQPriorityDownRecoveryAndManualOwnership(t *testing.T) {
	iqSeedEnforcer(t)
	iqEnforcerRound(t, 50)
	ch, err := model.GetChannelById(1, false)
	require.NoError(t, err)
	require.EqualValues(t, 100, ch.GetPriority())
	iqEnforcerRound(t, 50)
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.EqualValues(t, 90, ch.GetPriority())
	require.EqualValues(t, 100, *ch.IQBaselinePriority)
	for i := 0; i < 8; i++ {
		iqEnforcerRound(t, 50)
	}
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.EqualValues(t, 50, ch.GetPriority())
	for i := 0; i < 6; i++ {
		iqEnforcerRound(t, 100)
	}
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.EqualValues(t, 100, ch.GetPriority())
	require.Nil(t, ch.IQBaselinePriority)
	iqEnforcerRound(t, 50)
	iqEnforcerRound(t, 50)
	require.NoError(t, model.UpdateChannelHealthFields(1, map[string]any{"priority": int64(150)}))
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.Nil(t, ch.IQBaselinePriority)
	iqEnforcerRound(t, 100)
	iqEnforcerRound(t, 100)
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.EqualValues(t, 150, ch.GetPriority())
	var ability model.Ability
	require.NoError(t, model.DB.First(&ability, "channel_id = ?", 1).Error)
	require.True(t, ability.Enabled)
}

func TestIQDisableRecoveryAndConfigurationRelease(t *testing.T) {
	iqSeedEnforcer(t)
	cfg, err := GetIQTestSetting()
	require.NoError(t, err)
	cfg.DisableBelowBaseline = true
	_, err = SaveIQTestSetting(cfg, cfg.Version)
	require.NoError(t, err)
	iqEnforcerRound(t, 50)
	iqEnforcerRound(t, 50)
	ch, err := model.GetChannelById(1, false)
	require.NoError(t, err)
	require.True(t, ch.IQDisabled)
	require.Equal(t, common.ChannelStatusAutoDisabled, ch.Status)
	var ability model.Ability
	require.NoError(t, model.DB.First(&ability, "channel_id = ?", 1).Error)
	require.False(t, ability.Enabled)
	// Health recovery cannot clear an IQ disable.
	EnableChannel(1, "", "test")
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.True(t, ch.IQDisabled)
	iqEnforcerRound(t, 100)
	iqEnforcerRound(t, 100)
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.False(t, ch.IQDisabled)
	require.Equal(t, common.ChannelStatusEnabled, ch.Status)
	iqEnforcerRound(t, 50)
	iqEnforcerRound(t, 50)
	cfg, err = GetIQTestSetting()
	require.NoError(t, err)
	cfg.Enabled = false
	_, err = SaveIQTestSetting(cfg, cfg.Version)
	require.NoError(t, err)
	require.NoError(t, ReconcileIQTestState(context.Background()))
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.False(t, ch.IQDisabled)
	require.Equal(t, common.ChannelStatusEnabled, ch.Status)
}

func TestIQEnforcementRejectsChangedChannelAndIsIdempotent(t *testing.T) {
	iqSeedEnforcer(t)
	iqEnforcerRound(t, 50)
	run, _, err := PrepareIQTestRun("run_now", 1, 1, "", nil)
	require.NoError(t, err)
	var snapshot IQRunSnapshot
	require.NoError(t, common.Unmarshal([]byte(run.ConfigSnapshot), &snapshot))
	score, margin := 50, -20
	row := model.IQTestResult{RunID: run.RunID, ChannelID: 1, RequestedModel: "test-model", Status: "success", Score: &score, Margin: &margin, BaselineScoreSnapshot: 70}
	require.NoError(t, model.DB.Create(&row).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", 1).Update("priority", 222).Error)
	require.NoError(t, EnforceIQTestRun(context.Background(), run.RunID, snapshot.Setting))
	require.NoError(t, model.DB.First(&row, row.Id).Error)
	require.Equal(t, "skipped_conflict", row.Action)
	ch, err := model.GetChannelById(1, false)
	require.NoError(t, err)
	require.EqualValues(t, 222, ch.GetPriority())
	require.NoError(t, model.ReleaseIQTestLease(run.OwnerID))
}

func TestIQChannelWriteHooksPreserveMetricUpdates(t *testing.T) {
	iqSeedEnforcer(t)
	iqEnforcerRound(t, 50)
	iqEnforcerRound(t, 50)
	ch, err := model.GetChannelById(1, false)
	require.NoError(t, err)
	revision := ch.IQRevision
	ch.UpdateResponseTime(123)
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.Equal(t, revision, ch.IQRevision)
	require.NotNil(t, ch.IQBaselinePriority)
	tag := "iq-test"
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", 1).Update("tag", tag).Error)
	p := int64(321)
	require.NoError(t, model.EditChannelByTag(tag, nil, nil, nil, nil, &p, nil, nil, nil))
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.Equal(t, revision+1, ch.IQRevision)
	require.Nil(t, ch.IQBaselinePriority)
	require.NoError(t, model.DisableChannelByTag(tag))
	ch, err = model.GetChannelById(1, false)
	require.NoError(t, err)
	require.False(t, ch.IQDisabled)
	require.Equal(t, common.ChannelStatusManuallyDisabled, ch.Status)
}

func TestIQPriorityArithmeticBounds(t *testing.T) {
	for _, test := range []struct{ current, target, step, want int64 }{{math.MinInt64, 0, 10, math.MinInt64 + 10}, {math.MaxInt64, math.MaxInt64, 100, math.MaxInt64}, {0, -50, 10, -10}, {-1, 0, 10, 0}} {
		t.Run(fmt.Sprint(test.current), func(t *testing.T) { require.Equal(t, test.want, iqPriorityStep(test.current, test.target, test.step)) })
	}
	require.EqualValues(t, math.MinInt64, iqFloor(math.MinInt64+2, 10))
}

package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestIQCoveragePreviewSeparatesEligibleFromSkipped(t *testing.T) {
	setupIQRuntimeDB(t)
	seedIQRuntime(t)
	base := "https://upstream.invalid"
	require.NoError(t, model.DB.Create(&model.Channel{Id: 10, Type: constant.ChannelTypeAnthropic, Status: common.ChannelStatusEnabled, Models: "test-model", Name: "claude", Key: "sk-ant", BaseURL: &base}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 11, Type: constant.ChannelTypeGemini, Status: common.ChannelStatusEnabled, Models: "test-model", Name: "gemini", Key: "g", BaseURL: &base}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 12, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusManuallyDisabled, Models: "test-model", Name: "off", Key: "k", BaseURL: &base}).Error)

	preview, err := PreviewIQTestCoverage(0, nil)
	require.NoError(t, err)
	require.True(t, preview.Enabled)
	require.Equal(t, 2, preview.EligibleChannels, "openai plus anthropic channels are reachable")
	require.Equal(t, 2, preview.EligiblePairs)
	require.Equal(t, 2, preview.SkippedTotal)
	reasons := map[int]string{}
	for _, skip := range preview.Skipped {
		reasons[skip.ChannelID] = skip.Reason
	}
	require.Equal(t, "protocol_unsupported", reasons[11])
	require.Equal(t, "channel_disabled", reasons[12])
}

func TestIQCoveragePreviewHonoursModelSelection(t *testing.T) {
	setupIQRuntimeDB(t)
	seedIQRuntime(t)
	require.NoError(t, model.CreateIQTestModel(&model.IQTestModel{ModelName: "second-model", BaselineScore: 60, Enabled: true}))

	preview, err := PreviewIQTestCoverage(0, []string{"second-model"})
	require.NoError(t, err)
	require.Equal(t, []string{"second-model"}, preview.Models)
	require.Zero(t, preview.EligibleChannels, "the seeded channel only serves test-model")

	_, err = PreviewIQTestCoverage(0, []string{"not-configured"})
	require.Error(t, err)
}

func TestIQRunNowAppliesModelSelectionToAdmission(t *testing.T) {
	setupIQRuntimeDB(t)
	seedIQRuntime(t)
	require.NoError(t, model.CreateIQTestModel(&model.IQTestModel{ModelName: "second-model", BaselineScore: 60, Enabled: true}))
	_, _, err := PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, 0, "filter", []string{"not-configured"})
	require.Error(t, err)
	run, created, err := PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, 0, "scoped", []string{"test-model"})
	require.NoError(t, err)
	require.True(t, created)
	var snapshot IQRunSnapshot
	require.NoError(t, common.Unmarshal([]byte(run.ConfigSnapshot), &snapshot))
	require.Len(t, snapshot.Models, 1)
	require.Equal(t, "test-model", snapshot.Models[0].ModelName)
}

func TestIQReportOnlyModeScoresButNeverTouchesRouting(t *testing.T) {
	setupIQRuntimeDB(t)
	seedIQRuntime(t)
	base := "https://upstream.invalid"
	priority := int64(100)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 20, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Models: "test-model", Name: "weak", Key: "k", BaseURL: &base, Priority: &priority}).Error)
	require.NoError(t, model.DB.Create(&model.IQTestControl{ChannelID: 20, Revision: 0, ModelName: "test-model", ModelVersion: 1,
		BankVersion: IQBankVersion(), FailStreak: 1, PassStreak: 0}).Error)

	for _, mode := range []string{IQEnforcementEnforce, IQEnforcementReportOnly} {
		setting, err := GetIQTestSetting()
		require.NoError(t, err)
		setting.EnforcementMode = mode
		_, err = SaveIQTestSetting(setting, setting.Version)
		require.NoError(t, err)

		require.NoError(t, model.DB.Create(&model.IQTestResult{RunID: "run-" + mode, ChannelID: 20, RequestedModel: "test-model",
			Status: model.IQTestResultStatusSuccess, Score: iqPointer(0), Margin: iqPointer(-70), BaselineScoreSnapshot: 70,
			TotalQuestions: 8, BankVersion: IQBankVersion(), CreatedAt: time.Now().Unix()}).Error)
		require.NoError(t, model.DB.Create(&model.IQTestLease{Key: "scheduler", OwnerID: "owner-" + mode, ExpiresAt: time.Now().Add(time.Minute).Unix()}).Error)
		run := model.NewIQTestRun("run-"+mode, model.IQTestRunTriggerRunNow, "owner-"+mode, 1, mustIQSnapshot(t, mode), IQBankVersion())
		require.NoError(t, model.CreateIQTestRun(run))
		require.NoError(t, EnforceIQTestRun(context.Background(), run.RunID, mustIQSetting(t)))

		var ch model.Channel
		require.NoError(t, model.DB.First(&ch, 20).Error)
		if mode == IQEnforcementEnforce {
			require.Less(t, ch.GetPriority(), int64(100), "enforce mode demotes a below-baseline channel")
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", 20).Updates(map[string]any{"priority": int64(100), "iq_baseline_priority": nil, "iq_applied_priority": nil}).Error)
			require.NoError(t, model.DB.Where("channel_id = ?", 20).Delete(&model.IQTestControl{}).Error)
			require.NoError(t, model.DB.Where("channel_id = ?", 20).Delete(&model.IQTestAction{}).Error)
			require.NoError(t, model.DB.Where("owner_id = ?", "owner-"+mode).Delete(&model.IQTestLease{}).Error)
			continue
		}
		require.EqualValues(t, 100, ch.GetPriority(), "report only must never change routing")
		var controls, actions int64
		require.NoError(t, model.DB.Model(&model.IQTestControl{}).Where("channel_id = ?", 20).Count(&controls).Error)
		require.NoError(t, model.DB.Model(&model.IQTestAction{}).Where("channel_id = ?", 20).Count(&actions).Error)
		require.Zero(t, controls, "report only must not write control rows")
		require.Zero(t, actions, "report only must not write action rows")
	}
}

func mustIQSnapshot(t *testing.T, mode string) string {
	t.Helper()
	setting := mustIQSetting(t)
	setting.EnforcementMode = mode
	models, err := model.ListIQTestModels(true)
	require.NoError(t, err)
	encoded, err := common.Marshal(IQRunSnapshot{Setting: setting, Models: models, ChannelIDs: []int{20}, ChannelRevisions: map[int]int64{20: 0}})
	require.NoError(t, err)
	return string(encoded)
}

func mustIQSetting(t *testing.T) IQTestSetting {
	t.Helper()
	setting, err := GetIQTestSetting()
	require.NoError(t, err)
	return setting
}

func TestIQSettingRejectsUnknownEnforcementMode(t *testing.T) {
	setting := DefaultIQTestSetting()
	require.True(t, setting.Enforces())
	setting.EnforcementMode = IQEnforcementReportOnly
	require.NoError(t, setting.Validate())
	require.False(t, setting.Enforces())
	setting.EnforcementMode = "whatever"
	require.Error(t, setting.Validate())
	// An unset mode is the historical enforcing behaviour, not an error: stored
	// configs and run snapshots written before the field existed unmarshal to "".
	setting.EnforcementMode = ""
	require.NoError(t, setting.Validate())
	require.True(t, setting.Enforces())
}

func TestIQLastScheduledRunAnchorsCadence(t *testing.T) {
	setupIQRuntimeDB(t)
	last, err := model.LastScheduledIQTestRunAt()
	require.NoError(t, err)
	require.Zero(t, last)
	run := model.NewIQTestRun("sched-1", model.IQTestRunTriggerSchedule, "owner", 0, "{}", IQBankVersion())
	run.StartedAt = time.Now().Add(-90 * time.Second).Unix()
	require.NoError(t, model.CreateIQTestRun(run))
	manual := model.NewIQTestRun("manual-1", model.IQTestRunTriggerRunNow, "owner", 1, "{}", IQBankVersion())
	manual.StartedAt = time.Now().Unix()
	require.NoError(t, model.CreateIQTestRun(manual))
	last, err = model.LastScheduledIQTestRunAt()
	require.NoError(t, err)
	require.Equal(t, run.StartedAt, last, "a manual round must not postpone the schedule")
}

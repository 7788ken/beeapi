package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupIQRuntimeDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "iq.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous; require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.Ability{}, &model.IQTestControl{}, &model.IQTestAction{}, &model.IQTestNotice{}, &model.IQTestModel{}, &model.IQTestRun{}, &model.IQTestLease{}, &model.IQTestResult{}))
}

func seedIQRuntime(t *testing.T) {
	t.Helper()
	setting := DefaultIQTestSetting()
	setting.Enabled = true
	_, err := SaveIQTestSetting(setting, 0)
	require.NoError(t, err)
	require.NoError(t, model.CreateIQTestModel(&model.IQTestModel{ModelName: "test-model", BaselineScore: 70, Enabled: true}))
	require.NoError(t, model.DB.Create(&model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: "test-model", Name: "local test", Key: "local"}).Error)
}

func TestIQSettingAtomicVersionAndValidation(t *testing.T) {
	setupIQRuntimeDB(t)
	setting, err := GetIQTestSetting()
	require.NoError(t, err)
	require.False(t, setting.Enabled)
	setting.Enabled = true
	saved, err := SaveIQTestSetting(setting, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, saved.Version)
	_, err = SaveIQTestSetting(setting, 0)
	require.ErrorIs(t, err, ErrIQVersionConflict)
	saved.QuestionsPerRound = 0
	_, err = SaveIQTestSetting(saved, 1)
	require.Error(t, err)
	current, err := GetIQTestSetting()
	require.NoError(t, err)
	require.Equal(t, 8, current.QuestionsPerRound)
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 10; j++ {
				_, err := GetIQTestSetting()
				require.NoError(t, err)
			}
		}()
	}
	for i := 0; i < 10; i++ {
		current.Enabled = !current.Enabled
		current, err = SaveIQTestSetting(current, current.Version)
		require.NoError(t, err)
	}
	readers.Wait()
}

func TestIQModelTypedJSONPreservesZeroFalseAndVersion(t *testing.T) {
	setupIQRuntimeDB(t)
	var input IQTestModelInput
	require.NoError(t, common.Unmarshal([]byte(`{"model_name":"test-model","baseline_score":0,"enabled":false}`), &input))
	created, err := CreateIQTestModelInput(input)
	require.NoError(t, err)
	stored, err := model.GetIQTestModel(created.Id)
	require.NoError(t, err)
	require.Equal(t, 0, stored.BaselineScore)
	require.False(t, stored.Enabled)
	require.NoError(t, common.Unmarshal([]byte(`{"baseline_score":80,"version":1}`), &input))
	require.NoError(t, UpdateIQTestModel(created.Id, input))
	stored, err = model.GetIQTestModel(created.Id)
	require.NoError(t, err)
	require.Equal(t, 80, stored.BaselineScore)
	require.EqualValues(t, 2, stored.Version)
	require.ErrorIs(t, UpdateIQTestModel(created.Id, input), ErrIQVersionConflict)
	require.ErrorIs(t, DeleteIQTestModel(created.Id, 1), ErrIQVersionConflict)
	require.NoError(t, DeleteIQTestModel(created.Id, 2))
	_, err = model.GetIQTestModel(created.Id)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestIQCandidatesCoverEverySupportedProtocol(t *testing.T) {
	setupIQRuntimeDB(t)
	seedIQRuntime(t)
	base := "https://upstream.invalid"
	require.NoError(t, model.DB.Create(&model.Channel{Id: 10, Type: constant.ChannelTypeAnthropic, Status: common.ChannelStatusEnabled, Models: "test-model", Name: "claude channel", Key: "sk-ant", BaseURL: &base}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 11, Type: constant.ChannelTypeGemini, Status: common.ChannelStatusEnabled, Models: "test-model", Name: "gemini channel", Key: "g", BaseURL: &base}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 12, Type: constant.ChannelTypeAnthropic, Status: common.ChannelStatusEnabled, Models: "other-model", Name: "no match", Key: "sk-ant", BaseURL: &base}).Error)
	models, err := model.ListIQTestModels(true)
	require.NoError(t, err)
	channels, pairs, err := iqCandidates(0, models)
	require.NoError(t, err)
	ids := make([]int, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.Id)
	}
	require.Equal(t, []int{1, 10}, ids, "OpenAI and Anthropic channels with the model are eligible; other protocols are not")
	require.Equal(t, 2, pairs)
}

func TestIQAdmissionIdempotencyAndGlobalLease(t *testing.T) {
	setupIQRuntimeDB(t)
	seedIQRuntime(t)
	run, created, err := PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, 1, "same-key", nil)
	require.NoError(t, err)
	require.True(t, created)
	repeated, created, err := PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, 1, "same-key", nil)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, run.RunID, repeated.RunID)
	active, created, err := PrepareIQTestRun(model.IQTestRunTriggerSchedule, 0, 0, "", nil)
	require.ErrorIs(t, err, ErrIQBusy)
	require.False(t, created)
	require.Equal(t, run.RunID, active.RunID)
	ok, err := model.ClaimIQTestLease(run.OwnerID, iqLeaseSeconds)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = model.RenewIQTestLease("another-instance", iqLeaseSeconds)
	require.NoError(t, err)
	require.False(t, ok)
	_, _, err = PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, -1, "", nil)
	require.Error(t, err)
	_, _, err = PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, 99, "", nil)
	require.Error(t, err)
}

func TestIQExecutionFailureCancellationAndDisableAreTerminal(t *testing.T) {
	for _, scenario := range []string{"corrupt_snapshot", "cancel", "disable"} {
		t.Run(scenario, func(t *testing.T) {
			setupIQRuntimeDB(t)
			seedIQRuntime(t)
			run, _, err := PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, 1, "key", nil)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected := model.IQTestRunStatusAborted
			switch scenario {
			case "corrupt_snapshot":
				require.NoError(t, model.UpdateIQTestRun(run.RunID, map[string]any{"config_snapshot": "{"}))
				expected = model.IQTestRunStatusFailed
			case "cancel":
				cancel()
			case "disable":
				setting, err := GetIQTestSetting()
				require.NoError(t, err)
				setting.Enabled = false
				_, err = SaveIQTestSetting(setting, setting.Version)
				require.NoError(t, err)
			}
			require.Error(t, ExecuteIQTestRun(ctx, run.RunID, 1))
			stored, err := model.GetIQTestRun(run.RunID)
			require.NoError(t, err)
			require.Equal(t, expected, stored.Status)
			require.Positive(t, stored.FinishedAt)
			owned, err := model.OwnsIQTestLease(run.OwnerID)
			require.NoError(t, err)
			require.False(t, owned)
		})
	}
}

func TestIQExpiredExecutorIsAbortedBeforeNextRun(t *testing.T) {
	setupIQRuntimeDB(t)
	seedIQRuntime(t)
	old, _, err := PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, 1, "old", nil)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.IQTestLease{}).Where("owner_id = ?", old.OwnerID).Update("expires_at", time.Now().Unix()-1).Error)
	require.NoError(t, model.UpdateIQTestRun(old.RunID, map[string]any{"lease_expires_at": time.Now().Unix() - 1}))
	next, created, err := PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, 1, "next", nil)
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, old.OwnerID, next.OwnerID)
	stored, err := model.GetIQTestRun(old.RunID)
	require.NoError(t, err)
	require.Equal(t, model.IQTestRunStatusAborted, stored.Status)
	ok, err := model.RenewIQTestLease(old.OwnerID, iqLeaseSeconds)
	require.NoError(t, err)
	require.False(t, ok)
	require.True(t, errors.Is(iqCheckRunPermission(context.Background(), old.OwnerID), errIQLeaseLost))
}

func TestIQExecutorUsesAdmissionSnapshotAndChannelConcurrency(t *testing.T) {
	setupIQRuntimeDB(t)
	seedIQRuntime(t)
	var requests, active, peak atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		concurrent := active.Add(1)
		for prior := peak.Load(); concurrent > prior && !peak.CompareAndSwap(prior, concurrent); prior = peak.Load() {
		}
		defer active.Add(-1)
		time.Sleep(15 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"391"}}]}`))
	}))
	defer server.Close()
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", 1).Update("base_url", server.URL).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 2, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: "test-model", Name: "second local test", Key: "local", BaseURL: &server.URL}).Error)
	setting, err := GetIQTestSetting()
	require.NoError(t, err)
	setting.QuestionsPerRound, setting.Concurrency = 4, 2
	setting, err = SaveIQTestSetting(setting, setting.Version)
	require.NoError(t, err)
	run, _, err := PrepareIQTestRun(model.IQTestRunTriggerRunNow, 1, 0, "snapshot", nil)
	require.NoError(t, err)
	setting.QuestionsPerRound, setting.Concurrency = 8, 1
	_, err = SaveIQTestSetting(setting, setting.Version)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.IQTestModel{}).Where("id = ?", 1).Update("baseline_score", 99).Error)
	require.NoError(t, ExecuteIQTestRun(context.Background(), run.RunID, 0))
	require.EqualValues(t, 20, requests.Load(), "8 scoring questions plus 6 preference samples per channel")
	require.EqualValues(t, 2, peak.Load())
	var rows []model.IQTestResult
	require.NoError(t, model.DB.Find(&rows).Error)
	require.Len(t, rows, 2)
	for _, result := range rows {
		require.Equal(t, 4, result.TotalQuestions)
		require.Equal(t, 70, result.BaselineScoreSnapshot)
	}
	stored, err := model.GetIQTestRun(run.RunID)
	require.NoError(t, err)
	require.Equal(t, model.IQTestRunStatusFinished, stored.Status)
	require.Equal(t, 2, stored.SuccessCount)
}

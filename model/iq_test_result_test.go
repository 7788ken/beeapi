package model

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupIQResultDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "iq-results.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous; require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&IQTestRun{}, &IQTestResult{}))
}

func createIQResultRun(t *testing.T, name, status, bank string, at int64) *IQTestRun {
	t.Helper()
	run := &IQTestRun{RunID: name, Status: status, BankVersion: bank, CreatedAt: at, StartedAt: at, FinishedAt: at}
	if status == IQTestRunStatusRunning {
		run.FinishedAt = 0
	}
	require.NoError(t, CreateIQTestRun(run))
	return run
}

func createIQScore(t *testing.T, run string, channel int, name string, score, baseline, count int) *IQTestResult {
	t.Helper()
	margin := score - baseline
	result := &IQTestResult{RunID: run, ChannelID: channel, RequestedModel: name, Status: IQTestResultStatusSuccess,
		Score: &score, Margin: &margin, BaselineScoreSnapshot: baseline, TotalQuestions: count}
	require.NoError(t, CreateIQTestResult(result))
	return result
}

func TestIQScoreIgnoresUnfinishedRuns(t *testing.T) {
	setupIQResultDB(t)
	createIQResultRun(t, "done", IQTestRunStatusFinished, "v1", 10)
	createIQResultRun(t, "inflight", IQTestRunStatusRunning, "v1", 20)
	createIQScore(t, "done", 1, "a", 75, 70, 8)
	createIQScore(t, "inflight", 1, "a", 95, 70, 8)
	scores, err := GetLatestIQChannelScores([]int{1})
	require.NoError(t, err)
	require.Equal(t, 75, *scores[1].Score)
	require.Nil(t, scores[1].Previous)
	require.EqualValues(t, 10, scores[1].At)
}

func TestIQScoreComparesOnlyMatchingDecisionBasis(t *testing.T) {
	for _, changed := range []string{"none", "model", "baseline", "bank", "questions"} {
		t.Run(changed, func(t *testing.T) {
			setupIQResultDB(t)
			createIQResultRun(t, "old", IQTestRunStatusFinished, "v1", 10)
			createIQScore(t, "old", 1, "a", 75, 70, 8)
			createIQScore(t, "old", 1, "b", 90, 80, 8)
			bank, baseline, count := "v1", 70, 8
			if changed == "bank" {
				bank = "v2"
			}
			if changed == "baseline" {
				baseline = 72
			}
			if changed == "questions" {
				count = 9
			}
			createIQResultRun(t, "new", IQTestRunStatusPartial, bank, 20)
			if changed == "model" {
				createIQScore(t, "new", 1, "a", 90, baseline, count)
				createIQScore(t, "new", 1, "b", 85, 80, count)
			} else {
				createIQScore(t, "new", 1, "a", 80, baseline, count)
				createIQScore(t, "new", 1, "b", 100, 80, count)
			}
			scores, err := GetLatestIQChannelScores([]int{1})
			require.NoError(t, err)
			if changed == "none" {
				require.Equal(t, 75, *scores[1].Previous)
			} else {
				require.Nil(t, scores[1].Previous)
			}
			if changed == "model" {
				require.Equal(t, "b", scores[1].Model)
				require.Equal(t, 85, *scores[1].Score)
			}
		})
	}
}

func TestIQScoreStableRunAndMarginOrdering(t *testing.T) {
	setupIQResultDB(t)
	createIQResultRun(t, "old", IQTestRunStatusFinished, "v1", 10)
	createIQResultRun(t, "new", IQTestRunStatusFinished, "v1", 10)
	createIQScore(t, "old", 1, "a", 70, 70, 8)
	createIQScore(t, "new", 1, "b", 90, 80, 8)
	createIQScore(t, "new", 1, "a", 80, 70, 8)
	scores, err := GetLatestIQChannelScores([]int{1})
	require.NoError(t, err)
	require.Equal(t, "a", scores[1].Model)
	require.Equal(t, 80, *scores[1].Score)
	require.Equal(t, 70, *scores[1].Previous)
}

func TestIQScoreHistoryIsBoundedPerChannel(t *testing.T) {
	setupIQResultDB(t)
	createIQResultRun(t, "quiet", IQTestRunStatusFinished, "v1", 1)
	createIQScore(t, "quiet", 2, "a", 75, 70, 8)
	for i := 0; i < 401; i++ {
		run := fmt.Sprintf("busy-%d", i)
		createIQResultRun(t, run, IQTestRunStatusFinished, "v1", int64(i+2))
		createIQScore(t, run, 1, "a", 90, 70, 8)
	}
	scores, err := GetLatestIQChannelScores([]int{1, 2})
	require.NoError(t, err)
	require.Equal(t, 75, *scores[2].Score)
	require.Equal(t, 90, *scores[1].Score)
	require.Equal(t, 90, *scores[1].Previous)
}

func TestIQScoreLatestErrorIsVisibleWithoutZero(t *testing.T) {
	setupIQResultDB(t)
	createIQResultRun(t, "old", IQTestRunStatusFinished, "v1", 10)
	createIQScore(t, "old", 1, "a", 75, 70, 8)
	createIQResultRun(t, "error", IQTestRunStatusPartial, "v1", 20)
	for _, channel := range []int{1, 2} {
		require.NoError(t, CreateIQTestResult(&IQTestResult{RunID: "error", ChannelID: channel, RequestedModel: "a", BaselineScoreSnapshot: 70, Status: IQTestResultStatusError, ErrorClass: "auth_401_403"}))
	}
	scores, err := GetLatestIQChannelScores([]int{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, 75, *scores[1].Score)
	require.Nil(t, scores[1].Previous)
	require.Equal(t, "error", scores[1].Status)
	require.Equal(t, "auth_401_403", scores[1].ErrorClass)
	require.EqualValues(t, 20, scores[1].AttemptAt)
	require.Nil(t, scores[2].Score)
	require.Equal(t, "error", scores[2].Status)
	_, exists := scores[3]
	require.False(t, exists)
}

func TestIQScoreMixedModelFailureRetainsSuccessfulDecision(t *testing.T) {
	setupIQResultDB(t)
	createIQResultRun(t, "mixed", IQTestRunStatusPartial, "v1", 10)
	require.NoError(t, CreateIQTestResult(&IQTestResult{RunID: "mixed", ChannelID: 1, RequestedModel: "a", Status: IQTestResultStatusError, ErrorClass: "auth_401_403"}))
	createIQScore(t, "mixed", 1, "b", 0, 70, 8)
	scores, err := GetLatestIQChannelScores([]int{1})
	require.NoError(t, err)
	require.Equal(t, 0, *scores[1].Score)
	require.Equal(t, "success", scores[1].Status)
	require.Equal(t, "b", scores[1].Model)
}

func TestIQScoreAbortedPartialProbeDoesNotReportSuccess(t *testing.T) {
	setupIQResultDB(t)
	createIQResultRun(t, "done", IQTestRunStatusFinished, "v1", 10)
	createIQScore(t, "done", 1, "a", 75, 70, 8)
	createIQResultRun(t, "aborted", IQTestRunStatusAborted, "v1", 20)
	createIQScore(t, "aborted", 1, "a", 95, 70, 8)
	scores, err := GetLatestIQChannelScores([]int{1})
	require.NoError(t, err)
	require.Equal(t, 75, *scores[1].Score)
	require.Equal(t, "error", scores[1].Status)
	require.Equal(t, "run_aborted", scores[1].ErrorClass)
	require.Nil(t, scores[1].Previous)
}

func TestIQResultFiltersAndPagination(t *testing.T) {
	setupIQResultDB(t)
	createIQResultRun(t, "one", IQTestRunStatusFinished, "v1", 10)
	createIQScore(t, "one", 1, "a", 75, 70, 8)
	createIQScore(t, "one", 1, "b", 80, 70, 8)
	createIQScore(t, "one", 2, "a", 90, 70, 8)
	require.NoError(t, CreateIQTestResult(&IQTestResult{RunID: "two", ChannelID: 1, RequestedModel: "a", Status: IQTestResultStatusError}))
	rows, total, err := ListIQTestResultsFiltered("one", 1, "a", "success", 1, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	require.Equal(t, 75, *rows[0].Score)
	rows, total, err = ListIQTestResultsFiltered("", 0, "a", "success", 2, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, rows, 1)
	rows, total, err = ListIQTestResultsFiltered("", 0, "missing", "", 1, 10)
	require.NoError(t, err)
	require.Zero(t, total)
	require.NotNil(t, rows)
	require.Empty(t, rows)
}

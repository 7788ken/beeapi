package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupIQControllerDB(t *testing.T) *gin.Engine {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "iq-controller.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	previous := model.DB
	model.DB = db
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { model.DB = previous; require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.IQTestControl{}, &model.IQTestAction{}, &model.IQTestModel{}, &model.IQTestRun{}, &model.IQTestLease{}, &model.IQTestResult{}))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/run_now", RunIQTestNow)
	router.GET("/runs", GetIQTestRuns)
	router.POST("/models", CreateIQTestModel)
	router.PUT("/models/:id", UpdateIQTestModel)
	router.DELETE("/models/:id", DeleteIQTestModel)
	router.PUT("/setting", UpdateIQTestSetting)
	return router
}

func TestIQRunNowRejectsInvalidJSONWithoutCreatingRuns(t *testing.T) {
	router := setupIQControllerDB(t)
	for _, body := range []string{`{`, `{"channel_id":"bad"}`, `{"channel_id":-1}`, `{"channel_id":0}`, `{"channel_id":null}`, `{"channel_id":1.2}`, `[]`, ``} {
		t.Run(body, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/run_now", bytes.NewBufferString(body)))
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
	var count int64
	require.NoError(t, model.DB.Model(&model.IQTestRun{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestIQSettingAndModelHTTPVersionContract(t *testing.T) {
	router := setupIQControllerDB(t)
	setting := service.DefaultIQTestSetting()
	setting.Enabled = true
	encoded, err := common.Marshal(setting)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/setting", bytes.NewReader(encoded)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/setting", bytes.NewReader(encoded)))
	require.Equal(t, http.StatusConflict, rec.Code)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/models", bytes.NewBufferString(`{"model_name":"local","baseline_score":0,"enabled":false}`)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var row model.IQTestModel
	require.NoError(t, model.DB.First(&row).Error)
	require.Zero(t, row.BaselineScore)
	require.False(t, row.Enabled)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/models/1", bytes.NewBufferString(`{"baseline_score":80,"version":1}`)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/models/1", bytes.NewBufferString(`{"baseline_score":90,"version":1}`)))
	require.Equal(t, http.StatusConflict, rec.Code)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/models/1?version=2", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for _, path := range []string{"/runs?page=-1", "/runs?page_size=101", "/runs?page=bad"} {
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

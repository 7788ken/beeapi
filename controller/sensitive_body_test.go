package controller

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestGetSensitiveBlockBodyReturnsOwnerHintOnAnotherNode(t *testing.T) {
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousNodeName := common.NodeName
	previousUsingSQLite := common.UsingSQLite
	previousUsingMySQL := common.UsingMySQL
	previousUsingPostgreSQL := common.UsingPostgreSQL

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "sensitive-body.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)

	model.DB = db
	model.LOG_DB = db
	common.NodeName = "node-b"
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.NodeName = previousNodeName
		common.UsingSQLite = previousUsingSQLite
		common.UsingMySQL = previousUsingMySQL
		common.UsingPostgreSQL = previousUsingPostgreSQL
		_ = sqlDB.Close()
	})

	require.NoError(t, db.AutoMigrate(
		&model.SensitiveAuditJobRecord{},
		&model.SensitiveBlockLog{},
	))
	job := model.SensitiveAuditJobRecord{
		JobID:       "owner-hint-job",
		StorageNode: "node-a",
		Status:      model.SensitiveAuditJobSucceeded,
		DumpPath:    filepath.Join(t.TempDir(), "owner-only.json.gz"),
		BodySHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BodySize:    128,
		DumpState:   model.SensitiveAuditDumpReady,
		CreatedAt:   100,
	}
	require.NoError(t, db.Create(&job).Error)
	logEntry := model.SensitiveBlockLog{
		AuditJobId:     &job.JobID,
		MatchedWordId:  1,
		MatchedPattern: "secret",
		Action:         model.SensitiveActionMonitor,
		DumpPath:       job.DumpPath,
		DumpExists:     true,
	}
	require.NoError(t, db.Create(&logEntry).Error)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/sensitive_block/:id/body", GetSensitiveBlockBody)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/sensitive_block/"+strconv.Itoa(logEntry.Id)+"/body", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Success)
	require.Equal(t, "dump 文件位于节点 node-a，当前节点无法读取", response.Message)
}

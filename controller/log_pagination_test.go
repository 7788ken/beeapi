package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type userLogsPageContractResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Total         int  `json:"total"`
		TotalIsCapped bool `json:"total_is_capped"`
	} `json:"data"`
}

func TestUserLogsCappedTotalResponseContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Log{}))

	oldDB, oldLogDB := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	const cappedTotal = 10000
	rows := make([]*model.Log, 0, cappedTotal+1)
	for i := 0; i < cappedTotal+1; i++ {
		rows = append(rows, &model.Log{
			UserId:    7,
			Type:      model.LogTypeConsume,
			CreatedAt: int64(i + 1),
		})
	}
	require.NoError(t, db.CreateInBatches(rows, 1000).Error)

	userRecorder := httptest.NewRecorder()
	userContext, _ := gin.CreateTestContext(userRecorder)
	userContext.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/log/self?p=1&page_size=20",
		nil,
	)
	userContext.Set("id", 7)

	GetUserLogs(userContext)

	var userResponse userLogsPageContractResponse
	require.NoError(t, common.Unmarshal(userRecorder.Body.Bytes(), &userResponse))
	require.True(t, userResponse.Success)
	require.Equal(t, cappedTotal, userResponse.Data.Total)
	require.True(t, userResponse.Data.TotalIsCapped)

	adminRecorder := httptest.NewRecorder()
	adminContext, _ := gin.CreateTestContext(adminRecorder)
	adminContext.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/log/?p=1&page_size=20",
		nil,
	)

	GetAllLogs(adminContext)

	require.NotContains(t, adminRecorder.Body.String(), `"total_is_capped"`)
}

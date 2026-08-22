package controller

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// testAllChannels 在置起运行标志之后、把标志所有权交给后台任务之前会查一次库。
// 该查询失败时若不复位标志，定时全量测试会被永久挡在"测试已在运行中"，只能重启进程恢复。
func TestTestAllChannelsResetsRunningFlagWhenChannelQueryFails(t *testing.T) {
	previousDB := model.DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	// 不建 channels 表，令 GetAllChannels 走失败分支。
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		require.NoError(t, sqlDB.Close())
	})

	require.Error(t, testAllChannels(false))

	testAllChannelsLock.Lock()
	running := testAllChannelsRunning
	testAllChannelsLock.Unlock()
	require.False(t, running, "查库失败后运行标志必须复位，否则定时测试永久停摆")

	// 复位到位才能再次进入；否则这里会拿到"测试已在运行中"。
	require.Error(t, testAllChannels(false))
}

package model

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func withSystemRoleLiftOptionDB(t *testing.T) {
	t.Helper()

	previousDB := DB
	previousRedis := common.RedisEnabled
	common.OptionMapRWMutex.Lock()
	previousMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	previousLift := model_setting.GetClaudeSettings().SystemRoleLiftEnabled

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "options.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)

	DB = db
	common.RedisEnabled = false
	require.NoError(t, config.UpdateConfigFromMap(model_setting.GetClaudeSettings(), map[string]string{
		"system_role_lift_enabled": "false",
	}))

	t.Cleanup(func() {
		DB = previousDB
		common.RedisEnabled = previousRedis
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousMap
		common.OptionMapRWMutex.Unlock()
		_ = config.UpdateConfigFromMap(model_setting.GetClaudeSettings(), map[string]string{
			"system_role_lift_enabled": strconv.FormatBool(previousLift),
		})
		_ = sqlDB.Close()
	})
}

func optionValue(t *testing.T, key string) (string, bool) {
	t.Helper()
	var row Option
	err := DB.Where(map[string]any{"key": key}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false
	}
	require.NoError(t, err)
	return row.Value, true
}

func TestGrandfatherSystemRoleLiftLeavesFreshDatabaseOff(t *testing.T) {
	withSystemRoleLiftOptionDB(t)

	loadOptionsFromDatabase()

	_, liftStored := optionValue(t, systemRoleLiftOptionKey)
	require.False(t, liftStored)
	marker, ok := optionValue(t, systemRoleLiftGrandfatherKey)
	require.True(t, ok)
	require.Equal(t, "true", marker)
	require.False(t, model_setting.GetClaudeSettings().SystemRoleLiftEnabled)

	require.NoError(t, DB.Create(&Option{Key: "Notice", Value: "later"}).Error)
	common.OptionMapRWMutex.Lock()
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, config.UpdateConfigFromMap(model_setting.GetClaudeSettings(), map[string]string{
		"system_role_lift_enabled": "false",
	}))

	loadOptionsFromDatabase()

	_, liftStored = optionValue(t, systemRoleLiftOptionKey)
	require.False(t, liftStored)
	require.False(t, model_setting.GetClaudeSettings().SystemRoleLiftEnabled)
}

func TestGrandfatherSystemRoleLiftPreservesImplicitlyEnabledSite(t *testing.T) {
	withSystemRoleLiftOptionDB(t)
	require.NoError(t, DB.Create(&Option{Key: "Notice", Value: "existing"}).Error)

	loadOptionsFromDatabase()

	lift, ok := optionValue(t, systemRoleLiftOptionKey)
	require.True(t, ok)
	require.Equal(t, "true", lift)
	require.True(t, model_setting.GetClaudeSettings().SystemRoleLiftEnabled)
	marker, ok := optionValue(t, systemRoleLiftGrandfatherKey)
	require.True(t, ok)
	require.Equal(t, "true", marker)
}

func TestGrandfatherSystemRoleLiftKeepsExplicitFalse(t *testing.T) {
	withSystemRoleLiftOptionDB(t)
	require.NoError(t, DB.Create(&Option{Key: "Notice", Value: "existing"}).Error)
	require.NoError(t, DB.Create(&Option{Key: systemRoleLiftOptionKey, Value: "false"}).Error)

	loadOptionsFromDatabase()

	lift, ok := optionValue(t, systemRoleLiftOptionKey)
	require.True(t, ok)
	require.Equal(t, "false", lift)
	require.False(t, model_setting.GetClaudeSettings().SystemRoleLiftEnabled)
}

func TestGrandfatherSystemRoleLiftKeepsExplicitTrue(t *testing.T) {
	withSystemRoleLiftOptionDB(t)
	require.NoError(t, DB.Create(&Option{Key: systemRoleLiftOptionKey, Value: "true"}).Error)

	loadOptionsFromDatabase()

	lift, ok := optionValue(t, systemRoleLiftOptionKey)
	require.True(t, ok)
	require.Equal(t, "true", lift)
	require.True(t, model_setting.GetClaudeSettings().SystemRoleLiftEnabled)
}

package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupUserUpdateTestState clears the users table before and after each test so
// that email-uniqueness assertions run against a known-empty table. The shared
// in-memory DB is provisioned in TestMain (see task_cas_test.go).
func setupUserUpdateTestState(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Exec("DELETE FROM users").Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM users")
	})
}

func TestEnsureEmailAvailableRejectsExistingEmailCaseInsensitive(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "Taken@Example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := EnsureEmailAvailable(" taken@example.COM ", 0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	user, err := GetUniqueUserByEmail("TAKEN@example.com")
	require.NoError(t, err)
	assert.Equal(t, "existing", user.Username)

	require.NoError(t, EnsureEmailAvailable("taken@example.com", user.Id))
}

func TestInsertRejectsDuplicateEmailWithoutUniqueIndex(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "taken@example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	user := &User{
		Username: "oauth-user",
		Email:    "TAKEN@example.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	err := user.Insert(0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	var count int64
	require.NoError(t, DB.Model(&User{}).Where("username = ?", "oauth-user").Count(&count).Error)
	assert.Zero(t, count)
}

func TestInsertKeepsBlankPasswordForPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	user := &User{
		Username: "passwordless-user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	require.NoError(t, user.Insert(0))

	var stored User
	require.NoError(t, DB.Where("username = ?", user.Username).First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestValidateAndFillRejectsPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "passwordless-user",
		Password: "",
		Status:   common.UserStatusEnabled,
	}).Error)

	loginUser := User{
		Username: "passwordless-user",
		Password: "NewPassword123",
	}
	err := loginUser.ValidateAndFill()
	require.ErrorIs(t, err, ErrInvalidCredentials)

	var stored User
	require.NoError(t, DB.Where("username = ?", "passwordless-user").First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestResetUserPasswordByEmailRequiresSingleActiveMatch(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "duplicate-1",
		Password: "old-1",
		Email:    "legacy@example.com",
		AffCode:  "dupe1",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&User{
		Username: "duplicate-2",
		Password: "old-2",
		Email:    "LEGACY@example.com",
		AffCode:  "dupe2",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := ResetUserPasswordByEmail("legacy@example.com", "NewPassword123")
	require.ErrorIs(t, err, ErrEmailAmbiguous)

	var duplicates []User
	require.NoError(t, DB.Where("LOWER(email) = ?", "legacy@example.com").Order("username asc").Find(&duplicates).Error)
	require.Len(t, duplicates, 2)
	assert.Equal(t, "old-1", duplicates[0].Password)
	assert.Equal(t, "old-2", duplicates[1].Password)

	require.NoError(t, DB.Create(&User{
		Username: "unique",
		Password: "old",
		Email:    "unique@example.com",
		AffCode:  "unique",
		Status:   common.UserStatusEnabled,
	}).Error)

	require.NoError(t, ResetUserPasswordByEmail("UNIQUE@example.com", "NewPassword123"))

	var unique User
	require.NoError(t, DB.Where("username = ?", "unique").First(&unique).Error)
	assert.True(t, common.ValidatePasswordAndHash("NewPassword123", unique.Password))
	assert.Equal(t, int64(2), unique.AuthVersion)

	err = ResetUserPasswordByEmail("missing@example.com", "NewPassword123")
	require.True(t, errors.Is(err, ErrEmailNotFound))
}

func TestUserPasswordUpdateBumpsAuthVersionAtomically(t *testing.T) {
	setupUserUpdateTestState(t)

	user := &User{
		Username:    "password-update",
		Password:    "old-password",
		AffCode:     "password-update",
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(user).Error)

	user.Password = "NewPassword123"
	require.NoError(t, user.Update(true))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.True(t, common.ValidatePasswordAndHash("NewPassword123", stored.Password))
	require.Equal(t, int64(2), stored.AuthVersion)
}

func TestRoleAndStatusUpdateBumpsAuthVersionAtomically(t *testing.T) {
	setupUserUpdateTestState(t)
	user := &User{
		Username:    "authorization-update",
		Password:    "old-password",
		AffCode:     "authorization-update",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, UpdateUserRoleStatusAndBumpAuthVersion(
		user.Id,
		common.RoleAdminUser,
		common.UserStatusDisabled,
	))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.Equal(t, common.RoleAdminUser, stored.Role)
	require.Equal(t, common.UserStatusDisabled, stored.Status)
	require.Equal(t, int64(2), stored.AuthVersion)
}

func TestAdminPasswordEditBumpsAuthVersionAtomically(t *testing.T) {
	setupUserUpdateTestState(t)
	user := &User{
		Username:    "admin-password-edit",
		Password:    "old-password",
		DisplayName: "Before",
		AffCode:     "admin-password-edit",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		AuthVersion: 1,
	}
	require.NoError(t, DB.Create(user).Error)

	edit := &User{
		Id:          user.Id,
		Username:    user.Username,
		Password:    "NewPassword123",
		DisplayName: "After",
	}
	require.NoError(t, edit.Edit(true))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.True(t, common.ValidatePasswordAndHash("NewPassword123", stored.Password))
	require.Equal(t, "After", stored.DisplayName)
	require.Equal(t, int64(2), stored.AuthVersion)
}

package kernel

import (
	"context"
	"testing"

	"github.com/0xJacky/Nginx-UI/internal/testdb"
	"github.com/0xJacky/Nginx-UI/model"
	appSettings "github.com/0xJacky/Nginx-UI/settings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uozi-tech/cosy"
	cModel "github.com/uozi-tech/cosy/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupInitUserTest installs a fresh in-memory database as the Cosy global
// database, which is the one InitUser reads through cosy.UseDB.
func setupInitUserTest(t *testing.T) *gorm.DB {
	t.Helper()

	originalCryptoSecret := appSettings.CryptoSettings.Secret
	appSettings.CryptoSettings.Secret = "init-user-test-root"
	t.Cleanup(func() {
		appSettings.CryptoSettings.Secret = originalCryptoSecret
		cModel.ClearCollection()
	})

	cModel.ClearCollection()
	cosy.RegisterModels(model.User{}, model.Passkey{})
	return cosy.InitDB(sqlite.Open(testdb.DSN(t)))
}

func findUnscopedUser(t *testing.T, db *gorm.DB, id uint64) model.User {
	t.Helper()
	var user model.User
	require.NoError(t, db.Unscoped().Where("id = ?", id).First(&user).Error)
	return user
}

func TestInitUserCreatesAdminWhenMissing(t *testing.T) {
	db := setupInitUserTest(t)

	InitUser(context.Background())

	user := findUnscopedUser(t, db, 1)
	assert.Equal(t, "admin", user.Name)
	assert.Empty(t, user.Password)
	assert.Nil(t, user.DeletedAt)
}

func TestInitUserKeepsExistingAdmin(t *testing.T) {
	db := setupInitUserTest(t)
	require.NoError(t, db.Create(&model.User{
		Model:    model.Model{ID: 1},
		Name:     "root",
		Password: "existing-hash",
	}).Error)

	InitUser(context.Background())

	user := findUnscopedUser(t, db, 1)
	assert.Equal(t, "root", user.Name)
	assert.Equal(t, "existing-hash", user.Password)

	var count int64
	require.NoError(t, db.Unscoped().Model(&model.User{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestInitUserRestoresSoftDeletedAdmin(t *testing.T) {
	db := setupInitUserTest(t)
	require.NoError(t, db.Create(&model.User{
		Model:    model.Model{ID: 1},
		Name:     "root",
		Password: "existing-hash",
	}).Error)
	require.NoError(t, db.Delete(&model.User{}, 1).Error)
	require.NotNil(t, findUnscopedUser(t, db, 1).DeletedAt)

	InitUser(context.Background())

	user := findUnscopedUser(t, db, 1)
	assert.Nil(t, user.DeletedAt, "the soft-deleted initial user must be restored")
	assert.Equal(t, "root", user.Name)
	assert.Equal(t, "existing-hash", user.Password)
}

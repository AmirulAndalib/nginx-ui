package kernel

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xJacky/Nginx-UI/model"
	appSettings "github.com/0xJacky/Nginx-UI/settings"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cSettings "github.com/uozi-tech/cosy/settings"
)

// setupSettingsFile points the settings package at a fresh app.ini so that
// settings.Update persists into the test's temp dir. It restores every
// setting the kernel initializers may generate once the test ends.
func setupSettingsFile(t *testing.T) string {
	t.Helper()

	originalConfPath := cSettings.ConfPath
	originalNodeSecret := appSettings.NodeSettings.Secret
	originalInstanceID := appSettings.NodeSettings.InstanceID
	originalCryptoSecret := appSettings.CryptoSettings.Secret
	originalJwtSecret := cSettings.AppSettings.JwtSecret
	t.Cleanup(func() {
		cSettings.ConfPath = originalConfPath
		appSettings.NodeSettings.Secret = originalNodeSecret
		appSettings.NodeSettings.InstanceID = originalInstanceID
		appSettings.CryptoSettings.Secret = originalCryptoSecret
		cSettings.AppSettings.JwtSecret = originalJwtSecret
	})

	confPath := filepath.Join(t.TempDir(), "app.ini")
	require.NoError(t, os.WriteFile(confPath, []byte("[app]\n[server]\nPort = 9000\n"), 0o644))
	appSettings.Init(confPath)
	cSettings.ConfPath = confPath

	return confPath
}

// reloadSettings clears the given values in memory and reads app.ini again,
// so the caller can tell what was actually persisted.
func reloadSettings(t *testing.T, confPath string, clear ...*string) {
	t.Helper()
	for _, value := range clear {
		*value = ""
	}
	appSettings.Init(confPath)
}

func TestInitNodeSecret(t *testing.T) {
	t.Run("generates and persists a secret when empty", func(t *testing.T) {
		confPath := setupSettingsFile(t)
		appSettings.NodeSettings.Secret = ""

		InitNodeSecret()

		generated := appSettings.NodeSettings.Secret
		_, err := uuid.Parse(generated)
		require.NoError(t, err, "generated secret %q is not a UUID", generated)

		reloadSettings(t, confPath, &appSettings.NodeSettings.Secret)
		assert.Equal(t, generated, appSettings.NodeSettings.Secret)
	})

	t.Run("keeps an existing secret", func(t *testing.T) {
		setupSettingsFile(t)
		appSettings.NodeSettings.Secret = "existing-node-secret"

		InitNodeSecret()

		assert.Equal(t, "existing-node-secret", appSettings.NodeSettings.Secret)
	})
}

func TestInitNodeInstanceID(t *testing.T) {
	t.Run("generates and persists an instance ID when empty", func(t *testing.T) {
		confPath := setupSettingsFile(t)
		appSettings.NodeSettings.InstanceID = ""

		InitNodeInstanceID()

		generated := appSettings.NodeSettings.InstanceID
		_, err := uuid.Parse(generated)
		require.NoError(t, err, "generated instance ID %q is not a UUID", generated)

		reloadSettings(t, confPath, &appSettings.NodeSettings.InstanceID)
		assert.Equal(t, generated, appSettings.NodeSettings.InstanceID)
	})

	t.Run("keeps an existing instance ID", func(t *testing.T) {
		setupSettingsFile(t)
		appSettings.NodeSettings.InstanceID = "11111111-1111-4111-8111-111111111111"

		InitNodeInstanceID()

		assert.Equal(t, "11111111-1111-4111-8111-111111111111", appSettings.NodeSettings.InstanceID)
	})
}

func TestInitCryptoSecret(t *testing.T) {
	t.Run("generates and persists a 256-bit hex secret when empty", func(t *testing.T) {
		confPath := setupSettingsFile(t)
		appSettings.CryptoSettings.Secret = ""

		InitCryptoSecret()

		generated := appSettings.CryptoSettings.Secret
		key, err := hex.DecodeString(generated)
		require.NoError(t, err, "generated secret %q is not hex", generated)
		assert.Len(t, key, 32)

		reloadSettings(t, confPath, &appSettings.CryptoSettings.Secret)
		assert.Equal(t, generated, appSettings.CryptoSettings.Secret)
	})

	t.Run("keeps an existing secret", func(t *testing.T) {
		setupSettingsFile(t)
		appSettings.CryptoSettings.Secret = "existing-crypto-secret"

		InitCryptoSecret()

		assert.Equal(t, "existing-crypto-secret", appSettings.CryptoSettings.Secret)
	})
}

func TestSkipInstall(t *testing.T) {
	t.Run("generates and persists the JWT and node secrets when empty", func(t *testing.T) {
		confPath := setupSettingsFile(t)
		cSettings.AppSettings.JwtSecret = ""
		appSettings.NodeSettings.Secret = ""

		skipInstall()

		jwtSecret := cSettings.AppSettings.JwtSecret
		nodeSecret := appSettings.NodeSettings.Secret
		require.NotEmpty(t, jwtSecret)
		require.NotEmpty(t, nodeSecret)
		assert.NotEqual(t, jwtSecret, nodeSecret)

		reloadSettings(t, confPath, &cSettings.AppSettings.JwtSecret, &appSettings.NodeSettings.Secret)
		assert.Equal(t, jwtSecret, cSettings.AppSettings.JwtSecret)
		assert.Equal(t, nodeSecret, appSettings.NodeSettings.Secret)
	})

	t.Run("keeps existing secrets", func(t *testing.T) {
		setupSettingsFile(t)
		cSettings.AppSettings.JwtSecret = "existing-jwt-secret"
		appSettings.NodeSettings.Secret = "existing-node-secret"

		skipInstall()

		assert.Equal(t, "existing-jwt-secret", cSettings.AppSettings.JwtSecret)
		assert.Equal(t, "existing-node-secret", appSettings.NodeSettings.Secret)
	})
}

func TestInitJsExtensionType(t *testing.T) {
	InitJsExtensionType()

	assert.Equal(t, "text/javascript; charset=utf-8", mime.TypeByExtension(".js"))
}

func TestIsUnknownServerListenError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: false},
		{name: "canceled", err: context.Canceled, want: false},
		{name: "closed listener", err: net.ErrClosed, want: false},
		{name: "wrapped canceled", err: fmt.Errorf("serve: %w", context.Canceled), want: false},
		{name: "wrapped closed listener", err: &net.OpError{Op: "accept", Err: net.ErrClosed}, want: false},
		{name: "server closed", err: http.ErrServerClosed, want: true},
		{name: "address in use", err: errors.New("listen tcp :9000: bind: address already in use"), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsUnknownServerListenError(tt.err))
		})
	}
}

func TestRecoveryStopsPanicFromEscapingBoot(t *testing.T) {
	assert.NotPanics(t, func() {
		defer recovery()
		panic("boot step failed")
	})
}

func TestRegisterPredefinedUserSkipsWithoutCompleteConfiguration(t *testing.T) {
	tests := []struct {
		name             string
		skipInstallation bool
		userName         string
		password         string
	}{
		{name: "skip installation disabled", skipInstallation: false, userName: "admin", password: "my-secret-password"},
		{name: "missing password", skipInstallation: true, userName: "admin"},
		{name: "missing name", skipInstallation: true, password: "my-secret-password"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupRegisterPredefinedUserTest(t)
			appSettings.NodeSettings.SkipInstallation = tt.skipInstallation
			t.Setenv("NGINX_UI_PREDEFINED_USER_NAME", tt.userName)
			t.Setenv("NGINX_UI_PREDEFINED_USER_PASSWORD", tt.password)

			registerPredefinedUser(context.Background())

			var count int64
			require.NoError(t, model.UseDB().Model(&model.User{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

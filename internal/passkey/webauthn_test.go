package passkey

import (
	"context"
	"testing"

	"github.com/0xJacky/Nginx-UI/settings"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupWebAuthn swaps in the given WebAuthn settings and starts the test with
// no WebAuthn instance, restoring both afterwards.
func setupWebAuthn(t *testing.T, options settings.WebAuthn) {
	t.Helper()
	originalOptions := *settings.WebAuthnSettings
	originalInstance := instance
	t.Cleanup(func() {
		*settings.WebAuthnSettings = originalOptions
		instance = originalInstance
	})
	*settings.WebAuthnSettings = options
	instance = nil
}

var configuredOptions = settings.WebAuthn{
	RPDisplayName: "Nginx UI",
	RPID:          "nginx-ui.example",
	RPOrigins:     []string{"https://nginx-ui.example", "https://nginx-ui.example:9000"},
}

func TestEnabled(t *testing.T) {
	tests := []struct {
		name    string
		options settings.WebAuthn
		want    bool
	}{
		{name: "fully configured", options: configuredOptions, want: true},
		{name: "missing display name", options: settings.WebAuthn{RPID: "nginx-ui.example", RPOrigins: []string{"https://nginx-ui.example"}}},
		{name: "missing RPID", options: settings.WebAuthn{RPDisplayName: "Nginx UI", RPOrigins: []string{"https://nginx-ui.example"}}},
		{name: "missing origins", options: settings.WebAuthn{RPDisplayName: "Nginx UI", RPID: "nginx-ui.example"}},
		{name: "empty", options: settings.WebAuthn{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupWebAuthn(t, tt.options)
			assert.Equal(t, tt.want, Enabled())
		})
	}
}

func TestInitWithoutConfigurationLeavesWebAuthnUnavailable(t *testing.T) {
	setupWebAuthn(t, settings.WebAuthn{RPID: "nginx-ui.example"})

	Init(context.Background())

	assert.Nil(t, GetInstance())
}

func TestInitBuildsInstanceFromSettings(t *testing.T) {
	setupWebAuthn(t, configuredOptions)

	Init(context.Background())

	webAuthn := GetInstance()
	require.NotNil(t, webAuthn)
	assert.Equal(t, "Nginx UI", webAuthn.Config.RPDisplayName)
	assert.Equal(t, "nginx-ui.example", webAuthn.Config.RPID)
	assert.Equal(t, configuredOptions.RPOrigins, webAuthn.Config.RPOrigins)

	selection := webAuthn.Config.AuthenticatorSelection
	require.NotNil(t, selection.RequireResidentKey)
	assert.True(t, *selection.RequireResidentKey, "passkeys must be discoverable credentials")
	assert.Equal(t, protocol.VerificationRequired, selection.UserVerification)
}

package performance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xJacky/Nginx-UI/settings"
	"github.com/uozi-tech/cosy"
)

// setConfigPath points the nginx.conf entry path at path for the duration of
// the test.
func setConfigPath(t *testing.T, path string) {
	t.Helper()

	previous := settings.NginxSettings.ConfigPath
	settings.NginxSettings.ConfigPath = path
	t.Cleanup(func() {
		settings.NginxSettings.ConfigPath = previous
	})
}

// setConfDir points the nginx configuration directory at a fresh temporary
// directory that already contains conf.d, and returns the conf.d path. Setting
// ConfigDir keeps nginx.GetConfPath from shelling out to `nginx -V`.
func setConfDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	confD := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confD, 0o755); err != nil {
		t.Fatalf("failed to create conf.d: %v", err)
	}

	previous := settings.NginxSettings.ConfigDir
	settings.NginxSettings.ConfigDir = dir
	t.Cleanup(func() {
		settings.NginxSettings.ConfigDir = previous
	})

	return confD
}

// setStubStatusPort overrides the port the stub_status URL is built with.
func setStubStatusPort(t *testing.T, port uint) {
	t.Helper()

	previous := settings.NginxSettings.StubStatusPort
	settings.NginxSettings.StubStatusPort = port
	t.Cleanup(func() {
		settings.NginxSettings.StubStatusPort = previous
	})
}

// setStubStatusProvider installs a counter override and removes it afterwards.
func setStubStatusProvider(t *testing.T, p StubStatusProvider) {
	t.Helper()

	previous := stubStatusProvider
	SetStubStatusProvider(p)
	t.Cleanup(func() {
		SetStubStatusProvider(previous)
	})
}

// assertCosyCode fails unless err is a cosy error with the same scope and code
// as want. cosy.WrapErrorWithParams returns a new *cosy.Error rather than
// wrapping, so errors.Is cannot match it against the sentinel.
func assertCosyCode(t *testing.T, err, want error) {
	t.Helper()

	var got, wantErr *cosy.Error
	if !errors.As(err, &got) || !errors.As(want, &wantErr) {
		t.Fatalf("error = %v, want cosy error %v", err, want)
	}
	if got.Scope != wantErr.Scope || got.Code != wantErr.Code {
		t.Fatalf("error = %v (%s/%d), want %v (%s/%d)", err, got.Scope, got.Code, want, wantErr.Scope, wantErr.Code)
	}
}

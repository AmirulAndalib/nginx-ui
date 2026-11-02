package performance

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/0xJacky/Nginx-UI/settings"
)

// setPIDPath points the nginx PID file at path. nginx.IsRunning reads the
// configured override on every call in local control mode.
func setPIDPath(t *testing.T, path string) {
	t.Helper()

	previous := settings.NginxSettings.PIDPath
	settings.NginxSettings.PIDPath = path
	t.Cleanup(func() {
		settings.NginxSettings.PIDPath = previous
	})
}

// pretendNginxRunning writes the test process's own PID as the nginx PID, so
// nginx.IsRunning sees a live process without a real nginx.
func pretendNginxRunning(t *testing.T) {
	t.Helper()

	pidPath := filepath.Join(t.TempDir(), "nginx.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatalf("failed to write pid file: %v", err)
	}
	setPIDPath(t, pidPath)
}

func TestGetPerformanceData_NginxNotRunning(t *testing.T) {
	setPIDPath(t, filepath.Join(t.TempDir(), "nginx.pid"))
	// Even with counters available, nothing is reported while nginx is down.
	setStubStatusProvider(t, fixedStubStatus{data: &StubStatusData{Active: 5}})

	got := GetPerformanceData()

	if got.Running {
		t.Error("Running = true, want false without a PID file")
	}
	if got.StubStatusEnabled {
		t.Error("StubStatusEnabled = true, want false while nginx is not running")
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want empty", got.Error)
	}
	if got.Info != (NginxPerformanceInfo{}) {
		t.Errorf("Info = %+v, want zero value", got.Info)
	}
}

func TestGetPerformanceData_StubStatusDisabled(t *testing.T) {
	pretendNginxRunning(t)
	setConfDir(t)

	got := GetPerformanceData()

	if !got.Running {
		t.Fatal("Running = false, want true")
	}
	if got.StubStatusEnabled {
		t.Error("StubStatusEnabled = true, want false")
	}
	if got.Error != ErrStubStatusDisabled.Error() {
		t.Errorf("Error = %q, want %q", got.Error, ErrStubStatusDisabled.Error())
	}
	if got.Info != (NginxPerformanceInfo{}) {
		t.Errorf("Info = %+v, want zero value on error", got.Info)
	}
}

func TestGetNginxProcessInfo_HostViaSSH(t *testing.T) {
	previous := settings.NginxSettings.HostMode
	settings.NginxSettings.HostMode = settings.HostModeSSH
	t.Cleanup(func() {
		settings.NginxSettings.HostMode = previous
	})

	info, err := GetNginxProcessInfo()

	// nginx runs on the SSH host, so local counters would be meaningless.
	if !errors.Is(err, ErrProcessInfoUnavailable) {
		t.Errorf("GetNginxProcessInfo() error = %v, want ErrProcessInfoUnavailable", err)
	}
	if info != nil {
		t.Errorf("info = %+v, want nil", info)
	}
}

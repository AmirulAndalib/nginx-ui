package version

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// useBuildInfo replaces the generated build variables and clears the cached
// Info so GetVersionInfo observes them, restoring everything afterwards.
func useBuildInfo(t *testing.T, version string, buildID, totalBuild int, hash string) {
	t.Helper()
	prevVersion, prevBuildID, prevTotal, prevHash, prevInfo := Version, BuildId, TotalBuild, Hash, versionInfo
	Version, BuildId, TotalBuild, Hash = version, buildID, totalBuild, hash
	versionInfo = nil
	t.Cleanup(func() {
		Version, BuildId, TotalBuild, Hash, versionInfo = prevVersion, prevBuildID, prevTotal, prevHash, prevInfo
	})
}

func TestGetShortHash(t *testing.T) {
	tests := []struct {
		name string
		hash string
		want string
	}{
		{name: "full commit hash", hash: "5b2f70b51f6763e2a81954d894ae41677f2c8c34", want: "5b2f70b5"},
		{name: "exactly eight characters", hash: "abcdef01", want: "abcdef01"},
		{name: "shorter than eight characters", hash: "abc123", want: "abc123"},
		{name: "empty hash", hash: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useBuildInfo(t, "1.0.0", 1, 1, tt.hash)
			if got := GetShortHash(); got != tt.want {
				t.Fatalf("GetShortHash() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetVersionInfo(t *testing.T) {
	useBuildInfo(t, "2.3.4", 7, 600, "0123456789abcdef0123456789abcdef01234567")

	info := GetVersionInfo()
	want := Info{Version: "2.3.4", BuildId: 7, TotalBuild: 600, ShortHash: "01234567"}
	if *info != want {
		t.Fatalf("GetVersionInfo() = %+v, want %+v", *info, want)
	}

	// The info is computed once; later changes to the build variables are not
	// picked up and every caller shares the same value.
	Version, BuildId, Hash = "9.9.9", 99, ""
	again := GetVersionInfo()
	if again != info {
		t.Fatal("GetVersionInfo() returned a different pointer on the second call")
	}
	if *again != want {
		t.Fatalf("GetVersionInfo() after mutation = %+v, want cached %+v", *again, want)
	}
}

func TestGetVersionInfoWithoutHash(t *testing.T) {
	useBuildInfo(t, "", 0, 0, "")

	if got := *GetVersionInfo(); got != (Info{}) {
		t.Fatalf("GetVersionInfo() = %+v, want zero Info", got)
	}
}

func TestGetRuntimeInfo(t *testing.T) {
	useBuildInfo(t, "2.3.4", 7, 600, "0123456789abcdef0123456789abcdef01234567")

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	wantPath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}

	tests := []struct {
		name         string
		officialEnv  string
		ignoreSocket string
		wantInDocker bool
	}{
		{name: "outside docker", wantInDocker: false},
		{name: "official docker image", officialEnv: "true", wantInDocker: true},
		{name: "official docker image ignoring socket", officialEnv: "true", ignoreSocket: "true", wantInDocker: false},
		{name: "non-boolean env value", officialEnv: "yes please", wantInDocker: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NGINX_UI_OFFICIAL_DOCKER", tt.officialEnv)
			t.Setenv("NGINX_UI_IGNORE_DOCKER_SOCKET", tt.ignoreSocket)

			info, err := GetRuntimeInfo()
			if err != nil {
				t.Fatalf("GetRuntimeInfo() error = %v", err)
			}
			if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
				t.Fatalf("OS/Arch = %s/%s, want %s/%s", info.OS, info.Arch, runtime.GOOS, runtime.GOARCH)
			}
			if info.ExPath != wantPath {
				t.Fatalf("ExPath = %q, want %q", info.ExPath, wantPath)
			}
			if info.CurVersion != GetVersionInfo() {
				t.Fatalf("CurVersion = %+v, want the shared GetVersionInfo() value", info.CurVersion)
			}
			if info.InDocker != tt.wantInDocker {
				t.Fatalf("InDocker = %v, want %v", info.InDocker, tt.wantInDocker)
			}
		})
	}
}

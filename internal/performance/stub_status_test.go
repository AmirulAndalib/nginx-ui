package performance

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// stubStatusServer starts a local HTTP server that answers /stub_status with
// body, points the stub_status port at it, and writes the generated
// stub_status config into a temporary conf.d so IsStubStatusEnabled reports
// the module as enabled.
func stubStatusServer(t *testing.T, body string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != StubStatusPath {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	setStubStatusPort(t, listenerPort(t, srv.Listener.Addr()))
	setConfDir(t)

	if err := CreateStubStatusConfig(); err != nil {
		t.Fatalf("CreateStubStatusConfig() error = %v", err)
	}
}

func listenerPort(t *testing.T, addr net.Addr) uint {
	t.Helper()

	_, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		t.Fatalf("failed to split %q: %v", addr, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		t.Fatalf("failed to parse port %q: %v", portStr, err)
	}
	return uint(port)
}

// writeStubStatusConf writes content as the nginx-ui stub_status config.
func writeStubStatusConf(t *testing.T, confD, content string) string {
	t.Helper()

	path := filepath.Join(confD, StubStatusConfigName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
	return path
}

func TestIsStubStatusEnabled(t *testing.T) {
	tests := []struct {
		name        string
		content     *string
		port        uint
		wantEnabled bool
		wantURL     string
	}{
		{
			name:        "config file absent",
			content:     nil,
			wantEnabled: false,
			wantURL:     "",
		},
		{
			name: "location with stub_status uses the default port",
			content: ptr(`server {
    listen 51820;
    location /stub_status {
        stub_status;
    }
}
`),
			wantEnabled: true,
			wantURL:     "http://127.0.0.1:51820/stub_status",
		},
		{
			name: "configured port is used for the URL",
			content: ptr(`server {
    listen 8099;
    location /nginx_status {
        stub_status;
        allow 127.0.0.1;
        deny all;
    }
}
`),
			port:        8099,
			wantEnabled: true,
			wantURL:     "http://127.0.0.1:8099/stub_status",
		},
		{
			name: "server without stub_status",
			content: ptr(`server {
    listen 51820;
    location / {
        return 200;
    }
}
`),
			wantEnabled: false,
			wantURL:     "",
		},
		{
			name:        "unparseable config",
			content:     ptr("server {\n    location /stub_status {\n        stub_status;\n"),
			wantEnabled: false,
			wantURL:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			confD := setConfDir(t)
			setStubStatusPort(t, tt.port)
			if tt.content != nil {
				writeStubStatusConf(t, confD, *tt.content)
			}

			enabled, url := IsStubStatusEnabled()
			if enabled != tt.wantEnabled || url != tt.wantURL {
				t.Errorf("IsStubStatusEnabled() = (%v, %q), want (%v, %q)", enabled, url, tt.wantEnabled, tt.wantURL)
			}

			info := GetStubStatus()
			if info.Enabled != tt.wantEnabled || info.URL != tt.wantURL {
				t.Errorf("GetStubStatus() = %+v, want {Enabled:%v URL:%q}", info, tt.wantEnabled, tt.wantURL)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestCreateStubStatusConfig(t *testing.T) {
	confD := setConfDir(t)
	setStubStatusPort(t, 9123)

	if err := CreateStubStatusConfig(); err != nil {
		t.Fatalf("CreateStubStatusConfig() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(confD, StubStatusConfigName))
	if err != nil {
		t.Fatalf("stub_status config was not written: %v", err)
	}

	got := string(content)
	for _, want := range []string{
		"listen 9123;",
		"server_name localhost;",
		"location /stub_status",
		"stub_status;",
		"allow 127.0.0.1;",
		"deny all;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated config is missing %q:\n%s", want, got)
		}
	}

	enabled, url := IsStubStatusEnabled()
	if !enabled || url != "http://127.0.0.1:9123/stub_status" {
		t.Errorf("IsStubStatusEnabled() after create = (%v, %q)", enabled, url)
	}
}

func TestCreateStubStatusConfig_MissingConfD(t *testing.T) {
	confD := setConfDir(t)
	if err := os.Remove(confD); err != nil {
		t.Fatalf("failed to remove conf.d: %v", err)
	}

	if err := CreateStubStatusConfig(); err == nil {
		t.Fatal("CreateStubStatusConfig() error = nil, want an error when conf.d does not exist")
	}
}

func TestEnableStubStatus(t *testing.T) {
	t.Run("creates the config when absent", func(t *testing.T) {
		confD := setConfDir(t)
		setStubStatusPort(t, 0)

		if err := EnableStubStatus(); err != nil {
			t.Fatalf("EnableStubStatus() error = %v", err)
		}

		if _, err := os.Stat(filepath.Join(confD, StubStatusConfigName)); err != nil {
			t.Fatalf("stub_status config was not created: %v", err)
		}
		if enabled, _ := IsStubStatusEnabled(); !enabled {
			t.Error("IsStubStatusEnabled() = false after EnableStubStatus()")
		}
	})

	t.Run("keeps an existing config untouched", func(t *testing.T) {
		confD := setConfDir(t)
		const existing = `server {
    listen 51820;
    location /custom {
        stub_status;
    }
}
`
		path := writeStubStatusConf(t, confD, existing)

		if err := EnableStubStatus(); err != nil {
			t.Fatalf("EnableStubStatus() error = %v", err)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read config: %v", err)
		}
		if string(content) != existing {
			t.Errorf("EnableStubStatus() rewrote an already enabled config:\n%s", content)
		}
	})
}

func TestDisableStubStatus(t *testing.T) {
	t.Run("removes the config", func(t *testing.T) {
		confD := setConfDir(t)
		path := writeStubStatusConf(t, confD, "server {\n    location /stub_status {\n        stub_status;\n    }\n}\n")

		if err := DisableStubStatus(); err != nil {
			t.Fatalf("DisableStubStatus() error = %v", err)
		}

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("stub_status config still exists after DisableStubStatus(): %v", err)
		}
		if enabled, _ := IsStubStatusEnabled(); enabled {
			t.Error("IsStubStatusEnabled() = true after DisableStubStatus()")
		}
	})

	t.Run("no-op when absent", func(t *testing.T) {
		setConfDir(t)

		if err := DisableStubStatus(); err != nil {
			t.Errorf("DisableStubStatus() error = %v, want nil", err)
		}
	})
}

type fixedStubStatus struct {
	data *StubStatusData
}

func (f fixedStubStatus) StubStatus() *StubStatusData { return f.data }

func TestGetStubStatusData_Provider(t *testing.T) {
	// No stub_status config exists, so the result can only come from the
	// provider.
	setConfDir(t)

	want := &StubStatusData{Active: 3, Accepts: 10, Handled: 10, Requests: 42, Reading: 1, Writing: 1, Waiting: 1}
	setStubStatusProvider(t, fixedStubStatus{data: want})

	enabled, data, err := GetStubStatusData()
	if err != nil {
		t.Fatalf("GetStubStatusData() error = %v", err)
	}
	if !enabled {
		t.Error("enabled = false, want true when a provider supplies data")
	}
	if *data != *want {
		t.Errorf("data = %+v, want %+v", *data, *want)
	}
}

func TestGetStubStatusData_ProviderWithoutDataFallsBack(t *testing.T) {
	setConfDir(t)
	setStubStatusProvider(t, fixedStubStatus{data: nil})

	enabled, data, err := GetStubStatusData()
	if !errors.Is(err, ErrStubStatusDisabled) {
		t.Fatalf("GetStubStatusData() error = %v, want ErrStubStatusDisabled", err)
	}
	if enabled {
		t.Error("enabled = true, want false")
	}
	if data == nil || *data != (StubStatusData{}) {
		t.Errorf("data = %+v, want zero counters", data)
	}
}

func TestGetStubStatusData_FromServer(t *testing.T) {
	tests := []struct {
		name string
		body string
		want StubStatusData
	}{
		{
			name: "stock stub_status output",
			body: "Active connections: 291 \n" +
				"server accepts handled requests\n" +
				" 16630948 16630948 31070465 \n" +
				"Reading: 6 Writing: 179 Waiting: 106 \n",
			want: StubStatusData{
				Active: 291, Accepts: 16630948, Handled: 16630948, Requests: 31070465,
				Reading: 6, Writing: 179, Waiting: 106,
			},
		},
		{
			name: "idle server",
			body: "Active connections: 1 \n" +
				"server accepts handled requests\n" +
				" 1 1 1 \n" +
				"Reading: 0 Writing: 1 Waiting: 0 \n",
			want: StubStatusData{Active: 1, Accepts: 1, Handled: 1, Requests: 1, Writing: 1},
		},
		{
			name: "unrecognised body leaves counters at zero",
			body: "<html><body>not stub_status</body></html>",
			want: StubStatusData{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubStatusServer(t, tt.body)

			enabled, data, err := GetStubStatusData()
			if err != nil {
				t.Fatalf("GetStubStatusData() error = %v", err)
			}
			if !enabled {
				t.Error("enabled = false, want true")
			}
			if *data != tt.want {
				t.Errorf("data = %+v, want %+v", *data, tt.want)
			}
		})
	}
}

func TestGetStubStatusData_RequestError(t *testing.T) {
	// Reserve a local port and release it so nothing listens there; the
	// request then fails immediately with connection refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	port := listenerPort(t, ln.Addr())
	if err := ln.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}

	setConfDir(t)
	setStubStatusPort(t, port)
	if err := CreateStubStatusConfig(); err != nil {
		t.Fatalf("CreateStubStatusConfig() error = %v", err)
	}

	enabled, data, err := GetStubStatusData()
	assertCosyCode(t, err, ErrStubStatusRequest)
	if !enabled {
		t.Error("enabled = false, want true: the module is configured even though the request failed")
	}
	if data == nil || *data != (StubStatusData{}) {
		t.Errorf("data = %+v, want zero counters", data)
	}
}

package performance

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/0xJacky/Nginx-UI/settings"
)

// stockNginxConf reproduces the nginx.conf that nginx ships in conf/nginx.conf
// and that distributions install as the main configuration file. Every
// commented directive below is present verbatim in that file, including the
// `#keepalive_timeout  0;` that sits right above the real one.
const stockNginxConf = `
#user  nobody;
worker_processes  1;

#error_log  logs/error.log;
#pid        logs/nginx.pid;

events {
    worker_connections  1024;
}

http {
    include       mime.types;
    default_type  application/octet-stream;

    #log_format  main  '$remote_addr - $remote_user [$time_local] "$request" '
    #                  '$status $body_bytes_sent "$http_referer" '
    #                  '"$http_user_agent" "$http_x_forwarded_for"';

    #access_log  logs/access.log  main;

    sendfile        on;
    #tcp_nopush     on;

    #keepalive_timeout  0;
    keepalive_timeout  65;

    #gzip  on;

    server {
        listen       80;
        server_name  localhost;
    }
}
`

// debianGzipBlock reproduces the gzip section Debian and Ubuntu ship in
// /etc/nginx/nginx.conf: gzip is on, everything that tunes it is commented out.
const debianGzipBlock = `
worker_processes auto;

events {
	worker_connections 768;
}

http {
	client_max_body_size 64m;

	gzip on;

	# gzip_vary on;
	# gzip_proxied any;
	# gzip_comp_level 6;
	# gzip_min_length 256;
	# gzip_buffers 16 8k;
	# gzip_http_version 1.1;
}
`

func writeConf(t *testing.T, content string) {
	t.Helper()

	confPath := filepath.Join(t.TempDir(), "nginx.conf")
	if err := os.WriteFile(confPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write nginx.conf: %v", err)
	}

	previous := settings.NginxSettings.ConfigPath
	settings.NginxSettings.ConfigPath = confPath
	t.Cleanup(func() {
		settings.NginxSettings.ConfigPath = previous
	})
}

func TestGetNginxWorkerConfigInfo_StockConfIgnoresCommentedDirectives(t *testing.T) {
	writeConf(t, stockNginxConf)

	info, err := GetNginxWorkerConfigInfo()
	if err != nil {
		t.Fatalf("GetNginxWorkerConfigInfo() error = %v", err)
	}

	// `#keepalive_timeout  0;` comes first in the file, so a regex that does not
	// know about comments reports 0 instead of the 65 that is in effect.
	if info.KeepaliveTimeout != "65" {
		t.Errorf("KeepaliveTimeout = %q, want %q", info.KeepaliveTimeout, "65")
	}

	// gzip is only mentioned as `#gzip  on;`, so it is off.
	if info.Gzip != "off" {
		t.Errorf("Gzip = %q, want %q", info.Gzip, "off")
	}

	if info.WorkerProcesses != 1 {
		t.Errorf("WorkerProcesses = %d, want %d", info.WorkerProcesses, 1)
	}

	if info.WorkerConnections != 1024 {
		t.Errorf("WorkerConnections = %d, want %d", info.WorkerConnections, 1024)
	}
}

func TestGetNginxWorkerConfigInfo_DebianGzipBlock(t *testing.T) {
	writeConf(t, debianGzipBlock)

	info, err := GetNginxWorkerConfigInfo()
	if err != nil {
		t.Fatalf("GetNginxWorkerConfigInfo() error = %v", err)
	}

	if info.Gzip != "on" {
		t.Errorf("Gzip = %q, want %q", info.Gzip, "on")
	}

	// gzip_comp_level and gzip_min_length are commented out, so nginx uses its
	// own defaults and the dialog must not offer to write the commented values.
	if info.GzipCompLevel != 1 {
		t.Errorf("GzipCompLevel = %d, want the default %d", info.GzipCompLevel, 1)
	}

	if info.GzipMinLength != 1 {
		t.Errorf("GzipMinLength = %d, want the default %d", info.GzipMinLength, 1)
	}

	// An uncommented directive is still read.
	if info.ClientMaxBodySize != "64m" {
		t.Errorf("ClientMaxBodySize = %q, want %q", info.ClientMaxBodySize, "64m")
	}
}

// uppercaseSizeUnits uses uppercase size suffixes, which nginx accepts but the
// parsing regexes previously missed. When a directive was not matched, the
// default was returned instead and silently written back on save, shrinking
// limits the admin had intentionally raised.
const uppercaseSizeUnits = `
worker_processes 2;

events {
	worker_connections 512;
}

http {
	keepalive_timeout 30;
	gzip on;
	client_max_body_size 50M;
	client_header_buffer_size 4K;
	client_body_buffer_size 16K;
}
`

func TestGetNginxWorkerConfigInfo_UppercaseSizeUnits(t *testing.T) {
	writeConf(t, uppercaseSizeUnits)

	info, err := GetNginxWorkerConfigInfo()
	if err != nil {
		t.Fatalf("GetNginxWorkerConfigInfo() error = %v", err)
	}

	if info.ClientMaxBodySize != "50M" {
		t.Errorf("ClientMaxBodySize = %q, want %q", info.ClientMaxBodySize, "50M")
	}

	if info.ClientHeaderBufferSize != "4K" {
		t.Errorf("ClientHeaderBufferSize = %q, want %q", info.ClientHeaderBufferSize, "4K")
	}

	if info.ClientBodyBufferSize != "16K" {
		t.Errorf("ClientBodyBufferSize = %q, want %q", info.ClientBodyBufferSize, "16K")
	}
}

func TestStripComments(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "whole line comment",
			content: "    #gzip  on;\n",
			want:    "    \n",
		},
		{
			name:    "trailing comment",
			content: "gzip on; # enabled\n",
			want:    "gzip on; \n",
		},
		{
			name:    "hash inside double quotes is kept",
			content: "add_header X-Tag \"a#b\";\n",
			want:    "add_header X-Tag \"a#b\";\n",
		},
		{
			name:    "hash inside single quotes is kept",
			content: "log_format m '$a#$b';\n",
			want:    "log_format m '$a#$b';\n",
		},
		{
			name:    "quote inside a comment does not open a string",
			content: "# don't do this\ngzip on; # nor this\n",
			want:    "\ngzip on; \n",
		},
		{
			name:    "an unbalanced quote does not leak into the next line",
			content: "log_format m '$a;\n#gzip on;\n",
			want:    "log_format m '$a;\n\n",
		},
		{
			name:    "content without comments is unchanged",
			content: "worker_processes 4;\nevents {\n}\n",
			want:    "worker_processes 4;\nevents {\n}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripComments(tt.content); got != tt.want {
				t.Errorf("stripComments(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestGetNginxWorkerConfigInfo_EmptyFileReturnsDefaults(t *testing.T) {
	writeConf(t, "")

	info, err := GetNginxWorkerConfigInfo()
	if err != nil {
		t.Fatalf("GetNginxWorkerConfigInfo() error = %v", err)
	}

	want := NginxConfigInfo{
		WorkerProcesses:           1,
		WorkerConnections:         1024,
		ProcessMode:               "manual",
		KeepaliveTimeout:          "65s",
		Gzip:                      "off",
		GzipMinLength:             1,
		GzipCompLevel:             1,
		ClientMaxBodySize:         "1m",
		ServerNamesHashBucketSize: "32",
		ClientHeaderBufferSize:    "1k",
		ClientBodyBufferSize:      "8k",
		ProxyCache: ProxyCacheConfig{
			Enabled:     false,
			Path:        "/var/cache/nginx/proxy_cache",
			Levels:      "1:2",
			UseTempPath: "off",
			KeysZone:    "proxy_cache:10m",
			Inactive:    "60m",
			MaxSize:     "1g",
		},
	}
	if *info != want {
		t.Errorf("GetNginxWorkerConfigInfo() = %+v, want defaults %+v", *info, want)
	}
}

func TestGetNginxWorkerConfigInfo_MissingFile(t *testing.T) {
	setConfigPath(t, filepath.Join(t.TempDir(), "missing.conf"))

	info, err := GetNginxWorkerConfigInfo()
	if err == nil {
		t.Fatalf("GetNginxWorkerConfigInfo() = %+v, want an error for a missing nginx.conf", info)
	}
	if info != nil {
		t.Errorf("info = %+v, want nil on error", info)
	}
}

func TestGetNginxWorkerConfigInfo_WorkerProcessesAuto(t *testing.T) {
	writeConf(t, "worker_processes auto;\nevents {\n    worker_connections 65535;\n}\n")

	info, err := GetNginxWorkerConfigInfo()
	if err != nil {
		t.Fatalf("GetNginxWorkerConfigInfo() error = %v", err)
	}

	if info.ProcessMode != "auto" {
		t.Errorf("ProcessMode = %q, want %q", info.ProcessMode, "auto")
	}
	if info.WorkerProcesses != runtime.NumCPU() {
		t.Errorf("WorkerProcesses = %d, want runtime.NumCPU() = %d", info.WorkerProcesses, runtime.NumCPU())
	}
	if info.WorkerConnections != 65535 {
		t.Errorf("WorkerConnections = %d, want 65535", info.WorkerConnections)
	}
}

func TestGetNginxWorkerConfigInfo_TuningDirectives(t *testing.T) {
	writeConf(t, `
worker_processes 8;

events {
    worker_connections 4096;
}

http {
    keepalive_timeout 75s;
    gzip on;
    gzip_min_length 1024;
    gzip_comp_level 5;
    server_names_hash_bucket_size 64;
    client_max_body_size 2g;
    client_header_buffer_size 2k;
    client_body_buffer_size 128k;
}
`)

	info, err := GetNginxWorkerConfigInfo()
	if err != nil {
		t.Fatalf("GetNginxWorkerConfigInfo() error = %v", err)
	}

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"WorkerProcesses", info.WorkerProcesses, 8},
		{"ProcessMode", info.ProcessMode, "manual"},
		{"WorkerConnections", info.WorkerConnections, 4096},
		{"KeepaliveTimeout", info.KeepaliveTimeout, "75s"},
		{"Gzip", info.Gzip, "on"},
		{"GzipMinLength", info.GzipMinLength, 1024},
		{"GzipCompLevel", info.GzipCompLevel, 5},
		{"ServerNamesHashBucketSize", info.ServerNamesHashBucketSize, "64"},
		{"ClientMaxBodySize", info.ClientMaxBodySize, "2g"},
		{"ClientHeaderBufferSize", info.ClientHeaderBufferSize, "2k"},
		{"ClientBodyBufferSize", info.ClientBodyBufferSize, "128k"},
		{"ProxyCache.Enabled", info.ProxyCache.Enabled, false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
}

func TestGetNginxWorkerConfigInfo_ProxyCachePath(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    ProxyCacheConfig
	}{
		{
			name: "all parameters",
			content: `http {
    proxy_cache_path /data/nginx/cache levels=1:2 use_temp_path=on keys_zone=one:10m inactive=1d max_size=10g min_free=1g manager_files=50 manager_sleep=100ms manager_threshold=300ms loader_files=200 loader_sleep=20ms loader_threshold=100ms;
}
`,
			want: ProxyCacheConfig{
				Enabled:          true,
				Path:             "/data/nginx/cache",
				Levels:           "1:2",
				UseTempPath:      "on",
				KeysZone:         "one:10m",
				Inactive:         "1d",
				MaxSize:          "10g",
				MinFree:          "1g",
				ManagerFiles:     "50",
				ManagerSleep:     "100ms",
				ManagerThreshold: "300ms",
				LoaderFiles:      "200",
				LoaderSleep:      "20ms",
				LoaderThreshold:  "100ms",
			},
		},
		{
			name: "only required parameters keep the other defaults",
			content: `http {
    proxy_cache_path /tmp/cache keys_zone=two:1m;
}
`,
			want: ProxyCacheConfig{
				Enabled:     true,
				Path:        "/tmp/cache",
				Levels:      "1:2",
				UseTempPath: "off",
				KeysZone:    "two:1m",
				Inactive:    "60m",
				MaxSize:     "1g",
			},
		},
		{
			name: "commented proxy_cache_path is disabled",
			content: `http {
    # proxy_cache_path /data/nginx/cache keys_zone=one:10m;
}
`,
			want: ProxyCacheConfig{
				Enabled:     false,
				Path:        "/var/cache/nginx/proxy_cache",
				Levels:      "1:2",
				UseTempPath: "off",
				KeysZone:    "proxy_cache:10m",
				Inactive:    "60m",
				MaxSize:     "1g",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeConf(t, tt.content)

			info, err := GetNginxWorkerConfigInfo()
			if err != nil {
				t.Fatalf("GetNginxWorkerConfigInfo() error = %v", err)
			}
			if info.ProxyCache != tt.want {
				t.Errorf("ProxyCache = %+v, want %+v", info.ProxyCache, tt.want)
			}
		})
	}
}

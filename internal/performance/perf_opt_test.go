package performance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tufanbarisyildirim/gonginx/config"
	"github.com/tufanbarisyildirim/gonginx/dumper"
	"github.com/tufanbarisyildirim/gonginx/parser"
)

// applyPerfOpt runs the same parse -> update -> dump pipeline UpdatePerfOpt
// uses, without the final ngxConfig.Save that needs a database and an nginx
// binary for `nginx -t`.
func applyPerfOpt(t *testing.T, content string, opt *PerfOpt) string {
	t.Helper()

	conf, err := parser.NewStringParser(content, parser.WithSkipValidDirectivesErr()).Parse()
	if err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	updateNginxConfig(conf.Block, opt)

	return dumper.DumpBlock(conf.Block, dumper.IndentedStyle)
}

// readBack writes an updated configuration to nginx.conf and reads it through
// GetNginxWorkerConfigInfo, the same path the performance dialog loads from.
func readBack(t *testing.T, content string) *NginxConfigInfo {
	t.Helper()

	writeConf(t, content)

	info, err := GetNginxWorkerConfigInfo()
	if err != nil {
		t.Fatalf("GetNginxWorkerConfigInfo() error = %v", err)
	}

	return info
}

// findDirectives returns every directive with the given name in the http block
// of the dumped configuration.
func findHTTPDirectives(t *testing.T, content, name string) []config.IDirective {
	t.Helper()

	conf, err := parser.NewStringParser(content, parser.WithSkipValidDirectivesErr()).Parse()
	if err != nil {
		t.Fatalf("failed to parse dumped config: %v\n%s", err, content)
	}

	var found []config.IDirective
	for _, d := range conf.Block.GetDirectives() {
		if d.GetName() != "http" || d.GetBlock() == nil {
			continue
		}
		for _, hd := range d.GetBlock().GetDirectives() {
			if hd.GetName() == name {
				found = append(found, hd)
			}
		}
	}

	return found
}

func parameterValues(d config.IDirective) []string {
	values := make([]string, 0, len(d.GetParameters()))
	for _, p := range d.GetParameters() {
		values = append(values, p.Value)
	}
	return values
}

const perfOptBaseConf = `
worker_processes 1;

events {
    worker_connections 1024;
}

http {
    include mime.types;
    keepalive_timeout 65;
    gzip off;
    client_max_body_size 1m;

    server {
        listen 80;
        server_name localhost;
    }
}
`

func TestUpdateNginxConfig_UpdatesExistingDirectives(t *testing.T) {
	out := applyPerfOpt(t, perfOptBaseConf, &PerfOpt{
		WorkerProcesses:   "4",
		WorkerConnections: "4096",
		KeepaliveTimeout:  "30s",
		Gzip:              "on",
		ClientMaxBodySize: "128m",
	})

	info := readBack(t, out)

	if info.WorkerProcesses != 4 || info.ProcessMode != "manual" {
		t.Errorf("WorkerProcesses = %d (%s), want 4 (manual)", info.WorkerProcesses, info.ProcessMode)
	}
	if info.WorkerConnections != 4096 {
		t.Errorf("WorkerConnections = %d, want 4096", info.WorkerConnections)
	}
	if info.KeepaliveTimeout != "30s" {
		t.Errorf("KeepaliveTimeout = %q, want %q", info.KeepaliveTimeout, "30s")
	}
	if info.Gzip != "on" {
		t.Errorf("Gzip = %q, want %q", info.Gzip, "on")
	}
	if info.ClientMaxBodySize != "128m" {
		t.Errorf("ClientMaxBodySize = %q, want %q", info.ClientMaxBodySize, "128m")
	}

	// Existing directives are edited in place, not duplicated.
	for _, name := range []string{"keepalive_timeout", "gzip", "client_max_body_size"} {
		if got := len(findHTTPDirectives(t, out, name)); got != 1 {
			t.Errorf("%s appears %d times in the http block, want 1\n%s", name, got, out)
		}
	}

	// Unrelated content survives the rewrite.
	for _, want := range []string{"include mime.types;", "server_name localhost;", "listen 80;"} {
		if !strings.Contains(out, want) {
			t.Errorf("updated config lost %q:\n%s", want, out)
		}
	}
}

func TestUpdateNginxConfig_AddsMissingDirectives(t *testing.T) {
	const minimal = `
events {
}

http {
}
`

	out := applyPerfOpt(t, minimal, &PerfOpt{
		WorkerProcesses:           "auto",
		WorkerConnections:         "2048",
		KeepaliveTimeout:          "75",
		Gzip:                      "on",
		GzipMinLength:             "256",
		GzipCompLevel:             "6",
		ClientMaxBodySize:         "50M",
		ServerNamesHashBucketSize: "128",
		ClientHeaderBufferSize:    "4k",
		ClientBodyBufferSize:      "16k",
	})

	info := readBack(t, out)

	if info.ProcessMode != "auto" {
		t.Errorf("ProcessMode = %q, want %q", info.ProcessMode, "auto")
	}
	if info.WorkerConnections != 2048 {
		t.Errorf("WorkerConnections = %d, want 2048", info.WorkerConnections)
	}
	if info.KeepaliveTimeout != "75" {
		t.Errorf("KeepaliveTimeout = %q, want %q", info.KeepaliveTimeout, "75")
	}
	if info.Gzip != "on" {
		t.Errorf("Gzip = %q, want %q", info.Gzip, "on")
	}
	if info.GzipMinLength != 256 {
		t.Errorf("GzipMinLength = %d, want 256", info.GzipMinLength)
	}
	if info.GzipCompLevel != 6 {
		t.Errorf("GzipCompLevel = %d, want 6", info.GzipCompLevel)
	}
	if info.ClientMaxBodySize != "50M" {
		t.Errorf("ClientMaxBodySize = %q, want %q", info.ClientMaxBodySize, "50M")
	}
	if info.ServerNamesHashBucketSize != "128" {
		t.Errorf("ServerNamesHashBucketSize = %q, want %q", info.ServerNamesHashBucketSize, "128")
	}
	if info.ClientHeaderBufferSize != "4k" {
		t.Errorf("ClientHeaderBufferSize = %q, want %q", info.ClientHeaderBufferSize, "4k")
	}
	if info.ClientBodyBufferSize != "16k" {
		t.Errorf("ClientBodyBufferSize = %q, want %q", info.ClientBodyBufferSize, "16k")
	}

	// worker_connections belongs in events and the http tuning in http, not
	// in the main context.
	if got := len(findHTTPDirectives(t, out, "worker_connections")); got != 0 {
		t.Errorf("worker_connections was added to the http block:\n%s", out)
	}
	if got := len(findHTTPDirectives(t, out, "gzip_comp_level")); got != 1 {
		t.Errorf("gzip_comp_level appears %d times in the http block, want 1:\n%s", got, out)
	}
}

func TestUpdateNginxConfig_EmptyValuesLeaveConfigUntouched(t *testing.T) {
	out := applyPerfOpt(t, perfOptBaseConf, &PerfOpt{})

	info := readBack(t, out)

	if info.WorkerProcesses != 1 {
		t.Errorf("WorkerProcesses = %d, want 1", info.WorkerProcesses)
	}
	if info.WorkerConnections != 1024 {
		t.Errorf("WorkerConnections = %d, want 1024", info.WorkerConnections)
	}
	if info.KeepaliveTimeout != "65" {
		t.Errorf("KeepaliveTimeout = %q, want %q", info.KeepaliveTimeout, "65")
	}

	// An empty value means "not set by the dialog", so nothing new is written.
	for _, name := range []string{"gzip_min_length", "gzip_comp_level", "server_names_hash_bucket_size", "proxy_cache_path"} {
		if got := len(findHTTPDirectives(t, out, name)); got != 0 {
			t.Errorf("%s was added for an empty value:\n%s", name, out)
		}
	}
}

func TestUpdateNginxConfig_NilBlock(t *testing.T) {
	// Must not panic.
	updateNginxConfig(nil, &PerfOpt{WorkerProcesses: "2"})
}

func TestUpdateNginxConfig_ProxyCache(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "proxy_cache")

	out := applyPerfOpt(t, perfOptBaseConf, &PerfOpt{
		ProxyCache: ProxyCacheConfig{
			Enabled:          true,
			Path:             cacheDir,
			Levels:           "1:2",
			UseTempPath:      "off",
			KeysZone:         "my_cache:10m",
			Inactive:         "60m",
			MaxSize:          "1g",
			MinFree:          "100m",
			ManagerFiles:     "100",
			ManagerSleep:     "50ms",
			ManagerThreshold: "200ms",
			LoaderFiles:      "100",
			LoaderSleep:      "50ms",
			LoaderThreshold:  "200ms",
		},
	})

	directives := findHTTPDirectives(t, out, "proxy_cache_path")
	if len(directives) != 1 {
		t.Fatalf("proxy_cache_path appears %d times, want 1:\n%s", len(directives), out)
	}

	want := []string{
		cacheDir,
		"levels=1:2",
		"use_temp_path=off",
		"keys_zone=my_cache:10m",
		"inactive=60m",
		"max_size=1g",
		"min_free=100m",
		"manager_files=100",
		"manager_sleep=50ms",
		"manager_threshold=200ms",
		"loader_files=100",
		"loader_sleep=50ms",
		"loader_threshold=200ms",
	}
	if got := parameterValues(directives[0]); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("proxy_cache_path parameters = %v, want %v", got, want)
	}

	// The cache directory is created so nginx -t does not fail on it.
	if fi, err := os.Stat(cacheDir); err != nil || !fi.IsDir() {
		t.Errorf("cache directory %s was not created: %v", cacheDir, err)
	}

	// Reading the written config back yields the same cache settings.
	info := readBack(t, out)
	pc := info.ProxyCache
	if !pc.Enabled || pc.Path != cacheDir || pc.KeysZone != "my_cache:10m" || pc.MaxSize != "1g" || pc.LoaderThreshold != "200ms" {
		t.Errorf("ProxyCache read back = %+v", pc)
	}
}

func TestUpdateNginxConfig_ProxyCacheOnlyRequiredParameters(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")

	out := applyPerfOpt(t, perfOptBaseConf, &PerfOpt{
		ProxyCache: ProxyCacheConfig{
			Enabled:  true,
			Path:     cacheDir,
			KeysZone: "z:1m",
		},
	})

	directives := findHTTPDirectives(t, out, "proxy_cache_path")
	if len(directives) != 1 {
		t.Fatalf("proxy_cache_path appears %d times, want 1:\n%s", len(directives), out)
	}

	want := []string{cacheDir, "keys_zone=z:1m"}
	if got := parameterValues(directives[0]); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("proxy_cache_path parameters = %v, want %v", got, want)
	}
}

func TestUpdateNginxConfig_ProxyCacheReplacesExisting(t *testing.T) {
	const withCache = `
events {
    worker_connections 1024;
}

http {
    gzip on;
    proxy_cache_path /old/cache levels=1 keys_zone=old:1m;
}
`
	cacheDir := filepath.Join(t.TempDir(), "new_cache")

	out := applyPerfOpt(t, withCache, &PerfOpt{
		ProxyCache: ProxyCacheConfig{
			Enabled:  true,
			Path:     cacheDir,
			KeysZone: "new:5m",
		},
	})

	directives := findHTTPDirectives(t, out, "proxy_cache_path")
	if len(directives) != 1 {
		t.Fatalf("proxy_cache_path appears %d times, want exactly 1:\n%s", len(directives), out)
	}

	want := []string{cacheDir, "keys_zone=new:5m"}
	if got := parameterValues(directives[0]); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("proxy_cache_path parameters = %v, want %v", got, want)
	}
	if strings.Contains(out, "/old/cache") {
		t.Errorf("old proxy_cache_path is still present:\n%s", out)
	}
}

func TestUpdateNginxConfig_ProxyCacheDisabledRemovesDirective(t *testing.T) {
	const withCache = `
events {
    worker_connections 1024;
}

http {
    gzip on;
    proxy_cache_path /var/cache/nginx levels=1:2 keys_zone=c:10m;
    keepalive_timeout 65;
}
`

	out := applyPerfOpt(t, withCache, &PerfOpt{
		ProxyCache: ProxyCacheConfig{Enabled: false},
	})

	if got := len(findHTTPDirectives(t, out, "proxy_cache_path")); got != 0 {
		t.Errorf("proxy_cache_path was not removed:\n%s", out)
	}
	// Neighbouring directives are kept.
	if got := len(findHTTPDirectives(t, out, "gzip")); got != 1 {
		t.Errorf("gzip appears %d times, want 1:\n%s", got, out)
	}
	if got := len(findHTTPDirectives(t, out, "keepalive_timeout")); got != 1 {
		t.Errorf("keepalive_timeout appears %d times, want 1:\n%s", got, out)
	}

	if info := readBack(t, out); info.ProxyCache.Enabled {
		t.Errorf("ProxyCache.Enabled = true after disabling")
	}
}

func TestUpdateNginxConfig_ProxyCacheMissingRequiredParameters(t *testing.T) {
	tests := []struct {
		name     string
		withPath bool
		keysZone string
	}{
		{name: "no path", withPath: false, keysZone: "z:1m"},
		{name: "no keys_zone", withPath: true, keysZone: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := ProxyCacheConfig{Enabled: true, KeysZone: tt.keysZone}
			if tt.withPath {
				cache.Path = filepath.Join(t.TempDir(), "cache")
			}

			out := applyPerfOpt(t, perfOptBaseConf, &PerfOpt{ProxyCache: cache})

			// nginx rejects proxy_cache_path without a path or keys_zone, so
			// the directive must not be written at all.
			if got := len(findHTTPDirectives(t, out, "proxy_cache_path")); got != 0 {
				t.Errorf("proxy_cache_path written without a required parameter:\n%s", out)
			}
		})
	}
}

func TestUpdatePerfOpt_ReadAndParseErrors(t *testing.T) {
	t.Run("missing nginx.conf", func(t *testing.T) {
		setConfigPath(t, filepath.Join(t.TempDir(), "does-not-exist.conf"))

		err := UpdatePerfOpt(&PerfOpt{WorkerProcesses: "2"})
		if err == nil || !strings.Contains(err.Error(), "failed to read nginx.conf") {
			t.Errorf("UpdatePerfOpt() error = %v, want a read error", err)
		}
	})

	t.Run("unparseable nginx.conf", func(t *testing.T) {
		writeConf(t, "http {\n    gzip on;\n")

		err := UpdatePerfOpt(&PerfOpt{WorkerProcesses: "2"})
		if err == nil || !strings.Contains(err.Error(), "failed to parse nginx.conf") {
			t.Errorf("UpdatePerfOpt() error = %v, want a parse error", err)
		}
	})
}

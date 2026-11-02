package version

import (
	"testing"

	"github.com/0xJacky/Nginx-UI/settings"
)

func useGithubProxy(t *testing.T, proxy string) {
	t.Helper()
	previous := settings.HTTPSettings.GithubProxy
	settings.HTTPSettings.GithubProxy = proxy
	t.Cleanup(func() { settings.HTTPSettings.GithubProxy = previous })
}

func TestAPIUrlsGoThroughCloudflareWorker(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "dev commit", got: GetGithubDevCommitAPIUrl(), want: "https://cloud.nginxui.com/https://api.github.com/repos/0xJacky/nginx-ui/commits/dev?per_page=1"},
		{name: "latest release", got: GetGithubLatestReleaseAPIUrl(), want: "https://cloud.nginxui.com/https://api.github.com/repos/0xJacky/nginx-ui/releases/latest"},
		{name: "release list", got: GetGithubReleasesListAPIUrl(), want: "https://cloud.nginxui.com/https://api.github.com/repos/0xJacky/nginx-ui/releases"},
		{name: "worker root", got: GetCloudflareWorkerAPIUrl(), want: "https://cloud.nginxui.com/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestAPIUrlsIgnoreGithubProxySetting(t *testing.T) {
	// Release metadata is always fetched through the worker; only download
	// URLs built with GetUrl honour the user's GitHub proxy.
	useGithubProxy(t, "https://mirror.example.com/")

	if got, want := GetGithubLatestReleaseAPIUrl(), CloudflareWorkerAPI+GithubLatestReleaseAPI; got != want {
		t.Fatalf("GetGithubLatestReleaseAPIUrl() = %q, want %q", got, want)
	}
}

func TestGetUrl(t *testing.T) {
	const download = "https://github.com/0xJacky/nginx-ui/releases/download/v2.0.0/nginx-ui-linux-64.tar.gz"

	tests := []struct {
		name  string
		proxy string
		path  string
		want  string
	}{
		{name: "default worker", proxy: "", path: download, want: "https://cloud.nginxui.com/" + download},
		{name: "proxy with trailing slash", proxy: "https://mirror.example.com/", path: download, want: "https://mirror.example.com/" + download},
		{name: "proxy without trailing slash", proxy: "https://mirror.example.com", path: download, want: "https://mirror.example.com/" + download},
		{name: "proxy with sub path", proxy: "https://mirror.example.com/gh/", path: download, want: "https://mirror.example.com/gh/" + download},
		{name: "only one trailing slash is trimmed", proxy: "https://mirror.example.com//", path: "file", want: "https://mirror.example.com//file"},
		{name: "empty path", proxy: "https://mirror.example.com", path: "", want: "https://mirror.example.com/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useGithubProxy(t, tt.proxy)
			if got := GetUrl(tt.path); got != tt.want {
				t.Fatalf("GetUrl(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

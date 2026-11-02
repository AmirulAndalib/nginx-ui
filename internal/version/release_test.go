package version

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xJacky/Nginx-UI/settings"
	"github.com/uozi-tech/cosy"
)

const (
	latestReleasePath = "/https://api.github.com/repos/0xJacky/nginx-ui/releases/latest"
	releaseListPath   = "/https://api.github.com/repos/0xJacky/nginx-ui/releases"
	devCommitPath     = "/https://api.github.com/repos/0xJacky/nginx-ui/commits/dev"
	devBuildsPath     = "/dev-builds"
)

type stubResponse struct {
	status int
	body   string
}

// releaseAPIStub stands in for the Cloudflare worker. Requests made through
// http.DefaultClient are redirected to a local httptest server, so the real
// release URLs are exercised without any external network access.
type releaseAPIStub struct {
	mu        sync.Mutex
	responses map[string]stubResponse
	requested []string
}

type redirectTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	redirected := req.Clone(req.Context())
	redirected.URL.Scheme = rt.target.Scheme
	redirected.URL.Host = rt.target.Host
	redirected.Host = rt.target.Host
	return rt.base.RoundTrip(redirected)
}

func newReleaseAPIStub(t *testing.T, responses map[string]stubResponse) *releaseAPIStub {
	t.Helper()
	stub := &releaseAPIStub{responses: responses}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.requested = append(stub.requested, r.URL.RequestURI())
		resp, ok := stub.responses[r.URL.Path]
		stub.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(srv.Close)

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse stub URL: %v", err)
	}
	installDefaultTransport(t, redirectTransport{target: target, base: srv.Client().Transport})
	return stub
}

func (s *releaseAPIStub) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requested...)
}

// installDefaultTransport makes NewHTTPClient's default client use rt and
// guarantees no proxy setting diverts it to a different client.
func installDefaultTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	prevTransport := http.DefaultClient.Transport
	prevProxy := settings.HTTPSettings.HTTPProxy
	prevProvider := releaseProvider
	http.DefaultClient.Transport = rt
	settings.HTTPSettings.HTTPProxy = ""
	releaseProvider = nil
	t.Cleanup(func() {
		http.DefaultClient.Transport = prevTransport
		settings.HTTPSettings.HTTPProxy = prevProxy
		releaseProvider = prevProvider
	})
}

type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Errorf("unexpected outbound request to %s", req.URL)
	return nil, errors.New("network disabled in test")
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

func respondOK(body string) stubResponse { return stubResponse{status: http.StatusOK, body: body} }

func assertCosyCode(t *testing.T, err error, code int32) *cosy.Error {
	t.Helper()
	var cErr *cosy.Error
	if !errors.As(err, &cErr) || cErr.Code != code {
		t.Fatalf("error = %v, want cosy error with code %d", err, code)
	}
	return cErr
}

var (
	stableDate = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	stableJSON = `{
		"tag_name": "v2.0.0",
		"name": "v2.0.0",
		"published_at": "2026-05-01T12:00:00Z",
		"body": "stable notes",
		"prerelease": false,
		"html_url": "https://github.com/0xJacky/nginx-ui/releases/tag/v2.0.0",
		"assets": [
			{"name": "nginx-ui-linux-64.tar.gz", "browser_download_url": "https://example.com/linux.tar.gz", "size": 1024},
			{"name": "nginx-ui-linux-64.tar.gz.digest", "browser_download_url": "https://example.com/linux.digest", "size": 64}
		]
	}`
)

func TestGetAssetsMap(t *testing.T) {
	tests := []struct {
		name   string
		assets []TReleaseAsset
		want   map[string]TReleaseAsset
	}{
		{name: "nil assets", assets: nil, want: map[string]TReleaseAsset{}},
		{
			name:   "unique names",
			assets: []TReleaseAsset{{Name: "a", Size: 1}, {Name: "b", Size: 2}},
			want:   map[string]TReleaseAsset{"a": {Name: "a", Size: 1}, "b": {Name: "b", Size: 2}},
		},
		{
			name:   "duplicate names keep the last asset",
			assets: []TReleaseAsset{{Name: "a", Size: 1}, {Name: "a", Size: 2}},
			want:   map[string]TReleaseAsset{"a": {Name: "a", Size: 2}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := TRelease{Assets: tt.assets}
			got := release.GetAssetsMap()
			if got == nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("GetAssetsMap() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestNewHTTPClient(t *testing.T) {
	prevProxy := settings.HTTPSettings.HTTPProxy
	t.Cleanup(func() { settings.HTTPSettings.HTTPProxy = prevProxy })

	t.Run("no proxy uses the default client", func(t *testing.T) {
		settings.HTTPSettings.HTTPProxy = ""
		client, err := NewHTTPClient()
		if err != nil {
			t.Fatalf("NewHTTPClient() error = %v", err)
		}
		if client != http.DefaultClient {
			t.Fatal("NewHTTPClient() should return http.DefaultClient when no proxy is configured")
		}
	})

	t.Run("proxy is applied to the transport", func(t *testing.T) {
		settings.HTTPSettings.HTTPProxy = "http://proxy.example.com:8080"
		client, err := NewHTTPClient()
		if err != nil {
			t.Fatalf("NewHTTPClient() error = %v", err)
		}
		if client == http.DefaultClient {
			t.Fatal("NewHTTPClient() returned the shared default client despite a proxy")
		}
		transport, isTransport := client.Transport.(*http.Transport)
		if !isTransport || transport.Proxy == nil {
			t.Fatalf("client transport = %T, want *http.Transport with a proxy func", client.Transport)
		}
		req := httptest.NewRequest(http.MethodGet, "https://api.github.com/", nil)
		proxyURL, err := transport.Proxy(req)
		if err != nil || proxyURL == nil || proxyURL.String() != "http://proxy.example.com:8080" {
			t.Fatalf("proxy for request = %v, %v; want http://proxy.example.com:8080", proxyURL, err)
		}
	})

	for _, invalid := range []string{"http://[::1", "://missing-scheme"} {
		t.Run("invalid proxy "+invalid, func(t *testing.T) {
			settings.HTTPSettings.HTTPProxy = invalid
			client, err := NewHTTPClient()
			if err == nil || client != nil {
				t.Fatalf("NewHTTPClient() = %v, %v; want nil client and an error", client, err)
			}
		})
	}
}

func TestGetReleaseStable(t *testing.T) {
	for _, channel := range []string{"stable", "", "unknown-channel"} {
		t.Run("channel="+channel, func(t *testing.T) {
			stub := newReleaseAPIStub(t, map[string]stubResponse{latestReleasePath: respondOK(stableJSON)})

			release, err := GetRelease(channel)
			if err != nil {
				t.Fatalf("GetRelease(%q) error = %v", channel, err)
			}
			if release.TagName != "v2.0.0" || release.Body != "stable notes" || !release.PublishedAt.Equal(stableDate) {
				t.Fatalf("GetRelease(%q) = %+v, want the v2.0.0 stable release", channel, release)
			}
			if release.Type != ReleaseTypeStable {
				t.Fatalf("Type = %q, want %q", release.Type, ReleaseTypeStable)
			}
			assets := release.GetAssetsMap()
			if asset := assets["nginx-ui-linux-64.tar.gz"]; asset.BrowserDownloadUrl != "https://example.com/linux.tar.gz" || asset.Size != 1024 {
				t.Fatalf("linux asset = %+v, want parsed download URL and size", asset)
			}
			if got := stub.requests(); !reflect.DeepEqual(got, []string{latestReleasePath}) {
				t.Fatalf("requests = %v, want only the latest release lookup", got)
			}
		})
	}
}

func TestGetReleaseStableMarksTypeEvenForPrereleasePayload(t *testing.T) {
	newReleaseAPIStub(t, map[string]stubResponse{
		latestReleasePath: respondOK(`{"tag_name": "v2.1.0-beta.1", "prerelease": true}`),
	})

	release, err := GetRelease(string(ReleaseTypeStable))
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if release.Type != ReleaseTypeStable || !release.Prerelease {
		t.Fatalf("release = %+v, want Type stable with the upstream prerelease flag preserved", release)
	}
}

func TestGetReleaseLatestErrors(t *testing.T) {
	tests := []struct {
		name     string
		response stubResponse
		check    func(t *testing.T, err error)
	}{
		{
			name:     "non-200 status",
			response: stubResponse{status: http.StatusForbidden, body: "rate limited"},
			check: func(t *testing.T, err error) {
				cErr := assertCosyCode(t, err, 53002)
				if !strings.Contains(cErr.Error(), "rate limited") {
					t.Fatalf("error = %q, want the response body in the message", cErr.Error())
				}
			},
		},
		{
			name:     "invalid JSON",
			response: respondOK("{not json"),
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "json.Unmarshal") {
					t.Fatalf("error = %v, want a JSON decode error", err)
				}
			},
		},
	}

	for _, tt := range tests {
		for _, channel := range []ReleaseType{ReleaseTypeStable, ReleaseTypePrerelease, ReleaseTypeDev} {
			t.Run(tt.name+"/"+string(channel), func(t *testing.T) {
				stub := newReleaseAPIStub(t, map[string]stubResponse{latestReleasePath: tt.response})

				release, err := GetRelease(string(channel))
				tt.check(t, err)
				if !reflect.DeepEqual(release, TRelease{}) {
					t.Fatalf("release = %+v, want zero value on error", release)
				}
				// Every channel needs the stable release first, so a failure there
				// must stop before any channel-specific lookup.
				if got := stub.requests(); len(got) != 1 {
					t.Fatalf("requests = %v, want a single latest release lookup", got)
				}
			})
		}
	}
}

func TestGetReleaseTransportError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	target, _ := url.Parse(srv.URL)
	srv.Close() // connections to a closed server fail immediately
	installDefaultTransport(t, redirectTransport{target: target, base: &http.Transport{}})

	_, err := GetRelease(string(ReleaseTypeStable))
	if err == nil || !strings.Contains(err.Error(), "http.Get") {
		t.Fatalf("GetRelease() error = %v, want a wrapped request error", err)
	}
}

func TestGetReleasePrerelease(t *testing.T) {
	type entry struct {
		TagName     string    `json:"tag_name"`
		Prerelease  bool      `json:"prerelease"`
		PublishedAt time.Time `json:"published_at"`
	}

	tests := []struct {
		name    string
		list    []entry
		wantTag string
		want    ReleaseType
	}{
		{
			name: "newest prerelease is newer than stable",
			list: []entry{
				{TagName: "v2.1.0-beta.1", Prerelease: true, PublishedAt: stableDate.Add(24 * time.Hour)},
				{TagName: "v2.1.0-beta.2", Prerelease: true, PublishedAt: stableDate.Add(48 * time.Hour)},
				// Newer still, but not a prerelease, so it is ignored here.
				{TagName: "v2.0.1", Prerelease: false, PublishedAt: stableDate.Add(72 * time.Hour)},
				{TagName: "v2.0.0", Prerelease: false, PublishedAt: stableDate},
			},
			wantTag: "v2.1.0-beta.2",
			want:    ReleaseTypePrerelease,
		},
		{
			name: "list order does not matter",
			list: []entry{
				{TagName: "v2.1.0-beta.2", Prerelease: true, PublishedAt: stableDate.Add(48 * time.Hour)},
				{TagName: "v2.1.0-beta.1", Prerelease: true, PublishedAt: stableDate.Add(24 * time.Hour)},
			},
			wantTag: "v2.1.0-beta.2",
			want:    ReleaseTypePrerelease,
		},
		{
			name: "stable is newer than every prerelease",
			list: []entry{
				{TagName: "v2.0.0-rc.1", Prerelease: true, PublishedAt: stableDate.Add(-24 * time.Hour)},
			},
			wantTag: "v2.0.0",
			want:    ReleaseTypeStable,
		},
		{
			name: "prerelease published at the same time as stable",
			list: []entry{
				{TagName: "v2.0.0-rc.9", Prerelease: true, PublishedAt: stableDate},
			},
			wantTag: "v2.0.0",
			want:    ReleaseTypeStable,
		},
		{
			name:    "no prereleases",
			list:    []entry{{TagName: "v2.0.0", Prerelease: false, PublishedAt: stableDate}},
			wantTag: "v2.0.0",
			want:    ReleaseTypeStable,
		},
		{name: "empty list", list: []entry{}, wantTag: "v2.0.0", want: ReleaseTypeStable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := newReleaseAPIStub(t, map[string]stubResponse{
				latestReleasePath: respondOK(stableJSON),
				releaseListPath:   respondOK(mustJSON(t, tt.list)),
			})

			release, err := GetRelease(string(ReleaseTypePrerelease))
			if err != nil {
				t.Fatalf("GetRelease() error = %v", err)
			}
			if release.TagName != tt.wantTag || release.Type != tt.want {
				t.Fatalf("GetRelease() = %s (%s), want %s (%s)", release.TagName, release.Type, tt.wantTag, tt.want)
			}
			if got := stub.requests(); !reflect.DeepEqual(got, []string{latestReleasePath, releaseListPath}) {
				t.Fatalf("requests = %v, want latest release then release list", got)
			}
		})
	}
}

func TestGetReleasePrereleaseListErrors(t *testing.T) {
	tests := []struct {
		name     string
		response stubResponse
		check    func(t *testing.T, err error)
	}{
		{
			name:     "non-200 status",
			response: stubResponse{status: http.StatusBadGateway, body: "upstream down"},
			check: func(t *testing.T, err error) {
				cErr := assertCosyCode(t, err, 53002)
				if !strings.Contains(cErr.Error(), "upstream down") {
					t.Fatalf("error = %q, want the response body in the message", cErr.Error())
				}
			},
		},
		{
			name:     "invalid JSON",
			response: respondOK(`{"not": "a list"}`),
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "json.Unmarshal") {
					t.Fatalf("error = %v, want a JSON decode error", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newReleaseAPIStub(t, map[string]stubResponse{
				latestReleasePath: respondOK(stableJSON),
				releaseListPath:   tt.response,
			})

			release, err := GetRelease(string(ReleaseTypePrerelease))
			tt.check(t, err)
			if !reflect.DeepEqual(release, TRelease{}) {
				t.Fatalf("release = %+v, want zero value on error", release)
			}
		})
	}
}

func TestGetReleaseDev(t *testing.T) {
	commitDate := time.Date(2026, 6, 2, 8, 30, 0, 0, time.UTC)
	stub := newReleaseAPIStub(t, map[string]stubResponse{
		latestReleasePath: respondOK(stableJSON),
		devCommitPath: respondOK(`{
			"sha": "0123456789abcdef0123456789abcdef01234567",
			"commit": {"message": "fix: something", "committer": {"date": "2026-06-02T08:30:00Z"}}
		}`),
		devBuildsPath: respondOK(`[{"name": "nginx-ui-linux-64.tar.gz", "browser_download_url": "https://example.com/dev.tar.gz", "size": 2048}]`),
	})

	release, err := GetRelease(string(ReleaseTypeDev))
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}

	want := TRelease{
		TagName:     "sha-0123456",
		Name:        "sha-0123456",
		Body:        "fix: something",
		Type:        ReleaseTypeDev,
		PublishedAt: commitDate,
		HTMLURL:     "https://github.com/0xJacky/Nginx-UI/commit/0123456789abcdef0123456789abcdef01234567",
		Assets: []TReleaseAsset{
			{Name: "nginx-ui-linux-64.tar.gz", BrowserDownloadUrl: "https://example.com/dev.tar.gz", Size: 2048},
		},
	}
	if !release.PublishedAt.Equal(want.PublishedAt) {
		t.Fatalf("PublishedAt = %v, want %v", release.PublishedAt, want.PublishedAt)
	}
	release.PublishedAt = want.PublishedAt
	if !reflect.DeepEqual(release, want) {
		t.Fatalf("GetRelease() = %+v, want %+v", release, want)
	}

	wantRequests := []string{latestReleasePath, devCommitPath + "?per_page=1", devBuildsPath}
	if got := stub.requests(); !reflect.DeepEqual(got, wantRequests) {
		t.Fatalf("requests = %v, want %v", got, wantRequests)
	}
}

func TestGetReleaseDevErrors(t *testing.T) {
	validCommit := `{"sha": "0123456789abcdef", "commit": {"message": "m"}}`
	validBuilds := `[]`

	tests := []struct {
		name   string
		commit stubResponse
		builds stubResponse
		check  func(t *testing.T, err error)
	}{
		{
			name:   "commit SHA too short",
			commit: respondOK(`{"sha": "abc123"}`),
			builds: respondOK(validBuilds),
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrInvalidCommitSHA) {
					t.Fatalf("error = %v, want ErrInvalidCommitSHA", err)
				}
			},
		},
		{
			name:   "commit endpoint error status",
			commit: stubResponse{status: http.StatusForbidden, body: `{"message": "API rate limit exceeded"}`},
			builds: respondOK(validBuilds),
			check: func(t *testing.T, err error) {
				cErr := assertCosyCode(t, err, 53002)
				if !strings.Contains(cErr.Error(), "API rate limit exceeded") {
					t.Fatalf("error = %v, want the response body in the message", err)
				}
			},
		},
		{
			name:   "dev builds endpoint error status",
			commit: respondOK(validCommit),
			builds: stubResponse{status: http.StatusBadGateway, body: "bad gateway"},
			check: func(t *testing.T, err error) {
				assertCosyCode(t, err, 53002)
			},
		},
		{
			name:   "commit invalid JSON",
			commit: respondOK("{not json"),
			builds: respondOK(validBuilds),
			check: func(t *testing.T, err error) {
				var syntaxErr *json.SyntaxError
				if !errors.As(err, &syntaxErr) {
					t.Fatalf("error = %v, want a JSON syntax error", err)
				}
			},
		},
		{
			name:   "dev builds invalid JSON",
			commit: respondOK(validCommit),
			builds: respondOK(`{"not": "a list"}`),
			check: func(t *testing.T, err error) {
				var typeErr *json.UnmarshalTypeError
				if !errors.As(err, &typeErr) {
					t.Fatalf("error = %v, want a JSON type error", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newReleaseAPIStub(t, map[string]stubResponse{
				latestReleasePath: respondOK(stableJSON),
				devCommitPath:     tt.commit,
				devBuildsPath:     tt.builds,
			})

			release, err := GetRelease(string(ReleaseTypeDev))
			tt.check(t, err)
			if !reflect.DeepEqual(release, TRelease{}) {
				t.Fatalf("release = %+v, want zero value on error", release)
			}
		})
	}
}

type fakeReleaseProvider struct {
	releases map[string]TRelease
	asked    []string
}

func (p *fakeReleaseProvider) Release(channel string) (TRelease, bool) {
	p.asked = append(p.asked, channel)
	r, found := p.releases[channel]
	return r, found
}

func TestGetReleaseUsesProvider(t *testing.T) {
	installDefaultTransport(t, failingTransport{t: t})
	provider := &fakeReleaseProvider{releases: map[string]TRelease{
		"stable": {TagName: "v9.9.9", Type: ReleaseTypeStable},
		"dev":    {TagName: "sha-demo", Type: ReleaseTypeDev},
	}}
	SetReleaseProvider(provider)

	for channel, wantTag := range map[string]string{"stable": "v9.9.9", "dev": "sha-demo"} {
		release, err := GetRelease(channel)
		if err != nil {
			t.Fatalf("GetRelease(%q) error = %v", channel, err)
		}
		if release.TagName != wantTag {
			t.Fatalf("GetRelease(%q) tag = %q, want %q", channel, release.TagName, wantTag)
		}
	}
}

func TestGetReleaseFallsThroughWhenProviderDeclines(t *testing.T) {
	stub := newReleaseAPIStub(t, map[string]stubResponse{latestReleasePath: respondOK(stableJSON)})
	provider := &fakeReleaseProvider{releases: map[string]TRelease{}}
	SetReleaseProvider(provider)

	release, err := GetRelease("stable")
	if err != nil {
		t.Fatalf("GetRelease() error = %v", err)
	}
	if release.TagName != "v2.0.0" {
		t.Fatalf("GetRelease() tag = %q, want the upstream v2.0.0", release.TagName)
	}
	if !reflect.DeepEqual(provider.asked, []string{"stable"}) {
		t.Fatalf("provider asked for %v, want [stable]", provider.asked)
	}
	if len(stub.requests()) != 1 {
		t.Fatalf("requests = %v, want one upstream lookup after the provider declined", stub.requests())
	}
}

func TestGetReleaseInvalidProxySetting(t *testing.T) {
	installDefaultTransport(t, failingTransport{t: t})
	settings.HTTPSettings.HTTPProxy = "http://[::1"

	for _, channel := range []ReleaseType{ReleaseTypeStable, ReleaseTypePrerelease, ReleaseTypeDev} {
		release, err := GetRelease(string(channel))
		if err == nil {
			t.Fatalf("GetRelease(%q) error = nil, want the proxy parse error", channel)
		}
		if !reflect.DeepEqual(release, TRelease{}) {
			t.Fatalf("GetRelease(%q) = %+v, want zero value on error", channel, release)
		}
	}
}

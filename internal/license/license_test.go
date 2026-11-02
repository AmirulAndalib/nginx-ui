package license

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

// useLicenseData swaps the embedded archive for the duration of a test.
func useLicenseData(t *testing.T, data []byte) {
	t.Helper()
	previous := compressedLicenses
	compressedLicenses = data
	t.Cleanup(func() { compressedLicenses = previous })
}

func compress(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatalf("create xz writer: %v", err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("write xz data: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close xz writer: %v", err)
	}
	return buf.Bytes()
}

func compressJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal license fixture: %v", err)
	}
	return compress(t, raw)
}

func TestEmbeddedLicenseDataIsConsistent(t *testing.T) {
	info, err := GetLicenseInfo()
	if err != nil {
		t.Fatalf("GetLicenseInfo() error = %v", err)
	}
	if len(info.Backend) == 0 || len(info.Frontend) == 0 {
		t.Fatalf("embedded data has %d backend and %d frontend entries, want both non-empty", len(info.Backend), len(info.Frontend))
	}
	for _, l := range append(append([]License{}, info.Backend...), info.Frontend...) {
		if l.Name == "" {
			t.Fatalf("embedded license entry without a name: %+v", l)
		}
	}

	backend, err := GetBackendLicenses()
	if err != nil {
		t.Fatalf("GetBackendLicenses() error = %v", err)
	}
	if !reflect.DeepEqual(backend, info.Backend) {
		t.Fatal("GetBackendLicenses() differs from GetLicenseInfo().Backend")
	}

	frontend, err := GetFrontendLicenses()
	if err != nil {
		t.Fatalf("GetFrontendLicenses() error = %v", err)
	}
	if !reflect.DeepEqual(frontend, info.Frontend) {
		t.Fatal("GetFrontendLicenses() differs from GetLicenseInfo().Frontend")
	}

	stats, err := GetLicenseStats()
	if err != nil {
		t.Fatalf("GetLicenseStats() error = %v", err)
	}
	if stats["total_backend"] != len(info.Backend) || stats["total_frontend"] != len(info.Frontend) ||
		stats["total"] != len(info.Backend)+len(info.Frontend) {
		t.Fatalf("stats totals = %v/%v/%v, want %d/%d/%d", stats["total_backend"], stats["total_frontend"], stats["total"],
			len(info.Backend), len(info.Frontend), len(info.Backend)+len(info.Frontend))
	}
	distribution, ok := stats["license_distribution"].(map[string]int)
	if !ok {
		t.Fatalf("license_distribution has type %T, want map[string]int", stats["license_distribution"])
	}
	sum := 0
	for _, n := range distribution {
		sum += n
	}
	if sum != stats["total"] {
		t.Fatalf("license_distribution sums to %d, want %v", sum, stats["total"])
	}
}

func TestLicenseFunctionsWithFixture(t *testing.T) {
	fixture := ComponentInfo{
		Backend: []License{
			{Name: "a", License: "MIT", URL: "https://example.com/a", Version: "v1.0.0"},
			{Name: "b", License: "Apache-2.0", URL: "https://example.com/b", Version: "v2.0.0"},
			{Name: "c", License: "MIT", URL: "https://example.com/c", Version: "v3.0.0"},
		},
		Frontend: []License{
			{Name: "d", License: "MIT", URL: "https://example.com/d", Version: "1.0.0"},
			{Name: "e", License: "", URL: "https://example.com/e", Version: "2.0.0"},
		},
	}
	useLicenseData(t, compressJSON(t, fixture))

	info, err := GetLicenseInfo()
	if err != nil {
		t.Fatalf("GetLicenseInfo() error = %v", err)
	}
	if !reflect.DeepEqual(*info, fixture) {
		t.Fatalf("GetLicenseInfo() = %+v, want %+v", *info, fixture)
	}

	backend, err := GetBackendLicenses()
	if err != nil || !reflect.DeepEqual(backend, fixture.Backend) {
		t.Fatalf("GetBackendLicenses() = %+v, %v; want %+v", backend, err, fixture.Backend)
	}
	frontend, err := GetFrontendLicenses()
	if err != nil || !reflect.DeepEqual(frontend, fixture.Frontend) {
		t.Fatalf("GetFrontendLicenses() = %+v, %v; want %+v", frontend, err, fixture.Frontend)
	}

	stats, err := GetLicenseStats()
	if err != nil {
		t.Fatalf("GetLicenseStats() error = %v", err)
	}
	want := map[string]interface{}{
		"total_backend":  3,
		"total_frontend": 2,
		"total":          5,
		// Backend and frontend counts are merged; an empty license string is
		// counted under its own key rather than dropped.
		"license_distribution": map[string]int{"MIT": 3, "Apache-2.0": 1, "": 1},
	}
	if !reflect.DeepEqual(stats, want) {
		t.Fatalf("GetLicenseStats() = %v, want %v", stats, want)
	}
}

func TestLicenseFunctionsWithEmptyLists(t *testing.T) {
	useLicenseData(t, compress(t, []byte(`{}`)))

	backend, err := GetBackendLicenses()
	if err != nil || len(backend) != 0 {
		t.Fatalf("GetBackendLicenses() = %v, %v; want empty, nil", backend, err)
	}
	stats, err := GetLicenseStats()
	if err != nil {
		t.Fatalf("GetLicenseStats() error = %v", err)
	}
	if stats["total"] != 0 || len(stats["license_distribution"].(map[string]int)) != 0 {
		t.Fatalf("GetLicenseStats() = %v, want zero totals and empty distribution", stats)
	}
}

func TestLicenseFunctionsReportBadData(t *testing.T) {
	valid := compress(t, []byte(`{"backend":[],"frontend":[]}`))

	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{name: "no data", data: nil, wantErr: "no license data available"},
		{name: "not xz", data: []byte("plain text"), wantErr: "failed to create xz reader"},
		{name: "truncated xz stream", data: valid[:len(valid)-8], wantErr: "failed to decompress license data"},
		{name: "invalid JSON", data: compress(t, []byte("{not json")), wantErr: "failed to parse license data"},
		{name: "wrong JSON shape", data: compress(t, []byte(`{"backend":"oops"}`)), wantErr: "failed to parse license data"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useLicenseData(t, tt.data)

			calls := map[string]func() (any, error){
				"GetLicenseInfo":      func() (any, error) { return GetLicenseInfo() },
				"GetBackendLicenses":  func() (any, error) { return GetBackendLicenses() },
				"GetFrontendLicenses": func() (any, error) { return GetFrontendLicenses() },
				"GetLicenseStats":     func() (any, error) { return GetLicenseStats() },
			}
			for name, call := range calls {
				got, err := call()
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("%s() error = %v, want it to contain %q", name, err, tt.wantErr)
				}
				if v := reflect.ValueOf(got); v.IsValid() && !v.IsNil() {
					t.Fatalf("%s() returned %v alongside an error, want nil", name, got)
				}
			}
		})
	}
}

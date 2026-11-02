package config

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
)

func TestListConfigsReturnsEmbeddedProviders(t *testing.T) {
	filenames, err := ListConfigs()
	require.NoError(t, err)
	require.NotEmpty(t, filenames)

	assert.Contains(t, filenames, "cloudflare.toml")
	assert.Contains(t, filenames, "alidns.toml")

	seen := make(map[string]bool, len(filenames))
	for _, name := range filenames {
		assert.False(t, seen[name], "duplicate config %s", name)
		seen[name] = true
		assert.True(t, strings.HasSuffix(name, ".toml"), "unexpected config file %s", name)
		assert.NotContains(t, name, "/", "config %s should live at the archive root", name)
	}
}

func TestListConfigsReturnsIndependentSlices(t *testing.T) {
	first, err := ListConfigs()
	require.NoError(t, err)
	require.NotEmpty(t, first)

	first[0] = "mutated.toml"

	second, err := ListConfigs()
	require.NoError(t, err)
	assert.NotContains(t, second, "mutated.toml")
}

func TestGetConfigReturnsFileContent(t *testing.T) {
	data, err := GetConfig("cloudflare.toml")
	require.NoError(t, err)

	var provider struct {
		Name string
		Code string
	}
	require.NoError(t, toml.Unmarshal(data, &provider))
	assert.Equal(t, "Cloudflare", provider.Name)
	assert.Equal(t, "cloudflare", provider.Code)
}

// Every embedded file must decode as TOML and carry a unique provider code,
// because internal/cert/dns indexes providers by code at startup.
func TestEveryConfigIsValidTOMLWithUniqueCode(t *testing.T) {
	filenames, err := ListConfigs()
	require.NoError(t, err)

	codes := make(map[string]string, len(filenames))
	for _, name := range filenames {
		data, err := GetConfig(name)
		require.NoError(t, err, name)
		require.NotEmpty(t, data, name)

		var provider struct {
			Name string
			Code string
		}
		require.NoError(t, toml.Unmarshal(data, &provider), name)
		assert.NotEmpty(t, provider.Name, "%s has no Name", name)
		require.NotEmpty(t, provider.Code, "%s has no Code", name)

		if other, ok := codes[provider.Code]; ok {
			t.Errorf("code %q is declared by both %s and %s", provider.Code, other, name)
		}
		codes[provider.Code] = name
	}
}

func TestGetConfigUnknownFile(t *testing.T) {
	tests := []struct {
		name     string
		filename string
	}{
		{name: "empty name", filename: ""},
		{name: "missing provider", filename: "does-not-exist.toml"},
		{name: "missing extension", filename: "cloudflare"},
		{name: "case sensitive", filename: "Cloudflare.toml"},
		{name: "path traversal", filename: "../cloudflare.toml"},
		{name: "nested path", filename: "config/cloudflare.toml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := GetConfig(tt.filename)
			require.Error(t, err)
			assert.Nil(t, data)
			assert.Contains(t, err.Error(), "config file "+tt.filename+" not found")
		})
	}
}

func TestDecompressConfigsMatchesInitializedStore(t *testing.T) {
	files, err := decompressConfigs()
	require.NoError(t, err)

	filenames, err := ListConfigs()
	require.NoError(t, err)
	assert.Len(t, files, len(filenames))

	for name, data := range files {
		stored, err := GetConfig(name)
		require.NoError(t, err, name)
		assert.Equal(t, data, stored, name)
	}
}

// withCompressedData swaps the embedded archive for the duration of a test.
func withCompressedData(t *testing.T, data []byte) {
	t.Helper()
	original := compressedData
	compressedData = data
	t.Cleanup(func() { compressedData = original })
}

func xzCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	require.NoError(t, err)
	_, err = w.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestDecompressConfigsKeepsOnlyRegularFiles(t *testing.T) {
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "nested/", Typeflag: tar.TypeDir, Mode: 0o755}))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "link.toml", Typeflag: tar.TypeSymlink, Linkname: "a.toml"}))
	content := []byte("Code = \"a\"\n")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "a.toml", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "empty.toml", Typeflag: tar.TypeReg, Mode: 0o644}))
	require.NoError(t, tw.Close())

	withCompressedData(t, xzCompress(t, archive.Bytes()))

	files, err := decompressConfigs()
	require.NoError(t, err)
	assert.Equal(t, map[string][]byte{
		"a.toml":     content,
		"empty.toml": {},
	}, files)
}

func TestDecompressConfigsRejectsCorruptArchives(t *testing.T) {
	tests := []struct {
		name    string
		data    func(t *testing.T) []byte
		wantErr string
	}{
		{
			name:    "not xz",
			data:    func(t *testing.T) []byte { return []byte("plain text, not an xz stream") },
			wantErr: "failed to create xz reader",
		},
		{
			name:    "xz without tar",
			data:    func(t *testing.T) []byte { return xzCompress(t, bytes.Repeat([]byte("x"), 1024)) },
			wantErr: "failed to read tar header",
		},
		{
			name: "truncated tar entry",
			data: func(t *testing.T) []byte {
				var archive bytes.Buffer
				tw := tar.NewWriter(&archive)
				require.NoError(t, tw.WriteHeader(&tar.Header{Name: "a.toml", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4096}))
				_, err := tw.Write([]byte("short"))
				require.NoError(t, err)
				// tar.Writer writes through, so skipping Close leaves the entry
				// shorter than its declared size.
				return xzCompress(t, archive.Bytes())
			},
			wantErr: "failed to read file a.toml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withCompressedData(t, tt.data(t))

			files, err := decompressConfigs()
			require.Error(t, err)
			assert.Nil(t, files)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

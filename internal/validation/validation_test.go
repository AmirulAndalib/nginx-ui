package validation

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xJacky/Nginx-UI/settings"
	"github.com/gin-gonic/gin/binding"
)

type testPEMs struct {
	certificate string
	rsaPKCS1Key string
	ecSEC1Key   string
	pkcs8Key    string
}

// newTestPEMs builds a self-signed certificate plus one private key in each
// encoding the "privatekey" rule is expected to accept.
func newTestPEMs(t *testing.T) testPEMs {
	t.Helper()

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate EC key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "example.com"},
		DNSNames:     []string{"example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &ecKey.PublicKey, ecKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		t.Fatalf("marshal EC key: %v", err)
	}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	pkcs8DER, err := x509.MarshalPKCS8PrivateKey(edKey)
	if err != nil {
		t.Fatalf("marshal PKCS8 key: %v", err)
	}

	encode := func(blockType string, b []byte) string {
		return string(pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: b}))
	}

	return testPEMs{
		certificate: encode("CERTIFICATE", der),
		rsaPKCS1Key: encode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(rsaKey)),
		ecSEC1Key:   encode("EC PRIVATE KEY", ecDER),
		pkcs8Key:    encode("PRIVATE KEY", pkcs8DER),
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

type certificatePayload struct {
	Value string `binding:"certificate"`
}

type privateKeyPayload struct {
	Value string `binding:"privatekey"`
}

type certificatePathPayload struct {
	Value string `binding:"certificate_path"`
}

type privateKeyPathPayload struct {
	Value string `binding:"privatekey_path"`
}

type keyTypePayload struct {
	Value string `binding:"auto_cert_key_type"`
}

type redactedPayload struct {
	Value string `binding:"redacted"`
}

type optionalCertificatePayload struct {
	Value string `binding:"omitempty,certificate"`
}

// validate runs a payload through gin's binding validator, which is the
// engine Init registers the custom rules on.
func validate(t *testing.T, payload any) bool {
	t.Helper()
	Init()
	return binding.Validator.ValidateStruct(payload) == nil
}

func TestCertificateRule(t *testing.T) {
	pems := newTestPEMs(t)

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "valid certificate", value: pems.certificate, want: true},
		{name: "private key is not a certificate", value: pems.ecSEC1Key, want: false},
		{name: "empty", value: "", want: false},
		{name: "plain text", value: "not a certificate", want: false},
		{name: "certificate block with garbage body", value: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("garbage")})), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validate(t, certificatePayload{Value: tt.value}); got != tt.want {
				t.Fatalf("valid = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCertificateRuleWithOmitEmpty(t *testing.T) {
	if !validate(t, optionalCertificatePayload{}) {
		t.Fatal("empty value should pass when omitempty precedes certificate")
	}
	if validate(t, optionalCertificatePayload{Value: "not a certificate"}) {
		t.Fatal("non-empty invalid value should still fail with omitempty")
	}
}

func TestPrivateKeyRule(t *testing.T) {
	pems := newTestPEMs(t)

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "RSA PKCS1 key", value: pems.rsaPKCS1Key, want: true},
		{name: "EC SEC1 key", value: pems.ecSEC1Key, want: true},
		{name: "PKCS8 key", value: pems.pkcs8Key, want: true},
		{name: "certificate is not a private key", value: pems.certificate, want: false},
		{name: "empty", value: "", want: false},
		{name: "plain text", value: "not a key", want: false},
		{name: "key block with garbage body", value: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("garbage")})), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validate(t, privateKeyPayload{Value: tt.value}); got != tt.want {
				t.Fatalf("valid = %v, want %v", got, tt.want)
			}
		})
	}
}

// useManagedCertDir points the nginx configuration directory at a temporary
// directory. Only files under it are read and checked; any other path is
// treated as external and accepted without being opened.
func useManagedCertDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	original := settings.NginxSettings.ConfigDir
	settings.NginxSettings.ConfigDir = dir
	t.Cleanup(func() { settings.NginxSettings.ConfigDir = original })
	return dir
}

func TestCertificatePathRule(t *testing.T) {
	pems := newTestPEMs(t)
	dir := useManagedCertDir(t)
	externalJunk := writeFile(t, t.TempDir(), "junk.pem", "junk")
	certPath := writeFile(t, dir, "cert.pem", pems.certificate)
	keyPath := writeFile(t, dir, "key.pem", pems.ecSEC1Key)
	junkPath := writeFile(t, dir, "junk.pem", "junk")

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "file containing a certificate", value: certPath, want: true},
		// A missing file is accepted so the certificate can be written there later.
		{name: "missing file", value: filepath.Join(dir, "missing.pem"), want: true},
		{name: "file containing a private key", value: keyPath, want: false},
		{name: "file containing junk", value: junkPath, want: false},
		{name: "directory", value: dir, want: false},
		{name: "empty", value: "", want: false},
		// Files outside the nginx configuration directory are never opened.
		{name: "external path is not read", value: externalJunk, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validate(t, certificatePathPayload{Value: tt.value}); got != tt.want {
				t.Fatalf("valid = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrivateKeyPathRule(t *testing.T) {
	pems := newTestPEMs(t)
	dir := useManagedCertDir(t)
	externalJunk := writeFile(t, t.TempDir(), "junk.pem", "junk")
	keyPath := writeFile(t, dir, "key.pem", pems.ecSEC1Key)
	certPath := writeFile(t, dir, "cert.pem", pems.certificate)
	junkPath := writeFile(t, dir, "junk.pem", "junk")

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "file containing a private key", value: keyPath, want: true},
		// A missing file is accepted so the key can be written there later.
		{name: "missing file", value: filepath.Join(dir, "missing.key"), want: true},
		{name: "file containing a certificate", value: certPath, want: false},
		{name: "file containing junk", value: junkPath, want: false},
		{name: "directory", value: dir, want: false},
		{name: "empty", value: "", want: false},
		// Files outside the nginx configuration directory are never opened.
		{name: "external path is not read", value: externalJunk, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validate(t, privateKeyPathPayload{Value: tt.value}); got != tt.want {
				t.Fatalf("valid = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutoCertKeyTypeRule(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		// Empty means "use the default key type".
		{value: "", want: true},
		{value: "RSA2048", want: true},
		{value: "RSA3072", want: true},
		{value: "RSA4096", want: true},
		{value: "RSA8192", want: true},
		{value: "EC256", want: true},
		{value: "EC384", want: true},
		// Legacy spellings stored by older releases.
		{value: "2048", want: true},
		{value: "3072", want: true},
		{value: "4096", want: true},
		{value: "8192", want: true},
		{value: "P256", want: true},
		{value: "P384", want: true},
		{value: "1024", want: false},
		{value: "RSA1024", want: false},
		{value: "EC521", want: false},
		{value: "P521", want: false},
		{value: "rsa2048", want: false},
		{value: " RSA2048", want: false},
		{value: "unknown", want: false},
	}

	for _, tt := range tests {
		t.Run("value="+tt.value, func(t *testing.T) {
			if got := validate(t, keyTypePayload{Value: tt.value}); got != tt.want {
				t.Fatalf("valid = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRedactedRule(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "redaction placeholder", value: settings.RedactedSensitiveValue, want: true},
		{name: "empty", value: "", want: false},
		{name: "real secret", value: "s3cr3t", want: false},
		{name: "placeholder with whitespace", value: " " + settings.RedactedSensitiveValue, want: false},
		{name: "placeholder prefix", value: settings.RedactedSensitiveValue[:len(settings.RedactedSensitiveValue)-1], want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validate(t, redactedPayload{Value: tt.value}); got != tt.want {
				t.Fatalf("valid = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRedactedRuleInSliceDive mirrors how settings accept either a real IP or
// the redaction placeholder for each list item.
func TestRedactedRuleInSliceDive(t *testing.T) {
	type payload struct {
		Values []string `binding:"omitempty,dive,ip|redacted"`
	}

	tests := []struct {
		name   string
		values []string
		want   bool
	}{
		{name: "nil list", values: nil, want: true},
		{name: "real IPs", values: []string{"127.0.0.1", "::1"}, want: true},
		{name: "placeholder only", values: []string{settings.RedactedSensitiveValue}, want: true},
		{name: "IP and placeholder", values: []string{"10.0.0.1", settings.RedactedSensitiveValue}, want: true},
		{name: "invalid item", values: []string{"10.0.0.1", "not-an-ip"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validate(t, payload{Values: tt.values}); got != tt.want {
				t.Fatalf("valid = %v, want %v", got, tt.want)
			}
		})
	}
}

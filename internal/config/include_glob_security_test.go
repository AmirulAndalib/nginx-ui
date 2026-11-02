package config

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/uozi-tech/cosy"
)

// nginx resolves include patterns with glob(3) and opens arguments without
// "*?[" literally. The validator resolves them with Go's Glob, so any pattern
// the two read differently must either be translated or rejected; otherwise
// the files nginx loads through it are never validated.

func requireIncludeGlobUnsupportedError(t *testing.T, err error, wantPattern string) {
	t.Helper()
	cosyErr, ok := errors.AsType[*cosy.Error](err)
	if !ok {
		t.Fatalf("expected cosy error, got %v", err)
	}
	wantErr, _ := errors.AsType[*cosy.Error](ErrConfigIncludeGlobUnsupported)
	if cosyErr.Scope != wantErr.Scope || cosyErr.Code != wantErr.Code {
		t.Fatalf("expected cosy error %s:%d, got %s:%d (%v)", wantErr.Scope, wantErr.Code, cosyErr.Scope, cosyErr.Code, err)
	}
	if len(cosyErr.Params) != 1 || cosyErr.Params[0] != wantPattern {
		t.Fatalf("expected params [%q], got %v", wantPattern, cosyErr.Params)
	}
}

func TestIncludeGlobBracketExpressionsAreRejected(t *testing.T) {
	confDir := setupTokenizerConfDir(t)
	path := filepath.Join(confDir, "conf.d", "app.conf")
	writeIncludeTestFile(t, filepath.Join(confDir, "conf.d", "mod-perl.inc"), "js_import m.js;\n")

	for _, pattern := range []string{
		"mod-[[:alpha:]]erl.inc",
		"[[:alnum:]]od-perl.inc",
		"mod-[[:lower:]]erl.inc",
		"mod-[[.p.]]erl.inc",
		"mod-[[=p=]]erl.inc",
		"mod-[x[:alpha:]]erl.inc",
		"mod-[![:digit:]]erl.inc",
		"mod-[^[:digit:]]erl.inc",
		"mod-[a-[.z.]]erl.inc",
		"*/mod-[[:alpha:]]erl.inc",
	} {
		t.Run("absolute/"+pattern, func(t *testing.T) {
			full := filepath.Join(confDir, "conf.d", pattern)
			requireIncludeGlobUnsupportedError(t, ValidateConfigFile(path, "include "+full+";\n"), full)
		})
	}

	t.Run("relative", func(t *testing.T) {
		requireIncludeGlobUnsupportedError(t,
			ValidateConfigFile(path, "include conf.d/mod-[[:alpha:]]erl.inc;\n"),
			filepath.Join(confDir, "conf.d", "mod-[[:alpha:]]erl.inc"))
	})

	t.Run("nested include", func(t *testing.T) {
		inner := filepath.Join(confDir, "conf.d", "inner.inc")
		full := filepath.Join(confDir, "conf.d", "mod-[[:alpha:]]erl.inc")
		writeIncludeTestFile(t, inner, "include "+full+";\n")
		requireIncludeGlobUnsupportedError(t, ValidateConfigFile(path, "include "+inner+";\n"), full)
	})
}

func TestIncludeLiteralBackslashIsValidated(t *testing.T) {
	confDir := setupTokenizerConfDir(t)
	path := filepath.Join(confDir, "conf.d", "app.conf")
	writeIncludeTestFile(t, filepath.Join(confDir, "conf.d", `mod-\perl.inc`), "js_import m.js;\n")

	// Without any of "*?[" nginx opens the argument as is, and its tokenizer
	// keeps a backslash unless a quote, a backslash, t, r or n follows.
	dir := filepath.Join(confDir, "conf.d")
	for name, content := range map[string]string{
		"unquoted":          "include " + dir + `/mod-\perl.inc;` + "\n",
		"double quoted":     `include "` + dir + `/mod-\perl.inc";` + "\n",
		"single quoted":     `include '` + dir + `/mod-\perl.inc';` + "\n",
		"escaped backslash": "include " + dir + `/mod-\\perl.inc;` + "\n",
		// nginx reads "\\\\" as "\\", which glob(3) matches as one backslash.
		"glob": "include " + dir + `/mod-\\\\perl.in?;` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			requireRestrictedDirectiveError(t, ValidateConfigFile(path, content), "js_import")
		})
	}
}

func TestIncludeGlobSupportedPatternsAreValidated(t *testing.T) {
	confDir := setupTokenizerConfDir(t)
	path := filepath.Join(confDir, "conf.d", "app.conf")
	writeIncludeTestFile(t, filepath.Join(confDir, "conf.d", "mod-perl.inc"), "js_import m.js;\n")
	writeIncludeTestFile(t, filepath.Join(confDir, "conf.d", "[lit]!.inc"), "js_import m.js;\n")

	for _, pattern := range []string{
		"mod-perl.inc",
		"mod-*.inc",
		"mod-????.inc",
		"mod-[!x]erl.inc",
		"mod-[^x]erl.inc",
		"mod-[a-z]erl.inc",
		"mod-[p]erl.inc",
		"[!.]od-perl.inc",
		"mod-[.p]erl.inc",
		"mod-[:p]erl.inc",
		`mod-\perl.in?`,
		`\[lit\]!.in?`,
	} {
		t.Run(pattern, func(t *testing.T) {
			content := "include " + filepath.Join(confDir, "conf.d", pattern) + ";\n"
			requireRestrictedDirectiveError(t, ValidateConfigFile(path, content), "js_import")
		})
	}
}

func TestIncludeGlobKeepsStockLayoutsValid(t *testing.T) {
	confDir := setupTokenizerConfDir(t)
	for _, name := range []string{
		"mime.types",
		"fastcgi_params",
		"conf.d/default.conf",
		"sites-enabled/default",
		"sites-enabled/example.com",
		"modules-enabled/50-mod-http-geoip.conf",
		"snippets/ssl-params.conf",
		"conf.d/.hidden.conf",
	} {
		writeIncludeTestFile(t, filepath.Join(confDir, name), "# stock file\n")
	}

	for name, content := range map[string]string{
		"stock nginx.conf": "user nginx;\nevents {}\nhttp {\n    include mime.types;\n    include " +
			filepath.Join(confDir, "conf.d") + "/*.conf;\n}\n",
		"debian nginx.conf": "include " + filepath.Join(confDir, "modules-enabled") + "/*.conf;\nevents {}\nhttp {\n" +
			"    include " + filepath.Join(confDir, "mime.types") + ";\n" +
			"    include " + filepath.Join(confDir, "conf.d") + "/*.conf;\n" +
			"    include " + filepath.Join(confDir, "sites-enabled") + "/*;\n}\n",
		"relative includes": "http { include mime.types; include conf.d/*.conf; include sites-enabled/*; include snippets/ssl-params.conf; }\n",
		"site fastcgi":      "server { location ~ \\.php$ { include fastcgi_params; } }\n",
		"glob classes":      "http { include conf.d/[!.]*.conf; include conf.d/[a-z]*.conf; include sites-enabled/[^x]*; include sites-enabled/?xample.com; }\n",
		"missing targets":   "http { include missing.d/*.conf; include missing.conf; include conf.d/[[]missing; }\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateConfigFile(filepath.Join(confDir, "nginx.conf"), content); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestVisitStatementsWithIncludesUsesGlibcGlobSyntax(t *testing.T) {
	root := useDataValidationRoot(t)
	writeIncludeTestFile(t, filepath.Join(root, "snippets", "a.conf"), "ssl_certificate a.cer;\n")
	writeIncludeTestFile(t, filepath.Join(root, "snippets", `b\c.conf`), "ssl_certificate b.cer;\n")

	content := "include snippets/[!.]*.conf;\ninclude " + filepath.Join(root, "snippets", `b\c.conf`) +
		";\ninclude snippets/[[:alpha:]].conf;\n"
	got := collectIncludedStatements("", content)
	want := []string{
		"0:include snippets/[!.]*.conf",
		"1:ssl_certificate a.cer",
		"1:ssl_certificate b.cer",
		"0:include " + filepath.Join(root, "snippets", `b\c.conf`),
		"1:ssl_certificate b.cer",
		"0:include snippets/[[:alpha:]].conf",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statements = %#v, want %#v", got, want)
	}
}

func TestGlibcGlobPattern(t *testing.T) {
	for pattern, want := range map[string]string{
		"conf.d/*.conf":   "conf.d/*.conf",
		"[!.]*.conf":      "[^.]*.conf",
		"[^x]":            "[^x]",
		"[a!]":            "[a!]",
		"[a[!]":           "[a[!]",
		`\[!x]`:           `\[!x]`,
		`[\!x]`:           `[\!x]`,
		"[.]*.conf":       "[.]*.conf",
		"[:]":             "[:]",
		"a[!b]c[!d]":      "a[^b]c[^d]",
		"unterminated[!a": "unterminated[^a",
	} {
		got, err := glibcGlobPattern(pattern)
		if err != nil || got != want {
			t.Errorf("glibcGlobPattern(%q) = %q, %v; want %q", pattern, got, err, want)
		}
	}
	for _, pattern := range []string{"[[:alpha:]]", "[a[.b.]]", "[[=e=]]", "[![:space:]]", `[\][:alpha:]]`} {
		if _, err := glibcGlobPattern(pattern); err == nil {
			t.Errorf("glibcGlobPattern(%q) = nil error, want rejection", pattern)
		}
	}
}

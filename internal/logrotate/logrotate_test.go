package logrotate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/0xJacky/Nginx-UI/settings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupLogrotate swaps in the given logrotate settings for the test.
func setupLogrotate(t *testing.T, enabled bool, cmd string) {
	t.Helper()
	original := *settings.LogrotateSettings
	t.Cleanup(func() {
		*settings.LogrotateSettings = original
	})
	settings.LogrotateSettings.Enabled = enabled
	settings.LogrotateSettings.CMD = cmd
}

// writeRecorderScript creates a shell script that writes its arguments, one
// per line, into the file named by its first argument. It stands in for the
// logrotate binary so the test can see how Exec invoked it.
func writeRecorderScript(t *testing.T) (script, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the recorder script needs a POSIX shell")
	}
	dir = t.TempDir()
	if strings.ContainsAny(dir, " \t") {
		t.Skip("Exec splits the command on spaces; the temp dir path contains one")
	}
	script = filepath.Join(dir, "fake-logrotate")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nout=\"$1\"\nshift\nprintf '%s\\n' \"$@\" > \"$out\"\n"), 0o755))
	return script, dir
}

func TestExecRunsConfiguredCommandWithArguments(t *testing.T) {
	script, dir := writeRecorderScript(t)
	record := filepath.Join(dir, "args.txt")
	setupLogrotate(t, true, script+" "+record+" -f /etc/logrotate.d/nginx")

	Exec()

	got, err := os.ReadFile(record)
	require.NoError(t, err, "the configured command was not run")
	assert.Equal(t, "-f\n/etc/logrotate.d/nginx\n", string(got))
}

func TestExecDoesNothingWhenDisabled(t *testing.T) {
	script, dir := writeRecorderScript(t)
	record := filepath.Join(dir, "args.txt")
	setupLogrotate(t, false, script+" "+record)

	Exec()

	_, err := os.Stat(record)
	assert.ErrorIs(t, err, os.ErrNotExist, "the command ran although logrotate is disabled")
}

func TestExecToleratesFailingCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
	}{
		{name: "empty command", cmd: ""},
		{name: "missing binary", cmd: filepath.Join(t.TempDir(), "does-not-exist") + " --force"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupLogrotate(t, true, tt.cmd)

			assert.NotPanics(t, Exec)
		})
	}
}

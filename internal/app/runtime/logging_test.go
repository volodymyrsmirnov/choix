package runtime

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/appdir"
)

// setupLoggingForTest runs SetupLogging and restores the default logger
// (and closes the file) at the end of the test.
func setupLoggingForTest(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	closer, err := SetupLogging()
	if err != nil {
		t.Fatalf("SetupLogging: %v", err)
	}
	t.Cleanup(func() {
		slog.SetDefault(prev)
		_ = closer.Close()
	})
}

func logPath(t *testing.T) string {
	t.Helper()
	dir, err := appdir.Logs()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "choix.log")
}

func TestSetupLogging_WritesUnderLogsAndAppends(t *testing.T) {
	t.Setenv("CHOIX_DATA_DIR", t.TempDir())
	p := logPath(t)
	if err := os.WriteFile(p, []byte("earlier line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	setupLoggingForTest(t)
	slog.Info("runtime logging test", "k", "v")

	data, err := os.ReadFile(p) //nolint:gosec // the test's own log file
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("earlier line\n")) {
		t.Fatalf("log = %q; want the earlier content kept", data)
	}
	if !strings.Contains(string(data), "msg=\"runtime logging test\" k=v") {
		t.Fatalf("log = %q; want the record in slog's text format", data)
	}
}

func TestSetupLogging_TruncatesALargeLog(t *testing.T) {
	t.Setenv("CHOIX_DATA_DIR", t.TempDir())
	p := logPath(t)
	big := bytes.Repeat([]byte("x"), maxLogSize+1)
	if err := os.WriteFile(p, big, 0o600); err != nil {
		t.Fatal(err)
	}

	setupLoggingForTest(t)
	slog.Info("after truncation")

	data, err := os.ReadFile(p) //nolint:gosec // the test's own log file
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4096 || bytes.Contains(data, []byte("xxxx")) || !bytes.Contains(data, []byte("after truncation")) {
		t.Fatalf("log after start-up is %d bytes; want the old content dropped and the new record kept", len(data))
	}
}

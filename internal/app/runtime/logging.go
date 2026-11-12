package runtime

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/volodymyrsmirnov/choix/internal/appdir"
)

// logFileName is the log file inside appdir.Logs().
const logFileName = "choix.log"

// maxLogSize is the size above which the log is started afresh at launch.
const maxLogSize = 5 << 20

// SetupLogging makes slog's default logger a text handler writing to
// appdir.Logs()/choix.log and to stderr. The file is appended to, unless it
// is larger than 5 MB, in which case it is truncated first. The returned
// Closer closes the file; log nothing after closing it.
func SetupLogging() (io.Closer, error) {
	dir, err := appdir.Logs()
	if err != nil {
		return nil, fmt.Errorf("runtime: logs directory: %w", err)
	}
	path := filepath.Join(dir, logFileName)
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogSize {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600) //nolint:gosec // G304: the app's own log file in its logs directory
	if err != nil {
		return nil, fmt.Errorf("runtime: open log file: %w", err)
	}
	h := slog.NewTextHandler(io.MultiWriter(f, os.Stderr), &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))
	return f, nil
}

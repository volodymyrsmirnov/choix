package server

import (
	"errors"
	"os/exec"
	"runtime"
)

// OpenBrowser launches the default browser for url. macOS uses /usr/bin/open.
// On other platforms it returns an error so the caller can fall back to
// printing the URL.
//
// execCommand is a seam for tests so we don't actually launch a browser.
var execCommand = exec.Command

// OpenBrowser opens url in the system browser. On macOS it uses /usr/bin/open.
// On other platforms it returns a non-nil error.
func OpenBrowser(url string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("auto-open supported on macOS only (v1)")
	}
	cmd := execCommand("open", url)
	return cmd.Start()
}

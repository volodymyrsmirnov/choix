// Package appdir centralizes the on-disk location of Choix's app-level
// state: config.toml, the library database, preview caches and logs. Everything lives under
// <NSApplicationSupportDirectory, user domain>/Choix — inside the sandbox
// container when the app is sandboxed — and logs under
// <NSLibraryDirectory>/Logs/Choix.
//
// Non-shipping builds (no `production` tag, or the `server` tag) honour
// CHOIX_DATA_DIR as a replacement root for every path here, logs
// included, so tests and dev runs never touch the real data directory.
package appdir

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const appName = "Choix"

// folderIDPattern is the shape of a library folder id: 16 lower-case hex
// digits. Anything else could escape the previews directory.
var folderIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// Root returns the data root: the CHOIX_DATA_DIR override when the build
// allows it and it is set, otherwise <Application Support>/Choix. The
// directory is created (0700) on every call.
func Root() (string, error) {
	root, err := rootPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	return root, nil
}

// rootPath returns the data root as Root does, without creating it.
func rootPath() (string, error) {
	root, err := dataOverride()
	if err != nil {
		return "", err
	}
	if root == "" {
		support, err := applicationSupportDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(support, appName)
	}
	return root, nil
}

// Config returns Root/config.toml.
func Config() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "config.toml"), nil
}

// LegacyModels returns Root/models, where builds before the built-in
// grouping and quality kept their downloaded models. It creates nothing,
// neither that directory nor the data root, and returns "" when the data
// root cannot be determined. Only the start-up clean-up of those files uses
// it.
func LegacyModels() string {
	r, err := rootPath()
	if err != nil {
		return ""
	}
	return filepath.Join(r, "models")
}

// LibraryDB returns Root/library.db. The file itself is not created.
func LibraryDB() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "library.db"), nil
}

// Previews returns Root/previews/<folderID>, creating it. folderID must be
// 16 lower-case hex digits; anything else is an error and creates nothing.
func Previews(folderID string) (string, error) {
	if !folderIDPattern.MatchString(folderID) {
		return "", fmt.Errorf("invalid folder id %q", folderID)
	}
	r, err := Root()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(r, "previews", folderID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create previews dir: %w", err)
	}
	return dir, nil
}

// PreviewsPath returns the directory Previews names, Root/previews/<folderID>,
// with the same check of folderID, but creates nothing — neither that
// directory nor the data root. For readers that treat a missing directory as
// "no previews" (the media handler) and for removing a folder's previews.
func PreviewsPath(folderID string) (string, error) {
	if !folderIDPattern.MatchString(folderID) {
		return "", fmt.Errorf("invalid folder id %q", folderID)
	}
	r, err := rootPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "previews", folderID), nil
}

// Logs returns the log directory, creating it: <override>/logs when the
// data root is overridden, otherwise <Library>/Logs/Choix.
func Logs() (string, error) {
	over, err := dataOverride()
	if err != nil {
		return "", err
	}
	var dir string
	if over != "" {
		dir = filepath.Join(over, "logs")
	} else {
		lib, err := libraryDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(lib, "Logs", appName)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create logs dir: %w", err)
	}
	return dir, nil
}

// Sandboxed reports whether the process runs inside the App Sandbox, i.e.
// whether APP_SANDBOX_CONTAINER_ID is set and non-empty. It reads libc's
// environment: libsecinit sets the variable with setenv() after the Go
// runtime has copied the environment, so os.Getenv never sees it.
func Sandboxed() bool {
	return sandboxContainerID() != ""
}

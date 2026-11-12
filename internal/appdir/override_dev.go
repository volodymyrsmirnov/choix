//go:build !production || server

package appdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// dataOverride returns the absolute CHOIX_DATA_DIR, or "" when it is unset
// or empty. Only non-shipping builds (and the `server` test harness build)
// honour it.
func dataOverride() (string, error) {
	v := os.Getenv("CHOIX_DATA_DIR")
	if v == "" {
		return "", nil
	}
	abs, err := filepath.Abs(v)
	if err != nil {
		return "", fmt.Errorf("CHOIX_DATA_DIR=%q: %w", v, err)
	}
	return abs, nil
}

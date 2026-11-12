package runtime

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/appdir"
)

// legacyModelNames are the model files builds before the built-in grouping
// and quality downloaded into the data directory's models directory. A file
// whose name starts with one of them (the download's lock and partial files)
// goes with it.
var legacyModelNames = []string{"clip_vit_b32.onnx", "topiq_nr_koniq_384.onnx"}

// cleanLegacyModels removes, best effort, the model files an earlier build
// left in the app's own data directory (spec §11), and logs once what it
// removed or why it could not. It is not part of the self-check.
func cleanLegacyModels() {
	dir := appdir.LegacyModels()
	removed, err := removeLegacyModels(dir)
	if len(removed) > 0 {
		slog.Info("runtime: removed the model files of an earlier build", "files", strings.Join(removed, ","))
	}
	if err != nil {
		slog.Warn("runtime: remove the model files of an earlier build", "err", err)
	}
}

// removeLegacyModels removes from dir every regular file or symbolic link
// whose name starts with one of legacyModelNames, then dir itself when that
// left it empty, and returns the names it removed. A link is removed as a
// link; its target is never touched. Anything else in dir stays. A missing
// dir, or a dir that is itself a symbolic link or not a directory, is left
// alone and is not an error. Failures do not stop it: the first is returned
// once everything else was tried.
func removeLegacyModels(dir string) (removed []string, err error) {
	if dir == "" {
		return nil, nil
	}
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("models directory: %w", err)
	case !fi.IsDir():
		return nil, nil // a symbolic link or a file: not ours to clean
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read models directory: %w", err)
	}
	var first error
	left := 0
	for _, e := range entries {
		if e.IsDir() || !isLegacyModel(e.Name()) {
			left++
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			if first == nil {
				first = fmt.Errorf("remove %s: %w", e.Name(), err)
			}
			left++
			continue
		}
		removed = append(removed, e.Name())
	}
	if left == 0 {
		// os.Remove refuses a directory that is not empty, so a file that
		// appeared meanwhile keeps it.
		if err := os.Remove(dir); err != nil && first == nil {
			first = fmt.Errorf("remove models directory: %w", err)
		}
	}
	return removed, first
}

// isLegacyModel reports whether name is one of legacyModelNames or starts
// with one.
func isLegacyModel(name string) bool {
	for _, m := range legacyModelNames {
		if strings.HasPrefix(name, m) {
			return true
		}
	}
	return false
}

package picks

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// ErrDirHasOriginals refuses a picks directory under which the database has
// scanned originals (e.g. picks_dir pointed at a folder of photos). Nothing
// is exported there and the scanner keeps walking it as originals.
var ErrDirHasOriginals = errors.New("picks: the picks folder holds photos Choix has scanned as originals")

// DirError reports a picks directory setting that can never be used.
// Reason is one of "empty", "absolute", "traversal" or "reserved".
type DirError struct{ Reason string }

func (e *DirError) Error() string {
	switch e.Reason {
	case "empty":
		return "picks: the picks folder is not set"
	case "absolute":
		return "picks: the picks folder must be a folder inside the photo folder, not an absolute path"
	case "traversal":
		return "picks: the picks folder must be inside the photo folder"
	case "reserved":
		return "picks: the picks folder cannot be the photo folder itself or Choix's .choix folder"
	}
	return "picks: unusable picks folder (" + e.Reason + ")"
}

// ValidateDir checks a picks directory setting (relative to the folder root):
// "" ⇒ empty; an absolute path ⇒ absolute; a path that is not
// filepath.IsLocal ⇒ traversal; ".", ".choix" or anything under ".choix"
// (case-insensitively, as APFS is) ⇒ reserved. The error is a *DirError.
func ValidateDir(p string) error {
	switch {
	case p == "":
		return &DirError{Reason: "empty"}
	case filepath.IsAbs(p) || strings.HasPrefix(p, "/"):
		return &DirError{Reason: "absolute"}
	case !filepath.IsLocal(p):
		return &DirError{Reason: "traversal"}
	}
	c := cleanDir(p)
	if c == "." || strings.EqualFold(c, ".choix") ||
		(len(c) > len(".choix/") && strings.EqualFold(c[:len(".choix/")], ".choix/")) {
		return &DirError{Reason: "reserved"}
	}
	return nil
}

// cleanDir normalises a validated picks directory to the clean,
// slash-separated form safefs.OpenDir and the store expect.
func cleanDir(p string) string {
	return path.Clean(filepath.ToSlash(p))
}

// EffectiveDir chooses the folder's picks directory: the most used export
// directory when any pick has been exported (so a folder last used with
// another picks_dir keeps exporting where its picks already are), else
// configured. others are the remaining export directories, which the scanner
// must skip as well.
func EffectiveDir(st *store.Store, configured string) (dir string, others []string, err error) {
	dirs, err := st.Picks().ExportDirs()
	if err != nil {
		return "", nil, fmt.Errorf("picks: effective directory: %w", err)
	}
	if len(dirs) == 0 {
		return configured, nil, nil
	}
	others = make([]string, 0, len(dirs)-1)
	for _, d := range dirs[1:] {
		others = append(others, d.Dir)
	}
	return dirs[0].Dir, others, nil
}

// CheckDir refuses dir (ErrDirHasOriginals) when the database has scanned
// originals under it — one SQL query, no directory walk. Files on disk the
// database does not know (an orphan copy, a file the user dropped in) and
// rows marked missing never block.
func CheckDir(st *store.Store, dir string) error {
	n, err := st.Files().CountUnderPath(cleanDir(dir))
	if err != nil {
		return fmt.Errorf("picks: check %s: %w", dir, err)
	}
	if n > 0 {
		return fmt.Errorf("%w (%d under %s)", ErrDirHasOriginals, n, dir)
	}
	return nil
}

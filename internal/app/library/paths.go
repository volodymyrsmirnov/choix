package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Relations between a folder already in the Library and a candidate path.
const (
	relSame     = "same"     // the candidate is the folder
	relInside   = "inside"   // the candidate lies inside the folder
	relContains = "contains" // the candidate contains the folder
)

// relation reports how candidate relates to the existing folder path: "same",
// "inside" (candidate is below existing), "contains" (existing is below
// candidate) or "" (unrelated). Both paths are cleaned and compared ignoring
// case and Unicode normalization (NFC and NFD spellings of a name are equal),
// as the default macOS volume format does; a path that merely shares a prefix
// ("/a/Trip2" against "/a/Trip") is unrelated.
func relation(existing, candidate string) string {
	e := foldPath(existing)
	c := foldPath(candidate)
	switch {
	case e == c:
		return relSame
	case within(c, e):
		return relInside
	case within(e, c):
		return relContains
	}
	return ""
}

// foldPath returns the form of p used for comparisons: cleaned, lower-cased,
// then in Unicode normalization form C.
func foldPath(p string) string {
	return norm.NFC.String(strings.ToLower(filepath.Clean(p)))
}

// canonicalPath returns p with every symbolic link resolved. When p does not
// exist, the links of its nearest existing ancestor are resolved and the
// missing elements are appended unchanged.
func canonicalPath(p string) (string, error) {
	p = filepath.Clean(p)
	rest := ""
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// within reports whether the folded path p lies strictly below the folded
// directory dir.
func within(p, dir string) bool {
	prefix := strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)
	return p != dir && strings.HasPrefix(p, prefix)
}

// resolveFolder returns the canonical form of the folder at path: cleaned and
// with every symbolic link resolved. path must be absolute; a path that is
// not a directory is ErrNotDirectory.
func resolveFolder(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("library: add %q: not an absolute path", path)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("library: add %q: %w", path, err)
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("library: add %q: %w", path, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("library: add %q: %w", path, ErrNotDirectory)
	}
	return resolved, nil
}

package media

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// safeJoinUnderRoot is v1's internal/server guard, moved here verbatim (v1
// keeps its own copy until it is deleted).
//
// safeJoinUnderRoot guards against path-traversal: the resolved absolute
// path must remain under the scan root.
//
// The function resolves both the scan root and the candidate path through
// symlinks before comparison. This is required on macOS, where /var is a
// symlink to /private/var — os.MkdirTemp and filepath.Abs return the
// unresolved /var/... prefix while EvalSymlinks yields /private/var/....
// Without resolving both sides the containment check produces false negatives.
//
// When the candidate path does not yet exist on disk (e.g. a cache file that
// will be created on first request), EvalSymlinks fails; the function falls
// back to a lexical check on the unresolved path, which is still sufficient
// for the non-symlink traversal cases (e.g. "../../etc/passwd" in rel_path).
func safeJoinUnderRoot(root, abs string) (string, error) {
	clean, err := filepath.Abs(filepath.Clean(abs))
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// Resolve the scan root through symlinks (best-effort).
	if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = resolved
	}

	containedUnder := func(path, root string) bool {
		return strings.HasPrefix(path+string(filepath.Separator), root+string(filepath.Separator))
	}

	// Attempt to resolve the candidate path through symlinks. When the path
	// exists on disk EvalSymlinks catches:
	//  (a) OS-level symlink prefixes (e.g. /var -> /private/var on macOS), and
	//  (b) a symlink inside .choix/thumbs that points outside the scan root.
	// When the path doesn't exist yet we fall back to the lexical check.
	if _, statErr := os.Stat(clean); statErr == nil {
		resolved, evalErr := filepath.EvalSymlinks(clean)
		if evalErr == nil {
			resolvedAbs, err := filepath.Abs(resolved)
			if err != nil {
				return "", err
			}
			if !containedUnder(resolvedAbs, rootAbs) {
				return "", errors.New("path escapes scan root via symlink")
			}
			return resolvedAbs, nil
		}
	}

	// Path doesn't exist yet or EvalSymlinks failed: fall back to lexical
	// containment on the unresolved path. Use the unresolved rootAbs here
	// because clean is also unresolved — they share the same unresolved prefix
	// so the prefix comparison is still valid.
	rootUnresolved, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if !containedUnder(clean, rootUnresolved) {
		return "", errors.New("path escapes scan root")
	}
	return clean, nil
}

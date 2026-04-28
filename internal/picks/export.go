package picks

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Export copies the original from <scanRoot>/<file.path> to
// <scanRoot>/<picksDir>/<file.path>, creating directories as needed. The
// original is opened O_RDONLY. On filename collision (target exists with a
// different content_hash), it appends `_2`, `_3`, ... until unique. If the
// target exists with the same content_hash, the existing path is returned and
// no copy is performed (re-export idempotency). Returns the relative path of
// the exported file (relative to scanRoot).
//
// Export is the primitive used internally by Pick(); it is exported only so
// tests can drive it directly and so 9.3 can wire Pick → exportLocked → Export.
func (s *Service) Export(fileID int64) (string, error) {
	f, err := s.fileFor(fileID)
	if err != nil {
		return "", err
	}
	return s.exportFile(f)
}

func (s *Service) exportFile(f store.File) (string, error) {
	scanRoot := filepath.Clean(s.scanRoot)
	exportRoot := filepath.Clean(filepath.Join(scanRoot, s.picksDir))
	// Defence-in-depth: the export root must itself be under the scan root.
	// This catches a Service constructed with a traversal picksDir (e.g. "../out").
	if !isUnder(scanRoot, exportRoot) {
		return "", fmt.Errorf("picks dir %q escapes scan root %s", s.picksDir, scanRoot)
	}

	srcAbs := filepath.Join(scanRoot, f.Path)
	src, err := os.Open(srcAbs) //nolint:gosec // path is constructed from store-validated relPath + scanRoot
	if err != nil {
		return "", fmt.Errorf("open source %s: %w", srcAbs, err)
	}
	defer func() { _ = src.Close() }()

	relTarget, absTarget, err := s.resolveTarget(f)
	if err != nil {
		return "", err
	}
	// Containment guard: ensure the resolved target stays under the export root.
	if absTarget != "" && !isUnder(exportRoot, absTarget) {
		return "", fmt.Errorf("export target %s escapes picks root %s", absTarget, exportRoot)
	}
	// Idempotency: target already holds same content_hash → done.
	if absTarget == "" {
		return relTarget, nil
	}
	if err := os.MkdirAll(filepath.Dir(absTarget), 0o750); err != nil { //nolint:gosec // picks/ dirs are user-owned
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(absTarget), err)
	}
	tmp := absTarget + ".tmp"
	dst, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // copied media file permissions
	if err != nil {
		return "", fmt.Errorf("create tmp %s: %w", tmp, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("copy: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmp, absTarget); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("rename %s -> %s: %w", tmp, absTarget, err)
	}
	return relTarget, nil
}

// resolveTarget computes the relative + absolute target path. If the candidate
// path already exists and its content matches f.ContentHash, returns the
// existing relPath plus an empty absTarget signaling "no copy needed". On a
// content mismatch, walks `_2`, `_3`, ... until finding a path that either
// does not exist or matches the hash.
func (s *Service) resolveTarget(f store.File) (rel, abs string, err error) {
	base := filepath.Join(s.picksDir, f.Path)
	for n := 1; ; n++ {
		candidate := base
		if n > 1 {
			candidate = suffixedPath(base, n)
		}
		absC := filepath.Join(s.scanRoot, candidate)
		switch existing, err := hashIfExists(absC); {
		case err != nil:
			return "", "", err
		case existing == "":
			// Target slot is free.
			return candidate, absC, nil
		case existing == f.ContentHash:
			// Same content already there → idempotent.
			return candidate, "", nil
		}
		// else: occupied with different content; try next suffix.
	}
}

// suffixedPath inserts "_N" before the extension: "a/b.jpg" + 2 → "a/b_2.jpg".
func suffixedPath(p string, n int) string {
	dir := filepath.Dir(p)
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(filepath.Base(p), ext)
	return filepath.Join(dir, fmt.Sprintf("%s_%d%s", stem, n, ext))
}

// Unexport reads the recorded exported_path, deletes that file (if it exists),
// and clears the column. Idempotent: a missing file or missing exported_path
// returns nil. Used internally by Unpick and Reject; exported for symmetry
// with Export and to allow tests / future repair tooling to drive it directly.
func (s *Service) Unexport(fileID int64) error {
	scanRoot := filepath.Clean(s.scanRoot)
	exportRoot := filepath.Clean(filepath.Join(scanRoot, s.picksDir))
	// Defence-in-depth: the export root must itself be under the scan root.
	if !isUnder(scanRoot, exportRoot) {
		return fmt.Errorf("picks dir %q escapes scan root %s", s.picksDir, scanRoot)
	}

	pick, err := s.store.Picks().Get(fileID)
	if err != nil {
		return nil // nothing to do
	}
	if pick.ExportedPath.Valid && pick.ExportedPath.String != "" {
		abs := filepath.Clean(filepath.Join(scanRoot, pick.ExportedPath.String))
		if !isUnder(exportRoot, abs) {
			return fmt.Errorf("unexport path %s escapes picks root %s", abs, exportRoot)
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", abs, err)
		}
	}
	return s.store.Picks().ClearExportedPath(fileID)
}

// isUnder reports whether child is located under the parent directory.
// Both paths must already be filepath.Clean'd. A path is never under itself.
func isUnder(parent, child string) bool {
	sep := string(filepath.Separator)
	return strings.HasPrefix(child+sep, parent+sep)
}

// hashIfExists returns the xxhash64 of the file (lower-hex, 16 chars) if it
// exists, "" if it doesn't, or an error otherwise.
func hashIfExists(abs string) (string, error) {
	fp, err := os.Open(abs) //nolint:gosec // path is constructed from store-validated relPath + scanRoot
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("open %s: %w", abs, err)
	}
	defer func() { _ = fp.Close() }()
	h := xxhash.New()
	if _, err := io.Copy(h, fp); err != nil {
		return "", fmt.Errorf("hash %s: %w", abs, err)
	}
	return fmt.Sprintf("%016x", h.Sum64()), nil
}

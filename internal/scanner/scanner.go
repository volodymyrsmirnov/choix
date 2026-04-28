package scanner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// defaultSkipDirs are directory names skipped at any depth during a walk.
// Hidden dirs (leading dot) are also skipped via the pattern check below.
var defaultSkipDirs = []string{
	".choix",
	"node_modules",
}

// Scanner walks a filesystem tree rooted at Root, identifies media files by
// extension, computes a content hash, and upserts files rows in the store.
type Scanner struct {
	root     string
	store    *store.Store
	skipDirs map[string]struct{}
	picksDir string // relative path (slash-separated) of the picks export dir to skip
	seen     map[string]int64
}

// New returns a Scanner for the given root and store, populated with the
// project-default skip rules (hidden dirs, .choix, node_modules, picks/ at root).
func New(root string, st *store.Store) *Scanner {
	skip := make(map[string]struct{}, len(defaultSkipDirs))
	for _, d := range defaultSkipDirs {
		skip[d] = struct{}{}
	}
	return &Scanner{root: root, store: st, skipDirs: skip, picksDir: "picks"}
}

// SetPicksDir overrides the relative directory under the scan root that the
// walker treats as the picks export folder and skips during discovery. The
// argument is a path relative to the scan root, slash-separated; it may be
// nested (e.g. "exports/luminar"). Empty restores the default ("picks").
//
// This must match the picks_dir resolved from the per-folder KV / config.toml,
// otherwise files copied into the picks dir would get re-indexed as new
// originals on the next scan.
func (s *Scanner) SetPicksDir(dir string) {
	dir = strings.Trim(filepath.ToSlash(dir), "/")
	if dir == "" {
		dir = "picks"
	}
	s.picksDir = dir
}

// Walk traverses the scan root, upserting one files row per discovered media
// file. The traversal respects ctx cancellation and skips:
//   - any directory whose name begins with "." (hidden),
//   - directories named in defaultSkipDirs at any depth,
//   - the configured picks export directory (default "picks") under root.
//
// Walk is idempotent (Task 3.4) and reconciles missing files (Task 3.5).
func (s *Scanner) Walk(ctx context.Context) error {
	if s.root == "" {
		return errors.New("scanner: empty root")
	}

	s.seen = make(map[string]int64)
	start := time.Now()
	slog.Info("scanner walk start", "root", s.root, "picks_dir", s.picksDir)
	var dirCount, fileCount int
	const logEveryFiles = 250

	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", path, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil {
			return fmt.Errorf("rel %s: %w", path, relErr)
		}

		if d.IsDir() {
			if rel == "." {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			if _, skip := s.skipDirs[name]; skip {
				return fs.SkipDir
			}
			// Configured picks export dir at any depth under root. Match
			// the slash-normalized rel path so nested overrides like
			// "exports/luminar" work too.
			if filepath.ToSlash(rel) == s.picksDir {
				return fs.SkipDir
			}
			dirCount++
			return nil
		}

		// Regular file — must have a recognized media extension.
		fi, ok := DetectFromExt(d.Name())
		if !ok {
			return nil
		}

		// Stat for size + mtime.
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}

		hash, err := ContentHash(path)
		if err != nil {
			return fmt.Errorf("hash %s: %w", path, err)
		}

		rec := store.File{
			Path:        filepath.ToSlash(rel),
			Size:        info.Size(),
			Mtime:       info.ModTime().Unix(),
			ContentHash: hash,
			Kind:        fi.Kind,
			Format:      fi.Format,
			ScanStatus:  "discovered",
		}
		if err := s.upsert(rec); err != nil {
			return fmt.Errorf("upsert %s: %w", rec.Path, err)
		}
		fileCount++
		if fileCount%logEveryFiles == 0 {
			slog.Info("scanner walk progress", "files", fileCount, "dirs", dirCount)
		}
		return nil
	}

	if err := filepath.WalkDir(s.root, walkFn); err != nil {
		slog.Warn("scanner walk failed", "root", s.root, "files", fileCount, "err", err)
		return fmt.Errorf("scanner: %w", err)
	}
	missing, err := s.store.Files().MarkMissingExcept(s.seen)
	if err != nil {
		return fmt.Errorf("scanner: mark missing: %w", err)
	}
	slog.Info("scanner walk done",
		"root", s.root,
		"files", fileCount,
		"dirs", dirCount,
		"missing", missing,
		"elapsed_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// Scan walks the scan root, upserts file rows, and returns the count of files
// discovered together with any error. It delegates to Walk and captures the
// size of the internal seen-set populated during the walk.
func (s *Scanner) Scan(ctx context.Context) (int, error) {
	if err := s.Walk(ctx); err != nil {
		return len(s.seen), err
	}
	return len(s.seen), nil
}

// upsert calls UpsertOnRescan and records the file's id in s.seen so that
// Walk can later detect which previously-known paths were not visited.
func (s *Scanner) upsert(rec store.File) error {
	id, _, err := s.store.Files().UpsertOnRescan(rec)
	if err != nil {
		return err
	}
	s.seen[rec.Path] = id
	return nil
}

package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
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
	// skipRel are directories relative to the root (slash-separated) that the
	// walk skips and whose rows it evicts: the picks export directories.
	skipRel []string

	// beforeOpen, when set (tests only), is called with the slash-separated
	// relative path of each media entry right before it is opened.
	beforeOpen func(rel string)
}

// New returns a Scanner for the given root and store, populated with the
// project-default skip rules (hidden dirs, .choix, node_modules, picks/ at root).
func New(root string, st *store.Store) *Scanner {
	skip := make(map[string]struct{}, len(defaultSkipDirs))
	for _, d := range defaultSkipDirs {
		skip[d] = struct{}{}
	}
	return &Scanner{root: root, store: st, skipDirs: skip, skipRel: []string{"picks"}}
}

// SetSkipDirs replaces the directories, relative to the scan root and
// slash-separated, that Walk skips: the picks export directory and any older
// export directories. Rows already indexed under them are evicted at the
// start of each Walk, so exported copies never come back as originals.
//
// It replaces the default "picks" too: an empty slice skips nothing and
// evicts nothing, which is how a refused picks directory (one holding
// scanned originals) is walked as originals. ".choix" and hidden directories
// are skipped regardless. Matching is case-insensitive, like file identity.
func (s *Scanner) SetSkipDirs(dirs []string) {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d = strings.Trim(filepath.ToSlash(d), "/"); d != "" {
			out = append(out, d)
		}
	}
	s.skipRel = out
}

// walkStats counts what one walk did, for its closing log line.
type walkStats struct {
	dirs, files, failed, skipped int
}

// Walk traverses the scan root, upserting one files row per discovered media
// file. The traversal respects ctx cancellation between entries and skips:
//   - any directory whose name begins with "." (hidden),
//   - directories named in defaultSkipDirs at any depth,
//   - the directories set with SetSkipDirs (default "picks"),
//   - every entry that is not a regular file: a FIFO, socket or device is
//     never opened, and a symlink is followed only to a regular file.
//
// One entry never stops the walk (hard rule 3). A media file that cannot be
// read is recorded as 'failed' with the error text
// (FilesRepo.RecordDiscoveryFailure); an entry that vanished, a symlink whose
// target cannot be stat'ed and a sub-directory that cannot be listed are
// logged and skipped, so their files, if known, are marked missing.
//
// The outage guard: when an entry fails, the root is checked again. If the
// root itself is gone or cannot be listed (its volume was unplugged), Walk
// stops with an error wrapping pipeline.ErrFolderUnavailable and marks
// nothing missing. A walk that is cancelled marks nothing missing either.
// Only a walk that visited the whole folder marks the rows it did not see
// 'missing' (FilesRepo.MarkMissingNotSeen).
//
// Walk is idempotent (Task 3.4) and reconciles missing files (Task 3.5).
func (s *Scanner) Walk(ctx context.Context) error {
	if s.root == "" {
		return errors.New("scanner: empty root")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkRoot(); err != nil {
		return err
	}

	start := time.Now()
	slog.Info("scanner walk start", "root", s.root, "skip_dirs", s.skipRel)

	// Evict any rows previously indexed from inside a skipped export dir.
	// Earlier builds (or a picks_dir reconfiguration) may have ingested
	// copies as originals; once that's happened the user sees their own
	// picks come back as duplicate clusters every time the visual clusterer
	// runs. Drop them outright so a scan starts from a clean slate.
	for _, dir := range s.skipRel {
		if n, err := s.store.Files().DeleteUnderPath(ctx, dir); err != nil {
			return s.storeError(fmt.Errorf("evict rows under %s: %w", dir, err))
		} else if n > 0 {
			slog.Info("scanner evicted export-dir rows", "dir", dir, "rows", n)
		}
	}

	run, err := s.store.Files().NewWalkRun()
	if err != nil {
		return s.storeError(err)
	}

	var st walkStats
	walkFn := func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return s.walkError(path, d, err, &st)
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return fmt.Errorf("rel %s: %w", path, err)
		}
		if d.IsDir() {
			return s.enterDir(rel, d, &st)
		}
		return s.visitFile(path, filepath.ToSlash(rel), d, run, &st)
	}

	if err := filepath.WalkDir(s.root, walkFn); err != nil {
		slog.Warn("scanner walk stopped", "root", s.root, "files", st.files, "err", err)
		if errors.Is(err, pipeline.ErrFolderUnavailable) {
			return err
		}
		return fmt.Errorf("scanner: %w", err)
	}
	// Nothing is marked missing unless the walk saw the whole folder: not
	// after a cancellation that came while the last entry was taken (no
	// callback followed to notice it), and not when the root has become
	// unavailable — a walk that found the folder empty because its volume
	// went away has not seen the files.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkRoot(); err != nil {
		return err
	}
	missing, err := s.store.Files().MarkMissingNotSeen(ctx, run)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("scanner: mark missing: %w", cerr)
		}
		return s.storeError(err)
	}
	slog.Info("scanner walk done",
		"root", s.root,
		"files", st.files,
		"failed", st.failed,
		"skipped", st.skipped,
		"dirs", st.dirs,
		"missing", missing,
		"elapsed_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// logEveryFiles is how often (in files) a walk logs its progress.
const logEveryFiles = 250

// enterDir decides whether the walk descends into the directory at rel.
func (s *Scanner) enterDir(rel string, d fs.DirEntry, st *walkStats) error {
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
	// Skipped export dirs, matched on the slash-normalized rel path so
	// nested ones like "exports/luminar" work too. Case-insensitive to align
	// with the file-identity rules (LOWER(path) UNIQUE in migration 003).
	if s.underSkipped(filepath.ToSlash(rel)) {
		return fs.SkipDir
	}
	st.dirs++
	return nil
}

// visitFile takes one non-directory entry: a media file is hashed and
// upserted, or recorded as failed; anything else is skipped.
func (s *Scanner) visitFile(path, rel string, d fs.DirEntry, run int64, st *walkStats) error {
	fi, ok := DetectFromExt(d.Name())
	if !ok {
		return nil
	}
	// Defensive: if a regular file's relative path falls under a skipped
	// dir (e.g. picks_dir was changed at runtime and the directory wasn't
	// yet skipped above), don't ingest it.
	if s.underSkipped(rel) {
		return nil
	}

	follow := false
	switch typ := d.Type(); {
	case typ.IsRegular():
	case typ&fs.ModeSymlink != 0:
		target, err := os.Stat(path)
		if err != nil {
			return s.skipEntry(path, "symlink target unavailable", err, st)
		}
		if !target.Mode().IsRegular() {
			st.skipped++
			return nil
		}
		follow = true
	default:
		// A FIFO, socket or device named like media: never opened (a FIFO
		// would block open(2) until a writer appears).
		slog.Warn("scanner: skipping an entry that is not a regular file", "path", path, "type", typ.String())
		st.skipped++
		return nil
	}

	if s.beforeOpen != nil {
		s.beforeOpen(rel)
	}
	hash, info, err := hashRegular(path, follow)
	if err != nil {
		if rerr := s.checkRoot(); rerr != nil {
			return rerr
		}
		if errors.Is(err, fs.ErrNotExist) {
			// It went away after the directory was read: not seen.
			slog.Warn("scanner: entry vanished during the walk", "path", path, "err", err)
			st.skipped++
			return nil
		}
		return s.recordFailure(path, rel, fi, d, run, err, st)
	}

	rec := store.File{
		Path:        rel,
		Size:        info.Size(),
		Mtime:       info.ModTime().Unix(),
		ContentHash: hash,
		Kind:        fi.Kind,
		Format:      fi.Format,
		ScanStatus:  "discovered",
	}
	if _, _, err := s.store.Files().UpsertOnRescan(rec, run); err != nil {
		return s.storeError(fmt.Errorf("upsert %s: %w", rel, err))
	}
	st.files++
	if st.files%logEveryFiles == 0 {
		slog.Info("scanner walk progress", "files", st.files, "dirs", st.dirs)
	}
	return nil
}

// recordFailure records the media file at rel, which could not be read, as
// 'failed' with readErr's text. Its size and mtime are the entry's own
// status when that can be read, zero otherwise.
func (s *Scanner) recordFailure(path, rel string, fi FormatInfo, d fs.DirEntry, run int64, readErr error, st *walkStats) error {
	rec := store.File{Path: rel, Kind: fi.Kind, Format: fi.Format}
	if info, err := d.Info(); err == nil {
		rec.Size, rec.Mtime = info.Size(), info.ModTime().Unix()
	}
	if _, err := s.store.Files().RecordDiscoveryFailure(rec, run, readErr.Error()); err != nil {
		return s.storeError(err)
	}
	slog.Warn("scanner: file cannot be read; recorded as failed", "path", path, "err", readErr)
	st.failed++
	return nil
}

// walkError handles an error filepath.WalkDir reports for path: the root
// cannot be read (outage), or a sub-directory cannot be listed (skipped).
func (s *Scanner) walkError(path string, d fs.DirEntry, err error, st *walkStats) error {
	if path == s.root {
		return fmt.Errorf("scanner: %w: %s: %w", pipeline.ErrFolderUnavailable, s.root, err)
	}
	if rerr := s.checkRoot(); rerr != nil {
		return rerr
	}
	slog.Warn("scanner: cannot read an entry; skipping it", "path", path, "err", err)
	st.skipped++
	if d != nil && d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

// skipEntry logs and skips an entry that failed with err, unless the root
// itself is unavailable.
func (s *Scanner) skipEntry(path, why string, err error, st *walkStats) error {
	if rerr := s.checkRoot(); rerr != nil {
		return rerr
	}
	slog.Warn("scanner: skipping an entry", "path", path, "why", why, "err", err)
	st.skipped++
	return nil
}

// checkRoot is the outage guard: it returns an error wrapping
// pipeline.ErrFolderUnavailable unless the scan root can really be
// traversed — it exists, is a directory, can be listed (read permission)
// and searched (search permission: a name inside it resolves) — and nil
// otherwise. A root that is gone (its volume was unplugged) or that lacks
// either permission makes every entry fail, which is not a list of failed
// files.
func (s *Scanner) checkRoot() error {
	if err := probeRoot(s.root); err != nil {
		return fmt.Errorf("scanner: %w: %s: %w", pipeline.ErrFolderUnavailable, s.root, err)
	}
	return nil
}

// probeRoot lists one name of root and resolves a name through it.
func probeRoot(root string) error {
	f, err := os.Open(root) //nolint:gosec // the scan root, opened read-only to see that it can be listed
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("list: %w", err)
	}
	// "<root>/." resolves only with search permission on root, whether or not
	// root has entries (filepath.Join would clean the "." away).
	if _, err := os.Lstat(root + string(os.PathSeparator) + "."); err != nil {
		return err
	}
	return nil
}

// storeError wraps a database error of the walk. The database lives in the
// folder (.choix/state.db), so a write that fails because the volume went
// away is the outage, not a failure of the job.
func (s *Scanner) storeError(err error) error {
	if rerr := s.checkRoot(); rerr != nil {
		return fmt.Errorf("%w (%w)", rerr, err)
	}
	return fmt.Errorf("scanner: %w", err)
}

// isUnderDir reports whether the slash-separated path lives at or under the
// given directory. The comparison is case-insensitive to match the project's
// file-identity rules. Empty dir matches nothing.
func isUnderDir(path, dir string) bool {
	if dir == "" {
		return false
	}
	if strings.EqualFold(path, dir) {
		return true
	}
	prefix := dir + "/"
	if len(path) <= len(prefix) {
		return false
	}
	return strings.EqualFold(path[:len(prefix)], prefix)
}

// underSkipped reports whether the slash-separated relative path is at or
// under one of the skipped directories.
func (s *Scanner) underSkipped(rel string) bool {
	for _, dir := range s.skipRel {
		if isUnderDir(rel, dir) {
			return true
		}
	}
	return false
}

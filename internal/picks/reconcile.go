package picks

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
)

// ReconcileReport counts what Reconcile found.
type ReconcileReport struct {
	Adopted int // picked rows without a recorded copy whose identical copy was found and recorded
	Missing int // picked rows with no verified copy: the recorded copy is gone, or a candidate could not be checked against its unreadable original
	Orphans int // media files in the picks directory that no pick row refers to
}

// Reconcile compares the pick rows with the picks directory when a session
// opens. It never copies or deletes anything:
//   - a picked row without an exported path whose expected copy
//     (<picksDir>/<file path>) is a regular file byte-identical to the
//     original (compared in full, read-only) gets that path recorded
//     (Adopted); when the original cannot be read the copy is not adopted
//     and the row counts as Missing;
//   - a picked row whose recorded copy is not on disk is counted (Missing)
//     and left as it is — the user may have moved their picks away;
//   - media files under the picks directory that no row refers to are
//     counted (Orphans).
//
// A picked row with a recorded copy costs one Lstat; only the rare row
// without one (legacy data) reads its candidate and original. Adoption and
// orphan counting are skipped while the picks directory is invalid or refused
// by CheckDir. The counts are logged once.
func (s *Service) Reconcile(ctx context.Context) (ReconcileReport, error) {
	var rep ReconcileReport
	picked, err := s.store.Picks().ListPicked()
	if err != nil {
		return rep, fmt.Errorf("picks: reconcile: list picks: %w", err)
	}
	referenced, err := s.referencedPaths()
	if err != nil {
		return rep, err
	}
	dir, usable := s.usableDir()

	for _, p := range picked {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if p.ExportedPath.Valid && p.ExportedPath.String != "" {
			if s.copyMissing(p.ExportedPath.String) {
				rep.Missing++
			}
			continue
		}
		if !usable {
			continue
		}
		adopted, outcome, err := s.adopt(ctx, p.FileID, dir)
		if err != nil {
			return rep, err
		}
		switch outcome {
		case adoptRecorded:
			rep.Adopted++
			referenced[strings.ToLower(adopted)] = struct{}{}
		case adoptUnverified:
			rep.Missing++ // no verified copy: reported, never adopted
		case adoptNone:
		}
	}
	if usable {
		n, err := s.countOrphans(ctx, dir, referenced)
		if err != nil {
			return rep, err
		}
		rep.Orphans = n
	}
	slog.Info("picks reconcile", "picks_dir", s.picksDir,
		"adopted", rep.Adopted, "missing", rep.Missing, "orphans", rep.Orphans)
	return rep, nil
}

// usableDir returns the clean picks directory and whether picks may be
// exported there.
func (s *Service) usableDir() (string, bool) {
	if err := ValidateDir(s.picksDir); err != nil {
		slog.Warn("picks reconcile: picks directory unusable", "picks_dir", s.picksDir, "err", err)
		return "", false
	}
	dir := cleanDir(s.picksDir)
	if err := CheckDir(s.store, dir); err != nil {
		slog.Warn("picks reconcile: picks directory refused", "picks_dir", s.picksDir, "err", err)
		return dir, false
	}
	return dir, true
}

// referencedPaths returns the lower-cased exported paths of every pick row.
func (s *Service) referencedPaths() (map[string]struct{}, error) {
	all, err := s.store.Picks().All()
	if err != nil {
		return nil, fmt.Errorf("picks: reconcile: list rows: %w", err)
	}
	out := make(map[string]struct{}, len(all))
	for _, p := range all {
		if p.ExportedPath.Valid && p.ExportedPath.String != "" {
			out[strings.ToLower(strings.TrimLeft(p.ExportedPath.String, "/"))] = struct{}{}
		}
	}
	return out, nil
}

// copyMissing reports whether the recorded copy at exportedPath is absent.
func (s *Service) copyMissing(exportedPath string) bool {
	rel := strings.TrimLeft(exportedPath, "/")
	if !filepath.IsLocal(rel) {
		slog.Warn("picks reconcile: recorded copy is outside the folder", "path", exportedPath)
		return false
	}
	_, err := os.Lstat(filepath.Join(s.scanRoot, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		slog.Warn("picks reconcile: check recorded copy", "path", exportedPath, "err", err)
	}
	return false
}

// adoptOutcome is what adopt found for one picked row without a copy path.
type adoptOutcome int

const (
	adoptNone       adoptOutcome = iota // no candidate, or a candidate with other bytes
	adoptRecorded                       // identical candidate recorded
	adoptUnverified                     // a candidate exists but the original cannot be read
)

// adopt records the expected copy of fileID under dir when it is a regular
// file byte-identical to the original — compared in full, never by the
// scanner's sampled hash. When a candidate exists but the original cannot be
// read, nothing is recorded and the outcome is adoptUnverified. It returns
// the recorded path with adoptRecorded.
func (s *Service) adopt(ctx context.Context, fileID int64, dir string) (string, adoptOutcome, error) {
	f, err := s.fileFor(fileID)
	if err != nil {
		return "", adoptNone, err
	}
	if !filepath.IsLocal(f.Path) {
		return "", adoptNone, nil
	}
	rel := dir
	if sub := path.Dir(f.Path); sub != "." {
		rel = dir + "/" + sub
	}
	// Opening the directory through safefs refuses a symlinked component;
	// OpenRead refuses a symlink and never blocks on a special file.
	d, err := safefs.OpenDir(s.scanRoot, rel, false)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("picks reconcile: open picks directory", "dir", rel, "err", err)
		}
		return "", adoptNone, nil
	}
	defer func() { _ = d.Close() }()
	name := path.Base(f.Path)
	cand, err := d.OpenRead(name)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("picks reconcile: candidate copy unusable", "path", rel+"/"+name, "err", err)
		}
		return "", adoptNone, nil
	}
	defer func() { _ = cand.Close() }()

	orig, err := openOriginal(s.scanRoot, f.Path)
	if err != nil {
		slog.Warn("picks reconcile: cannot verify a copy against its original", "path", rel+"/"+name, "err", err)
		return "", adoptUnverified, nil
	}
	defer func() { _ = orig.Close() }()
	ci, err := cand.Stat()
	if err != nil {
		return "", adoptNone, nil
	}
	oi, err := orig.Stat()
	if err != nil {
		return "", adoptUnverified, nil
	}
	if ci.Size() != oi.Size() {
		return "", adoptNone, nil
	}
	same, err := sameBytes(ctx, cand, orig)
	if err != nil {
		if ctx.Err() != nil {
			return "", adoptNone, ctx.Err()
		}
		slog.Warn("picks reconcile: compare a copy with its original", "path", rel+"/"+name, "err", err)
		return "", adoptUnverified, nil
	}
	if !same {
		return "", adoptNone, nil
	}
	candidate := rel + "/" + name
	if err := s.commit(ctx, fileID, StatePicked, candidate); err != nil {
		return "", adoptNone, err
	}
	return candidate, adoptRecorded, nil
}

// countOrphans counts the media files under dir (by extension, regular
// files, dot-files such as AppleDouble "._x.jpg" excluded) whose path no
// pick row records.
func (s *Service) countOrphans(ctx context.Context, dir string, referenced map[string]struct{}) (int, error) {
	d, err := safefs.OpenDir(s.scanRoot, dir, false)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("picks reconcile: open picks directory", "dir", dir, "err", err)
		}
		return 0, nil
	}
	_ = d.Close()

	n := 0
	walkRoot := filepath.Join(s.scanRoot, filepath.FromSlash(dir))
	err = filepath.WalkDir(walkRoot, func(p string, e fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			slog.Warn("picks reconcile: walk picks directory", "path", p, "err", err)
			if e != nil && e.IsDir() && p != walkRoot {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(e.Name(), ".") && p != walkRoot {
			if e.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !e.Type().IsRegular() {
			return nil
		}
		if _, media := scanner.DetectFromExt(e.Name()); !media {
			return nil
		}
		rel, rerr := filepath.Rel(s.scanRoot, p)
		if rerr != nil {
			return nil
		}
		if _, ok := referenced[strings.ToLower(filepath.ToSlash(rel))]; !ok {
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("picks: reconcile: walk %s: %w", dir, err)
	}
	return n, nil
}

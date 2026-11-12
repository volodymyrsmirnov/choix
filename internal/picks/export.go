package picks

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// publishTries bounds how often export chooses a target again after another
// file appeared at the chosen name before the exclusive rename.
const publishTries = 100

// suffixTries bounds the _N suffixes tried for one target name.
const suffixTries = 10000

// exported is a copy export placed (or found) in the picks directory.
type exported struct {
	dir       *safefs.Dir // the directory holding the copy; the caller closes it
	name      string      // the copy's name in dir
	path      string      // the copy's path relative to the scan root
	published bool        // this call created the copy (false: an identical one was reused)
}

// openOriginal opens the original at rel (slash-separated, relative to root)
// read-only. O_NONBLOCK keeps a FIFO from blocking the open; anything but a
// regular file is refused.
func openOriginal(root, rel string) (*os.File, error) {
	if !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("file path %q is not inside the folder", rel)
	}
	//nolint:gosec // G304: rel is a files-table path checked by filepath.IsLocal, opened read-only under the folder root
	f, err := os.OpenFile(filepath.Join(root, filepath.FromSlash(rel)), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open original: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat original: %w", err)
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("original %s is not a regular file", rel)
	}
	return f, nil
}

// export copies the original f into <dir>/<dir of f.Path>/ through safefs and
// returns where it is. The original is opened read-only. Directories the
// export creates are made durable (safefs.OpenDirDurable). The target is f's
// base name; a regular file already there with identical bytes is reused
// (after syncing it and its directory, like a published copy); anything else
// there (other bytes, a symlink, a directory, a FIFO) moves on to the next _N
// suffix. A new copy is published exclusively and durably (file and directory
// synced); if another file takes the name between the choice and the rename,
// the target is chosen again.
func (s *Service) export(ctx context.Context, f store.File, dir string) (*exported, error) {
	src, err := openOriginal(s.scanRoot, f.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()
	srcInfo, err := src.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat original: %w", err)
	}

	rel := dir
	if sub := path.Dir(f.Path); sub != "." {
		rel = dir + "/" + sub
	}
	d, err := safefs.OpenDirDurable(s.scanRoot, rel)
	if err != nil {
		return nil, err
	}
	exp, err := publishCopy(ctx, d, path.Base(f.Path), src, srcInfo.Size())
	if err != nil {
		_ = d.Close()
		return nil, err
	}
	exp.dir = d
	exp.path = rel + "/" + exp.name
	return exp, nil
}

// publishCopy places a copy of src in d under base or a suffixed variant.
func publishCopy(ctx context.Context, d *safefs.Dir, base string, src *os.File, size int64) (*exported, error) {
	for range publishTries {
		name, identical, err := chooseTarget(ctx, d, base, src, size)
		if err != nil {
			return nil, err
		}
		if identical != nil {
			err := syncReused(d, identical)
			_ = identical.Close()
			if err != nil {
				return nil, err
			}
			return &exported{name: name}, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tmp, tmpName, err := d.CreateTemp(name)
		if err != nil {
			return nil, err
		}
		trace("copy")
		if err := copyFrom(ctx, tmp, src); err != nil {
			_ = tmp.Close()
			if rerr := d.Remove(tmpName); rerr != nil {
				slog.Warn("picks: remove temporary copy", "name", tmpName, "err", rerr)
			}
			return nil, fmt.Errorf("copy: %w", err)
		}
		err = d.Publish(tmp, tmpName, name, true)
		if errors.Is(err, safefs.ErrExists) {
			continue // a file appeared at name after the check: choose again
		}
		if err != nil {
			return nil, err
		}
		return &exported{name: name, published: true}, nil
	}
	return nil, fmt.Errorf("no free name for %s after %d attempts", base, publishTries)
}

// syncReused makes a reused copy as durable as a published one: the file,
// then its directory. A volume that cannot sync is tolerated by safefs.
func syncReused(d *safefs.Dir, f *os.File) error {
	if err := d.SyncFile(f); err != nil {
		return err
	}
	return d.Sync()
}

// chooseTarget returns the first of base, base_2, base_3… in d that is free,
// or that holds a regular file byte-identical to src; in that case identical
// is that file, open, and the caller closes it.
func chooseTarget(ctx context.Context, d *safefs.Dir, base string, src *os.File, size int64) (name string, identical *os.File, err error) {
	for n := 1; n <= suffixTries; n++ {
		name := suffixedName(base, n)
		free, same, err := compareTarget(ctx, d, name, src, size)
		if err != nil {
			return "", nil, err
		}
		if free || same != nil {
			return name, same, nil
		}
	}
	return "", nil, fmt.Errorf("no free name for %s after %d suffixes", base, suffixTries)
}

// compareTarget looks at name in d without following a symlink or blocking on
// a special file: free when absent; same (returned open) when it is a regular
// file with exactly src's bytes; otherwise occupied.
func compareTarget(ctx context.Context, d *safefs.Dir, name string, src *os.File, size int64) (free bool, same *os.File, err error) {
	f, err := d.OpenRead(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true, nil, nil
	case errors.Is(err, safefs.ErrSymlink), errors.Is(err, safefs.ErrNotRegular):
		return false, nil, nil
	case err != nil:
		return false, nil, fmt.Errorf("check %s: %w", name, err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return false, nil, fmt.Errorf("check %s: %w", name, err)
	}
	if fi.Size() != size {
		_ = f.Close()
		return false, nil, nil
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return false, nil, err
	}
	equal, err := sameBytes(ctx, f, src)
	if err != nil || !equal {
		_ = f.Close()
		if err != nil {
			return false, nil, fmt.Errorf("compare %s: %w", name, err)
		}
		return false, nil, nil
	}
	return false, f, nil
}

// sameBytes reports whether a and b hold the same bytes from their current
// positions to the end, checking ctx before every chunk.
func sameBytes(ctx context.Context, a, b io.Reader) (bool, error) {
	const chunk = 64 << 10
	ba, bb := make([]byte, chunk), make([]byte, chunk)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		na, ea := io.ReadFull(a, ba)
		nb, eb := io.ReadFull(b, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false, nil
		}
		aDone := errors.Is(ea, io.EOF) || errors.Is(ea, io.ErrUnexpectedEOF)
		bDone := errors.Is(eb, io.EOF) || errors.Is(eb, io.ErrUnexpectedEOF)
		switch {
		case ea != nil && !aDone:
			return false, ea
		case eb != nil && !bDone:
			return false, eb
		case aDone || bDone:
			return aDone && bDone, nil
		}
	}
}

// copyFrom copies src from its start into dst, stopping when ctx is done.
func copyFrom(ctx context.Context, dst io.Writer, src *os.File) error {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err := io.Copy(dst, ctxReader{ctx: ctx, r: src})
	return err
}

// ctxReader fails reads once ctx is done, so a long copy honours cancellation.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// suffixedName inserts "_N" before the extension for n > 1:
// "b.jpg", 2 → "b_2.jpg".
func suffixedName(name string, n int) string {
	if n <= 1 {
		return name
	}
	ext := path.Ext(name)
	return fmt.Sprintf("%s_%d%s", strings.TrimSuffix(name, ext), n, ext)
}

// discardCopy removes a copy this call published after its state could not
// be recorded, so a failed pick leaves nothing behind. (A copy published by
// this call under a fresh exclusive name is referenced by no row.)
func (s *Service) discardCopy(exp *exported) {
	trace("remove")
	if err := exp.dir.Remove(exp.name); err != nil {
		slog.Warn("picks: remove copy after a failed pick", "path", exp.path, "err", err)
		return
	}
	if err := exp.dir.Sync(); err != nil {
		slog.Warn("picks: sync after removing a copy", "path", exp.path, "err", err)
	}
}

// removalCandidate returns the copy path (relative to the root, no leading
// "/") that leaving the picked state may remove, or "" when exportedPath may
// never be removed by path alone: empty, escaping the folder, or not inside
// a valid export directory (at the folder root, under .choix, traversal).
func removalCandidate(f store.File, exportedPath string) string {
	if exportedPath == "" {
		return ""
	}
	rel := strings.TrimLeft(exportedPath, "/") // v1 stored an absolute picks_dir joined under the root
	if err := ValidateDir(store.ExportDirOf(exportedPath, f.Path)); err != nil || !filepath.IsLocal(rel) {
		slog.Warn("picks: not removing an exported copy outside an export directory", "path", exportedPath)
		return ""
	}
	return rel
}

// removable decides, inside the transaction that leaves the picked state
// (so no other mutation can interleave), whether the copy at rel may be
// deleted: never when the database knows rel as an original in any
// scan_status (a 'missing' original may be restored before the next scan),
// never while another pick row records rel as its copy, and never when
// either lookup fails (fail closed).
func (s *Service) removable(tx *sql.Tx, rel string, fileID int64) bool {
	known, err := s.store.Files().PathKnownTx(tx, rel)
	if err != nil {
		slog.Warn("picks: keeping an exported copy: original lookup failed", "path", rel, "err", err)
		return false
	}
	if known {
		slog.Warn("picks: not removing an exported copy the scanner knows as an original", "path", rel)
		return false
	}
	shared, err := s.store.Picks().ExportReferencedTx(tx, rel, fileID)
	if err != nil {
		slog.Warn("picks: keeping an exported copy: reference lookup failed", "path", rel, "err", err)
		return false
	}
	if shared {
		slog.Info("picks: keeping an exported copy another pick still records", "path", rel)
		return false
	}
	return true
}

// removeExport removes the copy at rel (already cleared by removable), after
// the state change that stopped it being a pick has been committed. The
// copy's directory is opened through safefs from the stored path, so a pick
// exported under an older picks directory is removed too; a symlink is never
// removed. Failures are logged and leave an orphan copy; the committed state
// stands.
func (s *Service) removeExport(rel string) {
	dirRel, name := path.Split(rel)
	d, err := safefs.OpenDir(s.scanRoot, strings.TrimSuffix(dirRel, "/"), false)
	if errors.Is(err, fs.ErrNotExist) {
		return // the copy's directory is gone, and the copy with it
	}
	if err != nil {
		slog.Warn("picks: open the directory of an exported copy", "path", rel, "err", err)
		return
	}
	defer func() { _ = d.Close() }()
	trace("remove")
	if err := d.Remove(name); err != nil {
		slog.Warn("picks: remove exported copy; it stays as an orphan", "path", rel, "err", err)
		return
	}
	if err := d.Sync(); err != nil {
		slog.Warn("picks: sync after removing an exported copy", "path", rel, "err", err)
	}
}

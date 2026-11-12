package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

const (
	// previewCount is how many images a folder's preview strip holds.
	previewCount = 6
	// previewWidth bounds the width of every strip image.
	previewWidth = 512
	// thumbsPrefix is the cache directory, inside a folder, that strip
	// sources are read from.
	thumbsPrefix = ".choix/thumbs/"
	// maxPreviewSource bounds how much of a cached thumbnail is read. A
	// tier-2 preview is a 1600-px JPEG, far below this.
	maxPreviewSource = 64 << 20
)

// SampleIndices returns k indices into a list of n spread evenly from the
// first to the last: for n ≤ k every index 0…n-1, otherwise
// round(i·(n-1)/(k-1)) for i in 0…k-1. It never returns nil.
func SampleIndices(n, k int) []int {
	if n <= 0 || k <= 0 {
		return []int{}
	}
	if n <= k {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}
	if k == 1 {
		return []int{0}
	}
	out := make([]int, k)
	for i := range out {
		// Rounded half up, in integers.
		out[i] = (2*i*(n-1) + (k - 1)) / (2 * (k - 1))
	}
	return out
}

// RefreshPreviews rebuilds the folder's preview strip (C21): up to six
// images sampled evenly by capture time (SampleIndices) from
// st.Files().PreviewCandidates() — files neither missing nor rejected. Each
// image is made from the file's cached 'preview' tier when it has that row,
// else its 'thumb' tier, read from root's .choix/thumbs through safefs
// (read-only, no symbolic link followed), scaled by stills to at most 512 px
// wide, and written to dir as 0.jpg, 1.jpg, … A source that cannot be read
// or scaled is logged and left out.
//
// dir is the folder's previews directory, the app's own cache (absolute,
// named by the folder id, outside root); it must already exist and is never
// created here. The new set is written under temporary names first and
// published only when complete, by renaming each into place; files beyond
// the new count up to 5.jpg are then removed, and SetPreviews records the
// count with a stamp of Now that is above the previous one, so the page's
// image URLs change. library:changed "stats" is emitted.
//
// Refreshes of one folder run one at a time (a caller waits for the running
// one, or returns ctx.Err() if ctx is done first); refreshes of different
// folders run concurrently. Cancellation stops the refresh within the time
// one detached native call takes to be abandoned, leaving the previous set
// as it was. A folder removed
// from the Library before the set is published is not an error: the result
// is dropped, and neither its row nor its previews directory comes back.
func (l *Library) RefreshPreviews(ctx context.Context, id string, st *store.Store, root string, stills platform.Stills, dir string) error {
	name, published, err := l.refreshPreviews(ctx, id, st, root, stills, dir)
	if err != nil || !published {
		return err
	}
	l.emitChange(reasonStats, id, name)
	return nil
}

// refreshPreviews does RefreshPreviews' work without the event: it returns
// the folder's name and whether a new strip was published (false, with a
// nil error, for a folder removed from the Library).
func (l *Library) refreshPreviews(ctx context.Context, id string, st *store.Store, root string, stills platform.Stills, dir string) (name string, published bool, err error) {
	// One refresh of a folder at a time, from reading the candidates to
	// publishing, so one that read older data (a file since rejected) can
	// never publish after a newer one. Waiting honours ctx; other folders
	// are not held up.
	unlock, err := l.previewLocks.Lock(ctx, id)
	if err != nil {
		return "", false, fmt.Errorf("library: refresh previews of %s: %w", id, err)
	}
	defer unlock()

	f, err := l.db.Folder(id)
	if errors.Is(err, ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("library: refresh previews: %w", err)
	}
	if err := checkPreviewsDir(id, dir, root, f.Path); err != nil {
		return "", false, err
	}

	cands, err := st.Files().PreviewCandidates()
	if err != nil {
		return "", false, fmt.Errorf("library: refresh previews of %s: %w", id, err)
	}
	images := make([][]byte, 0, previewCount)
	for _, i := range SampleIndices(len(cands), previewCount) {
		if err := ctx.Err(); err != nil {
			return "", false, fmt.Errorf("library: refresh previews of %s: %w", id, err)
		}
		img, err := previewImage(ctx, st, root, stills, cands[i].FileID)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return "", false, fmt.Errorf("library: refresh previews of %s: %w", id, cerr)
			}
			slog.Warn("library: preview source skipped", "folder", id, "file", cands[i].FileID, "err", err)
			continue
		}
		images = append(images, img)
	}

	temps, err := writePreviewTemps(dir, images)
	if err != nil {
		// Remove deletes the row before the directory: if the directory
		// went away with its folder, there is nothing to report.
		if _, ferr := l.db.Folder(id); errors.Is(ferr, ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("library: refresh previews of %s: %w", id, err)
	}
	return l.publishLocked(ctx, id, dir, temps)
}

// checkPreviewsDir refuses a previews directory that is not an absolute path
// named by the folder id, or that is, contains or lies inside the folder
// (its root or stored path, symbolic links resolved or not): strip images
// are written with plain file operations, which must never reach a user
// folder.
func checkPreviewsDir(id, dir, root, stored string) error {
	if !validPreviewsDir(dir, id) {
		return fmt.Errorf("library: refresh previews of %s: unexpected previews directory %q", id, dir)
	}
	targets := []string{dir}
	if c, err := canonicalPath(dir); err == nil && c != dir {
		targets = append(targets, c)
	}
	for _, p := range protectedPaths(root, stored) {
		for _, d := range targets {
			if rel := relation(p, d); rel != "" {
				return fmt.Errorf("library: refresh previews of %s: previews directory %q overlaps the folder (%s)", id, dir, rel)
			}
		}
	}
	return nil
}

// previewImage returns the strip image for fileID: its cached 'preview'
// tier, or 'thumb' tier when it has no preview row, scaled to at most
// previewWidth px wide.
func previewImage(ctx context.Context, st *store.Store, root string, stills platform.Stills, fileID int64) ([]byte, error) {
	t, err := st.Thumbs().Get(fileID, "preview")
	if errors.Is(err, store.ErrNotFound) {
		t, err = st.Thumbs().Get(fileID, "thumb")
	}
	if err != nil {
		return nil, fmt.Errorf("thumbnail row: %w", err)
	}
	data, err := readCachedThumb(root, t.RelPath)
	if err != nil {
		return nil, err
	}
	img, err := stills.Thumbnail(ctx, platform.ImageInput{Data: data}, previewWidth, platform.FitWidth)
	if err != nil {
		return nil, fmt.Errorf("scale %s: %w", t.RelPath, err)
	}
	return img.JPEG, nil
}

// readCachedThumb reads rel, a slash-separated path that must lie under
// root's .choix/thumbs, through safefs: every directory on the way must be a
// real directory inside root and the file a regular file, opened read-only.
func readCachedThumb(root, rel string) ([]byte, error) {
	if !fs.ValidPath(rel) || !strings.HasPrefix(rel, thumbsPrefix) {
		return nil, fmt.Errorf("thumbnail path %q is not under %s", rel, thumbsPrefix)
	}
	d, err := safefs.OpenDir(root, path.Dir(rel), false)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path.Dir(rel), err)
	}
	defer func() { _ = d.Close() }()
	f, err := d.OpenRead(path.Base(rel))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", rel, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxPreviewSource+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	if len(data) > maxPreviewSource {
		return nil, fmt.Errorf("read %s: larger than %d bytes", rel, maxPreviewSource)
	}
	return data, nil
}

// writePreviewTemps writes each image to a new temporary file in dir, named
// ".<i>.jpg.<random>.tmp" so it is never taken for a strip image, and
// returns their paths in order. On failure every file it made is removed.
func writePreviewTemps(dir string, images [][]byte) ([]string, error) {
	temps := make([]string, 0, len(images))
	for i, data := range images {
		f, err := os.CreateTemp(dir, fmt.Sprintf(".%d.jpg.*.tmp", i))
		if err != nil {
			removeTemps(temps)
			return nil, fmt.Errorf("create preview %d: %w", i, err)
		}
		temps = append(temps, f.Name())
		_, werr := f.Write(data)
		cerr := f.Close()
		if err := errors.Join(werr, cerr); err != nil {
			removeTemps(temps)
			return nil, fmt.Errorf("write preview %d: %w", i, err)
		}
	}
	return temps, nil
}

// removeTemps removes the temporary files, logging failures other than
// absence.
func removeTemps(temps []string) {
	for _, p := range temps {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("library: remove a temporary preview", "file", p, "err", err)
		}
	}
}

// publishLocked puts the temporary files in place as 0.jpg… and records
// the new count. It runs under changeMu, which Remove holds while deleting
// the folder's row and, separately, its previews directory, so the folder
// and the directory are checked and the set published with neither able to
// go away meanwhile. A folder that is gone drops the set (published false).
func (l *Library) publishLocked(ctx context.Context, id, dir string, temps []string) (name string, published bool, err error) {
	l.changeMu.Lock()
	defer l.changeMu.Unlock()
	renamed := 0
	defer func() { removeTemps(temps[renamed:]) }()

	f, err := l.db.Folder(id)
	if errors.Is(err, ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("library: publish previews: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", false, fmt.Errorf("library: refresh previews of %s: %w", id, err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return "", false, fmt.Errorf("library: publish previews of %s: %w", id, err)
	}
	if !fi.IsDir() {
		return "", false, fmt.Errorf("library: publish previews of %s: %s is not a directory", id, dir)
	}

	// Same-directory renames in the app's own cache. Should one fail part-way
	// the strip is a mix of old and new images until the next refresh, which
	// rewrites every file; the error is returned and SetPreviews is skipped.
	for ; renamed < len(temps); renamed++ {
		final := filepath.Join(dir, fmt.Sprintf("%d.jpg", renamed))
		if err := os.Rename(temps[renamed], final); err != nil {
			return "", false, fmt.Errorf("library: publish preview %d of %s: %w", renamed, id, err)
		}
	}
	for i := len(temps); i < previewCount; i++ {
		stale := filepath.Join(dir, fmt.Sprintf("%d.jpg", i))
		if err := os.Remove(stale); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", false, fmt.Errorf("library: remove stale preview %d of %s: %w", i, id, err)
		}
	}

	stamp := max(l.now().Unix(), f.PreviewsStamp+1)
	if err := l.db.SetPreviews(id, len(temps), stamp); err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("library: publish previews: %w", err)
	}
	return f.Name, true, nil
}

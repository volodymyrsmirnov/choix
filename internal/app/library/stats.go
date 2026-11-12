package library

import (
	"context"
	"errors"
	"fmt"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// reasonStats is the library:changed reason sent when a folder's cached
// statistics or previews change.
const reasonStats = "stats"

// RefreshStats recomputes the folder's cached statistics from st, the
// folder's open state.db, and stores them in library.db: photo, video and
// failed counts and the capture-time range (store.FilesRepo.LibraryStats),
// the number of clusters, and the number of picked and rejected files. It
// then emits library:changed "stats". A folder removed from the Library
// meanwhile is not an error: the result is dropped and nothing is emitted.
// Refreshes of one folder run one at a time, so statistics read earlier are
// never stored over later ones.
func (l *Library) RefreshStats(id string, st *store.Store) error {
	name, stored, err := l.refreshStats(id, st)
	if err != nil || !stored {
		return err
	}
	l.emitChange(reasonStats, id, name)
	return nil
}

// RefreshStatsAndPreviews is RefreshStats followed by RefreshPreviews, each
// under its own per-folder lock, with one library:changed "stats" at the end
// instead of one each: it is emitted when either stored something (the
// statistics are stored even when ctx is done; the previews are not). A
// folder removed from the Library is not an error and emits nothing. The
// errors of both are returned, joined.
func (l *Library) RefreshStatsAndPreviews(ctx context.Context, id string, st *store.Store, root string, stills platform.Stills, dir string) error {
	name, stored, serr := l.refreshStats(id, st)
	pname, published, perr := l.refreshPreviews(ctx, id, st, root, stills, dir)
	if published {
		name = pname
	}
	if stored || published {
		l.emitChange(reasonStats, id, name)
	}
	return errors.Join(serr, perr)
}

// refreshStats does RefreshStats' work without the event: it returns the
// folder's name and whether the statistics were stored (false, with a nil
// error, for a folder removed from the Library).
func (l *Library) refreshStats(id string, st *store.Store) (name string, stored bool, err error) {
	unlock, err := l.statsLocks.Lock(context.Background(), id)
	if err != nil {
		return "", false, fmt.Errorf("library: refresh stats of %s: %w", id, err)
	}
	defer unlock()

	fs, err := st.Files().LibraryStats()
	if err != nil {
		return "", false, fmt.Errorf("library: refresh stats of %s: %w", id, err)
	}
	var clusters int
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM clusters`).Scan(&clusters); err != nil {
		return "", false, fmt.Errorf("library: refresh stats of %s: count clusters: %w", id, err)
	}
	picked, err := st.Picks().CountState("picked")
	if err != nil {
		return "", false, fmt.Errorf("library: refresh stats of %s: count picked: %w", id, err)
	}
	rejected, err := st.Picks().CountState("rejected")
	if err != nil {
		return "", false, fmt.Errorf("library: refresh stats of %s: count rejected: %w", id, err)
	}

	s := Stats{
		Photos: fs.Photos, Videos: fs.Videos, Clusters: clusters,
		Picked: picked, Rejected: rejected, Failed: fs.Failed,
		FirstCaptured: fs.FirstCaptured, LastCaptured: fs.LastCaptured,
	}
	// An UPDATE: a folder removed meanwhile matches no row and is not
	// recreated.
	if err := l.db.SetStats(id, s); err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("library: refresh stats: %w", err)
	}
	f, err := l.db.Folder(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("library: refresh stats: %w", err)
	}
	return f.Name, true, nil
}

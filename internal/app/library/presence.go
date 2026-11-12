package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// MissingMessage is what the Library page shows for a folder whose presence
// is missing: its bookmark no longer resolves, its volume is mounted, and
// its absence could not be verified.
const MissingMessage = "This folder can no longer be found."

// Folder presence values (folders.presence) set by the presence checks.
const (
	presenceOffline = "offline"
	presenceMissing = "missing"
)

// reasonGone is the library:changed reason of a folder dropped because it
// was verified to be in the Trash or deleted.
const reasonGone = "gone"

// defaultPresenceEvery is RunPresence's interval when given a non-positive
// one.
const defaultPresenceEvery = 30 * time.Second

// PresenceResult lists the folder ids a presence check changed, each in
// added_at order. Removed holds the folders dropped from the Library and
// Moved those whose new location was recorded. Offline, Missing,
// AccessLost and Online hold the folders whose presence changed to that
// state (Online: back to present) in this check; a folder that already had
// the state is not listed again.
type PresenceResult struct {
	Removed, Offline, Missing, Online, Moved, AccessLost []string
}

// add appends o's lists to r.
func (r *PresenceResult) add(o presenceOutcome) {
	if o.removed {
		r.Removed = append(r.Removed, o.id)
		return
	}
	if o.moved != nil {
		r.Moved = append(r.Moved, o.id)
	}
	switch o.became {
	case presenceOffline:
		r.Offline = append(r.Offline, o.id)
	case presenceMissing:
		r.Missing = append(r.Missing, o.id)
	case presenceAccessLost:
		r.AccessLost = append(r.AccessLost, o.id)
	case presencePresent:
		r.Online = append(r.Online, o.id)
	}
}

// presenceOutcome is what checking one folder decided.
type presenceOutcome struct {
	folder Folder // the row as read before the check
	id     string
	name   string // the folder's name after the check
	// removed is set when the row was dropped; removedAt is where its
	// bookmark resolved ("" when it did not).
	removed   bool
	removedAt string
	moved     func() // the "moved" notification, when the location changed
	// became is the presence the folder changed to, "" when unchanged.
	became string
	// resume is set when the folder is present with job_pending = 1.
	resume bool
}

// CheckPresence checks every folder, in added_at order, and records what
// it finds (spec §4.1, call C7):
//
//   - A bookmark that resolves into the Trash drops the row (library:changed
//     "gone"). When the Trash check fails it is repeated inside a short
//     access lease; when it still fails the folder is treated as not in the
//     Trash and a warning is logged once per folder.
//   - Otherwise a resolved bookmark whose path differs from the stored one is
//     followed (library:changed "moved"), a stale bookmark is refreshed (the
//     old bytes are kept when that fails), and an offline or missing folder
//     becomes present (library:changed "online").
//   - A bookmark whose target is gone makes the folder offline when its
//     volume has no UUID (a network share) or is not mounted; drops the row
//     when Volumes.AbsentOn verifies, on the folder's own mounted volume,
//     that the stored path does not exist; and makes it missing otherwise.
//   - A bookmark that can no longer be used makes the folder access_lost.
//   - Any other failure leaves the row unchanged and is logged.
//
// A row is never dropped only because its bookmark fails to resolve. A
// change to offline, missing or access_lost emits library:changed "offline".
//
// Each folder is checked under the lock Add and Remove take, re-reading its
// row, and its notifications are queued under that lock: callbacks and
// events are delivered in the order the changes were recorded — also
// relative to Add and Acquire — after the lock is released, so they may
// call back into the Library (see Options). OnRemoved is called for every dropped folder,
// OnOffline for every folder that went offline, missing or access_lost and
// for a moved folder that is in use, and OnOnline for every folder that came
// back and, on every check, for every present folder with a pending job.
//
// CheckPresence stops between folders when ctx is done and returns what it
// did so far with ctx's error.
func (l *Library) CheckPresence(ctx context.Context) (PresenceResult, error) {
	var res PresenceResult
	folders, err := l.db.Folders()
	if err != nil {
		return res, fmt.Errorf("library: check presence: %w", err)
	}
	for _, f := range folders {
		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("library: check presence: %w", err)
		}
		if err := l.checkFolder(f.ID, &res); err != nil && !errors.Is(err, ErrNotFound) {
			// The row could not be read: leave it for the next check.
			slog.Warn("library: check the presence of a folder", "folder", f.ID, "err", err)
		}
	}
	return res, nil
}

// CheckFolder checks one folder at once, as CheckPresence does for each.
// An unknown id is ErrNotFound.
func (l *Library) CheckFolder(ctx context.Context, id string) (PresenceResult, error) {
	var res PresenceResult
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("library: check presence of %s: %w", id, err)
	}
	if err := l.checkFolder(id, &res); err != nil {
		return res, fmt.Errorf("library: check presence of %s: %w", id, err)
	}
	return res, nil
}

// RunPresence checks every folder every interval until ctx is done,
// skipping a tick while visible returns false (nil: always visible). The
// checks on launch and when the window regains focus are CheckPresence
// calls made by the caller. A non-positive every is 30 s.
func (l *Library) RunPresence(ctx context.Context, every time.Duration, visible func() bool) {
	if every <= 0 {
		every = defaultPresenceEvery
	}
	newTicker := l.newTicker
	if newTicker == nil {
		newTicker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	ticks, stop := newTicker(every)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if visible != nil && !visible() {
				continue
			}
			if _, err := l.CheckPresence(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("library: periodic presence check", "err", err)
			}
		}
	}
}

// checkFolder checks the folder id, delivers the notifications queued
// (its own among them, in the order the changes were recorded) once every
// lock is released, and adds its outcome to res.
func (l *Library) checkFolder(id string, res *PresenceResult) error {
	o, err := l.examine(id)
	if err != nil {
		return err
	}
	if l.afterExamine != nil {
		l.afterExamine(id)
	}
	l.drain()
	res.add(o)
	return nil
}

// examine checks the folder id under changeMu, records the outcome and
// queues its notifications under that lock, so they are delivered in the
// order of the changes they report.
func (l *Library) examine(id string) (presenceOutcome, error) {
	l.changeMu.Lock()
	defer l.changeMu.Unlock()
	o, err := l.examineLocked(id)
	if err == nil && o.notifies() {
		l.enqueueLocked(func() { l.notifyPresence(o) })
	}
	return o, err
}

// notifies reports whether the outcome calls for any notification.
func (o presenceOutcome) notifies() bool {
	return o.removed || o.moved != nil || o.became != "" || o.resume
}

// examineLocked does examine's work. Called holding changeMu.
func (l *Library) examineLocked(id string) (presenceOutcome, error) {
	folders, err := l.db.Folders()
	if err != nil {
		return presenceOutcome{}, err
	}
	i := slices.IndexFunc(folders, func(f Folder) bool { return f.ID == id })
	if i < 0 {
		return presenceOutcome{}, fmt.Errorf("library: folder %s: %w", id, ErrNotFound)
	}
	f := folders[i]
	o := presenceOutcome{folder: f, id: f.ID, name: f.Name}

	path, stale, err := l.bookmarks.Resolve(f.Bookmark)
	switch {
	case err == nil:
		l.examineResolvedLocked(&o, path, stale, folders)
	case errors.Is(err, platform.ErrGone):
		l.examineGoneLocked(&o)
	case errors.Is(err, platform.ErrUnusable):
		if l.setPresenceLocked(&o, presenceAccessLost) {
			slog.Warn("library: access to a folder was lost", "folder", f.ID, "err", err)
		}
	default:
		slog.Warn("library: resolve the bookmark of a folder", "folder", f.ID, "err", err)
	}
	return o, nil
}

// examineResolvedLocked handles a folder whose bookmark resolves to path:
// dropped when it is in the Trash, otherwise followed, refreshed and made
// present. Called holding changeMu.
func (l *Library) examineResolvedLocked(o *presenceOutcome, path string, stale bool, folders []Folder) {
	f := o.folder
	if l.inTrashLocked(f, path) {
		l.dropLocked(o, path, "in the Trash")
		return
	}
	if path != f.Path {
		if moved := l.relocateLocked(f, path, folders); moved != nil {
			o.moved = moved
			o.name = filepath.Base(path)
		}
	}
	if stale {
		l.refreshLocked(f)
	}
	presence := f.Presence
	if presence == presenceOffline || presence == presenceMissing {
		if l.setPresenceLocked(o, presencePresent) {
			presence = presencePresent
		}
	}
	o.resume = presence == presencePresent && f.JobPending
}

// refreshLocked replaces the folder's stale bookmark with a refreshed one.
// On any failure the old bytes are kept; a failing refresh is logged once
// per folder until one succeeds. Called holding changeMu.
func (l *Library) refreshLocked(f Folder) {
	fresh, err := l.bookmarks.Refresh(f.Bookmark)
	if err == nil {
		if err = l.db.SetBookmark(f.ID, fresh); err != nil {
			err = fmt.Errorf("store: %w", err)
		}
	}
	if err == nil {
		delete(l.refreshWarned, f.ID)
		return
	}
	if l.refreshWarned == nil {
		l.refreshWarned = map[string]bool{}
	}
	if !l.refreshWarned[f.ID] {
		l.refreshWarned[f.ID] = true
		slog.Warn("library: refresh the stale bookmark of a folder; keeping the old one", "folder", f.ID, "err", err)
	}
}

// examineGoneLocked handles a folder whose bookmark target is gone. That
// alone never drops the row: deleted and "volume not mounted" give the same
// error (spike S1), so they are told apart through the volume. Called
// holding changeMu.
func (l *Library) examineGoneLocked(o *presenceOutcome) {
	f := o.folder
	switch {
	case f.VolumeUUID == "" || !l.volumes.Mounted(f.VolumeUUID):
		// No identity (a network share) or not mounted: absence cannot be
		// verified.
		l.setPresenceLocked(o, presenceOffline)
	case l.volumes.AbsentOn(f.Path, f.VolumeUUID):
		l.dropLocked(o, "", "verified absent on its volume")
	default:
		// Mounted, but the absence is not verifiable: not permitted, or the
		// volume is mounted at another path.
		l.setPresenceLocked(o, presenceMissing)
	}
}

// inTrashLocked reports whether the folder, whose bookmark resolves to
// path, is verified to be in the Trash. When the check fails it is repeated
// inside a short access lease; when that fails too, the folder is treated as
// not in the Trash and a warning is logged once per folder. Called holding
// changeMu.
func (l *Library) inTrashLocked(f Folder, path string) bool {
	in, err := l.volumes.InTrash(path)
	if err == nil {
		return in
	}
	in, err = l.inTrashUnderLease(f, path)
	if err == nil {
		return in
	}
	if l.trashWarned == nil {
		l.trashWarned = map[string]bool{}
	}
	if !l.trashWarned[f.ID] {
		l.trashWarned[f.ID] = true
		slog.Warn("library: cannot tell whether a folder is in the Trash; keeping it", "folder", f.ID, "err", err)
	}
	return false
}

// inTrashUnderLease repeats the Trash check of path while security-scoped
// access to the folder is held, and stops that access before returning.
func (l *Library) inTrashUnderLease(f Folder, path string) (bool, error) {
	at, stop, err := l.bookmarks.StartAccess(f.Bookmark)
	if err != nil {
		return false, fmt.Errorf("start access for the Trash check: %w", err)
	}
	defer stop()
	if at != path {
		// Resolved elsewhere since: check where access was granted.
		path = at
	}
	return l.volumes.InTrash(path)
}

// dropLocked deletes the folder's row: it was verified to be in the Trash
// (its bookmark resolving to at) or deleted. A failure is logged and the row
// is left for the next check. Called holding changeMu.
func (l *Library) dropLocked(o *presenceOutcome, at, why string) {
	if err := l.db.DeleteFolder(o.id); err != nil {
		slog.Warn("library: drop a folder that is gone", "folder", o.id, "why", why, "err", err)
		return
	}
	slog.Info("library: dropped a folder that is gone", "folder", o.id, "why", why)
	o.removed = true
	o.removedAt = at
}

// setPresenceLocked records presence p for the folder when it differs from
// the stored one, and reports whether it changed. A failure is logged.
// Called holding changeMu.
func (l *Library) setPresenceLocked(o *presenceOutcome, p string) bool {
	if o.folder.Presence == p {
		return false
	}
	if err := l.db.SetPresence(o.id, p); err != nil {
		slog.Warn("library: record the presence of a folder", "folder", o.id, "presence", p, "err", err)
		return false
	}
	o.became = p
	return true
}

// notifyPresence sends the callbacks and events o calls for. It is queued by
// examine and run by drain, with no Library lock held.
func (l *Library) notifyPresence(o presenceOutcome) {
	if o.removed {
		if l.onRemoved != nil {
			l.onRemoved(o.id)
		}
		l.removePreviews(o.folder, o.removedAt)
		l.emitChange(reasonGone, o.id, o.folder.Name)
		return
	}
	if o.moved != nil {
		o.moved()
		// A job working at the old location stops; it resumes below or on
		// the next check, at the new one.
		if l.onOffline != nil && l.InUse(o.id) {
			l.onOffline(o.id)
		}
	}
	switch o.became {
	case presenceOffline, presenceMissing, presenceAccessLost:
		l.emitChange(reasonOffline, o.id, o.name)
		if l.onOffline != nil {
			l.onOffline(o.id)
		}
	case presencePresent:
		l.emitChange(reasonOnline, o.id, o.name)
	}
	if (o.became == presencePresent || o.resume) && l.onOnline != nil {
		l.onOnline(o.id)
	}
}

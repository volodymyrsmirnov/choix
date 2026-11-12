package library

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// insertFile adds a file row of kind with scan_status status, captured at
// captured (zero: no capture time), and returns its id.
func insertFile(t *testing.T, st *store.Store, path, kind, status string, captured time.Time) int64 {
	t.Helper()
	format := "jpeg"
	if kind == "video" {
		format = "mov"
	}
	id, err := st.Files().Insert(store.File{
		Path: path, Size: 1, Mtime: 1, ContentHash: path, Kind: kind, Format: format, ScanStatus: status,
	})
	if err != nil {
		t.Fatalf("insert %s: %v", path, err)
	}
	var at sql.NullInt64
	if !captured.IsZero() {
		at = sql.NullInt64{Int64: captured.Unix(), Valid: true}
	}
	if _, err := st.DB().ExecContext(context.Background(),
		`UPDATE files SET captured_at = ? WHERE id = ?`, at, id); err != nil {
		t.Fatalf("set captured_at of %s: %v", path, err)
	}
	return id
}

// setPick records state ("picked" or "rejected") for the file.
func setPick(t *testing.T, st *store.Store, fileID int64, state string) {
	t.Helper()
	if err := st.Picks().Upsert(store.Pick{FileID: fileID, State: state, PickedAt: 1}); err != nil {
		t.Fatalf("pick %d %s: %v", fileID, state, err)
	}
}

func TestRefreshStats_CountsAndRange(t *testing.T) {
	h := newHarness(t)
	res := h.mustAdd(t, h.mkdir(t, "Trip"))
	st := store.NewTestStore(t)

	first := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	last := time.Date(2026, 3, 6, 18, 0, 0, 0, time.UTC)
	a := insertFile(t, st, "a.jpg", "photo", "analyzed", first)
	b := insertFile(t, st, "b.jpg", "photo", "analyzed", first.Add(time.Hour))
	insertFile(t, st, "c.jpg", "photo", "failed", time.Time{})
	insertFile(t, st, "d.jpg", "photo", "analyzed", last.Add(-time.Hour))
	insertFile(t, st, "e.mov", "video", "analyzed", last)
	// A file gone from disk counts for nothing, not even its date.
	insertFile(t, st, "gone.jpg", "photo", "missing", first.Add(-24*time.Hour))
	for range 2 {
		if _, err := st.Clusters().Insert("cam", sql.NullInt64{}); err != nil {
			t.Fatalf("insert cluster: %v", err)
		}
	}
	setPick(t, st, a, "picked")
	setPick(t, st, b, "rejected")

	if err := h.lib.RefreshStats(res.ID, st); err != nil {
		t.Fatalf("RefreshStats: %v", err)
	}
	f, err := h.lib.Folder(res.ID)
	if err != nil {
		t.Fatalf("Folder: %v", err)
	}
	want := Stats{
		Photos: 4, Videos: 1, Clusters: 2, Picked: 1, Rejected: 1, Failed: 1,
		FirstCaptured: sql.NullInt64{Int64: first.Unix(), Valid: true},
		LastCaptured:  sql.NullInt64{Int64: last.Unix(), Valid: true},
	}
	if f.Stats != want {
		t.Fatalf("stats = %+v, want %+v", f.Stats, want)
	}
	if got := h.libraryEvents(t, res.ID); !slices.Equal(got, []string{"added", "stats"}) {
		t.Fatalf("events = %v, want [added stats]", got)
	}
}

func TestRefreshStats_EmptyFolder(t *testing.T) {
	h := newHarness(t)
	res := h.mustAdd(t, h.mkdir(t, "Trip"))
	if err := h.lib.RefreshStats(res.ID, store.NewTestStore(t)); err != nil {
		t.Fatalf("RefreshStats: %v", err)
	}
	f, err := h.lib.Folder(res.ID)
	if err != nil {
		t.Fatalf("Folder: %v", err)
	}
	if f.Stats != (Stats{}) {
		t.Fatalf("stats = %+v, want zero", f.Stats)
	}
}

// RefreshStats of a folder waits for one already running for that folder.
func TestRefreshStats_Serialised(t *testing.T) {
	h := newHarness(t)
	res := h.mustAdd(t, h.mkdir(t, "Trip"))
	st := store.NewTestStore(t)
	insertFile(t, st, "a.jpg", "photo", "analyzed", time.Time{})

	unlock, err := h.lib.statsLocks.Lock(context.Background(), res.ID) // a refresh in progress
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- h.lib.RefreshStats(res.ID, st) }()
	waitForCount(t, &h.lib.statsLocks, res.ID, 2)
	if f, err := h.lib.Folder(res.ID); err != nil || f.Stats.Photos != 0 {
		t.Fatalf("stats stored while another refresh runs: %+v, %v", f.Stats, err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatalf("RefreshStats: %v", err)
	}
	if f, err := h.lib.Folder(res.ID); err != nil || f.Stats.Photos != 1 {
		t.Fatalf("stats = %+v, %v; want 1 photo", f.Stats, err)
	}
}

// A folder removed before its statistics are stored is not resurrected and
// the refresh is not an error.
func TestRefreshStats_RemovedFolder(t *testing.T) {
	h := newHarness(t)
	res := h.mustAdd(t, h.mkdir(t, "Trip"))
	st := store.NewTestStore(t)
	insertFile(t, st, "a.jpg", "photo", "analyzed", time.Time{})
	if err := h.lib.Remove(res.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := h.lib.RefreshStats(res.ID, st); err != nil {
		t.Fatalf("RefreshStats of a removed folder = %v, want nil", err)
	}
	if fs := h.mustFolders(t); len(fs) != 0 {
		t.Fatalf("folders = %d, want 0", len(fs))
	}
	if got := h.libraryEvents(t, res.ID); !slices.Equal(got, []string{"added", "removed"}) {
		t.Fatalf("events = %v, want [added removed]", got)
	}
}

// RefreshStatsAndPreviews stores both and emits one library:changed "stats".
func TestRefreshStatsAndPreviews_OneEvent(t *testing.T) {
	e := newPreviewEnv(t)
	for n := range 3 {
		e.addFile(t, n, "thumb", "preview")
	}
	if err := e.h.lib.RefreshStatsAndPreviews(context.Background(), e.id, e.st, e.root, e.stills, e.dir); err != nil {
		t.Fatalf("RefreshStatsAndPreviews: %v", err)
	}
	f := e.folder(t)
	if f.Stats.Photos != 3 || f.Previews != 3 || f.PreviewsStamp == 0 {
		t.Fatalf("row = photos %d previews %d stamp %d; want 3, 3 and a stamp", f.Stats.Photos, f.Previews, f.PreviewsStamp)
	}
	if got := e.strip(t); len(got) != 3 {
		t.Fatalf("strip = %v; want 3 images", got)
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "stats"}) {
		t.Fatalf("events = %v, want [added stats]", got)
	}
}

// A folder removed before the refresh is not an error, is not recreated and
// emits nothing.
func TestRefreshStatsAndPreviews_RemovedFolder(t *testing.T) {
	e := newPreviewEnv(t)
	e.addFile(t, 0, "thumb")
	if err := e.h.lib.Remove(e.id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := e.h.lib.RefreshStatsAndPreviews(context.Background(), e.id, e.st, e.root, e.stills, e.dir); err != nil {
		t.Fatalf("RefreshStatsAndPreviews of a removed folder = %v, want nil", err)
	}
	if fs := e.h.mustFolders(t); len(fs) != 0 {
		t.Fatalf("folders = %d, want 0", len(fs))
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "removed"}) {
		t.Fatalf("events = %v, want [added removed]", got)
	}
}

// A cancelled context stores the statistics but leaves the strip as it was;
// the one event reports the statistics.
func TestRefreshStatsAndPreviews_Cancelled(t *testing.T) {
	e := newPreviewEnv(t)
	ids := make([]int64, 3)
	for n := range ids {
		ids[n] = e.addFile(t, n, "thumb", "preview")
	}
	e.mustRefresh(t)
	before := e.folder(t)
	setPick(t, e.st, ids[1], "rejected")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := e.h.lib.RefreshStatsAndPreviews(ctx, e.id, e.st, e.root, e.stills, e.dir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RefreshStatsAndPreviews = %v, want context.Canceled", err)
	}
	f := e.folder(t)
	if f.Stats.Rejected != 1 {
		t.Fatalf("rejected = %d; want the statistics stored", f.Stats.Rejected)
	}
	if f.Previews != before.Previews || f.PreviewsStamp != before.PreviewsStamp {
		t.Fatalf("previews %d stamp %d; want the previous %d stamp %d", f.Previews, f.PreviewsStamp, before.Previews, before.PreviewsStamp)
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "stats", "stats"}) {
		t.Fatalf("events = %v, want [added stats stats] (the first refresh's, then one)", got)
	}
}

package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/catalog"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/settings"
	"github.com/volodymyrsmirnov/choix/internal/app/system"
	"github.com/volodymyrsmirnov/choix/internal/appdir"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

func ptr[T any](v T) *T { return &v }

// openState opens the folder's state.db directly (the runtime is shut down
// or still holds its own connection; SQLite's WAL allows both).
func openState(t *testing.T, root string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(root, ".choix", "state.db"))
	if err != nil {
		t.Fatalf("open state.db of %s: %v", root, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// openLibrary opens library.db directly, after the runtime has shut down.
func openLibrary(t *testing.T) *library.DB {
	t.Helper()
	p, err := appdir.LibraryDB()
	if err != nil {
		t.Fatal(err)
	}
	db, err := library.OpenDB(p)
	if err != nil {
		t.Fatalf("open library.db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func countRows(t *testing.T, st *store.Store, query string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// shutdownWithin runs Shutdown and fails the test when it takes longer than
// limit; it returns how long it took.
func shutdownWithin(t *testing.T, r *Runtime, limit time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	r.Shutdown()
	took := time.Since(start)
	if took > limit {
		t.Fatalf("Shutdown took %v; want under %v", took, limit)
	}
	return took
}

func TestRuntime_AddScanOpenPick(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 12, 64, 48)
	r := h.start(t)

	id := h.add(t, r, root)
	s := waitReady(t, r, id)
	if s.Photos != 12 {
		t.Fatalf("photos = %d; want 12", s.Photos)
	}
	if len(s.Previews) != 6 {
		t.Fatalf("previews = %v; want six URLs", s.Previews)
	}

	view, err := r.Catalog.Open(id)
	if err != nil {
		t.Fatalf("Catalog.Open: %v", err)
	}
	if len(view.Clusters) == 0 || len(view.Clusters[0].Members) == 0 {
		t.Fatalf("Catalog.Open returned no clusters: %+v", view)
	}
	fileID := view.Clusters[0].Members[0].FileID
	res, err := r.Catalog.SetPick(id, fileID, "pick")
	if err != nil {
		t.Fatalf("SetPick: %v", err)
	}
	if res.State != "picked" || !strings.HasPrefix(res.ExportedPath, "picks/") {
		t.Fatalf("SetPick = %+v; want picked, exported under picks/", res)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(res.ExportedPath))); err != nil {
		t.Fatalf("the pick's copy is missing: %v", err)
	}

	shutdownWithin(t, r, QuitDeadline+time.Second)
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("bookmark access still active after Shutdown: %v", got)
	}
}

func TestRuntime_EventsOnTheWire(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 3, 64, 48)
	r := h.start(t)
	id := h.add(t, r, root)
	waitReady(t, r, id)

	if !h.em.libraryChanged(id, "added") {
		t.Error(`no library:changed "added" for the folder`)
	}
	states := h.em.scanStates(id)
	q, s, rd := slices.Index(states, "queued"), slices.Index(states, "scanning"), slices.Index(states, "ready")
	if q < 0 || s < q || rd < s {
		t.Errorf("scan:state = %v; want queued, scanning, ready in that order", states)
	}
	progress := 0
	for _, d := range h.em.all(events.ScanProgress) {
		if ev, ok := d.(events.ScanProgressEvent); ok && ev.FolderID == id {
			progress++
		}
	}
	if progress == 0 {
		t.Error("no scan:progress for the folder")
	}
	if h.em.count(events.ModelsProgress) == 0 {
		t.Error("no models:progress")
	}
}

func TestRuntime_ModelDownloadedQueuesBackfill(t *testing.T) {
	h := newHarness(t)
	h.dl = newFakeDownloader(local.ModelQuality) // CLIP is missing
	h.emb.dl = h.dl
	releaseCLIP := h.dl.holdKind(local.ModelCLIP)
	defer releaseCLIP()
	root := h.folder(t, "Trip", 3, 64, 48)
	r := h.start(t)
	id := h.add(t, r, root)

	waitFor(t, "the first job to finish", func() bool { return h.em.countState(id, "ready") >= 1 })
	if n := countRows(t, openState(t, root), `SELECT COUNT(*) FROM ai_signals WHERE clip_embedding IS NOT NULL`); n != 0 {
		t.Fatalf("embeddings before CLIP was installed = %d; want 0", n)
	}

	releaseCLIP()
	waitFor(t, "the backfill job to finish", func() bool { return h.em.countState(id, "ready") >= 2 })
	waitReady(t, r, id)
	if n := countRows(t, openState(t, root), `SELECT COUNT(*) FROM ai_signals WHERE clip_embedding IS NOT NULL`); n != 3 {
		t.Fatalf("embeddings after the backfill = %d; want 3", n)
	}
}

func TestRuntime_ModelPresentAtLaunchQueuesNothing(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 3, 64, 48)
	r := h.start(t)
	id := h.add(t, r, root)
	waitReady(t, r, id)
	r.Shutdown()

	calls := h.stills.callCount()
	embeds := h.emb.calls.Load()
	h.em = newRecEmitter(h.log)
	r2 := h.start(t)
	if !r2.Idle() {
		t.Fatal("Idle() = false right after Start with both models on disk and nothing pending")
	}
	if n := h.stills.callCount(); n != calls {
		t.Fatalf("Stills calls after Start = %d; want none (%d before)", n-calls, calls)
	}
	if n := h.emb.calls.Load(); n != embeds {
		t.Fatalf("Embed calls after Start = %d; want none", n-embeds)
	}
	if n := h.em.count(events.ScanState); n != 0 {
		t.Fatalf("scan:state events after Start = %d; want none", n)
	}
}

func TestRuntime_VisualGroupingOffNeverDownloadsClip(t *testing.T) {
	h := newHarness(t)
	h.setConfig(t, func(c *config.Config) { c.VisualGrouping = false })
	h.dl = newFakeDownloader() // nothing installed
	h.emb.dl = h.dl
	root := h.folder(t, "Trip", 3, 64, 48)
	r := h.start(t)
	id := h.add(t, r, root)
	waitReady(t, r, id)

	if n := h.dl.downloads(local.ModelCLIP); n != 0 {
		t.Fatalf("CLIP downloads = %d; want none", n)
	}
	if n := h.emb.calls.Load(); n != 0 {
		t.Fatalf("Embed calls = %d; want none", n)
	}
	st, err := r.Models.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st[0].Kind != "clip" || st[0].State != "disabled" {
		t.Fatalf("clip status = %+v; want disabled", st[0])
	}
}

func TestRuntime_SettingSwitchedOnQueuesBackfill(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 3, 512, 512)
	r := h.start(t)
	id := h.add(t, r, root)
	waitReady(t, r, id)
	if n := h.inf.calls.Load(); n != 0 {
		t.Fatalf("quality crops while the rating is off = %d; want none", n)
	}

	if _, err := r.Settings.Save(settings.Patch{QualityRating: ptr(true)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	waitFor(t, "the backfill job to finish", func() bool { return h.em.countState(id, "ready") >= 2 })
	waitReady(t, r, id)
	if n := countRows(t, openState(t, root), `SELECT COUNT(*) FROM files WHERE quality_raw IS NOT NULL`); n != 3 {
		t.Fatalf("rated photos = %d; want 3", n)
	}
}

func TestRuntime_QualityOffStopsScoringAtOnce(t *testing.T) {
	h := newHarness(t)
	h.setConfig(t, func(c *config.Config) { c.QualityRating = true })
	release := h.inf.holdCalls()
	defer release()
	root := h.folder(t, "Trip", 3, 512, 512)
	r := h.start(t)
	id := h.add(t, r, root)
	receive(t, "a quality crop in flight", h.inf.entered)

	if _, err := r.Settings.Save(settings.Patch{QualityRating: ptr(false)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	q, err := r.Models.Quality()
	if err != nil {
		t.Fatal(err)
	}
	if q.State != "disabled" {
		t.Fatalf("quality status right after switching it off = %q; want disabled", q.State)
	}
	release()
	waitReady(t, r, id)

	// The photo in flight may finish; no other photo is scored.
	if n := h.inf.calls.Load(); n > 5 {
		t.Fatalf("quality crops = %d; want at most one photo's five", n)
	}
	if n := countRows(t, openState(t, root), `SELECT COUNT(*) FROM files WHERE quality_model IS NOT NULL`); n > 1 {
		t.Fatalf("photos rated = %d; want at most the one in flight", n)
	}
}

// A new folder's clusters are published before its quality pass: while the
// first photo is being rated, the Library row already has its counts and
// previews (still "scanning"), and the open folder has had its folder:changed
// "clusters".
func TestRuntime_ClustersShowWhileRatingRuns(t *testing.T) {
	h := newHarness(t)
	h.setConfig(t, func(c *config.Config) { c.QualityRating = true })
	release := h.inf.holdCalls()
	defer release()
	root := h.folder(t, "Trip", 3, 512, 512)
	r := h.start(t)
	id := h.add(t, r, root)
	receive(t, "a quality crop in flight", h.inf.entered)

	waitFor(t, "the Library row's counts and previews during the rating pass", func() bool {
		s := summary(t, r, id)
		return s.Photos == 3 && s.Clusters > 0 && len(s.Previews) > 0
	})
	if s := summary(t, r, id); s.State != "scanning" {
		t.Fatalf("row state during the rating pass = %q; want scanning", s.State)
	}
	clusters := func() int {
		n := 0
		for _, d := range h.em.all(events.FolderChanged) {
			if ev, ok := d.(events.FolderChangedEvent); ok && ev.FolderID == id && ev.Reason == "clusters" {
				n++
			}
		}
		return n
	}
	waitFor(t, `folder:changed "clusters" during the rating pass`, func() bool { return clusters() == 1 })

	release()
	waitReady(t, r, id)
	if n := clusters(); n != 1 {
		t.Fatalf(`folder:changed "clusters" = %d; want 1 (the pass changes no cluster)`, n)
	}
	if n := countRows(t, openState(t, root), `SELECT COUNT(*) FROM files WHERE quality_raw IS NOT NULL`); n != 3 {
		t.Fatalf("rated photos = %d; want 3", n)
	}
}

// Review Focus 1: a drive unplugged while its folder scans.
func TestRuntime_UnplugAndReplug(t *testing.T) {
	h := newHarness(t)
	card := filepath.Join(h.parent, "Card")
	h.vols.Add(card, platform.VolumeInfo{UUID: "UUID-CARD", Name: "Card", Removable: true})
	root := h.folder(t, "Card/Trip", 3, 64, 48)
	release := h.stills.holdUnder(root)
	defer release()
	r := h.start(t)
	id := h.add(t, r, root)
	receive(t, "a native call on the folder", h.stills.held)

	away := filepath.Join(h.parent, "unplugged")
	if err := os.Rename(root, away); err != nil {
		t.Fatal(err)
	}
	h.bm.unplug(root)
	h.vols.Unmount("UUID-CARD")
	release()

	// No presence tick runs (presenceEvery is an hour): the suspension's own
	// CheckFolder marks the row offline.
	waitFor(t, "the folder offline", func() bool { return summary(t, r, id).State == "offline" })
	waitFor(t, "the queue idle", r.Idle)
	f, err := r.lib.Folder(id)
	if err != nil {
		t.Fatal(err)
	}
	if !f.JobPending {
		t.Fatal("job_pending = 0 after the unplug; want the job kept")
	}
	if s := summary(t, r, id); s.VolumeName != "Card" {
		t.Fatalf("offline row = %+v; want volume Card", s)
	}

	if err := os.Rename(away, root); err != nil {
		t.Fatal(err)
	}
	h.bm.plug(root)
	h.vols.Mount("UUID-CARD")
	if err := r.Library.CheckNow(); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	s := waitReady(t, r, id)
	if s.Photos != 3 {
		t.Fatalf("photos after the drive returned = %d; want 3", s.Photos)
	}
	if n := countRows(t, openState(t, root), `SELECT COUNT(*) FROM files WHERE scan_status = 'failed'`); n != 0 {
		t.Fatalf("failed files = %d; want none (the outage is not a file failure)", n)
	}
}

// The open folder goes offline, comes back, and the frontend opens it again
// (as it does on library:changed "online") before the catalog's background
// close of the offline instance has run. That close acts only on the
// instance the offline notification let go of: the reopened folder stays
// open and usable.
func TestRuntime_OfflineCloseSparesReopenedFolder(t *testing.T) {
	h := newHarness(t)
	card := filepath.Join(h.parent, "Card")
	h.vols.Add(card, platform.VolumeInfo{UUID: "UUID-CARD", Name: "Card", Removable: true})
	root := h.folder(t, "Card/Trip", 3, 64, 48)
	r := h.newRuntime(t)
	var (
		heldMu sync.Mutex
		held   []func()
	)
	r.closeLater = func(fn func()) {
		heldMu.Lock()
		defer heldMu.Unlock()
		held = append(held, fn)
	}
	runHeld := func() int {
		heldMu.Lock()
		fns := held
		held = nil
		heldMu.Unlock()
		for _, fn := range fns {
			fn()
		}
		return len(fns)
	}
	t.Cleanup(func() { runHeld() }) // a close that is never run would hold up Shutdown
	r.Start(context.Background())
	id := h.add(t, r, root)
	waitReady(t, r, id)
	if _, err := r.Catalog.Open(id); err != nil {
		t.Fatalf("Catalog.Open: %v", err)
	}
	waitFor(t, "the rescan of the opened folder", func() bool { return h.em.countState(id, "ready") >= 2 })
	waitReady(t, r, id)

	// Unplugged: the folder turns offline and its close is handed off.
	away := filepath.Join(h.parent, "unplugged")
	if err := os.Rename(root, away); err != nil {
		t.Fatal(err)
	}
	h.bm.unplug(root)
	h.vols.Unmount("UUID-CARD")
	if err := r.Library.CheckNow(); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	waitFor(t, "the folder offline", func() bool { return summary(t, r, id).State == "offline" })
	waitFor(t, "the offline folder's close handed off", func() bool {
		heldMu.Lock()
		defer heldMu.Unlock()
		return len(held) > 0
	})

	// Back, and opened again by the frontend before that close has run.
	if err := os.Rename(away, root); err != nil {
		t.Fatal(err)
	}
	h.bm.plug(root)
	h.vols.Mount("UUID-CARD")
	if err := r.Library.CheckNow(); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	waitFor(t, "the folder online", func() bool { return summary(t, r, id).State != "offline" })
	view, err := r.Catalog.Open(id)
	if err != nil {
		t.Fatalf("Catalog.Open after the drive returned: %v", err)
	}

	if n := runHeld(); n == 0 {
		t.Fatal("no close was handed off")
	}
	if _, err := r.Catalog.Clusters(id); err != nil {
		t.Fatalf("Clusters after the offline instance's close ran = %v; want the reopened folder still open", err)
	}
	if len(view.Clusters) == 0 || len(view.Clusters[0].Members) == 0 {
		t.Fatalf("reopened view has no clusters: %+v", view)
	}
	res, err := r.Catalog.SetPick(id, view.Clusters[0].Members[0].FileID, "pick")
	if err != nil || res.State != "picked" {
		t.Fatalf("SetPick on the reopened folder = %+v, %v; want picked", res, err)
	}
}

// The folder disappears and comes back while its job is held: the job
// suspends, but no presence pass ever saw the folder gone. The suspension's
// CheckFolder finds it present with its job pending and queues it again.
func TestRuntime_ReplugBeforeAnyPresencePass(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 3, 64, 48)
	release := h.stills.holdUnder(root)
	defer release()
	r := h.start(t)
	id := h.add(t, r, root)
	receive(t, "a native call on the folder", h.stills.held)

	// Watch the Library row throughout: it is never ready while a job is
	// pending.
	stop := make(chan struct{})
	var violations atomic.Int64
	var watcher sync.WaitGroup
	watcher.Add(1)
	go func() {
		defer watcher.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			list, err := r.Library.List()
			if err != nil {
				return
			}
			for _, s := range list {
				if s.ID != id || s.State != "ready" {
					continue
				}
				// job_pending only goes from 1 to 0 when the last job ends, so
				// a row read after a "ready" one shows whether it was pending.
				if f, err := r.lib.Folder(id); err == nil && f.JobPending {
					violations.Add(1)
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	away := filepath.Join(h.parent, "gone-for-a-moment")
	if err := os.Rename(root, away); err != nil {
		t.Fatal(err)
	}
	// The folder is back by the time the suspension checks it.
	h.bm.armResolve(func() {
		if err := os.Rename(away, root); err != nil {
			t.Errorf("put the folder back: %v", err)
		}
	})
	release()

	s := waitReady(t, r, id)
	close(stop)
	watcher.Wait()
	if s.Photos != 3 {
		t.Fatalf("photos = %d; want 3", s.Photos)
	}
	if n := h.em.countState(id, "scanning"); n < 2 {
		t.Fatalf("scanning states = %d; want the job to have run again", n)
	}
	if n := violations.Load(); n != 0 {
		t.Fatalf("Library.List reported ready %d times while the job was pending", n)
	}
}

func TestRuntime_MovedWhileScanning(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 3, 64, 48)
	release := h.stills.holdUnder(root)
	defer release()
	r := h.start(t)
	id := h.add(t, r, root)
	receive(t, "a native call on the folder", h.stills.held)

	moved := filepath.Join(h.parent, "Trip renamed")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	h.bm.Move(root, moved)
	release()

	waitFor(t, "the new location recorded", func() bool {
		f, err := r.lib.Folder(id)
		return err == nil && f.Path == moved
	})
	if err := r.Library.CheckNow(); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	s := waitReady(t, r, id)
	if s.Name != "Trip renamed" || s.Photos != 3 {
		t.Fatalf("row = %+v; want the renamed folder with 3 photos", s)
	}
	if n := countRows(t, openState(t, moved), `SELECT COUNT(*) FROM files WHERE scan_status = 'analyzed'`); n != 3 {
		t.Fatalf("analyzed files at the new location = %d; want 3", n)
	}
}

func TestRuntime_OpenEnqueuesFullJob(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 3, 64, 48)
	r := h.start(t)
	id := h.add(t, r, root)
	waitReady(t, r, id)
	walks := h.em.discoverCount(id)

	if _, err := r.Catalog.Open(id); err != nil {
		t.Fatalf("Catalog.Open: %v", err)
	}
	waitFor(t, "the rescan to finish", func() bool { return h.em.countState(id, "ready") >= 2 })
	waitReady(t, r, id)
	if n := h.em.discoverCount(id); n <= walks {
		t.Fatalf("discover progress after Open = %d (was %d); want a full job, which walks the folder", n, walks)
	}
}

func TestRuntime_QueuedRescanSurvivesRestart(t *testing.T) {
	h := newHarness(t)
	b := h.folder(t, "B", 2, 64, 48)
	a := h.folder(t, "A", 2, 64, 48)
	r := h.start(t)
	idB := h.add(t, r, b)
	waitReady(t, r, idB)

	release := h.stills.holdUnder(a)
	defer release()
	idA := h.add(t, r, a)
	receive(t, "a native call on A", h.stills.held)
	if err := r.Library.Rescan(idB); err != nil {
		t.Fatalf("Rescan(B): %v", err)
	}
	// The held call is abandoned when the context is cancelled; letting it
	// return then closes A's session without waiting for NativeQuitWait.
	r.hook = func(step string) {
		if step == stepCancelled {
			release()
		}
	}
	r.Shutdown()
	waitFor(t, "every folder access released", func() bool { return len(h.bm.Active()) == 0 })

	h.em = newRecEmitter(h.log)
	r2 := h.start(t)
	waitReady(t, r2, idA)
	waitReady(t, r2, idB)
	if got := h.em.scanning(); !slices.Equal(got, []string{idA, idB}) {
		t.Fatalf("jobs run after the restart = %v; want [A B] = [%s %s]", got, idA, idB)
	}
}

func TestRuntime_ShutdownOrder(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 3, 64, 48)
	release := h.emb.holdCalls()
	defer release()
	r := h.start(t)
	id := h.add(t, r, root)
	receive(t, "an inference in flight", h.emb.entered)
	if _, err := r.Catalog.Open(id); err != nil {
		t.Fatalf("Catalog.Open: %v", err)
	}

	var activeAtSessions []string
	r.hook = func(step string) {
		h.log.add(step)
		switch step {
		case stepSilenced:
			// The inference in flight is awaited by queue.Stop; let it end.
			release()
		case stepSessionsClosed:
			activeAtSessions = h.bm.Active()
		}
	}
	shutdownWithin(t, r, QuitDeadline+time.Second)

	log := h.log.snapshot()
	at := func(s string) int {
		i := slices.Index(log, s)
		if i < 0 {
			t.Fatalf("%q missing from %v", s, log)
		}
		return i
	}
	order := []string{stepGateClosed, stepCancelled, stepEmitterClosedFake, stepSilenced, stepQueueStopped,
		stepCatalogClosed, stepModelsStopped, stepSessionsClosed, stepInferenceClosed, stepLibraryClosed}
	for i := 1; i < len(order); i++ {
		if at(order[i-1]) >= at(order[i]) {
			t.Fatalf("%q came after %q: %v", order[i-1], order[i], log)
		}
	}
	if len(activeAtSessions) != 0 {
		t.Fatalf("bookmark access when the sessions were closed: %v; want none", activeAtSessions)
	}
	// Every native call and inference ended before the queue stopped, so no
	// store or ONNX session was used after it was closed.
	queue := at(stepQueueStopped)
	for i, e := range log {
		if (e == "stills" || strings.HasPrefix(e, "embed")) && i > queue {
			t.Fatalf("%q at %d, after the queue stopped (%d): %v", e, i, queue, log)
		}
	}
	if n := h.em.emitsAfterClose(); n != 0 {
		t.Fatalf("%d events emitted after the emitter was closed", n)
	}
}

// A native call that never returns: Shutdown still returns in time, and
// what that call uses — its folder's store and bookmark — stays open.
func TestRuntime_ShutdownWithNativeCallInFlight(t *testing.T) {
	h := newHarness(t)
	b := h.folder(t, "Open", 2, 64, 48)
	a := h.folder(t, "Stuck", 2, 64, 48)
	r := h.start(t)
	idB := h.add(t, r, b)
	waitReady(t, r, idB)
	if _, err := r.Catalog.Open(idB); err != nil {
		t.Fatalf("Catalog.Open: %v", err)
	}
	waitFor(t, "the open folder's rescan", func() bool { return h.em.countState(idB, "ready") >= 2 })
	waitReady(t, r, idB)

	release := h.stills.holdUnder(a)
	idA := h.add(t, r, a)
	receive(t, "a native call on the stuck folder", h.stills.held)
	t.Cleanup(func() {
		release()
		waitFor(t, "the stuck folder's access released", func() bool { return len(h.bm.Active()) == 0 })
	})

	sessA, relA, ok := r.sessions.AcquireOpen(idA)
	if !ok {
		t.Fatal("the stuck folder has no open session")
	}
	relA()
	sessB, relB, ok := r.sessions.AcquireOpen(idB)
	if !ok {
		t.Fatal("the open folder has no open session")
	}
	relB()

	shutdownWithin(t, r, QuitDeadline+time.Second)

	if err := sessA.Store.DB().PingContext(context.Background()); err != nil {
		t.Fatalf("the stuck folder's store was closed: %v", err)
	}
	if active := h.bm.Active(); !slices.Equal(active, []string{a}) {
		t.Fatalf("active bookmark access = %v; want only the stuck folder %s", active, a)
	}
	if err := sessB.Store.DB().PingContext(context.Background()); err == nil {
		t.Fatal("the open folder's store is still open; want it closed")
	}
}

// Two folders scan at once: a folder whose job is stuck in a native call
// does not keep the next folder in the Library waiting.
func TestRuntime_SecondFolderScansWhileTheFirstIsBusy(t *testing.T) {
	h := newHarness(t)
	a := h.folder(t, "Busy", 2, 64, 48)
	b := h.folder(t, "Next", 3, 64, 48)
	r := h.start(t)

	release := h.stills.holdUnder(a)
	t.Cleanup(release)
	idA := h.add(t, r, a)
	receive(t, "a native call on the busy folder", h.stills.held)
	idB := h.add(t, r, b)

	waitFor(t, "the second folder ready while the first still scans", func() bool {
		return summary(t, r, idB).State == "ready"
	})
	if s := summary(t, r, idA); s.State != "scanning" {
		t.Fatalf("the busy folder's state = %q; want scanning", s.State)
	}
	release()
	if s := waitReady(t, r, idA); s.Photos != 2 {
		t.Fatalf("the busy folder's photos = %d; want 2", s.Photos)
	}
}

// Quit with two jobs running, both stuck in native calls: Shutdown still
// returns within the deadline, and what those calls use stays open.
func TestRuntime_ShutdownWithTwoJobsRunning(t *testing.T) {
	h := newHarness(t)
	a := h.folder(t, "One", 2, 64, 48)
	b := h.folder(t, "Two", 2, 64, 48)
	r := h.start(t)

	release := h.stills.holdUnder(h.parent)
	t.Cleanup(func() {
		release()
		waitFor(t, "the folders' access released", func() bool { return len(h.bm.Active()) == 0 })
	})
	idA := h.add(t, r, a)
	idB := h.add(t, r, b)
	waitFor(t, "both folders scanning", func() bool {
		return summary(t, r, idA).State == "scanning" && summary(t, r, idB).State == "scanning"
	})
	for heldA, heldB := false, false; !heldA || !heldB; {
		p := receive(t, "a native call of each job", h.stills.held)
		heldA = heldA || under(p, a)
		heldB = heldB || under(p, b)
	}

	shutdownWithin(t, r, QuitDeadline+time.Second)

	active := h.bm.Active()
	slices.Sort(active)
	want := []string{a, b}
	slices.Sort(want)
	if !slices.Equal(active, want) {
		t.Fatalf("active bookmark access = %v; want both stuck folders %v", active, want)
	}
}

func TestRuntime_ShutdownWithBindingStuckInDialog(t *testing.T) {
	h := newHarness(t)
	h.confirm = &fakeConfirmer{asked: make(chan string, 1), answers: make(chan bool)}
	root := h.folder(t, "Trip", 2, 64, 48)
	r := h.start(t)
	id := h.add(t, r, root)
	waitReady(t, r, id)

	type result struct {
		removed bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		removed, err := r.Library.Remove(id)
		done <- result{removed, err}
	}()
	receive(t, "the removal dialog", h.confirm.asked)

	shutdownWithin(t, r, QuitDeadline+time.Second)
	h.confirm.answers <- true
	res := receive(t, "Remove to return", done)
	if res.removed || !isUnavailable(res.err) {
		t.Fatalf("Remove after quit began = %v, %v; want false, unavailable", res.removed, res.err)
	}
	if _, err := openLibrary(t).Folder(id); err != nil {
		t.Fatalf("the folder was removed after quit began: %v", err)
	}
}

// A menu-context write waiting for the main thread while that thread runs
// Shutdown: the binding has left the gate before calling the menu, so the
// drain does not wait for it — library.db is closed and Shutdown returns
// well before the quit deadline.
func TestRuntime_ShutdownDoesNotWaitForMenuContextWrite(t *testing.T) {
	h := newHarness(t)
	menu := &mainThreadMenu{entered: make(chan struct{}, 1), free: make(chan struct{})}
	h.menu = menu
	r := h.start(t)
	var (
		stepsMu sync.Mutex
		steps   []string
	)
	r.hook = func(step string) {
		stepsMu.Lock()
		defer stepsMu.Unlock()
		steps = append(steps, step)
	}

	done := make(chan error, 1)
	go func() { done <- r.System.SetMenuContext(system.MenuContext{Screen: "library"}) }()
	receive(t, "the menu context write", menu.entered)

	shutdownWithin(t, r, QuitDeadline/2)
	stepsMu.Lock()
	got := slices.Clone(steps)
	stepsMu.Unlock()
	if !slices.Contains(got, stepLibraryClosed) {
		t.Fatalf("shutdown steps = %v; want %q: the drain waited for the menu write", got, stepLibraryClosed)
	}

	close(menu.free) // the main thread is free again
	if err := receive(t, "SetMenuContext to return", done); err != nil {
		t.Fatalf("SetMenuContext = %v; want nil (admitted before quit began)", err)
	}
}

func TestRuntime_AddDroppedAfterShutdownAddsNothing(t *testing.T) {
	h := newHarness(t)
	first := h.folder(t, "Dropped", 2, 64, 48)
	late := h.folder(t, "Late", 2, 64, 48)
	r := h.start(t)

	res := r.AddDropped([]string{first})
	if len(res) != 1 || res[0].Existing {
		t.Fatalf("AddDropped = %+v; want one new folder", res)
	}
	waitReady(t, r, res[0].ID)

	r.Shutdown()
	if got := r.AddDropped([]string{late}); got != nil {
		t.Fatalf("AddDropped after Shutdown = %+v; want nil", got)
	}
	folders, err := openLibrary(t).Folders()
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 || folders[0].ID != res[0].ID {
		t.Fatalf("folders after Shutdown = %+v; want only the first", folders)
	}
}

func TestRuntime_ShutdownDuringPickKeepsPick(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 2, 64, 48)
	r := h.start(t)
	id := h.add(t, r, root)
	waitReady(t, r, id)
	view, err := r.Catalog.Open(id)
	if err != nil {
		t.Fatalf("Catalog.Open: %v", err)
	}
	fileID := view.Clusters[0].Members[0].FileID

	copying := make(chan struct{}, 1)
	proceed := make(chan struct{})
	restore := picks.SetTraceForTest(func(event string) {
		if event == "copy" {
			copying <- struct{}{}
			<-proceed
		}
	})
	defer restore()

	type result struct {
		res catalog.PickResult
		err error
	}
	picked := make(chan result, 1)
	go func() {
		res, err := r.Catalog.SetPick(id, fileID, "pick")
		picked <- result{res, err}
	}()
	receive(t, "the pick to start copying", copying)

	reached := make(chan struct{})
	var once sync.Once
	r.hook = func(step string) {
		if step == stepQueueStopped {
			once.Do(func() { close(reached) })
		}
	}
	shut := make(chan struct{})
	go func() {
		r.Shutdown()
		close(shut)
	}()
	receive(t, "the drain to stop the queue", reached)
	close(proceed)
	got := receive(t, "the pick to return", picked)
	if got.err != nil || got.res.State != "picked" {
		t.Fatalf("SetPick during Shutdown = %+v, %v; want picked", got.res, got.err)
	}
	receive(t, "Shutdown to return", shut)

	st := openState(t, root)
	p, err := st.Picks().Get(fileID)
	if err != nil || p.State != "picked" || !p.ExportedPath.Valid {
		t.Fatalf("pick row after Shutdown = %+v, %v; want picked with its copy", p, err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p.ExportedPath.String))); err != nil {
		t.Fatalf("the pick's copy is missing: %v", err)
	}
}

func TestRuntime_ShutdownDoesNotBlockTheCallerOnDrain(t *testing.T) {
	h := newHarness(t)
	r := h.start(t)

	var returned atomic.Bool
	caller := make(chan string, 1)
	inDrain := make(chan string, 1)
	proceed := make(chan struct{})
	r.hook = func(step string) {
		if step == stepGateClosed {
			inDrain <- goid()
			<-proceed
		}
	}
	done := make(chan struct{})
	go func() {
		caller <- goid()
		r.Shutdown()
		returned.Store(true)
		close(done)
	}()
	callerID := receive(t, "the caller", caller)
	drainID := receive(t, "the drain", inDrain)
	if drainID == callerID {
		t.Fatalf("the drain runs on the caller's goroutine (%s)", drainID)
	}
	if returned.Load() {
		t.Fatal("Shutdown returned before the drain finished")
	}
	close(proceed)
	receive(t, "Shutdown to return", done)
}

func TestRuntime_BindingsAfterShutdownAreUnavailable(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 2, 64, 48)
	r := h.start(t)
	id := h.add(t, r, root)
	waitReady(t, r, id)
	r.Shutdown()

	if _, err := r.Library.List(); !isUnavailable(err) {
		t.Errorf("Library.List = %v; want unavailable", err)
	}
	if _, err := r.Models.Status(); !isUnavailable(err) {
		t.Errorf("Models.Status = %v; want unavailable", err)
	}
	if _, err := r.Models.Quality(); !isUnavailable(err) {
		t.Errorf("Models.Quality = %v; want unavailable", err)
	}
	if _, err := r.System.Info(); !isUnavailable(err) {
		t.Errorf("System.Info = %v; want unavailable", err)
	}
	if _, err := r.Catalog.Clusters(id); !isUnavailable(err) {
		t.Errorf("Catalog.Clusters = %v; want unavailable", err)
	}
	if _, err := r.Settings.Get(); !isUnavailable(err) {
		t.Errorf("Settings.Get = %v; want unavailable", err)
	}
	rec := httptest.NewRecorder()
	r.Media.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/preview/"+id+"/0", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("media request = %d; want 503", rec.Code)
	}
}

func TestRuntime_NoEmitAfterShutdown(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 3, 64, 48)
	release := h.emb.holdCalls()
	defer release()
	r := h.start(t)
	h.add(t, r, root)
	receive(t, "an inference in flight", h.emb.entered)
	r.hook = func(step string) {
		if step == stepSilenced {
			release() // queue.Stop awaits the inference in flight
		}
	}
	r.Shutdown()

	// Whatever still runs after quit is silenced before the frontend.
	r.coalescer.EmitNow(events.ScanState, "x", events.ScanStateEvent{FolderID: "x", State: "ready"})
	r.coalescer.Flush()
	r.out.Emit(events.LibraryChanged, events.LibraryChangedEvent{Reason: "stats"})
	r.quality.Changed()
	if n := h.em.emitsAfterClose(); n != 0 {
		t.Fatalf("%d events reached the emitter after Shutdown", n)
	}
	select {
	case <-h.em.closed:
	default:
		t.Fatal("Shutdown did not close the emitter")
	}
}

func TestRuntime_ShutdownDuringDownloadLeavesPartFile(t *testing.T) {
	h := newHarness(t)
	served := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Length", "1048576")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 4096))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case served <- struct{}{}:
		default:
		}
		<-req.Context().Done()
	}))
	t.Cleanup(srv.Close)

	inst := local.NewInstaller(appdir.Models())
	m := local.KnownModels[local.ModelCLIP]
	m.URL = srv.URL + "/clip.onnx"
	inst.Models = map[local.ModelKind]local.Model{local.ModelCLIP: m}
	d := h.deps()
	d.Downloader = inst
	r, err := New(d)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.presenceEvery = time.Hour
	t.Cleanup(r.Shutdown)
	r.Start(context.Background())

	part := inst.Path(local.ModelCLIP) + ".part"
	receive(t, "the download to start", served)
	waitFor(t, "bytes in the part file", func() bool {
		fi, err := os.Stat(part)
		return err == nil && fi.Size() > 0
	})
	if r.Idle() {
		t.Fatal("Idle() = true while a model downloads")
	}
	shutdownWithin(t, r, QuitDeadline+time.Second)

	if fi, err := os.Stat(part); err != nil || fi.Size() == 0 {
		t.Fatalf("part file after Shutdown: %v; want it kept for the next launch", err)
	}
	if _, err := os.Stat(inst.Path(local.ModelCLIP)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("model file after an interrupted download: %v; want none", err)
	}
}

func TestRuntime_RestartRestoresLibrary(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Trip", 3, 64, 48)
	r := h.start(t)
	id := h.add(t, r, root)
	before := waitReady(t, r, id)
	r.Shutdown()

	calls := h.stills.callCount()
	h.em = newRecEmitter(h.log)
	r2 := h.start(t)
	after := summary(t, r2, id)
	if after.State != "ready" || after.Photos != 3 || after.Clusters != before.Clusters || len(after.Previews) != 3 {
		t.Fatalf("row after restart = %+v; want it as before (%+v)", after, before)
	}
	if !r2.Idle() {
		t.Fatal("Idle() = false after a restart with nothing pending")
	}
	if n := h.stills.callCount(); n != calls {
		t.Fatalf("Stills calls after the restart = %d; want none", n-calls)
	}
}

func TestRuntime_RemovedFolderMidScan(t *testing.T) {
	h := newHarness(t)
	root := h.folder(t, "Doomed", 3, 64, 48)
	r := h.start(t)
	waitFor(t, "the runtime idle", r.Idle)
	baseline := goruntime.NumGoroutine()

	release := h.stills.holdUnder(root)
	defer release()
	id := h.add(t, r, root)
	receive(t, "a native call on the folder", h.stills.held)

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	h.bm.Delete(root)
	h.vols.SetAbsent(root, "UUID-TEST", true)
	if err := r.Library.CheckNow(); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	if _, err := r.lib.Folder(id); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("Folder after CheckNow = %v; want the row gone", err)
	}
	if !h.em.libraryChanged(id, "gone") {
		t.Fatal(`no library:changed "gone"`)
	}
	release()
	waitFor(t, "the queue idle", r.Idle)
	waitFor(t, "the goroutines back to the baseline", func() bool { return goruntime.NumGoroutine() <= baseline })
	if active := h.bm.Active(); len(active) != 0 {
		t.Fatalf("bookmark access still active: %v", active)
	}
}

func TestRuntime_ShutdownBeforeStart(t *testing.T) {
	h := newHarness(t)
	r := h.newRuntime(t)
	shutdownWithin(t, r, QuitDeadline+time.Second)
	r.Start(context.Background()) // does nothing after Shutdown
	if _, err := r.Library.List(); !isUnavailable(err) {
		t.Fatalf("List after Shutdown = %v; want unavailable", err)
	}
	shutdownWithin(t, r, time.Second) // idempotent
}

func TestNew_MissingDependency(t *testing.T) {
	h := newHarness(t)
	d := h.deps()
	d.Stills = nil
	if r, err := New(d); err == nil {
		r.Shutdown()
		t.Fatal("New without Stills succeeded")
	}
}

// The home directory comes from the password database, not $HOME: inside
// the App Sandbox $HOME is the app's container.
func TestHomeDir_FromPasswordDatabase(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skipf("no password database entry: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	if got := homeDir(""); got != u.HomeDir {
		t.Fatalf("homeDir(\"\") = %q; want %q", got, u.HomeDir)
	}
	if got := homeDir("/Users/someone"); got != "/Users/someone" {
		t.Fatalf("homeDir(given) = %q; want it unchanged", got)
	}
}

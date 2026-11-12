package catalog

import (
	"context"
	"database/sql"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// --- Refresh --------------------------------------------------------------------

func TestRefresh_DebouncedStatsAndPreviews(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "v1")
	catalogTestCopyV1(t, root)
	h.open(t, id)
	st := h.session(t, id).Store
	n := func(i int) int64 { return catalogTestV1ID(t, st, i) }

	// Ten mutations: pick the four unpicked photos, then unpick and pick
	// again three times.
	muts := []struct {
		file   int64
		action string
	}{
		{n(1), "pick"}, {n(3), "pick"}, {n(4), "pick"}, {n(6), "pick"},
		{n(1), "unpick"}, {n(1), "pick"}, {n(3), "unpick"}, {n(3), "pick"}, {n(4), "unpick"}, {n(4), "pick"},
	}
	for _, m := range muts {
		if _, err := h.cat.SetPick(id, m.file, m.action); err != nil {
			t.Fatalf("%s %d: %v", m.action, m.file, err)
		}
	}
	if got := h.statsEvents(id); got != 0 {
		t.Fatalf("stats events before the delay = %d; want 0", got)
	}
	pending := h.timers.pending()
	if len(pending) != 1 || pending[0].d != 500*time.Millisecond {
		t.Fatalf("pending refresh timers = %d; want one of RefreshDelay", len(pending))
	}
	h.timers.fire()
	if got := h.statsEvents(id); got != 1 {
		t.Fatalf("stats events after the delay = %d; want 1", got)
	}
	f, err := h.lib.Folder(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Stats.Picked != 6 || f.Previews != 6 {
		t.Fatalf("library row picked=%d previews=%d; want 6 and 6", f.Stats.Picked, f.Previews)
	}

	// A reject of a sampled photo changes the strip.
	stamp := f.PreviewsStamp
	if _, err := h.cat.SetPick(id, n(2), "reject"); err != nil {
		t.Fatal(err)
	}
	if fired := h.timers.fire(); fired != 1 {
		t.Fatalf("fired %d timers; want 1", fired)
	}
	f, err = h.lib.Folder(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.PreviewsStamp <= stamp || f.Previews != 5 || f.Stats.Rejected != 1 {
		t.Fatalf("after a reject: stamp %d (was %d), previews %d, rejected %d; want a newer stamp, 5, 1",
			f.PreviewsStamp, stamp, f.Previews, f.Stats.Rejected)
	}
	if got := h.statsEvents(id); got != 2 {
		t.Fatalf("stats events after the second refresh = %d; want 2", got)
	}
}

func TestRefresh_FlushedOnClose(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "v1")
	catalogTestCopyV1(t, root)
	h.open(t, id)
	one := catalogTestV1ID(t, h.session(t, id).Store, 1)
	if _, err := h.cat.SetPick(id, one, "reject"); err != nil {
		t.Fatal(err)
	}
	if got := h.statsEvents(id); got != 0 {
		t.Fatalf("stats events before Close = %d; want 0", got)
	}
	h.cat.Close(id)
	if got := h.statsEvents(id); got != 1 {
		t.Fatalf("stats events after Close = %d; want 1", got)
	}
	f, err := h.lib.Folder(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Stats.Rejected != 1 || f.Previews != 5 {
		t.Fatalf("library row rejected=%d previews=%d; want 1 and 5", f.Stats.Rejected, f.Previews)
	}
	if p := h.timers.pending(); len(p) != 0 {
		t.Fatalf("%d refresh timers still pending after Close", len(p))
	}
	// A timer that fires late does nothing.
	h.timers.mu.Lock()
	timers := slices.Clone(h.timers.timers)
	h.timers.mu.Unlock()
	for _, tm := range timers {
		tm.fn()
	}
	if got := h.statsEvents(id); got != 1 {
		t.Fatalf("stats events after a late timer = %d; want still 1", got)
	}
}

func TestRefresh_CloseAllRunsStatsOnly(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "v1")
	catalogTestCopyV1(t, root)
	h.open(t, id)
	one := catalogTestV1ID(t, h.session(t, id).Store, 1)
	if _, err := h.cat.SetPick(id, one, "reject"); err != nil {
		t.Fatal(err)
	}
	before, err := h.lib.Folder(id)
	if err != nil {
		t.Fatal(err)
	}
	h.cat.CloseAll()
	f, err := h.lib.Folder(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Stats.Rejected != 1 {
		t.Fatalf("library row rejected = %d after CloseAll; want 1", f.Stats.Rejected)
	}
	if f.PreviewsStamp != before.PreviewsStamp || f.Previews != before.Previews {
		t.Fatalf("CloseAll refreshed the previews (stamp %d → %d)", before.PreviewsStamp, f.PreviewsStamp)
	}
	if _, err := h.cat.Open(context.Background(), id); catalogTestCode(err) != apperr.CodeUnavailable {
		t.Fatalf("Open after CloseAll = %v; want unavailable", err)
	}
}

// TestRefresh_StaleRerunDropped: a timer that fired while a refresh ran asks
// for one more run, but a mutation after it restarts the debounce — so at
// most one refresh follows the burst, no earlier than the delay after the
// last mutation.
func TestRefresh_StaleRerunDropped(t *testing.T) {
	gate := make(chan struct{})
	stills := &platformtest.Stills{Gate: gate}
	h := newCatalogTestHarness(t, withStills(stills))
	release := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release) // runs before the harness's cleanups
	id, root := h.folder(t, "v1")
	catalogTestCopyV1(t, root)
	h.open(t, id)
	one := catalogTestV1ID(t, h.session(t, id).Store, 1)
	mutate := func(action string) {
		t.Helper()
		if _, err := h.cat.SetPick(id, one, action); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}

	mutate("reject")
	first := make(chan struct{})
	go func() {
		defer close(first)
		h.timers.fire() // the refresh runs on the timer's goroutine
	}()
	deadline := time.Now().Add(catalogTestTimeout)
	for len(stills.Calls()) == 0 { // the refresh is held inside its preview strip
		if time.Now().After(deadline) {
			t.Fatal("the refresh never reached the previews")
		}
		time.Sleep(time.Millisecond)
	}

	mutate("unreject")
	if fired := h.timers.fire(); fired != 1 { // fires while the refresh runs: a rerun is due
		t.Fatalf("fired %d timers; want 1", fired)
	}
	mutate("reject") // restarts the debounce: that rerun is stale

	release()
	select {
	case <-first:
	case <-time.After(catalogTestTimeout):
		t.Fatal("the first refresh never ended")
	}
	if got := h.statsEvents(id); got != 1 {
		t.Fatalf("stats events once the first refresh ended = %d; want 1 (no rerun before the last delay)", got)
	}
	if p := h.timers.pending(); len(p) != 1 {
		t.Fatalf("pending timers = %d; want the last mutation's", len(p))
	}
	h.timers.fire()
	if got := h.statsEvents(id); got != 2 {
		t.Fatalf("stats events after the last delay = %d; want 2", got)
	}
	f, err := h.lib.Folder(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Stats.Rejected != 1 || f.Previews != 5 {
		t.Fatalf("library row rejected=%d previews=%d; want 1 and 5", f.Stats.Rejected, f.Previews)
	}
}

// TestRecluster_SchedulesStatsRefresh: a re-cluster keeps the Library's
// cluster count current through the same debounced path, statistics only.
func TestRecluster_SchedulesStatsRefresh(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	catalogTestSeed(t, root, func(st *store.Store) {
		for _, n := range []string{"A", "B"} {
			f := catalogTestFile(t, st, root, n+".JPG", nil)
			if err := st.Files().UpdateMetadata(store.File{
				ID: f, DeviceKey: sql.NullString{String: "X-T5#A1", Valid: true},
				CapturedAt: sql.NullInt64{Int64: 1714185600, Valid: true},
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.Files().UpdateStatus(f, "analyzed", ""); err != nil {
				t.Fatal(err)
			}
		}
	})
	h.open(t, id)
	if err := h.cat.Recluster(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := h.statsEvents(id); got != 0 {
		t.Fatalf("stats events before the delay = %d; want 0", got)
	}
	if fired := h.timers.fire(); fired != 1 {
		t.Fatalf("fired %d timers after Recluster; want 1", fired)
	}
	f, err := h.lib.Folder(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Stats.Clusters != 1 || f.Stats.Photos != 2 {
		t.Fatalf("library row clusters=%d photos=%d; want 1 and 2", f.Stats.Clusters, f.Stats.Photos)
	}
	if f.PreviewsStamp != 0 {
		t.Fatalf("previews stamp = %d; want untouched (statistics only)", f.PreviewsStamp)
	}
	if got := h.statsEvents(id); got != 1 {
		t.Fatalf("stats events = %d; want 1", got)
	}
}

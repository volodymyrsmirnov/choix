package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

func TestSampleIndices(t *testing.T) {
	tests := []struct {
		n, k int
		want []int
	}{
		{0, 6, []int{}},
		{3, 6, []int{0, 1, 2}},
		{6, 6, []int{0, 1, 2, 3, 4, 5}},
		{11, 6, []int{0, 2, 4, 6, 8, 10}},
		{5000, 6, []int{0, 1000, 2000, 2999, 3999, 4999}},
		{20, 6, []int{0, 4, 8, 11, 15, 19}},
		{7, 6, []int{0, 1, 2, 4, 5, 6}},
		{5, 1, []int{0}},
		{5, 0, []int{}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d_%d", tt.n, tt.k), func(t *testing.T) {
			got := SampleIndices(tt.n, tt.k)
			if got == nil || !slices.Equal(got, tt.want) {
				t.Fatalf("SampleIndices(%d, %d) = %#v, want %v", tt.n, tt.k, got, tt.want)
			}
		})
	}
}

// previewEnv is a Library holding one folder, the folder's state.db and the
// directory its previews go to.
type previewEnv struct {
	h      *harness
	id     string
	root   string
	st     *store.Store
	dir    string
	stills *platformtest.Stills
	base   time.Time
}

func newPreviewEnv(t *testing.T) *previewEnv {
	t.Helper()
	h := newHarness(t)
	root := h.mkdir(t, "Trip")
	res := h.mustAdd(t, root)
	dir, err := h.lib.previewsDir(res.ID)
	if err != nil {
		t.Fatalf("previewsDir: %v", err)
	}
	return &previewEnv{
		h: h, id: res.ID, root: root, st: store.NewTestStore(t), dir: dir,
		stills: &platformtest.Stills{},
		base:   time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC),
	}
}

// Sizes of the cached thumbnails a test file gets. File n's preview is
// 640×(320+10n) and its thumb 256×(128+4n), so a 512-wide preview made from
// the preview tier is 256+8n high and one made from the thumb tier is
// 128+4n high: the height tells which file was used.
func previewTierSize(n int) (int, int) { return 640, 320 + 10*n }
func thumbTierSize(n int) (int, int)   { return 256, 128 + 4*n }

// addFile adds file n, captured n minutes after base, with a cached image
// for each tier.
func (e *previewEnv) addFile(t *testing.T, n int, tiers ...string) int64 {
	t.Helper()
	id := insertFile(t, e.st, fmt.Sprintf("img_%03d.jpg", n), "photo", "analyzed", e.base.Add(time.Duration(n)*time.Minute))
	for _, tier := range tiers {
		w, h := thumbTierSize(n)
		if tier == "preview" {
			w, h = previewTierSize(n)
		}
		rel, err := thumb.WriteCache(e.root, id, tier, platformtest.MakeJPEG(t, w, h))
		if err != nil {
			t.Fatalf("write %s of file %d: %v", tier, n, err)
		}
		if err := e.st.Thumbs().Upsert(store.Thumb{FileID: id, Tier: tier, RelPath: rel, Width: int64(w), Height: int64(h)}); err != nil {
			t.Fatalf("thumb row %s of file %d: %v", tier, n, err)
		}
	}
	return id
}

func (e *previewEnv) refresh(ctx context.Context, stills platform.Stills) error {
	return e.h.lib.RefreshPreviews(ctx, e.id, e.st, e.root, stills, e.dir)
}

func (e *previewEnv) mustRefresh(t *testing.T) {
	t.Helper()
	if err := e.refresh(context.Background(), e.stills); err != nil {
		t.Fatalf("RefreshPreviews: %v", err)
	}
}

func (e *previewEnv) folder(t *testing.T) Folder {
	t.Helper()
	f, err := e.h.lib.Folder(e.id)
	if err != nil {
		t.Fatalf("Folder: %v", err)
	}
	return f
}

var previewName = regexp.MustCompile(`^[0-5]\.jpg$`)

// strip returns the size of every preview file in the directory, in name
// order, failing on a gap in the numbering or any other entry (a leftover
// temporary file).
func (e *previewEnv) strip(t *testing.T) [][2]int {
	t.Helper()
	entries, err := os.ReadDir(e.dir)
	if err != nil {
		t.Fatalf("read previews dir: %v", err)
	}
	var sizes [][2]int
	for i, ent := range entries {
		if !previewName.MatchString(ent.Name()) || ent.Name() != fmt.Sprintf("%d.jpg", i) {
			t.Fatalf("previews dir holds %q at position %d, want only 0.jpg… in order", ent.Name(), i)
		}
		data, err := os.ReadFile(filepath.Join(e.dir, ent.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", ent.Name(), err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s is not a JPEG: %v", ent.Name(), err)
		}
		sizes = append(sizes, [2]int{cfg.Width, cfg.Height})
	}
	return sizes
}

// fromPreview is the size of the strip image made from file n's preview tier.
func fromPreview(n int) [2]int { return [2]int{512, 256 + 8*n} }

// fromThumb is the size of the strip image made from file n's thumb tier.
func fromThumb(n int) [2]int { return [2]int{256, 128 + 4*n} }

func previewsOf(ns ...int) [][2]int {
	out := make([][2]int, len(ns))
	for i, n := range ns {
		out[i] = fromPreview(n)
	}
	return out
}

func TestRefreshPreviews_WritesSix(t *testing.T) {
	e := newPreviewEnv(t)
	// Inserted newest first: the strip follows capture time, not row order.
	for n := 19; n >= 0; n-- {
		e.addFile(t, n, "thumb", "preview")
	}
	e.mustRefresh(t)

	if got, want := e.strip(t), previewsOf(0, 4, 8, 11, 15, 19); !slices.Equal(got, want) {
		t.Fatalf("strip = %v, want %v", got, want)
	}
	f := e.folder(t)
	if f.Previews != 6 || f.PreviewsStamp != testNow.Unix() {
		t.Fatalf("previews = %d stamp %d, want 6 stamp %d", f.Previews, f.PreviewsStamp, testNow.Unix())
	}
	for _, c := range e.stills.Calls() {
		if c.Method != "Thumbnail" || c.Path != "" || c.Limit != 512 || c.Fit != platform.FitWidth {
			t.Fatalf("stills call %+v, want Thumbnail of Data, 512, FitWidth", c)
		}
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "stats"}) {
		t.Fatalf("events = %v, want [added stats]", got)
	}
}

func TestRefreshPreviews_FewerThanSix(t *testing.T) {
	e := newPreviewEnv(t)
	e.addFile(t, 0, "thumb")
	e.addFile(t, 1, "thumb")
	stale := filepath.Join(e.dir, "4.jpg")
	if err := os.WriteFile(stale, platformtest.MakeJPEG(t, 8, 8), 0o600); err != nil {
		t.Fatal(err)
	}
	e.mustRefresh(t)

	// Without a preview tier, the thumb tier is used (and never upscaled).
	if got, want := e.strip(t), [][2]int{fromThumb(0), fromThumb(1)}; !slices.Equal(got, want) {
		t.Fatalf("strip = %v, want %v", got, want)
	}
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stale 4.jpg: %v, want it removed", err)
	}
	if f := e.folder(t); f.Previews != 2 {
		t.Fatalf("previews = %d, want 2", f.Previews)
	}
}

func TestRefreshPreviews_SkipsRejected(t *testing.T) {
	e := newPreviewEnv(t)
	ids := make([]int64, 7)
	for n := range ids {
		ids[n] = e.addFile(t, n, "thumb", "preview")
	}
	e.mustRefresh(t)
	if got, want := e.strip(t), previewsOf(0, 1, 2, 4, 5, 6); !slices.Equal(got, want) {
		t.Fatalf("first strip = %v, want %v", got, want)
	}
	before := e.folder(t).PreviewsStamp

	setPick(t, e.st, ids[1], "rejected")
	e.mustRefresh(t)
	if got, want := e.strip(t), previewsOf(0, 2, 3, 4, 5, 6); !slices.Equal(got, want) {
		t.Fatalf("strip after a reject = %v, want %v", got, want)
	}
	// The clock has not moved, yet the stamp grows: it busts the cache of
	// the page showing the strip.
	if f := e.folder(t); f.Previews != 6 || f.PreviewsStamp <= before {
		t.Fatalf("previews = %d stamp %d, want 6 and a stamp above %d", f.Previews, f.PreviewsStamp, before)
	}

	later := testNow.Add(time.Hour)
	e.h.lib.now = func() time.Time { return later }
	e.mustRefresh(t)
	if f := e.folder(t); f.PreviewsStamp != later.Unix() {
		t.Fatalf("stamp = %d, want the clock's %d", f.PreviewsStamp, later.Unix())
	}
}

func TestRefreshPreviews_EmptyFolder(t *testing.T) {
	e := newPreviewEnv(t)
	stale := filepath.Join(e.dir, "0.jpg")
	if err := os.WriteFile(stale, platformtest.MakeJPEG(t, 8, 8), 0o600); err != nil {
		t.Fatal(err)
	}
	e.mustRefresh(t)
	if got := e.strip(t); len(got) != 0 {
		t.Fatalf("strip = %v, want none", got)
	}
	if f := e.folder(t); f.Previews != 0 || f.PreviewsStamp != testNow.Unix() {
		t.Fatalf("previews = %d stamp %d, want 0 stamp %d", f.Previews, f.PreviewsStamp, testNow.Unix())
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "stats"}) {
		t.Fatalf("events = %v, want [added stats]", got)
	}
}

// snapshot returns the content of every file in dir by name.
func snapshot(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, ent := range entries {
		data, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", ent.Name(), err)
		}
		out[ent.Name()] = data
	}
	return out
}

// waitForCall waits until s has received a call.
func waitForCall(t *testing.T, s *platformtest.Stills) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for len(s.Calls()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no call reached the stills fake")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRefreshPreviews_CancelledContext(t *testing.T) {
	e := newPreviewEnv(t)
	ids := make([]int64, 7)
	for n := range ids {
		ids[n] = e.addFile(t, n, "thumb", "preview")
	}
	e.mustRefresh(t)
	want := snapshot(t, e.dir)
	wantRow := e.folder(t)
	setPick(t, e.st, ids[1], "rejected") // the next strip would differ

	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := e.refresh(ctx, e.stills); !errors.Is(err, context.Canceled) {
			t.Fatalf("RefreshPreviews = %v, want context.Canceled", err)
		}
	})

	t.Run("cancelled while scaling", func(t *testing.T) {
		gate := make(chan struct{})
		defer close(gate) // lets the abandoned native call return
		gated := &platformtest.Stills{Gate: gate}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- e.refresh(ctx, gated) }()
		waitForCall(t, gated)
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("RefreshPreviews = %v, want context.Canceled", err)
			}
		case <-time.After(2 * time.Second): // ~1 s rule plus scheduling tolerance
			t.Fatal("RefreshPreviews did not return within 2 s of cancellation")
		}
	})

	got := snapshot(t, e.dir)
	if len(got) != len(want) {
		t.Fatalf("previews dir holds %d files, want the previous %d", len(got), len(want))
	}
	for name, data := range want {
		if !bytes.Equal(got[name], data) {
			t.Fatalf("%s changed by a cancelled refresh", name)
		}
	}
	if f := e.folder(t); f.Previews != wantRow.Previews || f.PreviewsStamp != wantRow.PreviewsStamp {
		t.Fatalf("row = %d stamp %d, want the previous %d stamp %d",
			f.Previews, f.PreviewsStamp, wantRow.Previews, wantRow.PreviewsStamp)
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "stats"}) {
		t.Fatalf("events = %v, want only the first refresh's", got)
	}
}

// An older refresh still scaling when a file is rejected and a newer refresh
// starts must not publish after the newer one: refreshes of one folder run
// one at a time, so the rejected image is not in the final strip.
func TestRefreshPreviews_OverlappingRefreshes(t *testing.T) {
	e := newPreviewEnv(t)
	ids := make([]int64, 7)
	for n := range ids {
		ids[n] = e.addFile(t, n, "thumb", "preview")
	}

	gate := make(chan struct{})
	gated := &platformtest.Stills{Gate: gate}
	older := make(chan error, 1)
	go func() { older <- e.refresh(context.Background(), gated) }()
	waitForCall(t, gated) // the older refresh has read its candidates

	setPick(t, e.st, ids[1], "rejected")
	newerStills := &platformtest.Stills{}
	newer := make(chan error, 1)
	go func() { newer <- e.refresh(context.Background(), newerStills) }()
	waitForCount(t, &e.h.lib.previewLocks, e.id, 2) // the newer one waits for the older

	close(gate)
	if err := <-older; err != nil {
		t.Fatalf("older RefreshPreviews: %v", err)
	}
	if err := <-newer; err != nil {
		t.Fatalf("newer RefreshPreviews: %v", err)
	}
	if got, want := e.strip(t), previewsOf(0, 2, 3, 4, 5, 6); !slices.Equal(got, want) {
		t.Fatalf("final strip = %v, want %v (without the rejected file 1)", got, want)
	}
	if f := e.folder(t); f.Previews != 6 || f.PreviewsStamp != testNow.Unix()+1 {
		t.Fatalf("previews = %d stamp %d, want 6 stamp %d (published second)", f.Previews, f.PreviewsStamp, testNow.Unix()+1)
	}
}

// A refresh waiting for another refresh of the same folder returns as soon
// as its context is cancelled, without refreshing; other folders are not
// held up.
func TestRefreshPreviews_CancelledWhileWaiting(t *testing.T) {
	e := newPreviewEnv(t)
	e.addFile(t, 0, "thumb", "preview")
	gate := make(chan struct{})
	defer close(gate)
	gated := &platformtest.Stills{Gate: gate}
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	go func() { _ = e.refresh(ctxA, gated) }()
	waitForCall(t, gated)

	waiter := &platformtest.Stills{}
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- e.refresh(ctx, waiter) }()
	waitForCount(t, &e.h.lib.previewLocks, e.id, 2)
	cancel()
	select {
	case err := <-res:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting RefreshPreviews = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a waiting RefreshPreviews did not return within 2 s of cancellation")
	}
	if calls := waiter.Calls(); len(calls) != 0 {
		t.Fatalf("cancelled waiter made %d stills calls, want none", len(calls))
	}

	// Another folder refreshes while the first is still held.
	other := e.h.mkdir(t, "Other")
	res2 := e.h.mustAdd(t, other)
	dir2, err := e.h.lib.previewsDir(res2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.h.lib.RefreshPreviews(context.Background(), res2.ID, store.NewTestStore(t), other, waiter, dir2); err != nil {
		t.Fatalf("RefreshPreviews of another folder: %v", err)
	}
}

// A refresh still scaling when its folder is removed drops its result: the
// row is not resurrected and the previews directory is not recreated.
func TestRefreshPreviews_FolderRemovedMeanwhile(t *testing.T) {
	e := newPreviewEnv(t)
	for n := range 3 {
		e.addFile(t, n, "thumb", "preview")
	}
	gate := make(chan struct{})
	gated := &platformtest.Stills{Gate: gate}
	done := make(chan error, 1)
	go func() { done <- e.refresh(context.Background(), gated) }()
	waitForCall(t, gated)

	if err := e.h.lib.Remove(e.id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(e.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("previews dir after Remove: %v, want it gone", err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("RefreshPreviews of a removed folder = %v, want nil", err)
	}

	if _, err := os.Stat(e.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("previews dir after the refresh: %v, want it not recreated", err)
	}
	if fs := e.h.mustFolders(t); len(fs) != 0 {
		t.Fatalf("folders = %d, want 0", len(fs))
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "removed"}) {
		t.Fatalf("events = %v, want [added removed]", got)
	}
}

// The folder's row is gone by the time the set would be published, but the
// directory is still there (as between Remove's two steps): the temporary
// files are discarded and nothing is published.
func TestRefreshPreviews_RowRemovedBeforePublish(t *testing.T) {
	e := newPreviewEnv(t)
	for n := range 3 {
		e.addFile(t, n, "thumb", "preview")
	}
	gate := make(chan struct{})
	gated := &platformtest.Stills{Gate: gate}
	done := make(chan error, 1)
	go func() { done <- e.refresh(context.Background(), gated) }()
	waitForCall(t, gated)

	e.h.lib.previewsDir = nil // Remove leaves the directory in place
	if err := e.h.lib.Remove(e.id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("RefreshPreviews of a removed folder = %v, want nil", err)
	}
	if got := snapshot(t, e.dir); len(got) != 0 {
		t.Fatalf("previews dir holds %d files, want none (temporaries discarded)", len(got))
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "removed"}) {
		t.Fatalf("events = %v, want [added removed]", got)
	}
}

func TestRefreshPreviews_RemovedBeforeStart(t *testing.T) {
	e := newPreviewEnv(t)
	e.addFile(t, 0, "thumb")
	if err := e.h.lib.Remove(e.id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	e.mustRefresh(t)
	if _, err := os.Stat(e.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("previews dir: %v, want it not recreated", err)
	}
	if calls := e.stills.Calls(); len(calls) != 0 {
		t.Fatalf("stills calls = %v, want none for a removed folder", calls)
	}
}

// A source that cannot be read safely is left out of the strip: a thumbs
// row pointing outside .choix/thumbs, a symlinked cache file, a missing one
// and one that is not a JPEG.
func TestRefreshPreviews_UnusableSourcesAreSkipped(t *testing.T) {
	e := newPreviewEnv(t)
	e.addFile(t, 0, "thumb", "preview")

	outside := filepath.Join(e.root, "outside.jpg")
	if err := os.WriteFile(outside, platformtest.MakeJPEG(t, 64, 64), 0o600); err != nil {
		t.Fatal(err)
	}
	row := func(n int, rel string) {
		t.Helper()
		id := insertFile(t, e.st, fmt.Sprintf("bad_%d.jpg", n), "photo", "analyzed", e.base.Add(time.Duration(n)*time.Minute))
		if err := e.st.Thumbs().Upsert(store.Thumb{FileID: id, Tier: "thumb", RelPath: rel, Width: 64, Height: 64}); err != nil {
			t.Fatal(err)
		}
	}
	row(1, "outside.jpg")
	row(2, ".choix/thumbs/../../outside.jpg")
	row(3, ".choix/thumbs/00/missing-thumb.jpg")
	if err := os.Symlink(outside, filepath.Join(e.root, ".choix", "thumbs", "link-thumb.jpg")); err != nil {
		t.Fatal(err)
	}
	row(4, ".choix/thumbs/link-thumb.jpg")
	if err := os.WriteFile(filepath.Join(e.root, ".choix", "thumbs", "junk-thumb.jpg"), []byte("not a jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	row(5, ".choix/thumbs/junk-thumb.jpg")

	e.mustRefresh(t)
	if got, want := e.strip(t), previewsOf(0); !slices.Equal(got, want) {
		t.Fatalf("strip = %v, want only file 0's %v", got, want)
	}
	if f := e.folder(t); f.Previews != 1 {
		t.Fatalf("previews = %d, want 1", f.Previews)
	}
	// Only the file inside .choix/thumbs that is not a JPEG reached the
	// decoder (which refused it); the others were never read.
	if calls := e.stills.Calls(); len(calls) != 2 {
		t.Fatalf("stills calls = %d, want 2 (file 0 and the junk file)", len(calls))
	}
}

func TestRefreshPreviews_RefusesUnexpectedDir(t *testing.T) {
	e := newPreviewEnv(t)
	e.addFile(t, 0, "thumb")
	inside := filepath.Join(e.root, e.id)
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"relative":             e.id,
		"not named by the id":  filepath.Dir(e.dir),
		"inside the folder":    inside,
		"the folder's own dir": e.root,
	} {
		t.Run(name, func(t *testing.T) {
			err := e.h.lib.RefreshPreviews(context.Background(), e.id, e.st, e.root, e.stills, dir)
			if err == nil {
				t.Fatalf("RefreshPreviews into %s = nil, want an error", dir)
			}
		})
	}
	if entries, err := os.ReadDir(inside); err != nil || len(entries) != 0 {
		t.Fatalf("folder-side dir holds %v (%v), want nothing", entries, err)
	}
	if f := e.folder(t); f.Previews != 0 || f.PreviewsStamp != 0 {
		t.Fatalf("row = %d stamp %d, want untouched", f.Previews, f.PreviewsStamp)
	}
}

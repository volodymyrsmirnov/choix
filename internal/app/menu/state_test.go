package menu

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/system"
)

// recEmitter records the events emitted.
type recEmitter struct {
	mu     sync.Mutex
	events []string
	data   []any
}

func (e *recEmitter) Emit(name string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, name)
	e.data = append(e.data, data)
}

func (e *recEmitter) all() ([]string, []any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.events), slices.Clone(e.data)
}

// shown is one context menu the state showed.
type shown struct {
	name string
	x, y int
}

// fixture is a State over recording factories.
type fixture struct {
	s    *State
	bar  *recorder
	emit *recEmitter

	mu      sync.Mutex
	menus   map[string]*recorder // the pooled context menus, by name
	built   int                  // context menus made through the factory
	shows   []shown
	onShow  func(name string, x, y int) // optional, runs inside show
	showing sync.Mutex                  // guards onShow
}

func newFixture(t *testing.T, singleKeys bool) *fixture {
	t.Helper()
	f := &fixture{bar: newRecorder(), emit: &recEmitter{}, menus: map[string]*recorder{}}
	f.s = newState(realManifest(t), f.bar,
		func(name string) Factory {
			r := newRecorder()
			f.mu.Lock()
			defer f.mu.Unlock()
			if _, dup := f.menus[name]; dup {
				t.Errorf("context menu %q made twice", name)
			}
			f.menus[name] = r
			f.built++
			return r
		},
		func(name string, x, y int) {
			f.mu.Lock()
			f.shows = append(f.shows, shown{name, x, y})
			f.mu.Unlock()
			f.showing.Lock()
			hook := f.onShow
			f.showing.Unlock()
			if hook != nil {
				hook(name, x, y)
			}
		},
		f.emit, singleKeys)
	return f
}

// menu returns the pooled context menu called name.
func (f *fixture) menu(t *testing.T, name string) *recorder {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.menus[name]
	if !ok {
		t.Fatalf("no context menu %q", name)
	}
	return r
}

// popup opens the folder's context menu and returns the pooled menu shown.
func (f *fixture) popup(t *testing.T, folderID string, flags []string, x, y int) (string, *recorder) {
	t.Helper()
	f.mu.Lock()
	n := len(f.shows)
	f.mu.Unlock()
	f.s.PopupFolderMenu(folderID, flags, x, y)
	f.mu.Lock()
	if len(f.shows) != n+1 {
		f.mu.Unlock()
		t.Fatalf("PopupFolderMenu(%s) showed %d menus; want 1", folderID, len(f.shows)-n)
	}
	sh := f.shows[n]
	f.mu.Unlock()
	if sh.x != x || sh.y != y {
		t.Errorf("PopupFolderMenu(%s) showed at %d,%d; want %d,%d", folderID, sh.x, sh.y, x, y)
	}
	return sh.name, f.menu(t, sh.name)
}

// folderOf clicks Remove (in every variant) in the pooled menu r and returns
// the folder the click acts on.
func (f *fixture) folderOf(t *testing.T, r *recorder) string {
	t.Helper()
	_, byLabel := contextIDs(t, r)
	before, _ := f.emit.all()
	byLabel["Remove from Library…"].onClick()
	_, data := f.emit.all()
	if len(data) != len(before)+1 {
		t.Fatalf("a click emitted %d events; want 1", len(data)-len(before))
	}
	return data[len(data)-1].(events.MenuActionEvent).Data
}

// TestSetContext_TogglesHandles: every custom item of the menu bar is
// enabled exactly when Enabled says so for the context, and hidden when
// Hidden says so for its flags; before the first context it shows the
// Library with nothing selected.
func TestSetContext_TogglesHandles(t *testing.T) {
	f := newFixture(t, false)
	m := realManifest(t)
	built := itemsOf(t, f.bar, m)

	check := func(c system.MenuContext) {
		t.Helper()
		var on, off int
		for _, menu := range m.Menus {
			for _, it := range menu.Items {
				if it.ID == "" {
					continue
				}
				enabled, hidden := built[it.ID].handle.state()
				if want := Enabled(it, c); enabled != want {
					t.Errorf("context %+v: %s enabled = %v; want %v", c, it.ID, enabled, want)
				}
				if want := Hidden(it, c.Flags); hidden != want {
					t.Errorf("context %+v: %s hidden = %v; want %v", c, it.ID, hidden, want)
				}
				if enabled {
					on++
				} else {
					off++
				}
			}
		}
		if on == 0 || off == 0 {
			t.Errorf("context %+v: %d enabled, %d disabled; want both", c, on, off)
		}
	}

	check(initialContext)
	if enabled, _ := built["add-folder"].handle.state(); !enabled {
		t.Error("add-folder is disabled before the first context; want enabled on the Library")
	}
	if enabled, _ := built["pick"].handle.state(); enabled {
		t.Error("pick is enabled before the first context; want disabled on the Library")
	}

	for _, c := range []system.MenuContext{
		{Screen: "focus", HasSelection: true},
		{Screen: "library", HasSelection: true},
		{Screen: "library"},
		{Screen: "folder", HasSelection: true},
		{Screen: "settings", Flags: []string{"offline"}},
	} {
		f.s.SetContext(c)
		check(c)
	}

	// Spot checks against the manifest's intent.
	f.s.SetContext(system.MenuContext{Screen: "focus", HasSelection: true})
	if enabled, _ := built["pick"].handle.state(); !enabled {
		t.Error("pick is disabled on Focus")
	}
	f.s.SetContext(system.MenuContext{Screen: "library"})
	if enabled, _ := built["remove-folder"].handle.state(); enabled {
		t.Error("remove-folder is enabled on the Library with no selection")
	}
}

// TestSetContext_Concurrent: contexts set from several goroutines leave the
// items consistent with the last one (run under -race).
func TestSetContext_Concurrent(t *testing.T) {
	f := newFixture(t, false)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.s.SetContext(system.MenuContext{Screen: []string{"library", "folder", "focus", "compare"}[i%4], HasSelection: i%2 == 0})
		}()
	}
	wg.Wait()
	last := system.MenuContext{Screen: "compare"}
	f.s.SetContext(last)
	m := realManifest(t)
	built := itemsOf(t, f.bar, m)
	for _, menu := range m.Menus {
		for _, it := range menu.Items {
			if it.ID == "" {
				continue
			}
			if enabled, _ := built[it.ID].handle.state(); enabled != Enabled(it, last) {
				t.Errorf("%s enabled = %v after the last context; want %v", it.ID, enabled, Enabled(it, last))
			}
		}
	}
}

// TestClick_EmitsMenuAction: a click on a menu-bar item emits one
// menu:action with the item's id and no data.
func TestClick_EmitsMenuAction(t *testing.T) {
	f := newFixture(t, false)
	built := itemsOf(t, f.bar, realManifest(t))
	built["add-folder"].onClick()
	built["pick"].onClick()

	names, data := f.emit.all()
	if !slices.Equal(names, []string{events.MenuAction, events.MenuAction}) {
		t.Fatalf("emitted %q; want two %s", names, events.MenuAction)
	}
	want := []any{events.MenuActionEvent{ID: "add-folder"}, events.MenuActionEvent{ID: "pick"}}
	if !slices.Equal(data, want) {
		t.Errorf("payloads = %#v; want %#v", data, want)
	}
}

// contextIDs returns the recorded context menu as ids, "-" for a separator.
func contextIDs(t *testing.T, r *recorder) (ids []string, byID map[string]*entry) {
	t.Helper()
	byID = map[string]*entry{}
	for _, e := range *r.entries {
		switch e.kind {
		case "separator":
			ids = append(ids, "-")
		case "item":
			ids = append(ids, e.label)
			byID[e.label] = e
			if e.accelerator != "" {
				t.Errorf("context item %q has accelerator %q", e.label, e.accelerator)
			}
			if enabled, hidden := e.handle.state(); !enabled || hidden {
				t.Errorf("context item %q enabled=%v hidden=%v; want enabled and shown", e.label, enabled, hidden)
			}
		default:
			t.Errorf("context menu has a %s entry", e.kind)
		}
	}
	return ids, byID
}

// wantContext is the manifest's folder-row list for flags, as labels and
// "-" for separators.
func wantContext(items []Item, flags []string) []string {
	var want []string
	for _, it := range Visible(items, flags) {
		if it.Separator {
			want = append(want, "-")
		} else {
			want = append(want, it.Label)
		}
	}
	return want
}

// TestContextMenus_PoolEqualsManifest: the pool is built once: for each of
// the three variants, two menus under stable names, every one of them the
// manifest's folder-row list without the items hidden for its flag (and the
// separators they strand), with no accelerators, all enabled.
func TestContextMenus_PoolEqualsManifest(t *testing.T) {
	f := newFixture(t, false)
	items := realManifest(t).Context[FolderRow]
	if f.built != 6 {
		t.Fatalf("built %d context menus; want 3 variants × 2 slots", f.built)
	}
	for _, v := range []struct{ name, flag string }{{"default", ""}, {"offline", "offline"}, {"scanning", "scanning"}} {
		var flags []string
		if v.flag != "" {
			flags = []string{v.flag}
		}
		for i := range 2 {
			name := fmt.Sprintf("%s/%s/%d", FolderRow, v.name, i)
			got, _ := contextIDs(t, f.menu(t, name))
			if want := wantContext(items, flags); !slices.Equal(got, want) {
				t.Errorf("%s = %q; want %q", name, got, want)
			}
		}
	}

	// The approved copy.
	all, _ := contextIDs(t, f.menu(t, FolderRow+"/default/0"))
	if want := []string{"Open", "Open Picks Folder", "Reveal in Finder", "-", "Rescan", "-", "Remove from Library…"}; !slices.Equal(all, want) {
		t.Errorf("default = %q; want %q", all, want)
	}
	offline, _ := contextIDs(t, f.menu(t, FolderRow+"/offline/1"))
	if want := []string{"Remove from Library…"}; !slices.Equal(offline, want) {
		t.Errorf("offline = %q; want %q", offline, want)
	}
}

// TestPopupFolderMenu_PicksVariant: a popup shows a menu of the variant for
// the folder's flags; offline wins over scanning, and an unknown flag gets
// the default.
func TestPopupFolderMenu_PicksVariant(t *testing.T) {
	f := newFixture(t, false)
	for _, tc := range []struct {
		flags   []string
		variant string
	}{
		{nil, "default"},
		{[]string{"offline"}, "offline"},
		{[]string{"scanning"}, "scanning"},
		{[]string{"scanning", "offline"}, "offline"},
		{[]string{"other"}, "default"},
	} {
		name, _ := f.popup(t, "folder-1", tc.flags, 3, 4)
		if !strings.HasPrefix(name, FolderRow+"/"+tc.variant+"/") {
			t.Errorf("flags %v showed %q; want a %s menu", tc.flags, name, tc.variant)
		}
	}
}

// TestPopupFolderMenu: a click in the menu shown emits menu:action with the
// item and the folder.
func TestPopupFolderMenu(t *testing.T) {
	f := newFixture(t, false)
	_, r := f.popup(t, "folder-9", nil, 10, 20)
	_, byLabel := contextIDs(t, r)
	byLabel["Rescan"].onClick()
	_, offline := f.popup(t, "folder-8", []string{"offline"}, 1, 2)
	_, byLabel = contextIDs(t, offline)
	byLabel["Remove from Library…"].onClick()

	_, data := f.emit.all()
	want := []any{
		events.MenuActionEvent{ID: "rescan-folder", Data: "folder-9"},
		events.MenuActionEvent{ID: "remove-folder", Data: "folder-8"},
	}
	if !slices.Equal(data, want) {
		t.Errorf("payloads = %#v; want %#v", data, want)
	}
}

// TestPopupFolderMenu_DelayedClickKeepsItsFolder: Wails runs a click's
// callback asynchronously, so it can run after another folder's menu has
// opened. The click still acts on the folder whose menu it was in.
func TestPopupFolderMenu_DelayedClickKeepsItsFolder(t *testing.T) {
	f := newFixture(t, false)
	_, a := f.popup(t, "folder-a", nil, 1, 1)
	_, b := f.popup(t, "folder-b", nil, 2, 2)
	_, aItems := contextIDs(t, a)
	_, bItems := contextIDs(t, b)

	aItems["Open"].onClick() // A's callback runs only now, after B opened
	bItems["Rescan"].onClick()
	aItems["Remove from Library…"].onClick()

	_, data := f.emit.all()
	want := []any{
		events.MenuActionEvent{ID: "open-folder", Data: "folder-a"},
		events.MenuActionEvent{ID: "rescan-folder", Data: "folder-b"},
		events.MenuActionEvent{ID: "remove-folder", Data: "folder-a"},
	}
	if !slices.Equal(data, want) {
		t.Errorf("payloads = %#v; want %#v", data, want)
	}
}

// TestPopupFolderMenu_SlotLimit pins the pool's limit: with two slots per
// variant, the third popup of a variant reuses the first one's menu, so a
// click on it still unprocessed by then would act on the newer folder.
func TestPopupFolderMenu_SlotLimit(t *testing.T) {
	f := newFixture(t, false)
	n1, a := f.popup(t, "folder-a", nil, 0, 0)
	n2, _ := f.popup(t, "folder-b", nil, 0, 0)
	n3, _ := f.popup(t, "folder-c", nil, 0, 0)
	if n1 == n2 || n3 != n1 {
		t.Fatalf("slots %q, %q, %q; want two alternating", n1, n2, n3)
	}
	if got := f.folderOf(t, a); got != "folder-c" {
		t.Errorf("the reused slot acts on %q; want folder-c", got)
	}
}

// TestPopupFolderMenu_BoundedAllocation: popups make no menu beyond the
// pool built at newState.
func TestPopupFolderMenu_BoundedAllocation(t *testing.T) {
	f := newFixture(t, false)
	before := f.built
	names := map[string]bool{}
	for i := range 50 {
		var flags []string
		switch i % 3 {
		case 1:
			flags = []string{"offline"}
		case 2:
			flags = []string{"scanning"}
		}
		name, _ := f.popup(t, fmt.Sprintf("folder-%d", i), flags, i, i)
		names[name] = true
	}
	if f.built != before || before != 6 {
		t.Errorf("built %d context menus before and %d after 50 popups; want 6 and 6", before, f.built)
	}
	if len(names) != 6 {
		t.Errorf("50 popups showed %d distinct menus; want the 6 pooled ones", len(names))
	}
}

// TestPopupFolderMenu_PendingShowKeepsItsFolder: while a popup's show is
// still pending (on the desktop, while its menu is up), later popup
// requests cannot take its slot or change its folder: the first popup
// shows its own folder's menu, and each later one shows its own once the
// first is done.
func TestPopupFolderMenu_PendingShowKeepsItsFolder(t *testing.T) {
	f := newFixture(t, false)
	entered := make(chan string, 1)
	release := make(chan struct{})
	var first sync.Once
	type seen struct {
		x      int
		folder string
	}
	var mu sync.Mutex
	var seenAt []seen
	f.showing.Lock()
	f.onShow = func(name string, x, y int) {
		folder := f.folderOf(t, f.menu(t, name)) // what this show is about to show
		mu.Lock()
		seenAt = append(seenAt, seen{x, folder})
		mu.Unlock()
		first.Do(func() {
			entered <- name
			<-release
		})
	}
	f.showing.Unlock()

	done := make(chan struct{}, 3)
	go func() { f.s.PopupFolderMenu("folder-a", nil, 1, 0); done <- struct{}{} }()
	name := <-entered // A is showing, and stays so until release

	go func() { f.s.PopupFolderMenu("folder-b", nil, 2, 0); done <- struct{}{} }()
	go func() { f.s.PopupFolderMenu("folder-c", nil, 3, 0); done <- struct{}{} }()
	// B and C cannot finish while A's show is pending. The wait only gives
	// a broken implementation the chance to let them through; it does not
	// decide the result of a correct one.
	select {
	case <-done:
		t.Error("a later popup finished while the first one's show was pending")
	case <-time.After(100 * time.Millisecond):
	}
	if got := f.folderOf(t, f.menu(t, name)); got != "folder-a" {
		t.Errorf("while pending, the first popup's menu acts on %q; want folder-a", got)
	}
	f.mu.Lock()
	n := len(f.shows)
	f.mu.Unlock()
	if n != 1 {
		t.Errorf("%d shows while the first was pending; want 1", n)
	}

	close(release)
	for range 3 {
		<-done
	}
	mu.Lock()
	defer mu.Unlock()
	want := map[int]string{1: "folder-a", 2: "folder-b", 3: "folder-c"}
	if len(seenAt) != 3 {
		t.Fatalf("saw %d shows; want 3", len(seenAt))
	}
	for _, sh := range seenAt {
		if sh.folder != want[sh.x] {
			t.Errorf("the show for %s acted on %s", want[sh.x], sh.folder)
		}
	}
}

// TestSingleKeysNative: the state reports the setting it was built with.
func TestSingleKeysNative(t *testing.T) {
	for _, singleKeys := range []bool{false, true} {
		if got := newFixture(t, singleKeys).s.SingleKeysNative(); got != singleKeys {
			t.Errorf("SingleKeysNative() = %v; want %v", got, singleKeys)
		}
	}
}

// TestState_IsMenuState: State is the System binding's MenuState.
func TestState_IsMenuState(t *testing.T) {
	var _ system.MenuState = (*State)(nil)
}

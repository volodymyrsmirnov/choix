package menu

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/app/system"
)

// entry is one recorded menu entry: a submenu, a custom item, a role item
// or a separator.
type entry struct {
	kind        string // submenu | item | role | separator
	label       string // the submenu title, the item's or the role's label
	accelerator string // custom items
	role        string // role items
	children    []*entry
	handle      *recHandle
	onClick     func()
}

// recorder is a Factory that records what is built.
type recorder struct {
	mu      *sync.Mutex
	entries *[]*entry
}

func newRecorder() *recorder {
	return &recorder{mu: &sync.Mutex{}, entries: &[]*entry{}}
}

func (r *recorder) add(e *entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.entries = append(*r.entries, e)
}

func (r *recorder) Submenu(title string) Factory {
	e := &entry{kind: "submenu", label: title}
	r.add(e)
	return &recorder{mu: r.mu, entries: &e.children}
}

func (r *recorder) Item(label, accelerator string, onClick func()) Handle {
	h := &recHandle{enabled: true}
	r.add(&entry{kind: "item", label: label, accelerator: accelerator, handle: h, onClick: onClick})
	return h
}

func (r *recorder) Role(role, label string) {
	r.add(&entry{kind: "role", label: label, role: role})
}

func (r *recorder) Separator() { r.add(&entry{kind: "separator"}) }

// recHandle records the state set on a custom item.
type recHandle struct {
	mu      sync.Mutex
	enabled bool
	hidden  bool
}

func (h *recHandle) SetEnabled(b bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enabled = b
}

func (h *recHandle) SetHidden(b bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hidden = b
}

func (h *recHandle) state() (enabled, hidden bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enabled, h.hidden
}

// fileItem and fileMenu decode the manifest independently of Parse, so the
// equality test compares against the file itself.
type fileItem struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	Accelerator    string   `json:"accelerator"`
	Display        string   `json:"display"`
	Separator      bool     `json:"separator"`
	Role           string   `json:"role"`
	EnabledIn      []string `json:"enabledIn"`
	NeedsSelection bool     `json:"needsSelection"`
}

type fileMenu struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Items []fileItem `json:"items"`
}

func fileManifest(t *testing.T) (menus []fileMenu, context map[string][]fileItem) {
	t.Helper()
	var raw struct {
		Menus   []fileMenu            `json:"menus"`
		Context map[string][]fileItem `json:"context"`
	}
	if err := json.Unmarshal(readManifest(t), &raw); err != nil {
		t.Fatalf("decode the approved manifest: %v", err)
	}
	return raw.Menus, raw.Context
}

// expectedAccelerator is the accelerator the manifest gives a raw item's
// native item: its accelerator or its display's, through the one ownership
// decision.
func expectedAccelerator(t *testing.T, it fileItem, singleKeys bool) string {
	t.Helper()
	return NativeAccelerator(Item{Accelerator: it.Accelerator, Display: it.Display}, singleKeys)
}

// describe is one manifest entry as the equality test compares it.
func describe(e *entry) string {
	switch e.kind {
	case "item":
		return fmt.Sprintf("item(%q, %q)", e.label, e.accelerator)
	case "role":
		return fmt.Sprintf("role(%s, %q)", e.role, e.label)
	default:
		return e.kind
	}
}

func describeRaw(t *testing.T, it fileItem, singleKeys bool) string {
	t.Helper()
	switch {
	case it.Separator:
		return "separator"
	case it.Role != "":
		return fmt.Sprintf("role(%s, %q)", it.Role, it.Label)
	default:
		return fmt.Sprintf("item(%q, %q)", it.Label, expectedAccelerator(t, it, singleKeys))
	}
}

// TestBuild_EqualsManifest: the menu bar built from the manifest is the
// manifest, element by element: menu titles in order; per menu the sequence
// of (label, accelerator) for custom items, (role, label) for role items and
// separators. Accelerators are the manifest's through NativeAccelerator, for
// both settings of singleKeys. Every custom item has a handle under its id.
func TestBuild_EqualsManifest(t *testing.T) {
	menus, _ := fileManifest(t)
	m := realManifest(t)
	for _, singleKeys := range []bool{false, true} {
		t.Run(fmt.Sprintf("singleKeys=%v", singleKeys), func(t *testing.T) {
			r := newRecorder()
			handles := Build(m, r, singleKeys, func(string) {})

			top := *r.entries
			if len(top) != len(menus) {
				t.Fatalf("built %d menus; the manifest has %d", len(top), len(menus))
			}
			var ids []string
			for i, menu := range menus {
				sub := top[i]
				if sub.kind != "submenu" || sub.label != menu.Title {
					t.Fatalf("menu %d = %s %q; want submenu %q", i, sub.kind, sub.label, menu.Title)
				}
				if len(sub.children) != len(menu.Items) {
					t.Fatalf("menu %q has %d entries; the manifest has %d", menu.Title, len(sub.children), len(menu.Items))
				}
				for j, it := range menu.Items {
					got, want := describe(sub.children[j]), describeRaw(t, it, singleKeys)
					if got != want {
						t.Errorf("menu %q entry %d = %s; want %s", menu.Title, j, got, want)
					}
					if it.ID != "" {
						ids = append(ids, it.ID)
						if handles[it.ID] != sub.children[j].handle {
							t.Errorf("handles[%q] is not the handle of the item built for it", it.ID)
						}
					}
				}
			}
			if len(handles) != len(ids) {
				t.Errorf("Build returned %d handles; the manifest has %d custom items", len(handles), len(ids))
			}
		})
	}
}

// itemsOf returns the custom items of the built menu bar, by item id.
func itemsOf(t *testing.T, r *recorder, m Manifest) map[string]*entry {
	t.Helper()
	out := map[string]*entry{}
	for i, menu := range m.Menus {
		sub := (*r.entries)[i]
		for j, it := range menu.Items {
			if it.ID != "" {
				out[it.ID] = sub.children[j]
			}
		}
	}
	return out
}

// TestSingleKeys_OffHasNoAccelerator: with single keys left to the webview
// (spike S2 not passed, design call C31's fallback), an item with only a
// display has no accelerator, and every accelerator built has a ⌘, ⌃ or ⌥.
func TestSingleKeys_OffHasNoAccelerator(t *testing.T) {
	m := realManifest(t)
	r := newRecorder()
	Build(m, r, false, func(string) {})
	built := itemsOf(t, r, m)
	var withAccel int
	for _, menu := range m.Menus {
		for _, it := range menu.Items {
			if it.ID == "" {
				continue
			}
			got := built[it.ID].accelerator
			switch {
			case it.Accelerator == "" && got != "":
				t.Errorf("%s (display %q) has accelerator %q; want none", it.ID, it.Display, got)
			case it.Accelerator != "" && got != it.Accelerator:
				t.Errorf("%s accelerator = %q; want the manifest's %q", it.ID, got, it.Accelerator)
			}
			if got != "" {
				withAccel++
				if !hasChordModifier(got) {
					t.Errorf("%s accelerator %q has no ⌘, ⌃ or ⌥", it.ID, got)
				}
			}
		}
	}
	if withAccel == 0 {
		t.Fatal("no item has an accelerator; the test checked nothing")
	}
	if got := built["pick"].accelerator; got != "" {
		t.Errorf("pick accelerator = %q; want none", got)
	}
}

// TestSingleKeys_OnUsesDisplay: with single keys owned by the menu, an item
// with only a display gets DisplayAccelerator(display).
func TestSingleKeys_OnUsesDisplay(t *testing.T) {
	m := realManifest(t)
	r := newRecorder()
	Build(m, r, true, func(string) {})
	built := itemsOf(t, r, m)
	var displays int
	for _, menu := range m.Menus {
		for _, it := range menu.Items {
			if it.ID == "" || it.Display == "" || it.Accelerator != "" {
				continue
			}
			displays++
			want, ok := DisplayAccelerator(it.Display)
			if !ok {
				t.Fatalf("%s display %q has no accelerator", it.ID, it.Display)
			}
			if got := built[it.ID].accelerator; got != want {
				t.Errorf("%s accelerator = %q; want %q", it.ID, got, want)
			}
		}
	}
	if displays == 0 {
		t.Fatal("no display-only item; the test checked nothing")
	}
	if got := built["pick"].accelerator; got != "Space" {
		t.Errorf("pick accelerator = %q; want Space", got)
	}
}

// TestNativeAccelerator_Table: the one place deciding shortcut ownership.
func TestNativeAccelerator_Table(t *testing.T) {
	for _, tc := range []struct {
		name       string
		it         Item
		singleKeys bool
		want       string
	}{
		{"chord, single keys off", Item{Accelerator: "CmdOrCtrl+O"}, false, "CmdOrCtrl+O"},
		{"chord, single keys on", Item{Accelerator: "CmdOrCtrl+O"}, true, "CmdOrCtrl+O"},
		{"control chord", Item{Accelerator: "Ctrl+Command+F"}, false, "Ctrl+Command+F"},
		{"display, single keys off", Item{Display: "X"}, false, ""},
		{"display, single keys on", Item{Display: "X"}, true, "X"},
		{"shifted display, single keys off", Item{Display: "⇧↩"}, false, ""},
		{"shifted display, single keys on", Item{Display: "⇧↩"}, true, "Shift+Enter"},
		{"modifier-less accelerator, single keys off", Item{Accelerator: "Space"}, false, ""},
		{"modifier-less accelerator, single keys on", Item{Accelerator: "Space"}, true, "Space"},
		{"shift-only accelerator, single keys off", Item{Accelerator: "Shift+X"}, false, ""},
		{"accelerator wins over display", Item{Accelerator: "CmdOrCtrl+R", Display: "R"}, false, "CmdOrCtrl+R"},
		{"neither", Item{}, true, ""},
	} {
		if got := NativeAccelerator(tc.it, tc.singleKeys); got != tc.want {
			t.Errorf("%s: NativeAccelerator(%+v, %v) = %q; want %q", tc.name, tc.it, tc.singleKeys, got, tc.want)
		}
	}
}

// TestEnabled_Table: an item is enabled on the screens it lists (all when it
// lists none), and when it needs a selection, only with one.
func TestEnabled_Table(t *testing.T) {
	for _, tc := range []struct {
		name string
		it   Item
		c    system.MenuContext
		want bool
	}{
		{"listed screen", Item{EnabledIn: []string{"library", "folder"}}, system.MenuContext{Screen: "folder"}, true},
		{"unlisted screen", Item{EnabledIn: []string{"library"}}, system.MenuContext{Screen: "focus"}, false},
		{"empty list is everywhere", Item{}, system.MenuContext{Screen: "settings"}, true},
		{"needs selection, has one", Item{EnabledIn: []string{"library"}, NeedsSelection: true}, system.MenuContext{Screen: "library", HasSelection: true}, true},
		{"needs selection, has none", Item{EnabledIn: []string{"library"}, NeedsSelection: true}, system.MenuContext{Screen: "library"}, false},
		{"needs selection, wrong screen", Item{EnabledIn: []string{"library"}, NeedsSelection: true}, system.MenuContext{Screen: "focus", HasSelection: true}, false},
	} {
		if got := Enabled(tc.it, tc.c); got != tc.want {
			t.Errorf("%s: Enabled = %v; want %v", tc.name, got, tc.want)
		}
	}
}

// TestHidden_Table: an item is hidden when any of its hiddenWhen flags is set.
func TestHidden_Table(t *testing.T) {
	for _, tc := range []struct {
		name  string
		it    Item
		flags []string
		want  bool
	}{
		{"no flags", Item{HiddenWhen: []string{"offline"}}, nil, false},
		{"its flag", Item{HiddenWhen: []string{"offline"}}, []string{"offline"}, true},
		{"one of its flags", Item{HiddenWhen: []string{"offline", "scanning"}}, []string{"scanning"}, true},
		{"another flag", Item{HiddenWhen: []string{"offline"}}, []string{"scanning"}, false},
		{"never hidden", Item{}, []string{"offline", "scanning"}, false},
	} {
		if got := Hidden(tc.it, tc.flags); got != tc.want {
			t.Errorf("%s: Hidden = %v; want %v", tc.name, got, tc.want)
		}
	}
}

// TestVisible_DropsSeparators: hidden items are left out, then the
// separators left leading, trailing or doubled (the approved replica's
// visibleEntries).
func TestVisible_DropsSeparators(t *testing.T) {
	sep := Item{Separator: true}
	a := Item{ID: "a", Label: "A"}
	b := Item{ID: "b", Label: "B", HiddenWhen: []string{"offline"}}
	c := Item{ID: "c", Label: "C", HiddenWhen: []string{"offline", "scanning"}}
	d := Item{ID: "d", Label: "D"}
	items := []Item{b, sep, a, sep, c, sep, d}
	ids := func(items []Item) []string {
		var out []string
		for _, it := range items {
			if it.Separator {
				out = append(out, "-")
			} else {
				out = append(out, it.ID)
			}
		}
		return out
	}
	for _, tc := range []struct {
		flags []string
		want  []string
	}{
		{nil, []string{"b", "-", "a", "-", "c", "-", "d"}},
		{[]string{"offline"}, []string{"a", "-", "d"}},
		{[]string{"scanning"}, []string{"b", "-", "a", "-", "d"}},
	} {
		if got := ids(Visible(items, tc.flags)); !slices.Equal(got, tc.want) {
			t.Errorf("Visible(%v) = %v; want %v", tc.flags, got, tc.want)
		}
	}
	if got := ids(Visible([]Item{sep, b, sep}, []string{"offline"})); len(got) != 0 {
		t.Errorf("Visible(only separators left) = %v; want none", got)
	}
}

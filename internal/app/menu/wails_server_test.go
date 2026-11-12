//go:build server

package menu

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/volodymyrsmirnov/choix/internal/app/system"
)

// In the server build application.New makes no native app, so the real
// factory can build into Wails' menu types and the tests can walk them.
// Wails' role constructors and its accelerator error path dereference the
// global application, so TestMain makes one first.

var testApp *application.App

func TestMain(m *testing.M) {
	testApp = application.New(application.Options{Name: "Choix"})
	os.Exit(m.Run())
}

// normalised returns acc as Wails stores it ("" for none).
func normalised(t *testing.T, acc string) string {
	t.Helper()
	if acc == "" {
		return ""
	}
	return application.NewMenuItem("probe").SetAccelerator(acc).GetAccelerator()
}

// checkMenu compares the Wails menu got with the manifest items, entry by
// entry: labels (role items included, which proves SetLabel), separators,
// and custom accelerators normalised through Wails on both sides.
func checkMenu(t *testing.T, where string, got *application.Menu, items []Item, singleKeys bool) {
	t.Helper()
	for j, it := range items {
		mi := got.ItemAt(j)
		if mi == nil {
			t.Fatalf("%s: entry %d missing; the manifest has %d", where, j, len(items))
		}
		if it.Separator {
			if !mi.IsSeparator() {
				t.Errorf("%s entry %d = %q; want a separator", where, j, mi.Label())
			}
			continue
		}
		if mi.IsSeparator() {
			t.Errorf("%s entry %d is a separator; want %q", where, j, it.Label)
			continue
		}
		if mi.Label() != it.Label {
			t.Errorf("%s entry %d label = %q; want %q", where, j, mi.Label(), it.Label)
		}
		if it.Role != "" {
			if got.FindByRole(wailsRoles[it.Role]) != mi {
				t.Errorf("%s entry %d is not the %s role item", where, j, it.Role)
			}
			continue
		}
		want := normalised(t, NativeAccelerator(it, singleKeys))
		if got := mi.GetAccelerator(); got != want {
			t.Errorf("%s %s accelerator = %q; want %q", where, it.ID, got, want)
		}
	}
	if extra := got.ItemAt(len(items)); extra != nil {
		t.Errorf("%s: extra entry %q after the manifest's %d", where, extra.Label(), len(items))
	}
}

// TestInstalledMenu_EqualsManifest: the menu the real factory builds is the
// manifest: every title, every label (role items included), every
// separator, and every custom accelerator.
func TestInstalledMenu_EqualsManifest(t *testing.T) {
	m := realManifest(t)
	for _, singleKeys := range []bool{false, true} {
		t.Run(fmt.Sprintf("singleKeys=%v", singleKeys), func(t *testing.T) {
			bar := application.NewMenu()
			Build(m, wailsMenu{bar}, singleKeys, func(string) {})
			for i, menu := range m.Menus {
				sub := bar.ItemAt(i)
				if sub == nil || sub.GetSubmenu() == nil {
					t.Fatalf("menu %d (%s) missing or not a submenu", i, menu.Title)
				}
				if sub.Label() != menu.Title {
					t.Errorf("menu %d title = %q; want %q", i, sub.Label(), menu.Title)
				}
				checkMenu(t, menu.Title, sub.GetSubmenu(), menu.Items, singleKeys)
			}
			if extra := bar.ItemAt(len(m.Menus)); extra != nil {
				t.Errorf("extra menu %q", extra.Label())
			}
			checkUniqueKeys(t, bar)
		})
	}
}

// checkUniqueKeys fails when two items of the menu bar, Wails' role items
// included, carry the same key equivalent: AppKit keeps only the first.
func checkUniqueKeys(t *testing.T, bar *application.Menu) {
	t.Helper()
	owner := map[string]string{}
	for i := 0; bar.ItemAt(i) != nil; i++ {
		sub := bar.ItemAt(i).GetSubmenu()
		for j := 0; sub.ItemAt(j) != nil; j++ {
			mi := sub.ItemAt(j)
			acc := mi.GetAccelerator()
			if acc == "" {
				continue
			}
			if other, ok := owner[acc]; ok {
				t.Errorf("%q and %q both use %s", other, mi.Label(), acc)
			}
			owner[acc] = mi.Label()
		}
	}
	// The role items' own shortcuts take part.
	if owner["Cmd+W"] != "Close Window" || owner["Cmd+Q"] != "Quit Choix" {
		t.Errorf("role shortcuts not seen: Cmd+W=%q, Cmd+Q=%q", owner["Cmd+W"], owner["Cmd+Q"])
	}
}

// TestRealManifest_AcceleratorsAreValidForWails: every accelerator in the
// manifest, and every display's accelerator, is one Wails parses.
func TestRealManifest_AcceleratorsAreValidForWails(t *testing.T) {
	m := realManifest(t)
	var n int
	all := slices.Clone(m.Context[FolderRow])
	for _, menu := range m.Menus {
		all = append(all, menu.Items...)
	}
	for _, it := range all {
		for _, acc := range []string{it.Accelerator, displayAcc(it.Display)} {
			if acc == "" {
				continue
			}
			n++
			if normalised(t, acc) == "" {
				t.Errorf("%s: Wails does not parse %q", it.ID, acc)
			}
		}
	}
	if n == 0 {
		t.Fatal("no accelerator checked")
	}
}

func displayAcc(display string) string {
	acc, _ := DisplayAccelerator(display)
	return acc
}

// TestRoles_EveryVocabularyRoleBuilds: every role of the vocabulary makes a
// Wails role item, which takes the manifest's label.
func TestRoles_EveryVocabularyRoleBuilds(t *testing.T) {
	for _, role := range roleVocabulary {
		menu := application.NewMenu()
		wailsMenu{menu}.Role(role, "Label "+role)
		mi := menu.ItemAt(0)
		if mi == nil {
			t.Errorf("role %s built nothing", role)
			continue
		}
		if mi.Label() != "Label "+role {
			t.Errorf("role %s label = %q; want the manifest's", role, mi.Label())
		}
	}
}

// TestInstall_SetsApplicationMenu: Install sets the application menu built
// from the manifest; a popup with no window shows nothing and emits nothing.
func TestInstall_SetsApplicationMenu(t *testing.T) {
	m := realManifest(t)
	emit := &recEmitter{}
	s := Install(testApp, func() *application.WebviewWindow { return nil }, m, emit, false)
	if s.SingleKeysNative() {
		t.Error("SingleKeysNative() = true; want false")
	}
	bar := testApp.Menu.GetApplicationMenu()
	if bar == nil {
		t.Fatal("no application menu")
	}
	for i, menu := range m.Menus {
		checkMenu(t, menu.Title, bar.ItemAt(i).GetSubmenu(), menu.Items, false)
	}
	s.PopupFolderMenu("folder-1", nil, 1, 2)
	if names, _ := emit.all(); len(names) != 0 {
		t.Errorf("emitted %q without a click", names)
	}
}

// expectEnabled is whether the manifest entry it is enabled on screen with
// or without a selection, computed from the file's own fields.
func expectEnabled(it fileItem, screen string, selection bool) bool {
	return (len(it.EnabledIn) == 0 || slices.Contains(it.EnabledIn, screen)) && (!it.NeedsSelection || selection)
}

// TestInstalledMenu_EnabledFollowsContext: for every screen, with and
// without a selection, every custom item of the installed Wails menu bar is
// enabled exactly when the manifest says so — an item enabled where it
// should be disabled fails as much as the reverse. The expectation comes
// from an independent decode of menus.json.
func TestInstalledMenu_EnabledFollowsContext(t *testing.T) {
	menus, _ := fileManifest(t)
	s := Install(testApp, func() *application.WebviewWindow { return nil }, realManifest(t), &recEmitter{}, false)
	bar := testApp.Menu.GetApplicationMenu()

	check := func(screen string, selection bool) (on, off int) {
		t.Helper()
		for i, menu := range menus {
			sub := bar.ItemAt(i).GetSubmenu()
			for j, it := range menu.Items {
				if it.ID == "" {
					continue
				}
				mi := sub.ItemAt(j)
				want := expectEnabled(it, screen, selection)
				if mi.Enabled() != want {
					t.Errorf("%s, selection %v: %s enabled = %v; want %v", screen, selection, it.ID, mi.Enabled(), want)
				}
				if mi.Hidden() {
					t.Errorf("%s, selection %v: %s is hidden", screen, selection, it.ID)
				}
				if want {
					on++
				} else {
					off++
				}
			}
		}
		return on, off
	}

	// Before the frontend reports a screen: the Library, nothing selected.
	check("library", false)

	var on, off int
	for _, screen := range []string{"library", "folder", "focus", "compare", "settings"} {
		for _, selection := range []bool{false, true} {
			s.SetContext(system.MenuContext{Screen: screen, HasSelection: selection})
			n, f := check(screen, selection)
			on, off = on+n, off+f
		}
	}
	if on == 0 || off == 0 {
		t.Fatalf("%d enabled and %d disabled expectations; the test must check both", on, off)
	}
}

// pooled returns the registered context menus of the folder row's pool, by
// name.
func pooled() map[string]*application.ContextMenu {
	out := map[string]*application.ContextMenu{}
	for _, name := range []string{"default", "offline", "scanning"} {
		for i := range 2 {
			n := fmt.Sprintf("%s/%s/%d", FolderRow, name, i)
			if cm, ok := testApp.ContextMenu.Get(n); ok {
				out[n] = cm
			}
		}
	}
	return out
}

// TestInstall_ContextMenuPool: Install registers the folder row's context
// menus once, two per variant, each the manifest's list for its variant
// built with the real factory; 50 popups register, replace and destroy
// nothing, and each shows one of the pooled names.
func TestInstall_ContextMenuPool(t *testing.T) {
	m := realManifest(t)
	var shown []string
	s := install(testApp, func(name string, x, y int) { shown = append(shown, name) }, m, &recEmitter{}, false)

	before := pooled()
	if len(before) != 6 {
		t.Fatalf("%d pooled context menus registered; want 6", len(before))
	}
	total := len(testApp.ContextMenu.GetAll())
	for name, cm := range before {
		var flags []string
		if _, variant, _ := strings.Cut(name, "/"); !strings.HasPrefix(variant, "default/") {
			flags = []string{strings.Split(variant, "/")[0]}
		}
		checkMenu(t, name, cm.Menu, Visible(m.Context[FolderRow], flags), false)
	}

	for i := range 50 {
		var flags []string
		switch i % 3 {
		case 1:
			flags = []string{"offline"}
		case 2:
			flags = []string{"scanning"}
		}
		s.PopupFolderMenu(fmt.Sprintf("folder-%d", i), flags, i, i)
	}
	if len(shown) != 50 {
		t.Fatalf("%d shows; want 50", len(shown))
	}
	for _, name := range shown {
		if before[name] == nil {
			t.Errorf("showed %q, which is not a pooled menu", name)
		}
	}
	after := pooled()
	for name, cm := range before {
		if after[name] != cm {
			t.Errorf("%q was replaced or removed by the popups", name)
		}
		if cm.ItemAt(0) == nil {
			t.Errorf("%q lost its items", name)
		}
	}
	if got := len(testApp.ContextMenu.GetAll()); got != total {
		t.Errorf("%d context menus registered after 50 popups; want %d", got, total)
	}
}

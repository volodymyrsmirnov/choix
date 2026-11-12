package menu

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/volodymyrsmirnov/choix"
)

// manifestPath is the approved manifest, relative to this package.
var manifestPath = filepath.Join("..", "..", "..", "frontend", "src", "design", "menus.json")

// readManifest reads the approved manifest from disk.
func readManifest(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read the approved manifest: %v", err)
	}
	return data
}

// realManifest parses the approved manifest from disk.
func realManifest(t *testing.T) Manifest {
	t.Helper()
	m, err := Parse(readManifest(t))
	if err != nil {
		t.Fatalf("Parse(menus.json): %v", err)
	}
	return m
}

// TestMenuManifest_IsTheApprovedFile: the embedded manifest is the file on
// disk, byte for byte, and parses.
func TestMenuManifest_IsTheApprovedFile(t *testing.T) {
	if !bytes.Equal(choix.MenuManifest, readManifest(t)) {
		t.Fatal("choix.MenuManifest differs from frontend/src/design/menus.json")
	}
	if _, err := Parse(choix.MenuManifest); err != nil {
		t.Fatalf("Parse(choix.MenuManifest): %v", err)
	}
}

// findItem returns the item with id in the menu with menuID.
func findItem(t *testing.T, m Manifest, menuID, id string) Item {
	t.Helper()
	for _, menu := range m.Menus {
		if menu.ID != menuID {
			continue
		}
		for _, it := range menu.Items {
			if it.ID == id {
				return it
			}
		}
	}
	t.Fatalf("menu %q has no item %q", menuID, id)
	return Item{}
}

// roleLabel returns the label of the first item with role, anywhere in the
// menu bar.
func roleLabel(t *testing.T, m Manifest, role string) string {
	t.Helper()
	for _, menu := range m.Menus {
		for _, it := range menu.Items {
			if it.Role == role {
				return it.Label
			}
		}
	}
	t.Fatalf("the menu bar has no %q role item", role)
	return ""
}

// TestParse_RealManifest: the committed manifest parses, with the values the
// spec names.
func TestParse_RealManifest(t *testing.T) {
	m := realManifest(t)
	if len(m.Menus) == 0 {
		t.Fatal("no menus")
	}
	if got := findItem(t, m, "file", "add-folder").Accelerator; got != "CmdOrCtrl+O" {
		t.Errorf("add-folder accelerator = %q; want CmdOrCtrl+O", got)
	}
	if got := findItem(t, m, "file", "open-picks").Accelerator; got != "CmdOrCtrl+P" {
		t.Errorf("open-picks accelerator = %q; want CmdOrCtrl+P", got)
	}
	if got := findItem(t, m, "edit", "find").EnabledIn; !slices.Equal(got, []string{"library", "folder"}) {
		t.Errorf("find enabledIn = %q; want [library folder]", got)
	}
	if got := roleLabel(t, m, "closeWindow"); got != "Close Window" {
		t.Errorf("closeWindow label = %q; want Close Window", got)
	}
	if got := roleLabel(t, m, "toggleFullscreen"); got != "Enter Full Screen" {
		t.Errorf("toggleFullscreen label = %q; want Enter Full Screen", got)
	}
	if len(m.Context[FolderRow]) == 0 {
		t.Errorf("context %q is empty", FolderRow)
	}
	open := findItem(t, m, "file", "open-picks")
	if !open.NeedsSelection || len(open.EnabledIn) == 0 {
		t.Errorf("open-picks = %+v; want needsSelection and enabledIn", open)
	}
}

// TestParse_Errors: a manifest the native menu cannot show as approved is
// refused, one fixture per error.
func TestParse_Errors(t *testing.T) {
	// wrap puts items into the File menu of a minimal manifest.
	wrap := func(items string) string {
		return `{"menus":[{"id":"file","title":"File","items":[` + items + `]}],"context":{}}`
	}
	for _, tc := range []struct {
		name, json string
		want       error // nil: any error
	}{
		{"not JSON", `{`, nil},
		{"unknown field", wrap(`{"id":"a","label":"A","enabledIn":["library"],"acelerator":"CmdOrCtrl+A"}`), nil},
		{"duplicate id among menus", `{"menus":[
			{"id":"file","title":"File","items":[{"id":"a","label":"A","enabledIn":["library"]}]},
			{"id":"edit","title":"Edit","items":[{"id":"a","label":"A2","enabledIn":["library"]}]}],"context":{}}`, errDuplicateID},
		{"duplicate id in a context list", `{"menus":[],"context":{"folder-row":[
			{"id":"a","label":"A","enabledIn":["library"]},{"id":"a","label":"B","enabledIn":["library"]}]}}`, errDuplicateID},
		{"duplicate menu id", `{"menus":[{"id":"file","title":"File","items":[]},{"id":"file","title":"File 2","items":[]}],"context":{}}`, errDuplicateID},
		{"duplicate accelerator", wrap(`{"id":"a","label":"A","accelerator":"CmdOrCtrl+O","enabledIn":["library"]},
			{"id":"b","label":"B","accelerator":"CmdOrCtrl+O","enabledIn":["library"]}`), errDuplicateAccelerator},
		{"duplicate accelerator, other spelling", wrap(`{"id":"a","label":"A","accelerator":"CmdOrCtrl+Shift+O","enabledIn":["library"]},
			{"id":"b","label":"B","accelerator":"shift+cmd+o","enabledIn":["library"]}`), errDuplicateAccelerator},
		{"invalid accelerator", wrap(`{"id":"a","label":"A","accelerator":"Hyper+O","enabledIn":["library"]}`), errBadAccelerator},
		{"unknown role", wrap(`{"role":"undo","label":"Undo"}`), errUnknownRole},
		{"role item without label", wrap(`{"role":"quit"}`), errNoLabel},
		{"item without label", wrap(`{"id":"a","enabledIn":["library"]}`), errNoLabel},
		{"display outside the vocabulary", wrap(`{"id":"a","label":"A","display":"F5","enabledIn":["library"]}`), errUnknownDisplay},
		{"enabledIn not a ScreenId", wrap(`{"id":"a","label":"A","enabledIn":["gallery"]}`), errUnknownScreen},
		{"item with none of id, separator, role", wrap(`{"label":"A"}`), errItemKind},
		{"item with two kinds", wrap(`{"id":"a","role":"quit","label":"A","enabledIn":["library"]}`), errItemKind},
		{"separator false", wrap(`{"separator":false}`), errItemKind},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Parse([]byte(tc.json))
			if err == nil {
				t.Fatalf("Parse = %+v, nil; want an error", m)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Parse error = %v; want %v", err, tc.want)
			}
		})
	}

	// A context list may reuse a menu's id and accelerator-free items.
	ok := `{"menus":[{"id":"file","title":"File","items":[
		{"id":"open-picks","label":"Open Picks Folder","accelerator":"CmdOrCtrl+Shift+P","enabledIn":["library"],"needsSelection":true},
		{"separator":true},
		{"role":"closeWindow","label":"Close Window"}]}],
		"context":{"folder-row":[{"id":"open-picks","label":"Open Picks Folder","enabledIn":["library"],"hiddenWhen":["offline"]}]}}`
	m, err := Parse([]byte(ok))
	if err != nil {
		t.Fatalf("Parse(valid) = %v", err)
	}
	if len(m.Menus) != 1 || len(m.Menus[0].Items) != 3 || len(m.Context[FolderRow]) != 1 {
		t.Fatalf("Parse(valid) = %+v", m)
	}
	if got := m.Context[FolderRow][0].HiddenWhen; len(got) != 1 || got[0] != "offline" {
		t.Errorf("hiddenWhen = %q; want [offline]", got)
	}
}

// TestDisplayAccelerator_Table: the seven display glyphs map to Wails
// accelerators; anything else does not.
func TestDisplayAccelerator_Table(t *testing.T) {
	for _, tc := range []struct {
		display, want string
		ok            bool
	}{
		{"Space", "Space", true},
		{"X", "X", true},
		{"F", "F", true},
		{"→", "Right", true},
		{"←", "Left", true},
		{"↩", "Enter", true},
		{"⇧↩", "Shift+Enter", true},
		{"?", "Shift+/", true},
		{"F5", "", false},
		{"", "", false},
		{"x", "", false},
		{"XX", "", false},
		{"⌘O", "", false},
	} {
		got, ok := DisplayAccelerator(tc.display)
		if got != tc.want || ok != tc.ok {
			t.Errorf("DisplayAccelerator(%q) = %q, %v; want %q, %v", tc.display, got, ok, tc.want, tc.ok)
		}
	}
}

// TestHasChordModifier_Table: only ⌘, ⌃ and ⌥ make a chord; Shift alone
// does not.
func TestHasChordModifier_Table(t *testing.T) {
	for _, tc := range []struct {
		acc  string
		want bool
	}{
		{"CmdOrCtrl+O", true},
		{"CmdOrCtrl+Shift+P", true},
		{"Ctrl+Command+F", true},
		{"Option+X", true},
		{"Super+X", true},
		{"Space", false},
		{"X", false},
		{"Right", false},
		{"Shift+Enter", false},
		{"Shift+/", false},
		{"", false},
	} {
		if got := hasChordModifier(tc.acc); got != tc.want {
			t.Errorf("hasChordModifier(%q) = %v; want %v", tc.acc, got, tc.want)
		}
	}
}

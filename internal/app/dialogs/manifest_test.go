package dialogs

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/volodymyrsmirnov/choix"
)

// manifestPath is the approved manifest, relative to this package.
var manifestPath = filepath.Join("..", "..", "..", "frontend", "src", "design", "dialogs.json")

// rawManifest reads the approved manifest from disk with a plain JSON decode,
// independently of ParseAlerts, so the tests compare against the file itself.
func rawManifest(t *testing.T) (data []byte, raw map[string]struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	Buttons []struct {
		Label string `json:"label"`
		Role  string `json:"role"`
	} `json:"buttons"`
}) {
	t.Helper()
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read the approved manifest: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode the approved manifest: %v", err)
	}
	return data, raw
}

// TestRemoveAlert_EqualsManifest: the remove confirmation is the approved
// copy, word for word: the manifest's title with the folder's name in place
// of {name}, its message, its buttons in order, and the default and cancel
// buttons taken from their roles.
func TestRemoveAlert_EqualsManifest(t *testing.T) {
	data, raw := rawManifest(t)
	want, ok := raw["remove-folder"]
	if !ok {
		t.Fatal(`the approved manifest has no "remove-folder" alert`)
	}

	alerts, err := ParseAlerts(data)
	if err != nil {
		t.Fatalf("ParseAlerts(approved manifest): %v", err)
	}
	got := build(alerts["remove-folder"].For("Trip"))

	if wantTitle := strings.ReplaceAll(want.Title, "{name}", "Trip"); got.title != wantTitle {
		t.Errorf("title = %q; want %q", got.title, wantTitle)
	}
	if !strings.Contains(got.title, "Trip") || strings.Contains(got.title, "{name}") {
		t.Errorf("title = %q; want the name substituted for {name}", got.title)
	}
	if got.message != want.Message {
		t.Errorf("message = %q; want %q", got.message, want.Message)
	}
	var labels []string
	wantDefault, wantCancel := -1, -1
	for i, b := range want.Buttons {
		labels = append(labels, b.Label)
		switch b.Role {
		case "default":
			wantDefault = i
		case "cancel":
			wantCancel = i
		}
	}
	if !slices.Equal(got.buttons, labels) {
		t.Errorf("buttons = %q; want %q (the manifest's order)", got.buttons, labels)
	}
	if got.defaultButton != wantDefault || got.cancelButton != wantCancel {
		t.Errorf("default, cancel = %d, %d; want %d, %d", got.defaultButton, got.cancelButton, wantDefault, wantCancel)
	}
	if wantCancel < 0 {
		t.Error("the approved remove alert has no cancel button")
	}
}

// TestDialogManifest_IsTheApprovedFile: what the binary embeds is the
// approved manifest, byte for byte, and it parses.
func TestDialogManifest_IsTheApprovedFile(t *testing.T) {
	data, _ := rawManifest(t)
	if !bytes.Equal(choix.DialogManifest, data) {
		t.Fatal("choix.DialogManifest differs from frontend/src/design/dialogs.json")
	}
	alerts, err := ParseAlerts(choix.DialogManifest)
	if err != nil {
		t.Fatalf("ParseAlerts(choix.DialogManifest): %v", err)
	}
	if _, ok := alerts[removeFolder]; !ok {
		t.Fatalf("the embedded manifest has no %q alert", removeFolder)
	}
}

// TestFor_LongName: a long name is shown whole, and a name that itself
// contains {name} is not expanded a second time.
func TestFor_LongName(t *testing.T) {
	a := Alert{
		Title:   "Remove “{name}” from Choix?",
		Message: "Keep {name} on disk.",
		Buttons: []Button{{"Remove", RoleDefault}, {"Cancel", RoleCancel}},
	}

	long := strings.Repeat("abcdefghij", 8)
	if len(long) != 80 {
		t.Fatalf("test name is %d characters; want 80", len(long))
	}
	got := a.For(long)
	if got.Title != "Remove “"+long+"” from Choix?" {
		t.Errorf("title = %q; want the 80-character name unshortened", got.Title)
	}
	if got.Message != "Keep "+long+" on disk." {
		t.Errorf("message = %q; want the 80-character name unshortened", got.Message)
	}

	got = a.For("x{name}y")
	if got.Title != "Remove “x{name}y” from Choix?" {
		t.Errorf("title = %q; want the name inserted once, its {name} left as is", got.Title)
	}
	if got.Message != "Keep x{name}y on disk." {
		t.Errorf("message = %q; want the name inserted once, its {name} left as is", got.Message)
	}

	if a.Title != "Remove “{name}” from Choix?" || a.Message != "Keep {name} on disk." {
		t.Error("For changed the alert it was called on")
	}
	got.Buttons[0].Label = "changed"
	if a.Buttons[0].Label != "Remove" {
		t.Error("For's result shares its buttons with the manifest's alert")
	}
}

// TestParseAlerts_Errors: a manifest the native alert cannot show as
// approved is refused.
func TestParseAlerts_Errors(t *testing.T) {
	for _, tc := range []struct {
		name, json string
		want       error // nil: any error
	}{
		{"not JSON", `{`, nil},
		{"unknown role", `{"a":{"title":"t","message":"m","buttons":[{"label":"OK","role":"primary"},{"label":"Cancel","role":"cancel"}]}}`, errUnknownRole},
		{"empty role", `{"a":{"title":"t","message":"m","buttons":[{"label":"OK"},{"label":"Cancel","role":"cancel"}]}}`, errUnknownRole},
		{"no cancel", `{"a":{"title":"t","message":"m","buttons":[{"label":"OK","role":"default"}]}}`, errNoCancel},
		{"no buttons", `{"a":{"title":"t","message":"m","buttons":[]}}`, errNoCancel},
		{"two cancels", `{"a":{"title":"t","message":"m","buttons":[{"label":"No","role":"cancel"},{"label":"Cancel","role":"cancel"}]}}`, errManyCancel},
		{"two defaults", `{"a":{"title":"t","message":"m","buttons":[{"label":"A","role":"default"},{"label":"B","role":"default"},{"label":"Cancel","role":"cancel"}]}}`, errManyDefault},
		{"five buttons", `{"a":{"title":"t","message":"m","buttons":[{"label":"A","role":"destructive"},{"label":"B","role":"destructive"},{"label":"C","role":"destructive"},{"label":"D","role":"default"},{"label":"Cancel","role":"cancel"}]}}`, errTooManyButtons},
	} {
		t.Run(tc.name, func(t *testing.T) {
			alerts, err := ParseAlerts([]byte(tc.json))
			if err == nil {
				t.Fatalf("ParseAlerts = %v, nil; want an error", alerts)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("ParseAlerts error = %v; want %v", err, tc.want)
			}
			if alerts != nil {
				t.Errorf("ParseAlerts returned alerts %v with its error", alerts)
			}
		})
	}

	// The valid shapes: one cancel, at most one default, destructive
	// buttons, up to four buttons.
	ok := `{
		"one": {"title":"t","message":"m","buttons":[{"label":"Cancel","role":"cancel"}]},
		"four": {"title":"t","message":"m","buttons":[
			{"label":"A","role":"destructive"},{"label":"B","role":"destructive"},
			{"label":"C","role":"default"},{"label":"Cancel","role":"cancel"}]}
	}`
	alerts, err := ParseAlerts([]byte(ok))
	if err != nil {
		t.Fatalf("ParseAlerts(valid) = %v", err)
	}
	if len(alerts) != 2 || len(alerts["four"].Buttons) != 4 {
		t.Fatalf("ParseAlerts(valid) = %+v; want both alerts", alerts)
	}
	s := build(alerts["four"])
	if !slices.Equal(s.buttons, []string{"A", "B", "C", "Cancel"}) || s.defaultButton != 2 || s.cancelButton != 3 {
		t.Errorf("build(four) = %+v; want A, B, C, Cancel with default 2, cancel 3", s)
	}
	if s := build(alerts["one"]); s.defaultButton != -1 || s.cancelButton != 0 {
		t.Errorf("build(one) = %+v; want no default (-1), cancel 0", s)
	}
}

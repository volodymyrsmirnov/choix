package dialogs

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// removeAlerts is a manifest with the remove alert in the approved shape.
var removeAlerts = map[string]Alert{
	removeFolder: {
		Title:   "Remove “{name}” from Choix?",
		Message: "Stays on disk.",
		Buttons: []Button{{"Remove", RoleDefault}, {"Cancel", RoleCancel}},
	},
}

// failPick is a picker that must not be called.
func failPick(t *testing.T) func() ([]string, error) {
	return func() ([]string, error) {
		t.Error("the folder picker was shown")
		return nil, nil
	}
}

// failPrompt is an alert that must not be shown.
func failPrompt(t *testing.T) func(spec) (int, error) {
	return func(spec) (int, error) {
		t.Error("an alert was shown")
		return -1, nil
	}
}

// TestConfirm_OnlyNonCancelButtonConfirms: the alert shown is the manifest's
// remove alert for the folder, and only a button other than the cancel button
// confirms; no button (the dialog went away at quit) and an error decline.
func TestConfirm_OnlyNonCancelButtonConfirms(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name    string
		clicked int
		err     error
		want    bool
	}{
		{"Remove", 0, nil, true},
		{"Cancel", 1, nil, false},
		{"no button", -1, nil, false},
		{"error", 0, boom, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var shown []spec
			d := newDialogs(removeAlerts, failPick(t), func(s spec) (int, error) {
				shown = append(shown, s)
				return tc.clicked, tc.err
			}, nil)

			ok, err := d.ConfirmRemove("Trip")
			if ok != tc.want {
				t.Errorf("ConfirmRemove = %v; want %v", ok, tc.want)
			}
			if !errors.Is(err, tc.err) || (tc.err == nil) != (err == nil) {
				t.Errorf("ConfirmRemove error = %v; want %v", err, tc.err)
			}
			if len(shown) != 1 {
				t.Fatalf("alerts shown = %d; want 1", len(shown))
			}
			want := spec{
				title:         "Remove “Trip” from Choix?",
				message:       "Stays on disk.",
				buttons:       []string{"Remove", "Cancel"},
				defaultButton: 0,
				cancelButton:  1,
			}
			s := shown[0]
			if s.title != want.title || s.message != want.message || !slices.Equal(s.buttons, want.buttons) ||
				s.defaultButton != want.defaultButton || s.cancelButton != want.cancelButton {
				t.Errorf("alert shown = %+v; want %+v", s, want)
			}
		})
	}

	// With the cancel button first, the other one confirms.
	flipped := map[string]Alert{removeFolder: {
		Title:   "t",
		Buttons: []Button{{"Cancel", RoleCancel}, {"Remove", RoleDestructive}},
	}}
	for clicked, want := range []bool{false, true} {
		d := newDialogs(flipped, failPick(t), func(spec) (int, error) { return clicked, nil }, nil)
		if ok, err := d.ConfirmRemove("Trip"); ok != want || err != nil {
			t.Errorf("cancel first, button %d: ConfirmRemove = %v, %v; want %v, nil", clicked, ok, err, want)
		}
	}
}

// TestConfirm_NoAlertInManifest: without a remove alert, nothing is shown
// and the removal is not confirmed.
func TestConfirm_NoAlertInManifest(t *testing.T) {
	d := newDialogs(map[string]Alert{}, failPick(t), failPrompt(t), nil)
	ok, err := d.ConfirmRemove("Trip")
	if ok || err == nil {
		t.Fatalf("ConfirmRemove = %v, %v; want false and an error", ok, err)
	}
}

// TestPickFolders: the picker's paths come back in order; a cancelled panel
// (no paths) gives an empty slice and no error; an error is wrapped.
func TestPickFolders(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name    string
		paths   []string
		err     error
		want    []string
		wantErr bool
	}{
		{"chosen", []string{"/a/one", "/b/two"}, nil, []string{"/a/one", "/b/two"}, false},
		{"cancelled", nil, nil, []string{}, false},
		{"empty path dropped", []string{"", "/a/one"}, nil, []string{"/a/one"}, false},
		{"error", nil, boom, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newDialogs(removeAlerts, func() ([]string, error) { return tc.paths, tc.err }, failPrompt(t), nil)
			got, err := d.PickFolders()
			if tc.wantErr {
				if !errors.Is(err, tc.err) || got != nil {
					t.Fatalf("PickFolders = %q, %v; want nil, %v", got, err, tc.err)
				}
				return
			}
			if err != nil || got == nil || !slices.Equal(got, tc.want) {
				t.Fatalf("PickFolders = %#v, %v; want %#v, nil", got, err, tc.want)
			}
		})
	}
}

// TestQuit_DialogsReturn: once the application is quitting, an open dialog
// returns cancelled (picker) or declined (alert) without waiting for it, and
// no new dialog is shown.
func TestQuit_DialogsReturn(t *testing.T) {
	t.Run("open while quitting", func(t *testing.T) {
		done := make(chan struct{})
		release := make(chan struct{})
		defer close(release)
		shown := make(chan struct{}, 2)
		d := newDialogs(removeAlerts,
			func() ([]string, error) {
				shown <- struct{}{}
				<-release
				return []string{"/late"}, nil
			},
			func(spec) (int, error) {
				shown <- struct{}{}
				<-release
				return 0, nil
			},
			done)

		type pickResult struct {
			paths []string
			err   error
		}
		picked := make(chan pickResult, 1)
		go func() {
			p, err := d.PickFolders()
			picked <- pickResult{p, err}
		}()
		type confirmResult struct {
			ok  bool
			err error
		}
		confirmed := make(chan confirmResult, 1)
		go func() {
			ok, err := d.ConfirmRemove("Trip")
			confirmed <- confirmResult{ok, err}
		}()
		<-shown
		<-shown
		close(done)

		if r := <-picked; r.err != nil || r.paths == nil || len(r.paths) != 0 {
			t.Errorf("PickFolders at quit = %#v, %v; want an empty slice, nil", r.paths, r.err)
		}
		if r := <-confirmed; r.ok || r.err != nil {
			t.Errorf("ConfirmRemove at quit = %v, %v; want false, nil", r.ok, r.err)
		}
	})

	t.Run("asked after quit began", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		d := newDialogs(removeAlerts, failPick(t), failPrompt(t), done)
		if p, err := d.PickFolders(); err != nil || p == nil || len(p) != 0 {
			t.Errorf("PickFolders = %#v, %v; want an empty slice, nil", p, err)
		}
		if ok, err := d.ConfirmRemove("Trip"); ok || err != nil {
			t.Errorf("ConfirmRemove = %v, %v; want false, nil", ok, err)
		}
	})
}

// TestAwait: await returns f's result, or zero once done is closed, and does
// not start f when done is already closed.
func TestAwait(t *testing.T) {
	if v, err := await(nil, -1, func() (int, error) { return 7, nil }); v != 7 || err != nil {
		t.Errorf("await = %d, %v; want 7, nil", v, err)
	}

	done := make(chan struct{})
	close(done)
	called := false
	if v, err := await(done, -1, func() (int, error) { called = true; return 7, nil }); v != -1 || err != nil || called {
		t.Errorf("await after done = %d, %v (f called %v); want -1, nil, not called", v, err, called)
	}

	done = make(chan struct{})
	block := make(chan struct{})
	defer close(block)
	started := make(chan struct{})
	res := make(chan int, 1)
	go func() {
		v, _ := await(done, -1, func() (int, error) { close(started); <-block; return 7, nil })
		res <- v
	}()
	<-started
	close(done)
	select {
	case v := <-res:
		if v != -1 {
			t.Errorf("await when done closes = %d; want -1", v)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("await did not return when done was closed")
	}
}

// TestNativeOrder: Wails' macOS alert adds its buttons to the NSAlert in
// reverse, so they are handed to it reversed and the NSAlert gets the
// manifest's order (the first, the default, trailing).
func TestNativeOrder(t *testing.T) {
	for n, want := range map[int][]int{0: {}, 1: {0}, 2: {1, 0}, 4: {3, 2, 1, 0}} {
		if got := nativeOrder(n); !slices.Equal(got, want) {
			t.Errorf("nativeOrder(%d) = %v; want %v", n, got, want)
		}
	}
}

// TestParent_NoWindow: before the window exists, a dialog has no parent: a
// nil interface, never an interface holding a nil window.
func TestParent_NoWindow(t *testing.T) {
	get := Parent(func() *application.WebviewWindow { return nil })
	if w := get(); w != nil {
		t.Fatalf("Parent(nil window)() = %#v; want a nil Window", w)
	}
}

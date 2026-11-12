// Package dialogs is Choix's two native dialogs: the folder picker (an open
// panel choosing one or more folders) and the "Remove from Library"
// confirmation (an NSAlert).
//
// The package is design-gated: the alert's text and buttons come only from
// the approved manifest, frontend/src/design/dialogs.json (embedded as
// choix.DialogManifest), so the alert that ships is the approved one.
package dialogs

import (
	"fmt"
	"slices"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Dialogs shows the native dialogs. It implements library.FolderPicker and
// library.Confirmer. Both block until the dialog closes, and return at once
// — cancelled or declined — once the application is quitting. They are
// called from a binding's goroutine, never on the main thread: Wails shows
// the dialog on the main thread and delivers its answer there.
type Dialogs struct {
	alerts map[string]Alert
	// pick shows the folder picker and returns the paths chosen.
	pick func() ([]string, error)
	// prompt shows the alert s and returns the index, in s.buttons, of the
	// button clicked; -1 when the alert closed without one.
	prompt func(s spec) (int, error)
	// done is closed when the application begins to quit.
	done <-chan struct{}
}

// New returns the dialogs of app, attached as sheets to the window that
// window returns (shown on their own when it returns nil), with the alerts
// of the approved manifest (ParseAlerts).
func New(app *application.App, window func() application.Window, alerts map[string]Alert) *Dialogs {
	return newDialogs(alerts,
		func() ([]string, error) { return openPanel(app, window()) },
		func(s spec) (int, error) { return showAlert(app, window(), s) },
		app.Context().Done())
}

// newDialogs returns Dialogs over the given picker and alert; a nil done
// never closes.
func newDialogs(alerts map[string]Alert, pick func() ([]string, error), prompt func(spec) (int, error), done <-chan struct{}) *Dialogs {
	return &Dialogs{alerts: alerts, pick: pick, prompt: prompt, done: done}
}

// Parent adapts get, which returns the application's window or nil before it
// exists, to New's window argument. A missing window is a nil Window — never
// a Window holding a nil pointer, which Wails would dereference.
func Parent(get func() *application.WebviewWindow) func() application.Window {
	return func() application.Window {
		if w := get(); w != nil {
			return w
		}
		return nil
	}
}

// PickFolders shows the folder picker — folders only, several at once, no
// New Folder button — and returns the absolute paths chosen, in the panel's
// order. A cancelled picker, or one still open when the application begins
// to quit, gives an empty slice and no error.
func (d *Dialogs) PickFolders() ([]string, error) {
	paths, err := await(d.done, nil, d.pick)
	if err != nil {
		return nil, fmt.Errorf("dialogs: folder picker: %w", err)
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// ConfirmRemove shows the manifest's remove alert for the folder called name
// and reports whether a button other than the cancel button was clicked. An
// alert still open when the application begins to quit is declined.
func (d *Dialogs) ConfirmRemove(name string) (bool, error) {
	a, ok := d.alerts[removeFolder]
	if !ok {
		return false, fmt.Errorf("dialogs: the manifest has no %q alert", removeFolder)
	}
	if err := a.validate(); err != nil {
		return false, fmt.Errorf("dialogs: manifest alert %q: %w", removeFolder, err)
	}
	s := build(a.For(name))
	clicked, err := await(d.done, -1, func() (int, error) { return d.prompt(s) })
	if err != nil {
		return false, fmt.Errorf("dialogs: remove confirmation: %w", err)
	}
	return clicked >= 0 && clicked < len(s.buttons) && clicked != s.cancelButton, nil
}

// await runs f on its own goroutine and returns its result, or zero and no
// error as soon as done is closed. When done is already closed, f is not
// started. A goroutine left waiting on a dialog at quit ends with the process.
func await[T any](done <-chan struct{}, zero T, f func() (T, error)) (T, error) {
	select {
	case <-done:
		return zero, nil
	default:
	}
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := f()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-done:
		return zero, nil
	}
}

// openPanel shows the open panel for folders, as a sheet on w when there is
// one, and waits for it to close. Wails returns no paths for a cancelled
// panel.
//
// The sandbox grant for the paths comes from the panel itself (NSOpenPanel);
// whether Wails' panel carries it is the owner's pending hand check, spike S1
// in docs/superpowers/specs/2026-11-11-choix-2-spike-results.md.
func openPanel(app *application.App, w application.Window) ([]string, error) {
	p := app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(false)
	if w != nil {
		p = p.AttachToWindow(w)
	}
	return p.PromptForMultipleSelection()
}

// showAlert shows s as a warning alert, as a sheet on w when there is one,
// and waits for a button. MessageDialog.Show returns once the alert is up; the
// button clicked arrives through its callback on the main thread. An alert
// that closes without a button (its window closing at quit) never answers;
// await stops waiting when the application quits.
func showAlert(app *application.App, w application.Window, s spec) (int, error) {
	clicked := make(chan int, 1)
	m := app.Dialog.Warning().SetTitle(s.title).SetMessage(s.message)
	if w != nil {
		m.AttachToWindow(w)
	}
	buttons := make([]*application.Button, len(s.buttons))
	for _, i := range nativeOrder(len(s.buttons)) {
		buttons[i] = m.AddButton(s.buttons[i]).OnClick(func() {
			select {
			case clicked <- i:
			default:
			}
		})
	}
	if s.defaultButton >= 0 {
		m.SetDefaultButton(buttons[s.defaultButton])
	}
	m.SetCancelButton(buttons[s.cancelButton])
	m.Show()
	return <-clicked, nil
}

// nativeOrder returns the order in which to add n manifest buttons to a Wails
// alert. Wails' macOS alert adds its buttons to the NSAlert last first
// (macosDialog.show in pkg/application/dialogs_darwin.go, v3.1.0), so they
// are added reversed and the NSAlert gets the manifest's order: its first
// button, the default, trailing ([Cancel] [Remove], as approved).
func nativeOrder(n int) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	slices.Reverse(order)
	return order
}

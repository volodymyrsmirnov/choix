package platformtest

import (
	"fmt"
	"net/url"
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// WorkspaceCall is one call made to a Workspace fake.
type WorkspaceCall struct {
	Method string // "Reveal", "OpenFolder" or "OpenURL"
	Arg    string
}

// Workspace is a platform.Workspace that records its calls instead of
// talking to Finder. Like the real one, OpenURL refuses anything but https;
// a refused call is not recorded. The zero value is ready to use; it is safe
// for concurrent use.
type Workspace struct {
	mu    sync.Mutex
	calls []WorkspaceCall
}

var _ platform.Workspace = (*Workspace)(nil)

func (w *Workspace) record(method, arg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, WorkspaceCall{Method: method, Arg: arg})
}

// Reveal records the call.
func (w *Workspace) Reveal(path string) error {
	w.record("Reveal", path)
	return nil
}

// OpenFolder records the call.
func (w *Workspace) OpenFolder(path string) error {
	w.record("OpenFolder", path)
	return nil
}

// OpenURL records the call when rawURL is an https URL and fails otherwise.
func (w *Workspace) OpenURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("platformtest: open url: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("platformtest: open url %q: only https URLs are opened", rawURL)
	}
	w.record("OpenURL", rawURL)
	return nil
}

// Calls returns the recorded calls in order.
func (w *Workspace) Calls() []WorkspaceCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]WorkspaceCall(nil), w.calls...)
}

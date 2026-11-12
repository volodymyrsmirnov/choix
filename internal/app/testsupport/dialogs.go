//go:build server

package testsupport

import (
	"slices"
	"sync"
)

// Dialogs stands in for the native folder picker and the remove
// confirmation in server builds, where native dialogs cannot be shown or
// automated. A test sets their answers through the Service. The zero value
// is ready to use: the picker is cancelled and a removal is declined. It is
// safe for concurrent use.
type Dialogs struct {
	mu      sync.Mutex
	paths   []string
	confirm bool
}

// PickFolders returns the paths set for the next pick and forgets them, so
// they answer one AddFolders only; with none set the picker counts as
// cancelled (no paths, no error).
func (d *Dialogs) PickFolders() ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	paths := d.paths
	d.paths = nil
	return paths, nil
}

// ConfirmRemove returns the answer last set; false (declined) until one is.
func (d *Dialogs) ConfirmRemove(string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.confirm, nil
}

// setPickerPaths sets what the next PickFolders returns (a copy of paths).
func (d *Dialogs) setPickerPaths(paths []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paths = slices.Clone(paths)
}

// setConfirm sets the answer of every later ConfirmRemove.
func (d *Dialogs) setConfirm(yes bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.confirm = yes
}

//go:build server

package testsupport

import (
	"slices"
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/app/library"
)

var (
	_ library.FolderPicker = (*Dialogs)(nil)
	_ library.Confirmer    = (*Dialogs)(nil)
)

func TestDialogs_ZeroValueCancelsAndDeclines(t *testing.T) {
	var d Dialogs
	paths, err := d.PickFolders()
	if err != nil || paths != nil {
		t.Errorf("PickFolders = %v, %v; want nil, nil (cancelled)", paths, err)
	}
	ok, err := d.ConfirmRemove("folder-1")
	if err != nil || ok {
		t.Errorf("ConfirmRemove = %v, %v; want false, nil (declined)", ok, err)
	}
}

func TestDialogs_PickerPathsAnswerOnce(t *testing.T) {
	var d Dialogs
	in := []string{"/e2e/a", "/e2e/b"}
	d.setPickerPaths(in)
	in[0] = "/changed" // the caller's slice is not kept

	got, err := d.PickFolders()
	if err != nil || !slices.Equal(got, []string{"/e2e/a", "/e2e/b"}) {
		t.Fatalf("PickFolders = %v, %v; want [/e2e/a /e2e/b]", got, err)
	}
	if got, err := d.PickFolders(); err != nil || got != nil {
		t.Errorf("second PickFolders = %v, %v; want nil, nil (cancelled)", got, err)
	}
}

func TestDialogs_ConfirmAnswerPersists(t *testing.T) {
	var d Dialogs
	d.setConfirm(true)
	for range 2 {
		if ok, err := d.ConfirmRemove("folder-1"); err != nil || !ok {
			t.Fatalf("ConfirmRemove = %v, %v; want true", ok, err)
		}
	}
	d.setConfirm(false)
	if ok, _ := d.ConfirmRemove("folder-1"); ok {
		t.Error("ConfirmRemove = true after setConfirm(false)")
	}
}

func TestDialogs_ConcurrentUse(t *testing.T) {
	var d Dialogs
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			d.setPickerPaths([]string{"/e2e/a"})
			d.setConfirm(i%2 == 0)
			_, _ = d.PickFolders()
			_, _ = d.ConfirmRemove("folder-1")
		})
	}
	wg.Wait()
}

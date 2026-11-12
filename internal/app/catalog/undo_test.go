package catalog

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

func TestUndoStack_DropsOldest(t *testing.T) {
	var u undoStack
	for i := range undoLimit + 5 {
		u.push(undoEntry{fileID: int64(i)})
	}
	if got := len(u.entries); got != undoLimit {
		t.Fatalf("len = %d; want %d", got, undoLimit)
	}
	for want := int64(undoLimit + 4); want >= 5; want-- {
		e, ok := u.pop()
		if !ok || e.fileID != want {
			t.Fatalf("pop = %+v, %v; want file %d", e, ok, want)
		}
	}
	if _, ok := u.pop(); ok {
		t.Fatal("pop on an empty stack succeeded")
	}
	u.push(undoEntry{fileID: 1})
	u.clear()
	if _, ok := u.pop(); ok {
		t.Fatal("pop after clear succeeded")
	}
}

// --- Undo through the catalog ----------------------------------------------------

func TestUndo_RestoresPreviousState(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", []byte("stub"))
	})
	h.open(t, id)
	res, err := h.cat.SetPick(id, a, "pick")
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(root, filepath.FromSlash(res.ExportedPath))
	if _, err := h.cat.SetPick(id, a, "reject"); err != nil {
		t.Fatal(err)
	}

	res, err = h.cat.Undo(id)
	if err != nil || res.State != "picked" || res.FileID != a || res.ExportedPath == "" {
		t.Fatalf("first Undo = %+v, %v; want picked with a copy", res, err)
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(res.ExportedPath))); err != nil {
		t.Fatalf("the copy is not back after undoing the reject: %v", err)
	}
	res, err = h.cat.Undo(id)
	if err != nil || res.State != "unmarked" {
		t.Fatalf("second Undo = %+v, %v; want unmarked", res, err)
	}
	if _, err := os.Lstat(copyPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the second Undo left the copy: %v", err)
	}
	if state, _ := catalogTestPickState(t, h.session(t, id).Store, a); state != "" {
		t.Fatalf("stored state = %q; want unmarked", state)
	}
}

func TestUndo_EmptyStack(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, _ := h.folder(t, "f")
	h.open(t, id)
	_, err := h.cat.Undo(id)
	if e := apperr.From(err); e == nil || e.Code != apperr.CodeNotFound || e.Message != "nothing to undo" {
		t.Fatalf("Undo on an empty stack = %+v; want not_found \"nothing to undo\"", e)
	}
}

func TestUndo_ClearedOnClose(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", []byte("stub"))
	})
	h.open(t, id)
	if _, err := h.cat.SetPick(id, a, "reject"); err != nil {
		t.Fatal(err)
	}
	h.cat.Close(id)
	h.open(t, id)
	if _, err := h.cat.Undo(id); catalogTestCode(err) != apperr.CodeNotFound {
		t.Fatalf("Undo after reopening = %v; want not_found", err)
	}
	if state, _ := catalogTestPickState(t, h.session(t, id).Store, a); state != "rejected" {
		t.Fatalf("stored state = %q; want the reject kept", state)
	}
}

func TestUndo_CappedAt100(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", nil)
	})
	h.open(t, id)
	for i := range 101 {
		action := "reject"
		if i%2 == 1 {
			action = "unreject"
		}
		if _, err := h.cat.SetPick(id, a, action); err != nil {
			t.Fatalf("mutation %d: %v", i, err)
		}
	}
	for i := range 100 {
		if _, err := h.cat.Undo(id); err != nil {
			t.Fatalf("Undo %d: %v", i+1, err)
		}
	}
	if _, err := h.cat.Undo(id); catalogTestCode(err) != apperr.CodeNotFound {
		t.Fatalf("Undo 101 = %v; want not_found", err)
	}
	// 101 mutations ending rejected; 100 undone leaves the first one.
	if state, _ := catalogTestPickState(t, h.session(t, id).Store, a); state != "rejected" {
		t.Fatalf("stored state = %q; want rejected (the oldest mutation dropped from the stack)", state)
	}
}

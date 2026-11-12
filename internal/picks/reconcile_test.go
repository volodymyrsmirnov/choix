package picks

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/scanner"
)

func TestReconcile_NeverReexports(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "A")
	if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.root, "picks", "a.jpg")); err != nil {
		t.Fatal(err)
	}

	rep, err := e.svc.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep != (ReconcileReport{Missing: 1}) {
		t.Errorf("report = %+v, want Missing 1 only", rep)
	}
	if got := regularFilesUnder(t, e.root, "picks"); len(got) != 0 {
		t.Errorf("Reconcile wrote %q into picks/", got)
	}
	if st, exp := e.pickRow(t, id); st != "picked" || exp != "picks/a.jpg" {
		t.Errorf("row = (%q, %q), want (picked, picks/a.jpg) unchanged", st, exp)
	}
}

func TestReconcile_AdoptsIdenticalCopy(t *testing.T) {
	e := newEnv(t, "picks")
	same := e.addFile(t, "Day1/a.jpg", "A")
	other := e.addFile(t, "Day1/b.jpg", "B")
	writeFile(t, e.root, "picks/Day1/a.jpg", "A")
	writeFile(t, e.root, "picks/Day1/b.jpg", "not B")
	e.setRow(t, same, "picked", "")
	e.setRow(t, other, "picked", "")

	rep, err := e.svc.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep.Adopted != 1 {
		t.Errorf("Adopted = %d, want 1", rep.Adopted)
	}
	if rep.Orphans != 1 {
		t.Errorf("Orphans = %d, want 1 (the different picks/Day1/b.jpg)", rep.Orphans)
	}
	if st, exp := e.pickRow(t, same); st != "picked" || exp != "picks/Day1/a.jpg" {
		t.Errorf("adopted row = (%q, %q), want (picked, picks/Day1/a.jpg)", st, exp)
	}
	if st, exp := e.pickRow(t, other); st != "picked" || exp != "" {
		t.Errorf("different-content row = (%q, %q), want (picked, \"\")", st, exp)
	}
	if got := readFile(t, e.root, "picks/Day1/b.jpg"); got != "not B" {
		t.Errorf("picks/Day1/b.jpg = %q, want untouched", got)
	}
}

func TestReconcile_CountsOrphansAndDeletesNothing(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "A")
	if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
		t.Fatal(err)
	}
	writeFile(t, e.root, "picks/old/orphan.jpg", "left by a failed removal")
	writeFile(t, e.root, "picks/stray.HEIC", "dropped in by the user")
	writeFile(t, e.root, "picks/notes.txt", "not media")
	before := snapshot(t, e.root)

	rep, err := e.svc.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep != (ReconcileReport{Orphans: 2}) {
		t.Errorf("report = %+v, want Orphans 2 only", rep)
	}
	after := snapshot(t, e.root)
	if len(after) != len(before) {
		t.Errorf("tree changed: %q -> %q", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed", k)
		}
	}
}

func TestReconcile_OpensNoOriginal(t *testing.T) {
	e := newEnv(t, "picks")
	present := e.addFile(t, "orig/a.jpg", "A")
	gone := e.addFile(t, "orig/b.jpg", "B")
	legacy := e.addFile(t, "orig/c.jpg", "C")
	if _, err := e.svc.Set(context.Background(), present, StatePicked); err != nil {
		t.Fatal(err)
	}
	e.setRow(t, gone, "picked", "picks/orig/b.jpg")
	e.setRow(t, legacy, "picked", "")
	writeFile(t, e.root, "picks/orig/c.jpg", "C")

	orig := filepath.Join(e.root, "orig")
	if err := os.Chmod(orig, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(orig, 0o700) })

	// Reconcile still succeeds with the originals unreadable. Adoption needs
	// a full comparison with the original (fix round 1, finding 5), so the
	// candidate picks/orig/c.jpg cannot be verified: it is not adopted, the
	// row counts as missing, and the unreferenced file as an orphan.
	rep, err := e.svc.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile with unreadable originals: %v", err)
	}
	if rep != (ReconcileReport{Missing: 2, Orphans: 1}) {
		t.Errorf("report = %+v, want Missing 2 (one gone, one unverifiable), Orphans 1", rep)
	}
	if _, exp := e.pickRow(t, legacy); exp != "" {
		t.Errorf("unverifiable copy recorded as %q", exp)
	}
}

// Finding 5: equal size and equal first/last 64 KiB (so the scanner's
// sampled hash matches) but different middle bytes is not identical content.
func TestReconcile_SampledHashCollisionNotAdopted(t *testing.T) {
	e := newEnv(t, "picks")
	orig := bytes.Repeat([]byte{'o'}, 300<<10)
	copyBytes := bytes.Clone(orig)
	copyBytes[150<<10] = 'X'
	id := e.addFile(t, "Day1/a.jpg", string(orig))
	writeFile(t, e.root, "picks/Day1/a.jpg", string(copyBytes))
	e.setRow(t, id, "picked", "")

	h1, err := scanner.ContentHash(filepath.Join(e.root, "Day1/a.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := scanner.ContentHash(filepath.Join(e.root, "picks/Day1/a.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("test setup: sampled hashes differ (%s vs %s)", h1, h2)
	}

	rep, err := e.svc.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep.Adopted != 0 {
		t.Errorf("Adopted = %d, want 0", rep.Adopted)
	}
	if _, exp := e.pickRow(t, id); exp != "" {
		t.Errorf("different copy recorded as %q", exp)
	}
	if rep.Orphans != 1 {
		t.Errorf("Orphans = %d, want 1 (the different copy)", rep.Orphans)
	}
}

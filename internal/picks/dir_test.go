package picks

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

func TestValidateDir(t *testing.T) {
	for _, tc := range []struct {
		dir    string
		reason string // "" = accepted
	}{
		{"", "empty"},
		{"/x", "absolute"},
		{"../x", "traversal"},
		{"a/../../b", "traversal"},
		{".", "reserved"},
		{".choix", "reserved"},
		{".choix/x", "reserved"},
		{".Choix/x", "reserved"},
		{"a/..", "reserved"},
		{"picks", ""},
		{"exports/luminar", ""},
	} {
		err := ValidateDir(tc.dir)
		if tc.reason == "" {
			if err != nil {
				t.Errorf("ValidateDir(%q) = %v, want nil", tc.dir, err)
			}
			continue
		}
		var de *DirError
		if !errors.As(err, &de) || de.Reason != tc.reason {
			t.Errorf("ValidateDir(%q) = %v, want DirError %q", tc.dir, err, tc.reason)
		}
	}
}

func TestCheckDir_ScannedOriginalsBlock(t *testing.T) {
	e := newEnv(t, "shoot1")
	e.addFile(t, "shoot1/a.jpg", "A")
	id := e.addFile(t, "shoot1/b.jpg", "B")

	if err := CheckDir(e.st, "shoot1"); !errors.Is(err, ErrDirHasOriginals) {
		t.Fatalf("CheckDir = %v, want ErrDirHasOriginals", err)
	}
	before := snapshot(t, e.root)
	if _, err := e.svc.Set(context.Background(), id, StatePicked); !errors.Is(err, ErrDirHasOriginals) {
		t.Fatalf("Set picked = %v, want ErrDirHasOriginals", err)
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("state = %q, want unchanged", st)
	}
	if after := snapshot(t, e.root); len(after) != len(before) {
		t.Errorf("tree changed: %q -> %q", before, after)
	}
}

func TestCheckDir_UnknownFilesDoNotBlock(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "A")
	writeFile(t, e.root, "picks/old.jpg", "orphan copy")
	writeFile(t, e.root, "picks/stray.jpg", "dropped in by the user")

	if err := CheckDir(e.st, "picks"); err != nil {
		t.Fatalf("CheckDir = %v, want nil", err)
	}
	if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
		t.Fatalf("Set picked: %v", err)
	}
	if got := readFile(t, e.root, "picks/a.jpg"); got != "A" {
		t.Errorf("copy = %q", got)
	}
}

func TestCheckDir_MissingRowsDoNotBlock(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "picks/gone.jpg", "G")
	if err := e.st.Files().UpdateStatus(id, "missing", ""); err != nil {
		t.Fatal(err)
	}
	if err := CheckDir(e.st, "picks"); err != nil {
		t.Fatalf("CheckDir = %v, want nil (missing rows do not count)", err)
	}
}

func TestEffectiveDir_LegacyWins(t *testing.T) {
	e := newEnv(t, "picks")
	a := e.addFile(t, "a.jpg", "A")
	b := e.addFile(t, "Day1/b.jpg", "B")
	c := e.addFile(t, "c.jpg", "C")
	e.setRow(t, a, "picked", "exports/out/a.jpg")
	e.setRow(t, b, "picked", "exports/out/Day1/b.jpg")
	e.setRow(t, c, "picked", "old/c.jpg")

	dir, others, err := EffectiveDir(e.st, "picks")
	if err != nil {
		t.Fatalf("EffectiveDir: %v", err)
	}
	if dir != "exports/out" {
		t.Errorf("dir = %q, want exports/out", dir)
	}
	if !slices.Equal(others, []string{"old"}) {
		t.Errorf("others = %q, want [old]", others)
	}
}

// A v1 database whose picks were exported under a traversal directory keeps
// the brief's literal choice; picking there is refused with a typed
// DirError whose reason a service can show.
func TestEffectiveDir_InvalidLegacyDirGivesDirError(t *testing.T) {
	parent := t.TempDir()
	e := newEnvAt(t, filepath.Join(parent, "root"), "picks")
	a := e.addFile(t, "a.jpg", "A")
	b := e.addFile(t, "b.jpg", "B")
	e.setRow(t, a, "picked", "../x/a.jpg")

	dir, _, err := EffectiveDir(e.st, "picks")
	if err != nil {
		t.Fatalf("EffectiveDir: %v", err)
	}
	if dir != "../x" {
		t.Fatalf("dir = %q, want ../x", dir)
	}
	_, err = New(e.st, e.root, dir).Set(context.Background(), b, StatePicked)
	var de *DirError
	if !errors.As(err, &de) || de.Reason != "traversal" {
		t.Fatalf("Set picked = %v, want DirError traversal", err)
	}
	if exists(parent, "x") {
		t.Error("created a directory outside the scan root")
	}
}

func TestEffectiveDir_NoExportsUsesConfigured(t *testing.T) {
	e := newEnv(t, "picks")
	a := e.addFile(t, "a.jpg", "A")
	e.setRow(t, a, "rejected", "")

	dir, others, err := EffectiveDir(e.st, "exports/luminar")
	if err != nil {
		t.Fatalf("EffectiveDir: %v", err)
	}
	if dir != "exports/luminar" || len(others) != 0 {
		t.Errorf("EffectiveDir = (%q, %q), want (exports/luminar, [])", dir, others)
	}
}

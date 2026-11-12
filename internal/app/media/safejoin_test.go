package media

import (
	"os"
	"path/filepath"
	"testing"
)

// safeJoinTestTree makes root/inside.jpg, root/sub/, a file outside root and
// returns root (unresolved, as os.MkdirTemp gives it on macOS: /var/…) and
// the outside file.
func safeJoinTestTree(t *testing.T) (root, outside string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.jpg"), []byte("in"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside = filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

func TestSafeJoinUnderRoot_InsidePasses(t *testing.T) {
	root, _ := safeJoinTestTree(t)
	got, err := safeJoinUnderRoot(root, filepath.Join(root, "inside.jpg"))
	if err != nil {
		t.Fatalf("safeJoinUnderRoot: %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(root, "inside.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q, want the resolved path %q", got, want)
	}
}

func TestSafeJoinUnderRoot_DotDotEscapes(t *testing.T) {
	root, _ := safeJoinTestTree(t)
	for _, rel := range []string{"../escape", "../../etc/passwd", "sub/../../x"} {
		if got, err := safeJoinUnderRoot(root, filepath.Join(root, rel)); err == nil {
			t.Errorf("%q: got %q, want an error", rel, got)
		}
	}
}

func TestSafeJoinUnderRoot_AbsoluteOutsideRefused(t *testing.T) {
	root, outside := safeJoinTestTree(t)
	if got, err := safeJoinUnderRoot(root, outside); err == nil {
		t.Fatalf("got %q, want an error", got)
	}
	if got, err := safeJoinUnderRoot(root, "/etc/passwd"); err == nil {
		t.Fatalf("got %q, want an error", got)
	}
}

// A sibling whose name merely starts with the root's is outside it.
func TestSafeJoinUnderRoot_SiblingPrefixRefused(t *testing.T) {
	root, _ := safeJoinTestTree(t)
	sibling := root + "-other"
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := safeJoinUnderRoot(root, filepath.Join(sibling, "x.jpg")); err == nil {
		t.Fatalf("got %q, want an error", got)
	}
}

func TestSafeJoinUnderRoot_SymlinkOutsideRefused(t *testing.T) {
	root, outside := safeJoinTestTree(t)
	link := filepath.Join(root, "sub", "link.jpg")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if got, err := safeJoinUnderRoot(root, link); err == nil {
		t.Fatalf("got %q, want an error for a symlink leaving the root", got)
	}
	dirLink := filepath.Join(root, "outdir")
	if err := os.Symlink(filepath.Dir(outside), dirLink); err != nil {
		t.Fatal(err)
	}
	if got, err := safeJoinUnderRoot(root, filepath.Join(dirLink, filepath.Base(outside))); err == nil {
		t.Fatalf("got %q, want an error for a symlinked directory leaving the root", got)
	}
}

func TestSafeJoinUnderRoot_SymlinkInsidePasses(t *testing.T) {
	root, _ := safeJoinTestTree(t)
	link := filepath.Join(root, "sub", "alias.jpg")
	if err := os.Symlink(filepath.Join(root, "inside.jpg"), link); err != nil {
		t.Fatal(err)
	}
	got, err := safeJoinUnderRoot(root, link)
	if err != nil {
		t.Fatalf("safeJoinUnderRoot: %v", err)
	}
	if filepath.Base(got) != "inside.jpg" {
		t.Fatalf("got %q, want the link's target", got)
	}
}

// The root itself reached through a symlink (macOS's /var → /private/var)
// still contains its files.
func TestSafeJoinUnderRoot_SymlinkedRoot(t *testing.T) {
	root, _ := safeJoinTestTree(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := safeJoinUnderRoot(alias, filepath.Join(alias, "inside.jpg")); err != nil {
		t.Fatalf("safeJoinUnderRoot through a symlinked root: %v", err)
	}
}

// A path that does not exist yet is checked lexically.
func TestSafeJoinUnderRoot_MissingPathLexical(t *testing.T) {
	root, _ := safeJoinTestTree(t)
	want := filepath.Join(root, "sub", "new.jpg")
	got, err := safeJoinUnderRoot(root, want)
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q, nil", got, err, want)
	}
	if got, err := safeJoinUnderRoot(root, filepath.Join(root, "..", "new.jpg")); err == nil {
		t.Fatalf("got %q, want an error", got)
	}
}

package picks

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// testEnv is a scan root with its state.db under <root>/.choix, like a real
// folder.
type testEnv struct {
	root string
	st   *store.Store
	svc  *Service
}

func newEnvAt(t *testing.T, root, picksDir string) *testEnv {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".choix"), 0o750); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, ".choix", "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &testEnv{root: root, st: st, svc: New(st, root, picksDir)}
}

func newEnv(t *testing.T, picksDir string) *testEnv {
	t.Helper()
	return newEnvAt(t, t.TempDir(), picksDir)
}

// writeFile creates rel under root with contents (making parents).
func writeFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// addFile writes an original and inserts its files row the way the scanner
// would.
func (e *testEnv) addFile(t *testing.T, rel, contents string) int64 {
	t.Helper()
	writeFile(t, e.root, rel, contents)
	return e.addRow(t, rel)
}

// addRow inserts a files row for an existing file under root.
func (e *testEnv) addRow(t *testing.T, rel string) int64 {
	t.Helper()
	abs := filepath.Join(e.root, rel)
	hash, err := scanner.ContentHash(abs)
	if err != nil {
		t.Fatalf("hash %s: %v", rel, err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.st.Files().Insert(store.File{
		Path: rel, Size: fi.Size(), Mtime: fi.ModTime().Unix(), ContentHash: hash,
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert %s: %v", rel, err)
	}
	return id
}

// pickRow returns the stored state ("" when there is no row) and exported path.
func (e *testEnv) pickRow(t *testing.T, id int64) (state, exported string) {
	t.Helper()
	p, err := e.st.Picks().Get(id)
	if errors.Is(err, store.ErrNotFound) {
		return "", ""
	}
	if err != nil {
		t.Fatalf("Picks().Get: %v", err)
	}
	return p.State, p.ExportedPath.String
}

// setRow writes a pick row directly, bypassing the service.
func (e *testEnv) setRow(t *testing.T, id int64, state, exported string) {
	t.Helper()
	if err := e.st.Picks().SetState(id, state); err != nil {
		t.Fatal(err)
	}
	if exported != "" {
		if err := e.st.Picks().SetExportedPath(id, exported); err != nil {
			t.Fatal(err)
		}
	}
}

// pick, unpick, reject and unreject are v1's Service wrappers (removed with
// the v1 shell in Task 62), kept here so the v1 behaviour tests still run.
// pick and reject are Set; unpick and unreject return the file to unmarked
// only when it is in the state they undo, and are a no-op otherwise.
func (e *testEnv) pick(id int64) error {
	_, err := e.svc.Set(context.Background(), id, StatePicked)
	return err
}

func (e *testEnv) reject(id int64) error {
	_, err := e.svc.Set(context.Background(), id, StateRejected)
	return err
}

func (e *testEnv) unpick(id int64) error { return e.leave(id, StatePicked) }

func (e *testEnv) unreject(id int64) error { return e.leave(id, StateRejected) }

// leave sets id to StateNone when it is currently in state from.
func (e *testEnv) leave(id int64, from State) error {
	if _, err := e.svc.fileFor(id); err != nil {
		return err
	}
	cur, _, err := e.svc.current(id)
	if err != nil || cur != from {
		return err
	}
	_, err = e.svc.Set(context.Background(), id, StateNone)
	return err
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func exists(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, rel))
	return err == nil
}

// snapshot lists every entry under root except .choix: files with their
// contents, symlinks with their targets, directories as "dir".
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		if rel == ".choix" {
			return fs.SkipDir
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[rel] = "-> " + target
		case d.IsDir():
			out[rel] = "dir"
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}

// regularFilesUnder lists the regular files under root/rel.
func regularFilesUnder(t *testing.T, root, rel string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(root, rel), func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipDir
		}
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			r, _ := filepath.Rel(root, p)
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

// recorder collects picks' trace, including the safefs events forwarded into
// it. Extra safefs hooks (failure injection) can be passed in h.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) record(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events = nil
	return out
}

func traceWith(t *testing.T, h safefs.Hooks) *recorder {
	t.Helper()
	r := &recorder{}
	restore := SetTraceForTest(r.record)
	h.Trace = r.record
	restoreFS := safefs.SetHooksForTest(h)
	t.Cleanup(func() {
		restoreFS()
		restore()
	})
	return r
}

func TestSet_PickOrder(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	rec := traceWith(t, safefs.Hooks{})

	res, err := e.svc.Set(context.Background(), id, StatePicked)
	if err != nil {
		t.Fatalf("Set picked: %v", err)
	}
	// picks/ did not exist: its creation is made durable (the root is
	// synced) before anything is published in it.
	want := []string{"mkdir-sync", "copy", "file-sync", "rename", "dir-sync", "tx-commit"}
	if got := rec.take(); !slices.Equal(got, want) {
		t.Errorf("trace = %q, want %q", got, want)
	}
	if res.State != StatePicked || res.ExportedPath != "picks/a.jpg" {
		t.Errorf("result = %+v, want picked at picks/a.jpg", res)
	}
	if got := readFile(t, e.root, "picks/a.jpg"); got != "PIXELS" {
		t.Errorf("copy = %q, want PIXELS", got)
	}
	if st, exp := e.pickRow(t, id); st != "picked" || exp != "picks/a.jpg" {
		t.Errorf("row = (%q, %q), want (picked, picks/a.jpg)", st, exp)
	}
}

// With the export directory already there, the trace is exactly the brief's.
func TestSet_PickOrderExistingDir(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "Day1/a.jpg", "PIXELS")
	if err := os.MkdirAll(filepath.Join(e.root, "picks", "Day1"), 0o750); err != nil {
		t.Fatal(err)
	}
	rec := traceWith(t, safefs.Hooks{})
	if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
		t.Fatalf("Set picked: %v", err)
	}
	want := []string{"copy", "file-sync", "rename", "dir-sync", "tx-commit"}
	if got := rec.take(); !slices.Equal(got, want) {
		t.Errorf("trace = %q, want %q", got, want)
	}
}

// Each directory the export creates has its parent synced: picks in the
// root, then Day1 in picks.
func TestSet_PickOrderNestedCreation(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "Day1/a.jpg", "PIXELS")
	rec := traceWith(t, safefs.Hooks{})
	if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
		t.Fatalf("Set picked: %v", err)
	}
	want := []string{"mkdir-sync", "mkdir-sync", "copy", "file-sync", "rename", "dir-sync", "tx-commit"}
	if got := rec.take(); !slices.Equal(got, want) {
		t.Errorf("trace = %q, want %q", got, want)
	}
}

func TestSet_MkdirSyncErrors(t *testing.T) {
	t.Run("EIO fails the pick", func(t *testing.T) {
		e := newEnv(t, "picks")
		id := e.addFile(t, "Day1/a.jpg", "PIXELS")
		traceWith(t, safefs.Hooks{SyncMkdir: func(*os.File) error { return unix.EIO }})
		_, err := e.svc.Set(context.Background(), id, StatePicked)
		if !errors.Is(err, unix.EIO) {
			t.Fatalf("Set picked = %v, want EIO", err)
		}
		if st, _ := e.pickRow(t, id); st != "" {
			t.Errorf("state = %q, want unchanged", st)
		}
		if got := regularFilesUnder(t, e.root, "picks"); len(got) != 0 {
			t.Errorf("files under picks/: %q", got)
		}
	})
	// Finding 8: the failed directory is removed, so a retry cannot skip the
	// parent sync and succeed on an unconfirmed directory.
	t.Run("persistent EIO fails every retry until lifted", func(t *testing.T) {
		e := newEnv(t, "picks")
		id := e.addFile(t, "Day1/a.jpg", "PIXELS")
		failing := true
		rec := traceWith(t, safefs.Hooks{SyncMkdir: func(f *os.File) error {
			if failing {
				return unix.EIO
			}
			return f.Sync()
		}})
		for attempt := 1; attempt <= 2; attempt++ {
			_, err := e.svc.Set(context.Background(), id, StatePicked)
			if !errors.Is(err, unix.EIO) {
				t.Fatalf("attempt %d: Set picked = %v, want EIO", attempt, err)
			}
			if got := rec.take(); !slices.Contains(got, "mkdir-sync") || slices.Contains(got, "tx-commit") {
				t.Errorf("attempt %d: trace = %q, want a mkdir-sync and no commit", attempt, got)
			}
			if st, _ := e.pickRow(t, id); st != "" {
				t.Errorf("attempt %d: state = %q, want unchanged", attempt, st)
			}
			if exists(e.root, "picks") {
				t.Errorf("attempt %d: picks/ left behind after the failed parent sync", attempt)
			}
		}

		failing = false
		res, err := e.svc.Set(context.Background(), id, StatePicked)
		if err != nil {
			t.Fatalf("Set picked after recovery: %v", err)
		}
		want := []string{"mkdir-sync", "mkdir-sync", "copy", "file-sync", "rename", "dir-sync", "tx-commit"}
		if got := rec.take(); !slices.Equal(got, want) {
			t.Errorf("trace = %q, want %q", got, want)
		}
		if res.ExportedPath != "picks/Day1/a.jpg" {
			t.Errorf("exported = %q", res.ExportedPath)
		}
	})
	t.Run("not supported is tolerated", func(t *testing.T) {
		e := newEnv(t, "picks")
		id := e.addFile(t, "Day1/a.jpg", "PIXELS")
		traceWith(t, safefs.Hooks{SyncMkdir: func(*os.File) error { return unix.ENOTSUP }})
		if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
			t.Fatalf("Set picked: %v", err)
		}
		if st, exp := e.pickRow(t, id); st != "picked" || exp != "picks/Day1/a.jpg" {
			t.Errorf("row = (%q, %q)", st, exp)
		}
	})
}

func TestSet_UnpickOrder(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
		t.Fatal(err)
	}
	rec := traceWith(t, safefs.Hooks{})

	res, err := e.svc.Set(context.Background(), id, StateNone)
	if err != nil {
		t.Fatalf("Set unmarked: %v", err)
	}
	want := []string{"tx-commit", "remove", "dir-sync"}
	if got := rec.take(); !slices.Equal(got, want) {
		t.Errorf("trace = %q, want %q", got, want)
	}
	if res.State != StateNone || res.ExportedPath != "" {
		t.Errorf("result = %+v, want unmarked", res)
	}
	if exists(e.root, "picks/a.jpg") {
		t.Error("copy still present after unpick")
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("row state = %q, want no row", st)
	}
}

func TestSet_RejectedUnmarkedOnlyCommits(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	rec := traceWith(t, safefs.Hooks{})

	if _, err := e.svc.Set(context.Background(), id, StateRejected); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); !slices.Equal(got, []string{"tx-commit"}) {
		t.Errorf("reject trace = %q, want [tx-commit]", got)
	}
	if st, _ := e.pickRow(t, id); st != "rejected" {
		t.Errorf("state = %q, want rejected", st)
	}
	if _, err := e.svc.Set(context.Background(), id, StateNone); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); !slices.Equal(got, []string{"tx-commit"}) {
		t.Errorf("unreject trace = %q, want [tx-commit]", got)
	}
	if exists(e.root, "picks") {
		t.Error("reject/unreject created the picks directory")
	}
}

func TestSet_PickedToRejectedRemovesCopy(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "y.jpg", "data2")
	if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
		t.Fatal(err)
	}
	rec := traceWith(t, safefs.Hooks{})
	if _, err := e.svc.Set(context.Background(), id, StateRejected); err != nil {
		t.Fatalf("Set rejected: %v", err)
	}
	if got, want := rec.take(), []string{"tx-commit", "remove", "dir-sync"}; !slices.Equal(got, want) {
		t.Errorf("trace = %q, want %q", got, want)
	}
	if exists(e.root, "picks/y.jpg") {
		t.Error("rejected file should be removed from picks/")
	}
	if st, exp := e.pickRow(t, id); st != "rejected" || exp != "" {
		t.Errorf("row = (%q, %q), want (rejected, \"\")", st, exp)
	}
}

func TestSet_PickTxFailureRemovesCopy(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	// Close the store once the copy is published, so only the commit fails.
	rec := traceWith(t, safefs.Hooks{SyncDir: func(d *os.File) error {
		_ = e.st.Close()
		return d.Sync()
	}})

	_, err := e.svc.Set(context.Background(), id, StatePicked)
	if err == nil || !strings.Contains(err.Error(), "database is closed") {
		t.Fatalf("Set picked with a closed store = %v, want the commit to fail", err)
	}
	want := []string{"mkdir-sync", "copy", "file-sync", "rename", "dir-sync", "tx-commit", "remove", "dir-sync"}
	if got := rec.take(); !slices.Equal(got, want) {
		t.Errorf("trace = %q, want %q", got, want)
	}
	if got := regularFilesUnder(t, e.root, "picks"); len(got) != 0 {
		t.Errorf("files left under picks/: %q", got)
	}
}

func TestSet_SourceOpenedReadOnly(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "orig/a.jpg", "READONLY")
	src := filepath.Join(e.root, "orig", "a.jpg")
	if err := os.Chmod(src, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(src), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(src), 0o700) })

	if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
		t.Fatalf("Set picked: %v", err)
	}
	if got := readFile(t, e.root, "orig/a.jpg"); got != "READONLY" {
		t.Errorf("original = %q, want unchanged", got)
	}
	if got := readFile(t, e.root, "picks/orig/a.jpg"); got != "READONLY" {
		t.Errorf("copy = %q, want READONLY", got)
	}
}

func TestSet_SymlinkedPicksDirRefused(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	if err := os.Symlink(".", filepath.Join(e.root, "picks")); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, e.root)

	_, err := e.svc.Set(context.Background(), id, StatePicked)
	if !errors.Is(err, safefs.ErrSymlink) {
		t.Fatalf("Set picked = %v, want safefs.ErrSymlink", err)
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("state = %q, want unchanged (no row)", st)
	}
	after := snapshot(t, e.root)
	if len(before) != len(after) {
		t.Errorf("tree changed:\nbefore %q\nafter  %q", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed: %q -> %q", k, v, after[k])
		}
	}
}

func TestSet_PlantedTmpSymlinkDoesNotTruncateOriginal(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "ORIGINAL-BYTES")
	if err := os.Mkdir(filepath.Join(e.root, "picks"), 0o750); err != nil {
		t.Fatal(err)
	}
	// v1 wrote through the fixed name <target>.tmp with O_TRUNC.
	if err := os.Symlink("../a.jpg", filepath.Join(e.root, "picks", "a.jpg.tmp")); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
		t.Fatalf("Set picked: %v", err)
	}
	if got := readFile(t, e.root, "a.jpg"); got != "ORIGINAL-BYTES" {
		t.Errorf("original = %q, want ORIGINAL-BYTES", got)
	}
	if got := readFile(t, e.root, "picks/a.jpg"); got != "ORIGINAL-BYTES" {
		t.Errorf("copy = %q, want ORIGINAL-BYTES", got)
	}
	if target, err := os.Readlink(filepath.Join(e.root, "picks", "a.jpg.tmp")); err != nil || target != "../a.jpg" {
		t.Errorf("planted symlink = %q, %v; want left alone", target, err)
	}
}

func TestSet_CollisionSuffix(t *testing.T) {
	e := newEnv(t, "picks")
	writeFile(t, e.root, "picks/a.jpg", "OTHER")
	id := e.addFile(t, "a.jpg", "MINE")

	res, err := e.svc.Set(context.Background(), id, StatePicked)
	if err != nil {
		t.Fatalf("Set picked: %v", err)
	}
	if res.ExportedPath != "picks/a_2.jpg" {
		t.Errorf("exported = %q, want picks/a_2.jpg", res.ExportedPath)
	}
	if got := readFile(t, e.root, "picks/a.jpg"); got != "OTHER" {
		t.Errorf("existing file = %q, want OTHER (never overwritten)", got)
	}
	if got := readFile(t, e.root, "picks/a_2.jpg"); got != "MINE" {
		t.Errorf("copy = %q, want MINE", got)
	}
}

func TestSet_IdempotentOnSameContent(t *testing.T) {
	e := newEnv(t, "picks")
	writeFile(t, e.root, "picks/a.jpg", "SAME")
	id := e.addFile(t, "a.jpg", "SAME")
	rec := traceWith(t, safefs.Hooks{})

	res, err := e.svc.Set(context.Background(), id, StatePicked)
	if err != nil {
		t.Fatalf("Set picked: %v", err)
	}
	if res.ExportedPath != "picks/a.jpg" {
		t.Errorf("exported = %q, want the identical picks/a.jpg reused", res.ExportedPath)
	}
	// No copy, but the reused file and its directory are synced before the
	// commit, like a published copy.
	if got, want := rec.take(), []string{"file-sync", "dir-sync", "tx-commit"}; !slices.Equal(got, want) {
		t.Errorf("trace = %q, want %q (no copy)", got, want)
	}
	if got := regularFilesUnder(t, e.root, "picks"); len(got) != 1 {
		t.Errorf("picks/ holds %q, want just a.jpg", got)
	}

	// A second pick is a no-op that reports the same export.
	res2, err := e.svc.Set(context.Background(), id, StatePicked)
	if err != nil {
		t.Fatalf("second Set picked: %v", err)
	}
	if res2 != res {
		t.Errorf("second result = %+v, want %+v", res2, res)
	}
	if got := rec.take(); len(got) != 0 {
		t.Errorf("second pick trace = %q, want nothing", got)
	}
}

func TestSet_CollisionArrivingAfterCheckIsNotOverwritten(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rename func(dirfd int, from, to string) error
	}{
		{name: "rename-excl"},
		{name: "no-rename-excl", rename: func(int, string, string) error { return unix.ENOTSUP }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, "picks")
			id := e.addFile(t, "a.jpg", "MINE")
			planted := false
			traceWith(t, safefs.Hooks{
				RenameExcl: tc.rename,
				BeforeRename: func() {
					if !planted {
						planted = true
						writeFile(t, e.root, "picks/a.jpg", "LATE")
					}
				},
			})

			res, err := e.svc.Set(context.Background(), id, StatePicked)
			if err != nil {
				t.Fatalf("Set picked: %v", err)
			}
			if res.ExportedPath != "picks/a_2.jpg" {
				t.Errorf("exported = %q, want picks/a_2.jpg", res.ExportedPath)
			}
			if got := readFile(t, e.root, "picks/a.jpg"); got != "LATE" {
				t.Errorf("late file = %q, want LATE (never overwritten)", got)
			}
			if got := readFile(t, e.root, "picks/a_2.jpg"); got != "MINE" {
				t.Errorf("copy = %q, want MINE", got)
			}
			if got := regularFilesUnder(t, e.root, "picks"); len(got) != 2 {
				t.Errorf("picks/ holds %q, want a.jpg and a_2.jpg only", got)
			}
		})
	}
}

func TestSet_SyncNotSupportedStillPicks(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	notSup := func(*os.File) error { return unix.ENOTSUP }
	traceWith(t, safefs.Hooks{SyncFile: notSup, SyncDir: notSup})

	if _, err := e.svc.Set(context.Background(), id, StatePicked); err != nil {
		t.Fatalf("Set picked on a volume without sync: %v", err)
	}
	if st, exp := e.pickRow(t, id); st != "picked" || exp != "picks/a.jpg" {
		t.Errorf("row = (%q, %q), want (picked, picks/a.jpg)", st, exp)
	}
	if got := readFile(t, e.root, "picks/a.jpg"); got != "PIXELS" {
		t.Errorf("copy = %q", got)
	}
}

func TestSet_SyncIOErrorFailsPick(t *testing.T) {
	eio := func(*os.File) error { return unix.EIO }
	for _, tc := range []struct {
		name  string
		hooks safefs.Hooks
	}{
		{"file-sync", safefs.Hooks{SyncFile: eio}},
		{"dir-sync", safefs.Hooks{SyncDir: eio}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, "picks")
			id := e.addFile(t, "a.jpg", "PIXELS")
			traceWith(t, tc.hooks)

			_, err := e.svc.Set(context.Background(), id, StatePicked)
			if !errors.Is(err, unix.EIO) {
				t.Fatalf("Set picked = %v, want EIO", err)
			}
			if st, _ := e.pickRow(t, id); st != "" {
				t.Errorf("state = %q, want unchanged (no row)", st)
			}
			if got := regularFilesUnder(t, e.root, "picks"); len(got) != 0 {
				t.Errorf("files left under picks/: %q", got)
			}
		})
	}
}

func TestSet_UnpickUnderLegacyDir(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	writeFile(t, e.root, "exports/out/a.jpg", "PIXELS")
	e.setRow(t, id, "picked", "exports/out/a.jpg")

	if _, err := e.svc.Set(context.Background(), id, StateNone); err != nil {
		t.Fatalf("Set unmarked: %v", err)
	}
	if exists(e.root, "exports/out/a.jpg") {
		t.Error("legacy copy exports/out/a.jpg was not removed")
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("state = %q, want no row", st)
	}
	if got := readFile(t, e.root, "a.jpg"); got != "PIXELS" {
		t.Errorf("original = %q", got)
	}
}

func TestSet_UnpickLeavesSymlinkedCopy(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	if err := os.Mkdir(filepath.Join(e.root, "picks"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../a.jpg", filepath.Join(e.root, "picks", "a.jpg")); err != nil {
		t.Fatal(err)
	}
	e.setRow(t, id, "picked", "picks/a.jpg")

	if _, err := e.svc.Set(context.Background(), id, StateNone); err != nil {
		t.Fatalf("Set unmarked = %v, want the state change to succeed", err)
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("state = %q, want no row", st)
	}
	if target, err := os.Readlink(filepath.Join(e.root, "picks", "a.jpg")); err != nil || target != "../a.jpg" {
		t.Errorf("symlink = %q, %v; want left alone", target, err)
	}
	if got := readFile(t, e.root, "a.jpg"); got != "PIXELS" {
		t.Errorf("original = %q, want untouched", got)
	}
}

func TestSet_UnpickNeverRemovesAnOriginal(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	e.addFile(t, "shoot1/a.jpg", "PIXELS") // a scanned original
	e.setRow(t, id, "picked", "shoot1/a.jpg")

	if _, err := e.svc.Set(context.Background(), id, StateRejected); err != nil {
		t.Fatalf("Set rejected: %v", err)
	}
	if st, _ := e.pickRow(t, id); st != "rejected" {
		t.Errorf("state = %q, want rejected", st)
	}
	if got := readFile(t, e.root, "shoot1/a.jpg"); got != "PIXELS" {
		t.Errorf("original shoot1/a.jpg = %q, want untouched", got)
	}
}

// Finding 1: a 'missing' row still names an original — it may have been
// restored before the next scan — so the path is never removed.
func TestSet_UnpickNeverRemovesAMissingOriginal(t *testing.T) {
	t.Run("restored original at the recorded path", func(t *testing.T) {
		e := newEnv(t, "picks")
		id := e.addFile(t, "a.jpg", "PIXELS")
		orig := e.addFile(t, "shoot1/a.jpg", "PIXELS")
		if err := e.st.Files().UpdateStatus(orig, "missing", ""); err != nil {
			t.Fatal(err)
		}
		e.setRow(t, id, "picked", "shoot1/a.jpg")
		if err := e.unpick(id); err != nil {
			t.Fatalf("Unpick: %v", err)
		}
		if st, _ := e.pickRow(t, id); st != "" {
			t.Errorf("state = %q, want no row", st)
		}
		if got := readFile(t, e.root, "shoot1/a.jpg"); got != "PIXELS" {
			t.Errorf("original = %q, want untouched", got)
		}
	})
	// v1 with picks_dir "." recorded the original's own path as its export.
	for _, status := range []string{"analyzed", "missing"} {
		t.Run("nested v1 self-export, "+status, func(t *testing.T) {
			e := newEnv(t, "picks")
			id := e.addFile(t, "Day1/x.jpg", "PIXELS")
			if err := e.st.Files().UpdateStatus(id, status, ""); err != nil {
				t.Fatal(err)
			}
			e.setRow(t, id, "picked", "Day1/x.jpg")
			if err := e.reject(id); err != nil {
				t.Fatalf("Reject: %v", err)
			}
			if st, _ := e.pickRow(t, id); st != "rejected" {
				t.Errorf("state = %q, want rejected", st)
			}
			if got := readFile(t, e.root, "Day1/x.jpg"); got != "PIXELS" {
				t.Errorf("original = %q, want untouched", got)
			}
		})
	}
}

// Finding 1: a failing lookup keeps the copy (fail closed).
func TestRemovable_LookupFailureKeepsCopy(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	tx, err := e.st.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if e.svc.removable(tx, "picks/a.jpg", id) {
		t.Fatal("removable = true with a failing lookup, want false")
	}
}

// Finding 2: a copy another pick row still records is kept until the last
// reference goes, for unpick and for reject.
func TestSet_SharedCopyKeptUntilLastReference(t *testing.T) {
	e := newEnv(t, "picks")
	a := e.addFile(t, "a.jpg", "SAME")
	b := e.addFile(t, "b/a.jpg", "SAME")
	writeFile(t, e.root, "picks/a.jpg", "SAME")
	e.setRow(t, a, "picked", "picks/a.jpg")
	e.setRow(t, b, "picked", "picks/A.jpg")

	if err := e.unpick(a); err != nil {
		t.Fatalf("Unpick a: %v", err)
	}
	if !exists(e.root, "picks/a.jpg") {
		t.Fatal("unpick removed a copy another pick still records")
	}
	if err := e.reject(b); err != nil {
		t.Fatalf("Reject b: %v", err)
	}
	if exists(e.root, "picks/a.jpg") {
		t.Error("copy not removed once no pick records it")
	}

	// And the other way round: reject first, unpick last.
	writeFile(t, e.root, "picks/a.jpg", "SAME")
	e.setRow(t, a, "picked", "picks/a.jpg")
	e.setRow(t, b, "picked", "picks/a.jpg")
	if err := e.reject(a); err != nil {
		t.Fatalf("Reject a: %v", err)
	}
	if !exists(e.root, "picks/a.jpg") {
		t.Fatal("reject removed a copy another pick still records")
	}
	if err := e.unpick(b); err != nil {
		t.Fatalf("Unpick b: %v", err)
	}
	if exists(e.root, "picks/a.jpg") {
		t.Error("copy not removed once no pick records it")
	}
}

// Finding 4: a reused identical copy is synced before the commit; a real
// I/O error fails the pick and leaves both the state and the file alone.
func TestSet_ReusedCopySyncErrors(t *testing.T) {
	eio := func(*os.File) error { return unix.EIO }
	notSup := func(*os.File) error { return unix.ENOTSUP }
	for _, tc := range []struct {
		name    string
		hooks   safefs.Hooks
		wantErr error
	}{
		{"file-sync EIO", safefs.Hooks{SyncFile: eio}, unix.EIO},
		{"dir-sync EIO", safefs.Hooks{SyncDir: eio}, unix.EIO},
		{"not supported", safefs.Hooks{SyncFile: notSup, SyncDir: notSup}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, "picks")
			writeFile(t, e.root, "picks/a.jpg", "SAME")
			id := e.addFile(t, "a.jpg", "SAME")
			traceWith(t, tc.hooks)

			_, err := e.svc.Set(context.Background(), id, StatePicked)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Set picked: %v", err)
				}
				if st, exp := e.pickRow(t, id); st != "picked" || exp != "picks/a.jpg" {
					t.Errorf("row = (%q, %q)", st, exp)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Set picked = %v, want %v", err, tc.wantErr)
			}
			if st, _ := e.pickRow(t, id); st != "" {
				t.Errorf("state = %q, want unchanged", st)
			}
			if got := readFile(t, e.root, "picks/a.jpg"); got != "SAME" {
				t.Errorf("reused file = %q, want left in place", got)
			}
		})
	}
}

// Finding 6: the comparison with an existing same-sized file stops when the
// context is cancelled.
func TestSet_CancelledDuringComparison(t *testing.T) {
	e := newEnv(t, "picks")
	big := strings.Repeat("v", 1<<20)
	writeFile(t, e.root, "picks/clip.mov", big)
	id := e.addFile(t, "clip.mov", big)
	rec := traceWith(t, safefs.Hooks{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := e.svc.Set(ctx, id, StatePicked)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Set picked = %v, want context.Canceled", err)
	}
	// The comparison itself stopped: nothing after it (sync, commit) ran.
	if got := rec.take(); len(got) != 0 {
		t.Errorf("trace = %q, want nothing after the cancelled comparison", got)
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("state = %q, want unchanged", st)
	}
	if got := regularFilesUnder(t, e.root, "picks"); len(got) != 1 {
		t.Errorf("picks/ holds %q, want only the existing file", got)
	}
}

// Finding 7: a FIFO at the target name never blocks the pick; it is an
// occupied name.
func TestSet_FIFOAtTargetIsSkipped(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "MINE")
	if err := os.Mkdir(filepath.Join(e.root, "picks"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(e.root, "picks", "a.jpg"), 0o600); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := e.svc.Set(context.Background(), id, StatePicked)
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("Set picked: %v", o.err)
		}
		if o.res.ExportedPath != "picks/a_2.jpg" {
			t.Errorf("exported = %q, want picks/a_2.jpg", o.res.ExportedPath)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pick blocked on a FIFO at the target name")
	}
}

func TestSet_UnknownTargetRefused(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "PIXELS")
	if _, err := e.svc.Set(context.Background(), id, State("favourite")); err == nil {
		t.Fatal("Set with an unknown state: want an error")
	}
}

// --- v1 behaviour kept, through the wrappers ---------------------------------

func TestPickRejectTransitions(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "Day1/IMG_0001.JPG", "pixels")

	if err := e.pick(id); err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if st, _ := e.pickRow(t, id); st != string(StatePicked) {
		t.Errorf("state=%q want %q", st, StatePicked)
	}
	if err := e.pick(id); err != nil {
		t.Fatalf("Pick second call: %v", err)
	}
	if err := e.reject(id); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if st, _ := e.pickRow(t, id); st != string(StateRejected) {
		t.Errorf("after Reject state=%q want %q", st, StateRejected)
	}
	// Unpick of a rejected file is a no-op (v1).
	if err := e.unpick(id); err != nil {
		t.Fatalf("Unpick of rejected: %v", err)
	}
	if st, _ := e.pickRow(t, id); st != string(StateRejected) {
		t.Errorf("Unpick changed a rejection: state=%q", st)
	}
	if err := e.unreject(id); err != nil {
		t.Fatalf("Unreject: %v", err)
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("after Unreject state=%q, want no row", st)
	}
	// Unreject of an unmarked file is a no-op.
	if err := e.unreject(id); err != nil {
		t.Fatalf("Unreject of unmarked: %v", err)
	}
}

func TestUnrejectOfPickedIsNoop(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "a.jpg", "pixels")
	if err := e.pick(id); err != nil {
		t.Fatal(err)
	}
	if err := e.unreject(id); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.pickRow(t, id); st != "picked" {
		t.Errorf("state = %q, want picked", st)
	}
	if !exists(e.root, "picks/a.jpg") {
		t.Error("copy removed by Unreject")
	}
}

func TestPickMissingFile(t *testing.T) {
	e := newEnv(t, "picks")
	if err := e.pick(99999); err == nil {
		t.Fatal("Pick on nonexistent file id should error")
	}
}

func TestUnpickRemovesExported(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "x.jpg", "data")
	if err := e.pick(id); err != nil {
		t.Fatal(err)
	}
	if !exists(e.root, "picks/x.jpg") {
		t.Fatal("expected picks/x.jpg before unpick")
	}
	if err := e.unpick(id); err != nil {
		t.Fatalf("Unpick: %v", err)
	}
	if exists(e.root, "picks/x.jpg") {
		t.Error("picks/x.jpg still present after Unpick")
	}
}

func TestUnpickWithMissingCopy(t *testing.T) {
	e := newEnv(t, "picks")
	id := e.addFile(t, "z.jpg", "z")
	e.setRow(t, id, "picked", "picks/ghost.jpg")
	if err := e.unpick(id); err != nil {
		t.Fatalf("Unpick with the copy already gone: %v", err)
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("state = %q, want no row", st)
	}
}

func TestPickTraversalPicksDirRejected(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	e := newEnvAt(t, root, "../escaped")
	id := e.addFile(t, "img.jpg", "data")

	var de *DirError
	if err := e.pick(id); !errors.As(err, &de) || de.Reason != "traversal" {
		t.Fatalf("Pick with picks dir ../escaped = %v, want DirError traversal", err)
	}
	if exists(parent, "escaped") {
		t.Error("traversal directory created outside the scan root")
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("state = %q, want unchanged", st)
	}
}

func TestUnpickTraversalExportedPathLeavesOutsideFile(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	e := newEnvAt(t, root, "picks")
	id := e.addFile(t, "z.jpg", "z")
	writeFile(t, parent, "escaped2/z.jpg", "sensitive")
	e.setRow(t, id, "picked", "../escaped2/z.jpg")

	if err := e.unpick(id); err != nil {
		t.Fatalf("Unpick = %v, want the state change to succeed", err)
	}
	if st, _ := e.pickRow(t, id); st != "" {
		t.Errorf("state = %q, want no row", st)
	}
	if got := readFile(t, parent, "escaped2/z.jpg"); got != "sensitive" {
		t.Errorf("file outside the scan root = %q, want untouched", got)
	}
}

func TestPickAbsolutePicksDirRejected(t *testing.T) {
	e := newEnv(t, "/tmp/out")
	id := e.addFile(t, "img.jpg", "data")
	var de *DirError
	if err := e.pick(id); !errors.As(err, &de) || de.Reason != "absolute" {
		t.Fatalf("Pick = %v, want DirError absolute", err)
	}
	if got := snapshot(t, e.root); len(got) != 1 || !strings.HasPrefix(got["img.jpg"], "data") {
		t.Errorf("tree = %q, want only img.jpg", got)
	}
}

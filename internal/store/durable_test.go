package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// pragmaInt reads an integer PRAGMA through q (a *sql.DB, *sql.Conn or *sql.Tx).
func pragmaInt(t *testing.T, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, name string) int {
	t.Helper()
	var v int
	if err := q.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

func insertTestFile(t *testing.T, s *Store, path string) int64 {
	t.Helper()
	id, err := s.Files().Insert(File{
		Path: path, Size: 1, Mtime: 1, ContentHash: "h-" + path,
		Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
	})
	if err != nil {
		t.Fatalf("insert %s: %v", path, err)
	}
	return id
}

func TestDurable_RunsWithFullSync(t *testing.T) {
	s := newTestStore(t)
	id := insertTestFile(t, s, "a.jpg")

	err := s.Durable(context.Background(), func(tx *sql.Tx) error {
		if got := pragmaInt(t, tx, "synchronous"); got != 2 {
			t.Errorf("inside fn: synchronous = %d, want 2 (FULL)", got)
		}
		if got := pragmaInt(t, tx, "fullfsync"); got != 1 {
			t.Errorf("inside fn: fullfsync = %d, want 1", got)
		}
		return s.Picks().SetTx(tx, id, "picked", "picks/a.jpg")
	})
	if err != nil {
		t.Fatalf("Durable: %v", err)
	}
	if got := pragmaInt(t, s.db, "synchronous"); got != 1 {
		t.Errorf("after: synchronous = %d, want 1 (NORMAL)", got)
	}
	if got := pragmaInt(t, s.db, "fullfsync"); got != 0 {
		t.Errorf("after: fullfsync = %d, want 0", got)
	}
	p, err := s.Picks().Get(id)
	if err != nil || p.State != "picked" || p.ExportedPath.String != "picks/a.jpg" {
		t.Errorf("pick row = %+v, err %v; want picked at picks/a.jpg", p, err)
	}
}

func TestDurable_RestoresAfterError(t *testing.T) {
	s := newTestStore(t)
	id := insertTestFile(t, s, "a.jpg")
	boom := errors.New("boom")

	err := s.Durable(context.Background(), func(tx *sql.Tx) error {
		if err := s.Picks().SetTx(tx, id, "picked", "picks/a.jpg"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Durable error = %v, want %v", err, boom)
	}
	if _, err := s.Picks().Get(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("pick row after failed fn: err = %v, want ErrNotFound (rolled back)", err)
	}
	if got := pragmaInt(t, s.db, "synchronous"); got != 1 {
		t.Errorf("synchronous = %d, want 1", got)
	}
	if got := pragmaInt(t, s.db, "fullfsync"); got != 0 {
		t.Errorf("fullfsync = %d, want 0", got)
	}
}

func TestDurable_RestoresWhenContextIsCancelledInsideFn(t *testing.T) {
	s := newTestStore(t)
	id := insertTestFile(t, s, "a.jpg")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := s.Durable(ctx, func(tx *sql.Tx) error {
		if err := s.Picks().SetTx(tx, id, "picked", "picks/a.jpg"); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Durable error = %v, want context.Canceled", err)
	}
	if got := pragmaInt(t, s.db, "synchronous"); got != 1 {
		t.Errorf("synchronous = %d, want 1", got)
	}
	if got := pragmaInt(t, s.db, "fullfsync"); got != 0 {
		t.Errorf("fullfsync = %d, want 0", got)
	}
	if _, err := s.Picks().Get(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("pick row: err = %v, want ErrNotFound (rolled back)", err)
	}
}

func TestDurable_CleanupFailureAfterCommitReturnsNil(t *testing.T) {
	s := newTestStore(t)
	id := insertTestFile(t, s, "a.jpg")

	prev := restoreSettings
	restoreSettings = func(context.Context, *sql.Conn) error { return errors.New("injected restore failure") }
	t.Cleanup(func() { restoreSettings = prev })

	err := s.Durable(context.Background(), func(tx *sql.Tx) error {
		return s.Picks().SetTx(tx, id, "picked", "picks/a.jpg")
	})
	if err != nil {
		t.Fatalf("Durable = %v, want nil after a successful commit", err)
	}
	p, err := s.Picks().Get(id)
	if err != nil || p.State != "picked" {
		t.Fatalf("pick row = %+v, err %v; want committed", p, err)
	}
	// The injected restore left synchronous=FULL on the old connection, so a
	// reading of NORMAL proves the next statement ran on a fresh connection.
	if got := pragmaInt(t, s.db, "synchronous"); got != 1 {
		t.Errorf("synchronous = %d, want 1 (fresh connection)", got)
	}
	if got := pragmaInt(t, s.db, "fullfsync"); got != 0 {
		t.Errorf("fullfsync = %d, want 0 (fresh connection)", got)
	}
	if got := pragmaInt(t, s.db, "busy_timeout"); got != 10000 {
		t.Errorf("busy_timeout = %d, want 10000", got)
	}
	if got := pragmaInt(t, s.db, "foreign_keys"); got != 1 {
		t.Errorf("foreign_keys = %d, want 1", got)
	}
}

func TestDurable_SerialisesWithOtherWriters(t *testing.T) {
	s := newTestStore(t)
	ids := make([]int64, 20)
	for i := range ids {
		ids[i] = insertTestFile(t, s, fmt.Sprintf("f%02d.jpg", i))
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(ids))
	for i, id := range ids {
		wg.Go(func() {
			if i%2 == 0 {
				errs <- s.Durable(context.Background(), func(tx *sql.Tx) error {
					return s.Picks().SetTx(tx, id, "rejected", "")
				})
				return
			}
			errs <- s.Files().UpdateStatus(id, "thumbed", "")
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			if strings.Contains(err.Error(), "database is locked") {
				t.Errorf("writer hit %v", err)
			} else {
				t.Errorf("writer: %v", err)
			}
		}
	}
	if n, _ := s.Picks().CountState("rejected"); n != 10 {
		t.Errorf("rejected rows = %d, want 10", n)
	}
	if n, _ := s.Files().CountByStatus("thumbed"); n != 10 {
		t.Errorf("thumbed rows = %d, want 10", n)
	}
}

func TestOpen_PragmasComeFromDSN(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Mark the first connection, then discard it so the pool has to open a
	// second one.
	c, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	if _, err := c.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	_ = c.Raw(func(any) error { return driver.ErrBadConn })
	_ = c.Close()

	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	if got := pragmaInt(t, s.db, "foreign_keys"); got != 1 {
		t.Errorf("foreign_keys = %d, want 1", got)
	}
	if got := pragmaInt(t, s.db, "busy_timeout"); got != 10000 {
		t.Errorf("busy_timeout = %d, want 10000", got)
	}
	if got := pragmaInt(t, s.db, "synchronous"); got != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL)", got)
	}
}

func TestPicksSetTx_EmptyStateDeletes(t *testing.T) {
	s := newTestStore(t)
	id := insertTestFile(t, s, "a.jpg")
	set := func(state, exported string) {
		t.Helper()
		if err := s.Durable(context.Background(), func(tx *sql.Tx) error {
			return s.Picks().SetTx(tx, id, state, exported)
		}); err != nil {
			t.Fatalf("SetTx(%q, %q): %v", state, exported, err)
		}
	}

	set("picked", "picks/a.jpg")
	set("rejected", "")
	p, err := s.Picks().Get(id)
	if err != nil || p.State != "rejected" || p.ExportedPath.Valid {
		t.Errorf("after reject: %+v err %v; want rejected with NULL exported_path", p, err)
	}
	set("", "")
	if _, err := s.Picks().Get(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("after clear: err = %v, want ErrNotFound", err)
	}
}

// inTx runs fn in a durable transaction and fails the test on error.
func inTx(t *testing.T, s *Store, fn func(tx *sql.Tx) error) {
	t.Helper()
	if err := s.Durable(context.Background(), fn); err != nil {
		t.Fatalf("Durable: %v", err)
	}
}

func TestPathKnownTx_AnyStatus(t *testing.T) {
	s := newTestStore(t)
	insertTestFile(t, s, "Day1/a.jpg")
	gone := insertTestFile(t, s, "Day1/b.jpg")
	if err := s.Files().UpdateStatus(gone, "missing", ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"Day1/a.jpg", true},
		{"day1/A.JPG", true}, // case-insensitive, like file identity
		{"Day1/b.jpg", true}, // a missing row still names an original
		{"Day1/c.jpg", false},
	} {
		inTx(t, s, func(tx *sql.Tx) error {
			got, err := s.Files().PathKnownTx(tx, tc.path)
			if err != nil {
				return err
			}
			if got != tc.want {
				t.Errorf("PathKnownTx(%q) = %v, want %v", tc.path, got, tc.want)
			}
			return nil
		})
	}
}

func TestExportReferencedTx(t *testing.T) {
	s := newTestStore(t)
	a := insertTestFile(t, s, "a.jpg")
	b := insertTestFile(t, s, "b.jpg")
	c := insertTestFile(t, s, "c.jpg")
	inTx(t, s, func(tx *sql.Tx) error {
		if err := s.Picks().SetTx(tx, a, "picked", "picks/a.jpg"); err != nil {
			return err
		}
		if err := s.Picks().SetTx(tx, b, "picked", "/Picks/A.jpg"); err != nil { // v1 absolute form, other case
			return err
		}
		return s.Picks().SetTx(tx, c, "picked", "picks/c.jpg")
	})
	for _, tc := range []struct {
		path   string
		except int64
		want   bool
	}{
		{"picks/a.jpg", a, true}, // b refers to it too
		{"picks/a.jpg", b, true},
		{"picks/c.jpg", c, false},
		{"picks/c.jpg", a, true},
		{"picks/x.jpg", a, false},
	} {
		inTx(t, s, func(tx *sql.Tx) error {
			got, err := s.Picks().ExportReferencedTx(tx, tc.path, tc.except)
			if err != nil {
				return err
			}
			if got != tc.want {
				t.Errorf("ExportReferencedTx(%q, except %d) = %v, want %v", tc.path, tc.except, got, tc.want)
			}
			return nil
		})
	}
}

func TestExportDirs_Derives(t *testing.T) {
	cases := []struct {
		file, exported, want string
	}{
		{"a/b.jpg", "picks/a/b.jpg", "picks"},
		{"c.jpg", "exports/out/c.jpg", "exports/out"},
		{"a/b.jpg", "/Users/tester/out/a/b.jpg", "Users/tester/out"},
		{"a/b.jpg", "picks/a/b_2.jpg", "picks"},
	}
	for _, tc := range cases {
		t.Run(tc.exported, func(t *testing.T) {
			s := newTestStore(t)
			id := insertTestFile(t, s, tc.file)
			if err := s.Picks().SetState(id, "picked"); err != nil {
				t.Fatal(err)
			}
			if err := s.Picks().SetExportedPath(id, tc.exported); err != nil {
				t.Fatal(err)
			}
			got, err := s.Picks().ExportDirs()
			if err != nil {
				t.Fatalf("ExportDirs: %v", err)
			}
			want := []ExportDir{{Dir: tc.want, Count: 1}}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("ExportDirs = %+v, want %+v", got, want)
			}
		})
	}
}

func TestExportDirs_MostUsedFirst(t *testing.T) {
	s := newTestStore(t)
	rows := map[string]string{
		"a.jpg":   "exports/out/a.jpg",
		"b.jpg":   "picks/b.jpg",
		"d/c.jpg": "picks/d/c.jpg",
		"e.jpg":   "",
	}
	for file, exported := range rows {
		id := insertTestFile(t, s, file)
		if err := s.Picks().SetState(id, "picked"); err != nil {
			t.Fatal(err)
		}
		if exported != "" {
			if err := s.Picks().SetExportedPath(id, exported); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := s.Picks().ExportDirs()
	if err != nil {
		t.Fatalf("ExportDirs: %v", err)
	}
	want := []ExportDir{{Dir: "picks", Count: 2}, {Dir: "exports/out", Count: 1}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("ExportDirs = %+v, want %+v", got, want)
	}
}

func TestCountUnderPath(t *testing.T) {
	s := newTestStore(t)
	for _, p := range []string{"shoot1/a.jpg", "shoot1/x/b.jpg", "shoot1/c.jpg", "shoot10/d.jpg", "shootX1/e.jpg"} {
		insertTestFile(t, s, p)
	}
	missing, err := s.Files().GetByPath("shoot1/c.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Files().UpdateStatus(missing.ID, "missing", ""); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		dir  string
		want int
	}{
		{"shoot1", 2},
		{"Shoot1", 2}, // case-insensitive, like file identity
		{"shoot10", 1},
		{"shoot_1", 0}, // LIKE wildcards are not honoured
		{"shoot%", 0},
		{"nothing", 0},
	} {
		got, err := s.Files().CountUnderPath(tc.dir)
		if err != nil {
			t.Fatalf("CountUnderPath(%q): %v", tc.dir, err)
		}
		if got != tc.want {
			t.Errorf("CountUnderPath(%q) = %d, want %d", tc.dir, got, tc.want)
		}
	}
}

//go:build darwin

package workspace

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dispatched is one call that reached dispatch.
type dispatched struct {
	op  operation
	url string
}

// recorder replaces dispatch for one test, so no Finder window or browser
// tab ever opens. block, when set, stands for the AppKit call: it runs
// inside the replaced dispatch before that returns.
type recorder struct {
	mu    sync.Mutex
	calls []dispatched
	block func()
}

func useRecorder(t *testing.T, block func()) *recorder {
	t.Helper()
	r := &recorder{block: block}
	old := dispatch
	dispatch = func(op operation, rawURL string) error {
		r.mu.Lock()
		r.calls = append(r.calls, dispatched{op: op, url: rawURL})
		r.mu.Unlock()
		if r.block != nil {
			r.block()
		}
		return nil
	}
	t.Cleanup(func() { dispatch = old })
	return r
}

func (r *recorder) recorded() []dispatched {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]dispatched(nil), r.calls...)
}

// pathOf returns the local path a recorded file URL names.
func pathOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	if u.Scheme != "file" || u.Host != "" {
		t.Fatalf("%q is not a local file URL", rawURL)
	}
	return u.Path
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReveal_PassesFileURL(t *testing.T) {
	rec := useRecorder(t, nil)
	dir := t.TempDir()
	file := filepath.Join(dir, "Day #2 ?50% é.jpg")
	writeFile(t, file)

	if err := New().Reveal(file); err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	calls := rec.recorded()
	if len(calls) != 1 {
		t.Fatalf("dispatched %d calls, want 1", len(calls))
	}
	if calls[0].op != opReveal {
		t.Errorf("op = %v, want opReveal", calls[0].op)
	}
	if got := pathOf(t, calls[0].url); got != file {
		t.Errorf("URL %q names %q, want %q", calls[0].url, got, file)
	}
	// Cocoa reads the URL back as the same path.
	got, err := nsURLPathForTest(calls[0].url)
	if err != nil {
		t.Fatalf("NSURL: %v", err)
	}
	if got != file {
		t.Errorf("NSURL path = %q, want %q", got, file)
	}
}

func TestReveal_Directory(t *testing.T) {
	rec := useRecorder(t, nil)
	dir := filepath.Join(t.TempDir(), "Trip")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := New().Reveal(dir); err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	calls := rec.recorded()
	if len(calls) != 1 || calls[0].op != opReveal {
		t.Fatalf("calls = %+v, want one reveal", calls)
	}
	if !strings.HasSuffix(calls[0].url, "/") {
		t.Errorf("directory URL %q has no trailing slash", calls[0].url)
	}
	if got := filepath.Clean(pathOf(t, calls[0].url)); got != dir {
		t.Errorf("URL names %q, want %q", got, dir)
	}
}

func TestReveal_MissingPath(t *testing.T) {
	rec := useRecorder(t, nil)
	missing := filepath.Join(t.TempDir(), "gone.jpg")
	if err := New().Reveal(missing); err == nil {
		t.Fatal("Reveal of a missing path succeeded")
	}
	if calls := rec.recorded(); len(calls) != 0 {
		t.Fatalf("dispatched %+v, want nothing", calls)
	}
}

func TestReveal_RelativePath(t *testing.T) {
	rec := useRecorder(t, nil)
	if err := New().Reveal("relative/x.jpg"); err == nil {
		t.Fatal("Reveal of a relative path succeeded")
	}
	if calls := rec.recorded(); len(calls) != 0 {
		t.Fatalf("dispatched %+v, want nothing", calls)
	}
}

func TestOpenFolder_PassesDirectoryURL(t *testing.T) {
	rec := useRecorder(t, nil)
	dir := filepath.Join(t.TempDir(), "Day #2")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := New().OpenFolder(dir); err != nil {
		t.Fatalf("OpenFolder: %v", err)
	}
	calls := rec.recorded()
	if len(calls) != 1 || calls[0].op != opOpen {
		t.Fatalf("calls = %+v, want one open", calls)
	}
	if !strings.HasSuffix(calls[0].url, "/") {
		t.Errorf("directory URL %q has no trailing slash", calls[0].url)
	}
	got, err := nsURLPathForTest(calls[0].url)
	if err != nil {
		t.Fatalf("NSURL: %v", err)
	}
	if got != dir {
		t.Errorf("NSURL path = %q, want %q", got, dir)
	}
}

func TestOpenFolder_RejectsFile(t *testing.T) {
	rec := useRecorder(t, nil)
	file := filepath.Join(t.TempDir(), "a.jpg")
	writeFile(t, file)
	if err := New().OpenFolder(file); err == nil {
		t.Fatal("OpenFolder of a file succeeded")
	}
	if err := New().OpenFolder(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("OpenFolder of a missing path succeeded")
	}
	if calls := rec.recorded(); len(calls) != 0 {
		t.Fatalf("dispatched %+v, want nothing", calls)
	}
}

func TestReveal_ReturnsOnlyAfterDispatchedBlockRan(t *testing.T) {
	const hold = 200 * time.Millisecond
	var ran atomic.Bool
	useRecorder(t, func() {
		time.Sleep(hold)
		ran.Store(true)
	})
	file := filepath.Join(t.TempDir(), "a.jpg")
	writeFile(t, file)

	start := time.Now()
	if err := New().Reveal(file); err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if elapsed := time.Since(start); elapsed < hold {
		t.Errorf("Reveal returned after %v, before the %v block finished", elapsed, hold)
	}
	if !ran.Load() {
		t.Error("Reveal returned before its block ran")
	}
}

func TestReveal_NoEarlyReturnWhenBlockIsDelayed(t *testing.T) {
	const hold = 6 * time.Second // longer than any old timeout
	var returned atomic.Bool
	type observation struct {
		returned bool
		at       time.Time
	}
	ranAt := make(chan observation, 1)
	useRecorder(t, func() {
		time.Sleep(hold)
		ranAt <- observation{returned: returned.Load(), at: time.Now()}
	})
	file := filepath.Join(t.TempDir(), "a.jpg")
	writeFile(t, file)

	type outcome struct {
		err error
		at  time.Time
	}
	done := make(chan outcome, 1)
	go func() {
		err := New().Reveal(file)
		returned.Store(true)
		done <- outcome{err: err, at: time.Now()}
	}()

	var obs observation
	select {
	case obs = <-ranAt:
	case <-time.After(hold + 30*time.Second):
		t.Fatal("the block never ran")
	}
	if obs.returned {
		t.Fatal("Reveal had returned before its block ran")
	}
	var out outcome
	select {
	case out = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Reveal did not return after its block ran")
	}
	if out.err != nil {
		t.Fatalf("Reveal: %v", out.err)
	}
	if lag := out.at.Sub(obs.at); lag > time.Second {
		t.Errorf("Reveal returned %v after its block ran, want right after", lag)
	}
}

func TestOpenURL_HTTPSOnly(t *testing.T) {
	rec := useRecorder(t, nil)
	ws := New()
	for _, bad := range []string{
		"http://x",
		"file:///etc/passwd",
		"javascript:1",
		"https:opaque",
		"https:///no-host",
		"",
	} {
		if err := ws.OpenURL(bad); err == nil {
			t.Errorf("OpenURL(%q) succeeded", bad)
		}
	}
	if calls := rec.recorded(); len(calls) != 0 {
		t.Fatalf("dispatched %+v for refused URLs, want nothing", calls)
	}

	const good = "https://github.com/volodymyrsmirnov/choix"
	if err := ws.OpenURL(good); err != nil {
		t.Fatalf("OpenURL(%q): %v", good, err)
	}
	calls := rec.recorded()
	if len(calls) != 1 {
		t.Fatalf("dispatched %d calls, want 1", len(calls))
	}
	if calls[0].op != opOpen || calls[0].url != good {
		t.Errorf("dispatched %+v, want open %q", calls[0], good)
	}
}

func TestDispatchError_IsReturned(t *testing.T) {
	old := dispatch
	t.Cleanup(func() { dispatch = old })
	failure := errors.New("NSWorkspace did not open it")
	dispatch = func(operation, string) error { return failure }

	if err := New().OpenURL("https://github.com/volodymyrsmirnov/choix"); !errors.Is(err, failure) {
		t.Fatalf("OpenURL err = %v, want the dispatch failure", err)
	}
	dir := t.TempDir()
	if err := New().OpenFolder(dir); !errors.Is(err, failure) {
		t.Fatalf("OpenFolder err = %v, want the dispatch failure", err)
	}
	if err := New().Reveal(dir); !errors.Is(err, failure) {
		t.Fatalf("Reveal err = %v, want the dispatch failure", err)
	}
}

func TestNative_ExceptionBecomesError(t *testing.T) {
	err := raiseForTest()
	if err == nil {
		t.Fatal("a raised NSException produced no error")
	}
	if !strings.Contains(err.Error(), "ChoixTestException") {
		t.Errorf("err = %v, want the exception name", err)
	}
}

func TestNative_InvalidURLIsError(t *testing.T) {
	if _, err := nsURLPathForTest("\xff"); err == nil {
		t.Fatal("invalid UTF-8 produced an NSURL")
	}
}

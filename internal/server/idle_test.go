package server

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

func TestIdleWatcherShutsDownAfterTimeout(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "test.db") + "?_pragma=journal_mode(WAL)"
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv, err := New(Config{
		Store:     st,
		ScanRoot:  dir,
		IdleAfter: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}

	// Don't override Now; rely on real wall clock for the short timeout.
	done := make(chan error, 1)
	go func() { done <- srv.Run(context.Background()) }()

	// Hit one endpoint to set lastReq, then wait for idle shutdown.
	resp, err := http.Get(srv.URL() + "/api/library")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle shutdown did not fire within 2s of 100ms timeout")
	}
}

func TestProgressDoesNotResetIdleTimer(t *testing.T) {
	env := newTestServer(t)
	env.server.cfg.IdleAfter = 200 * time.Millisecond
	frozen := time.Now()
	env.server.Now = func() time.Time { return frozen }

	// First request: lastReq = frozen.
	env.get("/api/library").Body.Close()
	beforeProgress := env.server.lastReq

	// SSE GET — must NOT reset lastReq.
	env.get("/api/progress").Body.Close()
	if env.server.lastReq != beforeProgress {
		t.Errorf("lastReq advanced on /api/progress")
	}

	// Other request — must reset.
	frozen = frozen.Add(time.Second)
	env.server.Now = func() time.Time { return frozen }
	env.get("/api/library").Body.Close()
	if !env.server.lastReq.Equal(frozen) {
		t.Errorf("lastReq = %v, want %v", env.server.lastReq, frozen)
	}
}

// While the quality model downloads or photos are rated, an open tab polls
// /api/quality every second and refreshes the Library by itself. Those
// requests carry BackgroundRequestHeader and must not postpone idle
// shutdown; the same endpoints without the header (a real page load) do.
func TestBackgroundRequestsDoNotResetIdleTimer(t *testing.T) {
	env := newTestServer(t)
	frozen := time.Now()
	env.server.Now = func() time.Time { return frozen }
	env.get("/api/library").Body.Close()
	before := env.server.lastReq

	frozen = frozen.Add(time.Minute)
	for _, path := range []string{"/api/quality", "/api/library", "/api/clusters/1"} {
		req, err := http.NewRequest(http.MethodGet, env.httptest.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(BackgroundRequestHeader, "1")
		resp, err := env.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if env.server.lastReq != before {
			t.Fatalf("background GET %s reset the idle timer", path)
		}
	}

	env.get("/api/quality").Body.Close()
	if !env.server.lastReq.Equal(frozen) {
		t.Fatal("a foreground request must still count as activity")
	}
}

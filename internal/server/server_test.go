package server

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

func TestNewRequiresStoreAndScanRoot(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New with empty Config: want error")
	}
	if _, err := New(Config{Store: &store.Store{}}); err == nil {
		t.Fatal("New without ScanRoot: want error")
	}
}

func TestServerStartBindsListener(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "test.db") + "?_pragma=journal_mode(WAL)"
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv, err := New(Config{Store: st, ScanRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if srv.URL() == "" {
		t.Fatal("URL is empty after Start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _ = srv.Run(ctx) }()

	// Wait briefly for serve goroutine to take ownership of the listener.
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(srv.URL() + "/api/library")
	if err != nil {
		t.Fatalf("GET /api/library: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Fatal("library response body empty")
	}
}

func TestServerShutdownReleasesPort(t *testing.T) {
	env := newTestServer(t)
	resp, err := http.Get(env.httptest.URL + "/api/library")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	if err := env.server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

func TestRunUntilSignalExitsOnSIGINT(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "test.db") + "?_pragma=journal_mode(WAL)"
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv, err := New(Config{Store: st, ScanRoot: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- RunUntilSignal(context.Background(), srv) }()

	// Give the server a moment to settle, then verify it's serving.
	time.Sleep(50 * time.Millisecond)
	resp, err := http.Get(srv.URL() + "/api/library")
	if err != nil {
		t.Fatalf("GET pre-signal: %v", err)
	}
	resp.Body.Close()

	// Send ourselves SIGINT.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("kill SIGINT: %v", err)
	}

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("RunUntilSignal: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop after SIGINT")
	}

	// Confirm the listener is gone.
	if _, err := http.Get(srv.URL() + "/api/library"); err == nil {
		t.Error("server still answering after shutdown")
	}
	_ = os.Stderr // silence unused import on some go versions
}

package runtime

import (
	"errors"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
)

// TestKV_RoundTripAndRefusedAfterShutdown: the window's key-value access
// reads and writes library.db's kv table, and once Shutdown has begun it
// refuses without touching the closed database.
func TestKV_RoundTripAndRefusedAfterShutdown(t *testing.T) {
	h := newHarness(t)
	r := h.start(t)
	kv := r.KV()

	if v, ok, err := kv.KVGet("window"); err != nil || ok || v != "" {
		t.Fatalf("KVGet before any save = %q, %v, %v; want absent", v, ok, err)
	}
	if err := kv.KVSet("window", `{"x":1}`); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := kv.KVGet("window"); err != nil || !ok || v != `{"x":1}` {
		t.Fatalf("KVGet = %q, %v, %v; want the saved value", v, ok, err)
	}

	r.Shutdown()
	var e *apperr.Error
	if err := kv.KVSet("window", `{"x":2}`); !errors.As(err, &e) || e.Code != apperr.CodeUnavailable {
		t.Fatalf("KVSet after Shutdown = %v; want unavailable", err)
	}
	if _, _, err := kv.KVGet("window"); !errors.As(err, &e) || e.Code != apperr.CodeUnavailable {
		t.Fatalf("KVGet after Shutdown = %v; want unavailable", err)
	}
	if v, ok, err := openLibrary(t).KVGet("window"); err != nil || !ok || v != `{"x":1}` {
		t.Fatalf("library.db after Shutdown holds %q, %v, %v; want the value saved before it", v, ok, err)
	}
}

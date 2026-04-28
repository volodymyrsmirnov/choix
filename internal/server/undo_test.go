package server

import (
	"net/http"
	"testing"
)

func TestUndoRevertsLastPick(t *testing.T) {
	env := newTestServer(t)
	id := seedFileOnDisk(t, env.store, env.scanRoot, "Day1/A.JPG")

	// Pick the file.
	resp := postJSON(t, env, "/api/picks", map[string]any{"file_id": id, "action": "pick"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pick status %d", resp.StatusCode)
	}

	// Verify it's picked.
	pick, err := env.store.Picks().GetByFileID(id)
	if err != nil {
		t.Fatalf("GetByFileID after pick: %v", err)
	}
	if pick.State != "picked" {
		t.Fatalf("state = %q, want picked", pick.State)
	}

	// Undo.
	resp = postJSON(t, env, "/api/picks/undo", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("undo status %d", resp.StatusCode)
	}

	// Verify reverted: either row is gone (delete-on-unpick) or state is "none"/""
	// Both mean "unmarked". The picks service deletes the row on Unpick, so
	// ErrNotFound is the expected success result.
	pick, err = env.store.Picks().GetByFileID(id)
	if err == nil && pick.State == "picked" {
		t.Errorf("state after undo still = %q, want unmarked (not found or none)", pick.State)
	}
	// err != nil (not found) is the expected success case — row was deleted on undo.
}

func TestUndoAfterRejectFromUnmarkedClearsState(t *testing.T) {
	env := newTestServer(t)
	id := seedFileOnDisk(t, env.store, env.scanRoot, "Day1/C.JPG")

	// Reject an unmarked file (no prior pick row).
	resp := postJSON(t, env, "/api/picks", map[string]any{"file_id": id, "action": "reject"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject status %d", resp.StatusCode)
	}

	// Undo the reject — file must return to unmarked (no row).
	resp = postJSON(t, env, "/api/picks/undo", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("undo status %d", resp.StatusCode)
	}

	// Row must be gone: ErrNotFound is the expected success case.
	_, err := env.store.Picks().GetByFileID(id)
	if err == nil {
		t.Errorf("expected no pick row after undo of first-time reject, but row exists")
	}
}

func TestUndoOnEmptyLogReturns204(t *testing.T) {
	env := newTestServer(t)
	resp := postJSON(t, env, "/api/picks/undo", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status %d, want 204", resp.StatusCode)
	}
}

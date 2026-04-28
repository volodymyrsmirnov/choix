package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPicksPickThenUnpick(t *testing.T) {
	env := newTestServer(t)
	id := seedFileOnDisk(t, env.store, env.scanRoot, "Day1/IMG_0001.JPG")

	resp := postJSON(t, env, "/api/picks", map[string]any{"file_id": id, "action": "pick"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pick status %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["state"] != "picked" {
		t.Errorf("state = %v want picked", got["state"])
	}

	resp2 := postJSON(t, env, "/api/picks", map[string]any{"file_id": id, "action": "unpick"})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("unpick status %d", resp2.StatusCode)
	}
	var got2 map[string]any
	json.NewDecoder(resp2.Body).Decode(&got2)
	if got2["state"] != "unmarked" {
		t.Errorf("state = %v want unmarked", got2["state"])
	}
}

func TestPicksRejectThenUnreject(t *testing.T) {
	env := newTestServer(t)
	id := seedFileOnDisk(t, env.store, env.scanRoot, "Day1/IMG_0002.JPG")

	resp := postJSON(t, env, "/api/picks", map[string]any{"file_id": id, "action": "reject"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject status %d", resp.StatusCode)
	}
	got, err := env.store.Picks().GetByFileID(id)
	if err != nil || got.State != "rejected" {
		t.Errorf("after reject: %+v err=%v", got, err)
	}

	resp = postJSON(t, env, "/api/picks", map[string]any{"file_id": id, "action": "unreject"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unreject status %d", resp.StatusCode)
	}
}

func TestPicksRejectsUnknownAction(t *testing.T) {
	env := newTestServer(t)
	id := seedFile(t, env.store, "Day1/IMG_0003.JPG")
	resp := postJSON(t, env, "/api/picks", map[string]any{"file_id": id, "action": "destroy"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", resp.StatusCode)
	}
}

func TestPicksRequiresKnownFile(t *testing.T) {
	env := newTestServer(t)
	resp := postJSON(t, env, "/api/picks", map[string]any{"file_id": 999999, "action": "pick"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}

func TestPicksGetReturnsCurrentState(t *testing.T) {
	env := newTestServer(t)
	id1 := seedFileOnDisk(t, env.store, env.scanRoot, "Day1/A.JPG")
	id2 := seedFileOnDisk(t, env.store, env.scanRoot, "Day1/B.JPG")
	postJSON(t, env, "/api/picks", map[string]any{"file_id": id1, "action": "pick"}).Body.Close()
	postJSON(t, env, "/api/picks", map[string]any{"file_id": id2, "action": "reject"}).Body.Close()

	resp := env.get("/api/picks")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		Picks []picksResponse `json:"picks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	state := map[int64]string{}
	for _, p := range got.Picks {
		state[p.FileID] = p.State
	}
	if state[id1] != "picked" {
		t.Errorf("id1 = %q, want picked", state[id1])
	}
	if state[id2] != "rejected" {
		t.Errorf("id2 = %q, want rejected", state[id2])
	}
}

func TestMultipleMembersOfClusterCanBePicked(t *testing.T) {
	env := newTestServer(t)
	id1 := seedFileOnDisk(t, env.store, env.scanRoot, "Day1/A.JPG")
	id2 := seedFileOnDisk(t, env.store, env.scanRoot, "Day1/B.JPG")
	id3 := seedFileOnDisk(t, env.store, env.scanRoot, "Day1/C.JPG")
	seedCluster(t, env.store, "X-T5#A1", 1714185600, []int64{id1, id2, id3}, id1)

	for _, id := range []int64{id1, id3} {
		resp := postJSON(t, env, "/api/picks", map[string]any{
			"file_id": id, "action": "pick",
		})
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("pick %d: status %d", id, resp.StatusCode)
		}
	}

	all, err := env.store.Picks().All()
	if err != nil {
		t.Fatal(err)
	}
	picked := 0
	for _, p := range all {
		if p.State == "picked" {
			picked++
		}
	}
	if picked != 2 {
		t.Errorf("picked count = %d, want 2", picked)
	}
}

package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// Two jobs, two rating passes: the status lists each with its folder, and
// Done, Total and FolderID describe the latest one begun.
func TestStatus_PassesPerFolder(t *testing.T) {
	e := newQualityTestEnv(t)
	e.set(func(e *qualityTestEnv) { e.busy = true })
	_, progressA, endA := e.tr.Begin(context.Background(), "A")
	progressA(1, 4)
	_, progressB, endB := e.tr.Begin(context.Background(), "B")
	progressB(2, 10)
	assertQuality(t, e.tr.Status(), events.Quality{
		Enabled: true, State: "scoring", Active: true, Done: 2, Total: 10, Rated: 3, FolderID: "B",
		Passes: []events.QualityPass{{FolderID: "A", Done: 1, Total: 4}, {FolderID: "B", Done: 2, Total: 10}},
	})

	progressA(4, 4)
	endA(4, nil)
	assertQuality(t, e.tr.Status(), events.Quality{
		Enabled: true, State: "scoring", Active: true, Done: 2, Total: 10, Rated: 6, FolderID: "B",
		Passes: []events.QualityPass{{FolderID: "B", Done: 2, Total: 10}},
	})

	endB(2, nil)
	e.set(func(e *qualityTestEnv) { e.busy = false })
	assertQuality(t, e.tr.Status(), events.Quality{Enabled: true, State: "idle", Rated: 6})
}

// A pass that begins or ends is a change of state, sent at once; progress
// within the same passes is coalesced.
func TestPublish_PassesChangeAtOnceProgressCoalesced(t *testing.T) {
	e := newQualityTestEnv(t)
	_, progressA, _ := e.tr.Begin(context.Background(), "A")
	_, _, endB := e.tr.Begin(context.Background(), "B")
	got := e.sent(t)
	if len(got) != 2 {
		t.Fatalf("events after two Begins = %+v; want two, at once", got)
	}
	if n := len(got[1].Passes); n != 2 {
		t.Errorf("second event's passes = %+v; want two", got[1].Passes)
	}
	progressA(1, 3)
	if n := len(e.sent(t)); n != 2 {
		t.Fatalf("progress was sent at once (%d events); want it coalesced", n)
	}
	endB(0, nil)
	got = e.sent(t)
	if len(got) != 3 {
		t.Fatalf("events after B ended = %d; want 3 (the end at once)", len(got))
	}
	if p := got[2].Passes; len(p) != 1 || p[0] != (events.QualityPass{FolderID: "A", Done: 1, Total: 3}) {
		t.Errorf("passes after B ended = %+v; want only A at 1 of 3", p)
	}
}

// On the wire: lowercase tags; passes and folder_id are left out when no
// pass runs (never null).
func TestQualityStatus_JSON(t *testing.T) {
	e := newQualityTestEnv(t)
	idle, err := json.Marshal(e.tr.Status())
	if err != nil {
		t.Fatal(err)
	}
	if s := string(idle); strings.Contains(s, "passes") || strings.Contains(s, "folder_id") {
		t.Errorf("idle status JSON %s; want neither passes nor folder_id", s)
	}

	_, progress, _ := e.tr.Begin(context.Background(), "A")
	progress(1, 4)
	scoring, err := json.Marshal(e.tr.Status())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"folder_id":"A"`, `"passes":[{"folder_id":"A","done":1,"total":4}]`} {
		if !strings.Contains(string(scoring), want) {
			t.Errorf("scoring status JSON %s; want it to contain %s", scoring, want)
		}
	}
}

// The job begins its pass with its folder's id.
func TestJob_BeginsThePassWithItsFolder(t *testing.T) {
	models := &jobTestModels{}
	models.set(local.ModelQuality, true)
	stills := &platformtest.Stills{ReadPaths: true, Props: map[string]map[string]any{}}
	h := newJobTestHarness(t, sessionTestOptions{stills: stills, inferer: &jobTestInferer{}}, models)
	id, root := h.folder(t, "which")
	jobTestWriteShots(t, stills, root, 512, 512, time.Second, "a.jpg")
	tr := &jobTestTracker{}
	if o, msg := h.run(context.Background(), h.job(tr), id, JobFull); o != Done {
		t.Fatalf("Run = %v %q; want Done", o, msg)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.folders) != 1 || tr.folders[0] != id {
		t.Errorf("passes begun for %v; want [%s]", tr.folders, id)
	}
}

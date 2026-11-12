package events

import (
	"encoding/json"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
)

// jsonKeys marshals v and returns its top-level object keys, sorted.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func TestPayloadJSON_MatchesDesignTypes(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"Progress", Progress{Stage: "thumb", Done: 1, Total: 2, Failed: 0, State: "running"},
			[]string{"stage", "done", "total", "failed", "state"}},
		{"ModelStatus without error", ModelStatus{Kind: "clip", State: "downloading", Received: 1, Size: 2},
			[]string{"kind", "state", "received", "size"}},
		{"ModelStatus with error", ModelStatus{Kind: "quality", State: "failed", Error: "boom"},
			[]string{"kind", "state", "received", "size", "error"}},
		{"Quality without error or pass", Quality{Enabled: true, State: "idle"},
			[]string{"enabled", "state", "active", "received", "size", "done", "total", "rated"}},
		{"Quality scoring", Quality{Enabled: true, State: "scoring", FolderID: "f1", Passes: []QualityPass{{FolderID: "f1"}}},
			[]string{"enabled", "state", "active", "received", "size", "done", "total", "rated", "folder_id", "passes"}},
		{"QualityPass", QualityPass{FolderID: "f1", Done: 1, Total: 2},
			[]string{"folder_id", "done", "total"}},
		{"ScanProgressEvent", ScanProgressEvent{FolderID: "f1", Progress: Progress{Stage: "discover"}, Phase: "running"},
			[]string{"folderId", "progress", "phase"}},
		{"ScanStateEvent without error", ScanStateEvent{FolderID: "f1", State: "ready"},
			[]string{"folderId", "state"}},
		{"LibraryChangedEvent stats", LibraryChangedEvent{Reason: "stats"},
			[]string{"reason"}},
		{"LibraryChangedEvent added", LibraryChangedEvent{Reason: "added", FolderID: "f1", Name: "Trip"},
			[]string{"reason", "folderId", "name"}},
		{"FolderChangedEvent", FolderChangedEvent{FolderID: "f1", Reason: "clusters"},
			[]string{"folderId", "reason"}},
		{"MenuActionEvent without data", MenuActionEvent{ID: "open-folder"},
			[]string{"id"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if got := jsonKeys(t, tt.v); !reflect.DeepEqual(got, want) {
				t.Errorf("keys = %v, want %v", got, want)
			}
		})
	}
}

func TestNames_Seven(t *testing.T) {
	want := []string{
		"library:changed", "scan:progress", "scan:state",
		"models:progress", "quality:status",
		"folder:changed", "menu:action",
	}
	got := Names()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	// The caller owns the returned slice.
	got[0] = "mutated"
	if Names()[0] != LibraryChanged {
		t.Fatal("Names() returned a shared slice")
	}
}

func TestProgressFromPipeline(t *testing.T) {
	tests := []struct {
		phase     string
		wantState string
	}{
		{"starting", "running"},
		{"running", "running"},
		{"done", "running"},
		{"failed", "stopped"},
	}
	for _, tt := range tests {
		t.Run(tt.phase, func(t *testing.T) {
			in := pipeline.Progress{Stage: "analyze", Done: 3, Total: 7, Failed: 1, Phase: tt.phase}
			got, phase := ProgressFromPipeline(in)
			want := Progress{Stage: "analyze", Done: 3, Total: 7, Failed: 1, State: tt.wantState}
			if got != want {
				t.Errorf("progress = %+v, want %+v", got, want)
			}
			if phase != tt.phase {
				t.Errorf("phase = %q, want %q", phase, tt.phase)
			}
		})
	}
}

func TestRecorder_RecordsPerName(t *testing.T) {
	var r Recorder
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() { r.Emit(ScanState, i) })
	}
	wg.Wait()
	r.Emit(MenuAction, "x")

	if got := len(r.Events(ScanState)); got != 10 {
		t.Errorf("ScanState events = %d, want 10", got)
	}
	if got := r.Events(MenuAction); !reflect.DeepEqual(got, []any{"x"}) {
		t.Errorf("MenuAction events = %v, want [x]", got)
	}
	if got := r.Events(FolderChanged); len(got) != 0 {
		t.Errorf("FolderChanged events = %v, want none", got)
	}
}

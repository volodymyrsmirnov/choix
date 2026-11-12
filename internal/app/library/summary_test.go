package library

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

// testPreviewURL is the PreviewURL the summary tests pass.
func testPreviewURL(id string, n int, stamp int64) string {
	return fmt.Sprintf("preview://%s/%d?v=%d", id, n, stamp)
}

// summaryFolder is a present, idle folder at /Users/tester/Photos/Trip.
func summaryFolder() Folder {
	return Folder{
		ID: "f1", Path: "/Users/tester/Photos/Trip", Name: "Trip",
		VolumeUUID: "UUID-1", VolumeName: "Macintosh HD", Presence: presencePresent,
	}
}

func TestSummarise_States(t *testing.T) {
	progress := &events.Progress{Stage: "thumb", Done: 3, Total: 10, State: "running"}
	tests := []struct {
		name      string
		presence  string
		pending   bool
		jobError  string
		jobState  string
		p         *events.Progress
		wantState string
		wantError string
		wantScan  bool
		wantVol   bool
	}{
		{name: "offline", presence: presenceOffline, wantState: "offline", wantVol: true},
		{name: "offline while the job winds down", presence: presenceOffline, pending: true,
			jobState: "scanning", p: progress, wantState: "offline", wantVol: true},
		{name: "scanning with an old error", presence: presencePresent, pending: true,
			jobError: "The scan failed.", jobState: "scanning", p: progress, wantState: "scanning", wantScan: true},
		{name: "queued", presence: presencePresent, pending: true, jobState: "queued", wantState: "queued"},
		{name: "pending, not in the queue", presence: presencePresent, pending: true, wantState: "queued"},
		{name: "missing", presence: presenceMissing, wantState: "error", wantError: MissingMessage},
		{name: "access lost", presence: presenceAccessLost, wantState: "error", wantError: AccessLostMessage},
		{name: "job error", presence: presencePresent, jobError: "The folder could not be read.",
			wantState: "error", wantError: "The folder could not be read."},
		{name: "ready", presence: presencePresent, wantState: "ready"},
		// Controller ruling 1: a suspended job never hides the presence error.
		{name: "access lost with a pending job", presence: presenceAccessLost, pending: true,
			wantState: "error", wantError: AccessLostMessage},
		{name: "missing with a pending job", presence: presenceMissing, pending: true,
			wantState: "error", wantError: MissingMessage},
		{name: "missing while still listed as queued", presence: presenceMissing, pending: true,
			jobState: "queued", wantState: "error", wantError: MissingMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := summaryFolder()
			f.Presence, f.JobPending, f.JobError = tt.presence, tt.pending, tt.jobError
			if tt.pending {
				f.JobKind = jobFull
			}
			s := Summarise(f, tt.jobState, tt.p, "/Users/tester", testPreviewURL)
			if s.State != tt.wantState {
				t.Errorf("State = %q, want %q", s.State, tt.wantState)
			}
			if s.Error != tt.wantError {
				t.Errorf("Error = %q, want %q", s.Error, tt.wantError)
			}
			if tt.wantScan {
				if s.Scan == nil || *s.Scan != *tt.p {
					t.Errorf("Scan = %+v, want %+v", s.Scan, tt.p)
				}
				if s.Scan == tt.p {
					t.Error("Scan aliases the caller's progress, want a copy")
				}
			} else if s.Scan != nil {
				t.Errorf("Scan = %+v, want nil", s.Scan)
			}
			wantVol := ""
			if tt.wantVol {
				wantVol = "Macintosh HD"
			}
			if s.VolumeName != wantVol {
				t.Errorf("VolumeName = %q, want %q", s.VolumeName, wantVol)
			}
			if tt.pending && s.State == "ready" {
				t.Error("a folder with pending work is reported ready")
			}
		})
	}
}

func TestSummarise_MonthAndRange(t *testing.T) {
	first := time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC).Unix()
	last := time.Date(2026, 3, 6, 19, 30, 0, 0, time.UTC).Unix()
	f := summaryFolder()
	f.Stats = Stats{
		Photos: 12, Videos: 2, Clusters: 5, Picked: 3, Rejected: 1, Failed: 4,
		FirstCaptured: sql.NullInt64{Int64: first, Valid: true},
		LastCaptured:  sql.NullInt64{Int64: last, Valid: true},
	}
	s := Summarise(f, "", nil, "", nil)
	if s.Month == nil || *s.Month != "2026-03" {
		t.Errorf("Month = %v, want 2026-03", s.Month)
	}
	if s.FirstCaptured == nil || *s.FirstCaptured != "2026-03-04T08:00:00Z" {
		t.Errorf("FirstCaptured = %v, want 2026-03-04T08:00:00Z", s.FirstCaptured)
	}
	if s.LastCaptured == nil || *s.LastCaptured != "2026-03-06T19:30:00Z" {
		t.Errorf("LastCaptured = %v, want 2026-03-06T19:30:00Z", s.LastCaptured)
	}
	if s.Photos != 12 || s.Videos != 2 || s.Clusters != 5 || s.Picked != 3 || s.Rejected != 1 {
		t.Errorf("counts = %+v", s)
	}

	// The month is taken in UTC, whatever the local time zone.
	f.Stats.FirstCaptured.Int64 = time.Date(2026, 4, 30, 23, 30, 0, 0, time.UTC).Unix()
	if s := Summarise(f, "", nil, "", nil); s.Month == nil || *s.Month != "2026-04" {
		t.Errorf("Month = %v, want 2026-04 (UTC)", s.Month)
	}

	undated := summaryFolder()
	s = Summarise(undated, "", nil, "", nil)
	if s.Month != nil || s.FirstCaptured != nil || s.LastCaptured != nil {
		t.Errorf("undated: Month %v, FirstCaptured %v, LastCaptured %v, want all nil", s.Month, s.FirstCaptured, s.LastCaptured)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"month", "firstCaptured", "lastCaptured"} {
		v, ok := m[k]
		if !ok || v != nil {
			t.Errorf("JSON %q = %v (present %v), want null", k, v, ok)
		}
	}
}

func TestSummarise_ParentPathAbbreviated(t *testing.T) {
	tests := []struct {
		path, home, want string
	}{
		{"/Users/tester/Photos/Trip", "/Users/tester", "~/Photos"},
		{"/Users/tester/Photos/Trip", "/Users/tester/", "~/Photos"},
		{"/Users/tester/Trip", "/Users/tester", "~"},
		{"/Volumes/SD/Trip", "/Users/tester", "/Volumes/SD"},
		{"/Users/testerx/Trip", "/Users/tester", "/Users/testerx"},
		{"/Users/tester/Photos/Trip", "", "/Users/tester/Photos"},
		{"/Trip", "/Users/tester", "/"},
	}
	for _, tt := range tests {
		f := summaryFolder()
		f.Path = tt.path
		if got := Summarise(f, "", nil, tt.home, nil).ParentPath; got != tt.want {
			t.Errorf("ParentPath(%q, home %q) = %q, want %q", tt.path, tt.home, got, tt.want)
		}
	}
}

func TestSummarise_PreviewURLs(t *testing.T) {
	f := summaryFolder()
	f.Previews, f.PreviewsStamp = 3, 1_700_000_042
	got := Summarise(f, "", nil, "", testPreviewURL).Previews
	want := []string{
		"preview://f1/0?v=1700000042",
		"preview://f1/1?v=1700000042",
		"preview://f1/2?v=1700000042",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Previews = %v, want %v", got, want)
	}

	f.Previews = 0
	if got := Summarise(f, "", nil, "", testPreviewURL).Previews; got == nil || len(got) != 0 {
		t.Errorf("no previews: %#v, want an empty, non-nil slice", got)
	}
	f.Previews = 2
	if got := Summarise(f, "", nil, "", nil).Previews; got == nil || len(got) != 0 {
		t.Errorf("nil PreviewURL: %#v, want an empty, non-nil slice", got)
	}
}

func TestSummarise_AddedAt(t *testing.T) {
	// added_at is library.db's folders.added_at, unix seconds, for every
	// folder whatever its state (design FolderSummary.added_at: the
	// "Recently added" order sorts on it).
	for _, presence := range []string{presencePresent, presenceOffline, presenceMissing, presenceAccessLost} {
		f := summaryFolder()
		f.Presence, f.AddedAt = presence, 1_700_000_123
		s := Summarise(f, "", nil, "", nil)
		if s.AddedAt != 1_700_000_123 {
			t.Errorf("%s: AddedAt = %d, want 1700000123", presence, s.AddedAt)
		}
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if string(m["added_at"]) != "1700000123" {
			t.Errorf("%s: JSON added_at = %s, want 1700000123", presence, m["added_at"])
		}
	}
}

func TestSummarise_JSONKeys(t *testing.T) {
	// Every key of the design's FolderSummary (frontend/src/design/types.ts).
	all := []string{
		"id", "name", "parentPath", "state", "month", "firstCaptured", "lastCaptured",
		"photos", "videos", "clusters", "picked", "rejected", "previews",
		"scan", "volumeName", "error", "added_at",
	}
	optional := []string{"scan", "volumeName", "error"}

	keys := func(s FolderSummary) []string {
		t.Helper()
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if string(m["previews"]) == "null" {
			t.Errorf("previews is null, want an array")
		}
		return slices.Sorted(maps.Keys(m))
	}

	ready := Summarise(summaryFolder(), "", nil, "", nil)
	want := slices.DeleteFunc(slices.Clone(all), func(k string) bool { return slices.Contains(optional, k) })
	slices.Sort(want)
	if got := keys(ready); !slices.Equal(got, want) {
		t.Errorf("ready keys = %v, want %v", got, want)
	}

	// Every optional key appears when it has a value.
	full := FolderSummary{
		Scan: &events.Progress{Stage: "discover"}, VolumeName: "SD", Error: "x",
		Previews: []string{},
	}
	want = slices.Sorted(slices.Values(all))
	if got := keys(full); !slices.Equal(got, want) {
		t.Errorf("full keys = %v, want %v", got, want)
	}

	// The struct has no field the design does not know.
	typ := reflect.TypeFor[FolderSummary]()
	var tags []string
	for f := range typ.Fields() {
		tag := f.Tag.Get("json")
		for i := range len(tag) {
			if tag[i] == ',' {
				tag = tag[:i]
				break
			}
		}
		tags = append(tags, tag)
	}
	slices.Sort(tags)
	if !slices.Equal(tags, slices.Sorted(slices.Values(all))) {
		t.Errorf("FolderSummary JSON names = %v, want %v", tags, slices.Sorted(slices.Values(all)))
	}
}

package settings

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
	"github.com/volodymyrsmirnov/choix/internal/config"
)

func newGate() *opgate.Gate { return &opgate.Gate{} }

func newService(t *testing.T, h Hooks) (*Service, *Store) {
	t.Helper()
	s := newStore(t)
	return NewService(s, h, newGate()), s
}

func TestGet_Defaults(t *testing.T) {
	svc, _ := newService(t, Hooks{})
	got, err := svc.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := Body{
		BucketSize:         600,
		Similarity:         0.90,
		PicksDir:           "picks",
		AdvanceOnAction:    false,
		HideRejectedPhotos: false,
		CrossDeviceMerging: false,
		QualityRating:      true,
		VisualGrouping:     true,
	}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

func TestSave_PartialPatchKeepsOtherFields(t *testing.T) {
	svc, _ := newService(t, Hooks{})
	if _, err := svc.Save(Patch{BucketSize: ptr(120), HideRejectedPhotos: ptr(true)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	res, err := svc.Save(Patch{PicksDir: ptr("selects")})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !res.Saved {
		t.Error("Saved = false, want true")
	}
	got, err := svc.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := Body{
		BucketSize: 120, Similarity: 0.90, PicksDir: "selects",
		HideRejectedPhotos: true, QualityRating: true, VisualGrouping: true,
	}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

func TestSave_Validation(t *testing.T) {
	tests := []struct {
		name   string
		patch  Patch
		field  string
		reason string // "" ⇒ accepted
	}{
		{"bucket 59", Patch{BucketSize: ptr(59)}, "bucket_size", "min"},
		{"bucket 60", Patch{BucketSize: ptr(60)}, "", ""},
		{"similarity 0", Patch{Similarity: ptr(0.0)}, "similarity", "range"},
		{"similarity 1.01", Patch{Similarity: ptr(1.01)}, "similarity", "range"},
		{"similarity -0.5", Patch{Similarity: ptr(-0.5)}, "similarity", "range"},
		{"similarity 1", Patch{Similarity: ptr(1.0)}, "", ""},
		{"picks empty", Patch{PicksDir: ptr("")}, "picks_dir", "empty"},
		{"picks ../x", Patch{PicksDir: ptr("../x")}, "picks_dir", "traversal"},
		{"picks a/../../b", Patch{PicksDir: ptr("a/../../b")}, "picks_dir", "traversal"},
		{"picks absolute", Patch{PicksDir: ptr("/Users/x/picks")}, "picks_dir", "absolute"},
		{"picks dot", Patch{PicksDir: ptr(".")}, "picks_dir", "reserved"},
		{"picks .choix/x", Patch{PicksDir: ptr(".choix/x")}, "picks_dir", "reserved"},
		{"picks exports/luminar", Patch{PicksDir: ptr("exports/luminar")}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newService(t, Hooks{})
			res, err := svc.Save(tt.patch)
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("Save = %v, want accepted", err)
				}
				if !res.Saved {
					t.Error("Saved = false, want true")
				}
				return
			}
			var e *apperr.Error
			if !errors.As(err, &e) {
				t.Fatalf("Save error = %v (%T), want *apperr.Error", err, err)
			}
			if e.Code != apperr.CodeInvalid || e.Field != tt.field || e.Reason != tt.reason {
				t.Errorf("error = %+v, want invalid %s/%s", e, tt.field, tt.reason)
			}
			if e.Message == "" {
				t.Error("Message is empty, want a log text")
			}
			if res.Saved {
				t.Error("Saved = true for a refused patch")
			}
			if store.Get() != config.Defaults() {
				t.Errorf("settings changed to %+v by a refused patch", store.Get())
			}
		})
	}
}

func TestSave_ReclustersOnlyWhenClusteringChanged(t *testing.T) {
	tests := []struct {
		name  string
		patch Patch
		want  bool
	}{
		{"bucket size", Patch{BucketSize: ptr(300)}, true},
		{"similarity", Patch{Similarity: ptr(0.85)}, true},
		{"cross-device merging", Patch{CrossDeviceMerging: ptr(true)}, true},
		{"visual grouping", Patch{VisualGrouping: ptr(false)}, true},
		{"same bucket size", Patch{BucketSize: ptr(600)}, false},
		{"same similarity", Patch{Similarity: ptr(0.90)}, false},
		{"picks dir", Patch{PicksDir: ptr("selects")}, false},
		{"advance on action", Patch{AdvanceOnAction: ptr(true)}, false},
		{"hide rejected", Patch{HideRejectedPhotos: ptr(true)}, false},
		{"quality rating", Patch{QualityRating: ptr(false)}, false},
		{"empty patch", Patch{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			svc, _ := newService(t, Hooks{Recluster: func() (bool, error) { calls++; return true, nil }})
			res, err := svc.Save(tt.patch)
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			wantCalls := 0
			if tt.want {
				wantCalls = 1
			}
			if calls != wantCalls || res.Reclustered != tt.want || !res.Saved {
				t.Errorf("calls = %d, result = %+v; want %d calls, Reclustered %v, Saved true",
					calls, res, wantCalls, tt.want)
			}
		})
	}
}

func TestSave_ReclusterReportsNoOpenFolder(t *testing.T) {
	svc, _ := newService(t, Hooks{Recluster: func() (bool, error) { return false, nil }})
	res, err := svc.Save(Patch{BucketSize: ptr(300)})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if res != (SaveResult{Saved: true, Reclustered: false}) {
		t.Errorf("result = %+v, want saved and not reclustered", res)
	}
}

// TestSave_ReclusterFailureIsReportedAndRetried: a failed re-cluster leaves
// the settings saved, reaches the frontend as the mapped error, and is owed:
// the same patch saved again (the stored values no longer differ) calls the
// hook again, until a call succeeds.
func TestSave_ReclusterFailureIsReportedAndRetried(t *testing.T) {
	calls := 0
	fail := true
	svc, _ := newService(t, Hooks{Recluster: func() (bool, error) {
		calls++
		if fail {
			return false, fmt.Errorf("recluster: %w", library.ErrOffline)
		}
		return true, nil
	}})

	_, err := svc.Save(Patch{BucketSize: ptr(300)})
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != apperr.CodeOffline {
		t.Fatalf("Save = %v, want the hook's error mapped to offline", err)
	}
	if calls != 1 {
		t.Fatalf("hook calls = %d, want 1", calls)
	}
	got, err := svc.Get()
	if err != nil || got.BucketSize != 300 {
		t.Fatalf("Get = %+v, %v; want the new bucket size saved", got, err)
	}

	// Still failing: still reported, still owed.
	if _, err := svc.Save(Patch{BucketSize: ptr(300)}); !errors.As(err, &e) || e.Code != apperr.CodeOffline {
		t.Fatalf("second Save = %v, want offline again", err)
	}

	fail = false
	res, err := svc.Save(Patch{BucketSize: ptr(300)})
	if err != nil {
		t.Fatalf("Save after recovery: %v", err)
	}
	if calls != 3 || res != (SaveResult{Saved: true, Reclustered: true}) {
		t.Errorf("calls = %d, result = %+v; want 3 calls and saved+reclustered", calls, res)
	}

	// Paid: the same patch no longer re-clusters.
	res, err = svc.Save(Patch{BucketSize: ptr(300)})
	if err != nil || calls != 3 || res != (SaveResult{Saved: true}) {
		t.Errorf("Save after the debt was paid = %+v, %v with %d calls; want saved, no re-cluster", res, err, calls)
	}
}

func TestSave_OwedReclusterRunsOnAnUnrelatedSave(t *testing.T) {
	calls := 0
	fail := true
	svc, _ := newService(t, Hooks{Recluster: func() (bool, error) {
		calls++
		if fail {
			return false, errors.New("boom")
		}
		return true, nil
	}})
	if _, err := svc.Save(Patch{Similarity: ptr(0.8)}); err == nil {
		t.Fatal("Save = nil error, want the hook's failure")
	}
	fail = false
	res, err := svc.Save(Patch{AdvanceOnAction: ptr(true)})
	if err != nil || calls != 2 || !res.Reclustered {
		t.Errorf("unrelated Save = %+v, %v with %d calls; want the owed re-cluster run", res, err, calls)
	}
}

func TestSave_OwedReclusterClearedWhenNoFolderIsOpen(t *testing.T) {
	calls := 0
	fail := true
	svc, _ := newService(t, Hooks{Recluster: func() (bool, error) {
		calls++
		if fail {
			return false, errors.New("boom")
		}
		return false, nil // no folder open: it is re-clustered when opened
	}})
	if _, err := svc.Save(Patch{Similarity: ptr(0.8)}); err == nil {
		t.Fatal("Save = nil error, want the hook's failure")
	}
	fail = false
	if res, err := svc.Save(Patch{}); err != nil || res != (SaveResult{Saved: true}) || calls != 2 {
		t.Fatalf("Save = %+v, %v with %d calls; want saved, not reclustered, 2 calls", res, err, calls)
	}
	if _, err := svc.Save(Patch{}); err != nil || calls != 2 {
		t.Errorf("Save = %v with %d calls; want nothing owed any more", err, calls)
	}
}

func TestSave_NilReclusterHook(t *testing.T) {
	svc, _ := newService(t, Hooks{})
	res, err := svc.Save(Patch{BucketSize: ptr(300)})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if res != (SaveResult{Saved: true}) {
		t.Errorf("result = %+v, want saved and not reclustered", res)
	}
}

func TestSave_AfterGateClosedIsUnavailable(t *testing.T) {
	g := newGate()
	s := newStore(t)
	called := false
	svc := NewService(s, Hooks{Recluster: func() (bool, error) { called = true; return true, nil }}, g)
	if !g.Close(0) {
		t.Fatal("Close = false on an idle gate")
	}

	res, err := svc.Save(Patch{BucketSize: ptr(300)})
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != apperr.CodeUnavailable {
		t.Fatalf("Save = %v, want an unavailable *apperr.Error", err)
	}
	if res.Saved || called || s.Get() != config.Defaults() {
		t.Errorf("Save after Close did work: result %+v, recluster %v, settings %+v", res, called, s.Get())
	}
	if _, err := svc.Get(); !errors.As(err, &e) || e.Code != apperr.CodeUnavailable {
		t.Errorf("Get = %v, want an unavailable *apperr.Error", err)
	}
}

func TestService_ExportedMethods(t *testing.T) {
	typ := reflect.TypeFor[*Service]()
	var got []string
	for m := range typ.Methods() {
		got = append(got, m.Name)
	}
	if want := []string{"Get", "Save"}; !slices.Equal(got, want) {
		t.Errorf("exported methods of *Service = %v, want exactly %v (each is a frontend binding)", got, want)
	}
}

func TestBodyAndPatch_JSONNames(t *testing.T) {
	names := func(typ reflect.Type) []string {
		var out []string
		for f := range typ.Fields() {
			out = append(out, f.Tag.Get("json"))
		}
		return out
	}
	body := names(reflect.TypeFor[Body]())
	want := []string{
		"bucket_size", "similarity", "picks_dir", "advance_on_action",
		"hide_rejected_photos", "cross_device_merging", "quality_rating", "visual_grouping",
	}
	if !slices.Equal(body, want) {
		t.Errorf("Body json tags = %v, want %v", body, want)
	}
	patch := names(reflect.TypeFor[Patch]())
	for i := range patch {
		want[i] += ",omitempty"
	}
	if !slices.Equal(patch, want) {
		t.Errorf("Patch json tags = %v, want %v", patch, want)
	}
	for f := range reflect.TypeFor[Patch]().Fields() {
		if f.Type.Kind() != reflect.Pointer {
			t.Errorf("Patch.%s is %v, want a pointer", f.Name, f.Type)
		}
	}
}

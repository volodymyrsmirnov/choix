package models

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
)

// newServiceManager returns a Manager, not started, with CLIP installed and
// both settings on.
func newServiceManager() *Manager {
	return New(Options{
		Downloader: newFake(local.ModelCLIP),
		Enabled:    func(local.ModelKind) bool { return true },
	})
}

// wantCode fails unless err is an *apperr.Error with code.
func wantCode(t *testing.T, err error, code string) *apperr.Error {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %#v, want an *apperr.Error with code %q", err, code)
	}
	return ae
}

func TestService_ExportedMethods(t *testing.T) {
	typ := reflect.TypeFor[*Service]()
	var got []string
	for m := range typ.Methods() {
		got = append(got, m.Name)
	}
	want := []string{"Quality", "Retry", "Status"}
	if !slices.Equal(got, want) {
		t.Errorf("exported methods of *Service = %v, want exactly %v (each is a frontend binding)", got, want)
	}
}

func TestModels_Status(t *testing.T) {
	m := newServiceManager()
	svc := NewService(m, nil, &opgate.Gate{})
	got, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 2 || got[0].Kind != "clip" || got[0].State != stateReady ||
		got[1].Kind != "quality" || got[1].State != stateQueued {
		t.Errorf("Status = %+v, want [clip ready, quality queued]", got)
	}
}

func TestModels_StatusAfterGateClosed(t *testing.T) {
	var gate opgate.Gate
	svc := NewService(newServiceManager(), func() events.Quality { return events.Quality{State: "idle"} }, &gate)
	gate.Close(0)
	got, err := svc.Status()
	wantCode(t, err, apperr.CodeUnavailable)
	if got != nil {
		t.Errorf("Status = %+v, want nil", got)
	}
	wantCode(t, svc.Retry("clip"), apperr.CodeUnavailable)
	_, err = svc.Quality()
	wantCode(t, err, apperr.CodeUnavailable)
}

func TestModelsRetry_UnknownKind(t *testing.T) {
	svc := NewService(newServiceManager(), nil, &opgate.Gate{})
	for _, kind := range []string{"", "CLIP", "nima", "unknown"} {
		ae := wantCode(t, svc.Retry(kind), apperr.CodeInvalid)
		if ae.Field != "kind" || ae.Reason != "unknown" {
			t.Errorf("Retry(%q) = %+v, want field kind, reason unknown", kind, ae)
		}
	}
}

func TestModelsRetry_FailedModelIsQueued(t *testing.T) {
	// CLIP installed; the quality model's only attempt fails.
	h := newHarness(t, newFake(local.ModelCLIP), newSwitches(true, true), []time.Duration{})
	h.m.Start(t.Context())
	h.dl.next(t, local.ModelQuality).finish(errors.New("network down"))
	waitState(t, h.m, local.ModelQuality, stateFailed)

	svc := NewService(h.m, nil, &opgate.Gate{})
	if err := svc.Retry("quality"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	h.dl.next(t, local.ModelQuality).finish(nil)
	waitState(t, h.m, local.ModelQuality, stateReady)

	// A model that has not failed: nothing happens, no error.
	if err := svc.Retry("clip"); err != nil {
		t.Errorf("Retry(clip): %v", err)
	}
	if n := h.dl.callCount(local.ModelCLIP); n != 0 {
		t.Errorf("CLIP downloaded %d times, want 0", n)
	}
}

func TestModels_Quality(t *testing.T) {
	want := events.Quality{
		Enabled: true, State: "scoring", Active: true, Done: 2, Total: 9, Rated: 5, FolderID: "b",
		Passes: []events.QualityPass{{FolderID: "a", Done: 3, Total: 4}, {FolderID: "b", Done: 2, Total: 9}},
	}
	svc := NewService(newServiceManager(), func() events.Quality { return want }, &opgate.Gate{})
	got, err := svc.Quality()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Quality = %+v, %v, want %+v", got, err, want)
	}

	none := NewService(newServiceManager(), nil, &opgate.Gate{})
	if got, err := none.Quality(); err != nil || got.State != "disabled" || len(got.Passes) != 0 {
		t.Errorf("Quality without a source = %+v, %v, want disabled with no passes", got, err)
	}
}

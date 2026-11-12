package models

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
)

// wantCode fails unless err is an *apperr.Error with code.
func wantCode(t *testing.T, err error, code string) *apperr.Error {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %#v, want an *apperr.Error with code %q", err, code)
	}
	return ae
}

// The shim keeps the binding's three methods, so the generated bindings the
// unchanged app code imports stay the same.
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

func TestShim_Status(t *testing.T) {
	svc := NewService(nil, &opgate.Gate{})
	got, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := []events.ModelStatus{{Kind: "clip", State: "ready"}, {Kind: "quality", State: "ready"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Status = %+v, want %+v", got, want)
	}
}

func TestShim_Retry(t *testing.T) {
	svc := NewService(nil, &opgate.Gate{})
	for _, kind := range []string{"clip", "quality"} {
		if err := svc.Retry(kind); err != nil {
			t.Errorf("Retry(%q) = %v, want nil", kind, err)
		}
	}
	for _, kind := range []string{"", "CLIP", "nima", "unknown"} {
		ae := wantCode(t, svc.Retry(kind), apperr.CodeInvalid)
		if ae.Field != "kind" || ae.Reason != "unknown" {
			t.Errorf("Retry(%q) = %+v, want field kind, reason unknown", kind, ae)
		}
	}
}

func TestShim_Quality(t *testing.T) {
	want := events.Quality{
		Enabled: true, State: "scoring", Active: true, Done: 2, Total: 9, Rated: 5, FolderID: "b",
		Passes: []events.QualityPass{{FolderID: "a", Done: 3, Total: 4}, {FolderID: "b", Done: 2, Total: 9}},
	}
	svc := NewService(func() events.Quality { return want }, &opgate.Gate{})
	got, err := svc.Quality()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Quality = %+v, %v, want %+v", got, err, want)
	}

	none := NewService(nil, &opgate.Gate{})
	if got, err := none.Quality(); err != nil || got.State != "disabled" || len(got.Passes) != 0 {
		t.Errorf("Quality without a source = %+v, %v, want disabled with no passes", got, err)
	}
}

func TestShim_AfterGateClosed(t *testing.T) {
	var gate opgate.Gate
	svc := NewService(func() events.Quality { return events.Quality{State: "idle"} }, &gate)
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

package catalog

import (
	"reflect"
	"slices"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

func TestService_ExportedMethods(t *testing.T) {
	typ := reflect.TypeFor[*Service]()
	var got []string
	for m := range typ.Methods() {
		got = append(got, m.Name)
	}
	want := []string{"Close", "Cluster", "Clusters", "Open", "Ratings", "Recluster", "SetPick", "Undo"}
	if !slices.Equal(got, want) {
		t.Errorf("exported methods of *Service = %v, want exactly %v (each is a frontend binding)", got, want)
	}
}

func TestService_RoundTrip(t *testing.T) {
	h := newCatalogTestHarness(t)
	id, root := h.folder(t, "f")
	var a int64
	catalogTestSeed(t, root, func(st *store.Store) {
		a = catalogTestFile(t, st, root, "A.JPG", []byte("stub"))
		catalogTestCluster(t, st, "X-T5#A1", 1714185600, a)
	})
	var gate opgate.Gate
	svc := NewService(h.cat, &gate)

	v, err := svc.Open(id)
	if err != nil || len(v.Clusters) != 1 {
		t.Fatalf("Open = %+v, %v", v, err)
	}
	if _, err := svc.Clusters(id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Cluster(id, v.Clusters[0].ID); err != nil {
		t.Fatal(err)
	}
	if res, err := svc.SetPick(id, a, "pick"); err != nil || res.State != "picked" {
		t.Fatalf("SetPick = %+v, %v", res, err)
	}
	if res, err := svc.Undo(id); err != nil || res.State != "unmarked" {
		t.Fatalf("Undo = %+v, %v", res, err)
	}
	if err := svc.Recluster(id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Ratings(id); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(id); err != nil {
		t.Fatal(err)
	}
	// A binding returns the *apperr.Error itself.
	_, err = svc.Clusters(id)
	if e, ok := err.(*apperr.Error); !ok || e.Code != apperr.CodeNotOpen { //nolint:errorlint // the binding must return the *apperr.Error itself
		t.Fatalf("Clusters after Close = %#v; want an *apperr.Error not_open", err)
	}

	if !gate.Close(0) {
		t.Fatal("an operation is still inside the gate")
	}
	if _, err := svc.Open(id); catalogTestCode(err) != apperr.CodeUnavailable {
		t.Fatalf("Open after the gate closed = %v; want unavailable", err)
	}
	if err := svc.Close(id); catalogTestCode(err) != apperr.CodeUnavailable {
		t.Fatalf("Close after the gate closed = %v; want unavailable", err)
	}
}

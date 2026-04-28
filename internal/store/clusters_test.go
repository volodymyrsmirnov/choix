package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestClustersInsertAndGet(t *testing.T) {
	s := newTestStore(t)

	id, err := s.Clusters().Insert("Cam#1", sql.NullInt64{Int64: 1700000000, Valid: true})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id == 0 {
		t.Fatal("Insert returned id=0")
	}

	got, err := s.Clusters().Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DeviceKey != "Cam#1" {
		t.Errorf("DeviceKey = %q, want Cam#1", got.DeviceKey)
	}
	if !got.TimeBucket.Valid || got.TimeBucket.Int64 != 1700000000 {
		t.Errorf("TimeBucket = %+v", got.TimeBucket)
	}

	if _, err := s.Clusters().Get(99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing: got %v, want ErrNotFound", err)
	}
}

func TestClustersListByBucket(t *testing.T) {
	s := newTestStore(t)

	mk := func(dev string, bucket sql.NullInt64) int64 {
		id, err := s.Clusters().Insert(dev, bucket)
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		return id
	}
	a := mk("Cam#1", sql.NullInt64{Int64: 1700000000, Valid: true})
	b := mk("Cam#1", sql.NullInt64{Int64: 1700000000, Valid: true})
	mk("Cam#1", sql.NullInt64{Int64: 1700000600, Valid: true})
	mk("Cam#2", sql.NullInt64{Int64: 1700000000, Valid: true})

	got, err := s.Clusters().ListByBucket("Cam#1", sql.NullInt64{Int64: 1700000000, Valid: true})
	if err != nil {
		t.Fatalf("ListByBucket: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	ids := map[int64]bool{got[0].ID: true, got[1].ID: true}
	if !ids[a] || !ids[b] {
		t.Errorf("ids = %v, want {%d, %d}", ids, a, b)
	}
}

func TestClustersDeleteByBucket(t *testing.T) {
	s := newTestStore(t)

	mk := func(dev string, bucket int64) int64 {
		id, err := s.Clusters().Insert(dev, sql.NullInt64{Int64: bucket, Valid: true})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		return id
	}
	keep1 := mk("Cam#1", 1700000600)
	keep2 := mk("Cam#2", 1700000000)
	gone1 := mk("Cam#1", 1700000000)
	gone2 := mk("Cam#1", 1700000000)

	if err := s.Clusters().DeleteByBucket("Cam#1", sql.NullInt64{Int64: 1700000000, Valid: true}); err != nil {
		t.Fatalf("DeleteByBucket: %v", err)
	}

	if _, err := s.Clusters().Get(gone1); !errors.Is(err, ErrNotFound) {
		t.Errorf("gone1 still present: %v", err)
	}
	if _, err := s.Clusters().Get(gone2); !errors.Is(err, ErrNotFound) {
		t.Errorf("gone2 still present: %v", err)
	}
	if _, err := s.Clusters().Get(keep1); err != nil {
		t.Errorf("keep1 missing: %v", err)
	}
	if _, err := s.Clusters().Get(keep2); err != nil {
		t.Errorf("keep2 missing: %v", err)
	}
}

func TestClustersListAll(t *testing.T) {
	s := newTestStore(t)

	s.Clusters().Insert("Cam#1", sql.NullInt64{Int64: 1700000000, Valid: true}) //nolint:errcheck
	s.Clusters().Insert("Cam#2", sql.NullInt64{Int64: 1700000600, Valid: true}) //nolint:errcheck

	all, err := s.Clusters().ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("ListAll: got %d want 2", len(all))
	}
}

package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestClusterMembersAddAndQuery(t *testing.T) {
	s := newTestStore(t)

	mkFile := func(path string) int64 {
		id, err := s.Files().Insert(File{
			Path: path, Size: 1, Mtime: 1, ContentHash: "h",
			Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
		})
		if err != nil {
			t.Fatalf("Insert file %s: %v", path, err)
		}
		return id
	}

	cid1, _ := s.Clusters().Insert("Cam#1", sql.NullInt64{Int64: 1700000000, Valid: true})
	cid2, _ := s.Clusters().Insert("Cam#1", sql.NullInt64{Int64: 1700000600, Valid: true})

	f1 := mkFile("a.jpg")
	f2 := mkFile("b.jpg")
	f3 := mkFile("c.jpg")
	orphan := mkFile("orphan.jpg")
	_ = orphan

	if err := s.ClusterMembers().AddMember(cid1, f1); err != nil {
		t.Fatalf("AddMember c1/f1: %v", err)
	}
	if err := s.ClusterMembers().AddMember(cid1, f2); err != nil {
		t.Fatalf("AddMember c1/f2: %v", err)
	}
	if err := s.ClusterMembers().AddMember(cid2, f3); err != nil {
		t.Fatalf("AddMember c2/f3: %v", err)
	}

	// Adding the same member twice is a no-op (PK conflict ignored).
	if err := s.ClusterMembers().AddMember(cid1, f1); err != nil {
		t.Fatalf("AddMember duplicate: %v", err)
	}

	members, err := s.ClusterMembers().MembersOf(cid1)
	if err != nil {
		t.Fatalf("MembersOf: %v", err)
	}
	got := map[int64]bool{}
	for _, fid := range members {
		got[fid] = true
	}
	if len(members) != 2 || !got[f1] || !got[f2] {
		t.Errorf("MembersOf(c1) = %v, want {%d, %d}", members, f1, f2)
	}

	cid, err := s.ClusterMembers().MembershipOf(f3)
	if err != nil {
		t.Fatalf("MembershipOf: %v", err)
	}
	if cid != cid2 {
		t.Errorf("MembershipOf(f3) = %d, want %d", cid, cid2)
	}

	if _, err := s.ClusterMembers().MembershipOf(orphan); !errors.Is(err, ErrNotFound) {
		t.Errorf("MembershipOf(orphan): got %v, want ErrNotFound", err)
	}
}

func TestClusterMembersInsertManyAndListByCluster(t *testing.T) {
	s := newTestStore(t)

	mkFile := func(path string) int64 {
		id, err := s.Files().Insert(File{
			Path: path, Size: 1, Mtime: 1, ContentHash: "h",
			Kind: "photo", Format: "jpeg", ScanStatus: "analyzed",
		})
		if err != nil {
			t.Fatalf("Insert file %s: %v", path, err)
		}
		return id
	}

	cid, _ := s.Clusters().Insert("Cam#1", sql.NullInt64{Int64: 1700000000, Valid: true})
	f1 := mkFile("x.jpg")
	f2 := mkFile("y.jpg")
	f3 := mkFile("z.jpg")

	if err := s.ClusterMembers().InsertMany(cid, []int64{f1, f2, f3}); err != nil {
		t.Fatalf("InsertMany: %v", err)
	}

	members, err := s.ClusterMembers().ListByCluster(cid)
	if err != nil {
		t.Fatalf("ListByCluster: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("ListByCluster: got %d want 3", len(members))
	}
	got := map[int64]bool{}
	for _, fid := range members {
		got[fid] = true
	}
	if !got[f1] || !got[f2] || !got[f3] {
		t.Errorf("ListByCluster = %v, want {%d, %d, %d}", members, f1, f2, f3)
	}
}

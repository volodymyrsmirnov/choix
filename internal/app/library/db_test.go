package library

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := OpenDB(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func testFolder(id, path string, addedAt int64) Folder {
	return Folder{
		ID:         id,
		Path:       path,
		Name:       filepath.Base(path),
		Bookmark:   []byte("bookmark-" + id),
		VolumeUUID: "UUID-" + id,
		VolumeName: "Volume " + id,
		AddedAt:    addedAt,
	}
}

func mustInsert(t *testing.T, d *DB, f Folder) {
	t.Helper()
	if err := d.InsertFolder(f); err != nil {
		t.Fatalf("InsertFolder(%s): %v", f.ID, err)
	}
}

func mustFolder(t *testing.T, d *DB, id string) Folder {
	t.Helper()
	f, err := d.Folder(id)
	if err != nil {
		t.Fatalf("Folder(%s): %v", id, err)
	}
	return f
}

func mustEnqueue(t *testing.T, d *DB, id, kind string) int64 {
	t.Helper()
	seq, err := d.EnqueueJob(id, kind)
	if err != nil {
		t.Fatalf("EnqueueJob(%s, %s): %v", id, kind, err)
	}
	return seq
}

func userVersion(t *testing.T, d *DB) int {
	t.Helper()
	var v int
	if err := d.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	return v
}

func TestOpenDB_MigratesToVersion1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	d, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if v := userVersion(t, d); v != 1 {
		t.Fatalf("user_version = %d, want 1", v)
	}
	for _, tbl := range []string{"folders", "kv"} {
		var name string
		if err := d.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", tbl, err)
		}
	}
	mustInsert(t, d, testFolder("0123456789abcdef", "/Volumes/X/Trip", 10))
	if err := d.KVSet("k", "v"); err != nil {
		t.Fatalf("KVSet: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening is a no-op: same version, data kept, nothing re-run.
	d, err = OpenDB(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = d.Close() }()
	if v := userVersion(t, d); v != 1 {
		t.Fatalf("user_version after reopen = %d, want 1", v)
	}
	if fs, err := d.Folders(); err != nil || len(fs) != 1 {
		t.Fatalf("Folders after reopen = %d rows, %v; want 1 row", len(fs), err)
	}
	if v, ok, err := d.KVGet("k"); err != nil || !ok || v != "v" {
		t.Fatalf("KVGet after reopen = %q, %v, %v; want \"v\", true, nil", v, ok, err)
	}
}

func TestOpenDB_CreatesFileInExistingDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "library.db")
	d, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = d.Close() }()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file (mode %v)", path, fi.Mode())
	}
}

func TestOpenDB_EmptyPath(t *testing.T) {
	if d, err := OpenDB(""); err == nil {
		_ = d.Close()
		t.Fatal("OpenDB(\"\") succeeded, want an error")
	}
}

func TestOpenDB_DirectoryNameWithURICharacters(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "a #1?b%41")
	if err := os.Mkdir(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "library.db")
	d, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB(%q): %v", path, err)
	}
	mustInsert(t, d, testFolder("0123456789abcdef", "/Volumes/X/Trip", 10))
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("library.db not created inside %s: %v", dataDir, err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a #1?b%41" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("%s holds %q, want only the data directory (nothing beside it)", tmp, names)
	}

	d, err = OpenDB(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = d.Close() }()
	mustFolder(t, d, "0123456789abcdef")
}

func TestInsertFolder_Defaults(t *testing.T) {
	d := openTestDB(t)
	in := testFolder("0123456789abcdef", "/Volumes/X/Trip", 42)
	mustInsert(t, d, in)

	got := mustFolder(t, d, in.ID)
	if got.JobPending {
		t.Error("JobPending = true, want false")
	}
	if got.Presence != "present" {
		t.Errorf("Presence = %q, want \"present\"", got.Presence)
	}
	if got.JobKind != "" || got.JobError != "" {
		t.Errorf("JobKind, JobError = %q, %q; want empty", got.JobKind, got.JobError)
	}
	if got.Stats != (Stats{}) || got.Previews != 0 || got.PreviewsStamp != 0 {
		t.Errorf("Stats, Previews, PreviewsStamp = %+v, %d, %d; want zero", got.Stats, got.Previews, got.PreviewsStamp)
	}
	if got.ID != in.ID || got.Path != in.Path || got.Name != in.Name || !bytes.Equal(got.Bookmark, in.Bookmark) ||
		got.VolumeUUID != in.VolumeUUID || got.VolumeName != in.VolumeName || got.AddedAt != in.AddedAt {
		t.Errorf("Folder = %+v, want the inserted %+v", got, in)
	}
}

func TestInsertFolder_PathIsUniqueIgnoringCase(t *testing.T) {
	d := openTestDB(t)
	mustInsert(t, d, testFolder("0000000000000001", "/Volumes/X/Trip", 1))
	if err := d.InsertFolder(testFolder("0000000000000002", "/volumes/x/trip", 2)); err == nil {
		t.Fatal("inserting the same path in another case succeeded, want an error")
	}
	fs, err := d.Folders()
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if len(fs) != 1 || fs[0].ID != "0000000000000001" {
		t.Fatalf("Folders = %+v, want only the first folder", fs)
	}
}

func TestFolder_NotFound(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.Folder("ffffffffffffffff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Folder(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestMutations_UnknownFolder(t *testing.T) {
	d := openTestDB(t)
	const id = "ffffffffffffffff"
	cases := []struct {
		name string
		fn   func() error
	}{
		{"DeleteFolder", func() error { return d.DeleteFolder(id) }},
		{"UpdateLocation", func() error { return d.UpdateLocation(id, "/p", "p", platform.VolumeInfo{}) }},
		{"SetBookmark", func() error { return d.SetBookmark(id, []byte("b")) }},
		{"SetPresence", func() error { return d.SetPresence(id, "offline") }},
		{"EnqueueJob", func() error { _, err := d.EnqueueJob(id, "full"); return err }},
		{"ClearJob", func() error { return d.ClearJob(id) }},
		{"SetStats", func() error { return d.SetStats(id, Stats{Photos: 1}) }},
		{"SetPreviews", func() error { return d.SetPreviews(id, 1, 1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
	}
	// A failed enqueue does not consume a sequence number.
	mustInsert(t, d, testFolder("0000000000000001", "/a", 1))
	if seq := mustEnqueue(t, d, "0000000000000001", "full"); seq != 1 {
		t.Fatalf("first successful enqueue seq = %d, want 1", seq)
	}
}

func TestDeleteFolder(t *testing.T) {
	d := openTestDB(t)
	mustInsert(t, d, testFolder("0000000000000001", "/a", 1))
	if err := d.DeleteFolder("0000000000000001"); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if _, err := d.Folder("0000000000000001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Folder after delete err = %v, want ErrNotFound", err)
	}
	// The path is free again.
	mustInsert(t, d, testFolder("0000000000000002", "/A", 2))
}

func TestEnqueueJob_SequenceGrows(t *testing.T) {
	d := openTestDB(t)
	mustInsert(t, d, testFolder("0000000000000001", "/a", 1))
	mustInsert(t, d, testFolder("0000000000000002", "/b", 2))

	got := []int64{
		mustEnqueue(t, d, "0000000000000001", "full"),
		mustEnqueue(t, d, "0000000000000002", "backfill"),
		mustEnqueue(t, d, "0000000000000001", "full"),
	}
	for i, want := range []int64{1, 2, 3} {
		if got[i] != want {
			t.Fatalf("enqueue seqs = %v, want [1 2 3]", got)
		}
	}
	f := mustFolder(t, d, "0000000000000001")
	if !f.JobPending || f.JobKind != "full" {
		t.Fatalf("folder 1 JobPending, JobKind = %v, %q; want true, \"full\"", f.JobPending, f.JobKind)
	}
	f = mustFolder(t, d, "0000000000000002")
	if !f.JobPending || f.JobKind != "backfill" {
		t.Fatalf("folder 2 JobPending, JobKind = %v, %q; want true, \"backfill\"", f.JobPending, f.JobKind)
	}
}

func TestEnqueueJob_RejectsUnknownKind(t *testing.T) {
	d := openTestDB(t)
	mustInsert(t, d, testFolder("0000000000000001", "/a", 1))
	for _, kind := range []string{"", "partial"} {
		if _, err := d.EnqueueJob("0000000000000001", kind); err == nil {
			t.Errorf("EnqueueJob(kind %q) succeeded, want an error", kind)
		}
	}
	if f := mustFolder(t, d, "0000000000000001"); f.JobPending {
		t.Fatal("a rejected enqueue left the folder pending")
	}
}

func TestEnqueueJob_FullIsNotDowngraded(t *testing.T) {
	d := openTestDB(t)
	const id = "0000000000000001"
	mustInsert(t, d, testFolder(id, "/a", 1))

	first := mustEnqueue(t, d, id, "full")
	second := mustEnqueue(t, d, id, "backfill")
	if second <= first {
		t.Fatalf("second seq = %d, want > %d", second, first)
	}
	f := mustFolder(t, d, id)
	if !f.JobPending || f.JobKind != "full" {
		t.Fatalf("after full then backfill: JobPending, JobKind = %v, %q; want true, \"full\"", f.JobPending, f.JobKind)
	}
	jobs, err := d.PendingJobs()
	if err != nil {
		t.Fatalf("PendingJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0] != (PendingJob{ID: id, Kind: "full", Seq: second}) {
		t.Fatalf("PendingJobs = %+v, want [{%s full %d}]", jobs, id, second)
	}

	// Once the full job is done, a backfill is a backfill; a full upgrades it.
	if cleared, err := d.FinishJob(id, second, ""); err != nil || !cleared {
		t.Fatalf("FinishJob = %v, %v; want true, nil", cleared, err)
	}
	mustEnqueue(t, d, id, "backfill")
	if f := mustFolder(t, d, id); f.JobKind != "backfill" {
		t.Fatalf("backfill on an idle folder: JobKind = %q, want \"backfill\"", f.JobKind)
	}
	mustEnqueue(t, d, id, "full")
	if f := mustFolder(t, d, id); f.JobKind != "full" {
		t.Fatalf("full over a pending backfill: JobKind = %q, want \"full\"", f.JobKind)
	}
}

func TestFinishJob_OnlyItsOwnSequence(t *testing.T) {
	d := openTestDB(t)
	const id = "0000000000000001"
	mustInsert(t, d, testFolder(id, "/a", 1))

	if seq := mustEnqueue(t, d, id, "full"); seq != 1 {
		t.Fatalf("first seq = %d, want 1", seq)
	}
	if seq := mustEnqueue(t, d, id, "full"); seq != 2 {
		t.Fatalf("second seq = %d, want 2", seq)
	}

	cleared, err := d.FinishJob(id, 1, "")
	if err != nil {
		t.Fatalf("FinishJob(1): %v", err)
	}
	if cleared {
		t.Fatal("FinishJob(1) cleared a job re-enqueued as seq 2")
	}
	if f := mustFolder(t, d, id); !f.JobPending || f.JobKind != "full" {
		t.Fatalf("after FinishJob(1): JobPending, JobKind = %v, %q; want true, \"full\"", f.JobPending, f.JobKind)
	}

	cleared, err = d.FinishJob(id, 2, "")
	if err != nil {
		t.Fatalf("FinishJob(2): %v", err)
	}
	if !cleared {
		t.Fatal("FinishJob(2) did not clear its own job")
	}
	if f := mustFolder(t, d, id); f.JobPending || f.JobKind != "" {
		t.Fatalf("after FinishJob(2): JobPending, JobKind = %v, %q; want false, \"\"", f.JobPending, f.JobKind)
	}
}

func TestFinishJob_StoresError(t *testing.T) {
	d := openTestDB(t)
	const id = "0000000000000001"
	mustInsert(t, d, testFolder(id, "/a", 1))
	seq := mustEnqueue(t, d, id, "full")

	if cleared, err := d.FinishJob(id, seq, "disk on fire"); err != nil || !cleared {
		t.Fatalf("FinishJob = %v, %v; want true, nil", cleared, err)
	}
	f := mustFolder(t, d, id)
	if f.JobError != "disk on fire" || f.JobPending {
		t.Fatalf("JobError, JobPending = %q, %v; want \"disk on fire\", false", f.JobError, f.JobPending)
	}

	// The next enqueue clears the previous run's error.
	mustEnqueue(t, d, id, "backfill")
	if f := mustFolder(t, d, id); f.JobError != "" {
		t.Fatalf("JobError after re-enqueue = %q, want empty", f.JobError)
	}
}

func TestClearJob(t *testing.T) {
	d := openTestDB(t)
	const id = "0000000000000001"
	mustInsert(t, d, testFolder(id, "/a", 1))
	mustEnqueue(t, d, id, "full")

	if err := d.ClearJob(id); err != nil {
		t.Fatalf("ClearJob: %v", err)
	}
	f := mustFolder(t, d, id)
	if f.JobPending || f.JobKind != "" {
		t.Fatalf("after ClearJob: JobPending, JobKind = %v, %q; want false, \"\"", f.JobPending, f.JobKind)
	}
	jobs, err := d.PendingJobs()
	if err != nil {
		t.Fatalf("PendingJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("PendingJobs = %+v, want none", jobs)
	}
}

func TestPendingJobs_InEnqueueOrder(t *testing.T) {
	d := openTestDB(t)
	mustInsert(t, d, testFolder("0000000000000001", "/a", 1))
	mustInsert(t, d, testFolder("0000000000000002", "/b", 2))
	mustInsert(t, d, testFolder("0000000000000003", "/c", 3))

	// The folder added last is enqueued first; the one added second is idle.
	s3 := mustEnqueue(t, d, "0000000000000003", "full")
	s1 := mustEnqueue(t, d, "0000000000000001", "backfill")

	jobs, err := d.PendingJobs()
	if err != nil {
		t.Fatalf("PendingJobs: %v", err)
	}
	want := []PendingJob{
		{ID: "0000000000000003", Kind: "full", Seq: s3},
		{ID: "0000000000000001", Kind: "backfill", Seq: s1},
	}
	if len(jobs) != len(want) {
		t.Fatalf("PendingJobs = %+v, want %+v", jobs, want)
	}
	for i := range want {
		if jobs[i] != want[i] {
			t.Fatalf("PendingJobs = %+v, want %+v", jobs, want)
		}
	}
}

func TestSetStats_RoundTrip(t *testing.T) {
	d := openTestDB(t)
	const id = "0000000000000001"
	mustInsert(t, d, testFolder(id, "/a", 1))

	s := Stats{
		Photos: 120, Videos: 7, Clusters: 33, Picked: 12, Rejected: 4, Failed: 2,
		FirstCaptured: sql.NullInt64{Int64: 1_700_000_000, Valid: true},
		LastCaptured:  sql.NullInt64{Int64: 1_700_086_400, Valid: true},
	}
	if err := d.SetStats(id, s); err != nil {
		t.Fatalf("SetStats: %v", err)
	}
	if got := mustFolder(t, d, id).Stats; got != s {
		t.Fatalf("Stats = %+v, want %+v", got, s)
	}

	// NULL capture times round-trip as invalid.
	empty := Stats{Failed: 1}
	if err := d.SetStats(id, empty); err != nil {
		t.Fatalf("SetStats(empty): %v", err)
	}
	if got := mustFolder(t, d, id).Stats; got != empty {
		t.Fatalf("Stats = %+v, want %+v", got, empty)
	}

	if err := d.SetPreviews(id, 98, 1_700_090_000); err != nil {
		t.Fatalf("SetPreviews: %v", err)
	}
	f := mustFolder(t, d, id)
	if f.Previews != 98 || f.PreviewsStamp != 1_700_090_000 {
		t.Fatalf("Previews, PreviewsStamp = %d, %d; want 98, 1700090000", f.Previews, f.PreviewsStamp)
	}
	if f.Stats != empty {
		t.Fatalf("SetPreviews changed Stats to %+v", f.Stats)
	}
}

func TestUpdateLocation_RoundTrip(t *testing.T) {
	d := openTestDB(t)
	const id = "0000000000000001"
	mustInsert(t, d, testFolder(id, "/Volumes/Old/Trip", 1))

	v := platform.VolumeInfo{UUID: "UUID-NEW", Name: "New", Removable: true}
	if err := d.UpdateLocation(id, "/Volumes/New/Trip 2", "Trip 2", v); err != nil {
		t.Fatalf("UpdateLocation: %v", err)
	}
	f := mustFolder(t, d, id)
	if f.Path != "/Volumes/New/Trip 2" || f.Name != "Trip 2" || f.VolumeUUID != "UUID-NEW" || f.VolumeName != "New" {
		t.Fatalf("location = %q, %q, %q, %q; want the updated one", f.Path, f.Name, f.VolumeUUID, f.VolumeName)
	}

	// Moving onto another folder's path (in any case) is refused.
	mustInsert(t, d, testFolder("0000000000000002", "/Volumes/Other", 2))
	if err := d.UpdateLocation(id, "/volumes/other", "other", v); err == nil {
		t.Fatal("UpdateLocation onto another folder's path succeeded, want an error")
	}
}

func TestSetBookmark_RoundTrip(t *testing.T) {
	d := openTestDB(t)
	const id = "0000000000000001"
	mustInsert(t, d, testFolder(id, "/a", 1))

	b := []byte{0x00, 0x01, 0xfe, 0xff, 'b', 'o', 'o', 'k'}
	if err := d.SetBookmark(id, b); err != nil {
		t.Fatalf("SetBookmark: %v", err)
	}
	if got := mustFolder(t, d, id).Bookmark; !bytes.Equal(got, b) {
		t.Fatalf("Bookmark = %x, want %x", got, b)
	}
}

func TestSetPresence_RoundTrip(t *testing.T) {
	d := openTestDB(t)
	const id = "0000000000000001"
	mustInsert(t, d, testFolder(id, "/a", 1))
	for _, p := range []string{"offline", "missing", "access_lost", "present"} {
		if err := d.SetPresence(id, p); err != nil {
			t.Fatalf("SetPresence(%s): %v", p, err)
		}
		if got := mustFolder(t, d, id).Presence; got != p {
			t.Fatalf("Presence = %q, want %q", got, p)
		}
	}
}

func TestKV_RoundTrip(t *testing.T) {
	d := openTestDB(t)
	if v, ok, err := d.KVGet("missing"); err != nil || ok || v != "" {
		t.Fatalf("KVGet(missing) = %q, %v, %v; want \"\", false, nil", v, ok, err)
	}
	if err := d.KVSet("k", "one"); err != nil {
		t.Fatalf("KVSet: %v", err)
	}
	if err := d.KVSet("k", "two"); err != nil {
		t.Fatalf("KVSet (overwrite): %v", err)
	}
	if v, ok, err := d.KVGet("k"); err != nil || !ok || v != "two" {
		t.Fatalf("KVGet(k) = %q, %v, %v; want \"two\", true, nil", v, ok, err)
	}
}

func TestFolders_OrderedByAddedAt(t *testing.T) {
	d := openTestDB(t)
	// Inserted out of order; two share an added_at, so id breaks the tie.
	mustInsert(t, d, testFolder("0000000000000003", "/c", 30))
	mustInsert(t, d, testFolder("000000000000000b", "/b", 20))
	mustInsert(t, d, testFolder("000000000000000a", "/a", 20))
	mustInsert(t, d, testFolder("0000000000000001", "/d", 10))

	fs, err := d.Folders()
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	want := []string{"0000000000000001", "000000000000000a", "000000000000000b", "0000000000000003"}
	if len(fs) != len(want) {
		t.Fatalf("Folders returned %d rows, want %d", len(fs), len(want))
	}
	for i, id := range want {
		if fs[i].ID != id {
			got := make([]string, len(fs))
			for j, f := range fs {
				got[j] = f.ID
			}
			t.Fatalf("Folders order = %v, want %v", got, want)
		}
	}
}

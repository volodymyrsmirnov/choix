package store

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

func TestAISignalsGetMissing(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.AISignals().GetByFileID(99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByFileID missing: got %v, want ErrNotFound", err)
	}
}

// insertClipRow writes a retired clip_embedding value for the file, as a
// folder last processed by an earlier build holds.
func insertClipRow(t *testing.T, s *Store, fileID int64, clip []byte) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO ai_signals (file_id, clip_embedding) VALUES (?, ?)`, fileID, clip); err != nil {
		t.Fatal(err)
	}
}

const testPrintID = "test-print/v1"

// testPrint is a FeaturePrintLen vector whose values depend on seed, with
// awkward float32 values so a lossy round trip shows.
func testPrint(seed float32) []float32 {
	v := make([]float32, platform.FeaturePrintLen)
	for i := range v {
		v[i] = seed + float32(i)*1.000001e-3 - float32(math.Pi)
	}
	v[0] = math.SmallestNonzeroFloat32
	v[1] = -math.MaxFloat32
	return v
}

func TestPrint_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	id := insertAnalyzed(t, s, "a.jpg", "photo", "h")
	want := testPrint(1)
	if err := s.AISignals().UpsertPrint(id, want, testPrintID); err != nil {
		t.Fatalf("UpsertPrint: %v", err)
	}
	got, err := s.AISignals().GetByFileID(id)
	if err != nil {
		t.Fatalf("GetByFileID: %v", err)
	}
	if !slices.Equal(got.Print, want) || got.PrintID != testPrintID || !got.ComputedAt.Valid {
		t.Fatalf("got print len %d, id %q, computed %v; want the stored print", len(got.Print), got.PrintID, got.ComputedAt)
	}
	all, err := s.AISignals().AllPrints(testPrintID)
	if err != nil {
		t.Fatalf("AllPrints: %v", err)
	}
	if len(all) != 1 || !slices.Equal(all[id], want) {
		t.Fatalf("AllPrints = %d entries; want the one print", len(all))
	}
}

// A print written over an existing CLIP row leaves clip_embedding alone, and
// a nil print is stored as NULL but stamped.
func TestPrint_NilIsStampedAndClipKept(t *testing.T) {
	s := newTestStore(t)
	id := insertAnalyzed(t, s, "a.jpg", "photo", "h")
	clip := bytes.Repeat([]byte{0x01}, 512*4)
	insertClipRow(t, s, id, clip)
	if err := s.AISignals().UpsertPrint(id, testPrint(1), testPrintID); err != nil {
		t.Fatal(err)
	}
	if err := s.AISignals().UpsertPrint(id, nil, testPrintID); err != nil {
		t.Fatalf("UpsertPrint(nil): %v", err)
	}
	got, err := s.AISignals().GetByFileID(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Print != nil || got.PrintID != testPrintID {
		t.Fatalf("print len %d, id %q; want no print, stamped %q", len(got.Print), got.PrintID, testPrintID)
	}
	var kept []byte
	if err := s.db.QueryRow(`SELECT clip_embedding FROM ai_signals WHERE file_id = ?`, id).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept, clip) {
		t.Fatal("UpsertPrint changed clip_embedding")
	}
	if all, err := s.AISignals().AllPrints(testPrintID); err != nil || len(all) != 0 {
		t.Fatalf("AllPrints = %v, %v; want none", all, err)
	}
	if n, err := s.Files().CountAnalyzedMissingPrint(testPrintID); err != nil || n != 0 {
		t.Fatalf("CountAnalyzedMissingPrint = %d, %v; want 0 (stamped)", n, err)
	}
}

func TestPrint_UpsertRejectsBadInput(t *testing.T) {
	s := newTestStore(t)
	id := insertAnalyzed(t, s, "a.jpg", "photo", "h")
	if err := s.AISignals().UpsertPrint(id, []float32{1, 2, 3}, testPrintID); err == nil {
		t.Error("UpsertPrint of a 3-element print: want an error")
	}
	if err := s.AISignals().UpsertPrint(id, testPrint(1), ""); err == nil {
		t.Error("UpsertPrint without an id: want an error")
	}
}

func TestPrint_AllPrintsFilters(t *testing.T) {
	s := newTestStore(t)
	good := insertAnalyzed(t, s, "good.jpg", "photo", "h1")
	other := insertAnalyzed(t, s, "other.jpg", "photo", "h2")
	null := insertAnalyzed(t, s, "null.jpg", "photo", "h3")
	short := insertAnalyzed(t, s, "short.jpg", "photo", "h4")
	insertAnalyzed(t, s, "none.jpg", "photo", "h5")
	if err := s.AISignals().UpsertPrint(good, testPrint(1), testPrintID); err != nil {
		t.Fatal(err)
	}
	if err := s.AISignals().UpsertPrint(other, testPrint(2), "older/v0"); err != nil {
		t.Fatal(err)
	}
	if err := s.AISignals().UpsertPrint(null, nil, testPrintID); err != nil {
		t.Fatal(err)
	}
	// A blob of the wrong length, as a damaged or foreign row would hold.
	if _, err := s.db.Exec(`INSERT INTO ai_signals (file_id, vision_print, vision_print_id) VALUES (?, ?, ?)`,
		short, bytes.Repeat([]byte{0}, 3068), testPrintID); err != nil {
		t.Fatal(err)
	}
	all, err := s.AISignals().AllPrints(testPrintID)
	if err != nil {
		t.Fatalf("AllPrints: %v", err)
	}
	if len(all) != 1 || all[good] == nil {
		t.Fatalf("AllPrints = %d entries; want only file %d", len(all), good)
	}
}

func TestPrint_CountAndBumpMissing(t *testing.T) {
	s := newTestStore(t)
	stamped := insertAnalyzed(t, s, "stamped.jpg", "photo", "h1")
	stampedNull := insertAnalyzed(t, s, "stamped-null.jpg", "photo", "h2")
	other := insertAnalyzed(t, s, "other.jpg", "photo", "h3")
	unstamped := insertAnalyzed(t, s, "unstamped.jpg", "photo", "h4")
	norow := insertAnalyzed(t, s, "norow.jpg", "photo", "h5")
	thumbed, err := s.Files().Insert(File{Path: "thumbed.jpg", Size: 1, Mtime: 1, ContentHash: "h6", Kind: "photo", Format: "jpeg", ScanStatus: "thumbed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AISignals().UpsertPrint(stamped, testPrint(1), testPrintID); err != nil {
		t.Fatal(err)
	}
	if err := s.AISignals().UpsertPrint(stampedNull, nil, testPrintID); err != nil {
		t.Fatal(err)
	}
	if err := s.AISignals().UpsertPrint(other, testPrint(2), "older/v0"); err != nil {
		t.Fatal(err)
	}
	// A row with only a CLIP embedding has a NULL print id.
	insertClipRow(t, s, unstamped, bytes.Repeat([]byte{1}, 16))
	if err := s.Files().UpdateStatus(stamped, "analyzed", "earlier error"); err != nil {
		t.Fatal(err)
	}

	n, err := s.Files().CountAnalyzedMissingPrint(testPrintID)
	if err != nil || n != 3 {
		t.Fatalf("CountAnalyzedMissingPrint = %d, %v; want 3", n, err)
	}
	if err := s.Files().UpdateStatus(norow, "analyzed", "kept until bumped"); err != nil {
		t.Fatal(err)
	}
	bumped, err := s.Files().BumpAnalyzedMissingPrint(testPrintID)
	if err != nil || bumped != 3 {
		t.Fatalf("BumpAnalyzedMissingPrint = %d, %v; want 3", bumped, err)
	}
	want := map[int64]string{
		stamped: "analyzed", stampedNull: "analyzed",
		other: "thumbed", unstamped: "thumbed", norow: "thumbed", thumbed: "thumbed",
	}
	for id, status := range want {
		f, err := s.Files().GetByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if f.ScanStatus != status {
			t.Errorf("file %s: status %q, want %q", f.Path, f.ScanStatus, status)
		}
		if id == norow && f.ErrMsg.Valid {
			t.Errorf("file %s: error %q, want it cleared", f.Path, f.ErrMsg.String)
		}
	}
	if n, err := s.Files().CountAnalyzedMissingPrint(testPrintID); err != nil || n != 0 {
		t.Fatalf("after the bump: %d, %v; want 0", n, err)
	}
}

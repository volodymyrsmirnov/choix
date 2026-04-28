package store

import (
	"errors"
	"testing"
	"time"
)

func TestScanRunsLifecycle(t *testing.T) {
	s := newTestStore(t)

	start := time.Now().Unix()
	id, err := s.ScanRuns().Start(start, "tok-abc")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id == 0 {
		t.Fatal("Start returned id=0")
	}

	got, err := s.ScanRuns().GetLatest()
	if err != nil {
		t.Fatalf("GetLatest after Start: %v", err)
	}
	if got.ID != id || got.Status != "running" {
		t.Errorf("after Start: %+v", got)
	}
	if !got.CancelToken.Valid || got.CancelToken.String != "tok-abc" {
		t.Errorf("cancel_token = %+v", got.CancelToken)
	}
	if got.FinishedAt.Valid {
		t.Errorf("finished_at should be NULL on a running scan, got %+v", got.FinishedAt)
	}

	if err := s.ScanRuns().UpdateProgress(id, 100, 25, 80, 10); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	got, _ = s.ScanRuns().GetLatest()
	if got.FilesTotal.Int64 != 100 || got.FilesDone.Int64 != 25 ||
		got.AITotal.Int64 != 80 || got.AIDone.Int64 != 10 {
		t.Errorf("progress = %+v", got)
	}

	finishTs := start + 60
	if err := s.ScanRuns().Finish(id, "completed", finishTs); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	got, _ = s.ScanRuns().GetLatest()
	if got.Status != "completed" {
		t.Errorf("Status = %q, want completed", got.Status)
	}
	if !got.FinishedAt.Valid || got.FinishedAt.Int64 != finishTs {
		t.Errorf("FinishedAt = %+v", got.FinishedAt)
	}
}

func TestScanRunsGetLatestEmpty(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.ScanRuns().GetLatest(); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLatest empty: got %v, want ErrNotFound", err)
	}
}

func TestScanRunsGetLatestPicksMostRecent(t *testing.T) {
	s := newTestStore(t)

	id1, _ := s.ScanRuns().Start(1000, "t1")
	_ = s.ScanRuns().Finish(id1, "completed", 1100)
	id2, _ := s.ScanRuns().Start(2000, "t2")

	got, err := s.ScanRuns().GetLatest()
	if err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
	if got.ID != id2 || got.Status != "running" {
		t.Errorf("got %+v, want id=%d status=running", got, id2)
	}
}

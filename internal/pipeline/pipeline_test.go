package pipeline

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeScanner records Walk calls.
type fakeScanner struct {
	walked int32
	err    error
}

func (f *fakeScanner) Walk(ctx context.Context) error {
	atomic.AddInt32(&f.walked, 1)
	return f.err
}

// fakeStore returns IDs grouped by status. The map is consumed: once a
// status is fetched and ListByStatus is called for it, it returns the
// same IDs (the test then expects them to be re-marked via Process).
type fakeStore struct {
	mu       sync.Mutex
	byStatus map[string][]int64 // logical "files in this stage waiting"
}

func (f *fakeStore) ListByStatus(ctx context.Context, status string, limit int) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := f.byStatus[status]
	f.byStatus[status] = nil // simulate stage advancing them out
	return ids, nil
}

func (f *fakeStore) CountByStatus(ctx context.Context, status string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byStatus[status]), nil
}

// fakeProcessor records ids it saw on Process.
type fakeProcessor struct {
	mu  sync.Mutex
	ids []int64
}

func (f *fakeProcessor) Process(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, id)
	return nil
}

func (f *fakeProcessor) Build(ctx context.Context, id int64) error   { return f.Process(ctx, id) }
func (f *fakeProcessor) Analyze(ctx context.Context, id int64) error { return f.Process(ctx, id) }

// fakeGrouper records RebuildAll calls.
type fakeGrouper struct{ called int32 }

func (f *fakeGrouper) RebuildAll(ctx context.Context) error {
	atomic.AddInt32(&f.called, 1)
	return nil
}

func newPipeline(t *testing.T) (*Pipeline, *fakeScanner, *fakeStore, *fakeProcessor, *fakeProcessor, *fakeProcessor, *fakeGrouper, chan Progress) {
	t.Helper()
	scn := &fakeScanner{}
	st := &fakeStore{byStatus: map[string][]int64{
		"discovered": {1, 2, 3},
		"metadata":   {1, 2, 3},
		"thumbed":    {1, 2, 3},
	}}
	meta := &fakeProcessor{}
	thumb := &fakeProcessor{}
	ana := &fakeProcessor{}
	grp := &fakeGrouper{}
	prog := make(chan Progress, 64)
	p := &Pipeline{
		Store:         st,
		Scanner:       scn,
		MetaExtractor: meta,
		ThumbBuilder:  thumb,
		Analyzer:      ana,
		Grouper:       grp,
		Reporter:      NewReporter(prog),
		Concurrency:   map[string]int{"metadata": 2, "thumb": 2, "analyze": 1},
		BatchSize:     100,
	}
	return p, scn, st, meta, thumb, ana, grp, prog
}

func TestPipelineRunFlowsAllStages(t *testing.T) {
	p, scn, _, meta, thumb, ana, grp, _ := newPipeline(t)
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if atomic.LoadInt32(&scn.walked) != 1 {
		t.Errorf("scanner.Walk called %d times, want 1", scn.walked)
	}
	if got := len(meta.ids); got != 3 {
		t.Errorf("metadata processed %d, want 3", got)
	}
	if got := len(thumb.ids); got != 3 {
		t.Errorf("thumb processed %d, want 3", got)
	}
	if got := len(ana.ids); got != 3 {
		t.Errorf("analyze processed %d, want 3", got)
	}
	if atomic.LoadInt32(&grp.called) != 1 {
		t.Errorf("grouper.RebuildAll called %d, want 1", grp.called)
	}
}

func TestPipelineRunEmitsProgress(t *testing.T) {
	p, _, _, _, _, _, _, prog := newPipeline(t)
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	close(prog)
	stages := map[string]bool{}
	for ev := range prog {
		stages[ev.Stage] = true
	}
	for _, want := range []string{"metadata", "thumb", "analyze", "cluster"} {
		if !stages[want] {
			t.Errorf("no progress event for stage %q", want)
		}
	}
}

func TestPipelineRunCancelStopsCleanly(t *testing.T) {
	p, _, _, _, _, _, grp, _ := newPipeline(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run
	err := p.Run(ctx)
	if err == nil {
		t.Fatal("Run with cancelled ctx must return non-nil error")
	}
	if atomic.LoadInt32(&grp.called) != 0 {
		t.Errorf("grouper called despite cancel: %d", grp.called)
	}
}

func TestPipelineDefaultsConcurrencyToNumCPU(t *testing.T) {
	p := &Pipeline{}
	if got := p.workersFor("metadata"); got < 1 {
		t.Errorf("workersFor with empty config = %d, want >= 1", got)
	}
}

func TestPipelineResumeSkipsScanner(t *testing.T) {
	p, scn, _, _, _, _, grp, _ := newPipeline(t)
	if err := p.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if atomic.LoadInt32(&scn.walked) != 0 {
		t.Errorf("Resume invoked scanner: walked = %d, want 0", scn.walked)
	}
	if atomic.LoadInt32(&grp.called) != 1 {
		t.Errorf("Resume must still cluster: called = %d, want 1", grp.called)
	}
}

func TestPipelineResumeFromPartialState(t *testing.T) {
	// Simulate a previous run that completed metadata for ids 1,2,3 and
	// thumb for 1,2 but stopped before thumb for id 3 and analyze for any.
	scn := &fakeScanner{}
	st := &fakeStore{byStatus: map[string][]int64{
		"discovered": {},        // already done
		"metadata":   {3},       // only id 3 still needs thumb
		"thumbed":    {1, 2, 3}, // none analyzed yet
	}}
	meta := &fakeProcessor{}
	thumb := &fakeProcessor{}
	ana := &fakeProcessor{}
	grp := &fakeGrouper{}
	prog := make(chan Progress, 64)
	p := &Pipeline{
		Store:         st,
		Scanner:       scn,
		MetaExtractor: meta,
		ThumbBuilder:  thumb,
		Analyzer:      ana,
		Grouper:       grp,
		Reporter:      NewReporter(prog),
		Concurrency:   map[string]int{"metadata": 2, "thumb": 2, "analyze": 1},
		BatchSize:     100,
	}
	if err := p.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := len(meta.ids); got != 0 {
		t.Errorf("metadata re-processed: %d, want 0 (already done)", got)
	}
	if got := len(thumb.ids); got != 1 || thumb.ids[0] != 3 {
		t.Errorf("thumb processed = %v, want [3]", thumb.ids)
	}
	if got := len(ana.ids); got != 3 {
		t.Errorf("analyze processed = %d, want 3", got)
	}
}

func TestPipelineResumeRespectsContextCancel(t *testing.T) {
	p, _, _, _, _, _, _, _ := newPipeline(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Resume(ctx); err == nil {
		t.Fatal("Resume with cancelled ctx must return error")
	}
}

// stuckStore is a fakeStore variant whose ListByStatus always returns the same
// ids without consuming them, simulating items that fail without advancing
// their status (so CountByStatus never decrements).
type stuckStore struct {
	ids    []int64
	status string
}

func (s *stuckStore) ListByStatus(_ context.Context, status string, _ int) ([]int64, error) {
	if status == s.status {
		return s.ids, nil
	}
	return nil, nil
}

func (s *stuckStore) CountByStatus(_ context.Context, status string) (int, error) {
	if status == s.status {
		return len(s.ids), nil
	}
	return 0, nil
}

// alwaysFailProcessor always returns an error without changing the file status.
type alwaysFailProcessor struct{}

func (p *alwaysFailProcessor) Process(_ context.Context, _ int64) error {
	return fmt.Errorf("synthetic permanent failure")
}

// TestPipelineNoProgressBreaks verifies that runStage terminates (rather than
// looping forever) when every item in a batch fails without advancing its
// scan_status, and surfaces a "no progress" error.
func TestPipelineNoProgressBreaks(t *testing.T) {
	st := &stuckStore{ids: []int64{1, 2, 3}, status: "discovered"}
	proc := &alwaysFailProcessor{}
	prog := make(chan Progress, 64)
	p := &Pipeline{
		Store:         st,
		Scanner:       &fakeScanner{},
		MetaExtractor: proc,
		ThumbBuilder:  &fakeProcessor{},
		Analyzer:      &fakeProcessor{},
		Grouper:       &fakeGrouper{},
		Reporter:      NewReporter(prog),
		Concurrency:   map[string]int{"metadata": 1, "thumb": 1, "analyze": 1},
		BatchSize:     100,
	}

	err := p.Resume(context.Background())
	if err == nil {
		t.Fatal("Resume must return an error when stage makes no progress")
	}
	if !strings.Contains(err.Error(), "no progress") {
		t.Errorf("error = %q; want it to contain 'no progress'", err.Error())
	}
}

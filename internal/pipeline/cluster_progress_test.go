package pipeline

import (
	"context"
	"errors"
	"testing"
)

// progressGrouper reports the progress steps it is given, then returns err.
type progressGrouper struct {
	steps [][2]int
	err   error
}

func (g *progressGrouper) RebuildAll(ctx context.Context) error {
	return g.RebuildAllProgress(ctx, nil)
}

func (g *progressGrouper) RebuildAllProgress(_ context.Context, progress func(done, total int)) error {
	for _, s := range g.steps {
		if progress != nil {
			progress(s[0], s[1])
		}
	}
	return g.err
}

// clusterEvents runs p and returns the cluster stage's progress events.
func clusterEvents(t *testing.T, p *Pipeline, prog chan Progress) ([]Progress, error) {
	t.Helper()
	err := p.Resume(context.Background())
	close(prog)
	var out []Progress
	for ev := range prog {
		if ev.Stage == "cluster" {
			out = append(out, ev)
		}
	}
	return out, err
}

func TestCluster_ReportsDoneOfTotalLikeTheOtherStages(t *testing.T) {
	p, _, _, _, _, _, _, _ := newPipeline(t)
	prog := make(chan Progress, 256)
	p.Reporter = NewReporter(prog)
	p.Grouper = &progressGrouper{steps: [][2]int{{0, 5}, {2, 5}, {5, 5}}}

	evs, err := clusterEvents(t, p, prog)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	want := []Progress{
		{Stage: "cluster", Phase: "starting"},
		{Stage: "cluster", Done: 0, Total: 5, Phase: "running"},
		{Stage: "cluster", Done: 2, Total: 5, Phase: "running"},
		{Stage: "cluster", Done: 5, Total: 5, Phase: "running"},
		{Stage: "cluster", Done: 5, Total: 5, Phase: "done"},
	}
	if len(evs) != len(want) {
		t.Fatalf("cluster events %+v; want %+v", evs, want)
	}
	for i := range want {
		if evs[i] != want[i] {
			t.Errorf("cluster event %d = %+v; want %+v", i, evs[i], want[i])
		}
	}
}

func TestCluster_FailedKeepsItsLastCount(t *testing.T) {
	p, _, _, _, _, _, _, _ := newPipeline(t)
	prog := make(chan Progress, 256)
	p.Reporter = NewReporter(prog)
	boom := errors.New("boom")
	p.Grouper = &progressGrouper{steps: [][2]int{{0, 4}, {3, 4}}, err: boom}

	evs, err := clusterEvents(t, p, prog)
	if !errors.Is(err, boom) {
		t.Fatalf("Resume = %v; want boom", err)
	}
	last := evs[len(evs)-1]
	if last != (Progress{Stage: "cluster", Done: 3, Total: 4, Phase: "failed"}) {
		t.Errorf("last cluster event %+v; want failed at 3 of 4", last)
	}
}

func TestCluster_GrouperWithoutProgressStaysIndeterminate(t *testing.T) {
	p, _, _, _, _, _, _, _ := newPipeline(t)
	prog := make(chan Progress, 256)
	p.Reporter = NewReporter(prog)

	evs, err := clusterEvents(t, p, prog)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	want := []Progress{{Stage: "cluster", Phase: "starting"}, {Stage: "cluster", Phase: "done"}}
	if len(evs) != 2 || evs[0] != want[0] || evs[1] != want[1] {
		t.Errorf("cluster events %+v; want %+v", evs, want)
	}
}

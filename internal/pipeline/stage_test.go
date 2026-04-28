package pipeline

import (
	"context"
	"errors"
	"testing"
)

// fakeMeta records calls and returns optional error.
type fakeMeta struct {
	calls   []int64
	wantErr error
}

func (f *fakeMeta) Process(ctx context.Context, fileID int64) error {
	f.calls = append(f.calls, fileID)
	return f.wantErr
}

func TestStageInterfaceCompliance(t *testing.T) {
	// Each adapter must satisfy the Stage interface.
	var _ Stage = (*MetadataStage)(nil)
	var _ Stage = (*ThumbStage)(nil)
	var _ Stage = (*AnalyzeStage)(nil)
}

func TestStageNamesAreStable(t *testing.T) {
	cases := map[string]Stage{
		"metadata": &MetadataStage{},
		"thumb":    &ThumbStage{},
		"analyze":  &AnalyzeStage{},
	}
	for want, st := range cases {
		if got := st.Name(); got != want {
			t.Errorf("Name() = %q, want %q", got, want)
		}
	}
}

func TestRegisterStagesOrder(t *testing.T) {
	cfg := Pipeline{}
	stages := RegisterStages(&cfg)
	want := []string{"metadata", "thumb", "analyze"}
	if len(stages) != len(want) {
		t.Fatalf("len = %d, want %d", len(stages), len(want))
	}
	for i, s := range stages {
		if s.Name() != want[i] {
			t.Errorf("stages[%d].Name() = %q, want %q", i, s.Name(), want[i])
		}
	}
}

func TestStageAdapterDelegates(t *testing.T) {
	fm := &fakeMeta{wantErr: errors.New("boom")}
	stage := &MetadataStage{Extractor: fm}
	err := stage.Process(context.Background(), 42)
	if err == nil || err.Error() != "boom" {
		t.Errorf("Process err = %v, want boom", err)
	}
	if len(fm.calls) != 1 || fm.calls[0] != 42 {
		t.Errorf("calls = %v, want [42]", fm.calls)
	}
}

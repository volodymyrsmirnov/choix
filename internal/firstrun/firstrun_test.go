package firstrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/deps"
)

type fakeResolver struct {
	resolve map[string]string
	err     map[string]error
}

func (f *fakeResolver) Resolve(_ context.Context, name string) (string, error) {
	if e, ok := f.err[name]; ok {
		return "", e
	}
	return f.resolve[name], nil
}

type fakeModelStore struct {
	present map[local.ModelKind]bool
}

func (s *fakeModelStore) Has(_ context.Context, k local.ModelKind) (bool, error) {
	return s.present[k], nil
}

func TestDetect_AllPresent(t *testing.T) {
	r := &fakeResolver{resolve: map[string]string{"exiftool": "/usr/bin/exiftool", "ffmpeg": "/usr/bin/ffmpeg"}}
	ms := &fakeModelStore{present: map[local.ModelKind]bool{local.ModelCLIP: true}}

	st, err := Detect(context.Background(), r, ms, config.Config{})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if st.RequiresExifTool || st.RequiresFFmpeg {
		t.Errorf("expected no tool requirements, got %+v", st)
	}
	if len(st.RequiresModels) != 0 {
		t.Errorf("expected no model requirements, got %v", st.RequiresModels)
	}
	if !st.IsReady() {
		t.Errorf("expected IsReady true")
	}
}

func TestDetect_MissingExifTool(t *testing.T) {
	r := &fakeResolver{
		resolve: map[string]string{"ffmpeg": "/usr/bin/ffmpeg"},
		err:     map[string]error{"exiftool": errors.New("not found")},
	}
	ms := &fakeModelStore{present: map[local.ModelKind]bool{local.ModelCLIP: true}}

	st, err := Detect(context.Background(), r, ms, config.Config{})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !st.RequiresExifTool {
		t.Errorf("expected RequiresExifTool true")
	}
	if st.RequiresFFmpeg {
		t.Errorf("expected RequiresFFmpeg false")
	}
	if st.IsReady() {
		t.Errorf("expected IsReady false")
	}
}

func TestDetect_MissingCLIPModel(t *testing.T) {
	r := &fakeResolver{resolve: map[string]string{"exiftool": "/x", "ffmpeg": "/y"}}
	ms := &fakeModelStore{present: map[local.ModelKind]bool{local.ModelCLIP: false}}

	st, err := Detect(context.Background(), r, ms, config.Config{})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(st.RequiresModels) != 1 || st.RequiresModels[0] != local.ModelCLIP {
		t.Errorf("expected only CLIP missing, got %v", st.RequiresModels)
	}
	if st.IsReady() {
		t.Errorf("expected IsReady false")
	}
}

type fakeDownloader struct {
	progress []deps.ProgressEvent
	err      error
}

func (d *fakeDownloader) Fetch(ctx context.Context, name string, progress chan<- deps.ProgressEvent) error {
	for _, ev := range d.progress {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case progress <- ev:
		}
	}
	close(progress)
	return d.err
}

func TestInstallTool_StreamsProgress(t *testing.T) {
	d := &fakeDownloader{progress: []deps.ProgressEvent{
		{Stage: "fetch", PercentDone: 0.0},
		{Stage: "fetch", PercentDone: 0.5},
		{Stage: "verify", PercentDone: 1.0},
	}}
	events := make([]deps.ProgressEvent, 0, 3)
	cb := func(ev deps.ProgressEvent) { events = append(events, ev) }

	if err := InstallTool(context.Background(), d, "exiftool", cb); err != nil {
		t.Fatalf("InstallTool: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[2].Stage != "verify" {
		t.Errorf("last event stage = %q, want verify", events[2].Stage)
	}
}

func TestInstallTool_PropagatesError(t *testing.T) {
	d := &fakeDownloader{err: errors.New("hash mismatch")}
	err := InstallTool(context.Background(), d, "ffmpeg", func(deps.ProgressEvent) {})
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("expected hash mismatch error, got %v", err)
	}
}

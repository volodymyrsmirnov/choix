package firstrun

import (
	"context"
	"errors"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/config"
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

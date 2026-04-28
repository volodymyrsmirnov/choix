// Package firstrun detects missing runtime dependencies (exiftool, ffmpeg,
// ONNX models) and provides helpers for the first-run setup wizard. It does
// not start any servers; HTTP handlers live alongside this package and can be
// mounted by the HTTP server in internal/server (plan 2).
package firstrun

import (
	"context"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/config"
)

// Resolver matches the subset of internal/deps used here so tests can fake it.
type Resolver interface {
	Resolve(ctx context.Context, name string) (string, error)
}

// ModelStore is the subset of internal/ai/local model registry used here.
type ModelStore interface {
	Has(ctx context.Context, kind local.ModelKind) (bool, error)
}

// SetupState describes what (if anything) needs to be installed before
// choix can run end-to-end. Only the CLIP model is required today; the
// AI top-pick flow that needed sharpness, face landmarks, and NIMA was
// retired and those models are no longer requested.
type SetupState struct {
	RequiresExifTool bool
	RequiresFFmpeg   bool
	RequiresModels   []local.ModelKind
}

// IsReady returns true when no tool installs or model downloads are
// required.
func (s SetupState) IsReady() bool {
	return !s.RequiresExifTool && !s.RequiresFFmpeg && len(s.RequiresModels) == 0
}

// Detect probes the environment and returns a SetupState. It does not
// modify any state; it never blocks the UI.
func Detect(ctx context.Context, r Resolver, ms ModelStore, _ config.Config) (SetupState, error) {
	var st SetupState

	if _, err := r.Resolve(ctx, "exiftool"); err != nil {
		st.RequiresExifTool = true
	}
	if _, err := r.Resolve(ctx, "ffmpeg"); err != nil {
		st.RequiresFFmpeg = true
	}

	for _, k := range []local.ModelKind{local.ModelCLIP} {
		ok, err := ms.Has(ctx, k)
		if err != nil {
			return SetupState{}, err
		}
		if !ok {
			st.RequiresModels = append(st.RequiresModels, k)
		}
	}

	return st, nil
}

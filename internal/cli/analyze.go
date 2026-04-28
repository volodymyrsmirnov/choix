package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/appdir"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// localAnalyzeAdapter adapts *local.Analyzer to pipeline.AnalyzeProcessor.
// It looks up the file's tier-1 thumbnail (from the thumbs table, falling
// back to the deterministic CachePath layout) and forwards to the package's
// Analyzer, which computes pure-Go signals unconditionally and ML signals
// when their models are present.
type localAnalyzeAdapter struct {
	a    *local.Analyzer
	s    *store.Store
	root string
}

func (l *localAnalyzeAdapter) Analyze(ctx context.Context, fileID int64) error {
	thumbPath, err := resolveTier1ThumbPath(l.s, l.root, fileID)
	if err != nil {
		return err
	}
	return l.a.Analyze(ctx, fileID, thumbPath)
}

// resolveTier1ThumbPath returns the absolute path to the file's tier-1 thumb,
// preferring the thumbs row but falling back to the deterministic on-disk
// cache layout when the row is missing (e.g., legacy databases or tests).
func resolveTier1ThumbPath(s *store.Store, root string, fileID int64) (string, error) {
	t, err := s.Thumbs().Get(fileID, thumb.TierThumb)
	if err == nil && t.RelPath != "" {
		return filepath.Join(root, t.RelPath), nil
	}
	abs := thumb.CachePath(root, fileID, thumb.TierThumb)
	if _, statErr := os.Stat(abs); statErr == nil {
		return abs, nil
	}
	return "", fmt.Errorf("tier-1 thumb missing for file %d: %w", fileID, err)
}

// modelsDir returns the user-level ONNX models directory. The directory is
// created on first use; missing model files are tolerated by local.Analyzer
// (it skips signals it has no model for).
func modelsDir() string { return appdir.Models() }

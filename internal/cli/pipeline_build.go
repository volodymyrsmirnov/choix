package cli

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/deps"
	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// storeIDLister adapts *store.Store to pipeline.IDLister.
type storeIDLister struct{ s *store.Store }

func (l *storeIDLister) ListByStatus(ctx context.Context, status string, limit int) ([]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return l.s.Files().IDsByStatus(status, limit)
}

func (l *storeIDLister) CountByStatus(ctx context.Context, status string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return l.s.Files().CountByStatus(status)
}

// metaProcessorAdapter adapts meta.Extractor to pipeline.MetaProcessor.
type metaProcessorAdapter struct {
	ext  *meta.Extractor
	root string
	s    *store.Store
}

func (a *metaProcessorAdapter) Process(ctx context.Context, fileID int64) error {
	f, err := a.s.Files().GetByID(fileID)
	if err != nil {
		return fmt.Errorf("meta: get file %d: %w", fileID, err)
	}
	absPath := filepath.Join(a.root, f.Path)
	return a.ext.Process(ctx, fileID, absPath)
}

// resolveMediaTools locates exiftool + ffmpeg on PATH. exiftool is
// load-bearing (metadata extraction); ffmpeg falls back to a bare-name
// runner because /full/ transcodes only invoke it as a last-resort fallback
// after sips and exiftool-preview both fail. Returned values are non-nil
// on success.
func resolveMediaTools() (*meta.ExifTool, *deps.Runner, error) {
	exiftoolBin, err := exec.LookPath("exiftool")
	if err != nil {
		return nil, nil, fmt.Errorf("exiftool not found in PATH: %w", err)
	}
	etool := meta.New(exiftoolBin)

	var ffRunner *deps.Runner
	if fp, err := exec.LookPath("ffmpeg"); err == nil {
		ffRunner = deps.NewRunner(fp)
	} else {
		ffRunner = &deps.Runner{Path: "ffmpeg"}
	}
	return etool, ffRunner, nil
}

// buildPipelineCore wires the engine pipeline. It is the single source of
// truth for stage concurrency, the grouper threshold, and the external-tool
// resolution rules.
//
// The Analyzer is returned separately so the serve adapter can Close() it
// after the pipeline goroutine exits — destroying ONNX C sessions while
// inference is in flight is a use-after-free.
func buildPipelineCore(st *store.Store, scanRoot string, cfg *config.Config) (*pipeline.Pipeline, *local.Analyzer, *scanner.Scanner, error) {
	etool, ffRunner, err := resolveMediaTools()
	if err != nil {
		return nil, nil, nil, err
	}

	tb := &thumb.Builder{
		ScanRoot: scanRoot,
		ExifTool: etool,
		Ffmpeg:   ffRunner,
		Store:    st,
	}

	bucketSec := int64(cfg.BucketSizeSec)
	if bucketSec <= 0 {
		bucketSec = 600
	}
	threshold := cfg.VisualClusterThreshold
	if threshold <= 0 {
		threshold = 0.92
	}
	grp := group.NewGrouper(st, bucketSec, threshold, cfg.CrossDeviceMerging)

	models := local.NewModelStore(modelsDir())
	analyzer := local.NewAnalyzer(st, models)

	sc := scanner.New(scanRoot, st)
	sc.SetPicksDir(effectivePicksDir(st, cfg))

	p := &pipeline.Pipeline{
		Store:         &storeIDLister{s: st},
		Scanner:       sc,
		MetaExtractor: &metaProcessorAdapter{ext: meta.NewExtractor(etool, st, scanRoot), root: scanRoot, s: st},
		ThumbBuilder:  tb,
		Analyzer:      &localAnalyzeAdapter{a: analyzer, s: st, root: scanRoot},
		Grouper:       grp,
		Concurrency: map[string]int{
			"metadata": runtime.NumCPU(),
			"thumb":    runtime.NumCPU(),
			"analyze":  1,
		},
		BatchSize: 100,
	}
	return p, analyzer, sc, nil
}

// effectivePicksDir resolves the picks export directory from the live
// machine-wide config. KV is no longer consulted — settings are TOML-only
// (see internal/server/settings_migration.go for the one-shot upgrade path).
//
// Absolute paths are returned as-is — they cannot live under the scan root,
// so they're irrelevant to the discovery walker; SetPicksDir will trim
// leading separators and the resulting absolute-style path will never match
// a relative walk entry.
func effectivePicksDir(_ *store.Store, cfg *config.Config) string {
	if cfg != nil && cfg.PicksDir != "" {
		return cfg.PicksDir
	}
	return "picks"
}

// loadEffectiveConfig resolves the layered configuration for a scan root:
// built-in defaults → global config.toml (where Settings persists) →
// per-folder .choix/config.toml → environment. CLI flag overrides are
// layered on by the caller after this returns. Reading the global config
// here is the load-bearing fix that lets `choix scan` honour the
// VisualClusterThreshold a user just changed in Settings.
func loadEffectiveConfig(scanRoot string) (config.Config, error) {
	g, err := config.Load()
	if err != nil {
		return config.Config{}, fmt.Errorf("load global config: %w", err)
	}
	cfg := *g
	if p, err := config.LoadFile(filepath.Join(scanRoot, ".choix", "config.toml")); err == nil {
		config.ApplyTOML(&cfg, p)
	}
	if err := config.ApplyEnv(&cfg); err != nil {
		return cfg, fmt.Errorf("apply env: %w", err)
	}
	return cfg, nil
}

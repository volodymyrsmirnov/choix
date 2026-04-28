package pipeline

import (
	"context"
	"log/slog"
)

// Stage represents one phase of the per-file pipeline. Implementations are
// thin adapters that move a file from one scan_status to the next. Stages
// must be idempotent for a given file_id: invoking Process twice with the
// same id is safe (the underlying repo updates are upserts).
type Stage interface {
	Name() string
	Process(ctx context.Context, fileID int64) error
}

// MetaProcessor is the surface needed from the meta package.
type MetaProcessor interface {
	Process(ctx context.Context, fileID int64) error
}

// ThumbProcessor is the surface needed from the thumb package.
type ThumbProcessor interface {
	Build(ctx context.Context, fileID int64) error
}

// AnalyzeProcessor is the surface needed from the ai/local package.
type AnalyzeProcessor interface {
	Analyze(ctx context.Context, fileID int64) error
}

// logFailure emits a structured warning for per-file pipeline errors. Used
// uniformly across all stage adapters so production runs surface failing
// file IDs and the underlying error text.
func logFailure(stage string, fileID int64, err error) {
	if err == nil {
		return
	}
	slog.Warn("stage failed", "stage", stage, "file_id", fileID, "err", err.Error())
}

// MetadataStage adapts a MetaProcessor to the Stage interface.
type MetadataStage struct{ Extractor MetaProcessor }

func (s *MetadataStage) Name() string { return "metadata" }
func (s *MetadataStage) Process(ctx context.Context, fileID int64) error {
	err := s.Extractor.Process(ctx, fileID)
	logFailure(s.Name(), fileID, err)
	return err
}

// ThumbStage adapts a ThumbProcessor to the Stage interface.
type ThumbStage struct{ Builder ThumbProcessor }

func (s *ThumbStage) Name() string { return "thumb" }
func (s *ThumbStage) Process(ctx context.Context, fileID int64) error {
	err := s.Builder.Build(ctx, fileID)
	logFailure(s.Name(), fileID, err)
	return err
}

// AnalyzeStage adapts an AnalyzeProcessor to the Stage interface.
type AnalyzeStage struct{ Analyzer AnalyzeProcessor }

func (s *AnalyzeStage) Name() string { return "analyze" }
func (s *AnalyzeStage) Process(ctx context.Context, fileID int64) error {
	err := s.Analyzer.Analyze(ctx, fileID)
	logFailure(s.Name(), fileID, err)
	return err
}

// RegisterStages returns the ordered list of per-file stages built from
// the Pipeline config. The cluster stage is intentionally not in this
// list — clustering operates over (device_key, time_bucket) groups, not
// per-file, and is invoked separately in Pipeline.Run.
func RegisterStages(p *Pipeline) []Stage {
	return []Stage{
		&MetadataStage{Extractor: p.MetaExtractor},
		&ThumbStage{Builder: p.ThumbBuilder},
		&AnalyzeStage{Analyzer: p.Analyzer},
	}
}

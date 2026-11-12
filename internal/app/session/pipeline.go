package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/analyze"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/group"
	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// Pipeline tuning. Feature-print calls are independent and already bounded
// by platform.NativeSlots, so analysis runs on a few workers.
const (
	sessionAnalyzeWorkers = 4
	sessionBatchSize      = 100
)

// Grouping defaults applied when a setting is unset (zero).
const (
	sessionDefaultBucketSec  = 600
	sessionDefaultSimilarity = 0.90
)

// sessionClusterMethod names how clusters are built: complete-link over the
// feature prints stamped analyze.PrintID. It is part of ClusterParams, so a
// folder clustered another way (by an earlier build) re-clusters once.
const sessionClusterMethod = analyze.PrintID + "+complete"

// ClusterParams identifies the settings and the method a clustering was
// built with. The value is stored in the folder's kv under "cluster_params"
// after every clustering; EnsureClustered compares it with the current
// settings.
func ClusterParams(cfg config.Config) string {
	return fmt.Sprintf("b=%d;t=%.4f;x=%t;v=%t;m=%s",
		cfg.BucketSizeSec, cfg.VisualSimilarity, cfg.CrossDeviceMerging, cfg.VisualGrouping, sessionClusterMethod)
}

// NewPipeline wires the folder's pipeline (ported from v1's
// internal/cli/pipeline_build.go): metadata and thumbnails on NumCPU
// workers, analysis on sessionAnalyzeWorkers, batches of 100, and a progress
// channel the pipeline's Reporter feeds without blocking.
//
// settings is read live:
//   - each walk skips the folder's effective picks directory and its other
//     export directories, except one CheckDir refuses (it holds scanned
//     originals), which is walked as originals;
//   - the analyze stage makes feature prints only while visual grouping is
//     on;
//   - the cluster stage is the session's live clusterer (see Recluster).
//
// Every native call the stages make is counted in the session's Inflight,
// whatever context the caller runs the pipeline with.
func (s *Session) NewPipeline(settings func() config.Config) (*pipeline.Pipeline, <-chan pipeline.Progress) {
	progress := make(chan pipeline.Progress, pipeline.ProgressBufferSize)

	var printer analyze.Printer // a nil Deps.Vision must stay a nil interface
	if s.deps.Vision != nil {
		printer = &sessionPrinter{vision: s.deps.Vision, settings: settings}
	}

	// A file whose stage fails while the folder is unreachable is left as it
	// was (pipeline.ErrFolderUnavailable), never marked failed.
	ext := meta.NewExtractor(&meta.NativeReader{Stills: s.deps.Stills, Video: s.deps.Video}, s.Store, s.Root)
	ext.FailureGuard = s.reachable
	analyzer := analyze.New(s.Store, printer)
	analyzer.FailureGuard = s.reachable

	p := &pipeline.Pipeline{
		Store:         sessionIDLister{st: s.Store},
		Scanner:       &sessionWalker{s: s, settings: settings, sc: scanner.New(s.Root, s.Store)},
		MetaExtractor: &sessionMetaStage{s: s, ext: ext},
		ThumbBuilder: &sessionThumbStage{s: s, b: &thumb.Builder{
			ScanRoot:     s.Root,
			Store:        s.Store,
			Stills:       s.deps.Stills,
			Video:        s.deps.Video,
			FailureGuard: s.reachable,
		}},
		Analyzer: &sessionAnalyzeStage{s: s, a: analyzer},
		Grouper:  &sessionClusterer{s: s, settings: settings},
		Reporter: pipeline.NewReporter(progress),
		Concurrency: map[string]int{
			"metadata": runtime.NumCPU(),
			"thumb":    runtime.NumCPU(),
			"analyze":  sessionAnalyzeWorkers,
		},
		BatchSize: sessionBatchSize,
	}
	return p, progress
}

// Recluster rebuilds the folder's clusters once with the settings in force
// when it holds the clustering lock, and stores their ClusterParams.
func (s *Session) Recluster(ctx context.Context, settings func() config.Config) error {
	return s.recluster(ctx, settings, nil)
}

// recluster is Recluster reporting the rebuild's progress in files (see
// group.Grouper.RebuildAllProgress); progress may be nil.
func (s *Session) recluster(ctx context.Context, settings func() config.Config, progress func(done, total int)) error {
	unlock, err := s.lockClustering(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.clusterLocked(ctx, settings(), progress)
}

// EnsureClustered reclusters when the folder's clusters were built with
// other settings than settings() and reports whether it did. A folder with
// no stored cluster_params (last clustered by v1) is left as it is and
// nothing is stored: it keeps its clusters until something rebuilds them.
func (s *Session) EnsureClustered(ctx context.Context, settings func() config.Config) (bool, error) {
	unlock, err := s.lockClustering(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	stored, err := s.Store.KV().Get(sessionKVClusterParams)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("session: read cluster params: %w", err)
	}
	cfg := settings()
	if stored == ClusterParams(cfg) {
		return false, nil
	}
	if err := s.clusterLocked(ctx, cfg, nil); err != nil {
		return false, err
	}
	return true, nil
}

// lockClustering takes the session's clustering lock, giving up when ctx
// ends. The returned func releases it.
func (s *Session) lockClustering(ctx context.Context) (func(), error) {
	s.clusterEvent(sessionClusterEventWait, "")
	select {
	case s.clusterLock <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("session: wait for clustering: %w", ctx.Err())
	}
	s.clusterEvent(sessionClusterEventLocked, "")
	return func() { <-s.clusterLock }, nil
}

// clusterLocked rebuilds every cluster with cfg and stores its params.
// Called holding the clustering lock.
func (s *Session) clusterLocked(ctx context.Context, cfg config.Config, progress func(done, total int)) error {
	bucket := int64(cfg.BucketSizeSec)
	if bucket <= 0 {
		bucket = sessionDefaultBucketSec
	}
	similarity := cfg.VisualSimilarity
	if similarity <= 0 {
		similarity = sessionDefaultSimilarity
	}
	g := group.NewGrouper(s.Store, bucket, similarity, cfg.CrossDeviceMerging)
	if cfg.VisualGrouping {
		g.UsePrints(analyze.PrintID)
	}
	if err := g.RebuildAllProgress(ctx, progress); err != nil {
		return fmt.Errorf("session: cluster: %w", err)
	}
	params := ClusterParams(cfg)
	if err := s.Store.KV().Set(sessionKVClusterParams, params); err != nil {
		return fmt.Errorf("session: store cluster params: %w", err)
	}
	s.clusterEvent(sessionClusterEventStored, params)
	return nil
}

func (s *Session) clusterEvent(event, params string) {
	if s.clusterHook != nil {
		s.clusterHook(event, params)
	}
}

// scannerSkipDirs returns the directories a walk skips (and whose rows it
// evicts): the effective picks directory and the other export directories,
// each normalised by sessionCleanPicksDir and only when it is a valid picks
// directory that CheckDir accepts; duplicates (case-insensitively, like file
// identity) are dropped. A directory holding scanned originals is left out,
// so it is walked as originals and nothing under it is evicted.
func (s *Session) scannerSkipDirs(cfg config.Config) ([]string, error) {
	dir, others, err := picks.EffectiveDir(s.Store, cfg.PicksDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range append([]string{dir}, others...) {
		d, err := sessionCleanPicksDir(d)
		if err != nil {
			continue // outside the folder, the folder itself or .choix: nothing to skip
		}
		if slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, d) }) {
			continue
		}
		err = picks.CheckDir(s.Store, d)
		switch {
		case errors.Is(err, picks.ErrDirHasOriginals):
			slog.Info("session: picks directory holds originals; walking it", "folder", s.FolderID, "dir", d)
			continue
		case err != nil:
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// sessionWalker is the pipeline's discover stage: the scanner, with the
// directories to skip worked out from the live settings at each walk.
type sessionWalker struct {
	s        *Session
	settings func() config.Config
	sc       *scanner.Scanner
}

func (w *sessionWalker) Walk(ctx context.Context) error {
	dirs, err := w.s.scannerSkipDirs(w.settings())
	if err != nil {
		return fmt.Errorf("session: picks directories to skip: %w", err)
	}
	w.sc.SetSkipDirs(dirs)
	return w.sc.Walk(ctx)
}

var _ pipeline.ProgressGrouper = (*sessionClusterer)(nil)

// sessionClusterer is the pipeline's Grouper: the session's live clusterer.
type sessionClusterer struct {
	s        *Session
	settings func() config.Config
}

func (c *sessionClusterer) RebuildAll(ctx context.Context) error {
	return c.s.Recluster(ctx, c.settings)
}

// RebuildAllProgress makes the cluster stage report done of total files
// (pipeline.ProgressGrouper).
func (c *sessionClusterer) RebuildAllProgress(ctx context.Context, progress func(done, total int)) error {
	return c.s.recluster(ctx, c.settings, progress)
}

// sessionIDLister adapts *store.Store to pipeline.IDLister.
type sessionIDLister struct{ st *store.Store }

func (l sessionIDLister) ListByStatus(ctx context.Context, status string, limit int) ([]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return l.st.Files().IDsByStatus(status, limit)
}

func (l sessionIDLister) CountByStatus(ctx context.Context, status string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return l.st.Files().CountByStatus(status)
}

// sessionMetaStage adapts meta.Extractor to pipeline.MetaProcessor.
type sessionMetaStage struct {
	s   *Session
	ext *meta.Extractor
}

func (m *sessionMetaStage) Process(ctx context.Context, fileID int64) error {
	f, err := m.s.Store.Files().GetByID(fileID)
	if err != nil {
		return fmt.Errorf("meta: get file %d: %w", fileID, err)
	}
	return m.ext.Process(m.s.Context(ctx), fileID, filepath.Join(m.s.Root, filepath.FromSlash(f.Path)))
}

// sessionThumbStage runs thumb.Builder under the session's context.
type sessionThumbStage struct {
	s *Session
	b *thumb.Builder
}

func (t *sessionThumbStage) Build(ctx context.Context, fileID int64) error {
	return t.b.Build(t.s.Context(ctx), fileID)
}

// sessionAnalyzeStage adapts analyze.Analyzer to pipeline.AnalyzeProcessor:
// it hands the analyzer the file's preview tier (at most 1600 px). A file
// without one is failed, so it cannot stall the stage — unless the folder is
// unreachable, which leaves it as it was (pipeline.ErrFolderUnavailable). The
// analyzer runs under the session's context, so its native calls are
// counted in the session's Inflight.
type sessionAnalyzeStage struct {
	s *Session
	a *analyze.Analyzer
}

func (a *sessionAnalyzeStage) Analyze(ctx context.Context, fileID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := sessionThumbPath(a.s.Store, a.s.Root, fileID, thumb.TierPreview)
	if err != nil {
		err = fmt.Errorf("cannot analyze: %w", err)
		if !a.s.reachable() {
			return fmt.Errorf("%w: %w", pipeline.ErrFolderUnavailable, err)
		}
		if ferr := a.s.Store.Files().UpdateStatus(fileID, "failed", err.Error()); ferr != nil {
			return errors.Join(err, fmt.Errorf("record failure: %w", ferr))
		}
		return err
	}
	return a.a.Analyze(a.s.Context(ctx), fileID, p)
}

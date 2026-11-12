// The folder job: everything one folder needs, run by the Queue — a few
// folders at a time, never one folder twice at once — the pipeline pass, the
// feature-print backfill, the refresh of the folder's cached stats and previews once
// its clusters are final, and the quality-rating pass.

package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/analyze"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/quality"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// QualityRun tracks the quality-rating passes of the jobs. Begin is called
// when a pass of the folder folderID starts; the pass runs under passCtx, which the tracker cancels
// when the quality_rating setting is switched off. progress is called after
// every photo with the photos dealt with and the number pending at the
// start; end is called once, when the pass ends, with the photos dealt with
// and the pass's error (nil when it finished).
type QualityRun interface {
	Begin(ctx context.Context, folderID string) (passCtx context.Context, progress func(done, total int), end func(rated int, err error))
}

// JobOptions configure a Job.
type JobOptions struct {
	// Sessions hands out the folders' sessions. Required.
	Sessions *Manager
	// Settings returns the live settings; each step reads them when it
	// runs. nil: the built-in defaults.
	Settings func() config.Config
	// Quality tracks the rating pass. nil: the pass runs untracked.
	Quality QualityRun
	// Emit receives folder:changed. nil: no events.
	Emit *events.Coalescer
	// AfterJob refreshes the folder's cached stats and previews. It runs once
	// per job that reaches it, as soon as the folder's clusters are final for
	// the job — before the quality pass, which changes neither — under the
	// session's context, so its native calls are counted in the session's
	// Inflight. nil: nothing.
	AfterJob func(ctx context.Context, s *Session)
	// BeforeScore runs before each photo of a rating pass is scored, after
	// the job's own checks. Tests use it to hold a photo in flight. nil:
	// nothing.
	BeforeScore func()
}

// Reasons of the folder:changed events a job emits.
const (
	jobReasonClusters = "clusters"
	jobReasonRatings  = "ratings"
)

// jobRatingsInterval is the least time between two folder:changed "ratings"
// events of one pass.
const jobRatingsInterval = time.Second

// errJobFolderGone reports that the folder's directory no longer exists.
var errJobFolderGone = errors.New("session: the folder's directory is gone")

// Job is the queue's Runner: one run does everything one folder needs.
// Steps, in order, each reading the live settings when it starts:
//
//  1. Acquire the folder's session.
//  2. The pipeline: a full job walks the folder and processes every file
//     (discover → cluster, resuming from each file's status); a backfill
//     only resumes, and only when files are pending.
//  3. Print backfill: while visual grouping is on, files analyzed without a
//     feature print stamped analyze.PrintID are rewound and analyzed again,
//     which reclusters.
//  4. EnsureClustered, for a setting changed after the cluster stage.
//  5. The clusters are final: AfterJob, then folder:changed "clusters", so
//     the open folder shows its grid and the Library row its counts and
//     previews while the ratings are computed. Without a quality pass to
//     come, this is the job's last step.
//  6. The quality-rating pass, while quality_rating is on. It writes only
//     ratings: its photos are announced by
//     folder:changed "ratings", the last one as the pass ends, and nothing
//     published in step 5 is refreshed or announced again.
//
// A broken file never fails the job: the pipeline marks it failed and goes
// on. Run is safe for concurrent use.
type Job struct {
	o JobOptions
	// now is the clock that throttles the ratings events.
	now func() time.Time
}

var _ Runner = (*Job)(nil)

// NewJob returns a Job working with o. It panics when o.Sessions is nil.
func NewJob(o JobOptions) *Job {
	if o.Sessions == nil {
		panic("session: JobOptions.Sessions is required")
	}
	if o.Settings == nil {
		o.Settings = config.Defaults
	}
	return &Job{o: o, now: time.Now}
}

// Run does the folder's job of kind and reports the pipeline's progress
// through report. Its outcome:
//   - Stopped when ctx is cancelled (also while the Manager shuts down);
//   - Busy, a suspension that is not a strike, for ErrBusy from Acquire:
//     the folder's previous session, at a location the folder left, is
//     still closing;
//   - Suspended when the folder is unreachable: library.ErrOffline or
//     library.ErrAccessLost from Acquire, or a step error, or the check
//     between two steps, finding that the folder's directory no longer
//     exists;
//   - Failed with ReadOnlyMessage or UnsafeStateMessage for ErrReadOnly and
//     ErrUnsafeState, and with the error text for any other error;
//   - Done otherwise.
//
// The session is always released.
func (j *Job) Run(ctx context.Context, folderID string, kind JobKind, report func(p events.Progress, phase string)) (Outcome, string) {
	if ctx.Err() != nil {
		return Stopped, ""
	}
	s, release, err := j.o.Sessions.Acquire(folderID)
	if err != nil {
		return j.outcome(ctx, folderID, j.recordedRoot(folderID), err)
	}
	defer release()
	if err := j.run(ctx, s, kind, report); err != nil {
		return j.outcome(ctx, folderID, s.Root, err)
	}
	return Done, ""
}

// run does steps 2 to 6 on s (see Job).
func (j *Job) run(ctx context.Context, s *Session, kind JobKind, report func(events.Progress, string)) error {
	sctx := s.Context(ctx)
	p, progress := s.NewPipeline(j.o.Settings)
	stop := jobForward(progress, report)
	defer stop()

	// 2. The pipeline pass.
	if kind == JobBackfill {
		n, err := p.Pending(sctx)
		if err != nil {
			return fmt.Errorf("session: files pending: %w", err)
		}
		if n > 0 {
			if err := p.Resume(sctx); err != nil {
				return fmt.Errorf("session: resume the pipeline: %w", err)
			}
		}
	} else if err := p.Run(sctx); err != nil {
		return fmt.Errorf("session: pipeline: %w", err)
	}
	if err := jobBetween(ctx, s); err != nil {
		return err
	}

	// 3. Print backfill.
	if err := j.backfillPrints(sctx, s, p); err != nil {
		return err
	}
	if err := jobBetween(ctx, s); err != nil {
		return err
	}

	// 4. A setting changed after the cluster stage.
	if _, err := s.EnsureClustered(sctx, j.o.Settings); err != nil {
		return fmt.Errorf("session: ensure clustered: %w", err)
	}
	if err := jobBetween(ctx, s); err != nil {
		return err
	}

	// 5. The clusters are final: stats and previews, then tell the folder
	// view, before the quality pass. The pipeline's last progress is
	// delivered first. AfterJob's native calls, if any, are counted in the
	// session's Inflight.
	stop()
	if j.o.AfterJob != nil {
		j.o.AfterJob(sctx, s)
	}
	j.emit(s.FolderID, jobReasonClusters)
	if !j.rating() {
		// No pass to come: the work is done, and a cancellation during
		// AfterJob no longer stops the job.
		return nil
	}
	// A pass is to come: this was not the job's end, so a cancellation
	// (preemption, quit) or a folder gone meanwhile still stops it here.
	if err := jobBetween(ctx, s); err != nil {
		return err
	}

	// 6. Quality rating.
	if err := j.rate(ctx, s); err != nil {
		return err
	}
	return jobBetween(ctx, s)
}

// backfillPrints rewinds the files analyzed without a feature print stamped
// analyze.PrintID (grouping was off, or an earlier build made them) and
// analyzes them again (which reclusters), when visual grouping is on, the
// session can make prints and such files exist. A file Vision could not
// print is stamped, so it is not rewound again.
func (j *Job) backfillPrints(ctx context.Context, s *Session, p *pipeline.Pipeline) error {
	if !j.o.Settings().VisualGrouping || s.deps.Vision == nil {
		return nil
	}
	files := s.Store.Files()
	n, err := files.CountAnalyzedMissingPrint(analyze.PrintID)
	if err != nil {
		return fmt.Errorf("session: print backfill: %w", err)
	}
	if n == 0 {
		return nil
	}
	if _, err := files.BumpAnalyzedMissingPrint(analyze.PrintID); err != nil {
		return fmt.Errorf("session: print backfill: %w", err)
	}
	if err := p.Resume(ctx); err != nil {
		return fmt.Errorf("session: print backfill: %w", err)
	}
	return nil
}

// rate runs the quality-rating pass when quality_rating is on. The rating is advisory: the pass's errors are handed to the
// tracker and logged, not returned. The pass also stops at the next photo
// once the setting reads off, whether or not a tracker cancelled it, and as
// soon as the folder is found unreachable — before a photo, or by the
// scorer's guard while reading its preview. That is the one error rate
// returns (wrapping errJobFolderGone or pipeline.ErrFolderUnavailable), so
// the job is suspended even if the folder is back by the time it returns.
//
// The photos rated are announced by folder:changed "ratings", throttled; the
// growth held back by the throttle is announced as the pass ends, unless the
// folder was found unreachable.
func (j *Job) rate(ctx context.Context, s *Session) error {
	if !j.rating() {
		return nil
	}
	passCtx, progress, end := ctx, func(int, int) {}, func(int, error) {}
	if j.o.Quality != nil {
		var p func(int, int)
		var e func(int, error)
		passCtx, p, e = j.o.Quality.Begin(ctx, s.FolderID)
		if p != nil {
			progress = p
		}
		if e != nil {
			end = e
		}
	}
	passCtx, cancel := context.WithCancelCause(passCtx)
	defer cancel(nil)

	notify, flush := j.ratingsNotifier(s.FolderID)
	sc := &jobScorer{
		Scorer:  s.Scorer(),
		enabled: func() bool { return j.o.Settings().QualityRating },
		root:    s.Root,
		stop:    cancel,
		rated:   notify,
		hook:    j.o.BeforeScore,
	}
	pass := &quality.Pass{Scorer: sc, OnProgress: progress}
	rated, err := pass.Run(s.Context(passCtx))
	end(rated, err)
	cause := context.Cause(passCtx)
	switch {
	case errors.Is(cause, errJobFolderGone), errors.Is(cause, pipeline.ErrFolderUnavailable):
		return fmt.Errorf("session: quality rating: %w", cause)
	}
	flush()
	switch {
	case err == nil, ctx.Err() != nil:
	case passCtx.Err() != nil:
		slog.Info("session: quality rating pass stopped", "folder", s.FolderID, "rated", rated, "cause", cause)
	default:
		slog.Warn("session: quality rating pass", "folder", s.FolderID, "rated", rated, "err", err)
	}
	return nil
}

// errJobQualityOff is the cause of a pass stopped because quality_rating
// reads off.
var errJobQualityOff = errors.New("session: the quality rating was switched off")

// jobScorer is the session's scorer as a pass sees it: it reports every
// photo dealt with, and stops the pass — cancelling it with the reason as
// its cause — instead of scoring once the setting reads off or the folder is
// unreachable, and when the scorer found the folder unreachable while
// reading a preview.
type jobScorer struct {
	quality.Scorer
	enabled func() bool
	root    string
	stop    context.CancelCauseFunc
	rated   func()
	hook    func() // test hook, nil in production
}

func (sc *jobScorer) Score(ctx context.Context, item store.QualityItem) error {
	switch {
	case !sc.enabled():
		sc.stop(errJobQualityOff)
		return context.Canceled
	case !sessionRootReachable(sc.root):
		sc.stop(fmt.Errorf("%w: %s", errJobFolderGone, sc.root))
		return context.Canceled
	}
	if sc.hook != nil {
		sc.hook()
	}
	err := sc.Scorer.Score(ctx, item)
	switch {
	case errors.Is(err, pipeline.ErrFolderUnavailable):
		sc.stop(err)
		return context.Canceled
	case err != nil:
		return err
	}
	sc.rated()
	return nil
}

// ratingsNotifier returns the func a pass calls each time the photos it
// dealt with grow, rated, and flush, called once the pass has returned.
// rated emits folder:changed "ratings" at once, then at most once per
// jobRatingsInterval; flush emits one more when rated held a growth back
// since its last event (the folder's clusters were announced before the
// pass, so no later event carries it). Neither is safe for concurrent use
// (a pass is sequential, and flush follows it).
func (j *Job) ratingsNotifier(folderID string) (rated, flush func()) {
	var last time.Time
	first, pending := true, false
	rated = func() {
		now := j.now()
		if !first && now.Sub(last) < jobRatingsInterval {
			pending = true
			return
		}
		first, last, pending = false, now, false
		j.emit(folderID, jobReasonRatings)
	}
	flush = func() {
		if pending {
			pending = false
			j.emit(folderID, jobReasonRatings)
		}
	}
	return rated, flush
}

// rating reports whether a quality-rating pass is due: quality_rating is on.
func (j *Job) rating() bool {
	return j.o.Settings().QualityRating
}

// emit sends folder:changed for the folder at once. Each reason has its own
// coalescing key, so it never drops another reason's pending event.
func (j *Job) emit(folderID, reason string) {
	if j.o.Emit == nil {
		return
	}
	j.o.Emit.EmitNow(events.FolderChanged, folderID+"\x00"+reason,
		events.FolderChangedEvent{FolderID: folderID, Reason: reason})
}

// outcome classifies the error that ended a run on the folder whose root is
// root ("" when unknown).
func (j *Job) outcome(ctx context.Context, folderID, root string, err error) (Outcome, string) {
	switch {
	case ctx.Err() != nil, errors.Is(err, ErrShuttingDown):
		return Stopped, ""
	case errors.Is(err, ErrBusy):
		slog.Info("session: the folder's previous session is still closing; the job is deferred", "folder", folderID)
		return Busy, ""
	case errors.Is(err, library.ErrOffline), errors.Is(err, library.ErrAccessLost),
		errors.Is(err, errJobFolderGone), errors.Is(err, pipeline.ErrFolderUnavailable),
		root != "" && !sessionRootReachable(root):
		slog.Info("session: folder unreachable; the job is suspended", "folder", folderID, "err", err)
		return Suspended, ""
	case errors.Is(err, ErrReadOnly):
		slog.Warn("session: folder job failed", "folder", folderID, "err", err)
		return Failed, ReadOnlyMessage
	case errors.Is(err, ErrUnsafeState):
		slog.Warn("session: folder job failed", "folder", folderID, "err", err)
		return Failed, UnsafeStateMessage
	}
	return Failed, err.Error()
}

// recordedRoot returns the path the Library last recorded for the folder, or
// "" when it cannot be read.
func (j *Job) recordedRoot(folderID string) string {
	lib := j.o.Sessions.deps.Library
	if lib == nil {
		return ""
	}
	f, err := lib.Folder(folderID)
	if err != nil {
		return ""
	}
	return f.Path
}

// jobBetween is the check between two steps: ctx's error, or
// errJobFolderGone when the folder is no longer reachable.
func jobBetween(ctx context.Context, s *Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.reachable() {
		return fmt.Errorf("%w: %s", errJobFolderGone, s.Root)
	}
	return nil
}

// jobForward delivers the pipeline's progress to report (nil: discarded) on
// a goroutine. The returned func (idempotent) delivers what is still
// buffered and stops it; call it once the pipeline has returned.
func jobForward(progress <-chan pipeline.Progress, report func(events.Progress, string)) func() {
	send := func(p pipeline.Progress) {
		if report != nil {
			report(events.ProgressFromPipeline(p))
		}
	}
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case p := <-progress:
				send(p)
			case <-quit:
				for {
					select {
					case p := <-progress:
						send(p)
					default:
						return
					}
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(quit)
			<-done
		})
	}
}

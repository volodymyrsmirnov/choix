// Package quality runs the advisory photo-quality rating in the background:
// it downloads the model when it is missing and then rates every photo that
// has no rating yet. It is deliberately separate from the scan pipeline —
// a scan or a CLIP re-analysis never waits for it, and it steps aside
// whenever the pipeline is working.
package quality

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Fetcher provides the model file.
type Fetcher interface {
	// Has reports whether the model is installed.
	Has() bool
	// Download installs it, reporting bytes received and the expected size
	// (0 if unknown). It must be resumable and return promptly on cancel.
	Download(ctx context.Context, onProgress func(received, size int64)) error
}

// Scorer rates photos. See local.QualityScorer for the contract of Score:
// nil means the photo was dealt with (rated, unratable or superseded); an
// error leaves it pending.
type Scorer interface {
	CountPending() (int, error)
	ListPending(afterID int64, limit int) ([]store.QualityItem, error)
	Score(ctx context.Context, item store.QualityItem) error
}

// State is what the worker is doing right now.
type State string

const (
	StateDisabled       State = "disabled"        // the setting is off
	StateDownloading    State = "downloading"     // fetching the model
	StateDownloadFailed State = "download_failed" // gave up; Retry or restart
	StateWaiting        State = "waiting"         // photos pending, pipeline busy
	StateScoring        State = "scoring"         // rating photos
	StateIdle           State = "idle"            // nothing to do
	StateError          State = "error"           // scoring cannot run; restart
)

// Status is a snapshot for the UI (GET /api/quality).
type Status struct {
	Enabled bool  `json:"enabled"`
	State   State `json:"state"`
	// Download progress in bytes; Size is 0 while unknown.
	Received int64 `json:"received"`
	Size     int64 `json:"size"`
	// Scoring progress for the current pass, in photos.
	Done  int `json:"done"`
	Total int `json:"total"`
	// Error explains download_failed and error states.
	Error string `json:"error,omitempty"`
}

// Options configures a Worker. Fetcher and Scorer are required.
type Options struct {
	Fetcher Fetcher
	Scorer  Scorer
	// Busy reports whether the scan pipeline is running. Scoring pauses
	// while it is, which keeps ONNX inference to one model at a time and
	// leaves the CPU to the scan. nil → never busy.
	Busy func() bool
	// Enabled is the initial value of the setting.
	Enabled bool
	// IsUnavailable reports whether a Score error means scoring cannot
	// work at all in this process (as opposed to one failed photo).
	IsUnavailable func(error) bool
	// Backoff lists the pauses between download attempts; one more attempt
	// than entries is made. nil → 2 s, 10 s.
	Backoff []time.Duration
	// PollInterval is how often a paused pass re-checks Busy. 0 → 500 ms.
	PollInterval time.Duration
}

// maxConsecutiveFailures ends a scoring pass after this many photos in a
// row failed for reasons other than the photo itself.
const maxConsecutiveFailures = 3

const listBatch = 50

// Worker owns one goroutine that downloads the model and rates photos, in
// that order. Because a single goroutine does both, there are no competing
// attempts: changing the setting just flips a flag, cancels whatever is in
// flight and wakes the loop.
type Worker struct {
	opts Options

	mu       sync.Mutex
	enabled  bool
	status   Status
	opCancel context.CancelFunc // cancels the in-flight download or pass
	started  bool
	stop     context.CancelFunc
	done     chan struct{}

	wake chan struct{}
}

// New builds a Worker. Call Start to run it.
func New(opts Options) *Worker {
	if opts.Backoff == nil {
		opts.Backoff = []time.Duration{2 * time.Second, 10 * time.Second}
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 500 * time.Millisecond
	}
	w := &Worker{opts: opts, enabled: opts.Enabled, wake: make(chan struct{}, 1), done: make(chan struct{})}
	w.status = Status{Enabled: opts.Enabled, State: StateIdle}
	if !opts.Enabled {
		w.status.State = StateDisabled
	}
	return w
}

// Start launches the worker goroutine and triggers a first cycle. It is a
// no-op when already started.
func (w *Worker) Start(ctx context.Context) {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	ctx, w.stop = context.WithCancel(ctx)
	w.mu.Unlock()
	go w.run(ctx)
	w.Wake()
}

// Stop cancels whatever is in flight and waits for the goroutine to exit.
// After Stop returns the worker never touches the scorer or the store
// again, so their owners can close them.
func (w *Worker) Stop() {
	w.mu.Lock()
	started, stop := w.started, w.stop
	w.mu.Unlock()
	if !started {
		return
	}
	stop()
	<-w.done
}

// Wake asks the worker to look for work: call it when a scan finishes (new
// photos may be pending) or after a failure the user wants retried.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default: // a wake-up is already queued
	}
}

// SetEnabled applies the setting. Turning it off cancels an in-flight
// download (the partial file is kept) or scoring pass; turning it on starts
// the download or the pass.
func (w *Worker) SetEnabled(on bool) {
	w.mu.Lock()
	w.enabled = on
	w.status.Enabled = on
	cancel := w.opCancel
	w.mu.Unlock()
	if !on && cancel != nil {
		cancel()
	}
	w.Wake()
}

// Enabled reports the current setting.
func (w *Worker) Enabled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enabled
}

// Status returns a snapshot of what the worker is doing.
func (w *Worker) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *Worker) update(f func(s *Status)) {
	w.mu.Lock()
	f(&w.status)
	w.mu.Unlock()
}

func (w *Worker) setState(st State, errText string) {
	w.update(func(s *Status) { s.State, s.Error = st, errText })
}

func (w *Worker) run(ctx context.Context) {
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		}
		w.cycle(ctx)
	}
}

// cycle does one round: make sure the model is there, then rate what is
// pending. It returns when there is nothing left, on failure, or when
// cancelled; the loop then sleeps until the next wake-up.
func (w *Worker) cycle(ctx context.Context) {
	opCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w.mu.Lock()
	on := w.enabled
	if on {
		w.opCancel = cancel
	}
	w.mu.Unlock()
	if !on {
		w.setState(StateDisabled, "")
		return
	}
	defer func() {
		w.mu.Lock()
		w.opCancel = nil
		w.mu.Unlock()
		// Cancelled by the setting (not by shutdown): show it as off.
		if ctx.Err() == nil && !w.Enabled() {
			w.setState(StateDisabled, "")
		}
	}()

	if !w.opts.Fetcher.Has() && !w.download(opCtx) {
		return
	}
	w.scorePass(opCtx)
}

// download fetches the model with a few retries. It reports whether the
// model is now installed.
func (w *Worker) download(ctx context.Context) bool {
	slog.Info("quality model download starting")
	w.update(func(s *Status) { s.State, s.Error, s.Received, s.Size = StateDownloading, "", 0, 0 })
	start := time.Now()
	for attempt := 0; ; attempt++ {
		err := w.opts.Fetcher.Download(ctx, func(received, size int64) {
			w.update(func(s *Status) { s.Received, s.Size = received, size })
		})
		if err == nil {
			slog.Info("quality model download done", "elapsed_ms", time.Since(start).Milliseconds())
			return true
		}
		if ctx.Err() != nil {
			slog.Info("quality model download cancelled")
			return false
		}
		if attempt >= len(w.opts.Backoff) {
			// Non-fatal: choix keeps working, just without ratings.
			slog.Warn("quality model download failed; ratings are unavailable until it is retried", "attempts", attempt+1, "err", err)
			w.setState(StateDownloadFailed, err.Error())
			return false
		}
		slog.Warn("quality model download attempt failed, will retry", "attempt", attempt+1, "err", err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(w.opts.Backoff[attempt]):
		}
	}
}

// scorePass sweeps the pending photos once, in id order.
func (w *Worker) scorePass(ctx context.Context) {
	total, err := w.opts.Scorer.CountPending()
	if err != nil {
		slog.Warn("quality: counting pending photos failed", "err", err)
		w.setState(StateError, err.Error())
		return
	}
	if total == 0 {
		w.update(func(s *Status) { s.State, s.Error, s.Done, s.Total = StateIdle, "", 0, 0 })
		return
	}
	slog.Info("quality rating pass starting", "photos", total)
	start := time.Now()
	w.update(func(s *Status) { s.State, s.Error, s.Done, s.Total = StateScoring, "", 0, total })

	var cursor int64
	rated, failed, streak := 0, 0, 0
	for {
		items, err := w.opts.Scorer.ListPending(cursor, listBatch)
		if err != nil {
			slog.Warn("quality: listing pending photos failed", "err", err)
			w.setState(StateError, err.Error())
			return
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			if !w.waitWhileBusy(ctx) {
				return
			}
			err := w.opts.Scorer.Score(ctx, item)
			cursor = item.FileID
			switch {
			case err == nil:
				rated, streak = rated+1, 0
			case ctx.Err() != nil:
				slog.Info("quality rating pass cancelled", "rated", rated)
				return
			case w.opts.IsUnavailable != nil && w.opts.IsUnavailable(err):
				slog.Warn("quality rating cannot run; restart choix to retry", "err", err)
				w.setState(StateError, err.Error())
				return
			default:
				// Not the photo's fault (inference or database error):
				// it stays pending for a later pass.
				failed, streak = failed+1, streak+1
				slog.Warn("quality: rating failed", "file_id", item.FileID, "err", err)
				if streak >= maxConsecutiveFailures {
					slog.Warn("quality rating pass stopped after repeated failures", "rated", rated, "failed", failed)
					w.setState(StateError, err.Error())
					return
				}
			}
			done := rated + failed
			w.update(func(s *Status) { s.Done = min(done, s.Total) })
		}
	}
	slog.Info("quality rating pass done", "rated", rated, "failed", failed, "elapsed_ms", time.Since(start).Milliseconds())
	w.update(func(s *Status) { s.State, s.Error = StateIdle, "" })
}

// waitWhileBusy blocks while the pipeline is running. It reports false when
// cancelled.
func (w *Worker) waitWhileBusy(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	if w.opts.Busy == nil || !w.opts.Busy() {
		return true
	}
	w.update(func(s *Status) { s.State = StateWaiting })
	for w.opts.Busy() {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(w.opts.PollInterval):
		}
	}
	w.update(func(s *Status) { s.State = StateScoring })
	return true
}

// Package models downloads the pinned ONNX models (CLIP for visual
// grouping, the quality scorer for the quality rating) automatically:
// sequentially, CLIP first, with retries, reporting every state change as a
// models:progress event, and never while the model's setting is off.
//
// The bytes are fetched, resumed and SHA-256 verified by the Downloader
// (local.Installer in production); this package decides when to fetch and
// reports the state. It does not import Wails.
package models

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

// Model states, as reported in events.ModelStatus.State.
const (
	stateDisabled    = "disabled"    // the model's setting is off
	stateQueued      = "queued"      // enabled and missing: waiting for its turn or a retry pause
	stateDownloading = "downloading" // a download attempt is running
	stateFailed      = "failed"      // every attempt failed; waits for Retry
	stateReady       = "ready"       // the file is installed
)

// order is the download order and the order of Status.
var order = [...]local.ModelKind{local.ModelCLIP, local.ModelQuality}

// defaultBackoff is the pause before the second and third attempt.
var defaultBackoff = []time.Duration{2 * time.Second, 10 * time.Second}

// Downloader fetches model files. *local.Installer implements it.
type Downloader interface {
	Has(kind local.ModelKind) bool
	Download(ctx context.Context, kind local.ModelKind, onProgress func(received, total int64)) error
}

var _ Downloader = (*local.Installer)(nil)

// Options configures a Manager. Downloader and Enabled are required.
type Options struct {
	Downloader Downloader
	// Enabled reports whether a model's setting is on (clip ← visual_grouping,
	// quality ← quality_rating). It is called by Start, Refresh and Retry and
	// must not call back into the Manager.
	Enabled func(kind local.ModelKind) bool
	// Emit receives a models:progress event (an events.ModelStatus, key =
	// kind) for every status change. nil → no events.
	Emit *events.Coalescer
	// Backoff holds the pauses between attempts; a model gets
	// len(Backoff)+1 attempts. nil → 2 s, 10 s (three attempts).
	Backoff []time.Duration
	// Sleep waits d, returning ctx.Err() promptly when ctx is done. nil → a
	// timer. Tests inject it to observe the pauses.
	Sleep func(ctx context.Context, d time.Duration) error
	// OnDownloaded is called exactly once per download that finishes in
	// this process, once the verified file is in place — whatever the
	// setting is at that instant — and never for a model that was already on
	// disk. Whether to act on it is the consumer's decision. It runs on the
	// download goroutine without the Manager's locks held, so it must not
	// call Stop.
	OnDownloaded func(kind local.ModelKind)
}

// entry is the state of one model.
type entry struct {
	status events.ModelStatus
	// gen changes whenever the model is switched off, so a download turn
	// that was cancelled by Refresh recognises its results as stale.
	gen uint64
	// cancel stops the running download turn; nil when none runs.
	cancel context.CancelFunc
	// failure is the last error of a turn whose attempts were all used. It
	// keeps the model failed across Stop/Start and setting toggles; only
	// Retry or the file appearing on disk clears it.
	failure string
}

// Manager downloads the enabled, missing models one at a time on a single
// goroutine. Its methods are safe for concurrent use.
type Manager struct {
	o       Options
	backoff []time.Duration
	sleep   func(ctx context.Context, d time.Duration) error
	wake    chan struct{}

	// Lock order: runMu, evalMu, mu.
	runMu  sync.Mutex // Start and Stop
	stop   context.CancelFunc
	done   chan struct{}
	evalMu sync.Mutex // serialises Start, Refresh and Retry, which read Enabled and Has outside mu

	// mu guards entries. Events are emitted while it is held so they reach
	// the Coalescer in the order the states changed.
	mu      sync.Mutex
	entries map[local.ModelKind]*entry
}

// New returns a Manager whose Status reflects the settings and the files on
// disk. Nothing is downloaded before Start. New panics when Downloader or
// Enabled is nil.
func New(o Options) *Manager {
	if o.Downloader == nil {
		panic("models: Options.Downloader is required")
	}
	if o.Enabled == nil {
		panic("models: Options.Enabled is required")
	}
	backoff := defaultBackoff
	if o.Backoff != nil {
		backoff = o.Backoff
	}
	sleep := o.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	m := &Manager{
		o:       o,
		backoff: append([]time.Duration(nil), backoff...),
		sleep:   sleep,
		wake:    make(chan struct{}, 1),
		entries: make(map[local.ModelKind]*entry, len(order)),
	}
	for _, k := range order {
		m.entries[k] = &entry{status: events.ModelStatus{
			Kind:  k.String(),
			State: m.initialState(k),
			Size:  local.KnownModels[k].Size,
		}}
	}
	return m
}

// evaluate reads kind's setting and, when it is on, whether its file is
// installed. It calls out of the Manager, so it runs without mu held.
func (m *Manager) evaluate(k local.ModelKind) (on, has bool) {
	on = m.o.Enabled(k)
	return on, on && m.o.Downloader.Has(k)
}

// settleLocked sets e's state from its setting and file: disabled when off,
// ready when installed, failed when its attempts are used up, else queued.
// It reports whether the download goroutine has work.
func (m *Manager) settleLocked(e *entry, on, has bool) (queued bool) {
	e.status.Received, e.status.Error = 0, ""
	switch {
	case !on:
		m.setLocked(e, stateDisabled)
	case has:
		e.failure = ""
		m.readyLocked(e)
	case e.failure != "":
		e.status.Error = e.failure
		m.setLocked(e, stateFailed)
	default:
		m.setLocked(e, stateQueued)
		return true
	}
	return false
}

func (m *Manager) initialState(k local.ModelKind) string {
	switch {
	case !m.o.Enabled(k):
		return stateDisabled
	case m.o.Downloader.Has(k):
		return stateReady
	default:
		return stateQueued
	}
}

// Start re-reads the settings and the files on disk, reports every model,
// and starts the goroutine that downloads what is enabled and missing: CLIP
// first, then the quality model. It returns at once. Start on a running
// Manager does nothing; after Stop it starts again and resumes.
func (m *Manager) Start(ctx context.Context) {
	m.runMu.Lock()
	if m.done != nil {
		m.runMu.Unlock()
		return
	}
	m.evalMu.Lock()
	var on, has [len(order)]bool
	for i, k := range order {
		on[i], has[i] = m.evaluate(k)
	}
	m.mu.Lock()
	for i, k := range order {
		m.settleLocked(m.entries[k], on[i], has[i])
	}
	m.mu.Unlock()
	m.evalMu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.stop, m.done = cancel, done
	go m.run(runCtx, done)
	m.runMu.Unlock()
}

// Stop cancels the running download and joins the goroutine. Partial
// downloads stay on disk and resume on the next Start. Stop without Start,
// or a second Stop, does nothing.
func (m *Manager) Stop() {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	if m.done == nil {
		return
	}
	m.stop()
	<-m.done
	m.stop, m.done = nil, nil
}

// Status returns the state of every model, always [clip, quality].
func (m *Manager) Status() []events.ModelStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]events.ModelStatus, len(order))
	for i, k := range order {
		out[i] = m.entries[k].status
	}
	return out
}

// Ready reports whether kind is enabled and its file is installed.
func (m *Manager) Ready(kind local.ModelKind) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[kind]
	return ok && e.status.State == stateReady
}

// Retry queues a failed model for a new round of attempts. It does nothing
// for a model that has not failed.
func (m *Manager) Retry(kind local.ModelKind) {
	m.evalMu.Lock()
	on, has := m.evaluate(kind)
	wake := false
	m.mu.Lock()
	if e, ok := m.entries[kind]; ok && e.status.State == stateFailed {
		e.failure = ""
		wake = m.settleLocked(e, on, has)
	}
	m.mu.Unlock()
	m.evalMu.Unlock()

	if wake {
		m.signal()
	}
}

// Refresh re-reads Enabled for every model. A model switched off has its
// download cancelled and is reported disabled; a model switched on is
// reported ready when its file is installed, failed when its attempts were
// already used up (until Retry), and queued otherwise.
func (m *Manager) Refresh() {
	m.evalMu.Lock()
	var on, has [len(order)]bool
	for i, k := range order {
		on[i], has[i] = m.evaluate(k)
	}
	wake := false
	m.mu.Lock()
	for i, k := range order {
		e := m.entries[k]
		switch {
		case !on[i] && e.status.State != stateDisabled:
			e.gen++
			if e.cancel != nil {
				e.cancel()
				e.cancel = nil
				slog.Info("model download stopped: setting switched off", "model", k)
			}
			e.status.Received, e.status.Error = 0, ""
			m.setLocked(e, stateDisabled)
		case on[i] && e.status.State == stateDisabled:
			if m.settleLocked(e, true, has[i]) {
				wake = true
			}
		}
	}
	m.mu.Unlock()
	m.evalMu.Unlock()

	if wake {
		m.signal()
	}
}

// run downloads queued models, one at a time in order, until ctx is done.
func (m *Manager) run(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	for ctx.Err() == nil {
		k, ok := m.next()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-m.wake:
			}
			continue
		}
		m.turn(ctx, k)
	}
}

// next returns the first queued model.
func (m *Manager) next() (local.ModelKind, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range order {
		if m.entries[k].status.State == stateQueued {
			return k, true
		}
	}
	return 0, false
}

// turn makes up to len(backoff)+1 attempts to download k, pausing between
// them, and leaves k ready, failed, or — when stopped — queued. A turn
// cancelled by Refresh leaves the state to Refresh, except that a download
// which completed anyway is still reported to OnDownloaded.
func (m *Manager) turn(ctx context.Context, k local.ModelKind) {
	// Installed by another process since it was queued: not downloaded
	// here, so OnDownloaded is not called.
	if m.o.Downloader.Has(k) {
		m.mu.Lock()
		if e := m.entries[k]; e.status.State == stateQueued {
			e.failure = ""
			m.readyLocked(e)
		}
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	e := m.entries[k]
	if e.status.State != stateQueued {
		m.mu.Unlock()
		return
	}
	gen := e.gen
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.cancel = cancel
	m.mu.Unlock()

	progress := func(received, total int64) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if e.gen != gen || e.status.State != stateDownloading {
			return
		}
		e.status.Received = received
		if total > 0 {
			e.status.Size = total
		}
		m.emitLocked(e)
	}

	var err error
	for attempt := 0; attempt <= len(m.backoff); attempt++ {
		if attempt > 0 {
			if err = m.sleep(turnCtx, m.backoff[attempt-1]); err != nil {
				break
			}
		}
		if !m.transition(e, gen, stateDownloading) {
			err = context.Canceled
			break
		}
		slog.Info("model download started", "model", k, "attempt", attempt+1)
		err = m.o.Downloader.Download(turnCtx, k, progress)
		if err == nil || turnCtx.Err() != nil {
			break
		}
		slog.Warn("model download attempt failed", "model", k, "attempt", attempt+1, "err", err)
		if attempt < len(m.backoff) {
			m.transition(e, gen, stateQueued)
		}
	}

	downloaded := false
	m.mu.Lock()
	switch {
	case err == nil:
		// The verified file is in place. If the setting was switched off
		// meanwhile the model stays disabled; if it was switched back on, the
		// model is ready.
		downloaded = true
		e.failure = ""
		if e.gen == gen {
			e.cancel = nil
		}
		if s := e.status.State; s != stateDisabled && s != stateReady {
			m.readyLocked(e)
		}
		slog.Info("model download finished", "model", k)
	case e.gen != gen:
		// Switched off while this turn ran; Refresh has reported it.
	case ctx.Err() != nil:
		// Stopped: the partial file stays and the next Start resumes it.
		e.cancel = nil
		m.setLocked(e, stateQueued)
		slog.Info("model download stopped", "model", k)
	default:
		e.cancel = nil
		e.failure = err.Error()
		e.status.Error = e.failure
		m.setLocked(e, stateFailed)
		slog.Warn("model download failed", "model", k, "err", err)
	}
	m.mu.Unlock()
	if downloaded && m.o.OnDownloaded != nil {
		m.o.OnDownloaded(k)
	}
}

// transition sets e's state unless the turn that read gen is stale.
func (m *Manager) transition(e *entry, gen uint64, state string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.gen != gen {
		return false
	}
	m.setLocked(e, state)
	return true
}

// readyLocked marks e ready.
func (m *Manager) readyLocked(e *entry) {
	e.status.Received = e.status.Size
	e.status.Error = ""
	m.setLocked(e, stateReady)
}

// setLocked changes e's state and emits it.
func (m *Manager) setLocked(e *entry, state string) {
	e.status.State = state
	m.emitLocked(e)
}

// emitLocked reports e: progress is coalesced, every other state is
// delivered at once.
func (m *Manager) emitLocked(e *entry) {
	if m.o.Emit == nil {
		return
	}
	s := e.status
	if s.State == stateDownloading {
		m.o.Emit.Emit(events.ModelsProgress, s.Kind, s)
		return
	}
	m.o.Emit.EmitNow(events.ModelsProgress, s.Kind, s)
}

// signal wakes the download goroutine without blocking.
func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// sleepCtx waits d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

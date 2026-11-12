// Package inference is the app-level owner of the ONNX sessions: one Engine
// per process holds the CLIP and quality-model sessions, creates each lazily
// on first use, shares them with every folder session (as local.Embedder and
// local.CropInferer), runs one inference at a time and closes them once, at
// quit, after every folder session has been torn down.
//
// It does not import Wails.
package inference

import (
	"errors"
	"fmt"
	"image"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
)

// errClosed is returned by every call made once Close has begun.
var errClosed = errors.New("inference engine closed")

// backend is what the Engine needs from the ONNX sessions. *local.Sessions
// implements it; tests substitute fakes through newEngine.
type backend interface {
	local.Embedder
	local.CropInferer
	Provider(kind local.ModelKind) string
	Close() error
}

var _ backend = (*local.Sessions)(nil)

// openFunc builds the sessions object. It must not create any ONNX session
// itself: those are created by the backend on each model's first inference.
type openFunc func(models *local.ModelStore, cpuOnly map[local.ModelKind]bool) backend

// Engine owns the app's ONNX sessions.
//
// Nothing is created by New. The sessions object is built on the first
// inference, and each model's ONNX session on that model's first inference
// (local.Sessions): the CLIP session (~0.1–0.2 s) when the first photo is
// embedded, the quality session (~5 s, ~354 MB of compiled-model files while
// it lives) only when a quality pass first runs a crop — never at start-up.
// A missing model is never remembered: each call checks the model store
// again, so a model that finishes downloading while the app runs is used
// from the next call on.
//
// One mutex serialises every inference and session creation across both
// models. Close marks the engine closing before it waits for that mutex, so
// it waits for the single call in flight only; calls that were waiting for
// their turn, and every later call, fail without running.
type Engine struct {
	models  *local.ModelStore
	cpuOnly map[local.ModelKind]bool
	open    openFunc

	// closing is set first thing in Close; it is read without mu so a call
	// made while Close waits fails at once instead of queueing.
	closing atomic.Bool

	// mu is held for each inference (including any session it creates) and
	// by Close.
	mu     sync.Mutex
	closed bool

	// bmu guards b separately so Provider never waits behind an inference.
	bmu sync.Mutex
	b   backend
}

// New returns an Engine loading models from models. cpuOnly lists the
// models that skip the CoreML execution provider; nil selects
// local.SessionOptions' defaults (CLIP CPU-only inside the App Sandbox —
// spike S5 — the quality model on CoreML everywhere). The map is copied.
// No session is created until the first inference needs it.
func New(models *local.ModelStore, cpuOnly map[local.ModelKind]bool) *Engine {
	return newEngine(models, cpuOnly, openSessions)
}

// openSessions is the production openFunc.
func openSessions(models *local.ModelStore, cpuOnly map[local.ModelKind]bool) backend {
	return local.NewSessions(models, local.SessionOptions{CPUOnly: cpuOnly})
}

// newEngine is New with the sessions factory injected.
func newEngine(models *local.ModelStore, cpuOnly map[local.ModelKind]bool, open openFunc) *Engine {
	return &Engine{models: models, cpuOnly: maps.Clone(cpuOnly), open: open}
}

// Embedder returns the CLIP embedder for one consumer. enabled is read on
// every call — before the call waits for its turn and again once it has
// it — and while it reports false Embed returns local.ErrModelMissing
// without loading anything. nil means always enabled. enabled must not call
// into the Engine.
func (e *Engine) Embedder(enabled func() bool) local.Embedder {
	return embedder{e: e, enabled: enabled}
}

// CropInferer returns the quality-model crop inferer. Its errors wrap
// local.ErrQualityUnavailable when the model or the runtime cannot be
// loaded, and once the Engine is closing.
func (e *Engine) CropInferer() local.CropInferer {
	return cropInferer{e: e}
}

// Provider reports the execution provider of kind's session: "coreml",
// "cpu", or "" while that session does not exist (before its first use and
// after Close). It never waits behind an inference.
func (e *Engine) Provider(kind local.ModelKind) string {
	e.bmu.Lock()
	b := e.b
	e.bmu.Unlock()
	if b == nil {
		return ""
	}
	return b.Provider(kind)
}

// Close waits for the inference (or session creation) in flight — it cannot
// be interrupted — then destroys every session. Calls made after Close has
// begun return an error without running. Close is idempotent: a later or
// concurrent call also waits until the sessions are gone, then returns nil.
func (e *Engine) Close() error {
	e.closing.Store(true)
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closeLocked()
}

// TryClose closes the engine as Close does, but only when no inference or
// session creation is in flight; it never waits. It reports whether the
// engine is closed when it returns (true also when it already was). When a
// call is in flight it changes nothing and returns false: the engine keeps
// serving, and the sessions stay open under that call. The quit sequence
// uses it so that it neither blocks on an inference nor destroys sessions
// one is using.
func (e *Engine) TryClose() (closed bool, err error) {
	if !e.mu.TryLock() {
		return false, nil
	}
	defer e.mu.Unlock()
	// Set while mu is held: a call waiting for mu finds the engine closing
	// once it gets it, and every later call fails at its entry check.
	e.closing.Store(true)
	return true, e.closeLocked()
}

// closeLocked destroys the sessions once. Callers hold mu and have set
// closing.
func (e *Engine) closeLocked() error {
	if e.closed {
		return nil
	}
	e.closed = true
	e.bmu.Lock()
	b := e.b
	e.b = nil
	e.bmu.Unlock()
	if b == nil {
		return nil
	}
	if err := b.Close(); err != nil {
		return fmt.Errorf("close onnx sessions: %w", err)
	}
	return nil
}

// lockedBackend returns the sessions object, building it on first use.
// Callers hold mu.
func (e *Engine) lockedBackend() (backend, error) {
	if e.closing.Load() {
		return nil, errClosed
	}
	e.bmu.Lock()
	defer e.bmu.Unlock()
	if e.b == nil {
		e.b = e.open(e.models, e.cpuOnly)
	}
	return e.b, nil
}

// embed runs one CLIP embedding.
func (e *Engine) embed(enabled func() bool, img image.Image) ([]float32, error) {
	if e.closing.Load() {
		return nil, fmt.Errorf("clip: %w", errClosed)
	}
	if !isOn(enabled) {
		return nil, local.ErrModelMissing
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closing.Load() {
		return nil, fmt.Errorf("clip: %w", errClosed)
	}
	// The setting may have been switched off while this call waited.
	if !isOn(enabled) {
		return nil, local.ErrModelMissing
	}
	b, err := e.lockedBackend()
	if err != nil {
		return nil, fmt.Errorf("clip: %w", err)
	}
	return b.Embed(img)
}

// qualityCrop runs one quality-model crop.
func (e *Engine) qualityCrop(input []float32) (float64, error) {
	if e.closing.Load() {
		return 0, fmt.Errorf("%w: %w", local.ErrQualityUnavailable, errClosed)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	b, err := e.lockedBackend()
	if err != nil {
		return 0, fmt.Errorf("%w: %w", local.ErrQualityUnavailable, err)
	}
	return b.QualityCrop(input)
}

func isOn(enabled func() bool) bool { return enabled == nil || enabled() }

// embedder is the local.Embedder handed to one consumer.
type embedder struct {
	e       *Engine
	enabled func() bool
}

func (m embedder) Embed(img image.Image) ([]float32, error) { return m.e.embed(m.enabled, img) }

// cropInferer is the local.CropInferer handed to consumers.
type cropInferer struct{ e *Engine }

func (c cropInferer) QualityCrop(input []float32) (float64, error) { return c.e.qualityCrop(input) }

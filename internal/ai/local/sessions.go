package local

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"sync"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/appdir"
)

// ErrModelMissing means the model file a call needs is not installed.
var ErrModelMissing = errors.New("model not installed")

// errSessionsClosed is returned by every inference after Sessions.Close.
var errSessionsClosed = errors.New("onnx sessions closed")

// SessionOptions configures Sessions.
type SessionOptions struct {
	// CPUOnly lists the models that skip the CoreML execution provider.
	// Nil selects the defaults: CLIP is CPU-only inside the App Sandbox
	// (spike S5: CoreML takes 4 of its 830 nodes and is slower than the CPU
	// provider), the quality model always keeps CoreML (faster there).
	CPUOnly map[ModelKind]bool
}

// defaultCPUOnly is SessionOptions.CPUOnly when the caller leaves it nil.
func defaultCPUOnly(sandboxed bool) map[ModelKind]bool {
	return map[ModelKind]bool{ModelCLIP: sandboxed, ModelQuality: false}
}

// Embedder computes a CLIP embedding for an image.
type Embedder interface {
	Embed(img image.Image) ([]float32, error)
}

// CropInferer runs one quality-model crop and returns its raw score.
type CropInferer interface {
	QualityCrop(input []float32) (float64, error)
}

// Sessions owns the app's ONNX sessions — one per model, created on first
// use and shared by every folder. One mutex serialises everything: at most
// one inference (or session creation) runs at a time, across both models,
// and Close waits for it before destroying anything.
type Sessions struct {
	models  *ModelStore
	cpuOnly map[ModelKind]bool

	mu     sync.Mutex
	closed bool
	sess   map[ModelKind]*OrtSession

	// providerMu guards provider separately so Provider never waits behind
	// an inference or a session being created.
	providerMu sync.Mutex
	provider   map[ModelKind]string

	// Test seams: when set they replace the model call (Embed / QualityCrop)
	// and still run under mu.
	embedFn   func(img image.Image) ([]float32, error)
	qualityFn func(input []float32) (float64, error)
}

// NewSessions returns Sessions loading models from models. No session is
// created until the first inference needs it.
func NewSessions(models *ModelStore, o SessionOptions) *Sessions {
	cpuOnly := o.CPUOnly
	if cpuOnly == nil {
		cpuOnly = defaultCPUOnly(appdir.Sandboxed())
	}
	c := make(map[ModelKind]bool, len(cpuOnly))
	for k, v := range cpuOnly {
		c[k] = v
	}
	return &Sessions{
		models:   models,
		cpuOnly:  c,
		sess:     map[ModelKind]*OrtSession{},
		provider: map[ModelKind]string{},
	}
}

// Embed returns the L2-normalised CLIP embedding of img. It returns an error
// wrapping ErrModelMissing when the CLIP model file is absent.
func (s *Sessions) Embed(img image.Image) ([]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("clip: %w", errSessionsClosed)
	}
	if s.embedFn != nil {
		return s.embedFn(img)
	}
	sess, err := s.lockedSession(ModelCLIP)
	if err != nil {
		return nil, fmt.Errorf("clip: %w", err)
	}
	return CLIPEmbed(sess, img)
}

// QualityCrop runs one qualitySide×qualitySide crop (float32 NCHW, RGB
// 0..1) through the quality model and returns the raw score. When the
// model or the runtime cannot be loaded, or after Close, the error wraps
// ErrQualityUnavailable.
func (s *Sessions) QualityCrop(input []float32) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fmt.Errorf("%w: %w", ErrQualityUnavailable, errSessionsClosed)
	}
	if s.qualityFn != nil {
		return s.qualityFn(input)
	}
	sess, err := s.lockedSession(ModelQuality)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrQualityUnavailable, err)
	}
	out, err := sess.Run(input, []int64{1, 3, qualitySide, qualitySide})
	if err != nil {
		return 0, fmt.Errorf("quality inference: %w", err)
	}
	if len(out) != 1 {
		return 0, fmt.Errorf("quality output len=%d, want 1", len(out))
	}
	return float64(out[0]), nil
}

// Provider reports the execution provider of kind's session: "coreml",
// "cpu", or "" while that session does not exist.
func (s *Sessions) Provider(kind ModelKind) string {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	return s.provider[kind]
}

// Close waits for the inference in flight, then destroys every session.
// Inferences after Close return an error; Close itself may be called again
// and then returns nil.
func (s *Sessions) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var errs []error
	for kind, sess := range s.sess {
		if err := sess.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s session: %w", kind, err))
		}
		delete(s.sess, kind)
	}
	s.providerMu.Lock()
	clear(s.provider)
	s.providerMu.Unlock()
	return errors.Join(errs...)
}

// lockedSession returns kind's session, creating it on first use. A failed
// creation is not remembered: the next call tries again (the runtime's own
// initialisation outcome is remembered by ensureRuntime). Callers hold mu.
func (s *Sessions) lockedSession(kind ModelKind) (*OrtSession, error) {
	if sess := s.sess[kind]; sess != nil {
		return sess, nil
	}
	if !s.models.Has(kind) {
		return nil, ErrModelMissing
	}
	path := s.models.Path(kind)
	slog.Info("loading onnx session", "model", kind.String(), "path", path)
	start := time.Now()
	sess, err := NewSession(path, s.cpuOnly[kind])
	if err != nil {
		slog.Warn("onnx session load failed", "model", kind.String(), "path", path, "err", err)
		return nil, err
	}
	s.sess[kind] = sess
	s.providerMu.Lock()
	s.provider[kind] = sess.provider
	s.providerMu.Unlock()
	slog.Info("onnx session ready", "model", kind.String(), "provider", sess.provider,
		"elapsed_ms", time.Since(start).Milliseconds())
	return sess, nil
}

package local

import (
	"errors"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessions_EmbedWithoutModel(t *testing.T) {
	s := NewSessions(NewModelStore(t.TempDir()), SessionOptions{})
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.Embed(image.NewRGBA(image.Rect(0, 0, 8, 8))); !errors.Is(err, ErrModelMissing) {
		t.Fatalf("Embed without the CLIP model = %v, want ErrModelMissing", err)
	}
	if p := s.Provider(ModelCLIP); p != "" {
		t.Errorf("Provider(ModelCLIP) = %q before any session exists, want empty", p)
	}

	_, err := s.QualityCrop(make([]float32, 3*qualitySide*qualitySide))
	if !errors.Is(err, ErrQualityUnavailable) || !errors.Is(err, ErrModelMissing) {
		t.Fatalf("QualityCrop without the quality model = %v, want ErrQualityUnavailable and ErrModelMissing", err)
	}
}

func TestSessions_CloseThenEmbedErrors(t *testing.T) {
	s := NewSessions(NewModelStore(t.TempDir()), SessionOptions{})
	s.embedFn = func(image.Image) ([]float32, error) { return []float32{1}, nil }
	s.qualityFn = func([]float32) (float64, error) { return 0.5, nil }

	if _, err := s.Embed(image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("Embed before Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.Embed(image.NewRGBA(image.Rect(0, 0, 1, 1))); err == nil {
		t.Fatal("Embed after Close = nil error")
	}
	if _, err := s.QualityCrop(nil); err == nil {
		t.Fatal("QualityCrop after Close = nil error")
	} else if !errors.Is(err, ErrQualityUnavailable) {
		t.Errorf("QualityCrop after Close = %v, want it to wrap ErrQualityUnavailable", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
}

// Close waits for the inference in flight instead of destroying a session
// under it.
func TestSessions_CloseWaitsForInference(t *testing.T) {
	s := NewSessions(NewModelStore(t.TempDir()), SessionOptions{})
	entered := make(chan struct{})
	release := make(chan struct{})
	s.qualityFn = func([]float32) (float64, error) {
		close(entered)
		<-release
		return 0.5, nil
	}

	inferDone := make(chan error, 1)
	go func() {
		_, err := s.QualityCrop(nil)
		inferDone <- err
	}()
	<-entered

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned while an inference was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-inferDone; err != nil {
		t.Fatalf("in-flight QualityCrop: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSessions_Serialised(t *testing.T) {
	s := NewSessions(NewModelStore(t.TempDir()), SessionOptions{})
	t.Cleanup(func() { _ = s.Close() })

	var inFlight, maxInFlight, calls atomic.Int32
	enter := func() {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		calls.Add(1)
		time.Sleep(time.Millisecond)
		inFlight.Add(-1)
	}
	s.embedFn = func(image.Image) ([]float32, error) { enter(); return []float32{1}, nil }
	s.qualityFn = func([]float32) (float64, error) { enter(); return 0.5, nil }

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 10 {
				var err error
				if (g+i)%2 == 0 {
					_, err = s.Embed(image.NewRGBA(image.Rect(0, 0, 1, 1)))
				} else {
					_, err = s.QualityCrop(nil)
				}
				if err != nil {
					t.Errorf("inference: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 80 {
		t.Fatalf("calls = %d, want 80", calls.Load())
	}
	if m := maxInFlight.Load(); m != 1 {
		t.Fatalf("up to %d inferences overlapped, want 1", m)
	}
}

// CLIP runs on the CPU provider by default inside the App Sandbox (spike S5:
// CoreML takes 4 of its 830 nodes and is slower); the quality model keeps
// CoreML. Explicit options are used as given.
func TestNewSessions_DefaultCPUOnly(t *testing.T) {
	const key = "APP_SANDBOX_CONTAINER_ID"
	models := NewModelStore(t.TempDir())

	t.Setenv(key, "com.github.volodymyrsmirnov.choix")
	s := NewSessions(models, SessionOptions{})
	if !s.cpuOnly[ModelCLIP] || s.cpuOnly[ModelQuality] {
		t.Errorf("sandboxed defaults = %v, want CLIP CPU-only and quality on CoreML", s.cpuOnly)
	}

	t.Setenv(key, "")
	s = NewSessions(models, SessionOptions{})
	if s.cpuOnly[ModelCLIP] || s.cpuOnly[ModelQuality] {
		t.Errorf("unsandboxed defaults = %v, want CoreML for both", s.cpuOnly)
	}

	t.Setenv(key, "com.github.volodymyrsmirnov.choix")
	s = NewSessions(models, SessionOptions{CPUOnly: map[ModelKind]bool{ModelQuality: true}})
	if s.cpuOnly[ModelCLIP] || !s.cpuOnly[ModelQuality] {
		t.Errorf("explicit options = %v, want them used as given", s.cpuOnly)
	}
}

// installedModels returns a ModelStore holding kind, or skips: the model is
// looked for in CHOIX_MODELS_DIR, the app's data directory and v1's
// ~/.choix/models. Nothing is downloaded or created.
func installedModels(t *testing.T, kind ModelKind) *ModelStore {
	t.Helper()
	var dirs []string
	if d := os.Getenv("CHOIX_MODELS_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, "Library", "Application Support", "Choix", "models"),
			filepath.Join(home, ".choix", "models"))
	}
	for _, d := range dirs {
		if m := NewModelStore(d); m.Has(kind) {
			return m
		}
	}
	t.Skipf("%s model not installed (looked in %v)", kind, dirs)
	return nil
}

// skipWithoutRuntime skips when no ONNX Runtime library can be initialised.
func skipWithoutRuntime(t *testing.T) {
	t.Helper()
	if err := ensureRuntime(); err != nil {
		t.Skipf("ONNX runtime unavailable: %v", err)
	}
}

func TestSessions_RealCLIP(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	models := installedModels(t, ModelCLIP)
	skipWithoutRuntime(t)

	img := image.NewRGBA(image.Rect(0, 0, 224, 224))
	for y := range 224 {
		for x := range 224 {
			img.Set(x, y, color.RGBA{200, 100, uint8(x), 255})
		}
	}
	for _, cpuOnly := range []bool{true, false} {
		s := NewSessions(models, SessionOptions{CPUOnly: map[ModelKind]bool{ModelCLIP: cpuOnly}})
		emb, err := s.Embed(img)
		if err != nil {
			_ = s.Close()
			t.Fatalf("cpuOnly=%v: Embed: %v", cpuOnly, err)
		}
		if len(emb) != 512 {
			t.Errorf("cpuOnly=%v: len=%d, want 512", cpuOnly, len(emb))
		}
		var sumSq float64
		for _, v := range emb {
			sumSq += float64(v) * float64(v)
		}
		// Written so that NaN (every comparison false) fails too.
		if !(math.Abs(math.Sqrt(sumSq)-1) <= 1e-3) {
			t.Errorf("cpuOnly=%v: ||v|| = %v, want 1", cpuOnly, math.Sqrt(sumSq))
		}
		p := s.Provider(ModelCLIP)
		if p == "" || (cpuOnly && p != "cpu") {
			t.Errorf("cpuOnly=%v: Provider(ModelCLIP) = %q", cpuOnly, p)
		}
		t.Logf("cpuOnly=%v: provider %s", cpuOnly, p)
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}

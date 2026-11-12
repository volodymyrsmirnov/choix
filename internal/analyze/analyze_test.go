package analyze

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// fakePrinter answers vec and err for every call and counts them.
type fakePrinter struct {
	vec   []float32
	err   error
	calls int
	paths []string
}

func (p *fakePrinter) Print(_ context.Context, path string) ([]float32, error) {
	p.calls++
	p.paths = append(p.paths, path)
	return p.vec, p.err
}

// fixture is a store with one thumbed photo and its preview on disk.
type fixture struct {
	st      *store.Store
	id      int64
	preview string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	st := store.NewTestStore(t)
	id, err := st.Files().Insert(store.File{Path: "a.jpg", Size: 1, Mtime: 1, ContentHash: "h", Kind: "photo", Format: "jpeg", ScanStatus: "thumbed"})
	if err != nil {
		t.Fatal(err)
	}
	preview := filepath.Join(t.TempDir(), "1-preview.jpg")
	if err := os.WriteFile(preview, []byte("preview bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture{st: st, id: id, preview: preview}
}

func (f fixture) status(t *testing.T) (string, string) {
	t.Helper()
	file, err := f.st.Files().GetByID(f.id)
	if err != nil {
		t.Fatal(err)
	}
	return file.ScanStatus, file.ErrMsg.String
}

func (f fixture) signals(t *testing.T) (store.AISignals, bool) {
	t.Helper()
	a, err := f.st.AISignals().GetByFileID(f.id)
	if errors.Is(err, store.ErrNotFound) {
		return store.AISignals{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return a, true
}

func (f fixture) missing(t *testing.T) int {
	t.Helper()
	n, err := f.st.Files().CountAnalyzedMissingPrint(PrintID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// vector returns a FeaturePrintLen vector of norm 2.
func vector() []float32 {
	v := make([]float32, platform.FeaturePrintLen)
	for i := range v {
		v[i] = float32(i%7) - 3
	}
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	s := float32(2 / math.Sqrt(n))
	for i := range v {
		v[i] *= s
	}
	return v
}

func TestAnalyze_StoresNormalisedPrint(t *testing.T) {
	f := newFixture(t)
	p := &fakePrinter{vec: vector()}
	if err := New(f.st, p).Analyze(context.Background(), f.id, f.preview); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if st, _ := f.status(t); st != "analyzed" {
		t.Fatalf("status %q, want analyzed", st)
	}
	if len(p.paths) != 1 || p.paths[0] != f.preview {
		t.Fatalf("printer paths %v, want the preview", p.paths)
	}
	a, ok := f.signals(t)
	if !ok || a.PrintID != PrintID || len(a.Print) != platform.FeaturePrintLen {
		t.Fatalf("row %+v, %v; want a print stamped %q", a.PrintID, ok, PrintID)
	}
	var n float64
	in := vector()
	for i, x := range a.Print {
		n += float64(x) * float64(x)
		if want := in[i] / 2; math.Abs(float64(x-want)) > 1e-6 {
			t.Fatalf("print[%d] = %v, want %v", i, x, want)
		}
	}
	if math.Abs(n-1) > 1e-5 {
		t.Fatalf("stored norm² %v, want 1", n)
	}
	if f.missing(t) != 0 {
		t.Fatal("the file is still pending")
	}
}

func TestAnalyze_NoPrinter(t *testing.T) {
	f := newFixture(t)
	if err := New(f.st, nil).Analyze(context.Background(), f.id, f.preview); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if st, _ := f.status(t); st != "analyzed" {
		t.Fatalf("status %q, want analyzed", st)
	}
	if a, ok := f.signals(t); ok && a.PrintID != "" {
		t.Fatalf("row stamped %q, want unstamped", a.PrintID)
	}
	if n := f.missing(t); n != 1 {
		t.Fatalf("CountAnalyzedMissingPrint = %d, want 1", n)
	}
}

func TestAnalyze_PrintsOff(t *testing.T) {
	f := newFixture(t)
	// ErrPrintsOff wins over the context's state: it is checked first.
	p := &fakePrinter{err: errors.Join(ErrPrintsOff, errors.New("other"))}
	if err := New(f.st, p).Analyze(context.Background(), f.id, f.preview); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if st, _ := f.status(t); st != "analyzed" {
		t.Fatalf("status %q, want analyzed", st)
	}
	if a, ok := f.signals(t); ok && a.PrintID != "" {
		t.Fatalf("row stamped %q, want unstamped", a.PrintID)
	}
	if n := f.missing(t); n != 1 {
		t.Fatalf("CountAnalyzedMissingPrint = %d, want 1", n)
	}
}

func TestAnalyze_VisionFailureIsStampedOnce(t *testing.T) {
	f := newFixture(t)
	p := &fakePrinter{err: errors.New("vision: cannot read preview")}
	a := New(f.st, p)
	a.FailureGuard = func() bool { return true }
	if err := a.Analyze(context.Background(), f.id, f.preview); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if st, msg := f.status(t); st != "analyzed" || msg != "" {
		t.Fatalf("status %q (%q), want analyzed", st, msg)
	}
	row, ok := f.signals(t)
	if !ok || row.PrintID != PrintID || row.Print != nil {
		t.Fatalf("row %v stamped %q with %d values; want a NULL print stamped %q", ok, row.PrintID, len(row.Print), PrintID)
	}
	if n := f.missing(t); n != 0 {
		t.Fatalf("CountAnalyzedMissingPrint = %d, want 0: the file is not retried", n)
	}
	if n, err := f.st.Files().BumpAnalyzedMissingPrint(PrintID); err != nil || n != 0 {
		t.Fatalf("BumpAnalyzedMissingPrint = %d, %v; want 0", n, err)
	}
}

func TestAnalyze_BadVector(t *testing.T) {
	nan := vector()
	nan[3] = float32(math.NaN())
	inf := vector()
	inf[5] = float32(math.Inf(1))
	for name, vec := range map[string][]float32{
		"short": vector()[:100],
		"long":  append(vector(), 1),
		"zero":  make([]float32, platform.FeaturePrintLen),
		"NaN":   nan,
		"Inf":   inf,
		"nil":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if err := New(f.st, &fakePrinter{vec: vec}).Analyze(context.Background(), f.id, f.preview); err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if st, _ := f.status(t); st != "analyzed" {
				t.Fatalf("status %q, want analyzed", st)
			}
			row, ok := f.signals(t)
			if !ok || row.PrintID != PrintID || row.Print != nil {
				t.Fatalf("row %v stamped %q with %d values; want a NULL print stamped", ok, row.PrintID, len(row.Print))
			}
		})
	}
}

func TestAnalyze_Cancelled(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		f := newFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p := &fakePrinter{vec: vector()}
		if err := New(f.st, p).Analyze(ctx, f.id, f.preview); !errors.Is(err, context.Canceled) {
			t.Fatalf("Analyze = %v, want context.Canceled", err)
		}
		if p.calls != 0 {
			t.Fatal("the printer was called")
		}
		if st, _ := f.status(t); st != "thumbed" {
			t.Fatalf("status %q, want thumbed (untouched)", st)
		}
	})
	t.Run("during", func(t *testing.T) {
		f := newFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		p := &cancellingPrinter{cancel: cancel}
		if err := New(f.st, p).Analyze(ctx, f.id, f.preview); !errors.Is(err, context.Canceled) {
			t.Fatalf("Analyze = %v, want context.Canceled", err)
		}
		if st, _ := f.status(t); st != "thumbed" {
			t.Fatalf("status %q, want thumbed (untouched)", st)
		}
		if _, ok := f.signals(t); ok {
			t.Fatal("a row was written")
		}
	})
}

// cancellingPrinter cancels the context and answers its error, as a
// detached native call abandoned by its caller does.
type cancellingPrinter struct{ cancel context.CancelFunc }

func (p *cancellingPrinter) Print(ctx context.Context, _ string) ([]float32, error) {
	p.cancel()
	return nil, ctx.Err()
}

func TestAnalyze_FolderUnreachable(t *testing.T) {
	t.Run("preview gone", func(t *testing.T) {
		f := newFixture(t)
		a := New(f.st, &fakePrinter{vec: vector()})
		a.FailureGuard = func() bool { return false }
		err := a.Analyze(context.Background(), f.id, filepath.Join(t.TempDir(), "gone.jpg"))
		if !errors.Is(err, pipeline.ErrFolderUnavailable) {
			t.Fatalf("Analyze = %v, want ErrFolderUnavailable", err)
		}
		if st, _ := f.status(t); st != "thumbed" {
			t.Fatalf("status %q, want thumbed (untouched)", st)
		}
	})
	t.Run("vision failed", func(t *testing.T) {
		f := newFixture(t)
		a := New(f.st, &fakePrinter{err: errors.New("vision: cannot read")})
		a.FailureGuard = func() bool { return false }
		err := a.Analyze(context.Background(), f.id, f.preview)
		if !errors.Is(err, pipeline.ErrFolderUnavailable) {
			t.Fatalf("Analyze = %v, want ErrFolderUnavailable", err)
		}
		if st, _ := f.status(t); st != "thumbed" {
			t.Fatalf("status %q, want thumbed (untouched)", st)
		}
		if _, ok := f.signals(t); ok {
			t.Fatal("a row was written")
		}
	})
}

func TestAnalyze_UnreadablePreview(t *testing.T) {
	f := newFixture(t)
	p := &fakePrinter{vec: vector()}
	a := New(f.st, p)
	a.FailureGuard = func() bool { return true }
	err := a.Analyze(context.Background(), f.id, filepath.Join(t.TempDir(), "gone.jpg"))
	if err == nil {
		t.Fatal("Analyze: want an error")
	}
	st, msg := f.status(t)
	if st != "failed" || msg == "" {
		t.Fatalf("status %q (%q), want failed with the error text", st, msg)
	}
	if p.calls != 0 {
		t.Fatal("the printer was called for an unreadable preview")
	}
}

package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/deps"
)

// Installer wraps a ModelStore with the network behavior the first-run
// wizard needs: streamed download into the model cache, SHA-256
// verification, and per-chunk progress events. Construct via
// NewInstaller; the embedded *ModelStore exposes Has/Path so callers can
// reuse the same value as the firstrun.ModelStore detector input.
type Installer struct {
	*ModelStore
	HTTPClient *http.Client // nil → http.DefaultClient
}

// NewInstaller returns an Installer rooted at dir.
func NewInstaller(dir string) *Installer {
	return &Installer{ModelStore: NewModelStore(dir)}
}

// Has implements firstrun.ModelStore. ModelStore.Has is filesystem-only,
// so the context is unused — the wrapper exists solely to match the
// interface signature the first-run wizard expects.
func (i *Installer) Has(_ context.Context, kind ModelKind) (bool, error) {
	return i.ModelStore.Has(kind), nil
}

// Install implements firstrun.ModelInstaller. It streams the pinned URL
// for kind into the model cache directory, verifies the SHA-256, and
// publishes ProgressEvent values to ch as bytes arrive. ch is always
// closed before Install returns so callers can range over it without
// risking a leak. Returns nil on success even if ch is buffered and the
// reader hasn't drained it yet.
func (i *Installer) Install(ctx context.Context, kind ModelKind, ch chan<- deps.ProgressEvent) error {
	defer close(ch)

	if i.ModelStore.Has(kind) {
		send(ch, deps.ProgressEvent{Stage: "verify", PercentDone: 1, Message: "already installed"})
		return nil
	}
	m, ok := KnownModels[kind]
	if !ok {
		return fmt.Errorf("unknown model kind %v", kind)
	}
	if err := os.MkdirAll(i.dir, 0o750); err != nil { //nolint:gosec // G301: 0750 is fine for model cache dir
		return fmt.Errorf("mkdir models dir: %w", err)
	}

	dest := i.Path(kind)
	tmp := dest + ".tmp"
	defer func() { _ = os.Remove(tmp) }() // safe even on success — Rename removed it

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	client := i.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", m.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %s", m.URL, resp.Status)
	}

	total := resp.ContentLength
	if total <= 0 {
		// Fall back to the Content-Length header in case ContentLength is -1
		// (some proxies strip the value); harmless when both agree.
		if h := resp.Header.Get("Content-Length"); h != "" {
			if n, perr := strconv.ParseInt(h, 10, 64); perr == nil {
				total = n
			}
		}
	}

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // G304: tmp is i.dir + KnownModels[kind].FileName + ".tmp" — never user input
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	hasher := sha256.New()
	// Seed `last` in the past so the first Read passes the throttle and
	// emits a progress event immediately, instead of waiting 200ms.
	pr := &progressReader{r: resp.Body, total: total, ch: ch, throttle: 200 * time.Millisecond, last: time.Now().Add(-time.Second)}
	pr.emit("fetch")
	if _, err := io.Copy(io.MultiWriter(f, hasher), pr); err != nil {
		_ = f.Close()
		return fmt.Errorf("download body: %w", err)
	}
	// Flush bytes to disk before the rename. Without this, APFS may
	// reorder the metadata (rename) ahead of the data, leaving a
	// zero-byte .onnx that passes Has() forever after a power loss.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}

	send(ch, deps.ProgressEvent{Stage: "verify", PercentDone: 1, Message: "verifying checksum"})
	got := hex.EncodeToString(hasher.Sum(nil))
	if got != m.SHA256 {
		return fmt.Errorf("sha256 mismatch: got %s want %s", got, m.SHA256)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	send(ch, deps.ProgressEvent{Stage: "done", PercentDone: 1})
	return nil
}

// send pushes ev onto ch without blocking. The wizard's progress channel
// is small and the SSE writer drains it asynchronously, so dropping is
// preferable to stalling the download on a slow client.
func send(ch chan<- deps.ProgressEvent, ev deps.ProgressEvent) {
	select {
	case ch <- ev:
	default:
	}
}

// progressReader wraps an io.Reader and emits a deps.ProgressEvent at
// most once per throttle interval as bytes flow through. It also emits
// once at construction (via emit) so the UI shows "0%" immediately
// instead of waiting for the first chunk.
type progressReader struct {
	r        io.Reader
	read     atomic.Int64
	total    int64
	ch       chan<- deps.ProgressEvent
	throttle time.Duration
	last     time.Time
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.read.Add(int64(n))
	}
	if time.Since(p.last) >= p.throttle {
		p.emit("fetch")
		p.last = time.Now()
	}
	if errors.Is(err, io.EOF) {
		p.emit("fetch")
	}
	return n, err
}

func (p *progressReader) emit(stage string) {
	pct := 0.0
	if p.total > 0 {
		pct = float64(p.read.Load()) / float64(p.total)
		if pct > 1 {
			pct = 1
		}
	}
	send(p.ch, deps.ProgressEvent{Stage: stage, PercentDone: pct})
}

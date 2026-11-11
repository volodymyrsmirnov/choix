package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/deps"
)

// Installer wraps a ModelStore with the network behavior model downloads
// need: a resumable streamed download into the model cache, SHA-256
// verification before the file is put in place, and progress callbacks.
// Construct via NewInstaller; the embedded *ModelStore exposes Has/Path so
// callers can reuse the same value as the firstrun.ModelStore detector input.
type Installer struct {
	*ModelStore
	HTTPClient *http.Client // nil → http.DefaultClient

	// Models overrides KnownModels (tests point URL/SHA256 at an httptest
	// server). nil → KnownModels. FileName must match KnownModels because
	// ModelStore.Path resolves through the registry.
	Models map[ModelKind]Model
	// StallTimeout aborts a download whose body delivers no bytes for this
	// long, so a dead connection cannot hang the caller. 0 → 60 s.
	StallTimeout time.Duration
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

func (i *Installer) model(kind ModelKind) (Model, bool) {
	if i.Models != nil {
		m, ok := i.Models[kind]
		return m, ok
	}
	m, ok := KnownModels[kind]
	return m, ok
}

// lockPollInterval is how often Download re-tries the cross-process lock.
const lockPollInterval = 500 * time.Millisecond

// Download fetches the pinned model file for kind unless it is already
// installed. It is the single download implementation (the first-run
// wizard's Install and the automatic quality-model download both use it):
//
//   - Bytes are written to "<file>.part". A later call resumes it with an
//     HTTP Range request, so an interrupted 180 MB download does not start
//     over. The part file is kept on network errors and on cancellation.
//   - The SHA-256 of the complete file is checked before the rename; on a
//     mismatch the part file is deleted and an error is returned, so a
//     wrong or truncated file never becomes the model.
//   - The data is fsynced before the atomic rename into place.
//   - An advisory lock on "<file>.lock" keeps two choix processes from
//     writing the same part file. The second one waits, then finds the model
//     installed — or, if the first gave up, resumes its part file.
//   - It returns promptly when ctx is cancelled.
//
// onProgress (may be nil) receives the bytes on disk so far and the expected
// total (0 when the server did not say), at most a few times per second.
func (i *Installer) Download(ctx context.Context, kind ModelKind, onProgress func(received, total int64)) error {
	if i.ModelStore.Has(kind) {
		return nil
	}
	m, ok := i.model(kind)
	if !ok {
		return fmt.Errorf("unknown model kind %v", kind)
	}
	if err := os.MkdirAll(i.dir, 0o750); err != nil { //nolint:gosec // G301: 0750 is fine for model cache dir
		return fmt.Errorf("mkdir models dir: %w", err)
	}
	dest := i.Path(kind)

	unlock, err := lockFile(ctx, dest+".lock")
	if err != nil {
		return fmt.Errorf("lock model download: %w", err)
	}
	defer unlock()
	// Another process may have completed the download while we waited.
	if i.ModelStore.Has(kind) {
		return nil
	}

	// One restart is allowed: a part file the server can no longer extend
	// (HTTP 416) is discarded and the download begins again from byte 0.
	for attempt := 0; ; attempt++ {
		restart, err := i.fetch(ctx, m, dest, onProgress)
		if err != nil {
			return err
		}
		if !restart {
			return nil
		}
		if attempt > 0 {
			return fmt.Errorf("GET %s: server keeps rejecting the requested range", m.URL)
		}
	}
}

// fetch performs one HTTP request for m, resuming dest+".part" when present.
// restart=true means the stale part file was removed and the caller should
// call again.
func (i *Installer) fetch(ctx context.Context, m Model, dest string, onProgress func(received, total int64)) (restart bool, err error) {
	part := dest + ".part"
	hasher := sha256.New()
	have, err := hashExisting(part, hasher)
	if err != nil {
		return false, fmt.Errorf("read partial download: %w", err)
	}

	stall := i.StallTimeout
	if stall <= 0 {
		stall = 60 * time.Second
	}
	// The watchdog cancels the request when the body stops delivering.
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchdog := time.AfterFunc(stall, cancel)
	defer watchdog.Stop()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, m.URL, nil)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	client := i.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("GET %s: %w", m.URL, downloadErr(ctx, err))
	}
	defer func() { _ = resp.Body.Close() }()

	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	switch {
	case resp.StatusCode == http.StatusOK:
		// The server ignored the Range header (or there was no part file):
		// start from scratch.
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		hasher.Reset()
		have = 0
	case resp.StatusCode == http.StatusPartialContent && have > 0 && contentRangeStartsAt(resp.Header.Get("Content-Range"), have):
		// Resuming exactly where the part file ends.
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0:
		// Nothing beyond what we have. Either the part file is already the
		// whole model (a previous run died between the last byte and the
		// rename) or it is stale.
		if hex.EncodeToString(hasher.Sum(nil)) == m.SHA256 {
			return false, os.Rename(part, dest)
		}
		if rmErr := os.Remove(part); rmErr != nil {
			return false, fmt.Errorf("remove stale partial download: %w", rmErr)
		}
		return true, nil
	default:
		return false, fmt.Errorf("GET %s: status %s", m.URL, resp.Status)
	}

	var total int64
	if resp.ContentLength > 0 {
		total = have + resp.ContentLength
	}

	f, err := os.OpenFile(part, flags, 0o600) //nolint:gosec // G304: part is i.dir + a registry file name + ".part" — never user input
	if err != nil {
		return false, fmt.Errorf("open partial download: %w", err)
	}
	pw := &progressWriter{received: have, total: total, report: onProgress, watchdog: watchdog, stall: stall}
	pw.emit()
	if _, err := io.Copy(io.MultiWriter(f, hasher, pw), resp.Body); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("download body: %w", downloadErr(ctx, err))
	}
	pw.emit()
	// Flush bytes to disk before the rename. Without this, APFS may
	// reorder the metadata (rename) ahead of the data, leaving a
	// zero-byte .onnx that passes Has() forever after a power loss.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("sync partial download: %w", err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("close partial download: %w", err)
	}

	if got := hex.EncodeToString(hasher.Sum(nil)); got != m.SHA256 {
		_ = os.Remove(part)
		return false, fmt.Errorf("sha256 mismatch: got %s want %s", got, m.SHA256)
	}
	if err := os.Rename(part, dest); err != nil {
		return false, fmt.Errorf("rename into place: %w", err)
	}
	return false, nil
}

// downloadErr prefers the caller's cancellation over the watchdog's: when
// the parent context is done that is the real reason; otherwise a cancelled
// request means the body stalled.
func downloadErr(parent context.Context, err error) error {
	if perr := parent.Err(); perr != nil {
		return perr
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("connection stalled")
	}
	return err
}

// hashExisting feeds the bytes already in path into h and returns how many
// there were. A missing file is zero bytes, not an error.
func hashExisting(path string, h hash.Hash) (int64, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is an internal model cache path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer func() { _ = f.Close() }()
	return io.Copy(h, f)
}

// contentRangeStartsAt reports whether a Content-Range header
// ("bytes 100-199/200") begins at the given offset.
func contentRangeStartsAt(header string, start int64) bool {
	rest, ok := strings.CutPrefix(header, "bytes ")
	if !ok {
		return false
	}
	first, _, ok := strings.Cut(rest, "-")
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(first, 10, 64)
	return err == nil && n == start
}

// lockFile takes an exclusive advisory lock on path, polling so the wait
// honours ctx. The returned func releases it. The lock file itself is left
// behind on purpose: unlinking it would let a third process lock a different
// inode while a second still waits on the old one.
func lockFile(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // G304: path is an internal model cache path
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // G115: fd fits in int
		if err == nil {
			return func() { _ = f.Close() }, nil // closing the descriptor drops the lock
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

// progressWriter counts bytes as they are written, reports them at most
// once per throttle interval, and pets the stall watchdog.
type progressWriter struct {
	received int64
	total    int64
	report   func(received, total int64)
	last     time.Time
	watchdog *time.Timer
	stall    time.Duration
}

const progressThrottle = 200 * time.Millisecond

func (p *progressWriter) Write(b []byte) (int, error) {
	p.received += int64(len(b))
	p.watchdog.Reset(p.stall)
	if time.Since(p.last) >= progressThrottle {
		p.emit()
	}
	return len(b), nil
}

func (p *progressWriter) emit() {
	p.last = time.Now()
	if p.report != nil {
		p.report(p.received, p.total)
	}
}

// Install implements firstrun.ModelInstaller on top of Download, publishing
// deps.ProgressEvent values to ch as bytes arrive. ch is always closed
// before Install returns so callers can range over it without risking a
// leak. Returns nil on success even if ch is buffered and the reader hasn't
// drained it yet.
func (i *Installer) Install(ctx context.Context, kind ModelKind, ch chan<- deps.ProgressEvent) error {
	defer close(ch)

	if i.ModelStore.Has(kind) {
		send(ch, deps.ProgressEvent{Stage: "verify", PercentDone: 1, Message: "already installed"})
		return nil
	}
	err := i.Download(ctx, kind, func(received, total int64) {
		pct := 0.0
		if total > 0 {
			pct = min(float64(received)/float64(total), 1)
		}
		send(ch, deps.ProgressEvent{Stage: "fetch", PercentDone: pct})
	})
	if err != nil {
		return err
	}
	send(ch, deps.ProgressEvent{Stage: "verify", PercentDone: 1, Message: "checksum verified"})
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

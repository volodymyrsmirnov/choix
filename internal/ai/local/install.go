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
)

// Installer wraps a ModelStore with the network behavior model downloads
// need: a resumable streamed download into the model cache, SHA-256
// verification before the file is put in place, and progress callbacks.
// Construct via NewInstaller; the embedded *ModelStore's Has and Path are
// promoted, so Has(kind) bool and Download are the whole download API.
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
// installed. It is the single download implementation:
//
//   - Bytes are written to "<file>.part". A later call resumes it with an
//     HTTP Range request, so an interrupted 180 MB download does not start
//     over. The part file is kept on network errors and on cancellation.
//   - The SHA-256 of the complete file is checked before the rename; on a
//     mismatch the part file is deleted and an error is returned, so a
//     wrong or truncated file never becomes the model.
//   - The body is read only up to the pinned size; a longer response fails
//     and its part file is deleted, as for a mismatch.
//   - The data is fsynced before the atomic rename into place.
//   - An advisory lock on "<file>.lock" keeps two choix processes from
//     writing the same part file. The second one waits, then finds the model
//     installed — or, if the first gave up, resumes its part file.
//   - It returns promptly when ctx is cancelled.
//
// onProgress (may be nil) receives the bytes on disk so far and the expected
// total (0 when the server did not say), at most a few times per second.
func (i *Installer) Download(ctx context.Context, kind ModelKind, onProgress func(received, total int64)) error {
	if i.Has(kind) {
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
	if i.Has(kind) {
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
		// net/http's error already names the method and URL.
		return false, fmt.Errorf("download model: %w", downloadErr(ctx, err))
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
	// Read at most one byte past the pinned size: a wrong or hostile response
	// must not fill the disk before the SHA-256 check runs. A part file that
	// is already longer than the model leaves a negative limit, which reads
	// nothing and fails the same way. The size is checked before the copy
	// error: a read can return bytes and an error together, and an oversized
	// part file must go whatever the error was.
	n, err := io.Copy(io.MultiWriter(f, hasher, pw), io.LimitReader(resp.Body, m.Size-have+1))
	if have+n > m.Size {
		_ = f.Close()
		_ = os.Remove(part)
		return false, fmt.Errorf("GET %s: response is larger than the pinned size of %d bytes", m.URL, m.Size)
	}
	if err != nil {
		// At or below the pinned size: the part file is kept for a resume.
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

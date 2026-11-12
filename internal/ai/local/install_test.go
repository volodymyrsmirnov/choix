package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// modelBytes is a deterministic stand-in for a model file, large enough to
// span several reads.
func modelBytes() []byte {
	b := make([]byte, 300_000)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// testInstaller returns an Installer whose quality model points at url with
// the checksum of want.
func testInstaller(t *testing.T, url string, want []byte) *Installer {
	t.Helper()
	inst := NewInstaller(t.TempDir())
	m := KnownModels[ModelQuality]
	m.URL = url
	m.SHA256 = sum(want)
	inst.Models = map[ModelKind]Model{ModelQuality: m}
	return inst
}

// rangeServer serves body with Range support and counts requests.
func rangeServer(t *testing.T, body []byte, requests *atomic.Int32, lastRange *atomic.Value) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		lastRange.Store(r.Header.Get("Range"))
		http.ServeContent(w, r, "model.onnx", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func assertInstalled(t *testing.T, inst *Installer, want []byte) {
	t.Helper()
	got, err := os.ReadFile(inst.Path(ModelQuality))
	if err != nil {
		t.Fatalf("model file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("model file has %d bytes, want the %d served", len(got), len(want))
	}
	if _, err := os.Stat(inst.Path(ModelQuality) + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("part file should be gone after install, stat err = %v", err)
	}
}

func TestDownloadFreshInstallsAndReportsProgress(t *testing.T) {
	body := modelBytes()
	var reqs atomic.Int32
	var rng atomic.Value
	srv := rangeServer(t, body, &reqs, &rng)
	inst := testInstaller(t, srv.URL, body)

	var lastRecv, lastTotal int64
	err := inst.Download(context.Background(), ModelQuality, func(recv, total int64) { lastRecv, lastTotal = recv, total })
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertInstalled(t, inst, body)
	if lastRecv != int64(len(body)) || lastTotal != int64(len(body)) {
		t.Fatalf("final progress = %d/%d, want %d/%d", lastRecv, lastTotal, len(body), len(body))
	}

	// Already installed: no further request.
	if err := inst.Download(context.Background(), ModelQuality, nil); err != nil {
		t.Fatalf("second Download: %v", err)
	}
	if reqs.Load() != 1 {
		t.Fatalf("requests = %d, want 1 (an installed model must not be fetched again)", reqs.Load())
	}
}

func TestDownloadChecksumMismatchLeavesNothingBehind(t *testing.T) {
	body := modelBytes()
	var reqs atomic.Int32
	var rng atomic.Value
	srv := rangeServer(t, body, &reqs, &rng)
	inst := testInstaller(t, srv.URL, []byte("something else entirely"))

	err := inst.Download(context.Background(), ModelQuality, nil)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err = %v, want a sha256 mismatch", err)
	}
	if inst.Has(ModelQuality) {
		t.Fatal("a file with the wrong checksum must never become the model")
	}
	if _, statErr := os.Stat(inst.Path(ModelQuality) + ".part"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("a corrupt part file must be deleted so the next attempt starts clean")
	}
}

func TestDownloadResumesPartFile(t *testing.T) {
	body := modelBytes()
	var reqs atomic.Int32
	var rng atomic.Value
	srv := rangeServer(t, body, &reqs, &rng)
	inst := testInstaller(t, srv.URL, body)

	const have = 120_000
	if err := os.WriteFile(inst.Path(ModelQuality)+".part", body[:have], 0o600); err != nil {
		t.Fatal(err)
	}
	var first int64 = -1
	err := inst.Download(context.Background(), ModelQuality, func(recv, _ int64) {
		if first < 0 {
			first = recv
		}
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertInstalled(t, inst, body)
	if got := rng.Load().(string); got != "bytes=120000-" {
		t.Fatalf("Range header = %q, want bytes=120000-", got)
	}
	if first != have {
		t.Fatalf("first progress report = %d, want %d (resumed bytes count as received)", first, have)
	}
}

func TestDownloadRestartsWhenServerIgnoresRange(t *testing.T) {
	body := modelBytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body) // always 200 with the whole file
	}))
	t.Cleanup(srv.Close)
	inst := testInstaller(t, srv.URL, body)
	// A part file that is NOT a prefix of the body: appending would corrupt.
	if err := os.WriteFile(inst.Path(ModelQuality)+".part", []byte("garbage-from-an-older-attempt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := inst.Download(context.Background(), ModelQuality, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertInstalled(t, inst, body)
}

func TestDownloadCompletePartIsInstalledOn416(t *testing.T) {
	body := modelBytes()
	var reqs atomic.Int32
	var rng atomic.Value
	srv := rangeServer(t, body, &reqs, &rng)
	inst := testInstaller(t, srv.URL, body)
	// The previous run got every byte but died before the rename.
	if err := os.WriteFile(inst.Path(ModelQuality)+".part", body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := inst.Download(context.Background(), ModelQuality, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertInstalled(t, inst, body)
	if reqs.Load() != 1 {
		t.Fatalf("requests = %d, want 1 (no re-download of a complete part)", reqs.Load())
	}
}

func TestDownloadDiscardsStalePartOn416(t *testing.T) {
	body := modelBytes()
	var reqs atomic.Int32
	var rng atomic.Value
	srv := rangeServer(t, body, &reqs, &rng)
	inst := testInstaller(t, srv.URL, body)
	// Longer than the real file and with different content.
	stale := bytes.Repeat([]byte{0xAA}, len(body)+10)
	if err := os.WriteFile(inst.Path(ModelQuality)+".part", stale, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := inst.Download(context.Background(), ModelQuality, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	assertInstalled(t, inst, body)
}

// A response longer than the pinned size is cut off one byte past it and
// fails, instead of filling the disk until the SHA-256 check runs. The part
// file is removed as for a checksum mismatch. Covers a fresh download (200)
// and a resumed one (206).
func TestDownloadLargerThanPinnedSizeIsCutOffAndRemoved(t *testing.T) {
	body := modelBytes()
	const extra = 4 << 20 // far more than the model: a wrong or hostile response
	const have = 120_000
	cases := []struct {
		name    string
		part    []byte
		handler http.HandlerFunc
	}{
		{
			name: "fresh",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(body)
				_, _ = w.Write(make([]byte, extra))
			},
		},
		{
			name: "resumed",
			part: body[:have],
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Range", "bytes 120000-*/*")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(body[have:])
				_, _ = w.Write(make([]byte, extra))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)
			inst := testInstaller(t, srv.URL, body)
			m := inst.Models[ModelQuality]
			m.Size = int64(len(body))
			inst.Models[ModelQuality] = m
			part := inst.Path(ModelQuality) + ".part"
			if tc.part != nil {
				if err := os.WriteFile(part, tc.part, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var maxRecv int64
			err := inst.Download(context.Background(), ModelQuality, func(recv, _ int64) { maxRecv = max(maxRecv, recv) })
			if err == nil || !strings.Contains(err.Error(), "larger than the pinned size") {
				t.Fatalf("err = %v, want a larger-than-the-pinned-size error", err)
			}
			if maxRecv > m.Size+1 {
				t.Fatalf("wrote %d bytes to the part file, want at most the pinned size + 1 (%d)", maxRecv, m.Size+1)
			}
			if inst.Has(ModelQuality) {
				t.Fatal("an oversized download must never become the model")
			}
			if _, statErr := os.Stat(part); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("an oversized part file must be deleted so the next attempt starts clean")
			}
		})
	}
}

// failingBody delivers data and returns errBodyBroken together with the read
// that reaches failAt bytes, as a connection that breaks mid-read does.
type failingBody struct {
	data   []byte
	off    int
	failAt int
}

var errBodyBroken = errors.New("connection reset mid-body")

func (b *failingBody) Read(p []byte) (int, error) {
	n := copy(p, b.data[b.off:])
	b.off += n
	if b.off >= b.failAt {
		return n, errBodyBroken
	}
	return n, nil
}

func (*failingBody) Close() error { return nil }

// bodyTransport answers every request with 200 and body, without a network.
type bodyTransport struct{ body io.ReadCloser }

func (t bodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{},
		Body: t.body, ContentLength: -1, Request: req}, nil
}

// A read error that arrives together with bytes past the pinned size is the
// oversize failure, and the part file goes; an error below the size keeps
// the part file for a resume, as before.
func TestDownloadBodyErrorAroundThePinnedSize(t *testing.T) {
	body := modelBytes()
	size := int64(len(body))
	t.Run("past the size", func(t *testing.T) {
		data := append(append([]byte{}, body...), make([]byte, 64<<10)...)
		inst := testInstaller(t, "http://choix.invalid/model.onnx", body)
		m := inst.Models[ModelQuality]
		m.Size = size
		inst.Models[ModelQuality] = m
		inst.HTTPClient = &http.Client{Transport: bodyTransport{&failingBody{data: data, failAt: int(size) + 1}}}

		err := inst.Download(context.Background(), ModelQuality, nil)
		if err == nil || !strings.Contains(err.Error(), "larger than the pinned size") {
			t.Fatalf("err = %v, want a larger-than-the-pinned-size error", err)
		}
		if _, statErr := os.Stat(inst.Path(ModelQuality) + ".part"); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("an oversized part file must be deleted even when the read also failed")
		}
	})
	t.Run("below the size", func(t *testing.T) {
		const got = 200_000
		inst := testInstaller(t, "http://choix.invalid/model.onnx", body)
		m := inst.Models[ModelQuality]
		m.Size = size
		inst.Models[ModelQuality] = m
		inst.HTTPClient = &http.Client{Transport: bodyTransport{&failingBody{data: body[:got], failAt: got}}}

		err := inst.Download(context.Background(), ModelQuality, nil)
		if !errors.Is(err, errBodyBroken) {
			t.Fatalf("err = %v, want the body's read error", err)
		}
		st, statErr := os.Stat(inst.Path(ModelQuality) + ".part")
		if statErr != nil || st.Size() != got {
			t.Fatalf("part file should be kept with %d bytes for a resume: %v, %v", got, st, statErr)
		}
	})
}

// stallingServer sends the first half of body, then blocks until release is
// closed.
func stallingServer(t *testing.T, body []byte, release chan struct{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "300000")
		_, _ = w.Write(body[:len(body)/2])
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv
}

func TestDownloadCancellationIsPromptAndKeepsPart(t *testing.T) {
	body := modelBytes()
	srv := stallingServer(t, body, make(chan struct{}))
	inst := testInstaller(t, srv.URL, body)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- inst.Download(ctx, ModelQuality, nil) }()
	// Wait until the first half is on disk and the server has gone quiet.
	part := inst.Path(ModelQuality) + ".part"
	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, err := os.Stat(part); err == nil && st.Size() >= int64(len(body)/2) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("download never wrote the first half")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	start := time.Now()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Download did not return within 1s of cancellation")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("cancellation took %v", d)
	}
	st, err := os.Stat(part)
	if err != nil || st.Size() == 0 {
		t.Fatalf("part file should be kept for a later resume: %v", err)
	}
	if inst.Has(ModelQuality) {
		t.Fatal("a cancelled download must not install anything")
	}
}

func TestDownloadStalledConnectionFails(t *testing.T) {
	body := modelBytes()
	srv := stallingServer(t, body, make(chan struct{}))
	inst := testInstaller(t, srv.URL, body)
	inst.StallTimeout = 150 * time.Millisecond

	done := make(chan error, 1)
	go func() { done <- inst.Download(context.Background(), ModelQuality, nil) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stalled") {
			t.Fatalf("err = %v, want a stalled-connection error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a stalled download must fail instead of hanging")
	}
}

func TestDownloadWaitsForLockThenFindsModelInstalled(t *testing.T) {
	body := modelBytes()
	var reqs atomic.Int32
	var rng atomic.Value
	srv := rangeServer(t, body, &reqs, &rng)
	inst := testInstaller(t, srv.URL, body)

	// Another process holds the download lock.
	unlock, err := lockFile(context.Background(), inst.Path(ModelQuality)+".lock")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- inst.Download(context.Background(), ModelQuality, nil) }()
	select {
	case err := <-done:
		t.Fatalf("Download returned (%v) while the lock was held", err)
	case <-time.After(200 * time.Millisecond):
	}
	// ...and finishes the job before releasing it.
	if err := os.WriteFile(inst.Path(ModelQuality), body, 0o600); err != nil {
		t.Fatal(err)
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Download did not notice the released lock")
	}
	if reqs.Load() != 0 {
		t.Fatalf("requests = %d, want 0 (the other process already installed the model)", reqs.Load())
	}
}

func TestDownloadTakesOverWhenLockOwnerGivesUp(t *testing.T) {
	body := modelBytes()
	var reqs atomic.Int32
	var rng atomic.Value
	srv := rangeServer(t, body, &reqs, &rng)
	inst := testInstaller(t, srv.URL, body)

	unlock, err := lockFile(context.Background(), inst.Path(ModelQuality)+".lock")
	if err != nil {
		t.Fatal(err)
	}
	// The owner got part of the way, then gave up without installing.
	if err := os.WriteFile(inst.Path(ModelQuality)+".part", body[:1000], 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- inst.Download(context.Background(), ModelQuality, nil) }()
	time.Sleep(100 * time.Millisecond)
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter never took over")
	}
	assertInstalled(t, inst, body)
	if got := rng.Load().(string); got != "bytes=1000-" {
		t.Fatalf("Range = %q, want the waiter to resume the owner's part file", got)
	}
}

func TestDownloadLockWaitHonoursCancellation(t *testing.T) {
	inst := testInstaller(t, "http://127.0.0.1:0/unused", modelBytes())
	unlock, err := lockFile(context.Background(), inst.Path(ModelQuality)+".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := inst.Download(ctx, ModelQuality, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's error", err)
	}
}

// With Has(ctx, kind) (bool, error) gone, the embedded *ModelStore's Has is
// promoted, so the Installer is all a downloader needs.
func TestInstaller_HasIsModelStoreHas(t *testing.T) {
	var _ interface{ Has(ModelKind) bool } = (*Installer)(nil)
}

func TestKnownQualityModelIsPinned(t *testing.T) {
	m, ok := KnownModels[ModelQuality]
	if !ok {
		t.Fatal("KnownModels missing the quality entry")
	}
	if m.FileName == "" || !strings.HasPrefix(m.URL, "https://") || len(m.SHA256) != 64 {
		t.Errorf("quality model is not pinned: %+v", m)
	}
	if k, ok := ParseModelKind(ModelQuality.String()); !ok || k != ModelQuality {
		t.Errorf("ParseModelKind(%q) = %v, %v", ModelQuality.String(), k, ok)
	}
}

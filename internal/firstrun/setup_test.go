package firstrun

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/deps"
)

type stubDetector struct{ st SetupState }

func (s stubDetector) Detect(_ context.Context) (SetupState, error) { return s.st, nil }

func TestSetupHandler_RendersStepsForMissingDeps(t *testing.T) {
	d := stubDetector{st: SetupState{
		RequiresExifTool: true,
		RequiresFFmpeg:   true,
		RequiresModels:   []local.ModelKind{local.ModelCLIP},
	}}
	h := NewSetupHandler(d)
	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := strings.ToLower(rr.Body.String())
	for _, want := range []string{"exiftool", "ffmpeg", "clip"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestSetupHandler_RedirectsWhenReady(t *testing.T) {
	d := stubDetector{st: SetupState{}} // nothing required
	h := NewSetupHandler(d)
	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/" {
		t.Errorf("location = %q, want /", rr.Header().Get("Location"))
	}
}

type stubDownloader struct{ events []deps.ProgressEvent }

func (s stubDownloader) Fetch(_ context.Context, _ string, ch chan<- deps.ProgressEvent) error {
	for _, ev := range s.events {
		ch <- ev
	}
	close(ch)
	return nil
}

func TestInstallToolEndpoint_StreamsSSE(t *testing.T) {
	d := stubDownloader{events: []deps.ProgressEvent{
		{Stage: "fetch", PercentDone: 0.5},
		{Stage: "verify", PercentDone: 1.0},
	}}
	h := NewInstallToolHandler(d)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/install-tool?name=exiftool", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: progress") {
		t.Errorf("missing progress event: %q", body)
	}
	if !strings.Contains(body, `"percent_done":1`) {
		t.Errorf("missing 100%% event: %q", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("missing done event: %q", body)
	}
}

func TestInstallToolEndpoint_RejectsUnknownTool(t *testing.T) {
	h := NewInstallToolHandler(stubDownloader{})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/install-tool?name=evil", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

type stubModelInstaller struct{ events []deps.ProgressEvent }

func (s stubModelInstaller) Install(_ context.Context, _ local.ModelKind, ch chan<- deps.ProgressEvent) error {
	for _, ev := range s.events {
		ch <- ev
	}
	close(ch)
	return nil
}

func TestInstallModelEndpoint_StreamsSSE(t *testing.T) {
	m := stubModelInstaller{events: []deps.ProgressEvent{{Stage: "fetch", PercentDone: 0.25}, {Stage: "verify", PercentDone: 1.0}}}
	h := NewInstallModelHandler(m)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/install-model?kind=clip", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q", got)
	}
	if !strings.Contains(rr.Body.String(), `"percent_done":1`) {
		t.Errorf("missing finish event: %s", rr.Body.String())
	}
}

func TestInstallModelEndpoint_RejectsUnknownKind(t *testing.T) {
	h := NewInstallModelHandler(stubModelInstaller{})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/install-model?kind=bogus", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

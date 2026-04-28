//go:build !short

// Package firstrun_test exercises the cold-start integration: a fresh machine
// with nothing installed should be walked through the wizard via HTTP until
// the /setup endpoint redirects to /.
package firstrun_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/deps"
	"github.com/volodymyrsmirnov/choix/internal/firstrun"
)

// fullStub is a single in-memory implementation of every interface the
// /setup wizard needs. It transitions from "needs everything" to
// "ready" as Install* are called.
type fullStub struct {
	exif, ff bool
	models   map[local.ModelKind]bool
}

func (s *fullStub) Resolve(_ context.Context, name string) (string, error) {
	if (name == "exiftool" && s.exif) || (name == "ffmpeg" && s.ff) {
		return "/installed/" + name, nil
	}
	return "", deps.ErrNotFound
}

func (s *fullStub) Has(_ context.Context, k local.ModelKind) (bool, error) { return s.models[k], nil }

func (s *fullStub) Fetch(_ context.Context, name string, ch chan<- deps.ProgressEvent) error {
	ch <- deps.ProgressEvent{Stage: "fetch", PercentDone: 1}
	close(ch)
	switch name {
	case "exiftool":
		s.exif = true
	case "ffmpeg":
		s.ff = true
	}
	return nil
}

func (s *fullStub) Install(_ context.Context, k local.ModelKind, ch chan<- deps.ProgressEvent) error {
	ch <- deps.ProgressEvent{Stage: "fetch", PercentDone: 1}
	close(ch)
	s.models[k] = true
	return nil
}

func (s *fullStub) Detect(ctx context.Context) (firstrun.SetupState, error) {
	return firstrun.Detect(ctx, s, s, config.Config{})
}

func TestColdStart_WizardCompletesAndRedirects(t *testing.T) {
	st := &fullStub{models: map[local.ModelKind]bool{}}
	mux := http.NewServeMux()
	mux.Handle("/setup", firstrun.NewSetupHandler(st))
	mux.Handle("/api/setup/install-tool", firstrun.NewInstallToolHandler(st))
	mux.Handle("/api/setup/install-model", firstrun.NewInstallModelHandler(st))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// 1. Initial GET /setup should render the wizard, not redirect.
	resp, err := http.Get(srv.URL + "/setup")
	if err != nil {
		t.Fatalf("GET /setup: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("initial /setup status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. Install tools.
	for _, tool := range []string{"exiftool", "ffmpeg"} {
		r, err := http.Post(srv.URL+"/api/setup/install-tool?name="+tool, "", nil)
		if err != nil {
			t.Fatalf("install %s: %v", tool, err)
		}
		r.Body.Close()
	}
	// 3. Install the only model the wizard cares about now.
	r, err := http.Post(srv.URL+"/api/setup/install-model?kind="+local.ModelCLIP.String(), "", nil)
	if err != nil {
		t.Fatalf("install model: %v", err)
	}
	r.Body.Close()

	// 4. /setup should now redirect.
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp2, err := cli.Get(srv.URL + "/setup")
	if err != nil {
		t.Fatalf("final /setup: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusSeeOther {
		t.Fatalf("final /setup status = %d, want 303", resp2.StatusCode)
	}
}

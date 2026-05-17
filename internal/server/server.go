// Package server implements the choix HTTP server: HTML pages over chi,
// JSON / SSE API endpoints, and embedded UI assets.
//
// The server runs alongside the engine pipeline in the same process. It
// owns its lifecycle (Run/Shutdown) and exposes a few injectable
// dependencies (Store, ScanRoot, Pipeline) so tests can use httptest.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/deps"
	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/ui"
)

// PipelineRunner abstracts the engine pipeline so the server can drive
// a real pipeline at runtime and a stub in tests.
type PipelineRunner interface {
	Start(ctx context.Context) (<-chan pipeline.Event, error)
	Stop()
	Running() bool
}

// ErrPipelineAlreadyRunning is returned by PipelineRunner.Start when a
// pipeline is already in progress. Setup finalization treats this as a
// no-op rather than an error.
var ErrPipelineAlreadyRunning = errors.New("pipeline already running")

// Config configures a Server. Fields are required unless noted.
type Config struct {
	Store     *store.Store
	ScanRoot  string // absolute path to the folder being curated
	Port      int    // 0 = pick free
	IdleAfter time.Duration
	Pipeline  PipelineRunner   // optional; nil disables /api/scan
	Installer *local.Installer // optional; nil disables /setup wizard
	// Exiftool + Ffmpeg power the on-demand /full/ transcode path for
	// formats browsers can't render (HEIC, RAF). When nil the handler
	// falls back to a sips-only conversion, which is correct but slower
	// for camera RAW (sips demosaics the sensor; exiftool can extract a
	// large embedded preview JPEG in a fraction of the time).
	Exiftool *meta.ExifTool
	Ffmpeg   *deps.Runner
	// BackgroundContext is the long-lived parent context for work that
	// must outlive a single HTTP request — e.g. /api/setup/finalize
	// kicking off a pipeline re-analyze. Optional; defaults to
	// context.Background() when nil. The CLI passes its SIGINT-aware
	// rootCtx so Ctrl-C cancels background work cleanly.
	BackgroundContext context.Context
}

// Server is the HTTP front-end. The zero value is not usable; construct
// via New.
type Server struct {
	cfg        Config
	staticFS   fs.FS
	indexHTML  []byte
	httpServer *http.Server
	listener   net.Listener
	url        string
	idleMu     sync.Mutex
	lastReq    time.Time
	idleStopCh chan struct{}
	progress   *progressHub
	pipe       PipelineRunner
	Now        func() time.Time // injectable for tests
	liveCfg    atomic.Pointer[config.Config]
	detector   *installerStateDetector // nil when cfg.Installer == nil
	ready      atomic.Bool             // latched true once Detect reports ready
}

// New builds a Server with embedded static assets. It does not start
// listening; call Start to bind, then Run to serve.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("server.New: Store is required")
	}
	if cfg.ScanRoot == "" {
		return nil, errors.New("server.New: ScanRoot is required")
	}
	if cfg.IdleAfter == 0 {
		cfg.IdleAfter = 10 * time.Minute
	}
	idx, err := ui.IndexHTML()
	if err != nil {
		return nil, fmt.Errorf("load index.html: %w", err)
	}
	s := &Server{
		cfg:        cfg,
		staticFS:   ui.DistFS(),
		indexHTML:  idx,
		lastReq:    time.Now(),
		idleStopCh: make(chan struct{}),
		progress:   newProgressHub(),
		pipe:       cfg.Pipeline,
		Now:        time.Now,
	}
	if cfg.Installer != nil {
		s.detector = &installerStateDetector{r: pathResolver{}, installer: cfg.Installer}
	}
	// One-shot KV→TOML migration: carry pre-upgrade Settings (which used
	// to live in the per-folder KV table) into the machine-wide
	// config.toml. Runs on every Server.New for resilience; it's
	// idempotent — after the first successful pass the KV rows are gone
	// and subsequent calls short-circuit. Failures are logged but not
	// fatal: a stale Settings page is preferable to refusing to start.
	if err := migrateLegacyKVSettings(cfg.Store); err != nil {
		slog.Warn("legacy settings migration failed", "err", err)
	}

	// Initialize liveCfg with current config (best-effort, post-migration).
	if c, err := config.Load(); err == nil {
		s.liveCfg.Store(c)
	}
	return s, nil
}

// Start binds the listener and prepares the http.Server. It does not
// block. Call Run to serve.
func (s *Server) Start() error {
	port, err := ResolvePort(s.cfg.Port)
	if err != nil {
		return fmt.Errorf("resolve port: %w", err)
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.listener = ln
	s.url = "http://" + ln.Addr().String()
	s.httpServer = &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	slog.Info("server listening", "url", s.url, "idle_after", s.cfg.IdleAfter.String())
	return nil
}

// URL returns the bound URL. Call after Start.
func (s *Server) URL() string { return s.url }

// Run blocks serving HTTP until ctx is cancelled or Shutdown is called
// from another goroutine. Returns nil on graceful shutdown.
func (s *Server) Run(ctx context.Context) error {
	if s.httpServer == nil {
		if err := s.Start(); err != nil {
			return err
		}
	}
	errCh := make(chan error, 1)
	go func() {
		err := s.httpServer.Serve(s.listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	idleCtx, idleCancel := context.WithCancel(ctx)
	defer idleCancel()
	go s.idleWatcher(idleCtx)

	select {
	case <-ctx.Done():
		slog.Info("server shutdown", "reason", "context_cancelled")
		idleCancel()
		return s.Shutdown(context.Background())
	case <-s.idleStopCh:
		slog.Info("server shutdown", "reason", "idle_timeout", "idle_after", s.cfg.IdleAfter.String())
		idleCancel()
		return s.Shutdown(context.Background())
	case err := <-errCh:
		slog.Info("server shutdown", "reason", "serve_returned", "err", err)
		idleCancel()
		return err
	}
}

// Shutdown stops the server. SSE clients hold connections open
// indefinitely, so we wait briefly for normal handlers to drain and then
// force-close any remaining connections (the SSE clients) so the process
// can exit promptly. A user pressing Ctrl+C should see the program quit
// in well under a second, not block for the full graceful timeout.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	err := s.httpServer.Shutdown(shutdownCtx)
	if err != nil {
		// Graceful drain didn't finish (typically because of long-lived
		// SSE connections). Force-close them so the program can exit.
		_ = s.httpServer.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
	}
	return err
}

// idleWatcher checks for inactivity and closes idleStopCh when the idle
// timeout is exceeded. SSE keepalives do NOT reset the timer (excluded in middleware).
func (s *Server) idleWatcher(ctx context.Context) {
	// Poll at half the idle timeout, capped to 10s so production servers
	// don't spin, but test servers with a short IdleAfter respond quickly.
	interval := s.cfg.IdleAfter / 2
	if interval > 10*time.Second {
		interval = 10 * time.Second
	}
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.idleMu.Lock()
			last := s.lastReq
			s.idleMu.Unlock()
			if s.Now().Sub(last) > s.cfg.IdleAfter {
				select {
				case <-s.idleStopCh:
					// already closed
				default:
					close(s.idleStopCh)
				}
				return
			}
		}
	}
}

// idleResetMiddleware updates lastReq on every non-SSE request.
func (s *Server) idleResetMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SSE keepalives shouldn't count as activity — a long-lived
		// /api/progress connection would defeat the timer.
		if r.URL.Path != "/api/progress" {
			s.idleMu.Lock()
			s.lastReq = s.Now()
			s.idleMu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

// requestLogMiddleware logs each request via slog. State-changing requests
// (non-GET to /api/*) and any non-2xx response are logged at INFO so the
// console reflects user actions without drowning in /thumb/ + /api/library
// chatter; quiet successful reads stay at DEBUG.
func (s *Server) requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		level := slog.LevelDebug
		switch {
		case rw.status >= 400:
			level = slog.LevelWarn
		case r.Method != http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/"):
			level = slog.LevelInfo
		}
		slog.Log(r.Context(), level, "http", //nolint:gosec // path is from request URL, not injected into shell
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher so the SSE handler can stream events
// through the chi middleware without getting "Streaming unsupported!".
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap implements the http.ResponseController unwrap chain.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// serveIndex writes the SPA's index.html to w. Used for every non-API
// page route so client-side routing can resolve /, /focus/{id}, /settings.
//
// When the first-run wizard is wired (Config.Installer non-nil) and
// prerequisites are missing, redirect to /setup so the wizard runs
// before the SPA starts polling /api/library. We latch s.ready once
// Detect reports IsReady so subsequent page loads skip the syscalls
// (exec.LookPath × 2 + os.Stat).
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	if s.detector != nil && !s.ready.Load() {
		if st, err := s.detector.Detect(r.Context()); err == nil {
			if st.IsReady() {
				s.ready.Store(true)
			} else {
				http.Redirect(w, r, "/setup", http.StatusSeeOther)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(s.indexHTML)
}

// ReloadConfig reloads config from disk into the live atomic pointer.
func (s *Server) ReloadConfig() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	s.liveCfg.Store(cfg)
	return nil
}

// AttachProgress connects a pipeline event channel to the progress SSE hub.
// Events are consumed and broadcast to any active /api/progress listeners.
func (s *Server) AttachProgress(events <-chan pipeline.Event) {
	s.progress.Attach(events)
}

func (s *Server) handleProgressSSE(w http.ResponseWriter, r *http.Request) {
	s.progress.ServeHTTP(w, r)
}

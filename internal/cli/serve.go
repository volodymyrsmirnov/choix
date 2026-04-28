package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/meta"
	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/server"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// servePipelineAdapter wraps pipeline.Pipeline to satisfy server.PipelineRunner.
//
// preRun is invoked before each Run so the adapter can refresh state that
// may have changed via Settings since startup — most importantly the picks
// export dir, which must stay in sync with what the scanner skips.
type servePipelineAdapter struct {
	pipe     *pipeline.Pipeline
	analyzer *local.Analyzer
	preRun   func()
	mu       sync.Mutex
	run      bool
	stop     context.CancelFunc
	done     chan struct{}
}

func (a *servePipelineAdapter) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.run
}

func (a *servePipelineAdapter) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stop != nil {
		a.stop()
	}
	a.run = false
}

// Wait blocks until the running pipeline goroutine has fully exited (after
// any in-flight ONNX inference returns). Safe to call when no goroutine is
// running; returns immediately. Must be called before Close so analyzer
// session destruction doesn't race against an in-flight model.Run.
func (a *servePipelineAdapter) Wait() {
	a.mu.Lock()
	d := a.done
	a.mu.Unlock()
	if d != nil {
		<-d
	}
}

// Close releases analyzer resources (ONNX sessions). Caller must Wait()
// first; otherwise an in-flight inference can use-after-free the C handle.
func (a *servePipelineAdapter) Close() error {
	if a.analyzer == nil {
		return nil
	}
	return a.analyzer.Close()
}

func (a *servePipelineAdapter) Start(parent context.Context) (<-chan pipeline.Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.run {
		return nil, fmt.Errorf("pipeline already running: %w", server.ErrPipelineAlreadyRunning)
	}
	if a.preRun != nil {
		a.preRun()
	}
	ch := make(chan pipeline.Event, 256)
	a.pipe.Reporter = pipeline.NewReporter(ch)
	ctx, cancel := context.WithCancel(parent)
	a.stop = cancel
	a.run = true
	a.done = make(chan struct{})
	done := a.done
	slog.Info("pipeline starting")
	go func() {
		defer func() {
			close(ch)
			a.mu.Lock()
			a.run = false
			a.mu.Unlock()
			close(done)
			slog.Info("pipeline goroutine exit")
		}()
		err := a.pipe.Run(ctx)
		if err != nil {
			slog.Error("pipeline run failed", "err", err)
		} else {
			slog.Info("pipeline run done")
		}
	}()
	return ch, nil
}

// runDefault implements the default `choix [folder]` action.
func runDefault(cmd *cobra.Command, args []string) error {
	root := "."
	if len(args) == 1 {
		root = args[0]
	}
	return runServe(cmd, root)
}

func runServe(cmd *cobra.Command, root string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	if err := ensureScanRoot(absRoot); err != nil {
		return err
	}

	dbPath := filepath.Join(absRoot, ".choix", "state.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return fmt.Errorf("mkdir .choix: %w", err)
	}
	dsn := "file:" + dbPath + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	st, err := store.Open(dsn)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	idle, _ := cmd.Flags().GetDuration("idle-after")
	noOpen, _ := cmd.Flags().GetBool("no-open")
	port, _ := cmd.Flags().GetInt("port")

	// Populate GPS columns for files that completed the metadata stage
	// before migration 004 added gps_lat / gps_lon. Reads each row's
	// stored raw_exif blob and re-parses; never re-runs exiftool. The
	// function is idempotent so re-running it on later boots is safe and
	// near-instant once everything is filled in.
	if n, err := meta.BackfillGPS(cmd.Context(), st); err != nil {
		slog.Warn("gps backfill failed", "err", err)
	} else if n > 0 {
		slog.Info("gps backfill", "rows", n)
	}

	pipe, err := buildServePipeline(st, absRoot)
	if err != nil {
		return err
	}

	// Single signal-aware context governs both the HTTP server and the
	// background pipeline. SIGINT cancels both. Built before server.New
	// so handlers that kick off background work (e.g. /api/setup/finalize
	// re-analyze) can inherit it instead of tying long-lived work to a
	// single request.
	rootCtx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv, err := server.New(server.Config{
		Store:             st,
		ScanRoot:          absRoot,
		Port:              port,
		IdleAfter:         idle,
		Pipeline:          pipe,
		Installer:         local.NewInstaller(modelsDir()),
		BackgroundContext: rootCtx,
	})
	if err != nil {
		return err
	}
	if err := srv.Start(); err != nil {
		return err
	}

	fmt.Println("choix serving at", srv.URL())
	slog.Info("choix serving", "url", srv.URL(), "root", absRoot, "idle_after", idle.String(), "no_open", noOpen)
	if !noOpen {
		slog.Info("opening browser", "url", srv.URL())
		if openErr := server.OpenBrowser(srv.URL()); openErr != nil {
			// Non-fatal: the user can copy/paste the URL.
			slog.Warn("auto-open browser failed", "err", openErr)
			fmt.Fprintln(os.Stderr, "warning: could not auto-open browser:", openErr)
		}
	}

	// Trigger an initial scan automatically.
	go func() {
		slog.Info("auto-trigger initial scan", "root", absRoot)
		events, err := pipe.Start(rootCtx)
		if err != nil {
			slog.Error("auto-trigger scan failed to start", "err", err)
			return
		}
		slog.Info("attaching pipeline events to progress hub")
		srv.AttachProgress(events)
	}()

	err = srv.Run(rootCtx)
	// Stop the pipeline (cancels its context), wait for any in-flight
	// inference to return, then release analyzer resources. Order matters:
	// Close() destroys ONNX C sessions, so Wait() must complete first.
	pipe.Stop()
	pipe.Wait()
	_ = pipe.Close()
	return err
}

// buildServePipeline constructs a pipeline adapter for the serve command.
// Returns an error if a required external tool is missing — exiftool is
// load-bearing for metadata extraction, and the metadata stage panics if
// MetaExtractor is nil. Better to fail fast with a clear message than
// crash the auto-triggered scan goroutine on the first file.
//
// Honours the persisted clustering settings (BucketSizeSec, VisualClusterThreshold)
// so the auto-triggered first scan uses what the user last saved in Settings,
// not the built-in defaults.
func buildServePipeline(st *store.Store, scanRoot string) (*servePipelineAdapter, error) {
	cfg, err := loadEffectiveConfig(scanRoot)
	if err != nil {
		return nil, err
	}
	p, analyzer, sc, err := buildPipelineCore(st, scanRoot, &cfg)
	if err != nil {
		return nil, err
	}
	// Refresh the scanner's picks-dir override on every Run so a picks_dir
	// changed via Settings after startup takes effect on the next scan
	// without requiring a restart.
	preRun := func() {
		freshCfg, cfgErr := loadEffectiveConfig(scanRoot)
		if cfgErr != nil {
			freshCfg = cfg
		}
		sc.SetPicksDir(effectivePicksDir(st, &freshCfg))
	}
	return &servePipelineAdapter{pipe: p, analyzer: analyzer, preRun: preRun}, nil
}

// addServeFlags registers the serve-related flags on cmd.
func addServeFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().Duration("idle-after", 10*time.Minute, "auto-shutdown after no requests")
	cmd.PersistentFlags().Bool("no-open", false, "do not auto-open the browser")
	cmd.PersistentFlags().Int("port", 0, "preferred listen port (0 = pick free)")
}

// ensureScanRoot checks that absRoot exists and is a directory. Returns a
// descriptive error if absent or not a directory, so a typo on the CLI
// produces a clear message rather than silently creating a new tree.
func ensureScanRoot(absRoot string) error {
	info, err := os.Stat(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("scan root %q does not exist or is not a directory", absRoot)
		}
		return fmt.Errorf("scan root %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("scan root %q does not exist or is not a directory", absRoot)
	}
	return nil
}

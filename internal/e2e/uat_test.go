//go:build uat && darwin

// TestUAT runs the whole headless runtime — the real platform packages,
// Vision's feature print and the quality formula — over clones of the owner's photo archive and
// checks what a user would rely on. It is opt-in: it skips unless
// CHOIX_UAT_ROOT is set to an existing folder; there is no default.
//
//	CHOIX_UAT_ROOT=<archive> go test -tags uat -short -run TestUAT -v ./internal/e2e/   (200 files a folder)
//	CHOIX_UAT_ROOT=<archive> task uat                                                   (every file)
//	CHOIX_UAT_ROOT=<archive> go test -tags uat -run TestUAT -v ./internal/e2e/ -folder 1  (folder-1 only, every file)
//
// -folder K scans and checks only folder-K and skips the preemption and quit
// phase; the originals of every folder are still compared. Visual grouping
// and the quality rating are both on.
//
// Privacy (R22): the archive is private and this repository is public. The
// test never prints a folder name, file name, serial, camera model, date or
// location. Folders are "folder-1" (the most media files) … "folder-N"; a
// file is "folder-K, <extension>, #<index>"; error texts have their paths
// stripped. Expected counts are computed from the tree itself.
//
// The archive is only read: its manifest is recorded, each folder is cloned
// with `cp -c` (APFS clones) into a temporary directory, and every write the
// app makes lands in the clone or in a temporary data directory.
package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg" // DecodeConfig of thumbnails and sources
	_ "image/png"  // DecodeConfig of PNG sources
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/analyze"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/runtime"
	"github.com/volodymyrsmirnov/choix/internal/app/session"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/avmedia"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/bookmark"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/imageio"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/vision"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/volumes"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/workspace"
	"github.com/volodymyrsmirnov/choix/internal/rawpreview"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

var (
	uatLimit = flag.Int("limit", 0, "TestUAT: clone only the first N media files of each folder (0: every file; -short: 200)")
	uatOnly  = flag.Int("folder", 0, "TestUAT: scan and check only folder-K (1: the most media files) and skip the preemption and quit phase (0: every folder)")
)

const (
	// uatRootEnv names the archive. It has no default.
	uatRootEnv = "CHOIX_UAT_ROOT"
	// uatShortLimit is the files per folder under -short.
	uatShortLimit = 200
	// uatPreemptStart bounds the time from opening a queued folder to the
	// start of its job while every job slot is busy.
	uatPreemptStart = 2 * time.Second
	// uatPreviews is the length of a Library preview strip.
	uatPreviews = 6
	// uatKeyframes is the number of keyframes kept per video.
	uatKeyframes = 5
	// uatExamples bounds the per-file examples logged for one finding.
	uatExamples = 10
)

// uatStages are the pipeline stages in order (events.Progress.Stage).
var uatStages = []string{"discover", "metadata", "thumb", "analyze", "cluster"}

// --- the corpus -------------------------------------------------------------

// uatFolder is one immediate sub-folder of the archive that holds media.
type uatFolder struct {
	label  string   // folder-K
	src    string   // the original; never printed
	media  []string // the media files the scanner sees, slash-separated, walk order
	before manifest
}

// mediaFiles lists, in walk order, the files under root the scanner indexes
// (scanner.Walk's rules): a media extension (scanner.DetectFromExt), outside
// hidden directories (.choix among them) and node_modules at any depth, and
// outside skip — the export directories relative to root, matched
// case-insensitively, that the engine has the scanner skip (engineSkipDirs).
func mediaFiles(root string, skip []string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || underAny(rel, skip) {
				return fs.SkipDir
			}
			return nil
		}
		if _, ok := scanner.DetectFromExt(d.Name()); ok && !underAny(rel, skip) {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

// underAny reports whether rel is one of dirs or under one, compared
// case-insensitively (the scanner's isUnderDir).
func underAny(rel string, dirs []string) bool {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if strings.EqualFold(rel, d) || len(rel) > len(d)+1 && strings.EqualFold(rel[:len(d)+1], d+"/") {
			return true
		}
	}
	return false
}

// engineSkipDirs are the directories the engine has the scanner skip in a
// folder — session.scannerSkipDirs, which is not exported, rebuilt from the
// same exported pieces: the effective picks directory and the folder's other
// export directories (picks.EffectiveDir over the folder's state.db and the
// picks_dir setting), each validated and cleaned as
// session.sessionCleanPicksDir does (picks.ValidateDir before and after
// path.Clean), and kept only when picks.CheckDir accepts it: a directory
// under which state.db has scanned originals is walked as originals.
// Duplicates are dropped case-insensitively.
func engineSkipDirs(st *store.Store, cfg config.Config) ([]string, error) {
	dir, others, err := picks.EffectiveDir(st, cfg.PicksDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range append([]string{dir}, others...) {
		if picks.ValidateDir(d) != nil {
			continue
		}
		d = path.Clean(filepath.ToSlash(d))
		if picks.ValidateDir(d) != nil {
			continue
		}
		if slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, d) }) {
			continue
		}
		err := picks.CheckDir(st, d)
		switch {
		case errors.Is(err, picks.ErrDirHasOriginals):
			continue
		case err != nil:
			return nil, err
		}
		out = append(out, strings.Trim(d, "/"))
	}
	return out, nil
}

// manifest maps every entry of a tree (relative path) to its type, size and
// modification time. Finder's .DS_Store files are left out: Finder rewrites
// them at any time. Directories are recorded by presence only.
type manifest map[string]manifestEntry

type manifestEntry struct {
	mode  fs.FileMode
	size  int64
	mtime int64
}

func readManifest(root string) (manifest, error) {
	m := manifest{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." || d.Name() == ".DS_Store" {
			return nil
		}
		e := manifestEntry{mode: d.Type()}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			e.size, e.mtime = info.Size(), info.ModTime().UnixNano()
		}
		m[filepath.ToSlash(rel)] = e
		return nil
	})
	return m, err
}

// diff counts the entries of after added, removed and changed against m.
func (m manifest) diff(after manifest) (added, removed, changed int) {
	for k, e := range m {
		a, ok := after[k]
		switch {
		case !ok:
			removed++
		case a != e:
			changed++
		}
	}
	for k := range after {
		if _, ok := m[k]; !ok {
			added++
		}
	}
	return added, removed, changed
}

// stateDirs counts the entries named .choix or picks (any case).
func (m manifest) stateDirs() int {
	n := 0
	for k := range m {
		if b := path.Base(k); b == ".choix" || strings.EqualFold(b, "picks") {
			n++
		}
	}
	return n
}

// --- privacy ---------------------------------------------------------------

// treeNames returns the relative paths of a folder's tree for the redactor:
// each manifest entry relative to the folder, and relative to the corpus
// root (under top, the folder's own name). Entries inside a .choix
// directory are left out: Choix names them itself (thumbnail buckets and
// file ids), and their short hex and numeric names would only make ordinary
// error texts unprintable.
func treeNames(top string, m manifest) []string {
	out := make([]string, 0, 2*len(m))
	for rel := range m {
		if slices.Contains(strings.Split(rel, "/"), ".choix") {
			continue
		}
		out = append(out, rel, top+"/"+rel)
	}
	return out
}

// countingHandler swallows every log record — they name files — and counts
// warnings and errors.
type countingHandler struct{ warn, errs *atomic.Int64 }

func (h countingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h countingHandler) Handle(_ context.Context, r slog.Record) error {
	switch {
	case r.Level >= slog.LevelError:
		h.errs.Add(1)
	case r.Level >= slog.LevelWarn:
		h.warn.Add(1)
	}
	return nil
}

func (h countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h countingHandler) WithGroup(string) slog.Handler      { return h }

// --- runtime fakes and recorder -----------------------------------------------

type uatPicker struct {
	mu    sync.Mutex
	paths []string
}

func (p *uatPicker) set(paths ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = paths
}

func (p *uatPicker) PickFolders() ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.paths), nil
}

type uatConfirm struct{}

func (uatConfirm) ConfirmRemove(string) (bool, error) { return true, nil }

type stamped struct {
	at    time.Time
	value string // a scan state, or a progress event's stage
	phase string // a progress event's phase
}

// recorder is the runtime's event sink: it timestamps every scan state and
// every stage seen in the scan progress of each folder.
type recorder struct {
	mu       sync.Mutex
	states   map[string][]stamped
	progress map[string][]stamped
}

func newRecorder() *recorder {
	return &recorder{states: map[string][]stamped{}, progress: map[string][]stamped{}}
}

func (r *recorder) Emit(name string, data any) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	switch ev := data.(type) {
	case events.ScanStateEvent:
		if name == events.ScanState {
			r.states[ev.FolderID] = append(r.states[ev.FolderID], stamped{at: now, value: ev.State})
		}
	case events.ScanProgressEvent:
		if name == events.ScanProgress {
			r.progress[ev.FolderID] = append(r.progress[ev.FolderID], stamped{at: now, value: ev.Progress.Stage, phase: ev.Phase})
		}
	}
}

// firstState returns when the folder first reported state at or after from.
func (r *recorder) firstState(id, state string, from time.Time) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.states[id] {
		if s.value == state && !s.at.Before(from) {
			return s.at, true
		}
	}
	return time.Time{}, false
}

// statesSince returns the states the folder reported at or after from.
func (r *recorder) statesSince(id string, from time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, s := range r.states[id] {
		if !s.at.Before(from) {
			out = append(out, s.value)
		}
	}
	return out
}

// progressCount counts the folder's progress events.
func (r *recorder) progressCount(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.progress[id])
}

// timing is one folder job's wall times.
type timing struct {
	stages map[string]time.Duration // per pipeline stage; absent: not observed
	after  time.Duration            // from the pipeline's end to ready: print backfill, rating, preview strip
	job    time.Duration            // from scanning to ready
}

// timing returns the wall times of the folder's first job. A stage runs
// from its first "starting" progress event to its first "done" one (both
// are sent at once, never coalesced); the pipeline ends with the first
// "done" of the cluster stage, and what follows until ready is the rest of
// the job.
func (r *recorder) timing(id string) (timing, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var start, ready time.Time
	for _, s := range r.states[id] {
		if s.value == "scanning" && start.IsZero() {
			start = s.at
		}
		if s.value == "ready" && !start.IsZero() {
			ready = s.at
			break
		}
	}
	if start.IsZero() || ready.IsZero() {
		return timing{}, false
	}
	began, ended := map[string]time.Time{}, map[string]time.Time{}
	for _, p := range r.progress[id] {
		if p.at.Before(start) || p.at.After(ready) {
			continue
		}
		switch p.phase {
		case "starting":
			if _, ok := began[p.value]; !ok {
				began[p.value] = p.at
			}
		case "done":
			_, b := began[p.value]
			_, e := ended[p.value]
			if b && !e {
				ended[p.value] = p.at
			}
		}
	}
	tm := timing{stages: map[string]time.Duration{}, job: ready.Sub(start)}
	for _, st := range uatStages {
		b, okB := began[st]
		e, okE := ended[st]
		if okB && okE {
			tm.stages[st] = e.Sub(b)
		}
	}
	if e, ok := ended["cluster"]; ok {
		tm.after = ready.Sub(e)
	}
	return tm, true
}

// --- the test ---------------------------------------------------------------

// uat is one TestUAT run.
type uat struct {
	t        *testing.T
	red      *redactor
	parent   string // the clones' directory
	rec      *recorder
	picker   *uatPicker
	r        *runtime.Runtime
	limit    int
	stills   platform.Stills // the real ImageIO reader, shared with the runtime
	stopped  chan struct{}
	stopOnce sync.Once
	sampled  atomic.Int64 // the most native calls seen running at once
}

// foreign marks text from outside the test (a stored error, a tool's
// output) passed to logf, errorf or fatalf: it is redacted like an error.
type foreign string

// say formats a message safe to print. The format and every other argument
// are the test's own (labels, numbers, kinds); each error argument is
// reduced by redactor.errText and each foreign one by redactor.text.
func (u *uat) say(format string, args ...any) string {
	args = slices.Clone(args)
	for i, a := range args {
		switch v := a.(type) {
		case error:
			args[i] = u.red.errText(v)
		case foreign:
			args[i] = u.red.text(string(v), "string")
		}
	}
	return fmt.Sprintf(format, args...)
}

func (u *uat) logf(format string, args ...any) {
	u.t.Helper()
	u.t.Log(u.say(format, args...))
}

func (u *uat) errorf(format string, args ...any) {
	u.t.Helper()
	u.t.Error(u.say(format, args...))
}

func (u *uat) fatalf(format string, args ...any) {
	u.t.Helper()
	u.t.Fatal(u.say(format, args...))
}

// fileRef is how a file is printed: "folder-K, <extension>, #<index>".
func fileRef(label, rel string, i int) string {
	return fmt.Sprintf("%s, %s, #%d", label, strings.ToLower(path.Ext(rel)), i+1)
}

func TestUAT(t *testing.T) {
	root := os.Getenv(uatRootEnv)
	if root == "" {
		t.Skip("set " + uatRootEnv + " to a folder of photos to run the UAT")
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		t.Skip(uatRootEnv + " is not an existing folder")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Skip(uatRootEnv + " cannot be made absolute")
	}

	// Redaction by what the test knows: the root, home and the temporary
	// directory (clones, data) are cut with the rest of any path under
	// them; the root's own name, every name directly under it and, once
	// each folder's manifest is read, every name of its tree are replaced.
	// The name set lives in memory only.
	red := newRedactor()
	red.addRoots(root, os.TempDir(), os.Getenv("HOME"))
	red.addPaths(filepath.Base(root))
	if real, err := filepath.EvalSymlinks(root); err == nil {
		red.addPaths(filepath.Base(real))
	}
	top, err := os.ReadDir(root)
	if err != nil {
		t.Fatal("read the corpus root: " + red.errText(err))
	}
	for _, e := range top {
		red.addPaths(e.Name())
	}

	limit := *uatLimit
	if limit <= 0 && testing.Short() {
		limit = uatShortLimit
	}

	// Log records name files: keep them out of the output, count them.
	var warns, errs atomic.Int64
	prevLog := slog.Default()
	slog.SetDefault(slog.New(countingHandler{warn: &warns, errs: &errs}))
	t.Cleanup(func() { slog.SetDefault(prevLog) })

	u := &uat{t: t, red: red, rec: newRecorder(), picker: &uatPicker{}, limit: limit}

	// The corpus folders, largest first, with their manifests.
	var folders []*uatFolder
	for _, e := range top {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		src := filepath.Join(root, e.Name())
		// No state.db is consulted here: the default picks directory.
		media, err := mediaFiles(src, []string{config.Defaults().PicksDir})
		if err != nil {
			u.fatalf("list the media of a corpus folder: %v", err)
		}
		if len(media) > 0 {
			folders = append(folders, &uatFolder{src: src, media: media})
		}
	}
	if len(folders) == 0 {
		t.Skip(uatRootEnv + " has no sub-folder with media")
	}
	sort.SliceStable(folders, func(i, j int) bool { return len(folders[i].media) > len(folders[j].media) })
	for i, f := range folders {
		f.label = fmt.Sprintf("folder-%d", i+1)
		m, err := readManifest(f.src)
		if err != nil {
			u.fatalf("%s: record the manifest: %v", f.label, err)
		}
		f.before = m
		red.addPaths(treeNames(filepath.Base(f.src), m)...)
		if n := m.stateDirs(); n > 0 {
			u.logf("%s: the original already holds %d entries named .choix or picks from earlier runs; checked to be unchanged", f.label, n)
		}
	}
	// The originals are as they were. A cleanup, so that it runs even when
	// the test stops early — after the runtime's Shutdown, registered later.
	t.Cleanup(func() { u.checkOriginals(folders) })

	if limit > 0 {
		u.logf("corpus: %d folder(s), the first %d media files of each", len(folders), limit)
	} else {
		u.logf("corpus: %d folder(s), every media file", len(folders))
	}
	run, err := selectFolders(len(folders), *uatOnly)
	if err != nil {
		u.fatalf("%v", err)
	}

	u.setup(t)

	// Every selected folder in turn: clone, add, wait, check.
	var rows []reportRow
	for _, i := range run {
		rows = append(rows, u.scanFolder(folders[i]))
	}

	if *uatOnly > 0 {
		u.logf("preemption and quit: skipped (-folder %d)", *uatOnly)
	} else {
		u.preemptAndQuit(folders)
	}

	u.stopSampling()
	high, sampled := platform.NativeHighWater(), int(u.sampled.Load())
	u.logf("native calls at once: high-water %d, sampled %d, slots %d", high, sampled, platform.NativeSlots)
	if high > platform.NativeSlots || sampled > platform.NativeSlots {
		u.errorf("native calls at once exceeded %d slots: high-water %d, sampled %d", platform.NativeSlots, high, sampled)
	}
	u.logf("log records swallowed: %d warnings, %d errors", warns.Load(), errs.Load())
	u.logf("redaction: %d text(s) withheld", u.red.withheldCount())

	report := renderReport(rows)
	fmt.Print(report)
	// The one file written outside t.TempDir: see writeReport.
	if _, err := writeReport(os.TempDir(), root, []byte(report)); err != nil {
		u.errorf("write the report: %v", err)
	}
}

// checkOriginals compares every original with its recorded manifest: no
// entry added, removed or changed, and no .choix or picks entry appeared.
func (u *uat) checkOriginals(folders []*uatFolder) {
	for _, f := range folders {
		after, err := readManifest(f.src)
		if err != nil {
			u.errorf("%s: re-read the manifest: %v", f.label, err)
			continue
		}
		if a, r, c := f.before.diff(after); a+r+c > 0 {
			u.errorf("%s: the original changed: %d added, %d removed, %d changed", f.label, a, r, c)
		}
		if n, was := after.stateDirs(), f.before.stateDirs(); n != was {
			u.errorf("%s: the original holds %d entries named .choix or picks; %d before the run", f.label, n, was)
		}
	}
	u.logf("originals: %d folder manifest(s) compared", len(folders))
}

// stopSampling stops the native-call sampler; it is idempotent.
func (u *uat) stopSampling() { u.stopOnce.Do(func() { close(u.stopped) }) }

// setup points the data directory at a temporary one, writes the settings,
// and starts a runtime on the real platform.
func (u *uat) setup(t *testing.T) {
	data := t.TempDir()
	t.Setenv("CHOIX_DATA_DIR", data)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		u.fatalf("temporary directory: %v", err)
	}
	u.parent = parent
	u.red.addRoots(data, parent)
	u.stills = imageio.New()

	// Neither setting needs anything downloaded: the feature prints come
	// from Vision and the rating from the built-in formula.
	cfg, err := config.Load()
	if err != nil {
		u.fatalf("load the settings: %v", err)
	}
	cfg.VisualGrouping = true
	cfg.QualityRating = true
	if err := config.Save(cfg); err != nil {
		u.fatalf("save the settings: %v", err)
	}

	r, err := runtime.New(runtime.Deps{
		Bookmarks: bookmark.New(),
		Volumes:   volumes.New(),
		Workspace: workspace.New(),
		Stills:    u.stills,
		Video:     avmedia.New(),
		Vision:    vision.New(),
		Emit:      u.rec,
		Picker:    u.picker,
		Confirm:   uatConfirm{},
		Home:      parent,
	})
	if err != nil {
		u.fatalf("runtime.New: %v", err)
	}
	u.r = r
	t.Cleanup(r.Shutdown)

	// Sample how many native calls run at once until the test ends.
	u.stopped = make(chan struct{})
	t.Cleanup(u.stopSampling)
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			if n := int64(platform.NativeRunning()); n > u.sampled.Load() {
				u.sampled.Store(n)
			}
			select {
			case <-u.stopped:
				return
			case <-tick.C:
			}
		}
	}()

	r.Start(context.Background())
}

// clone copies f into the clones' directory as name: the whole folder with
// `cp -cR`, or with a limit its first media files with `cp -c`. The archive
// is only read.
func (u *uat) clone(f *uatFolder, name string, limit int) string {
	dst := filepath.Join(u.parent, name)
	if limit <= 0 {
		if out, err := exec.Command("cp", "-cR", f.src, dst).CombinedOutput(); err != nil { //nolint:gosec // test-only clone of the corpus
			u.fatalf("%s: clone: %v: %s", f.label, err, foreign(out))
		}
		return dst
	}
	byDir := map[string][]string{}
	var dirs []string
	for _, rel := range f.media[:min(limit, len(f.media))] {
		d := path.Dir(rel)
		if _, ok := byDir[d]; !ok {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], filepath.Join(f.src, filepath.FromSlash(rel)))
	}
	for _, d := range dirs {
		to := filepath.Join(dst, filepath.FromSlash(d))
		if err := os.MkdirAll(to, 0o750); err != nil {
			u.fatalf("%s: clone: %v", f.label, err)
		}
		args := append([]string{"-c"}, byDir[d]...)
		args = append(args, to+string(filepath.Separator))
		if out, err := exec.Command("cp", args...).CombinedOutput(); err != nil { //nolint:gosec // test-only clone of the corpus
			u.fatalf("%s: clone: %v: %s", f.label, err, foreign(out))
		}
	}
	return dst
}

// add adds root through the folder picker and returns its id.
func (u *uat) add(label, root string) string {
	u.picker.set(root)
	res, err := u.r.Library.AddFolders()
	if err != nil {
		u.fatalf("%s: AddFolders: %v", label, err)
	}
	if len(res) != 1 || res[0].Existing {
		u.fatalf("%s: AddFolders returned %d result(s), existing=%t", label, len(res), len(res) == 1 && res[0].Existing)
	}
	return res[0].ID
}

// summary returns the folder's Library row.
func (u *uat) summary(label, id string) library.FolderSummary {
	list, err := u.r.Library.List()
	if err != nil {
		u.fatalf("%s: Library.List: %v", label, err)
	}
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	u.fatalf("%s: not in the Library", label)
	return library.FolderSummary{}
}

// waitIdle waits until the runtime is idle and every folder in ids is
// ready, failing the test on an error state or at the test's deadline.
func (u *uat) waitIdle(label string, ids ...string) {
	deadline := time.Now().Add(4 * time.Hour)
	if d, ok := u.t.Deadline(); ok {
		deadline = d.Add(-time.Minute)
	}
	for {
		done := u.r.Idle()
		for _, id := range ids {
			s := u.summary(label, id)
			if s.State == "error" {
				u.fatalf("%s: the folder is in error: %s", label, foreign(s.Error))
			}
			done = done && s.State == "ready"
		}
		if done {
			return
		}
		if time.Now().After(deadline) {
			u.fatalf("%s: not idle before the test's deadline", label)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitScanning waits until the folder's job runs and has reported progress.
func (u *uat) waitScanning(label, id string, from time.Time) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if _, ok := u.rec.firstState(id, "scanning", from); ok && u.rec.progressCount(id) > 0 {
			return
		}
		if time.Now().After(deadline) {
			u.fatalf("%s: its job did not start within 2 minutes", label)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// reportRow is one folder's line of the report.
type reportRow struct {
	label    string
	formats  map[string]int
	files    int
	failed   int
	clusters int
	timing   timing
	rss      int64
}

// scanFolder runs and checks one folder, and returns its report line.
func (u *uat) scanFolder(f *uatFolder) reportRow {
	clone := u.clone(f, f.label, u.limit)
	id := u.add(f.label, clone)
	u.waitIdle(f.label, id)
	row := reportRow{label: f.label, rss: peakRSS()}
	if tm, ok := u.rec.timing(id); ok {
		row.timing = tm
	} else {
		u.errorf("%s: no scanning → ready sequence was observed", f.label)
	}

	res := u.checkFiles(f.label, clone)
	row.formats, row.files, row.failed, row.clusters = res.formats, res.files, res.failed, res.clusters

	// The Library row and its preview strip.
	s := u.summary(f.label, id)
	if want := min(uatPreviews, res.previewable); len(s.Previews) != want {
		u.errorf("%s: Library.List shows %d previews; want %d", f.label, len(s.Previews), want)
	}

	u.checkPicks(f.label, id, clone)
	u.logf("%s: %d files, %d failed, %d clusters, job %.1fs", f.label, res.files, res.failed, res.clusters, row.timing.job.Seconds())
	return row
}

// checkResult is what checkFiles found.
type checkResult struct {
	formats     map[string]int // media files of the clone by extension
	files       int            // media files of the clone
	failed      int
	clusters    int
	previewable int // files with a cached thumbnail tier, neither missing nor rejected
}

// fileRow is one files row of state.db.
type fileRow struct {
	id                 int64
	path, kind, status string
	errText, deviceKey string
	captured           sql.NullInt64
	mtime              int64
	width, height      sql.NullInt64
}

// tierRow is one thumbs row of state.db.
type tierRow struct {
	rel           string
	width, height sql.NullInt64
}

// checkFiles checks a scanned clone's state.db against the clone itself.
func (u *uat) checkFiles(label, root string) checkResult {
	st, err := store.Open(filepath.Join(root, ".choix", "state.db"))
	if err != nil {
		u.fatalf("%s: open state.db: %v", label, err)
	}
	defer func() { _ = st.Close() }()
	db := st.DB()
	ctx := context.Background()

	// The media files the scanner indexes, with the export directories the
	// engine skips in this folder (the live settings, this state.db).
	cfg, err := config.Load()
	if err != nil {
		u.fatalf("%s: load the settings: %v", label, err)
	}
	skip, err := engineSkipDirs(st, *cfg)
	if err != nil {
		u.fatalf("%s: the export directories to skip: %v", label, err)
	}
	media, err := mediaFiles(root, skip)
	if err != nil {
		u.fatalf("%s: list the clone's media: %v", label, err)
	}
	res := checkResult{formats: map[string]int{}, files: len(media)}
	for _, rel := range media {
		res.formats[strings.ToLower(strings.TrimPrefix(path.Ext(rel), "."))]++
	}

	rows, err := db.QueryContext(ctx, `SELECT id, path, kind, scan_status, COALESCE(error, ''),
		COALESCE(device_key, ''), captured_at, mtime, width, height
		FROM files WHERE scan_status <> 'missing' ORDER BY path`)
	if err != nil {
		u.fatalf("%s: query files: %v", label, err)
	}
	var files []fileRow
	for rows.Next() {
		var f fileRow
		if err := rows.Scan(&f.id, &f.path, &f.kind, &f.status, &f.errText, &f.deviceKey,
			&f.captured, &f.mtime, &f.width, &f.height); err != nil {
			u.fatalf("%s: scan files: %v", label, err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		u.fatalf("%s: files: %v", label, err)
	}
	_ = rows.Close()

	thumbs := map[int64]map[string]tierRow{}
	trows, err := db.QueryContext(ctx, `SELECT file_id, tier, rel_path, width, height FROM thumbs`)
	if err != nil {
		u.fatalf("%s: query thumbs: %v", label, err)
	}
	for trows.Next() {
		var (
			id      int64
			tier, p string
			w, h    sql.NullInt64
		)
		if err := trows.Scan(&id, &tier, &p, &w, &h); err != nil {
			u.fatalf("%s: scan thumbs: %v", label, err)
		}
		if thumbs[id] == nil {
			thumbs[id] = map[string]tierRow{}
		}
		thumbs[id][tier] = tierRow{rel: p, width: w, height: h}
	}
	_ = trows.Close()

	embedded := map[int64]bool{}
	erows, err := db.QueryContext(ctx, `SELECT file_id FROM ai_signals
		WHERE vision_print IS NOT NULL AND length(vision_print) = ? AND vision_print_id = ?`,
		platform.FeaturePrintLen*4, analyze.PrintID)
	if err != nil {
		u.fatalf("%s: query ai_signals: %v", label, err)
	}
	for erows.Next() {
		var id int64
		if err := erows.Scan(&id); err != nil {
			u.fatalf("%s: scan ai_signals: %v", label, err)
		}
		embedded[id] = true
	}
	_ = erows.Close()

	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clusters`).Scan(&res.clusters); err != nil {
		u.fatalf("%s: count clusters: %v", label, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT f.id) FROM files f JOIN thumbs t ON t.file_id = f.id
		LEFT JOIN picks p ON p.file_id = f.id
		WHERE f.scan_status <> 'missing' AND COALESCE(p.state, '') <> 'rejected'
		AND t.tier IN ('thumb', 'preview')`).Scan(&res.previewable); err != nil {
		u.fatalf("%s: count previewable files: %v", label, err)
	}

	// Every file is analyzed or failed; the counts match the clone.
	if len(files) != res.files {
		u.errorf("%s: state.db holds %d present files; the clone has %d media files", label, len(files), res.files)
	}
	var problems []string
	problem := func(i int, f fileRow, format string, args ...any) {
		problems = append(problems, fileRef(label, f.path, i)+": "+u.say(format, args...))
	}
	photos, videos, nonFailed, withEmbedding := 0, 0, 0, 0
	deviceKeys := map[string]bool{} // the distinct device keys of the non-failed files
	for i, f := range files {
		switch f.kind {
		case "photo":
			photos++
		case "video":
			videos++
		}
		switch f.status {
		case "failed":
			res.failed++
			u.t.Log(fileRef(label, f.path, i) + ": failed: " + u.red.text(f.errText, "string"))
			continue
		case "analyzed":
		default:
			problem(i, f, "status %q; want analyzed or failed", f.status)
			continue
		}
		nonFailed++
		if embedded[f.id] {
			withEmbedding++
		}
		if k := f.deviceKey; k != "" && k != "Unknown" {
			deviceKeys[k] = true
		}
		abs := filepath.Join(root, filepath.FromSlash(f.path))
		ext := strings.ToLower(path.Ext(f.path))
		if ext == ".dng" {
			if f.deviceKey == "Unknown" || f.deviceKey == "" {
				problem(i, f, "DNG with device key Unknown")
			}
			if f.captured.Valid && f.captured.Int64 == f.mtime {
				problem(i, f, "DNG whose capture time is its file time")
			}
		}
		switch f.kind {
		case "photo":
			for _, tier := range []struct {
				name  string
				limit int
			}{{thumb.TierThumb, thumb.WidthThumb}, {thumb.TierPreview, thumb.WidthPreview}} {
				row, ok := thumbs[f.id][tier.name]
				if !ok {
					problem(i, f, "no %s row", tier.name)
					continue
				}
				if msg := u.checkTier(root, abs, ext, f, row, tier.limit); msg != "" {
					problem(i, f, "%s: %s", tier.name, msg)
				}
			}
		case "video":
			dir := thumb.KeyframesDir(root, f.id)
			n := 0
			for k := 1; k <= uatKeyframes+1; k++ {
				if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%d.jpg", k))); err == nil {
					n++
				}
			}
			if n != uatKeyframes {
				problem(i, f, "%d keyframes; want %d", n, uatKeyframes)
			}
		}
	}
	if photos+videos != res.files {
		u.errorf("%s: %d photos + %d videos; the clone has %d media files", label, photos, videos, res.files)
	}
	if res.failed*1000 > res.files*5 {
		u.errorf("%s: %d of %d files failed; at most 0.5%% may", label, res.failed, res.files)
	}
	if res.clusters == 0 {
		u.errorf("%s: no clusters", label)
	}
	if nonFailed > 0 && withEmbedding*100 < nonFailed*99 {
		u.errorf("%s: %d of %d non-failed files have a feature print; want at least 99%%", label, withEmbedding, nonFailed)
	}
	// One camera must be one device. Two bodies of one model (two serials)
	// are two devices by design; see splitCameras.
	split, bodies := splitCameras(slices.Collect(maps.Keys(deviceKeys)))
	if bodies > 0 {
		u.logf("%s: %d camera model(s) with more than one body (distinct serials): separate devices by design", label, bodies)
	}
	if split > 0 {
		u.errorf("%s: %d camera model(s) split into more than one device for one camera (a serial-less key beside exactly one serial, or one serial under two spellings)", label, split)
	}
	if len(problems) > 0 {
		for _, p := range problems[:min(len(problems), uatExamples)] {
			u.t.Log(p)
		}
		u.errorf("%s: %d file problem(s) (first %d above)", label, len(problems), min(len(problems), uatExamples))
	}
	return res
}

// checkTier checks one cached tier of a photo against the sizing rule of
// its source kind, and returns what is wrong ("" when nothing is); the text
// holds only numbers. A RAW still (RAF, DNG) is made from its embedded JPEG:
// its width is expectedRawWidth of that JPEG (embeddedPreview). Any other
// still, or a RAW without an embedded JPEG (the engine's last-resort
// decode): long side limit, or the source's when smaller.
func (u *uat) checkTier(root, abs, ext string, f fileRow, row tierRow, limit int) string {
	cache := filepath.Join(root, filepath.FromSlash(row.rel))
	fh, err := os.Open(cache) //nolint:gosec // a cache file of the clone
	if err != nil {
		return "cache file missing"
	}
	cfg, _, err := image.DecodeConfig(fh)
	_ = fh.Close()
	if err != nil {
		return "cache file is not an image"
	}
	w, h := cfg.Width, cfg.Height
	if row.width.Valid && int(row.width.Int64) != w {
		return fmt.Sprintf("row width %d, file width %d", row.width.Int64, w)
	}
	if row.height.Valid && int(row.height.Int64) != h {
		return fmt.Sprintf("row height %d, file height %d", row.height.Int64, h)
	}

	if ext == ".raf" || ext == ".dng" {
		ew, eh, orient, err := u.embeddedPreview(abs)
		switch {
		case err == nil:
			if want := expectedRawWidth(ew, eh, orient, limit); w != want {
				return fmt.Sprintf("width %d; want %d (embedded JPEG %d by %d, orientation %d)", w, want, ew, eh, orient)
			}
			return ""
		case !errors.Is(err, rawpreview.ErrNoPreview):
			return "the embedded JPEG cannot be read: " + u.red.errText(err)
		}
	}

	long := max(w, h)
	srcLong := 0
	if f.width.Valid && f.height.Valid {
		srcLong = int(max(f.width.Int64, f.height.Int64))
	}
	if srcLong == 0 && (ext == ".jpg" || ext == ".jpeg" || ext == ".png") {
		if src, err := os.Open(abs); err == nil { //nolint:gosec // an original of the clone, read only
			if c, _, err := image.DecodeConfig(src); err == nil {
				srcLong = max(c.Width, c.Height)
			}
			_ = src.Close()
		}
	}
	if srcLong == 0 {
		if long > limit {
			return fmt.Sprintf("long side %d above %d", long, limit)
		}
		return ""
	}
	if want := min(limit, srcLong); long != want {
		return fmt.Sprintf("long side %d; want %d (source %d)", long, want, srcLong)
	}
	return ""
}

// embeddedPreview returns the stored size and the orientation of the
// embedded JPEG the engine makes a RAW's tiers from — the widest candidate
// that decodes (rawpreview.Embedded with rawpreview.Usable, through the same
// ImageIO reader) — read through ImageIO's properties. The orientation is
// the candidate's own override (DNG's IFD0) when it is 1–8, else the JPEG's
// own tag, else 1. The file is only read.
func (u *uat) embeddedPreview(abs string) (w, h, orientation int, err error) {
	ctx := context.Background()
	usable := rawpreview.Usable(ctx, u.stills)
	_, err = rawpreview.Embedded(abs, func(p rawpreview.Preview) error {
		if err := usable(p); err != nil {
			return err
		}
		props, err := u.stills.Properties(ctx, platform.ImageInput{Data: p.JPEG})
		if err != nil {
			return err
		}
		w, h = propInt(props, "PixelWidth"), propInt(props, "PixelHeight")
		if w <= 0 || h <= 0 {
			w, h = p.Width, p.Height
		}
		orientation = p.Orientation
		if orientation < 1 || orientation > 8 {
			orientation = propInt(props, "Orientation")
		}
		if orientation < 1 || orientation > 8 {
			orientation = 1
		}
		return nil
	})
	return w, h, orientation, err
}

// propInt reads a whole number from a JSON-shaped property dictionary.
func propInt(props map[string]any, key string) int {
	switch v := props[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

// checkPicks picks the first, middle and last file of the folder's grid,
// checks each copy is byte-identical to its original, then unpicks them and
// checks the copies are gone.
func (u *uat) checkPicks(label, id, root string) {
	view, err := u.r.Catalog.Open(id)
	if err != nil {
		u.errorf("%s: Catalog.Open: %v", label, err)
		return
	}
	defer func() {
		if err := u.r.Catalog.Close(id); err != nil {
			u.errorf("%s: Catalog.Close: %v", label, err)
		}
	}()
	// Opening queues a rescan at the front; picks are made once it is done.
	u.waitIdle(label, id)
	if view.PicksBlocked != "" {
		u.errorf("%s: picks are blocked (%s)", label, view.PicksBlocked)
		return
	}
	type member struct {
		fileID int64
		path   string
	}
	var members []member
	for _, c := range view.Clusters {
		for _, m := range c.Members {
			if !m.Picked && !m.Rejected {
				members = append(members, member{m.FileID, m.Path})
			}
		}
	}
	if len(members) == 0 {
		u.errorf("%s: no member to pick", label)
		return
	}
	var chosen []member
	for _, i := range []int{0, len(members) / 2, len(members) - 1} {
		if !slices.Contains(chosen, members[i]) {
			chosen = append(chosen, members[i])
		}
	}
	for n, m := range chosen {
		res, err := u.r.Catalog.SetPick(id, m.fileID, "pick")
		if err != nil {
			u.errorf("%s: pick %d: %s", label, n+1, u.red.errText(err))
			continue
		}
		copyPath := filepath.Join(root, filepath.FromSlash(res.ExportedPath))
		if res.State != "picked" || !strings.HasPrefix(res.ExportedPath, view.PicksDir+"/") {
			u.errorf("%s: pick %d: state %q, the copy is not under the picks directory", label, n+1, res.State)
		} else if same, err := sameBytes(filepath.Join(root, filepath.FromSlash(m.path)), copyPath); err != nil || !same {
			u.errorf("%s: pick %d: the copy is not byte-identical (%s)", label, n+1, u.red.errText(err))
		}
		res, err = u.r.Catalog.SetPick(id, m.fileID, "unpick")
		if err != nil {
			u.errorf("%s: unpick %d: %s", label, n+1, u.red.errText(err))
			continue
		}
		if _, err := os.Lstat(copyPath); !errors.Is(err, fs.ErrNotExist) {
			u.errorf("%s: unpick %d: the copy is still there (state %q)", label, n+1, res.State)
		}
	}
}

// sameBytes compares two files by SHA-256.
func sameBytes(a, b string) (bool, error) {
	ha, err := fileHash(a)
	if err != nil {
		return false, err
	}
	hb, err := fileHash(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ha, hb), nil
}

func fileHash(p string) ([]byte, error) {
	f, err := os.Open(p) //nolint:gosec // a file of the clone, read only
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// preemptAndQuit: fresh clones of the largest folders fill every job slot
// (session.DefaultSlots folders scan at once); a fresh clone of the next is
// added, waits, and is opened: its job must start within uatPreemptStart by
// preempting the job started last, which is re-queued while the others keep
// scanning. Once all are done (and checked), Shutdown while two scans run
// must return within QuitDeadline + 1 s. These clones hold at most
// uatShortLimit files: they exercise the queue, not the throughput, and a
// full run would otherwise scan the largest folders twice.
func (u *uat) preemptAndQuit(folders []*uatFolder) {
	quick := uatShortLimit
	if u.limit > 0 {
		quick = min(u.limit, uatShortLimit)
	}
	pick := func(i int) *uatFolder { return folders[min(i, len(folders)-1)] }
	slots := session.DefaultSlots

	// Fill every slot, one job after the other, so they start in order.
	type busyJob struct{ id, label, root string }
	busy := make([]busyJob, 0, slots)
	for i := range slots {
		f := pick(i)
		b := busyJob{label: fmt.Sprintf("%s (busy %d)", f.label, i+1)}
		b.root = u.clone(f, fmt.Sprintf("%s-busy-%d", f.label, i+1), quick)
		from := time.Now()
		b.id = u.add(b.label, b.root)
		u.waitScanning(b.label, b.id, from)
		busy = append(busy, b)
	}
	last := busy[len(busy)-1]

	waiting := pick(slots)
	wLabel := waiting.label + " (opened)"
	wRoot := u.clone(waiting, waiting.label+"-opened", quick)
	wID := u.add(wLabel, wRoot)
	if s := u.summary(wLabel, wID); s.State != "queued" {
		u.errorf("%s was not waiting while %d folders scanned (state %s)", wLabel, slots, s.State)
	}
	for _, b := range busy {
		if s := u.summary(b.label, b.id); s.State != "scanning" {
			u.errorf("%s finished before the preemption could be exercised (state %s)", b.label, s.State)
		}
	}
	opened := time.Now()
	if _, err := u.r.Catalog.Open(wID); err != nil {
		u.fatalf("%s: Catalog.Open while %d folders scan: %v", wLabel, slots, err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		at, ok := u.rec.firstState(wID, "scanning", opened)
		if ok {
			took := at.Sub(opened)
			u.logf("preemption: %s's job started %.3fs after it was opened while %d folders scanned", wLabel, took.Seconds(), slots)
			if took > uatPreemptStart {
				u.errorf("preemption: %s's job started %.2fs after it was opened; want within %v", wLabel, took.Seconds(), uatPreemptStart)
			}
			break
		}
		if time.Now().After(deadline) {
			u.errorf("preemption: %s's job did not start within a minute of opening it", wLabel)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !slices.Contains(u.rec.statesSince(last.id, opened), "queued") {
		u.errorf("preemption: %s, started last, was not re-queued when %s was opened", last.label, wLabel)
	}
	for _, b := range busy[:len(busy)-1] {
		if slices.Contains(u.rec.statesSince(b.id, opened), "queued") {
			u.errorf("preemption: %s was re-queued too; want only the job started last", b.label)
		}
	}
	if err := u.r.Catalog.Close(wID); err != nil {
		u.errorf("%s: Catalog.Close: %v", wLabel, err)
	}
	ids := []string{wID}
	for _, b := range busy {
		ids = append(ids, b.id)
	}
	u.waitIdle("preemption", ids...)
	// The preempted scan resumed and finished, the others and the opened one
	// are whole.
	for _, b := range busy {
		u.checkFiles(b.label, b.root)
	}
	u.checkFiles(wLabel, wRoot)

	// Quit while two scans run.
	q1, q2 := pick(slots+1), pick(slots+2)
	r1 := u.clone(q1, q1.label+"-quit-1", quick)
	r2 := u.clone(q2, q2.label+"-quit-2", quick)
	from := time.Now()
	id1 := u.add(q1.label+" (quit 1)", r1)
	id2 := u.add(q2.label+" (quit 2)", r2)
	u.waitScanning(q1.label+" (quit 1)", id1, from)
	u.waitScanning(q2.label+" (quit 2)", id2, from)
	quit := time.Now()
	u.r.Shutdown()
	took := time.Since(quit)
	u.logf("shutdown during the scans of %s and %s: %.3fs", q1.label, q2.label, took.Seconds())
	if limit := runtime.QuitDeadline + time.Second; took > limit {
		u.errorf("Shutdown during two scans took %.2fs; want within %v", took.Seconds(), limit)
	}
}

// peakRSS returns the process's peak resident set size in bytes.
func peakRSS() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return ru.Maxrss // bytes on darwin
}

// renderReport is the Markdown table: per folder its files by format,
// failures, clusters, wall time per stage, files per second and peak RSS.
func renderReport(rows []reportRow) string {
	var b strings.Builder
	b.WriteString("| Folder | Files by format | Failed | Clusters |")
	for _, s := range uatStages {
		fmt.Fprintf(&b, " %s |", s)
	}
	b.WriteString(" after pipeline | Job | Files/s | Peak RSS |\n|---|---|--:|--:|")
	for range uatStages {
		b.WriteString("--:|")
	}
	b.WriteString("--:|--:|--:|--:|\n")
	for _, r := range rows {
		exts := make([]string, 0, len(r.formats))
		for e := range r.formats {
			exts = append(exts, e)
		}
		sort.Strings(exts)
		parts := make([]string, 0, len(exts))
		for _, e := range exts {
			parts = append(parts, fmt.Sprintf("%s %d", e, r.formats[e]))
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d |", r.label, strings.Join(parts, ", "), r.failed, r.clusters)
		for _, s := range uatStages {
			if d, ok := r.timing.stages[s]; ok {
				fmt.Fprintf(&b, " %.1fs |", d.Seconds())
			} else {
				b.WriteString(" – |")
			}
		}
		rate := 0.0
		if r.timing.job > 0 {
			rate = float64(r.files) / r.timing.job.Seconds()
		}
		fmt.Fprintf(&b, " %.1fs | %.1fs | %.1f | %d MiB |\n", r.timing.after.Seconds(), r.timing.job.Seconds(), rate, r.rss>>20)
	}
	return b.String()
}

package runtime

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/system"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// testTimeout bounds every wait for a condition. It is generous: the tests
// wait for conditions, never for time to pass, so a loaded machine only
// makes them slower.
const testTimeout = 60 * time.Second

// waitFor polls cond until it holds, failing the test after testTimeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// receive waits for a value on ch, failing the test after testTimeout.
func receive[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

// under reports whether path is p or lies inside it.
func under(path, p string) bool {
	return path == p || strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/")
}

// goid returns the id of the calling goroutine (tests only).
func goid() string {
	var buf [64]byte
	n := goruntime.Stack(buf[:], false)
	f := strings.Fields(string(buf[:n])) // "goroutine 123 [running]: ..."
	if len(f) < 2 {
		return ""
	}
	return f[1]
}

// orderLog records events from several goroutines in the order they
// happen.
type orderLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *orderLog) add(s string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, s)
}

func (l *orderLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.entries)
}

// recEmitter is the frontend: it records every event and, like cmd/choix's
// emitter, has a Close. An Emit after Close is counted as a violation.
type recEmitter struct {
	log    *orderLog
	closed chan struct{}

	mu         sync.Mutex
	events     []recEvent
	isClosed   bool
	afterClose int
}

type recEvent struct {
	name string
	data any
}

func newRecEmitter(log *orderLog) *recEmitter {
	return &recEmitter{log: log, closed: make(chan struct{})}
}

func (e *recEmitter) Emit(name string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.isClosed {
		e.afterClose++
		return
	}
	e.events = append(e.events, recEvent{name, data})
}

func (e *recEmitter) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.isClosed {
		return
	}
	e.isClosed = true
	e.log.add(stepEmitterClosedFake)
	close(e.closed)
}

// stepEmitterClosedFake is what recEmitter writes to the order log on Close.
const stepEmitterClosedFake = "fake emitter closed"

// emitsAfterClose returns how many Emit calls arrived after Close.
func (e *recEmitter) emitsAfterClose() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.afterClose
}

// all returns the payloads emitted under name, oldest first.
func (e *recEmitter) all(name string) []any {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []any
	for _, ev := range e.events {
		if ev.name == name {
			out = append(out, ev.data)
		}
	}
	return out
}

// count returns how many events were emitted under name.
func (e *recEmitter) count(name string) int { return len(e.all(name)) }

// scanStates returns the scan:state states emitted for the folder, in order.
func (e *recEmitter) scanStates(id string) []string {
	var out []string
	for _, d := range e.all(events.ScanState) {
		if ev, ok := d.(events.ScanStateEvent); ok && ev.FolderID == id {
			out = append(out, ev.State)
		}
	}
	return out
}

// scanning returns the folder ids of scan:state "scanning", in order.
func (e *recEmitter) scanning() []string {
	var out []string
	for _, d := range e.all(events.ScanState) {
		if ev, ok := d.(events.ScanStateEvent); ok && ev.State == "scanning" {
			out = append(out, ev.FolderID)
		}
	}
	return out
}

// countState counts scan:state events of the folder with state.
func (e *recEmitter) countState(id, state string) int {
	n := 0
	for _, s := range e.scanStates(id) {
		if s == state {
			n++
		}
	}
	return n
}

// libraryChanged reports whether library:changed with reason was emitted
// for the folder.
func (e *recEmitter) libraryChanged(id, reason string) bool {
	for _, d := range e.all(events.LibraryChanged) {
		if ev, ok := d.(events.LibraryChangedEvent); ok && ev.FolderID == id && ev.Reason == reason {
			return true
		}
	}
	return false
}

// discoverCount counts scan:progress events of the folder in the discover
// stage, which only a full job (a walk) reports.
func (e *recEmitter) discoverCount(id string) int {
	n := 0
	for _, d := range e.all(events.ScanProgress) {
		if ev, ok := d.(events.ScanProgressEvent); ok && ev.FolderID == id && ev.Progress.Stage == "discover" {
			n++
		}
	}
	return n
}

// shot is a synthetic photo as the fake ImageIO reports it.
type shot struct {
	w, h int
	at   time.Time
}

// testDay is the synthetic capture day of every photo.
var testDay = time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)

// fakeStills is a platform.Stills that answers from the shots it was told
// about and makes real JPEGs. Like ImageIO it fails for a path that does not
// exist. Every call is detached (platform.Detach), so it takes a native slot
// and is abandoned when its context ends. Calls for paths under the held
// directory wait, inside their detached function, until it is released.
type fakeStills struct {
	log *orderLog

	held chan string // receives the path of each call that starts waiting

	mu      sync.Mutex
	shots   map[string]shot
	calls   int
	holdDir string
	gate    chan struct{}
}

var _ platform.Stills = (*fakeStills)(nil)

func newFakeStills(log *orderLog) *fakeStills {
	return &fakeStills{log: log, held: make(chan string, 1024), shots: map[string]shot{}}
}

func (s *fakeStills) add(path string, sh shot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shots[path] = sh
}

// holdUnder holds every later call for a path at or under dir until the
// returned release (idempotent) is called.
func (s *fakeStills) holdUnder(dir string) (release func()) {
	gate := make(chan struct{})
	s.mu.Lock()
	s.holdDir, s.gate = dir, gate
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.holdDir, s.gate = "", nil
			s.mu.Unlock()
			close(gate)
		})
	}
}

func (s *fakeStills) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// begin counts a call and returns the gate it must wait on, if any.
func (s *fakeStills) begin(path string) chan struct{} {
	s.log.add("stills")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if path != "" && s.holdDir != "" && under(path, s.holdDir) {
		return s.gate
	}
	return nil
}

func (s *fakeStills) wait(path string, gate chan struct{}) {
	if gate == nil {
		return
	}
	select {
	case s.held <- path:
	default:
	}
	<-gate
}

func (s *fakeStills) shotOf(path string) shot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sh, ok := s.shots[path]; ok {
		return sh
	}
	return shot{w: 64, h: 48, at: testDay}
}

func (s *fakeStills) Properties(ctx context.Context, in platform.ImageInput) (map[string]any, error) {
	gate := s.begin(in.Path)
	return platform.Detach(ctx, func() (map[string]any, error) {
		s.wait(in.Path, gate)
		if in.Path == "" {
			return map[string]any{}, nil
		}
		if _, err := os.Stat(in.Path); err != nil {
			return nil, fmt.Errorf("fake imageio: %w", err)
		}
		sh := s.shotOf(in.Path)
		return map[string]any{
			"PixelWidth":  sh.w,
			"PixelHeight": sh.h,
			"{Exif}":      map[string]any{"DateTimeOriginal": sh.at.Format("2006:01:02 15:04:05")},
			"{TIFF}":      map[string]any{"Make": "TESTCAM", "Model": "T1"},
		}, nil
	})
}

func (s *fakeStills) Thumbnail(ctx context.Context, in platform.ImageInput, limit int, fit platform.Fit) (platform.Image, error) {
	gate := s.begin(in.Path)
	return platform.Detach(ctx, func() (platform.Image, error) {
		s.wait(in.Path, gate)
		var w, h int
		if in.Data != nil {
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(in.Data))
			if err != nil {
				return platform.Image{}, fmt.Errorf("fake imageio: decode data: %w", err)
			}
			w, h = cfg.Width, cfg.Height
		} else {
			if _, err := os.Stat(in.Path); err != nil {
				return platform.Image{}, fmt.Errorf("fake imageio: %w", err)
			}
			sh := s.shotOf(in.Path)
			w, h = sh.w, sh.h
		}
		w, h = fitSize(w, h, limit, fit)
		data, err := encodeJPEG(w, h)
		if err != nil {
			return platform.Image{}, err
		}
		return platform.Image{JPEG: data, Width: w, Height: h}, nil
	})
}

// fitSize scales w×h so the bounded side is at most limit, never upscaling.
func fitSize(w, h, limit int, fit platform.Fit) (int, int) {
	bound := w
	if fit == platform.FitLongSide && h > w {
		bound = h
	}
	if bound <= limit {
		return w, h
	}
	scale := func(n int) int { return max(1, (n*limit+bound/2)/bound) }
	return scale(w), scale(h)
}

func encodeJPEG(w, h int) ([]byte, error) {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i % 251)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60}); err != nil {
		return nil, fmt.Errorf("fake imageio: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// fakeDownloader is a models.Downloader: a model is installed when the test
// says so or when its download completes; a held model's download waits
// until released or cancelled.
type fakeDownloader struct {
	mu      sync.Mutex
	has     map[local.ModelKind]bool
	hold    map[local.ModelKind]chan struct{}
	started map[local.ModelKind]int
}

func newFakeDownloader(installed ...local.ModelKind) *fakeDownloader {
	d := &fakeDownloader{has: map[local.ModelKind]bool{}, hold: map[local.ModelKind]chan struct{}{}, started: map[local.ModelKind]int{}}
	for _, k := range installed {
		d.has[k] = true
	}
	return d
}

func (d *fakeDownloader) Has(kind local.ModelKind) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.has[kind]
}

func (d *fakeDownloader) holdKind(kind local.ModelKind) (release func()) {
	ch := make(chan struct{})
	d.mu.Lock()
	d.hold[kind] = ch
	d.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func (d *fakeDownloader) downloads(kind local.ModelKind) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.started[kind]
}

func (d *fakeDownloader) Download(ctx context.Context, kind local.ModelKind, onProgress func(received, total int64)) error {
	d.mu.Lock()
	d.started[kind]++
	hold := d.hold[kind]
	d.mu.Unlock()
	if onProgress != nil {
		onProgress(1, 10)
	}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	d.mu.Lock()
	d.has[kind] = true
	d.mu.Unlock()
	return nil
}

// fakeVision makes feature prints: the same unit vector for every image,
// as the analyze stage's input check passed. Each call is logged as "print"
// when it starts; a test can hold calls inside the detached function, as a
// slow native call would be.
type fakeVision struct {
	log     *orderLog
	calls   atomic.Int64
	entered chan struct{}

	mu   sync.Mutex
	hold chan struct{}
}

var _ platform.Vision = (*fakeVision)(nil)

func (v *fakeVision) holdCalls() (release func()) {
	ch := make(chan struct{})
	v.mu.Lock()
	v.hold = ch
	v.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func (v *fakeVision) FeaturePrint(ctx context.Context, in platform.ImageInput) ([]float32, error) {
	v.calls.Add(1)
	v.log.add("print")
	v.mu.Lock()
	hold := v.hold
	v.mu.Unlock()
	return platform.Detach(ctx, func() ([]float32, error) {
		if hold != nil {
			select {
			case v.entered <- struct{}{}:
			default:
			}
			<-hold
		}
		if _, err := os.Stat(in.Path); in.Path != "" && err != nil {
			return nil, fmt.Errorf("fake vision: %w", err)
		}
		p := make([]float32, platform.FeaturePrintLen)
		p[0] = 1
		return p, nil
	})
}

// scoreGate observes the photos the rating passes score (the runtime's
// scoreHook); a test can hold them.
type scoreGate struct {
	calls   atomic.Int64
	entered chan struct{}

	mu   sync.Mutex
	hold chan struct{}
}

func (f *scoreGate) holdCalls() (release func()) {
	ch := make(chan struct{})
	f.mu.Lock()
	f.hold = ch
	f.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

// score runs before each photo is scored.
func (f *scoreGate) score() {
	f.calls.Add(1)
	f.mu.Lock()
	hold := f.hold
	f.mu.Unlock()
	if hold != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
		<-hold
	}
}

// fakePicker is the folder picker: it returns what the test set.
type fakePicker struct {
	mu    sync.Mutex
	paths []string
}

func (p *fakePicker) set(paths ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = paths
}

func (p *fakePicker) PickFolders() ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.paths), nil
}

// fakeConfirmer is the removal dialog. With answers set it reports each
// question on asked and blocks until the test answers; otherwise it says
// yes.
type fakeConfirmer struct {
	asked   chan string
	answers chan bool
}

func (c *fakeConfirmer) ConfirmRemove(name string) (bool, error) {
	if c.answers == nil {
		return true, nil
	}
	c.asked <- name
	return <-c.answers, nil
}

// plugBookmarks wraps the bookmark fake so that a bookmark whose target is
// at or under an unplugged path fails with platform.ErrGone, as a real one
// does while its volume is not mounted. onResolve, when set, runs once at
// the next Resolve.
type plugBookmarks struct {
	*platformtest.Bookmarks

	mu        sync.Mutex
	unplugged []string
	onResolve func()
}

func (p *plugBookmarks) unplug(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unplugged = append(p.unplugged, path)
}

func (p *plugBookmarks) plug(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unplugged = slices.DeleteFunc(p.unplugged, func(u string) bool { return u == path })
}

func (p *plugBookmarks) armResolve(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onResolve = fn
}

func (p *plugBookmarks) gone(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, u := range p.unplugged {
		if under(path, u) {
			return fmt.Errorf("plug: %s is not mounted: %w", path, platform.ErrGone)
		}
	}
	return nil
}

func (p *plugBookmarks) Resolve(data []byte) (string, bool, error) {
	p.mu.Lock()
	fn := p.onResolve
	p.onResolve = nil
	p.mu.Unlock()
	if fn != nil {
		fn()
	}
	path, stale, err := p.Bookmarks.Resolve(data)
	if err != nil {
		return "", false, err
	}
	if err := p.gone(path); err != nil {
		return "", false, err
	}
	return path, stale, nil
}

func (p *plugBookmarks) StartAccess(data []byte) (string, func(), error) {
	path, _, err := p.Bookmarks.Resolve(data)
	if err == nil {
		err = p.gone(path)
	}
	if err != nil {
		return "", nil, err
	}
	return p.Bookmarks.StartAccess(data)
}

func (p *plugBookmarks) Refresh(data []byte) ([]byte, error) {
	path, _, err := p.Bookmarks.Resolve(data)
	if err == nil {
		err = p.gone(path)
	}
	if err != nil {
		return nil, err
	}
	return p.Bookmarks.Refresh(data)
}

// mainThreadMenu is a native menu whose SetContext waits, as Wails' item
// updates do (dispatch_sync to the main queue), for the main thread: it
// signals entered and blocks until free is closed. On quit the main thread
// is the one running Shutdown.
type mainThreadMenu struct {
	entered chan struct{}
	free    chan struct{}
}

func (m *mainThreadMenu) SetContext(system.MenuContext) {
	m.entered <- struct{}{}
	<-m.free
}

func (m *mainThreadMenu) PopupFolderMenu(string, []string, int, int) {}

func (m *mainThreadMenu) SingleKeysNative() bool { return false }

// harness is one test's fakes and data directory. Runtimes made from it
// share the fakes (a bookmark stored in library.db must resolve again after
// a restart).
type harness struct {
	data, parent string
	log          *orderLog
	bm           *plugBookmarks
	vols         *platformtest.Volumes
	stills       *fakeStills
	video        *platformtest.Video
	dl           *fakeDownloader
	vision       *fakeVision
	scores       *scoreGate
	em           *recEmitter
	picker       *fakePicker
	confirm      *fakeConfirmer
	ws           *platformtest.Workspace
	menu         system.MenuState // nil: no native menu
}

// newHarness points CHOIX_DATA_DIR at a new directory, writes a config with
// the quality rating off (tests that need it switch it on) and installs
// both models in the fake downloader.
func newHarness(t *testing.T) *harness {
	t.Helper()
	data := t.TempDir()
	t.Setenv("CHOIX_DATA_DIR", data)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := &orderLog{}
	vols := &platformtest.Volumes{}
	vols.Add(parent, platform.VolumeInfo{UUID: "UUID-TEST", Name: "Test HD"})
	dl := newFakeDownloader(local.ModelCLIP, local.ModelQuality)
	h := &harness{
		data:    data,
		parent:  parent,
		log:     log,
		bm:      &plugBookmarks{Bookmarks: &platformtest.Bookmarks{}},
		vols:    vols,
		stills:  newFakeStills(log),
		video:   &platformtest.Video{},
		dl:      dl,
		vision:  &fakeVision{log: log, entered: make(chan struct{}, 1)},
		scores:  &scoreGate{entered: make(chan struct{}, 1)},
		em:      newRecEmitter(log),
		picker:  &fakePicker{},
		confirm: &fakeConfirmer{},
		ws:      &platformtest.Workspace{},
	}
	h.setConfig(t, func(c *config.Config) { c.QualityRating = false })
	return h
}

// setConfig edits config.toml before a runtime is made.
func (h *harness) setConfig(t *testing.T, edit func(*config.Config)) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	edit(cfg)
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) deps() Deps {
	return Deps{
		Bookmarks:  h.bm,
		Volumes:    h.vols,
		Workspace:  h.ws,
		Stills:     h.stills,
		Video:      h.video,
		Vision:     h.vision,
		Downloader: h.dl,
		Emit:       h.em,
		Picker:     h.picker,
		Confirm:    h.confirm,
		Home:       h.parent,
		MenuState:  h.menu,
	}
}

// newRuntime makes a runtime that never ticks presence on its own, shut down
// at the end of the test.
func (h *harness) newRuntime(t *testing.T) *Runtime {
	t.Helper()
	r, err := New(h.deps())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.presenceEvery = time.Hour
	r.scoreHook = h.scores.score
	t.Cleanup(r.Shutdown)
	return r
}

func (h *harness) start(t *testing.T) *Runtime {
	t.Helper()
	r := h.newRuntime(t)
	r.Start(context.Background())
	return r
}

// folder makes the directory rel under the harness's parent with n photos
// of w×h taken a second apart, and returns its absolute path.
func (h *harness) folder(t *testing.T, rel string, n, w, ht int) string {
	t.Helper()
	root := filepath.Join(h.parent, filepath.FromSlash(rel))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		data, err := encodeJPEG(w, ht)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(root, fmt.Sprintf("IMG_%04d.jpg", i+1))
		if err := os.WriteFile(p, data, 0o644); err != nil { //nolint:gosec // test fixture
			t.Fatal(err)
		}
		h.stills.add(p, shot{w: w, h: ht, at: testDay.Add(time.Duration(i) * time.Second)})
	}
	return root
}

// add adds root through the folder picker and returns its id.
func (h *harness) add(t *testing.T, r *Runtime, root string) string {
	t.Helper()
	h.picker.set(root)
	res, err := r.Library.AddFolders()
	if err != nil {
		t.Fatalf("AddFolders(%s): %v", root, err)
	}
	if len(res) != 1 {
		t.Fatalf("AddFolders(%s) = %+v; want one result", root, res)
	}
	return res[0].ID
}

// summary returns the folder's Library row.
func summary(t *testing.T, r *Runtime, id string) library.FolderSummary {
	t.Helper()
	list, err := r.Library.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("folder %s is not in the Library", id)
	return library.FolderSummary{}
}

// waitReady waits until the runtime is idle and the folder is ready.
func waitReady(t *testing.T, r *Runtime, id string) library.FolderSummary {
	t.Helper()
	var s library.FolderSummary
	waitFor(t, "folder "+id+" ready and the runtime idle", func() bool {
		s = summary(t, r, id)
		return r.Idle() && s.State == "ready"
	})
	return s
}

// isUnavailable reports whether err is the binding error of a runtime that
// is shutting down.
func isUnavailable(err error) bool {
	e := apperr.From(err)
	return e != nil && e.Code == apperr.CodeUnavailable
}

package library

import (
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
)

// serviceWait bounds every wait in the service tests; they synchronise on
// channels, so a slow machine only makes them slower.
const serviceWait = 10 * time.Second

// fakePicker returns paths, or err. When block is set, PickFolders signals
// entered and waits for release first, like a modal dialog.
type fakePicker struct {
	paths   []string
	err     error
	block   bool
	entered chan struct{}
	release chan struct{}
	calls   int
}

func (p *fakePicker) PickFolders() ([]string, error) {
	p.calls++
	if p.block {
		p.entered <- struct{}{}
		<-p.release
	}
	return p.paths, p.err
}

// fakeConfirmer answers ok, or err, recording the names it was asked about.
// When block is set it waits like fakePicker.
type fakeConfirmer struct {
	ok      bool
	err     error
	block   bool
	entered chan struct{}
	release chan struct{}
	names   []string
}

func (c *fakeConfirmer) ConfirmRemove(name string) (bool, error) {
	c.names = append(c.names, name)
	if c.block {
		c.entered <- struct{}{}
		<-c.release
	}
	return c.ok, c.err
}

// fakeJobs is a JobStates over a fixed map.
type fakeJobs map[string]string

func (j fakeJobs) State(id string) (string, *events.Progress) { return j[id], nil }

// enqueued is one call of ServiceDeps.Enqueue.
type enqueued struct {
	id   string
	full bool
}

// serviceHarness is a Service over a harness Library.
type serviceHarness struct {
	*harness
	svc     *Service
	gate    *opgate.Gate
	picker  *fakePicker
	confirm *fakeConfirmer
	jobs    fakeJobs

	mu       sync.Mutex
	enqueues []enqueued
}

func newServiceHarness(t *testing.T) *serviceHarness {
	t.Helper()
	sh := &serviceHarness{
		harness: newHarness(t),
		gate:    &opgate.Gate{},
		picker:  &fakePicker{},
		confirm: &fakeConfirmer{},
		jobs:    fakeJobs{},
	}
	sh.svc = NewService(ServiceDeps{
		Library: sh.lib,
		Jobs:    sh.jobs,
		Enqueue: func(id string, full bool) error {
			sh.mu.Lock()
			defer sh.mu.Unlock()
			sh.enqueues = append(sh.enqueues, enqueued{id, full})
			return nil
		},
		Picker:     sh.picker,
		Confirm:    sh.confirm,
		Home:       sh.root,
		PreviewURL: testPreviewURL,
		Gate:       sh.gate,
	})
	return sh
}

func (sh *serviceHarness) enqueueCalls() []enqueued {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return slices.Clone(sh.enqueues)
}

// wantCode fails unless err is an *apperr.Error with code.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %#v, want an *apperr.Error with code %q", err, code)
	}
}

func TestService_ExportedMethods(t *testing.T) {
	typ := reflect.TypeFor[*Service]()
	var got []string
	for m := range typ.Methods() {
		got = append(got, m.Name)
	}
	want := []string{"AddFolders", "CheckNow", "List", "Remove", "Rescan"}
	if !slices.Equal(got, want) {
		t.Errorf("exported methods of *Service = %v, want exactly %v (each is a frontend binding)", got, want)
	}
}

func TestList_Order(t *testing.T) {
	sh := newServiceHarness(t)
	day := func(y int, m time.Month, d int) sql.NullInt64 {
		return sql.NullInt64{Int64: time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Unix(), Valid: true}
	}
	// Inserted in an order unrelated to the expected one; added_at grows
	// with i.
	rows := []struct {
		id    string
		first sql.NullInt64
	}{
		{"undated-old", sql.NullInt64{}},
		{"jan", day(2026, 1, 20)},
		{"mar-early", day(2026, 3, 2)},
		{"undated-new", sql.NullInt64{}},
		{"mar-late", day(2026, 3, 28)},
		{"dec", day(2025, 12, 31)},
	}
	for i, r := range rows {
		f := testFolder(r.id, "/Volumes/SD/"+r.id, int64(1000+i))
		mustInsert(t, sh.db, f)
		if err := sh.db.SetStats(r.id, Stats{FirstCaptured: r.first, LastCaptured: r.first}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := sh.svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	want := []string{"mar-late", "mar-early", "jan", "dec", "undated-new", "undated-old"}
	if !slices.Equal(ids, want) {
		t.Errorf("List order = %v, want %v", ids, want)
	}
	// Every row carries its library.db added_at (the "Recently added" order).
	for _, s := range got {
		i := slices.IndexFunc(rows, func(r struct {
			id    string
			first sql.NullInt64
		}) bool {
			return r.id == s.ID
		})
		if s.AddedAt != int64(1000+i) {
			t.Errorf("%s: AddedAt = %d, want %d", s.ID, s.AddedAt, 1000+i)
		}
	}
}

func TestList_SummarisesWithJobStates(t *testing.T) {
	sh := newServiceHarness(t)
	trip := sh.mkdir(t, "Photos/Trip")
	id := sh.mustAdd(t, trip).ID
	sh.jobs[id] = "scanning"

	got, err := sh.svc.List()
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if got[0].State != "scanning" || got[0].ParentPath != "~/Photos" || got[0].Name != "Trip" {
		t.Errorf("summary = %+v, want scanning Trip under ~/Photos", got[0])
	}

	empty := newServiceHarness(t)
	if got, err := empty.svc.List(); err != nil || got == nil || len(got) != 0 {
		t.Errorf("empty List = %#v, %v, want an empty, non-nil slice", got, err)
	}
}

func TestAddFolders_EnqueuesNewOnly(t *testing.T) {
	sh := newServiceHarness(t)
	known := sh.mkdir(t, "Known")
	knownID := sh.mustAdd(t, known).ID
	fresh := sh.mkdir(t, "Fresh")
	sh.picker.paths = []string{known, fresh, sh.mkdir(t, "Known/Inside")}

	res, err := sh.svc.AddFolders()
	if err != nil {
		t.Fatalf("AddFolders: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("AddFolders = %+v, want three results", res)
	}
	if !res[0].Existing || res[0].ID != knownID || res[0].Relation != relSame {
		t.Errorf("res[0] = %+v, want the known folder, existing", res[0])
	}
	if res[1].Existing || res[1].Name != "Fresh" {
		t.Errorf("res[1] = %+v, want a new folder Fresh", res[1])
	}
	if !res[2].Existing || res[2].ID != knownID || res[2].Relation != "inside" {
		t.Errorf("res[2] = %+v, want inside the known folder", res[2])
	}
	if got, want := sh.enqueueCalls(), []enqueued{{res[1].ID, true}}; !slices.Equal(got, want) {
		t.Errorf("Enqueue calls = %v, want %v", got, want)
	}
	if n := len(sh.mustFolders(t)); n != 2 {
		t.Errorf("%d folders, want 2", n)
	}

	// A folder whose access was lost, added again, gets a new bookmark but
	// is not new: OnOnline resumes its pending work, nothing is enqueued.
	sh.bm.MakeUnusable(known)
	if _, _, err := sh.lib.Acquire(knownID); !errors.Is(err, ErrAccessLost) {
		t.Fatalf("Acquire = %v, want ErrAccessLost", err)
	}
	sh.picker.paths = []string{known}
	res, err = sh.svc.AddFolders()
	if err != nil || len(res) != 1 || res[0].Existing || res[0].Relation != relSame || res[0].ID != knownID {
		t.Fatalf("AddFolders again = %+v, %v, want the known folder restored", res, err)
	}
	if got := sh.enqueueCalls(); len(got) != 1 {
		t.Errorf("Enqueue calls = %v, want only the first one", got)
	}
}

func TestAddFolders_EnqueueFailure(t *testing.T) {
	sh := newServiceHarness(t)
	// The queue as the runtime wires it: an enqueue persists job_pending.
	// The first call fails before anything is persisted.
	fail := true
	var attempts []enqueued
	sh.svc.d.Enqueue = func(id string, full bool) error {
		attempts = append(attempts, enqueued{id, full})
		if fail {
			return errors.New("database is locked")
		}
		_, err := sh.lib.EnqueueJob(id, jobFull)
		return err
	}
	ok := sh.mkdir(t, "Ok")
	sh.picker.paths = []string{sh.mkdir(t, "Trip"), ok}

	// Both folders fail to queue; the first failure is reported.
	res, err := sh.svc.AddFolders()
	wantCode(t, err, apperr.CodeInternal)
	if len(res) != 2 || res[0].Name != "Trip" || res[1].Name != "Ok" || res[0].Existing || res[1].Existing {
		t.Fatalf("AddFolders = %+v, want both new folders despite the error", res)
	}
	id := res[0].ID
	if len(attempts) != 2 {
		t.Errorf("Enqueue attempts = %v, want one per new folder", attempts)
	}
	list, lerr := sh.svc.List()
	if lerr != nil || len(list) != 2 {
		t.Fatalf("List = %+v, %v, want both folders still added", list, lerr)
	}

	// Adding it again finds it existing and queues nothing; Rescan starts it.
	fail = false
	sh.picker.paths = []string{sh.root + "/Trip"}
	if res, err := sh.svc.AddFolders(); err != nil || len(res) != 1 || !res[0].Existing {
		t.Fatalf("AddFolders again = %+v, %v, want existing", res, err)
	}
	if len(attempts) != 2 {
		t.Errorf("re-adding an existing folder enqueued it: %v", attempts)
	}
	if err := sh.svc.Rescan(id); err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	f, err := sh.lib.Folder(id)
	if err != nil || !f.JobPending || f.JobKind != jobFull {
		t.Fatalf("after Rescan: %+v, %v, want a pending full job", f, err)
	}
	list, _ = sh.svc.List()
	for _, s := range list {
		if s.ID == id && s.State != summaryQueued {
			t.Errorf("state after Rescan = %q, want queued", s.State)
		}
	}
}

func TestAddFolders_Cancelled(t *testing.T) {
	sh := newServiceHarness(t)
	res, err := sh.svc.AddFolders()
	if err != nil {
		t.Fatalf("AddFolders: %v", err)
	}
	if res == nil || len(res) != 0 {
		t.Errorf("AddFolders = %#v, want an empty, non-nil slice", res)
	}
	if len(sh.enqueueCalls()) != 0 || len(sh.mustFolders(t)) != 0 {
		t.Error("a cancelled picker added or enqueued something")
	}
}

func TestAddFolders_PickerError(t *testing.T) {
	sh := newServiceHarness(t)
	sh.picker.err = errors.New("panel failed")
	_, err := sh.svc.AddFolders()
	wantCode(t, err, apperr.CodeInternal)
}

func TestAddFolders_AllFailedIsAnError(t *testing.T) {
	sh := newServiceHarness(t)
	file := sh.root + "/file.jpg"
	writeFile(t, file)
	sh.picker.paths = []string{file}
	res, err := sh.svc.AddFolders()
	wantCode(t, err, apperr.CodeInvalid) // ErrNotDirectory
	if len(res) != 0 {
		t.Errorf("AddFolders = %+v, want nothing", res)
	}

	// One good path among bad ones: the good one is reported, no error.
	good := sh.mkdir(t, "Good")
	sh.picker.paths = []string{file, good}
	res, err = sh.svc.AddFolders()
	if err != nil || len(res) != 1 || res[0].Name != "Good" {
		t.Errorf("AddFolders = %+v, %v, want Good alone", res, err)
	}
}

func TestAddFolders_GateClosedBeforeDialog(t *testing.T) {
	sh := newServiceHarness(t)
	sh.gate.Close(0)
	_, err := sh.svc.AddFolders()
	wantCode(t, err, apperr.CodeUnavailable)
	if sh.picker.calls != 0 {
		t.Error("the picker was shown after quit began")
	}
}

func TestAddFolders_GateClosedWhileDialogOpen(t *testing.T) {
	sh := newServiceHarness(t)
	sh.picker.block = true
	sh.picker.entered = make(chan struct{})
	sh.picker.release = make(chan struct{})
	sh.picker.paths = []string{sh.mkdir(t, "A"), sh.mkdir(t, "B")}

	type result struct {
		res []AddResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := sh.svc.AddFolders()
		done <- result{res, err}
	}()
	select {
	case <-sh.picker.entered:
	case <-time.After(serviceWait):
		t.Fatal("the picker was not shown")
	}
	// The binding left the gate while the dialog is up: Close does not wait.
	if !sh.gate.Close(0) {
		t.Fatal("Close found an operation inside the gate while the dialog was up")
	}
	close(sh.picker.release)

	var r result
	select {
	case r = <-done:
	case <-time.After(serviceWait):
		t.Fatal("AddFolders did not return")
	}
	wantCode(t, r.err, apperr.CodeUnavailable)
	if len(r.res) != 0 {
		t.Errorf("AddFolders = %+v, want nothing", r.res)
	}
	if n := len(sh.mustFolders(t)); n != 0 {
		t.Errorf("%d folders added after quit began, want 0", n)
	}
	if len(sh.enqueueCalls()) != 0 {
		t.Error("a folder was enqueued after quit began")
	}
}

func TestRemove_Declined(t *testing.T) {
	sh := newServiceHarness(t)
	id := sh.mustAdd(t, sh.mkdir(t, "Trip")).ID
	ok, err := sh.svc.Remove(id)
	if err != nil || ok {
		t.Fatalf("Remove = %v, %v, want false, nil", ok, err)
	}
	if !slices.Equal(sh.confirm.names, []string{"Trip"}) {
		t.Errorf("confirmed %v, want [Trip]", sh.confirm.names)
	}
	if n := len(sh.mustFolders(t)); n != 1 {
		t.Errorf("%d folders, want 1", n)
	}
	if len(sh.calls(&sh.removed)) != 0 {
		t.Error("OnRemoved called for a declined removal")
	}
}

func TestRemove_CancelsJobThroughOnRemoved(t *testing.T) {
	sh := newServiceHarness(t)
	id := sh.mustAdd(t, sh.mkdir(t, "Trip")).ID
	sh.confirm.ok = true
	ok, err := sh.svc.Remove(id)
	if err != nil || !ok {
		t.Fatalf("Remove = %v, %v, want true, nil", ok, err)
	}
	if got := sh.calls(&sh.removed); !slices.Equal(got, []string{id}) {
		t.Errorf("OnRemoved calls = %v, want [%s]", got, id)
	}
	if n := len(sh.mustFolders(t)); n != 0 {
		t.Errorf("%d folders, want 0", n)
	}
}

func TestRemove_Unknown(t *testing.T) {
	sh := newServiceHarness(t)
	sh.confirm.ok = true
	_, err := sh.svc.Remove("nope")
	wantCode(t, err, apperr.CodeNotFound)
	if len(sh.confirm.names) != 0 {
		t.Error("asked to confirm the removal of an unknown folder")
	}
}

func TestRemove_ConfirmerError(t *testing.T) {
	sh := newServiceHarness(t)
	id := sh.mustAdd(t, sh.mkdir(t, "Trip")).ID
	sh.confirm.ok, sh.confirm.err = true, errors.New("alert failed")
	_, err := sh.svc.Remove(id)
	wantCode(t, err, apperr.CodeInternal)
	if n := len(sh.mustFolders(t)); n != 1 {
		t.Errorf("%d folders, want 1", n)
	}
}

func TestRemove_GateClosedWhileConfirming(t *testing.T) {
	sh := newServiceHarness(t)
	id := sh.mustAdd(t, sh.mkdir(t, "Trip")).ID
	sh.confirm.ok, sh.confirm.block = true, true
	sh.confirm.entered = make(chan struct{})
	sh.confirm.release = make(chan struct{})

	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		ok, err := sh.svc.Remove(id)
		done <- result{ok, err}
	}()
	select {
	case <-sh.confirm.entered:
	case <-time.After(serviceWait):
		t.Fatal("the confirmation was not shown")
	}
	if !sh.gate.Close(0) {
		t.Fatal("Close found an operation inside the gate while the confirmation was up")
	}
	close(sh.confirm.release)

	var r result
	select {
	case r = <-done:
	case <-time.After(serviceWait):
		t.Fatal("Remove did not return")
	}
	wantCode(t, r.err, apperr.CodeUnavailable)
	if r.ok {
		t.Error("Remove reported true after quit began")
	}
	if n := len(sh.mustFolders(t)); n != 1 {
		t.Errorf("%d folders, want the folder still there", n)
	}
	if len(sh.calls(&sh.removed)) != 0 {
		t.Error("OnRemoved called after quit began")
	}
}

func TestRemove_RemovedWhileConfirming(t *testing.T) {
	sh := newServiceHarness(t)
	id := sh.mustAdd(t, sh.mkdir(t, "Trip")).ID
	sh.confirm.ok, sh.confirm.block = true, true
	sh.confirm.entered = make(chan struct{})
	sh.confirm.release = make(chan struct{})

	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		ok, err := sh.svc.Remove(id)
		done <- result{ok, err}
	}()
	<-sh.confirm.entered
	if err := sh.lib.Remove(id); err != nil { // e.g. dropped as gone meanwhile
		t.Fatal(err)
	}
	close(sh.confirm.release)
	r := <-done
	if r.err != nil || !r.ok {
		t.Errorf("Remove = %v, %v, want true, nil: the folder is gone as asked", r.ok, r.err)
	}
}

func TestRescan(t *testing.T) {
	sh := newServiceHarness(t)
	id := sh.mustAdd(t, sh.mkdir(t, "Trip")).ID
	if err := sh.svc.Rescan(id); err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if got, want := sh.enqueueCalls(), []enqueued{{id, true}}; !slices.Equal(got, want) {
		t.Errorf("Enqueue calls = %v, want %v", got, want)
	}
	wantCode(t, sh.svc.Rescan("nope"), apperr.CodeNotFound)
}

func TestRescan_Offline(t *testing.T) {
	sh := newServiceHarness(t)
	id := sh.mustAdd(t, sh.mkdir(t, "Trip")).ID
	if err := sh.db.SetPresence(id, presenceOffline); err != nil {
		t.Fatal(err)
	}
	wantCode(t, sh.svc.Rescan(id), apperr.CodeOffline)
	if len(sh.enqueueCalls()) != 0 {
		t.Error("an offline folder was enqueued")
	}
}

func TestRescan_EnqueueError(t *testing.T) {
	sh := newServiceHarness(t)
	id := sh.mustAdd(t, sh.mkdir(t, "Trip")).ID
	sh.svc.d.Enqueue = func(string, bool) error { return ErrNotFound }
	wantCode(t, sh.svc.Rescan(id), apperr.CodeNotFound)
}

func TestCheckNow(t *testing.T) {
	sh := newServiceHarness(t)
	trip := sh.mkdir(t, "Trip")
	id := sh.mustAdd(t, trip).ID
	sh.vols.Unmount("UUID-SYSTEM")
	sh.bm.Delete(trip)
	if err := sh.svc.CheckNow(); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	f, err := sh.lib.Folder(id)
	if err != nil || f.Presence != presenceOffline {
		t.Errorf("presence = %q, %v, want offline", f.Presence, err)
	}
}

func TestService_UnavailableAfterGateClosed(t *testing.T) {
	sh := newServiceHarness(t)
	id := sh.mustAdd(t, sh.mkdir(t, "Trip")).ID
	sh.confirm.ok = true
	sh.gate.Close(0)
	_, err := sh.svc.List()
	wantCode(t, err, apperr.CodeUnavailable)
	_, err = sh.svc.Remove(id)
	wantCode(t, err, apperr.CodeUnavailable)
	wantCode(t, sh.svc.Rescan(id), apperr.CodeUnavailable)
	wantCode(t, sh.svc.CheckNow(), apperr.CodeUnavailable)
	if len(sh.confirm.names) != 0 || len(sh.enqueueCalls()) != 0 || len(sh.mustFolders(t)) != 1 {
		t.Error("a binding did work after quit began")
	}
}

func TestAddPaths(t *testing.T) {
	h := newHarness(t)
	a, b := h.mkdir(t, "A"), h.mkdir(t, "B")
	var got []string
	res := h.lib.AddPaths([]string{a, h.root + "/missing", b, a}, func(id string) {
		// Called outside every Library lock: a call that takes the
		// mutation lock must not deadlock.
		if _, err := h.lib.CheckFolder(t.Context(), id); err != nil {
			t.Errorf("CheckFolder(%s) from enqueue: %v", id, err)
		}
		got = append(got, id)
	})
	if len(res) != 3 {
		t.Fatalf("AddPaths = %+v, want three results (the missing path skipped)", res)
	}
	if res[0].Existing || res[1].Existing || !res[2].Existing || res[2].ID != res[0].ID {
		t.Errorf("AddPaths = %+v", res)
	}
	if want := []string{res[0].ID, res[1].ID}; !slices.Equal(got, want) {
		t.Errorf("enqueued %v, want %v", got, want)
	}
	if reasons := h.rec.Events(events.LibraryChanged); len(reasons) != 2 {
		t.Errorf("library:changed events = %v, want two added", reasons)
	}

	// A nil enqueue is allowed.
	if res := h.lib.AddPaths([]string{h.mkdir(t, "C")}, nil); len(res) != 1 {
		t.Errorf("AddPaths(nil enqueue) = %+v", res)
	}
}

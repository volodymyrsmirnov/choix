package library

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// testNow is the clock every harness Library uses.
var testNow = time.Unix(1_700_000_000, 0)

// harness is a Library over the platform fakes, recording its callbacks.
type harness struct {
	lib      *Library
	db       *DB
	bm       *platformtest.Bookmarks
	vols     *platformtest.Volumes
	rec      *events.Recorder
	root     string // a resolved temporary directory holding the test folders
	previews string // the directory PreviewsDir puts each folder's previews in

	mu                       sync.Mutex
	offline, removed, online []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	h := &harness{
		db:       openTestDB(t),
		bm:       &platformtest.Bookmarks{},
		vols:     &platformtest.Volumes{},
		rec:      &events.Recorder{},
		root:     root,
		previews: t.TempDir(),
	}
	h.vols.Add("/", platform.VolumeInfo{UUID: "UUID-SYSTEM", Name: "Macintosh HD"})
	h.lib = New(Options{
		DB:        h.db,
		Bookmarks: h.bm,
		Volumes:   h.vols,
		Emit:      h.rec,
		Now:       func() time.Time { return testNow },
		OnOffline: func(id string) { h.record(&h.offline, id) },
		OnRemoved: func(id string) { h.record(&h.removed, id) },
		OnOnline:  func(id string) { h.record(&h.online, id) },
		PreviewsDir: func(id string) (string, error) {
			dir := filepath.Join(h.previews, id)
			return dir, os.MkdirAll(dir, 0o700)
		},
	})
	return h
}

func (h *harness) record(list *[]string, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	*list = append(*list, id)
}

func (h *harness) calls(list *[]string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(*list)
}

// mkdir creates rel (and its parents) under h.root and returns its path.
func (h *harness) mkdir(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join(h.root, rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return p
}

func (h *harness) mustAdd(t *testing.T, path string) AddResult {
	t.Helper()
	res, err := h.lib.Add(path)
	if err != nil {
		t.Fatalf("Add(%s): %v", path, err)
	}
	return res
}

func (h *harness) mustFolders(t *testing.T) []Folder {
	t.Helper()
	fs, err := h.lib.Folders()
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	return fs
}

// libraryEvents returns the reasons of every library:changed event, checking
// that each carries id.
func (h *harness) libraryEvents(t *testing.T, id string) []string {
	t.Helper()
	var reasons []string
	for _, e := range h.rec.Events(events.LibraryChanged) {
		ev, ok := e.(events.LibraryChangedEvent)
		if !ok {
			t.Fatalf("library:changed payload is %T, want events.LibraryChangedEvent", e)
		}
		if ev.FolderID != id {
			t.Fatalf("library:changed %q for folder %q, want %q", ev.Reason, ev.FolderID, id)
		}
		reasons = append(reasons, ev.Reason)
	}
	return reasons
}

func TestAdd_NewFolder(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")

	res := h.mustAdd(t, trip)
	if res.Existing || res.Relation != "" || res.Name != "Trip" {
		t.Fatalf("Add = %+v, want a new folder named Trip", res)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(res.ID) {
		t.Fatalf("ID = %q, want 16 lowercase hex characters", res.ID)
	}

	fs := h.mustFolders(t)
	if len(fs) != 1 {
		t.Fatalf("folders = %d, want 1", len(fs))
	}
	f := fs[0]
	if f.ID != res.ID || f.Path != trip || f.Name != "Trip" {
		t.Fatalf("row = {%s %s %s}, want {%s %s Trip}", f.ID, f.Path, f.Name, res.ID, trip)
	}
	if len(f.Bookmark) == 0 {
		t.Fatal("bookmark is empty")
	}
	if p, _, err := h.bm.Resolve(f.Bookmark); err != nil || p != trip {
		t.Fatalf("bookmark resolves to %q, %v; want %q", p, err, trip)
	}
	if f.VolumeUUID != "UUID-SYSTEM" || f.VolumeName != "Macintosh HD" {
		t.Fatalf("volume = %q %q, want UUID-SYSTEM Macintosh HD", f.VolumeUUID, f.VolumeName)
	}
	if f.AddedAt != testNow.Unix() || f.Presence != "present" || f.JobPending {
		t.Fatalf("row = %+v, want added now, present, no job", f)
	}
	if got := h.libraryEvents(t, res.ID); !slices.Equal(got, []string{"added"}) {
		t.Fatalf("events = %v, want [added]", got)
	}
	ev := h.rec.Events(events.LibraryChanged)[0].(events.LibraryChangedEvent)
	if ev.Name != "Trip" {
		t.Fatalf("event name = %q, want Trip", ev.Name)
	}
}

func TestAdd_UsesNewID(t *testing.T) {
	h := newHarness(t)
	h.lib.newID = func() string { return "0123456789abcdef" }
	res := h.mustAdd(t, h.mkdir(t, "Trip"))
	if res.ID != "0123456789abcdef" {
		t.Fatalf("ID = %q, want the one NewID returned", res.ID)
	}
}

func TestAdd_FileIsErrNotDirectory(t *testing.T) {
	h := newHarness(t)
	file := filepath.Join(h.root, "photo.jpg")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.lib.Add(file); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("Add(file) = %v, want ErrNotDirectory", err)
	}
	if fs := h.mustFolders(t); len(fs) != 0 {
		t.Fatalf("folders = %d, want 0", len(fs))
	}
	if evs := h.rec.Events(events.LibraryChanged); len(evs) != 0 {
		t.Fatalf("events = %v, want none", evs)
	}
}

func TestAdd_MissingPath(t *testing.T) {
	h := newHarness(t)
	if _, err := h.lib.Add(filepath.Join(h.root, "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Add(missing) = %v, want os.ErrNotExist", err)
	}
	if _, err := h.lib.Add("relative/path"); err == nil {
		t.Fatal("Add(relative) succeeded, want an error")
	}
	if fs := h.mustFolders(t); len(fs) != 0 {
		t.Fatalf("folders = %d, want 0", len(fs))
	}
}

func TestAdd_SameFolderTwice(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	first := h.mustAdd(t, trip)
	second := h.mustAdd(t, trip)
	want := AddResult{ID: first.ID, Name: "Trip", Existing: true, Relation: "same"}
	if second != want {
		t.Fatalf("second Add = %+v, want %+v", second, want)
	}
	if fs := h.mustFolders(t); len(fs) != 1 {
		t.Fatalf("folders = %d, want 1", len(fs))
	}
	if got := h.libraryEvents(t, first.ID); !slices.Equal(got, []string{"added"}) {
		t.Fatalf("events = %v, want [added]", got)
	}
}

// caseInsensitive reports whether the file system holding dir ignores case.
func caseInsensitive(dir string) bool {
	_, err := os.Stat(strings.ToUpper(dir))
	return err == nil
}

// TestAdd_Variants pins Review Focus 3: the same folder under another
// spelling, a folder inside it and its parent all leave one row.
func TestAdd_Variants(t *testing.T) {
	tests := []struct {
		name     string
		variant  func(t *testing.T, h *harness, trip string) string
		relation string
	}{
		{"trailing slash", func(t *testing.T, h *harness, trip string) string {
			return trip + "/"
		}, "same"},
		{"different case", func(t *testing.T, h *harness, trip string) string {
			if !caseInsensitive(trip) {
				t.Skip("the temporary directory's file system is case-sensitive")
			}
			return strings.ToUpper(trip)
		}, "same"},
		{"different case of the last element", func(t *testing.T, h *harness, trip string) string {
			if !caseInsensitive(trip) {
				t.Skip("the temporary directory's file system is case-sensitive")
			}
			return filepath.Join(h.root, "tRIP")
		}, "same"},
		{"symlink to the folder", func(t *testing.T, h *harness, trip string) string {
			link := filepath.Join(h.root, "link")
			if err := os.Symlink(trip, link); err != nil {
				t.Fatal(err)
			}
			return link
		}, "same"},
		{"unresolved temporary path", func(t *testing.T, h *harness, trip string) string {
			return filepath.Join(h.root, "x", "..", "Trip")
		}, "same"},
		{"subfolder", func(t *testing.T, h *harness, trip string) string {
			return h.mkdir(t, "Trip/Day 1")
		}, "inside"},
		{"its own picks", func(t *testing.T, h *harness, trip string) string {
			return h.mkdir(t, "Trip/picks")
		}, "inside"},
		{"symlink to a subfolder", func(t *testing.T, h *harness, trip string) string {
			day := h.mkdir(t, "Trip/Day 2")
			link := filepath.Join(h.root, "day-link")
			if err := os.Symlink(day, link); err != nil {
				t.Fatal(err)
			}
			return link
		}, "inside"},
		{"parent", func(t *testing.T, h *harness, trip string) string {
			return h.root
		}, "contains"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.mkdir(t, "x")
			trip := h.mkdir(t, "Trip")
			first := h.mustAdd(t, trip)
			variant := tt.variant(t, h, trip)

			got := h.mustAdd(t, variant)
			want := AddResult{ID: first.ID, Name: "Trip", Existing: true, Relation: tt.relation}
			if got != want {
				t.Fatalf("Add(%s) = %+v, want %+v", variant, got, want)
			}
			fs := h.mustFolders(t)
			if len(fs) != 1 || fs[0].Path != trip {
				t.Fatalf("folders = %+v, want only %s", fs, trip)
			}
		})
	}
}

// TestAcquire_UnusableBookmarkIsAccessLost pins Review Focus 2.
func TestAcquire_UnusableBookmarkIsAccessLost(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	h.bm.MakeUnusable(trip)

	root, release, err := h.lib.Acquire(res.ID)
	if !errors.Is(err, ErrAccessLost) {
		t.Fatalf("Acquire = %q, %v; want ErrAccessLost", root, err)
	}
	if release != nil {
		t.Fatal("Acquire returned a release func with an error")
	}
	f, err := h.lib.Folder(res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Presence != "access_lost" {
		t.Fatalf("presence = %q, want access_lost", f.Presence)
	}
	if got := h.libraryEvents(t, res.ID); !slices.Equal(got, []string{"added", "offline"}) {
		t.Fatalf("events = %v, want [added offline]", got)
	}
	if got := h.calls(&h.offline); !slices.Equal(got, []string{res.ID}) {
		t.Fatalf("OnOffline calls = %v, want [%s]", got, res.ID)
	}
	if h.lib.InUse(res.ID) || len(h.bm.Active()) != 0 {
		t.Fatalf("InUse = %v, active = %v; want nothing held", h.lib.InUse(res.ID), h.bm.Active())
	}
	if AccessLostMessage != "Choix lost access to this folder. Add it again." {
		t.Fatalf("AccessLostMessage = %q", AccessLostMessage)
	}

	// A second failure is not a new transition.
	if _, _, err := h.lib.Acquire(res.ID); !errors.Is(err, ErrAccessLost) {
		t.Fatalf("second Acquire = %v, want ErrAccessLost", err)
	}
	if got := h.libraryEvents(t, res.ID); len(got) != 2 {
		t.Fatalf("events = %v, want no new event", got)
	}
	if got := h.calls(&h.offline); len(got) != 1 {
		t.Fatalf("OnOffline calls = %v, want one", got)
	}
}

func TestAcquire_DeniedAccessIsAccessLost(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	h.bm.DenyAccess(trip)
	if _, _, err := h.lib.Acquire(res.ID); !errors.Is(err, ErrAccessLost) {
		t.Fatalf("Acquire = %v, want ErrAccessLost", err)
	}
	if f, _ := h.lib.Folder(res.ID); f.Presence != "access_lost" {
		t.Fatalf("presence = %q, want access_lost", f.Presence)
	}
}

func TestAdd_AccessLostFolderReplacesBookmark(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	before, err := h.lib.Folder(res.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.bm.MakeUnusable(trip)
	if _, _, err := h.lib.Acquire(res.ID); !errors.Is(err, ErrAccessLost) {
		t.Fatalf("Acquire = %v, want ErrAccessLost", err)
	}

	// Adding the folder again (the picker grants access anew) makes a fresh,
	// usable bookmark.
	again := h.mustAdd(t, trip+"/")
	want := AddResult{ID: res.ID, Name: "Trip", Existing: false, Relation: "same"}
	if again != want {
		t.Fatalf("Add again = %+v, want %+v", again, want)
	}
	fs := h.mustFolders(t)
	if len(fs) != 1 {
		t.Fatalf("folders = %d, want 1", len(fs))
	}
	if fs[0].Presence != "present" {
		t.Fatalf("presence = %q, want present", fs[0].Presence)
	}
	if bytes.Equal(fs[0].Bookmark, before.Bookmark) {
		t.Fatal("bookmark bytes did not change")
	}
	if got := h.calls(&h.online); !slices.Equal(got, []string{res.ID}) {
		t.Fatalf("OnOnline calls = %v, want [%s]", got, res.ID)
	}
	if got := h.libraryEvents(t, res.ID); !slices.Equal(got, []string{"added", "offline", "online"}) {
		t.Fatalf("events = %v, want [added offline online]", got)
	}

	root, release, err := h.lib.Acquire(res.ID)
	if err != nil || root != trip {
		t.Fatalf("Acquire after re-adding = %q, %v; want %q", root, err, trip)
	}
	release()
}

// TestAccessLost_IgnoresReplacedBookmark: an Acquire that started with the
// old bookmark and fails after the folder was added again must not undo the
// restore.
func TestAccessLost_IgnoresReplacedBookmark(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	stale, err := h.lib.Folder(res.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.bm.MakeUnusable(trip)
	if _, _, err := h.lib.Acquire(res.ID); !errors.Is(err, ErrAccessLost) {
		t.Fatalf("Acquire = %v, want ErrAccessLost", err)
	}
	h.mustAdd(t, trip) // restores access with a new bookmark

	stale.Presence = "present"
	if h.lib.accessLost(stale) {
		t.Fatal("accessLost with a replaced bookmark returned a notification")
	}
	if f, _ := h.lib.Folder(res.ID); f.Presence != "present" {
		t.Fatalf("presence = %q, want present", f.Presence)
	}
	if err := h.lib.Remove(res.ID); err != nil {
		t.Fatal(err)
	}
	if h.lib.accessLost(stale) {
		t.Fatal("accessLost of a removed folder returned a notification")
	}
}

func TestAdd_AccessLostFolderOnlyReplacedBySameRelation(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	day := h.mkdir(t, "Trip/Day 1")
	res := h.mustAdd(t, trip)
	h.bm.MakeUnusable(trip)
	if _, _, err := h.lib.Acquire(res.ID); !errors.Is(err, ErrAccessLost) {
		t.Fatalf("Acquire = %v, want ErrAccessLost", err)
	}
	got := h.mustAdd(t, day)
	if !got.Existing || got.Relation != "inside" || got.ID != res.ID {
		t.Fatalf("Add(subfolder) = %+v, want the existing folder, inside", got)
	}
	if f, _ := h.lib.Folder(res.ID); f.Presence != "access_lost" {
		t.Fatalf("presence = %q, want access_lost", f.Presence)
	}
	if got := h.calls(&h.online); len(got) != 0 {
		t.Fatalf("OnOnline calls = %v, want none", got)
	}
}

func TestAcquire_ClearsAccessLost(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	if err := h.db.SetPresence(res.ID, "access_lost"); err != nil {
		t.Fatal(err)
	}
	_, release, err := h.lib.Acquire(res.ID)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()
	if f, _ := h.lib.Folder(res.ID); f.Presence != "present" {
		t.Fatalf("presence = %q, want present", f.Presence)
	}
	if got := h.libraryEvents(t, res.ID); !slices.Equal(got, []string{"added", "online"}) {
		t.Fatalf("events = %v, want [added online]", got)
	}
	if got := h.calls(&h.online); !slices.Equal(got, []string{res.ID}) {
		t.Fatalf("OnOnline calls = %v, want [%s]", got, res.ID)
	}

	// Sharing the lease, or starting it again while present, is no transition.
	_, release2, err := h.lib.Acquire(res.ID)
	if err != nil {
		t.Fatalf("Acquire 2: %v", err)
	}
	release2()
	release()
	_, release3, err := h.lib.Acquire(res.ID)
	if err != nil {
		t.Fatalf("Acquire 3: %v", err)
	}
	release3()
	if got := h.calls(&h.online); len(got) != 1 {
		t.Fatalf("OnOnline calls = %v, want one", got)
	}
}

// TestAcquire_OnOnlineMayReenter: OnOnline runs outside the Library's locks,
// so a callback that acquires the same folder (the queue resuming a job)
// does not deadlock.
func TestAcquire_OnOnlineMayReenter(t *testing.T) {
	h := newHarness(t)
	res := h.mustAdd(t, h.mkdir(t, "Trip"))
	if err := h.db.SetPresence(res.ID, "access_lost"); err != nil {
		t.Fatal(err)
	}
	reentered := make(chan error, 1)
	h.lib.onOnline = func(id string) {
		_, release, err := h.lib.Acquire(id)
		if err == nil {
			release()
		}
		reentered <- err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, release, err := h.lib.Acquire(res.ID)
		if err == nil {
			release()
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire deadlocked in OnOnline")
	}
	select {
	case err := <-reentered:
		if err != nil {
			t.Fatalf("Acquire from OnOnline: %v", err)
		}
	default:
		t.Fatal("OnOnline was not called")
	}
}

func TestAcquire_DoesNotChangeOtherPresence(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	if err := h.db.SetPresence(res.ID, "offline"); err != nil {
		t.Fatal(err)
	}
	_, release, err := h.lib.Acquire(res.ID)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	if f, _ := h.lib.Folder(res.ID); f.Presence != "offline" {
		t.Fatalf("presence = %q, want offline (presence checking is not Acquire's job)", f.Presence)
	}
}

func TestRemove_DeletesRowKeepsFolder(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	photo := filepath.Join(trip, "photo.jpg")
	if err := os.WriteFile(photo, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.mkdir(t, "Trip/.choix")
	res := h.mustAdd(t, trip)
	previews := filepath.Join(h.previews, res.ID)
	if err := os.MkdirAll(previews, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previews, "0.jpg"), []byte("preview"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.lib.Remove(res.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := h.lib.Folder(res.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Folder after Remove = %v, want ErrNotFound", err)
	}
	if fs := h.mustFolders(t); len(fs) != 0 {
		t.Fatalf("folders = %d, want 0", len(fs))
	}
	if data, err := os.ReadFile(photo); err != nil || string(data) != "original" {
		t.Fatalf("photo after Remove = %q, %v; want it untouched", data, err)
	}
	if _, err := os.Stat(filepath.Join(trip, ".choix")); err != nil {
		t.Fatalf("the folder's .choix after Remove: %v", err)
	}
	if _, err := os.Stat(previews); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previews directory after Remove: %v, want it gone", err)
	}
	if _, err := os.Stat(h.previews); err != nil {
		t.Fatalf("the previews parent after Remove: %v, want it kept", err)
	}
	if got := h.libraryEvents(t, res.ID); !slices.Equal(got, []string{"added", "removed"}) {
		t.Fatalf("events = %v, want [added removed]", got)
	}
	if got := h.calls(&h.removed); !slices.Equal(got, []string{res.ID}) {
		t.Fatalf("OnRemoved calls = %v, want [%s]", got, res.ID)
	}

	// The folder can be added again, as a new folder.
	again := h.mustAdd(t, trip)
	if again.Existing || again.ID == res.ID {
		t.Fatalf("Add after Remove = %+v, want a new folder", again)
	}
}

func TestRemove_UnknownIsErrNotFound(t *testing.T) {
	h := newHarness(t)
	if err := h.lib.Remove("0123456789abcdef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove(unknown) = %v, want ErrNotFound", err)
	}
	if got := h.calls(&h.removed); len(got) != 0 {
		t.Fatalf("OnRemoved calls = %v, want none", got)
	}
	if evs := h.rec.Events(events.LibraryChanged); len(evs) != 0 {
		t.Fatalf("events = %v, want none", evs)
	}
}

func TestRemove_PreviewsDirGuard(t *testing.T) {
	tests := []struct {
		name string
		dir  func(base, id string) string
	}{
		{"last element is not the id", func(base, id string) string { return base }},
		{"relative path", func(base, id string) string { return id }},
		{"empty path", func(base, id string) string { return "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			trip := h.mkdir(t, "Trip")
			res := h.mustAdd(t, trip)
			keep := filepath.Join(h.previews, "keep.jpg")
			if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			h.lib.previewsDir = func(id string) (string, error) { return tt.dir(h.previews, id), nil }

			if err := h.lib.Remove(res.ID); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if _, err := os.Stat(keep); err != nil {
				t.Fatalf("file outside the folder's previews directory: %v, want it kept", err)
			}
			if _, err := h.lib.Folder(res.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Folder after Remove = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestRemove_PreviewsDirErrorAndNil(t *testing.T) {
	h := newHarness(t)
	res := h.mustAdd(t, h.mkdir(t, "Trip"))
	h.lib.previewsDir = func(string) (string, error) { return "", errors.New("no app directory") }
	if err := h.lib.Remove(res.ID); err != nil {
		t.Fatalf("Remove with a failing PreviewsDir: %v", err)
	}

	res = h.mustAdd(t, h.mkdir(t, "Other"))
	h.lib.previewsDir = nil
	if err := h.lib.Remove(res.ID); err != nil {
		t.Fatalf("Remove with no PreviewsDir: %v", err)
	}
	if fs := h.mustFolders(t); len(fs) != 0 {
		t.Fatalf("folders = %d, want 0", len(fs))
	}
}

func TestAcquire_RefCounted(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	if h.lib.InUse(res.ID) {
		t.Fatal("InUse before Acquire")
	}

	root1, release1, err := h.lib.Acquire(res.ID)
	if err != nil {
		t.Fatalf("Acquire 1: %v", err)
	}
	root2, release2, err := h.lib.Acquire(res.ID)
	if err != nil {
		t.Fatalf("Acquire 2: %v", err)
	}
	if root1 != trip || root2 != trip {
		t.Fatalf("roots = %q, %q; want %q", root1, root2, trip)
	}
	if got := h.bm.Active(); !slices.Equal(got, []string{trip}) {
		t.Fatalf("active = %v, want one lease on %s", got, trip)
	}
	if !h.lib.InUse(res.ID) {
		t.Fatal("InUse = false while acquired")
	}

	release1()
	release1() // releasing twice is safe and does not drop the other lease
	if got := h.bm.Active(); len(got) != 1 || !h.lib.InUse(res.ID) {
		t.Fatalf("after one release: active = %v, InUse = %v; want still held", got, h.lib.InUse(res.ID))
	}
	release2()
	if got := h.bm.Active(); len(got) != 0 || h.lib.InUse(res.ID) {
		t.Fatalf("after both releases: active = %v, InUse = %v; want nothing", got, h.lib.InUse(res.ID))
	}
	release2()
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("after releasing again: active = %v", got)
	}

	// Access starts again after the last release.
	_, release3, err := h.lib.Acquire(res.ID)
	if err != nil {
		t.Fatalf("Acquire 3: %v", err)
	}
	if got := h.bm.Active(); len(got) != 1 {
		t.Fatalf("active = %v, want one lease", got)
	}
	release3()
}

func TestAcquire_UnknownIsErrNotFound(t *testing.T) {
	h := newHarness(t)
	if _, _, err := h.lib.Acquire("0123456789abcdef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Acquire(unknown) = %v, want ErrNotFound", err)
	}
	if h.lib.InUse("0123456789abcdef") {
		t.Fatal("InUse(unknown) = true")
	}
}

func TestAcquire_RemovedWhileInUse(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	_, release, err := h.lib.Acquire(res.ID)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := h.lib.Remove(res.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, _, err := h.lib.Acquire(res.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Acquire after Remove = %v, want ErrNotFound", err)
	}
	if !h.lib.InUse(res.ID) || len(h.bm.Active()) != 1 {
		t.Fatal("the lease taken before Remove was dropped")
	}
	release()
	if h.lib.InUse(res.ID) || len(h.bm.Active()) != 0 {
		t.Fatalf("after release: InUse = %v, active = %v", h.lib.InUse(res.ID), h.bm.Active())
	}
}

func TestAcquire_FollowsMove(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	h.vols.Add(filepath.Join(h.root, "Archive"), platform.VolumeInfo{UUID: "UUID-ARCHIVE", Name: "Archive"})
	moved := filepath.Join(h.mkdir(t, "Archive"), "Trip 2026")
	if err := os.Rename(trip, moved); err != nil {
		t.Fatal(err)
	}
	h.bm.Move(trip, moved)

	root, release, err := h.lib.Acquire(res.ID)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()
	if root != moved {
		t.Fatalf("root = %q, want %q", root, moved)
	}
	f, err := h.lib.Folder(res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != moved || f.Name != "Trip 2026" || f.VolumeUUID != "UUID-ARCHIVE" || f.VolumeName != "Archive" {
		t.Fatalf("row = {%s %s %s %s}, want the new location", f.Path, f.Name, f.VolumeUUID, f.VolumeName)
	}
	if got := h.libraryEvents(t, res.ID); !slices.Equal(got, []string{"added", "moved"}) {
		t.Fatalf("events = %v, want [added moved]", got)
	}
	// The old location is free; the new one is the folder.
	if got := h.mustAdd(t, moved); !got.Existing || got.Relation != "same" || got.ID != res.ID {
		t.Fatalf("Add(new location) = %+v, want the existing folder", got)
	}
}

func TestAcquire_GoneIsErrOffline(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	h.bm.Delete(trip)

	if _, release, err := h.lib.Acquire(res.ID); !errors.Is(err, ErrOffline) || release != nil {
		t.Fatalf("Acquire = %v, want ErrOffline and no release", err)
	}
	f, err := h.lib.Folder(res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Presence != "present" {
		t.Fatalf("presence = %q, want it unchanged", f.Presence)
	}
	if h.lib.InUse(res.ID) || len(h.bm.Active()) != 0 {
		t.Fatal("something is held after a failed Acquire")
	}
	if got := h.calls(&h.offline); len(got) != 0 {
		t.Fatalf("OnOffline calls = %v, want none (presence is not Acquire's job)", got)
	}
}

func TestAcquire_Concurrent(t *testing.T) {
	h := newHarness(t)
	res := h.mustAdd(t, h.mkdir(t, "Trip"))
	other := h.mustAdd(t, h.mkdir(t, "Other"))

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := range 64 {
		id := res.ID
		if i%2 == 1 {
			id = other.ID
		}
		wg.Go(func() {
			for range 20 {
				_, release, err := h.lib.Acquire(id)
				if err != nil {
					errs <- err
					return
				}
				if n := len(h.bm.Active()); n > 2 {
					errs <- fmt.Errorf("%d leases active, want at most one per folder", n)
				}
				release()
				release()
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("active = %v, want none", got)
	}
	if h.lib.InUse(res.ID) || h.lib.InUse(other.ID) {
		t.Fatal("InUse after every release")
	}
}

func TestAdd_ConcurrentNestedFolders(t *testing.T) {
	h := newHarness(t)
	// Every pair is related, whichever is added first.
	paths := []string{
		h.mkdir(t, "Trip"),
		h.mkdir(t, "Trip/Day 1"),
		h.mkdir(t, "Trip/Day 1/picks"),
		h.root,
		h.mkdir(t, "Trip") + "/",
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(paths)*4)
	for range 4 {
		for _, p := range paths {
			wg.Go(func() {
				if _, err := h.lib.Add(p); err != nil {
					errs <- err
				}
			})
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if fs := h.mustFolders(t); len(fs) != 1 {
		t.Fatalf("folders = %d, want 1", len(fs))
	}
}

func TestPassThroughs(t *testing.T) {
	h := newHarness(t)
	res := h.mustAdd(t, h.mkdir(t, "Trip"))
	seq, err := h.lib.EnqueueJob(res.ID, "full")
	if err != nil || seq != 1 {
		t.Fatalf("EnqueueJob = %d, %v; want 1", seq, err)
	}
	pending, err := h.lib.PendingJobs()
	if err != nil || len(pending) != 1 || pending[0] != (PendingJob{ID: res.ID, Kind: "full", Seq: 1}) {
		t.Fatalf("PendingJobs = %+v, %v", pending, err)
	}
	if cleared, err := h.lib.FinishJob(res.ID, seq, "boom"); err != nil || !cleared {
		t.Fatalf("FinishJob = %v, %v; want cleared", cleared, err)
	}
	if f, _ := h.lib.Folder(res.ID); f.JobPending || f.JobError != "boom" {
		t.Fatalf("row = %+v, want finished with error boom", f)
	}
	if _, err := h.lib.EnqueueJob(res.ID, "backfill"); err != nil {
		t.Fatal(err)
	}
	if err := h.lib.ClearJob(res.ID); err != nil {
		t.Fatal(err)
	}
	if pending, _ := h.lib.PendingJobs(); len(pending) != 0 {
		t.Fatalf("PendingJobs after ClearJob = %+v", pending)
	}
}

func TestNew_NilEmitterAndCallbacks(t *testing.T) {
	d := openTestDB(t)
	bm := &platformtest.Bookmarks{}
	vols := &platformtest.Volumes{}
	vols.Add("/", platform.VolumeInfo{UUID: "U"})
	lib := New(Options{DB: d, Bookmarks: bm, Volumes: vols})
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := lib.Add(dir)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	bm.MakeUnusable(dir)
	if _, _, err := lib.Acquire(res.ID); !errors.Is(err, ErrAccessLost) {
		t.Fatalf("Acquire = %v, want ErrAccessLost", err)
	}
	if _, err := lib.Add(dir); err != nil {
		t.Fatalf("Add again: %v", err)
	}
	if err := lib.Remove(res.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

// writeFile creates p with some content, and its parent directories.
func writeFile(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRemove_PreviewsDirOverlappingAFolderIsRefused: a PreviewsDir that
// names, or leads through a symbolic link into, a directory overlapping any
// user folder is never removed.
func TestRemove_PreviewsDirOverlappingAFolderIsRefused(t *testing.T) {
	tests := []struct {
		name string
		// setup returns the directory PreviewsDir answers and a file that
		// must survive Remove.
		setup func(t *testing.T, h *harness, trip, other, id string) (dir, keep string)
	}{
		{"inside the removed folder", func(t *testing.T, h *harness, trip, other, id string) (string, string) {
			dir := filepath.Join(trip, id)
			return dir, filepath.Join(dir, "photo.jpg")
		}},
		{"inside another folder", func(t *testing.T, h *harness, trip, other, id string) (string, string) {
			dir := filepath.Join(other, id)
			return dir, filepath.Join(dir, "photo.jpg")
		}},
		{"through a symlinked ancestor into the removed folder", func(t *testing.T, h *harness, trip, other, id string) (string, string) {
			link := filepath.Join(h.root, "Support")
			if err := os.Symlink(trip, link); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(link, id), filepath.Join(trip, id, "photo.jpg")
		}},
		{"through a symlinked ancestor into another folder", func(t *testing.T, h *harness, trip, other, id string) (string, string) {
			link := filepath.Join(h.root, "Support")
			if err := os.Symlink(filepath.Join(other, "nested"), link); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(link, id), filepath.Join(other, "nested", id, "photo.jpg")
		}},
		{"missing, under a symlinked ancestor into a folder", func(t *testing.T, h *harness, trip, other, id string) (string, string) {
			link := filepath.Join(h.root, "Support")
			if err := os.Symlink(other, link); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(link, "missing", id), filepath.Join(other, "photo.jpg")
		}},
		{"containing another folder", func(t *testing.T, h *harness, trip, other, id string) (string, string) {
			dir := filepath.Join(h.root, "Cache", id)
			inner := filepath.Join(dir, "Inner")
			if err := os.MkdirAll(inner, 0o755); err != nil {
				t.Fatal(err)
			}
			h.mustAdd(t, inner)
			return dir, filepath.Join(inner, "photo.jpg")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			trip := h.mkdir(t, "Trip")
			other := h.mkdir(t, "Other")
			res := h.mustAdd(t, trip)
			h.mustAdd(t, other)
			dir, keep := tt.setup(t, h, trip, other, res.ID)
			writeFile(t, keep)
			h.lib.previewsDir = func(string) (string, error) { return dir, nil }

			if err := h.lib.Remove(res.ID); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if _, err := os.Stat(keep); err != nil {
				t.Fatalf("%s after Remove: %v, want it kept", keep, err)
			}
			if _, err := h.lib.Folder(res.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Folder after Remove = %v, want ErrNotFound", err)
			}
		})
	}
}

// TestRemove_PreviewsDirInsideRenamedFolderIsRefused: a folder renamed on
// disk before anything recorded its new location is still protected at the
// location its bookmark resolves to.
func TestRemove_PreviewsDirInsideRenamedFolderIsRefused(t *testing.T) {
	for _, which := range []string{"the removed folder", "another folder"} {
		t.Run(which, func(t *testing.T) {
			logs := captureLogs(t)
			h := newHarness(t)
			trip := h.mkdir(t, "Trip")
			other := h.mkdir(t, "Other")
			res := h.mustAdd(t, trip)
			h.mustAdd(t, other)

			src := trip
			if which == "another folder" {
				src = other
			}
			renamed := filepath.Join(h.root, "Renamed")
			if err := os.Rename(src, renamed); err != nil {
				t.Fatal(err)
			}
			h.bm.Move(src, renamed)
			dir := filepath.Join(renamed, res.ID)
			keep := filepath.Join(dir, "photo.jpg")
			writeFile(t, keep)
			h.lib.previewsDir = func(string) (string, error) { return dir, nil }

			if err := h.lib.Remove(res.ID); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if _, err := os.Stat(keep); err != nil {
				t.Fatalf("%s after Remove: %v, want it kept", keep, err)
			}
			if out := logs.String(); !strings.Contains(out, "refusing to remove a previews directory that overlaps a folder") {
				t.Fatalf("log = %q, want a refusal warning", out)
			}
		})
	}
}

// normalizationInsensitive reports whether the file system finds a name
// created in NFC under its NFD spelling nfd.
func normalizationInsensitive(nfd string) bool {
	_, err := os.Stat(nfd)
	return err == nil
}

func TestAdd_UnicodeNormalizationVariants(t *testing.T) {
	h := newHarness(t)
	nfc := h.mkdir(t, "Café")
	nfd := filepath.Join(h.root, "Café")
	if !normalizationInsensitive(nfd) {
		t.Skip("the temporary directory's file system distinguishes NFC from NFD")
	}
	first := h.mustAdd(t, nfc)

	got := h.mustAdd(t, nfd)
	if want := (AddResult{ID: first.ID, Name: first.Name, Existing: true, Relation: "same"}); got != want {
		t.Fatalf("Add(NFD) = %+v, want %+v", got, want)
	}
	h.mkdir(t, "Café/Day 1")
	got = h.mustAdd(t, filepath.Join(nfd, "Day 1"))
	if !got.Existing || got.Relation != "inside" || got.ID != first.ID {
		t.Fatalf("Add(NFD/Day 1) = %+v, want the existing folder, inside", got)
	}
	if fs := h.mustFolders(t); len(fs) != 1 {
		t.Fatalf("folders = %d, want 1", len(fs))
	}
}

func TestAdd_AccessLostRecoveredUnderOtherNormalization(t *testing.T) {
	h := newHarness(t)
	nfc := h.mkdir(t, "Café")
	nfd := filepath.Join(h.root, "Café")
	if !normalizationInsensitive(nfd) {
		t.Skip("the temporary directory's file system distinguishes NFC from NFD")
	}
	res := h.mustAdd(t, nfc)
	before, err := h.lib.Folder(res.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.bm.MakeUnusable(nfc)
	if _, _, err := h.lib.Acquire(res.ID); !errors.Is(err, ErrAccessLost) {
		t.Fatalf("Acquire = %v, want ErrAccessLost", err)
	}

	got := h.mustAdd(t, nfd)
	if got.Existing || got.ID != res.ID || got.Relation != "same" {
		t.Fatalf("Add(NFD) = %+v, want the bookmark of %s replaced", got, res.ID)
	}
	f, err := h.lib.Folder(res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Presence != "present" || bytes.Equal(f.Bookmark, before.Bookmark) {
		t.Fatalf("row = presence %q, bookmark changed %v; want present with a new bookmark",
			f.Presence, !bytes.Equal(f.Bookmark, before.Bookmark))
	}
	if got := h.calls(&h.online); !slices.Equal(got, []string{res.ID}) {
		t.Fatalf("OnOnline calls = %v, want [%s]", got, res.ID)
	}
	if fs := h.mustFolders(t); len(fs) != 1 {
		t.Fatalf("folders = %d, want 1", len(fs))
	}
}

// TestAdd_RenamedFolderBeforeAcquire: a folder renamed on disk and added
// under its new name before anything acquired it is still one row, and the
// new location is recorded.
func TestAdd_RenamedFolderBeforeAcquire(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	renamed := filepath.Join(h.root, "Trip 2026")
	if err := os.Rename(trip, renamed); err != nil {
		t.Fatal(err)
	}
	h.bm.Move(trip, renamed)

	got := h.mustAdd(t, renamed)
	if want := (AddResult{ID: res.ID, Name: "Trip 2026", Existing: true, Relation: "same"}); got != want {
		t.Fatalf("Add(renamed) = %+v, want %+v", got, want)
	}
	fs := h.mustFolders(t)
	if len(fs) != 1 || fs[0].Path != renamed || fs[0].Name != "Trip 2026" {
		t.Fatalf("folders = %+v, want one row at %s", fs, renamed)
	}
	if got := h.libraryEvents(t, res.ID); !slices.Equal(got, []string{"added", "moved"}) {
		t.Fatalf("events = %v, want [added moved]", got)
	}
	got = h.mustAdd(t, h.mkdir(t, "Trip 2026/Day 1"))
	if !got.Existing || got.Relation != "inside" || got.ID != res.ID {
		t.Fatalf("Add(renamed/Day 1) = %+v, want the existing folder, inside", got)
	}
}

// TestAdd_NewFolderAtRenamedFolderOldPath: a registered folder renamed away
// frees its old path. A new folder created there is a new row on the first
// Add, and the renamed folder's row carries its new location.
func TestAdd_NewFolderAtRenamedFolderOldPath(t *testing.T) {
	h := newHarness(t)
	imp := h.mkdir(t, "Import")
	old := h.mustAdd(t, imp)
	renamed := filepath.Join(h.root, "Italy 2026")
	if err := os.Rename(imp, renamed); err != nil {
		t.Fatal(err)
	}
	h.bm.Move(imp, renamed)
	h.mkdir(t, "Import")

	got := h.mustAdd(t, imp)
	if got.Existing || got.Relation != "" || got.ID == old.ID || got.Name != "Import" {
		t.Fatalf("Add(new Import) = %+v, want a new folder", got)
	}
	f, err := h.lib.Folder(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != renamed || f.Name != "Italy 2026" {
		t.Fatalf("renamed folder = {%s %s}, want {%s Italy 2026}", f.Path, f.Name, renamed)
	}
	nf, err := h.lib.Folder(got.ID)
	if err != nil || nf.Path != imp {
		t.Fatalf("new folder = %q, %v; want it at %s", nf.Path, err, imp)
	}
	if fs := h.mustFolders(t); len(fs) != 2 {
		t.Fatalf("folders = %d, want 2", len(fs))
	}
}

// TestAdd_UnresolvableBookmarkComparedByStoredPath: a bookmark that does not
// resolve still keeps its stored path from being added twice.
func TestAdd_UnresolvableBookmarkComparedByStoredPath(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	h.bm.Delete(trip)
	got := h.mustAdd(t, trip)
	if !got.Existing || got.Relation != "same" || got.ID != res.ID {
		t.Fatalf("Add = %+v, want the existing folder", got)
	}
	if fs := h.mustFolders(t); len(fs) != 1 || fs[0].Path != trip {
		t.Fatalf("folders = %+v, want one row at %s", fs, trip)
	}
}

// syncBuffer is a bytes.Buffer safe for concurrent writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs sends slog's default logger to a buffer for the test.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestMoveInsideAnotherFolder_KeepsBothRows documents the allowed overlap:
// a folder the user moves inside another Library folder is followed (spec
// §4.1), both rows stay, and the overlap is logged.
func TestMoveInsideAnotherFolder_KeepsBothRows(t *testing.T) {
	for _, via := range []string{"Acquire", "Add"} {
		t.Run(via, func(t *testing.T) {
			logs := captureLogs(t)
			h := newHarness(t)
			a := h.mkdir(t, "A")
			b := h.mkdir(t, "B")
			resA := h.mustAdd(t, a)
			resB := h.mustAdd(t, b)
			moved := filepath.Join(b, "A")
			if err := os.Rename(a, moved); err != nil {
				t.Fatal(err)
			}
			h.bm.Move(a, moved)

			switch via {
			case "Acquire":
				_, release, err := h.lib.Acquire(resA.ID)
				if err != nil {
					t.Fatalf("Acquire: %v", err)
				}
				release()
			case "Add":
				h.mustAdd(t, h.mkdir(t, "C"))
			}

			fa, err := h.lib.Folder(resA.ID)
			if err != nil || fa.Path != moved {
				t.Fatalf("A = %q, %v; want it at %s", fa.Path, err, moved)
			}
			fb, err := h.lib.Folder(resB.ID)
			if err != nil || fb.Path != b {
				t.Fatalf("B = %q, %v; want it unchanged at %s", fb.Path, err, b)
			}
			want := 2 // A and B both stay
			if via == "Add" {
				want = 3 // plus C
			}
			if fs := h.mustFolders(t); len(fs) != want {
				t.Fatalf("folders = %d, want %d", len(fs), want)
			}
			out := logs.String()
			if !strings.Contains(out, "overlaps") || !strings.Contains(out, resA.ID) || !strings.Contains(out, resB.ID) {
				t.Fatalf("log = %q, want an overlap warning naming both folders", out)
			}
		})
	}
}

// TestRelocation_RaceWithAdd: Acquire recording a move and Add of the new
// location run concurrently; the folder stays one row, recorded once.
func TestRelocation_RaceWithAdd(t *testing.T) {
	for range 25 {
		h := newHarness(t)
		trip := h.mkdir(t, "Trip")
		res := h.mustAdd(t, trip)
		moved := filepath.Join(h.root, "Moved")
		if err := os.Rename(trip, moved); err != nil {
			t.Fatal(err)
		}
		h.bm.Move(trip, moved)
		day := h.mkdir(t, "Moved/Day 1")

		var wg sync.WaitGroup
		results := make([]AddResult, 2)
		errs := make([]error, 3)
		wg.Go(func() {
			_, release, err := h.lib.Acquire(res.ID)
			if err == nil {
				release()
			}
			errs[0] = err
		})
		wg.Go(func() { results[0], errs[1] = h.lib.Add(moved) })
		wg.Go(func() { results[1], errs[2] = h.lib.Add(day) })
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if results[0] != (AddResult{ID: res.ID, Name: "Moved", Existing: true, Relation: "same"}) {
			t.Fatalf("Add(moved) = %+v", results[0])
		}
		if !results[1].Existing || results[1].ID != res.ID || results[1].Relation != "inside" {
			t.Fatalf("Add(moved/Day 1) = %+v", results[1])
		}
		fs := h.mustFolders(t)
		if len(fs) != 1 || fs[0].Path != moved {
			t.Fatalf("folders = %+v, want one row at %s", fs, moved)
		}
		if got := h.libraryEvents(t, res.ID); !slices.Equal(got, []string{"added", "moved"}) {
			t.Fatalf("events = %v, want [added moved]", got)
		}
	}
}

// Location resolves the folder's bookmark without recording anything and
// without starting access: it follows a move the Library has not recorded.
func TestLocation_ResolvesWithoutRecording(t *testing.T) {
	h := newHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)

	if got, err := h.lib.Location(res.ID); err != nil || got != trip {
		t.Fatalf("Location = %q, %v; want %q", got, err, trip)
	}
	moved := filepath.Join(filepath.Dir(trip), "Trip moved")
	h.bm.Move(trip, moved)
	if got, err := h.lib.Location(res.ID); err != nil || got != moved {
		t.Fatalf("Location after a move = %q, %v; want %q", got, err, moved)
	}
	f, err := h.lib.Folder(res.ID)
	if err != nil || f.Path != trip {
		t.Fatalf("recorded path = %q, %v; want it unchanged (%q)", f.Path, err, trip)
	}
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("Location started access: %v", got)
	}

	h.bm.Delete(moved)
	if _, err := h.lib.Location(res.ID); !errors.Is(err, platform.ErrGone) {
		t.Fatalf("Location of a deleted folder = %v; want platform.ErrGone", err)
	}
	if _, err := h.lib.Location("0000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Location of an unknown folder = %v; want ErrNotFound", err)
	}
}

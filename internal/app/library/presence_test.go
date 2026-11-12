package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// plugBookmarks wraps the bookmark fake so that a bookmark whose target lies
// at or under an unplugged path fails with platform.ErrGone — as a real
// bookmark does while its volume is not mounted — until the path is plugged
// back. It counts Resolve calls.
type plugBookmarks struct {
	*platformtest.Bookmarks

	mu          sync.Mutex
	unplugged   []string
	resolves    int
	unusable    bool // every Resolve fails with platform.ErrUnusable
	failRefresh bool // every Refresh fails
}

func (p *plugBookmarks) setUnusable(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unusable = v
}

func (p *plugBookmarks) setFailRefresh(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failRefresh = v
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

func (p *plugBookmarks) resolveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resolves
}

// gone reports whether path lies at or under an unplugged path.
func (p *plugBookmarks) gone(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, u := range p.unplugged {
		if r := relation(u, path); r == relSame || r == relInside {
			return fmt.Errorf("plug: %s is not mounted: %w", path, platform.ErrGone)
		}
	}
	return nil
}

func (p *plugBookmarks) Resolve(data []byte) (string, bool, error) {
	p.mu.Lock()
	p.resolves++
	unusable := p.unusable
	p.mu.Unlock()
	if unusable {
		return "", false, fmt.Errorf("plug: resolve: %w", platform.ErrUnusable)
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
	p.mu.Lock()
	fail := p.failRefresh
	p.mu.Unlock()
	if fail {
		return nil, errors.New("plug: refresh refused")
	}
	path, _, err := p.Bookmarks.Resolve(data)
	if err == nil {
		err = p.gone(path)
	}
	if err != nil {
		return nil, err
	}
	return p.Bookmarks.Refresh(data)
}

// presenceHarness is a harness whose Library resolves bookmarks through a
// plugBookmarks.
type presenceHarness struct {
	*harness
	plug *plugBookmarks
}

func newPresenceHarness(t *testing.T) *presenceHarness {
	t.Helper()
	h := newHarness(t)
	p := &plugBookmarks{Bookmarks: h.bm}
	h.lib.bookmarks = p
	return &presenceHarness{harness: h, plug: p}
}

// volume makes the directory name under h.root and registers it as a mounted
// volume with this uuid.
func (h *presenceHarness) volume(t *testing.T, name, uuid string) string {
	t.Helper()
	p := h.mkdir(t, name)
	h.vols.Add(p, platform.VolumeInfo{UUID: uuid, Name: name, Removable: true})
	return p
}

func (h *presenceHarness) check(t *testing.T) PresenceResult {
	t.Helper()
	res, err := h.lib.CheckPresence(context.Background())
	if err != nil {
		t.Fatalf("CheckPresence: %v", err)
	}
	return res
}

func (h *presenceHarness) folder(t *testing.T, id string) Folder {
	t.Helper()
	f, err := h.lib.Folder(id)
	if err != nil {
		t.Fatalf("Folder(%s): %v", id, err)
	}
	return f
}

func (h *presenceHarness) wantGone(t *testing.T, id string) {
	t.Helper()
	if _, err := h.lib.Folder(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Folder(%s) = %v, want ErrNotFound (row dropped)", id, err)
	}
}

func (h *presenceHarness) wantEvents(t *testing.T, id string, want ...string) {
	t.Helper()
	if got := h.libraryEvents(t, id); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func wantIDs(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// wantResult compares every list of a PresenceResult; nil and empty are equal.
func wantResult(t *testing.T, got, want PresenceResult) {
	t.Helper()
	wantIDs(t, "Removed", got.Removed, want.Removed...)
	wantIDs(t, "Offline", got.Offline, want.Offline...)
	wantIDs(t, "Missing", got.Missing, want.Missing...)
	wantIDs(t, "Online", got.Online, want.Online...)
	wantIDs(t, "Moved", got.Moved, want.Moved...)
	wantIDs(t, "AccessLost", got.AccessLost, want.AccessLost...)
}

func TestPresence_VerifiedAbsentOnMountedVolumeIsRemoved(t *testing.T) {
	h := newPresenceHarness(t)
	card := h.volume(t, "Card", "UUID-CARD")
	trip := h.mkdir(t, "Card/Trip")
	res := h.mustAdd(t, trip)
	if err := os.MkdirAll(filepath.Join(h.previews, res.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(trip); err != nil {
		t.Fatal(err)
	}
	h.bm.Delete(trip)
	h.vols.SetAbsent(trip, "UUID-CARD", true)

	got := h.check(t)
	wantResult(t, got, PresenceResult{Removed: []string{res.ID}})
	h.wantGone(t, res.ID)
	h.wantEvents(t, res.ID, "added", "gone")
	ev := h.rec.Events(events.LibraryChanged)[1].(events.LibraryChangedEvent)
	if ev.Name != "Trip" {
		t.Fatalf("gone event name = %q, want Trip", ev.Name)
	}
	wantIDs(t, "OnRemoved", h.calls(&h.removed), res.ID)
	wantIDs(t, "OnOffline", h.calls(&h.offline))
	if _, err := os.Stat(filepath.Join(h.previews, res.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previews directory of a gone folder: %v, want it removed", err)
	}
	if _, err := os.Stat(card); err != nil {
		t.Fatalf("the volume's directory was touched: %v", err)
	}
}

func TestPresence_VolumeMountedAtAnotherPathKeepsRow(t *testing.T) {
	h := newPresenceHarness(t)
	h.volume(t, "Card", "UUID-CARD")
	trip := h.mkdir(t, "Card/Trip")
	res := h.mustAdd(t, trip)

	// The card comes back under another mount point; the old path is now a
	// path on the boot volume where nothing exists.
	h.vols.Unmount("UUID-CARD")
	h.volume(t, "Card 1", "UUID-CARD")
	if err := os.RemoveAll(filepath.Join(h.root, "Card")); err != nil {
		t.Fatal(err)
	}
	h.bm.Delete(trip)
	h.vols.SetAbsent(trip, "UUID-SYSTEM", true) // absent, but on another volume

	for range 2 {
		h.check(t)
	}
	f := h.folder(t, res.ID)
	if f.Presence != "missing" {
		t.Fatalf("presence = %q, want missing", f.Presence)
	}
	h.wantEvents(t, res.ID, "added", "offline")
	wantIDs(t, "OnRemoved", h.calls(&h.removed))
	wantIDs(t, "OnOffline", h.calls(&h.offline), res.ID)
}

func TestPresence_UnresolvedButNotVerifiedIsMissing(t *testing.T) {
	h := newPresenceHarness(t)
	h.volume(t, "Card", "UUID-CARD")
	trip := h.mkdir(t, "Card/Trip")
	res := h.mustAdd(t, trip)
	h.plug.unplug(trip) // ErrGone, the volume still mounted, AbsentOn false

	got := h.check(t)
	wantResult(t, got, PresenceResult{Missing: []string{res.ID}})
	if f := h.folder(t, res.ID); f.Presence != "missing" {
		t.Fatalf("presence = %q, want missing", f.Presence)
	}
	if MissingMessage != "This folder can no longer be found." {
		t.Fatalf("MissingMessage = %q", MissingMessage)
	}
	wantIDs(t, "OnOffline", h.calls(&h.offline), res.ID)

	// Still missing: no new transition.
	wantResult(t, h.check(t), PresenceResult{})
	wantIDs(t, "OnOffline", h.calls(&h.offline), res.ID)

	h.plug.plug(trip)
	wantResult(t, h.check(t), PresenceResult{Online: []string{res.ID}})
	if f := h.folder(t, res.ID); f.Presence != "present" {
		t.Fatalf("presence = %q, want present", f.Presence)
	}
	h.wantEvents(t, res.ID, "added", "offline", "online")
	wantIDs(t, "OnOnline", h.calls(&h.online), res.ID)
	wantIDs(t, "OnRemoved", h.calls(&h.removed))
}

// trash moves the folder at path into a .Trash directory under h.root, as
// the Finder does, and returns its new path. The bookmark follows it and is
// stale, as measured by spike S1.
func (h *presenceHarness) trash(t *testing.T, path string) string {
	t.Helper()
	trashed := filepath.Join(h.mkdir(t, ".Trash"), filepath.Base(path))
	if err := os.Rename(path, trashed); err != nil {
		t.Fatal(err)
	}
	h.bm.Move(path, trashed)
	h.bm.MarkStale(trashed)
	return trashed
}

func TestPresence_VerifiedTrashIsRemoved(t *testing.T) {
	h := newPresenceHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	trashed := h.trash(t, trip)
	h.vols.Trash(trashed)

	wantResult(t, h.check(t), PresenceResult{Removed: []string{res.ID}})
	h.wantGone(t, res.ID)
	h.wantEvents(t, res.ID, "added", "gone")
	wantIDs(t, "OnRemoved", h.calls(&h.removed), res.ID)
	if h.bm.Refreshed() != 0 {
		t.Fatal("the bookmark of a trashed folder was refreshed")
	}
	if _, err := os.Stat(trashed); err != nil {
		t.Fatalf("the trashed folder was touched: %v", err)
	}
}

func TestPresence_TrashCheckRetriedUnderLease(t *testing.T) {
	h := newPresenceHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	trashed := h.trash(t, trip)
	h.vols.Trash(trashed)
	h.vols.TrashNeedsAccess(trashed, h.bm)

	wantResult(t, h.check(t), PresenceResult{Removed: []string{res.ID}})
	h.wantGone(t, res.ID)
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("active leases = %v, want the trash-check lease stopped", got)
	}
}

func TestPresence_TrashUnknownKeepsRow(t *testing.T) {
	logs := captureLogs(t)
	h := newPresenceHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	h.vols.TrashUnknown(trip)

	for range 2 {
		wantResult(t, h.check(t), PresenceResult{})
	}
	if f := h.folder(t, res.ID); f.Presence != "present" {
		t.Fatalf("presence = %q, want present", f.Presence)
	}
	h.wantEvents(t, res.ID, "added")
	wantIDs(t, "OnRemoved", h.calls(&h.removed))
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("active leases = %v, want none", got)
	}
	out := logs.String()
	if n := strings.Count(out, "level=WARN"); n != 1 || !strings.Contains(out, "Trash") || !strings.Contains(out, res.ID) {
		t.Fatalf("log = %q, want one Trash warning naming the folder across two passes", out)
	}
}

func TestPresence_PendingPresentFolderIsResumedEveryPass(t *testing.T) {
	h := newPresenceHarness(t)
	pending := h.mustAdd(t, h.mkdir(t, "Pending"))
	idle := h.mustAdd(t, h.mkdir(t, "Idle"))
	if _, err := h.lib.EnqueueJob(pending.ID, "full"); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		wantResult(t, h.check(t), PresenceResult{})
	}
	wantIDs(t, "OnOnline", h.calls(&h.online), pending.ID, pending.ID)
	if slices.Contains(h.calls(&h.online), idle.ID) {
		t.Fatal("OnOnline called for a folder without pending work")
	}
	// Online lists transitions only; the folder was present all along.
	h.wantEventsFor(t, pending.ID, "added")
}

// wantEventsFor is wantEvents for a harness with several folders: only the
// events of id are compared.
func (h *presenceHarness) wantEventsFor(t *testing.T, id string, want ...string) {
	t.Helper()
	var got []string
	for _, e := range h.rec.Events(events.LibraryChanged) {
		if ev := e.(events.LibraryChangedEvent); ev.FolderID == id {
			got = append(got, ev.Reason)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events of %s = %v, want %v", id, got, want)
	}
}

func TestPresence_PendingFolderNotPresentIsNotResumed(t *testing.T) {
	h := newPresenceHarness(t)
	h.volume(t, "Card", "UUID-CARD")
	trip := h.mkdir(t, "Card/Trip")
	res := h.mustAdd(t, trip)
	if _, err := h.lib.EnqueueJob(res.ID, "full"); err != nil {
		t.Fatal(err)
	}
	h.vols.Unmount("UUID-CARD")
	h.plug.unplug(trip)
	for range 2 {
		h.check(t)
	}
	wantIDs(t, "OnOnline", h.calls(&h.online))

	// The drive comes back: one OnOnline for the transition, then one per
	// pass while the job is pending.
	h.vols.Mount("UUID-CARD")
	h.plug.plug(trip)
	wantResult(t, h.check(t), PresenceResult{Online: []string{res.ID}})
	wantIDs(t, "OnOnline", h.calls(&h.online), res.ID)
	h.check(t)
	wantIDs(t, "OnOnline", h.calls(&h.online), res.ID, res.ID)
}

func TestCheckFolder_OnlyThatFolder(t *testing.T) {
	h := newPresenceHarness(t)
	a := h.mkdir(t, "A")
	b := h.mkdir(t, "B")
	resA := h.mustAdd(t, a)
	resB := h.mustAdd(t, b)
	for _, p := range []string{a, b} {
		h.plug.unplug(p) // missing, not verified absent
	}

	got, err := h.lib.CheckFolder(context.Background(), resA.ID)
	if err != nil {
		t.Fatalf("CheckFolder: %v", err)
	}
	wantResult(t, got, PresenceResult{Missing: []string{resA.ID}})
	if f := h.folder(t, resA.ID); f.Presence != "missing" {
		t.Fatalf("A presence = %q, want missing", f.Presence)
	}
	if f := h.folder(t, resB.ID); f.Presence != "present" {
		t.Fatalf("B presence = %q, want present (not checked)", f.Presence)
	}
	wantIDs(t, "OnOffline", h.calls(&h.offline), resA.ID)

	if _, err := h.lib.CheckFolder(context.Background(), "0000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CheckFolder(unknown) = %v, want ErrNotFound", err)
	}
}

func TestCheckPresence_StopsWhenCancelled(t *testing.T) {
	h := newPresenceHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	h.plug.unplug(trip)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.lib.CheckPresence(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckPresence(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := h.lib.CheckFolder(ctx, res.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckFolder(cancelled) = %v, want context.Canceled", err)
	}
	if f := h.folder(t, res.ID); f.Presence != "present" {
		t.Fatalf("presence = %q, want unchanged", f.Presence)
	}
}

func TestPresence_UnmountedVolumeIsOffline(t *testing.T) {
	h := newPresenceHarness(t)
	h.volume(t, "Card", "UUID-CARD")
	trip := h.mkdir(t, "Card/Trip")
	res := h.mustAdd(t, trip)
	h.vols.Unmount("UUID-CARD")
	h.bm.Delete(trip)                         // spike S1: same error as a deletion
	h.vols.SetAbsent(trip, "UUID-CARD", true) // never consulted while unmounted

	wantResult(t, h.check(t), PresenceResult{Offline: []string{res.ID}})
	wantResult(t, h.check(t), PresenceResult{})
	if f := h.folder(t, res.ID); f.Presence != "offline" {
		t.Fatalf("presence = %q, want offline", f.Presence)
	}
	h.wantEvents(t, res.ID, "added", "offline")
	wantIDs(t, "OnOffline", h.calls(&h.offline), res.ID)
	wantIDs(t, "OnRemoved", h.calls(&h.removed))
}

func TestPresence_NoVolumeIdentityIsOfflineNotRemoved(t *testing.T) {
	h := newPresenceHarness(t)
	h.volume(t, "Share", "") // a network share: no volume UUID
	trip := h.mkdir(t, "Share/Trip")
	res := h.mustAdd(t, trip)
	if f := h.folder(t, res.ID); f.VolumeUUID != "" {
		t.Fatalf("volume_uuid = %q, want empty", f.VolumeUUID)
	}
	h.bm.Delete(trip)
	h.vols.SetAbsent(trip, "", true)

	wantResult(t, h.check(t), PresenceResult{Offline: []string{res.ID}})
	if f := h.folder(t, res.ID); f.Presence != "offline" {
		t.Fatalf("presence = %q, want offline", f.Presence)
	}
	wantIDs(t, "OnRemoved", h.calls(&h.removed))
}

func TestPresence_RemountComesBack(t *testing.T) {
	h := newPresenceHarness(t)
	h.volume(t, "Card", "UUID-CARD")
	trip := h.mkdir(t, "Card/Trip")
	res := h.mustAdd(t, trip)
	h.vols.Unmount("UUID-CARD")
	h.plug.unplug(trip)
	wantResult(t, h.check(t), PresenceResult{Offline: []string{res.ID}})

	h.vols.Mount("UUID-CARD")
	h.plug.plug(trip)
	wantResult(t, h.check(t), PresenceResult{Online: []string{res.ID}})
	if f := h.folder(t, res.ID); f.Presence != "present" {
		t.Fatalf("presence = %q, want present", f.Presence)
	}
	h.wantEvents(t, res.ID, "added", "offline", "online")
	wantIDs(t, "OnOnline", h.calls(&h.online), res.ID)
}

func TestPresence_RemountAtAnotherMountPointIsFollowed(t *testing.T) {
	h := newPresenceHarness(t)
	card := h.volume(t, "Card", "UUID-CARD")
	trip := h.mkdir(t, "Card/Trip")
	res := h.mustAdd(t, trip)
	h.vols.Unmount("UUID-CARD")
	h.plug.unplug(trip)
	wantResult(t, h.check(t), PresenceResult{Offline: []string{res.ID}})

	// Remounted at another mount point: resolves to the new path, not stale.
	h.plug.plug(trip)
	card1 := filepath.Join(h.root, "Card 1")
	if err := os.Rename(card, card1); err != nil {
		t.Fatal(err)
	}
	h.vols.Add(card1, platform.VolumeInfo{UUID: "UUID-CARD", Name: "Card"})
	h.bm.Move(trip, filepath.Join(card1, "Trip"))

	wantResult(t, h.check(t), PresenceResult{Online: []string{res.ID}, Moved: []string{res.ID}})
	f := h.folder(t, res.ID)
	if f.Path != filepath.Join(card1, "Trip") || f.Presence != "present" || f.VolumeUUID != "UUID-CARD" {
		t.Fatalf("row = {%s %s %s}, want present at the new mount point", f.Path, f.Presence, f.VolumeUUID)
	}
	h.wantEvents(t, res.ID, "added", "offline", "moved", "online")
	if h.bm.Refreshed() != 0 {
		t.Fatal("a bookmark that is not stale was refreshed")
	}
}

func TestPresence_RenameIsFollowed(t *testing.T) {
	h := newPresenceHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	renamed := filepath.Join(h.root, "Trip 2026")
	if err := os.Rename(trip, renamed); err != nil {
		t.Fatal(err)
	}
	h.bm.Move(trip, renamed)
	h.bm.MarkStale(renamed)

	wantResult(t, h.check(t), PresenceResult{Moved: []string{res.ID}})
	f := h.folder(t, res.ID)
	if f.Path != renamed || f.Name != "Trip 2026" || f.Presence != "present" {
		t.Fatalf("row = {%s %s %s}, want present at the new path", f.Path, f.Name, f.Presence)
	}
	h.wantEvents(t, res.ID, "added", "moved")
	ev := h.rec.Events(events.LibraryChanged)[1].(events.LibraryChangedEvent)
	if ev.Name != "Trip 2026" {
		t.Fatalf("moved event name = %q, want the new name", ev.Name)
	}
	if h.bm.Refreshed() != 1 {
		t.Fatalf("Refreshed = %d, want the stale bookmark refreshed", h.bm.Refreshed())
	}
	wantIDs(t, "OnOffline", h.calls(&h.offline))
	wantIDs(t, "OnRemoved", h.calls(&h.removed))

	// Followed: the next pass has nothing to do.
	wantResult(t, h.check(t), PresenceResult{})
}

func TestPresence_StaleBookmarkIsRefreshed(t *testing.T) {
	h := newPresenceHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	before := h.folder(t, res.ID).Bookmark
	h.bm.MarkStale(trip)

	wantResult(t, h.check(t), PresenceResult{})
	if h.bm.Refreshed() != 1 {
		t.Fatalf("Refreshed = %d, want 1", h.bm.Refreshed())
	}
	after := h.folder(t, res.ID).Bookmark
	if bytes.Equal(before, after) {
		t.Fatal("stored bookmark bytes did not change")
	}
	if p, stale, err := h.bm.Resolve(after); err != nil || p != trip || stale {
		t.Fatalf("new bookmark resolves to %q, stale %v, %v; want %q, fresh", p, stale, err, trip)
	}
	h.check(t)
	if h.bm.Refreshed() != 1 {
		t.Fatalf("Refreshed = %d after a second pass, want still 1", h.bm.Refreshed())
	}
	h.wantEvents(t, res.ID, "added")
}

func TestPresence_StaleRefreshFailureKeepsOldBytes(t *testing.T) {
	logs := captureLogs(t)
	h := newPresenceHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	before := h.folder(t, res.ID).Bookmark
	h.bm.MarkStale(trip)
	h.plug.setFailRefresh(true)

	refreshWarnings := func() int { return strings.Count(logs.String(), "refresh the stale bookmark") }
	for range 2 {
		wantResult(t, h.check(t), PresenceResult{})
	}
	f := h.folder(t, res.ID)
	if !bytes.Equal(before, f.Bookmark) {
		t.Fatal("stored bookmark bytes changed after a failed refresh")
	}
	if f.Presence != "present" {
		t.Fatalf("presence = %q, want present", f.Presence)
	}
	if h.bm.Refreshed() != 0 {
		t.Fatalf("Refreshed = %d, want 0", h.bm.Refreshed())
	}
	if n := refreshWarnings(); n != 1 || !strings.Contains(logs.String(), res.ID) {
		t.Fatalf("log = %q, want one refresh warning naming the folder across two passes", logs.String())
	}

	// It succeeds; a later failure is a new episode and is logged again.
	h.plug.setFailRefresh(false)
	h.check(t)
	if h.bm.Refreshed() != 1 {
		t.Fatalf("Refreshed = %d, want 1", h.bm.Refreshed())
	}
	h.bm.MarkStale(trip)
	h.plug.setFailRefresh(true)
	for range 2 {
		h.check(t)
	}
	if n := refreshWarnings(); n != 2 {
		t.Fatalf("refresh warnings = %d, want 2 (one per failing episode)", n)
	}
}

// TestPresence_UnusableBookmarkIsAccessLost pins Review Focus 2.
func TestPresence_UnusableBookmarkIsAccessLost(t *testing.T) {
	h := newPresenceHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	h.bm.MakeUnusable(trip)
	h.vols.SetAbsent(trip, "UUID-SYSTEM", true) // never consulted for ErrUnusable

	wantResult(t, h.check(t), PresenceResult{AccessLost: []string{res.ID}})
	wantResult(t, h.check(t), PresenceResult{})
	if f := h.folder(t, res.ID); f.Presence != "access_lost" {
		t.Fatalf("presence = %q, want access_lost", f.Presence)
	}
	h.wantEvents(t, res.ID, "added", "offline")
	wantIDs(t, "OnOffline", h.calls(&h.offline), res.ID)
	wantIDs(t, "OnRemoved", h.calls(&h.removed))

	// Adding the folder again restores it, and presence leaves it present.
	if again := h.mustAdd(t, trip); again.ID != res.ID || again.Existing {
		t.Fatalf("Add again = %+v, want the bookmark replaced", again)
	}
	wantResult(t, h.check(t), PresenceResult{})
	if f := h.folder(t, res.ID); f.Presence != "present" {
		t.Fatalf("presence = %q, want present", f.Presence)
	}
}

// TestPresence_AccessLostResolvingIsNotOnline: a folder whose bookmark
// resolves but whose access was lost stays access_lost; only adding it again
// or a successful Acquire clears it.
func TestPresence_AccessLostResolvingIsNotOnline(t *testing.T) {
	h := newPresenceHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	if _, err := h.lib.EnqueueJob(res.ID, "full"); err != nil {
		t.Fatal(err)
	}
	if err := h.db.SetPresence(res.ID, "access_lost"); err != nil {
		t.Fatal(err)
	}
	wantResult(t, h.check(t), PresenceResult{})
	if f := h.folder(t, res.ID); f.Presence != "access_lost" {
		t.Fatalf("presence = %q, want access_lost", f.Presence)
	}
	wantIDs(t, "OnOnline", h.calls(&h.online))
}

// TestPresence_UnpluggedWhileAcquired pins Review Focus 1.
func TestPresence_UnpluggedWhileAcquired(t *testing.T) {
	h := newPresenceHarness(t)
	h.volume(t, "Card", "UUID-CARD")
	trip := h.mkdir(t, "Card/Trip")
	res := h.mustAdd(t, trip)
	if _, err := h.lib.EnqueueJob(res.ID, "full"); err != nil {
		t.Fatal(err)
	}
	_, release, err := h.lib.Acquire(res.ID)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	h.vols.Unmount("UUID-CARD")
	h.plug.unplug(trip)

	wantResult(t, h.check(t), PresenceResult{Offline: []string{res.ID}})
	wantResult(t, h.check(t), PresenceResult{})
	wantIDs(t, "OnOffline", h.calls(&h.offline), res.ID)
	wantIDs(t, "OnOnline", h.calls(&h.online))
	f := h.folder(t, res.ID)
	if f.Presence != "offline" || !f.JobPending {
		t.Fatalf("row = {%s pending=%v}, want offline with its job pending", f.Presence, f.JobPending)
	}
	release()
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("active = %v, want none after release", got)
	}

	// The drive returns: the pending job is resumed.
	h.vols.Mount("UUID-CARD")
	h.plug.plug(trip)
	wantResult(t, h.check(t), PresenceResult{Online: []string{res.ID}})
	wantIDs(t, "OnOnline", h.calls(&h.online), res.ID)
}

func TestPresence_RenamedWhileAcquired(t *testing.T) {
	for _, inUse := range []bool{true, false} {
		t.Run(fmt.Sprintf("inUse=%v", inUse), func(t *testing.T) {
			h := newPresenceHarness(t)
			trip := h.mkdir(t, "Trip")
			res := h.mustAdd(t, trip)
			if _, err := h.lib.EnqueueJob(res.ID, "full"); err != nil {
				t.Fatal(err)
			}
			if inUse {
				_, release, err := h.lib.Acquire(res.ID)
				if err != nil {
					t.Fatalf("Acquire: %v", err)
				}
				defer release()
			}
			renamed := filepath.Join(h.root, "Renamed")
			if err := os.Rename(trip, renamed); err != nil {
				t.Fatal(err)
			}
			h.bm.Move(trip, renamed)
			h.bm.MarkStale(renamed)

			wantResult(t, h.check(t), PresenceResult{Moved: []string{res.ID}})
			if inUse {
				wantIDs(t, "OnOffline", h.calls(&h.offline), res.ID)
			} else {
				wantIDs(t, "OnOffline", h.calls(&h.offline))
			}
			// The folder is present with its job pending: it is resumed at
			// its new location.
			wantIDs(t, "OnOnline", h.calls(&h.online), res.ID)
			if f := h.folder(t, res.ID); f.Path != renamed {
				t.Fatalf("path = %q, want %q", f.Path, renamed)
			}
		})
	}
}

// TestPresence_CallbacksMayReenter: callbacks run outside the Library's
// locks, so the queue may acquire, check or remove the folder from them.
func TestPresence_CallbacksMayReenter(t *testing.T) {
	h := newPresenceHarness(t)
	trip := h.mkdir(t, "Trip")
	res := h.mustAdd(t, trip)
	gone := h.mkdir(t, "Gone")
	resGone := h.mustAdd(t, gone)
	if _, err := h.lib.EnqueueJob(res.ID, "full"); err != nil {
		t.Fatal(err)
	}
	h.plug.unplug(gone)
	var reentered []error
	var mu sync.Mutex
	note := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		reentered = append(reentered, err)
	}
	h.lib.onOnline = func(id string) {
		_, release, err := h.lib.Acquire(id)
		if err == nil {
			release()
		}
		note(err)
	}
	h.lib.onOffline = func(id string) {
		_, err := h.lib.CheckFolder(context.Background(), id)
		note(err)
		note(h.lib.Remove(id))
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = h.lib.CheckPresence(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("CheckPresence deadlocked in a callback")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reentered) != 3 {
		t.Fatalf("re-entrant calls = %v, want 3", reentered)
	}
	for _, err := range reentered {
		if err != nil {
			t.Fatalf("re-entrant call: %v", err)
		}
	}
	h.wantGone(t, resGone.ID)
}

// TestPresence_ConcurrentWithAddAndAcquire runs presence passes while
// folders are added, acquired and moved; run with -race.
func TestPresence_ConcurrentWithAddAndAcquire(t *testing.T) {
	h := newPresenceHarness(t)
	h.volume(t, "Card", "UUID-CARD")
	base := h.mustAdd(t, h.mkdir(t, "Base"))
	card := h.mustAdd(t, h.mkdir(t, "Card/Trip"))
	if _, err := h.lib.EnqueueJob(base.ID, "full"); err != nil {
		t.Fatal(err)
	}
	h.lib.onOnline = func(id string) {
		if _, release, err := h.lib.Acquire(id); err == nil {
			release()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	wg.Go(func() {
		for range 30 {
			if _, err := h.lib.CheckPresence(ctx); err != nil {
				errs <- err
				return
			}
		}
	})
	wg.Go(func() {
		for range 30 {
			if _, err := h.lib.CheckFolder(ctx, card.ID); err != nil {
				errs <- err
				return
			}
		}
	})
	var added []string
	for i := range 10 {
		added = append(added, h.mkdir(t, fmt.Sprintf("Added %d", i)))
	}
	wg.Go(func() {
		for _, p := range added {
			if _, err := h.lib.Add(p); err != nil {
				errs <- err
				return
			}
		}
	})
	wg.Go(func() {
		for range 40 {
			_, release, err := h.lib.Acquire(base.ID)
			if err != nil {
				errs <- err
				return
			}
			release()
		}
	})
	wg.Go(func() {
		for i := range 10 {
			if i%2 == 0 {
				h.vols.Unmount("UUID-CARD")
				h.plug.unplug(filepath.Join(h.root, "Card"))
			} else {
				h.vols.Mount("UUID-CARD")
				h.plug.plug(filepath.Join(h.root, "Card"))
			}
		}
	})
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(60 * time.Second):
		t.Fatal("presence deadlocked with Add or Acquire")
	}
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// Settle and check the final state is consistent.
	h.vols.Mount("UUID-CARD")
	h.plug.plug(filepath.Join(h.root, "Card"))
	h.check(t)
	if fs := h.mustFolders(t); len(fs) != 12 {
		t.Fatalf("folders = %d, want 12 (nothing dropped)", len(fs))
	}
	if f := h.folder(t, card.ID); f.Presence != "present" {
		t.Fatalf("card presence = %q, want present", f.Presence)
	}
	if got := h.bm.Active(); len(got) != 0 {
		t.Fatalf("active = %v, want none", got)
	}
	last := ""
	for _, e := range h.rec.Events(events.LibraryChanged) {
		if ev := e.(events.LibraryChangedEvent); ev.FolderID == card.ID && (ev.Reason == "offline" || ev.Reason == "online") {
			last = ev.Reason
		}
	}
	if last != "" && last != "online" {
		t.Fatalf("last presence event of the card = %q, want online", last)
	}
}

func TestRunPresence_SkipsWhileHidden(t *testing.T) {
	h := newPresenceHarness(t)
	h.volume(t, "Card", "UUID-CARD")
	trip := h.mkdir(t, "Card/Trip")
	res := h.mustAdd(t, trip)
	h.vols.Unmount("UUID-CARD")
	h.plug.unplug(trip)
	resolvesBefore := h.plug.resolveCount()

	ticks := make(chan time.Time)
	var gotEvery time.Duration
	stopped := make(chan struct{})
	h.lib.newTicker = func(every time.Duration) (<-chan time.Time, func()) {
		gotEvery = every
		return ticks, func() { close(stopped) }
	}
	var mu sync.Mutex
	// The fourth, hidden tick proves the third was handled: the loop takes
	// a tick only after finishing the previous one.
	answers := []bool{false, false, true, false}
	asked := 0
	visible := func() bool {
		mu.Lock()
		defer mu.Unlock()
		v := answers[asked]
		asked++
		return v
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.lib.RunPresence(ctx, 30*time.Second, visible)
	}()
	for range answers {
		ticks <- time.Time{}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunPresence did not return after cancel")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("ticker not stopped")
	}

	if gotEvery != 30*time.Second {
		t.Fatalf("ticker interval = %v, want 30s", gotEvery)
	}
	mu.Lock()
	defer mu.Unlock()
	if asked != 4 {
		t.Fatalf("visible asked %d times, want once per tick", asked)
	}
	if n := h.plug.resolveCount() - resolvesBefore; n != 1 {
		t.Fatalf("bookmark resolved %d times, want once (two hidden ticks skipped)", n)
	}
	if f := h.folder(t, res.ID); f.Presence != "offline" {
		t.Fatalf("presence = %q, want offline after the visible tick", f.Presence)
	}
	wantIDs(t, "OnOffline", h.calls(&h.offline), res.ID)
}

// orderLog records OnOffline, OnOnline and OnRemoved calls in one sequence.
type orderLog struct {
	mu    sync.Mutex
	calls []string
}

func (o *orderLog) add(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, s)
}

func (o *orderLog) get() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.calls)
}

func (o *orderLog) install(l *Library) {
	l.onOffline = func(id string) { o.add("offline " + id) }
	l.onOnline = func(id string) { o.add("online " + id) }
	l.onRemoved = func(id string) { o.add("removed " + id) }
}

// TestPresence_NotificationsKeepMutationOrder: a check that recorded a
// change and has not notified yet must not deliver its notification after a
// later change of the same folder made by another check, Add or Acquire.
// The first check is held between recording and notifying.
func TestPresence_NotificationsKeepMutationOrder(t *testing.T) {
	cases := []struct {
		name  string
		lose  func(h *presenceHarness, trip string) // what the first check sees
		state string                                // the presence it records
		back  func(t *testing.T, h *presenceHarness, trip, id string)
	}{
		{
			name: "remount seen by another check",
			lose: func(h *presenceHarness, trip string) {
				h.vols.Unmount("UUID-CARD")
				h.plug.unplug(trip)
			},
			state: "offline",
			back: func(t *testing.T, h *presenceHarness, trip, id string) {
				h.vols.Mount("UUID-CARD")
				h.plug.plug(trip)
				if _, err := h.lib.CheckFolder(context.Background(), id); err != nil {
					t.Errorf("CheckFolder: %v", err)
				}
			},
		},
		{
			name:  "access restored by adding the folder again",
			lose:  func(h *presenceHarness, _ string) { h.plug.setUnusable(true) },
			state: "access_lost",
			back: func(t *testing.T, h *presenceHarness, trip, id string) {
				h.plug.setUnusable(false)
				if _, err := h.lib.Add(trip); err != nil {
					t.Errorf("Add: %v", err)
				}
			},
		},
		{
			name:  "access restored by Acquire",
			lose:  func(h *presenceHarness, _ string) { h.plug.setUnusable(true) },
			state: "access_lost",
			back: func(t *testing.T, h *presenceHarness, trip, id string) {
				h.plug.setUnusable(false)
				_, release, err := h.lib.Acquire(id)
				if err != nil {
					t.Errorf("Acquire: %v", err)
					return
				}
				release()
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newPresenceHarness(t)
			h.volume(t, "Card", "UUID-CARD")
			trip := h.mkdir(t, "Card/Trip")
			res := h.mustAdd(t, trip)
			var log orderLog
			log.install(h.lib)

			examined := make(chan struct{})
			proceed := make(chan struct{})
			var once sync.Once
			h.lib.afterExamine = func(string) {
				first := false
				once.Do(func() { first = true })
				if first {
					close(examined)
					<-proceed
				}
			}

			tc.lose(h, trip)
			firstDone := make(chan error, 1)
			go func() {
				_, err := h.lib.CheckFolder(context.Background(), res.ID)
				firstDone <- err
			}()
			select {
			case <-examined:
			case <-time.After(10 * time.Second):
				t.Fatal("the first check never recorded its outcome")
			}
			if f := h.folder(t, res.ID); f.Presence != tc.state {
				t.Fatalf("presence after the first check = %q, want %q", f.Presence, tc.state)
			}

			// The folder comes back while the first check has not notified.
			backDone := make(chan struct{})
			go func() {
				defer close(backDone)
				tc.back(t, h, trip, res.ID)
			}()
			select {
			case <-backDone:
			case <-time.After(10 * time.Second):
				close(proceed)
				t.Fatal("the recovery blocked on the held check")
			}
			close(proceed)
			select {
			case err := <-firstDone:
				if err != nil {
					t.Fatalf("first CheckFolder: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the first check did not finish")
			}

			want := []string{"offline " + res.ID, "online " + res.ID}
			if got := log.get(); !slices.Equal(got, want) {
				t.Fatalf("callbacks = %v, want %v (in the order the changes were recorded)", got, want)
			}
			h.wantEvents(t, res.ID, "added", "offline", "online")
			if f := h.folder(t, res.ID); f.Presence != "present" {
				t.Fatalf("presence = %q, want present", f.Presence)
			}
		})
	}
}

// TestNotifications_BlockedCallbackDoesNotBlockOthers: while one callback
// is running, a change recorded by another goroutine (here Acquire finding
// its access lost) returns without waiting, and its notification follows the
// running one.
func TestNotifications_BlockedCallbackDoesNotBlockOthers(t *testing.T) {
	h := newPresenceHarness(t)
	pending := h.mustAdd(t, h.mkdir(t, "Pending"))
	lost := h.mustAdd(t, h.mkdir(t, "Lost"))
	if _, err := h.lib.EnqueueJob(pending.ID, "full"); err != nil {
		t.Fatal(err)
	}
	var log orderLog
	log.install(h.lib)
	inCallback := make(chan struct{})
	proceed := make(chan struct{})
	h.lib.onOnline = func(id string) {
		log.add("online " + id)
		close(inCallback)
		<-proceed
	}

	checkDone := make(chan struct{})
	go func() {
		defer close(checkDone)
		_, _ = h.lib.CheckFolder(context.Background(), pending.ID)
	}()
	<-inCallback

	h.bm.MakeUnusable(filepath.Join(h.root, "Lost"))
	acquired := make(chan error, 1)
	go func() {
		_, _, err := h.lib.Acquire(lost.ID)
		acquired <- err
	}()
	select {
	case err := <-acquired:
		if !errors.Is(err, ErrAccessLost) {
			t.Errorf("Acquire = %v, want ErrAccessLost", err)
		}
	case <-time.After(10 * time.Second):
		close(proceed)
		t.Fatal("Acquire waited for another folder's callback")
	}
	if got := log.get(); !slices.Equal(got, []string{"online " + pending.ID}) {
		t.Fatalf("callbacks while one is running = %v, want only the running one", got)
	}
	close(proceed)
	<-checkDone
	want := []string{"online " + pending.ID, "offline " + lost.ID}
	if got := log.get(); !slices.Equal(got, want) {
		t.Fatalf("callbacks = %v, want %v", got, want)
	}
}

// dropByPresence trashes the preview env's folder and runs a presence check,
// which must drop it.
func (e *previewEnv) dropByPresence(t *testing.T) {
	t.Helper()
	trashed := filepath.Join(e.h.mkdir(t, ".Trash"), filepath.Base(e.root))
	if err := os.Rename(e.root, trashed); err != nil {
		t.Fatal(err)
	}
	e.h.bm.Move(e.root, trashed)
	e.h.vols.Trash(trashed)
	res, err := e.h.lib.CheckPresence(context.Background())
	if err != nil {
		t.Fatalf("CheckPresence: %v", err)
	}
	if !slices.Equal(res.Removed, []string{e.id}) {
		t.Fatalf("Removed = %v, want [%s]", res.Removed, e.id)
	}
}

// TestRefreshPreviews_FolderDroppedByPresenceMeanwhile is
// TestRefreshPreviews_FolderRemovedMeanwhile with the folder dropped by a
// presence check instead of Remove: the result is dropped the same way.
func TestRefreshPreviews_FolderDroppedByPresenceMeanwhile(t *testing.T) {
	e := newPreviewEnv(t)
	for n := range 3 {
		e.addFile(t, n, "thumb", "preview")
	}
	gate := make(chan struct{})
	gated := &platformtest.Stills{Gate: gate}
	done := make(chan error, 1)
	go func() { done <- e.refresh(context.Background(), gated) }()
	waitForCall(t, gated)

	e.dropByPresence(t)
	if _, err := os.Stat(e.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previews dir after the drop: %v, want it gone", err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("RefreshPreviews of a dropped folder = %v, want nil", err)
	}
	if _, err := os.Stat(e.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previews dir after the refresh: %v, want it not recreated", err)
	}
	if fs := e.h.mustFolders(t); len(fs) != 0 {
		t.Fatalf("folders = %d, want 0", len(fs))
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "gone"}) {
		t.Fatalf("events = %v, want [added gone]", got)
	}
}

// TestRefreshPreviews_RowDroppedByPresenceBeforePublish mirrors
// TestRefreshPreviews_RowRemovedBeforePublish for a presence drop.
func TestRefreshPreviews_RowDroppedByPresenceBeforePublish(t *testing.T) {
	e := newPreviewEnv(t)
	for n := range 3 {
		e.addFile(t, n, "thumb", "preview")
	}
	gate := make(chan struct{})
	gated := &platformtest.Stills{Gate: gate}
	done := make(chan error, 1)
	go func() { done <- e.refresh(context.Background(), gated) }()
	waitForCall(t, gated)

	e.h.lib.previewsDir = nil // the drop leaves the directory in place
	e.dropByPresence(t)
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("RefreshPreviews of a dropped folder = %v, want nil", err)
	}
	if got := snapshot(t, e.dir); len(got) != 0 {
		t.Fatalf("previews dir holds %d files, want none (temporaries discarded)", len(got))
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "gone"}) {
		t.Fatalf("events = %v, want [added gone]", got)
	}
}

// TestRefreshStats_DroppedByPresence mirrors TestRefreshStats_RemovedFolder.
func TestRefreshStats_DroppedByPresence(t *testing.T) {
	e := newPreviewEnv(t)
	e.addFile(t, 0, "thumb")
	e.dropByPresence(t)
	if err := e.h.lib.RefreshStats(e.id, e.st); err != nil {
		t.Fatalf("RefreshStats of a dropped folder = %v, want nil", err)
	}
	if fs := e.h.mustFolders(t); len(fs) != 0 {
		t.Fatalf("folders = %d, want 0 (row not recreated)", len(fs))
	}
	if got := e.h.libraryEvents(t, e.id); !slices.Equal(got, []string{"added", "gone"}) {
		t.Fatalf("events = %v, want [added gone]", got)
	}
}

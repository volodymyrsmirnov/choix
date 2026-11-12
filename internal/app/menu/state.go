package menu

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/system"
)

// initialContext is the screen the menu shows before the frontend reports
// one: the Library, with no folder selected.
var initialContext = system.MenuContext{Screen: "library"}

// folderVariants are the folder row's context menus: each leaves out the
// items Hidden for its flag ("" leaves out none). PopupFolderMenu uses the
// first variant after the default whose flag the folder has.
var folderVariants = []struct{ name, flag string }{
	{"default", ""},
	{"offline", "offline"},
	{"scanning", "scanning"},
}

// slotsPerVariant is how many pre-built menus each variant has: the one a
// popup shows, and the previous one, whose click may still be on its way.
const slotsPerVariant = 2

// slot is one pre-built context menu: its registered name, and the folder
// its clicks act on. folder is written only by PopupFolderMenu, under
// popupMu, and read atomically by the slot's click closures.
type slot struct {
	name   string
	folder atomic.Pointer[string]
}

// variantPool is one variant's slots and the next one a popup takes.
type variantPool struct {
	flag  string
	slots []*slot
	next  int
}

// State is the installed native menu. It implements system.MenuState: the
// System binding passes on the screen the frontend shows and opens the
// folder row's context menu through it. It is safe for concurrent use.
type State struct {
	items      map[string]Item // the menu bar's custom items, by id
	singleKeys bool
	emit       events.Emitter
	// show opens the registered context menu called name at window
	// coordinates x, y. On the desktop it returns once the menu has been
	// dismissed.
	show func(name string, x, y int)

	mu      sync.Mutex // serialises SetContext
	handles map[string]Handle

	// popupMu makes taking a slot, writing its folder and showing it one
	// step (PopupFolderMenu).
	popupMu sync.Mutex
	pools   []*variantPool
}

var _ system.MenuState = (*State)(nil)

// newState builds the menu bar of m with bar and sets it to initialContext.
// It builds the folder row's context menus once, a fixed pool: for every
// variant, slotsPerVariant menus, each made with the factory contextMenu
// returns for its name. Nothing is built after newState returns. Clicks are
// emitted through emit as menu:action; show opens a pooled menu by name.
func newState(m Manifest, bar Factory, contextMenu func(name string) Factory, show func(name string, x, y int), emit events.Emitter, singleKeys bool) *State {
	s := &State{
		items:      map[string]Item{},
		singleKeys: singleKeys,
		emit:       emit,
		show:       show,
	}
	for _, menu := range m.Menus {
		for _, it := range menu.Items {
			if it.ID != "" {
				s.items[it.ID] = it
			}
		}
	}
	s.handles = Build(m, bar, singleKeys, s.clickBar)
	for _, v := range folderVariants {
		pool := &variantPool{flag: v.flag}
		for i := range slotsPerVariant {
			sl := &slot{name: fmt.Sprintf("%s/%s/%d", FolderRow, v.name, i)}
			buildSlot(m.Context[FolderRow], v.flag, contextMenu(sl.name), sl, s.send)
			pool.slots = append(pool.slots, sl)
		}
		s.pools = append(s.pools, pool)
	}
	s.SetContext(initialContext)
	return s
}

// SetContext enables each menu-bar item that is Enabled in c and hides each
// that is Hidden for c's flags.
func (s *State) SetContext(c system.MenuContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, h := range s.handles {
		it := s.items[id]
		h.SetEnabled(Enabled(it, c))
		h.SetHidden(Hidden(it, c.Flags))
	}
}

// buildSlot builds one pooled context menu with f: items without those
// Hidden for flag, each click emitting menu:action with the slot's folder at
// the time of the click.
func buildSlot(items []Item, flag string, f Factory, sl *slot, send func(events.MenuActionEvent)) {
	var flags []string
	if flag != "" {
		flags = []string{flag}
	}
	BuildContext(items, flags, f, func(id string) {
		var folder string
		if p := sl.folder.Load(); p != nil {
			folder = *p
		}
		send(events.MenuActionEvent{ID: id, Data: folder})
	})
}

// PopupFolderMenu opens the folder row's context menu for folderID at the
// window coordinates x, y: the variant for the folder's flags, in its next
// slot, round-robin. Both a right-click and the row's ⋯ button open it so.
//
// Taking the slot, writing its folder and showing it happen under popupMu,
// so no other popup can take the same slot between the write and the show.
// Wails' OpenContextMenu blocks until the menu is dismissed (InvokeSync of
// a synchronous NSMenu popUp), so the lock is held while the menu is up and
// a second popup waits for the first to close. That cannot deadlock: the
// main thread never takes popupMu, and a click reaches its closure through
// Wails' channel and a goroutine, where it reads only the slot's atomic
// folder. A click on the previous popup's menu keeps its folder, because
// the next popup of that variant takes the other slot.
func (s *State) PopupFolderMenu(folderID string, flags []string, x, y int) {
	s.popupMu.Lock()
	defer s.popupMu.Unlock()
	pool := s.pools[0]
	for _, p := range s.pools[1:] {
		if slices.Contains(flags, p.flag) {
			pool = p
			break
		}
	}
	sl := pool.slots[pool.next]
	pool.next = (pool.next + 1) % len(pool.slots)
	sl.folder.Store(&folderID)
	s.show(sl.name, x, y)
}

// SingleKeysNative reports whether the menu owns the single-key shortcuts
// (NativeAccelerator).
func (s *State) SingleKeysNative() bool { return s.singleKeys }

// clickBar emits a menu-bar click.
func (s *State) clickBar(id string) {
	s.send(events.MenuActionEvent{ID: id})
}

// send emits one menu:action.
func (s *State) send(e events.MenuActionEvent) {
	if s.emit != nil {
		s.emit.Emit(events.MenuAction, e)
	}
}

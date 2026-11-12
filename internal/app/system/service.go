// Package system is the app's Finder integration, its native menu state, its
// window's title bar and its "about" information: reveal a folder in
// Finder, open its picks folder, open the project's page, tell the native
// menu which screen is showing, pop up a folder's context menu, act on a
// double-click on the title bar and report the app's version.
//
// Finder and the browser are reached only through platform.Workspace, which
// calls NSWorkspace in-process (design call C6): never a helper executable,
// and never Wails' OpenFileManager or Browser.OpenURL, which run
// /usr/bin/open. Each Workspace call is made while the folder's
// security-scoped access is held, and that access is released only after
// the call has returned.
package system

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
	"github.com/volodymyrsmirnov/choix/internal/app/session"
	"github.com/volodymyrsmirnov/choix/internal/appdir"
	"github.com/volodymyrsmirnov/choix/internal/buildinfo"
	"github.com/volodymyrsmirnov/choix/internal/config"
	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/safefs"
)

// appName is the product name Info reports.
const appName = "Choix"

// helpURL is the only URL the frontend can open.
const helpURL = "https://github.com/volodymyrsmirnov/choix"

// screens are the design's ScreenIds a MenuContext may name.
var screens = []string{"library", "folder", "focus", "compare", "settings"}

// Field and reason codes of the invalid errors this package builds.
const (
	fieldScreen   = "screen"
	fieldPicksDir = "picks_dir"

	reasonUnknownScreen = "unknown"
	reasonSymlink       = "symlink"
	reasonNotDir        = "not_dir"
)

// sandboxed reports whether the process runs in the App Sandbox. Tests
// replace it.
var sandboxed = appdir.Sandboxed

// MenuContext is what the frontend tells the native menu about the screen
// it shows. Screen is a design ScreenId: library, folder, focus, compare or
// settings.
type MenuContext struct {
	Screen       string   `json:"screen"`
	HasSelection bool     `json:"hasSelection"`
	Flags        []string `json:"flags"`
}

// MenuState is the native menu. It is implemented by internal/app/menu; a
// nil MenuState in Deps (until then, and in server builds) is a no-op whose
// SingleKeysNative is false.
type MenuState interface {
	// SetContext updates the menu items for the screen the frontend shows.
	SetContext(c MenuContext)
	// PopupFolderMenu opens the native context menu for a folder row at
	// window coordinates.
	PopupFolderMenu(folderID string, flags []string, x, y int)
	// SingleKeysNative reports whether the native menu owns the
	// single-key shortcuts.
	SingleKeysNative() bool
}

// noMenu is the MenuState used when Deps has none.
type noMenu struct{}

func (noMenu) SetContext(MenuContext)                     {}
func (noMenu) PopupFolderMenu(string, []string, int, int) {}
func (noMenu) SingleKeysNative() bool                     { return false }

// TitleBar is the main window's title bar as the frontend's own title bar
// needs it. It is implemented by internal/app/window; a nil TitleBar in
// Deps (until the window exists, and in server builds) measures 0 and does
// nothing on a double-click.
type TitleBar interface {
	// ButtonsRight returns the right edge of the window's right-most
	// traffic light, in points from the window's left edge; 0 when it is
	// not known. It may wait for the main thread.
	ButtonsRight() float64
	// ButtonsCenterY returns the traffic lights' common vertical centre, in
	// points from the top of the window; 0 when it is not known. It may
	// wait for the main thread.
	ButtonsCenterY() float64
	// FullScreen reports whether the window is in native full screen now;
	// false when that is not known. It may wait for the main thread.
	FullScreen() bool
	// DoubleClick acts on a double-click on the frontend's title bar as a
	// native title bar would, for the system settings Wails does not act on
	// itself. It waits for the main thread.
	DoubleClick()
}

// noTitleBar is the TitleBar used when Deps has none.
type noTitleBar struct{}

func (noTitleBar) ButtonsRight() float64   { return 0 }
func (noTitleBar) ButtonsCenterY() float64 { return 0 }
func (noTitleBar) FullScreen() bool        { return false }
func (noTitleBar) DoubleClick()            {}

// Info is the app's "about" information, and what the frontend's title bar
// needs of the window: WindowButtonsRight is the right edge of the traffic
// lights in CSS pixels (points) from the window's left edge, 0 when unknown;
// WindowButtonsCenterY is their common vertical centre in CSS pixels from
// the top of the window (the page), 0 when unknown; WindowFullScreen is
// whether the window is in native full screen now (false when unknown).
type Info struct {
	Name                 string  `json:"name"`
	Version              string  `json:"version"`
	Commit               string  `json:"commit"`
	Sandboxed            bool    `json:"sandboxed"`
	SingleKeysNative     bool    `json:"singleKeysNative"`
	WindowButtonsRight   float64 `json:"windowButtonsRight"`
	WindowButtonsCenterY float64 `json:"windowButtonsCenterY"`
	WindowFullScreen     bool    `json:"windowFullScreen"`
}

// Deps are what the Service works with. Library, Sessions, Workspace and
// Gate are required. A nil Settings means the built-in defaults; a nil
// FolderState means every folder is ready; a nil MenuState or TitleBar is a
// no-op.
type Deps struct {
	Library  *library.Library
	Sessions *session.Manager
	Settings func() config.Config
	// FolderState returns the folder's state as the Library page shows it:
	// ready, scanning, queued, offline or error. The runtime supplies it
	// from the library summary.
	FolderState func(id string) (state string, err error)
	Workspace   platform.Workspace
	MenuState   MenuState
	TitleBar    TitleBar
	Gate        *opgate.Gate
}

// Service is the system binding. Every exported method is a frontend
// binding; nothing else may be exported on it. Its only state is the rate
// limit of the frontend's error reports; it is safe for concurrent use.
// Every method enters the gate first, so after quit began it answers
// unavailable (ReportClientError drops the report instead).
type Service struct {
	d       Deps
	reports *clientErrors
}

// NewService returns the system binding over d.
func NewService(d Deps) *Service {
	if d.Settings == nil {
		d.Settings = config.Defaults
	}
	if d.MenuState == nil {
		d.MenuState = noMenu{}
	}
	if d.TitleBar == nil {
		d.TitleBar = noTitleBar{}
	}
	return &Service{d: d, reports: newClientErrors()}
}

// ReportClientError logs an error the frontend could not handle (a screen
// that failed to render, an uncaught error or rejection) as one slog.Error
// line, "frontend error", with where it happened, its message and the
// first lines of its stack, all cut to a bounded length; at most a few such
// lines a minute are written (clienterror.go). The report goes through the
// gate like every binding, but never fails its caller: after quit began, or
// over the rate limit, it is dropped and nil is returned.
func (s *Service) ReportClientError(where, message, stack string) error {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return nil //nolint:nilerr // a report never fails its caller; after quit began it is dropped
	}
	defer leave()
	s.reports.report(where, message, stack)
	return nil
}

// Info returns the app's name, version, commit, whether it runs in the
// sandbox, whether the native menu owns the single-key shortcuts, where
// the window's traffic lights end and are centred vertically, and whether
// the window is in full screen.
// It is unavailable once quit began.
func (s *Service) Info() (Info, error) {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return Info{}, err
	}
	info := Info{
		Name:             appName,
		Version:          buildinfo.Version,
		Commit:           buildinfo.Commit,
		Sandboxed:        sandboxed(),
		SingleKeysNative: s.d.MenuState.SingleKeysNative(),
	}
	// The gate is left before the window is read: the measurement and the
	// full-screen state wait for the main thread (as SetMenuContext's
	// update).
	leave()
	info.WindowButtonsRight = s.d.TitleBar.ButtonsRight()
	info.WindowButtonsCenterY = s.d.TitleBar.ButtonsCenterY()
	info.WindowFullScreen = s.d.TitleBar.FullScreen()
	return info, nil
}

// TitleBarDoubleClick is the frontend's report of a double-click on an
// empty part of its title bar: the window zooms or minimises as the user's
// title-bar setting says, for the settings Wails does not act on itself
// (internal/app/window). It is unavailable once quit began.
func (s *Service) TitleBarDoubleClick() error {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return err
	}
	// Outside the gate, as in SetMenuContext: acting on the window waits
	// for the main thread.
	leave()
	s.d.TitleBar.DoubleClick()
	return nil
}

// OpenPicksFolder opens the folder's picks directory in Finder: its
// effective picks directory (where earlier picks were exported, else the
// picks_dir setting), created through safefs when missing. A session
// reference — and with it the folder's access — is held until
// Workspace.OpenFolder has returned. A directory that cannot be a picks
// directory (a symbolic link, not a directory, outside the folder, the
// folder itself, holding originals) is an invalid error with its reason,
// and nothing is opened.
func (s *Service) OpenPicksFolder(folderID string) error {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return err
	}
	defer leave()
	if err := s.openPicksFolder(folderID); err != nil {
		return apperr.From(err)
	}
	return nil
}

func (s *Service) openPicksFolder(folderID string) error {
	sess, release, err := s.d.Sessions.Acquire(folderID)
	if err != nil {
		return fmt.Errorf("system: open picks folder: %w", err)
	}
	defer release()

	dir, err := sess.PicksDir(s.d.Settings())
	if err != nil {
		return refusedPicksDir(folderID, dir, err)
	}
	// Durable, as an export would make it: a later pick finds the
	// directory existing and does not sync its parent again.
	d, err := safefs.OpenDirDurable(sess.Root, dir)
	if err != nil {
		return refusedPicksDir(folderID, dir, err)
	}
	if err := d.Close(); err != nil {
		slog.Warn("system: close picks directory", "folder", folderID, "err", err)
	}

	abs := filepath.Join(sess.Root, filepath.FromSlash(dir))
	if err := s.d.Workspace.OpenFolder(abs); err != nil {
		slog.Warn("system: open picks folder in Finder", "folder", folderID, "err", err)
		return fmt.Errorf("system: open picks folder: %w", err)
	}
	return nil
}

// refusedPicksDir classifies why dir, the folder's picks directory, cannot
// be opened: a refused directory is an invalid error for picks_dir with
// the reason (picks.ErrDirHasOriginals is registered with apperr as
// has_originals); anything else is returned wrapped.
func refusedPicksDir(folderID, dir string, err error) error {
	wrapped := fmt.Errorf("system: open picks folder: %w", err)
	var de *picks.DirError
	var reason string
	switch {
	case errors.As(err, &de):
		reason = de.Reason
	case errors.Is(err, safefs.ErrSymlink):
		reason = reasonSymlink
	case errors.Is(err, safefs.ErrNotDir):
		reason = reasonNotDir
	case errors.Is(err, picks.ErrDirHasOriginals):
		slog.Warn("system: refusing to open a picks directory", "folder", folderID, "picks_dir", dir, "err", err)
		return wrapped
	default:
		return wrapped
	}
	slog.Warn("system: refusing to open a picks directory", "folder", folderID, "picks_dir", dir, "reason", reason, "err", err)
	return apperr.Invalid(fieldPicksDir, reason, wrapped.Error())
}

// RevealFolder shows the folder in Finder, selected. The folder's access is
// held until Workspace.Reveal has returned.
func (s *Service) RevealFolder(folderID string) error {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return err
	}
	defer leave()
	root, release, err := s.d.Library.Acquire(folderID)
	if err != nil {
		return apperr.From(fmt.Errorf("system: reveal folder: %w", err))
	}
	defer release()
	if err := s.d.Workspace.Reveal(root); err != nil {
		slog.Warn("system: reveal folder in Finder", "folder", folderID, "err", err)
		return apperr.From(fmt.Errorf("system: reveal folder: %w", err))
	}
	return nil
}

// OpenHelp opens the project's page in the default browser. It is the only
// URL the frontend can open.
func (s *Service) OpenHelp() error {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return err
	}
	defer leave()
	if err := s.d.Workspace.OpenURL(helpURL); err != nil {
		slog.Warn("system: open the project page", "err", err)
		return apperr.From(fmt.Errorf("system: open help: %w", err))
	}
	return nil
}

// SetMenuContext tells the native menu which screen the frontend shows. A
// screen other than library, folder, focus, compare or settings is an
// invalid error for screen.
func (s *Service) SetMenuContext(c MenuContext) error {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return err
	}
	// The gate is left before the menu is updated, as AddFolders leaves it
	// around its dialog: each native item update waits for the main thread,
	// and on quit that thread runs Shutdown, which waits for the gate —
	// holding it here would make quit wait for the deadline. The update
	// uses nothing Shutdown closes.
	leave()
	if !slices.Contains(screens, c.Screen) {
		return apperr.Invalid(fieldScreen, reasonUnknownScreen, fmt.Sprintf("system: unknown screen %q", c.Screen))
	}
	c.Flags = slices.Clone(c.Flags)
	s.d.MenuState.SetContext(c)
	return nil
}

// ShowFolderMenu opens the native context menu of the folder's row at the
// window coordinates x, y; it serves both a right-click and the row's ⋯
// button. The menu's flags come from the folder's state: "offline" or
// "scanning", else none. An unknown folder is not_found.
func (s *Service) ShowFolderMenu(folderID string, x, y int) error {
	flags, err := s.folderMenuFlags(folderID)
	if err != nil {
		return err
	}
	// Outside the gate, as in SetMenuContext: opening the menu waits for
	// the main thread.
	s.d.MenuState.PopupFolderMenu(folderID, flags, x, y)
	return nil
}

// folderMenuFlags returns the flags of the folder's context menu, reading
// library.db inside the gate.
func (s *Service) folderMenuFlags(folderID string) ([]string, error) {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return nil, err
	}
	defer leave()
	if _, err := s.d.Library.Folder(folderID); err != nil {
		return nil, apperr.From(fmt.Errorf("system: folder menu: %w", err))
	}
	if s.d.FolderState == nil {
		return nil, nil
	}
	state, err := s.d.FolderState(folderID)
	if err != nil {
		return nil, apperr.From(fmt.Errorf("system: folder menu: state of %s: %w", folderID, err))
	}
	if state == "offline" || state == "scanning" {
		return []string{state}, nil
	}
	return nil, nil
}

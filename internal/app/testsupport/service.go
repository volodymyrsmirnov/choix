//go:build server

// Package testsupport is the server build's remote control for the
// end-to-end tests: Playwright drives the Wails server build of the app over
// HTTP, where the native folder picker and remove confirmation cannot be
// shown or automated. The Service is bound next to the app's own services;
// through it a test adds folders by path, sets what the next folder picker
// "picks" and how the next removal is answered (Dialogs), waits for the job
// queue to drain and fires a menu action.
//
// Every file of the package carries the server build tag: nothing of it is
// compiled into a build that ships. It accepts only paths inside the
// directory named by CHOIX_E2E_ROOT, after resolving symlinks; without that
// variable it refuses every path. The package does not import Wails:
// cmd/choix registers the Service.
package testsupport

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/runtime"
)

// Reasons of the invalid errors a refused path gets.
const (
	reasonNoRoot       = "no_e2e_root"  // CHOIX_E2E_ROOT is unset or unusable
	reasonNotAbsolute  = "not_absolute" // the path is relative
	reasonUnresolvable = "unresolvable" // the path or a symlink in it does not resolve
	reasonOutsideRoot  = "outside_root" // the resolved path is not inside the root
	reasonNotDirectory = "not_directory"
)

const (
	// maxWait bounds WaitIdle, whatever timeout the caller asks for: the
	// binding's answer must reach the client before the Wails server's write
	// timeout (30 s by default) cuts the response, so a longer wait is a
	// loop of calls in the test.
	maxWait = 25 * time.Second
	// idlePoll is how often WaitIdle checks the runtime.
	idlePoll = 10 * time.Millisecond
)

// Service is the test-support binding. Every exported method is a frontend
// binding (server builds only); nothing else may be exported on it.
type Service struct {
	d    *Dialogs
	emit events.Emitter
	// root is the E2E root with its symlinks resolved; "" refuses every path.
	root string

	// add adds folders and queues a full job for each new one
	// (Runtime.AddDropped); idle reports an idle runtime (Runtime.Idle).
	add  func(paths []string) []library.AddResult
	idle func() bool
}

// New returns the test-support binding over rt. d must be the Dialogs given
// to the runtime as its picker and confirmer; emit receives the menu actions
// (nil sends none). root is $CHOIX_E2E_ROOT: every path the Service accepts
// must lie inside it once symlinks are resolved. An empty root, or one that
// does not resolve to a directory, makes the Service refuse every path.
func New(rt *runtime.Runtime, d *Dialogs, emit events.Emitter, root string) *Service {
	return &Service{
		d:    d,
		emit: emit,
		root: resolveRoot(root),
		add:  rt.AddDropped,
		idle: rt.Idle,
	}
}

// resolveRoot returns root absolute with its symlinks resolved, or "" when it
// is empty, is not a directory that resolves, or is the filesystem root
// (which would let every path in).
func resolveRoot(root string) string {
	if root == "" {
		return ""
	}
	abs, err := filepath.Abs(root)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		slog.Warn("testsupport: the E2E root does not resolve; every path is refused", "err", err)
		return ""
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		slog.Warn("testsupport: the E2E root is not a directory; every path is refused", "root", abs)
		return ""
	}
	if filepath.Dir(abs) == abs {
		slog.Warn("testsupport: the E2E root is the filesystem root; every path is refused")
		return ""
	}
	return abs
}

// check returns path with its symlinks resolved when that lies inside the
// root (the root itself included); otherwise an invalid error for field.
func (s *Service) check(field, path string) (string, error) {
	if s.root == "" {
		return "", apperr.Invalid(field, reasonNoRoot, "testsupport: CHOIX_E2E_ROOT is not set; every path is refused")
	}
	if !filepath.IsAbs(path) {
		return "", apperr.Invalid(field, reasonNotAbsolute, fmt.Sprintf("testsupport: %q is not an absolute path", path))
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", apperr.Invalid(field, reasonUnresolvable, fmt.Sprintf("testsupport: resolve %q: %v", path, err))
	}
	if resolved != s.root && !strings.HasPrefix(resolved, s.root+string(filepath.Separator)) {
		return "", apperr.Invalid(field, reasonOutsideRoot, fmt.Sprintf("testsupport: %q is not inside the E2E root", path))
	}
	return resolved, nil
}

// AddFolderPath adds the folder at path to the Library and queues a full job
// for it when it is new, as a folder dropped on the window is. path must be
// absolute and, with its symlinks resolved, a directory inside the E2E root;
// anything else is invalid and nothing is added.
func (s *Service) AddFolderPath(path string) (library.AddResult, error) {
	resolved, err := s.check("path", path)
	if err != nil {
		return library.AddResult{}, err
	}
	if fi, err := os.Stat(resolved); err != nil || !fi.IsDir() {
		return library.AddResult{}, apperr.Invalid("path", reasonNotDirectory, fmt.Sprintf("testsupport: %q is not a directory", path))
	}
	res := s.add([]string{resolved})
	if len(res) != 1 {
		// AddDropped logs why; it adds nothing once quit has begun.
		return library.AddResult{}, apperr.From(fmt.Errorf("testsupport: add %q: the folder was not added (see the log)", path))
	}
	return res[0], nil
}

// SetPickerPaths sets what the next folder picker (Library.AddFolders)
// "picks": the paths, in order, with their symlinks resolved. Each must be
// absolute and inside the E2E root once resolved; when one is not, the whole
// list is refused as invalid and what was set before stays. The paths answer
// one pick only; an empty list makes the next pick a cancelled one.
func (s *Service) SetPickerPaths(paths []string) error {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		resolved, err := s.check("paths", p)
		if err != nil {
			return err
		}
		out = append(out, resolved)
	}
	s.d.setPickerPaths(out)
	return nil
}

// SetConfirmAnswer sets how every later remove confirmation is answered:
// yes confirms, false declines (the answer until one is set).
func (s *Service) SetConfirmAnswer(yes bool) {
	s.d.setConfirm(yes)
}

// WaitIdle waits until no job is queued or running, for at most timeoutMs
// milliseconds (capped at 25 s, see maxWait;
// zero or a negative timeout checks once), and reports whether that happened.
func (s *Service) WaitIdle(timeoutMs int) bool {
	wait := time.Duration(max(timeoutMs, 0)) * time.Millisecond
	deadline := time.Now().Add(min(wait, maxWait))
	for {
		if s.idle() {
			return true
		}
		left := time.Until(deadline)
		if left <= 0 {
			return false
		}
		time.Sleep(min(idlePoll, left))
	}
}

// EmitMenuAction sends the menu:action event the native menu would: id is
// the action, data the folder a context-menu click came from, if any.
func (s *Service) EmitMenuAction(id, data string) {
	if s.emit == nil {
		return
	}
	s.emit.Emit(events.MenuAction, events.MenuActionEvent{ID: id, Data: data})
}

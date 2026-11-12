//go:build darwin

// Package workspace implements platform.Workspace on AppKit's NSWorkspace,
// called in-process through cgo. It never runs a helper executable such as
// /usr/bin/open, which the app sandbox forbids.
//
// Each AppKit call is dispatched to the main queue and the method waits,
// without a timeout, until the call has run. A method that returned earlier
// would let its caller release the folder's access lease while the call is
// still queued, so the caller always holds its lease while AppKit runs. The
// methods are called from goroutines, never from the main thread; if the
// main thread is busy (for example with shutdown) a call simply waits.
package workspace

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework Foundation -framework AppKit
#include <stdlib.h>
#include "workspace.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// operation is an NSWorkspace call.
type operation int

const (
	// opReveal is activateFileViewerSelectingURLs: with one file URL.
	opReveal operation = C.CHOIX_WORKSPACE_REVEAL
	// opOpen is openURL:.
	opOpen operation = C.CHOIX_WORKSPACE_OPEN
)

// String names op for test failures and errors.
func (op operation) String() string {
	switch op {
	case opReveal:
		return "reveal"
	case opOpen:
		return "open"
	default:
		return fmt.Sprintf("operation(%d)", int(op))
	}
}

// dispatch makes the AppKit call op on rawURL and returns only after it has
// run. Tests replace it with a recorder so that no Finder window or browser
// tab opens.
var dispatch = realDispatch

// realDispatch runs op on the main queue and waits for it; see
// choix_workspace_run.
func realDispatch(op operation, rawURL string) error {
	curl := C.CString(rawURL)
	defer C.free(unsafe.Pointer(curl))
	return errFromNative(C.choix_workspace_run(C.int(op), curl))
}

// errFromNative converts a native failure message, freeing it; nil means
// success.
func errFromNative(msg *C.char) error {
	if msg == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(msg))
	return errors.New(C.GoString(msg))
}

type workspace struct{}

// New returns the NSWorkspace implementation of platform.Workspace.
func New() platform.Workspace { return workspace{} }

// fileURL returns the file URL of the absolute path p, with a trailing slash
// when p is a directory. The path is percent-encoded, so names holding '#',
// '?' or '%' stay part of the path.
func fileURL(p string, dir bool) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", p)
	}
	p = filepath.Clean(p)
	if dir && p != "/" {
		p += "/"
	}
	return (&url.URL{Scheme: "file", Path: p}).String(), nil
}

// Reveal implements platform.Workspace: it opens a Finder window with the
// item at path selected. The item itself is revealed, also when it is a
// symlink.
func (workspace) Reveal(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("workspace: reveal: %w", err)
	}
	u, err := fileURL(path, info.IsDir())
	if err != nil {
		return fmt.Errorf("workspace: reveal: %w", err)
	}
	if err := dispatch(opReveal, u); err != nil {
		return fmt.Errorf("workspace: reveal %q: %w", path, err)
	}
	return nil
}

// OpenFolder implements platform.Workspace: it opens a Finder window showing
// the contents of the existing directory at path.
func (workspace) OpenFolder(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("workspace: open folder: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace: open folder %q: not a directory", path)
	}
	u, err := fileURL(path, true)
	if err != nil {
		return fmt.Errorf("workspace: open folder: %w", err)
	}
	if err := dispatch(opOpen, u); err != nil {
		return fmt.Errorf("workspace: open folder %q: %w", path, err)
	}
	return nil
}

// OpenURL implements platform.Workspace: it opens rawURL in the default
// browser. Only https URLs with a host are accepted; what is opened is the
// URL as net/url re-serialises it, so AppKit never parses a string Go did
// not validate.
func (workspace) OpenURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("workspace: open url: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("workspace: open url %q: only https URLs are opened", rawURL)
	}
	if err := dispatch(opOpen, u.String()); err != nil {
		return fmt.Errorf("workspace: open url %q: %w", rawURL, err)
	}
	return nil
}

// nsURLPathForTest returns the path Cocoa reads from rawURL through the same
// conversion choix_workspace_run uses. It touches no AppKit.
func nsURLPathForTest(rawURL string) (string, error) {
	curl := C.CString(rawURL)
	defer C.free(unsafe.Pointer(curl))
	var cpath *C.char
	msg := C.choix_workspace_url_path_for_test(curl, &cpath) //nolint:gocritic // dupSubExpr is a false positive on cgo's pointer check for &cpath
	if err := errFromNative(msg); err != nil {
		return "", err
	}
	defer C.free(unsafe.Pointer(cpath))
	return C.GoString(cpath), nil
}

// raiseForTest raises an NSException inside the entry points' guard and
// returns the resulting error.
func raiseForTest() error {
	return errFromNative(C.choix_workspace_raise_for_test())
}

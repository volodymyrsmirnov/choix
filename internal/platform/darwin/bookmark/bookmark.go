//go:build darwin

// Package bookmark implements platform.Bookmarks with app-scoped
// security-scoped bookmarks on NSURL, called in-process through cgo. Spike
// S1 recorded which errors mean the target is gone: a resolve failure in
// NSCocoaErrorDomain with code 4 (NSFileNoSuchFileError) — a deleted or
// trashed-and-emptied folder, an ejected volume, an unmounted share. Every
// other failure is platform.ErrUnusable.
package bookmark

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework Foundation -framework CoreFoundation
#include <stdlib.h>
#include "bookmark.h"
*/
import "C"

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/appdir"
	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// sandboxed reports whether the process runs in the App Sandbox. It is a
// variable so that tests can force either answer.
var sandboxed = appdir.Sandboxed

// goneDomain and goneCode are the only resolve error spike S1 recorded for a
// target that no longer exists.
const (
	goneDomain = "NSCocoaErrorDomain"
	goneCode   = 4
)

type bookmarks struct{}

// New returns the NSURL implementation of platform.Bookmarks.
func New() platform.Bookmarks { return bookmarks{} }

// classify returns the platform error for an NSError's domain and code.
func classify(domain string, code int) error {
	if domain == goneDomain && code == goneCode {
		return platform.ErrGone
	}
	return platform.ErrUnusable
}

// native is a successful native result.
type native struct {
	bytes   []byte
	path    string
	stale   bool
	started bool
	handle  unsafe.Pointer
}

// convert turns r into a native result or an error for op, freeing r's
// buffers. A failure never carries a handle.
func convert(op string, r C.ChoixBookmarkResult) (native, error) {
	defer C.free(unsafe.Pointer(r.bytes))
	defer C.free(unsafe.Pointer(r.path))
	defer C.free(unsafe.Pointer(r.domain))
	defer C.free(unsafe.Pointer(r.message))
	msg := "unknown error"
	if r.message != nil {
		msg = C.GoString(r.message)
	}
	switch r.status {
	case C.CHOIX_BOOKMARK_OK:
		n := native{
			stale:   r.stale != 0,
			started: r.started != 0,
			handle:  r.handle,
		}
		if r.bytes != nil {
			n.bytes = bytes.Clone(unsafe.Slice((*byte)(unsafe.Pointer(r.bytes)), r.len))
		}
		if r.path != nil {
			n.path = C.GoString(r.path)
		}
		return n, nil
	case C.CHOIX_BOOKMARK_NSERROR:
		domain := ""
		if r.domain != nil {
			domain = C.GoString(r.domain)
		}
		code := int(r.code)
		return native{}, fmt.Errorf("bookmark: %s: %s (%s %d): %w", op, msg, domain, code, classify(domain, code))
	default:
		return native{}, fmt.Errorf("bookmark: %s: %s: %w", op, msg, platform.ErrUnusable)
	}
}

// cData returns a pointer to data's first byte, or nil when it is empty. The
// native side copies the bytes before using them.
func cData(data []byte) (unsafe.Pointer, C.size_t) {
	if len(data) == 0 {
		return nil, 0
	}
	return unsafe.Pointer(&data[0]), C.size_t(len(data))
}

// cBool converts b for the native side.
func cBool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

// Create implements platform.Bookmarks. Outside the sandbox a failed
// security-scoped creation is retried without the security scope.
func (bookmarks) Create(path string) ([]byte, error) {
	op := "create " + path
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return nil, fmt.Errorf("bookmark: create %q: invalid path: %w", path, platform.ErrUnusable)
	}
	// A path that cannot be stat'ed is left to Foundation, whose error is the
	// one spike S1 recorded.
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return nil, fmt.Errorf("bookmark: %s: not a directory: %w", op, platform.ErrUnusable)
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	n, err := convert(op, C.choix_bookmark_create(cpath, cBool(!sandboxed())))
	if err != nil {
		return nil, err
	}
	return n.bytes, nil
}

// Resolve implements platform.Bookmarks.
func (bookmarks) Resolve(data []byte) (path string, stale bool, err error) {
	p, n := cData(data)
	res, err := convert("resolve", C.choix_bookmark_resolve(p, n))
	if err != nil {
		return "", false, err
	}
	return res.path, res.stale, nil
}

// refusedStart returns the error for a start of security-scoped access to
// path that the system refused: ErrUnusable in the sandbox, nil — a lease
// whose stop does nothing — outside it.
func refusedStart(path string) error {
	if sandboxed() {
		return fmt.Errorf("bookmark: start access %s: security-scoped access refused: %w", path, platform.ErrUnusable)
	}
	return nil
}

// StartAccess implements platform.Bookmarks. stop is idempotent and safe for
// concurrent use.
func (bookmarks) StartAccess(data []byte) (path string, stop func(), err error) {
	p, n := cData(data)
	res, err := convert("start access", C.choix_bookmark_start(p, n))
	if err != nil {
		return "", nil, err
	}
	if !res.started {
		if err := refusedStart(res.path); err != nil {
			return "", nil, err
		}
		return res.path, func() {}, nil
	}
	handle := res.handle
	var once sync.Once
	return res.path, func() {
		once.Do(func() { C.choix_bookmark_stop(handle) })
	}, nil
}

// Refresh implements platform.Bookmarks. Access is started and stopped once
// around the creation, which uses the resolved URL: in the sandbox a new
// bookmark cannot be made from the path string. On error no bytes are
// returned and data is not modified.
func (bookmarks) Refresh(data []byte) ([]byte, error) {
	p, n := cData(data)
	res, err := convert("refresh", C.choix_bookmark_refresh(p, n, cBool(!sandboxed())))
	if err != nil {
		return nil, err
	}
	return res.bytes, nil
}

// raiseForTest raises an NSException named name inside the entry points'
// guard and returns the resulting error.
func raiseForTest(name string) error {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	_, err := convert("test", C.choix_bookmark_raise_for_test(cname))
	return err
}

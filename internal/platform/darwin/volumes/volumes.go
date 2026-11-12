//go:build darwin

// Package volumes implements platform.Volumes on Foundation, called
// in-process through cgo: volume identity from NSURL resource values,
// mounted volumes from NSFileManager, and Trash membership from
// NSFileManager's directory relationship — never from the path's name.
package volumes

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#include "volumes.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

type volumes struct{}

// New returns the Foundation implementation of platform.Volumes.
func New() platform.Volumes { return volumes{} }

// native is a ChoixVolumesResult converted to Go.
type native struct {
	uuid, name          string
	readOnly, removable bool
	flag                bool
}

// call runs fn with arg as a C string and converts its result, freeing
// every string the native side allocated.
func call(arg string, fn func(*C.char) C.ChoixVolumesResult) (native, error) {
	carg := C.CString(arg)
	defer C.free(unsafe.Pointer(carg))
	return result(fn(carg))
}

// result converts r and frees its strings.
func result(r C.ChoixVolumesResult) (native, error) {
	defer C.free(unsafe.Pointer(r.uuid))
	defer C.free(unsafe.Pointer(r.name))
	defer C.free(unsafe.Pointer(r.message))
	if r.status != C.CHOIX_VOLUMES_OK {
		msg := "unknown error"
		if r.message != nil {
			msg = C.GoString(r.message)
		}
		return native{}, errors.New(msg)
	}
	n := native{readOnly: r.read_only != 0, removable: r.removable != 0, flag: r.flag != 0}
	if r.uuid != nil {
		n.uuid = C.GoString(r.uuid)
	}
	if r.name != nil {
		n.name = C.GoString(r.name)
	}
	return n, nil
}

// Info reads the volume resource values of path; a volume without a UUID
// (a network share) has UUID "".
func (volumes) Info(path string) (platform.VolumeInfo, error) {
	n, err := call(path, func(p *C.char) C.ChoixVolumesResult { return C.choix_volumes_info(p) })
	if err != nil {
		return platform.VolumeInfo{}, fmt.Errorf("volumes: info %s: %w", path, err)
	}
	return platform.VolumeInfo{UUID: n.uuid, Name: n.name, ReadOnly: n.readOnly, Removable: n.removable}, nil
}

// Mounted reports whether a currently mounted volume has this UUID. It is
// false for "" and when the mounted volumes cannot be listed.
func (volumes) Mounted(uuid string) bool {
	if uuid == "" {
		return false
	}
	n, err := call(uuid, func(u *C.char) C.ChoixVolumesResult { return C.choix_volumes_mounted(u) })
	return err == nil && n.flag
}

// InTrash is true only when NSFileManager reports that a Trash directory
// contains path. When it cannot say — the path is missing, the volume has
// no Trash, access is denied — it returns the error.
func (volumes) InTrash(path string) (bool, error) {
	n, err := call(path, func(p *C.char) C.ChoixVolumesResult { return C.choix_volumes_in_trash(p) })
	if err != nil {
		return false, fmt.Errorf("volumes: trash check %s: %w", path, err)
	}
	return n.flag, nil
}

// AbsentOn is true only when lstat(path) fails with ENOENT and the nearest
// existing ancestor of path is on the volume whose UUID is uuid. Any other
// lstat answer, an ancestor whose volume cannot be read and an ancestor on
// another volume (a stale mount point) are all false.
//
// path must be absolute and already clean (filepath.Clean(path) == path);
// any other path is false. It is lstat'ed exactly as given, never
// normalised first: cleaning "a/file/../missing" or "a/locked/../missing"
// would replace lstat's ENOTDIR or EACCES with the ENOENT of "a/missing".
func (v volumes) AbsentOn(path, uuid string) bool {
	if uuid == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	if _, err := os.Lstat(path); !errors.Is(err, syscall.ENOENT) {
		return false
	}
	dir := path
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
		_, err := os.Lstat(dir)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.ENOENT) {
			return false
		}
	}
	info, err := v.Info(dir)
	return err == nil && info.UUID == uuid
}

// raiseForTest raises an NSException named name inside the entry points'
// guard and returns the resulting error.
func raiseForTest(name string) error {
	_, err := call(name, func(n *C.char) C.ChoixVolumesResult { return C.choix_volumes_raise_for_test(n) })
	return err
}

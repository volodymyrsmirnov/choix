//go:build darwin

// Package imageio implements platform.Stills on Apple's ImageIO, called
// in-process through cgo. Every native call runs through platform.Detach:
// it cannot be interrupted, so a cancelled caller returns at once and the
// call's result is discarded when it finishes. Native calls only return
// bytes; they never write a file.
package imageio

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework Foundation -framework CoreFoundation -framework CoreGraphics -framework ImageIO
#include <stdlib.h>
#include "imageio.h"
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// slowHook, when set by a test, runs inside every detached function before
// the native call.
var slowHook func()

type stills struct{}

// New returns the ImageIO implementation of platform.Stills.
func New() platform.Stills { return stills{} }

// source is a validated ImageInput: a path, or a private copy of the bytes
// so that a caller who reuses its buffer after an abandoned call cannot
// change what the still-running native call reads.
type source struct {
	name string // for error messages
	path string
	data []byte
}

func newSource(in platform.ImageInput) (source, error) {
	switch {
	case in.Path != "" && len(in.Data) > 0:
		return source{}, errors.New("imageio: both Path and Data are set")
	case in.Path != "":
		if strings.IndexByte(in.Path, 0) >= 0 {
			return source{}, fmt.Errorf("imageio: path %q contains a NUL byte", in.Path)
		}
		return source{name: in.Path, path: in.Path}, nil
	default:
		return source{name: "image data", data: bytes.Clone(in.Data)}, nil
	}
}

// orientation returns in.Orientation when it is a valid override (1–8) and
// 0 — keep the source's own tag — otherwise.
func orientation(in platform.ImageInput) C.int {
	if in.Orientation >= 1 && in.Orientation <= 8 {
		return C.int(in.Orientation)
	}
	return 0
}

// native is a successful native result.
type native struct {
	bytes         []byte
	width, height int
}

// call runs fn with the source's C arguments and converts its result.
func (s source) call(fn func(path *C.char, data unsafe.Pointer, n C.size_t) C.ChoixImageIOResult) (native, error) {
	if hook := slowHook; hook != nil {
		hook()
	}
	var cpath *C.char
	if s.path != "" {
		cpath = C.CString(s.path)
		defer C.free(unsafe.Pointer(cpath))
	}
	var data unsafe.Pointer
	if len(s.data) > 0 {
		data = unsafe.Pointer(&s.data[0])
	}
	return s.result(fn(cpath, data, C.size_t(len(s.data))))
}

// result converts r and frees its buffers.
func (s source) result(r C.ChoixImageIOResult) (native, error) {
	defer C.free(unsafe.Pointer(r.bytes))
	defer C.free(unsafe.Pointer(r.message))
	switch r.status {
	case C.CHOIX_IMAGEIO_OK:
		return native{
			bytes:  bytes.Clone(unsafe.Slice((*byte)(unsafe.Pointer(r.bytes)), r.len)),
			width:  int(r.width),
			height: int(r.height),
		}, nil
	case C.CHOIX_IMAGEIO_UNREADABLE:
		return native{}, fmt.Errorf("imageio: cannot read %s", s.name)
	default:
		msg := "unknown error"
		if r.message != nil {
			msg = C.GoString(r.message)
		}
		return native{}, fmt.Errorf("imageio: %s: %s", s.name, msg)
	}
}

// Properties implements platform.Stills.
func (stills) Properties(ctx context.Context, in platform.ImageInput) (map[string]any, error) {
	src, err := newSource(in)
	if err != nil {
		return nil, err
	}
	return platform.Detach(ctx, func() (map[string]any, error) {
		return src.decode(src.call(func(path *C.char, data unsafe.Pointer, n C.size_t) C.ChoixImageIOResult {
			return C.choix_imageio_properties(path, data, n)
		}))
	})
}

// decode turns a properties result into its map.
func (s source) decode(res native, err error) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	var props map[string]any
	if err := json.Unmarshal(res.bytes, &props); err != nil {
		return nil, fmt.Errorf("imageio: %s: decode properties: %w", s.name, err)
	}
	return props, nil
}

// Thumbnail implements platform.Stills.
func (stills) Thumbnail(ctx context.Context, in platform.ImageInput, limit int, fit platform.Fit) (platform.Image, error) {
	if limit <= 0 || limit > 1<<20 {
		return platform.Image{}, fmt.Errorf("imageio: invalid thumbnail limit %d", limit)
	}
	src, err := newSource(in)
	if err != nil {
		return platform.Image{}, err
	}
	fitWidth := C.int(0)
	if fit == platform.FitWidth {
		fitWidth = 1
	}
	o := orientation(in)
	return platform.Detach(ctx, func() (platform.Image, error) {
		res, err := src.call(func(path *C.char, data unsafe.Pointer, n C.size_t) C.ChoixImageIOResult {
			return C.choix_imageio_thumbnail(path, data, n, C.int(limit), fitWidth, o)
		})
		if err != nil {
			return platform.Image{}, err
		}
		return platform.Image{JPEG: res.bytes, Width: res.width, Height: res.height}, nil
	})
}

// raiseForTest raises an NSException named name inside the entry points'
// guard and returns the resulting error.
func raiseForTest(name string) error {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	_, err := source{name: "test"}.result(C.choix_imageio_raise_for_test(cname))
	return err
}

// jsonForTest returns a fixed dictionary with non-finite numbers, data,
// a date and a non-string key after the conversion Properties uses.
func jsonForTest() (map[string]any, error) {
	s := source{name: "test"}
	return s.decode(s.result(C.choix_imageio_json_for_test()))
}

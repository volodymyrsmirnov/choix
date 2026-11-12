//go:build darwin

// Package vision implements platform.Vision on Apple's Vision framework,
// called in-process through cgo. Every native call runs through
// platform.Detach: it cannot be interrupted, so a cancelled caller returns at
// once and the call's result is discarded when it finishes. Native calls
// only return values; they never write a file.
package vision

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework Foundation -framework CoreFoundation -framework CoreGraphics -framework ImageIO -framework Vision
#include <stdlib.h>
#include "vision.h"
*/
import "C"

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// Revision is the VNGenerateImageFeaturePrintRequest revision every print is
// made at (VNGenerateImageFeaturePrintRequestRevision2, macOS 14).
const Revision = 2

// slowHook, when set by a test, runs inside every detached function before
// the native call.
var slowHook func()

type vision struct{}

// New returns the Vision implementation of platform.Vision.
func New() platform.Vision { return vision{} }

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
		return source{}, errors.New("vision: both Path and Data are set")
	case in.Path == "" && len(in.Data) == 0:
		return source{}, errors.New("vision: neither Path nor Data is set")
	case in.Path != "":
		if strings.IndexByte(in.Path, 0) >= 0 {
			return source{}, fmt.Errorf("vision: path %q contains a NUL byte", in.Path)
		}
		return source{name: in.Path, path: in.Path}, nil
	default:
		return source{name: "image data", data: bytes.Clone(in.Data)}, nil
	}
}

// print makes the feature print natively and returns it with the revision
// the request ran at.
func (s source) print() ([]float32, int, error) {
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
	return s.result(C.choix_vision_feature_print(cpath, data, C.size_t(len(s.data)), C.int(Revision), C.size_t(platform.FeaturePrintLen)))
}

// result converts r and frees its buffers.
func (s source) result(r C.ChoixVisionResult) ([]float32, int, error) {
	defer C.free(unsafe.Pointer(r.values))
	defer C.free(unsafe.Pointer(r.message))
	switch r.status {
	case C.CHOIX_VISION_OK:
		if int(r.len) != platform.FeaturePrintLen || r.values == nil {
			return nil, 0, fmt.Errorf("vision: %s: feature print has %d elements, want %d", s.name, int(r.len), platform.FeaturePrintLen)
		}
		v := make([]float32, platform.FeaturePrintLen)
		copy(v, unsafe.Slice((*float32)(unsafe.Pointer(r.values)), platform.FeaturePrintLen))
		return v, int(r.revision), nil
	case C.CHOIX_VISION_UNREADABLE:
		return nil, 0, fmt.Errorf("vision: cannot read %s: %w", s.name, platform.ErrUnreadableImage)
	default:
		msg := "unknown error"
		if r.message != nil {
			msg = C.GoString(r.message)
		}
		return nil, 0, fmt.Errorf("vision: %s: %s", s.name, msg)
	}
}

// FeaturePrint implements platform.Vision.
func (vision) FeaturePrint(ctx context.Context, in platform.ImageInput) ([]float32, error) {
	src, err := newSource(in)
	if err != nil {
		return nil, err
	}
	return platform.Detach(ctx, func() ([]float32, error) {
		v, _, err := src.print()
		return v, err
	})
}

// raiseForTest raises an NSException named name inside the entry point's
// guard and returns the resulting error.
func raiseForTest(name string) error {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	_, _, err := source{name: "test"}.result(C.choix_vision_raise_for_test(cname))
	return err
}

// raiseAfterRequestForTest makes FeaturePrint raise an NSException once its
// Vision request handler and request exist (on), or stops it (off).
func raiseAfterRequestForTest(on bool) {
	v := 0
	if on {
		v = 1
	}
	C.choix_vision_raise_after_request_for_test(C.int(v))
}

// requestReleasedForTest reports whether the request handler and request of
// the last call raised by raiseAfterRequestForTest were deallocated.
func requestReleasedForTest() bool {
	return C.choix_vision_request_released_for_test() != 0
}

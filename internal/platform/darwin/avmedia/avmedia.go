//go:build darwin

// Package avmedia implements platform.Video on AVFoundation, in process.
//
// Both methods are asynchronous on the native side. Each call takes a
// native slot with platform.Acquire and keeps it — and with it the
// context's platform.Inflight count — until AVFoundation's last completion
// callback for the call has run, even when the Go caller has already
// returned because its context was cancelled. A callback that arrives after
// the caller has gone drops its result. Native code only reads the file and
// returns bytes; it never writes one.
package avmedia

/*
// The deployment target applies to compiling only: on the linker it would
// make ld warn about every object built without it.
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework Foundation -framework AVFoundation -framework CoreMedia -framework CoreGraphics -framework ImageIO -framework UniformTypeIdentifiers
#include <stdlib.h>
#include "avmedia.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime/cgo"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// errNativeCancelled is a cancellation reported by AVFoundation while the
// caller's context is still live.
var errNativeCancelled = errors.New("avmedia: cancelled by AVFoundation")

// stats counts native events for the tests.
var stats struct {
	framesRequested atomic.Int64 // times handed to AVAssetImageGenerator
	frameCallbacks  atomic.Int64 // generator completion callbacks run
	active          atomic.Int32 // calls between taking a slot and their last callback
	maxActive       atomic.Int32 // highest value active has reached
}

// liveRequests returns the number of native request objects not yet
// deallocated.
func liveRequests() int { return int(C.avm_live_requests()) }

// video is the AVFoundation platform.Video.
type video struct{}

// New returns the AVFoundation implementation of platform.Video.
func New() platform.Video { return video{} }

// native is the Go side of one native request: it owns the slot and the
// cgo handle until the last callback.
type native struct {
	release func()
	handle  cgo.Handle
}

// begin records that a call holds a slot.
func begin(release func()) native {
	n := stats.active.Add(1)
	for {
		m := stats.maxActive.Load()
		if n <= m || stats.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	return native{release: release}
}

// finish runs after the call's last callback: it frees the handle and the
// slot.
func (n native) finish() {
	n.handle.Delete()
	stats.active.Add(-1)
	n.release()
}

// absPath makes path absolute for NSURL.
func absPath(path string) (*C.char, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return C.CString(abs), nil
}

// statusError converts a native status to an error.
func statusError(status C.int, msg *C.char) error {
	text := "unknown error"
	if msg != nil {
		text = C.GoString(msg)
	}
	switch status {
	case C.AVM_CANCELLED:
		return errNativeCancelled
	case C.AVM_NO_VIDEO_TRACK:
		return platform.ErrNoVideoTrack
	default:
		return errors.New(text)
	}
}

// ---- Properties ----

type propsCall struct {
	native
	done  chan struct{} // closed by the one callback
	props map[string]any
	err   error
}

// Properties loads duration, tracks and metadata asynchronously and returns
// duration (seconds), width and height (the first video track's natural
// size, not rotated), make, model, software, location (ISO 6709) and
// encoder; absent values are omitted.
func (video) Properties(ctx context.Context, path string) (map[string]any, error) {
	cpath, err := absPath(path)
	if err != nil {
		return nil, fmt.Errorf("avmedia: properties %s: %w", path, err)
	}
	defer C.free(unsafe.Pointer(cpath))
	release, err := platform.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	c := &propsCall{native: begin(release), done: make(chan struct{})}
	c.handle = cgo.NewHandle(c)
	req := C.avm_properties_start(cpath, C.uintptr_t(c.handle))
	defer C.avm_release(req)
	select {
	case <-c.done:
		if c.err != nil {
			if errors.Is(c.err, errNativeCancelled) && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("avmedia: properties %s: %w", path, c.err)
		}
		return c.props, nil
	case <-ctx.Done():
		C.avm_cancel(req)
		return nil, ctx.Err()
	}
}

//export avmPropertiesDone
func avmPropertiesDone(h C.uintptr_t, status C.int, data unsafe.Pointer, n C.size_t, msg *C.char) {
	c := cgo.Handle(h).Value().(*propsCall)
	if status == C.AVM_OK {
		var props map[string]any
		if err := json.Unmarshal(C.GoBytes(data, C.int(n)), &props); err != nil {
			c.err = fmt.Errorf("decode properties: %w", err)
		} else {
			c.props = props
		}
	} else {
		c.err = statusError(status, msg)
	}
	// The channel publishes props and err to the caller, if still there.
	close(c.done)
	c.finish()
}

// ---- Frames ----

type framesCall struct {
	native
	ready chan struct{} // closed once the result is known

	mu        sync.Mutex
	readyOnce bool
	want, got int
	images    []platform.Image
	err       error
	abandoned bool
}

// settle publishes the result once; c.mu must be held.
func (c *framesCall) settle(err error) {
	if c.readyOnce {
		return
	}
	c.readyOnce = true
	c.err = err
	close(c.ready)
}

// Frames asks an AVAssetImageGenerator (preferred track transform applied,
// default tolerances) for one image per fraction of the duration and
// returns each as a full-size JPEG (quality 0.85), in the order of
// fractions. On ctx.Done() it cancels the generation and returns ctx.Err()
// at once; the slot is kept until AVFoundation has called back for every
// requested time.
func (video) Frames(ctx context.Context, path string, fractions []float64) ([]platform.Image, error) {
	for _, f := range fractions {
		if !(f >= 0 && f <= 1) { // also rejects NaN
			return nil, fmt.Errorf("avmedia: frames %s: fraction %v outside [0, 1]", path, f)
		}
	}
	cpath, err := absPath(path)
	if err != nil {
		return nil, fmt.Errorf("avmedia: frames %s: %w", path, err)
	}
	defer C.free(unsafe.Pointer(cpath))
	release, err := platform.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	c := &framesCall{
		native: begin(release),
		ready:  make(chan struct{}),
		images: make([]platform.Image, len(fractions)),
	}
	c.handle = cgo.NewHandle(c)
	var fp *C.double
	if len(fractions) > 0 {
		fp = (*C.double)(unsafe.Pointer(&fractions[0]))
	}
	req := C.avm_frames_start(cpath, fp, C.int(len(fractions)), C.uintptr_t(c.handle))
	defer C.avm_release(req)

	select {
	case <-c.ready:
	case <-ctx.Done():
	}
	c.mu.Lock()
	// After a cancellation the result may already be complete; prefer the
	// context's error so callers see a consistent context.Canceled.
	if ctxErr := ctx.Err(); ctxErr != nil {
		c.abandoned = true
		c.mu.Unlock()
		C.avm_cancel(req)
		return nil, ctxErr
	}
	err = c.err
	images := c.images
	if err != nil {
		// Stop the frames still being generated.
		c.abandoned = true
	}
	c.mu.Unlock()
	if err != nil {
		C.avm_cancel(req)
		return nil, fmt.Errorf("avmedia: frames %s: %w", path, err)
	}
	return images, nil
}

//export avmFramesLoaded
func avmFramesLoaded(h C.uintptr_t, status C.int, count C.int, msg *C.char) {
	c := cgo.Handle(h).Value().(*framesCall)
	if status == C.AVM_OK && count > 0 {
		stats.framesRequested.Add(int64(count))
		c.mu.Lock()
		c.want = int(count)
		c.mu.Unlock()
		return
	}
	// No frame callbacks follow: this was the last one.
	var err error
	if status != C.AVM_OK {
		err = statusError(status, msg)
	}
	c.mu.Lock()
	c.settle(err)
	c.mu.Unlock()
	c.finish()
}

//export avmFrameDone
func avmFrameDone(h C.uintptr_t, index, status C.int, data unsafe.Pointer, n C.size_t, width, height C.int, msg *C.char) {
	stats.frameCallbacks.Add(1)
	c := cgo.Handle(h).Value().(*framesCall)
	c.mu.Lock()
	c.got++
	last := c.got == c.want
	if !c.abandoned && !c.readyOnce {
		switch {
		case status != C.AVM_OK:
			c.settle(fmt.Errorf("frame %d: %w", int(index), statusError(status, msg)))
		case int(index) < 0 || int(index) >= len(c.images):
			c.settle(fmt.Errorf("frame index %d out of range", int(index)))
		default:
			c.images[index] = platform.Image{
				JPEG:   C.GoBytes(data, C.int(n)),
				Width:  int(width),
				Height: int(height),
			}
			if last {
				c.settle(nil)
			}
		}
	}
	if last {
		// A result not yet published here can only belong to an abandoned
		// call; settle anyway so ready is always closed.
		c.settle(errNativeCancelled)
	}
	c.mu.Unlock()
	if last {
		c.finish()
	}
}

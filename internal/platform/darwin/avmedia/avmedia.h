// avmedia.h — the C interface between avmedia.go and avmedia.m.
//
// Every request is asynchronous. A start function returns at once with a
// retained request object (owned by the caller until avm_release) and
// AVFoundation later calls back into Go through the exported functions
// declared in _cgo_export.h:
//
//   avmPropertiesDone(handle, status, json, len, msg)       exactly once
//   avmFramesLoaded(handle, status, count, msg)             exactly once, first
//   avmFrameDone(handle, index, status, jpeg, len, w, h, msg)
//                                                           count times after
//                                                           an AVM_OK load
//
// The callbacks may run on any thread, including — for an immediate
// failure or a cancellation — the thread that called the start or cancel
// function. Buffers passed to a callback are valid only during that call.
#ifndef CHOIX_AVMEDIA_H
#define CHOIX_AVMEDIA_H

#include <stddef.h>
#include <stdint.h>

// Callback status codes.
enum {
	AVM_OK = 0,
	AVM_FAILED = 1,
	AVM_CANCELLED = 2,
	AVM_NO_VIDEO_TRACK = 3,
};

typedef void *avm_request;

// avm_properties_start loads the asset's duration, tracks, common metadata
// and metadata, then the first video track's natural size, and reports a
// JSON object through avmPropertiesDone.
avm_request avm_properties_start(const char *path, uintptr_t handle);

// avm_frames_start loads the asset's duration and tracks, then asks an
// AVAssetImageGenerator for one image per fraction of the duration. The
// fractions are copied before it returns.
avm_request avm_frames_start(const char *path, const double *fractions, int count, uintptr_t handle);

// avm_cancel cancels loading and image generation. Callbacks still arrive,
// with AVM_CANCELLED, for everything not yet reported.
void avm_cancel(avm_request req);

// avm_release drops the caller's reference to the request.
void avm_release(avm_request req);

// avm_live_requests counts request objects not yet deallocated (tests).
int avm_live_requests(void);

#endif

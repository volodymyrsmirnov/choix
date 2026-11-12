//go:build darwin

// Vision image feature prints. Compiled with ARC, without
// -fobjc-arc-exceptions (cgo refuses the flag), so ARC does not release a
// strong local whose scope an exception unwinds: the entry point declares its
// Objective-C objects outside its @try and catches exceptions in its own
// frame. Core Foundation and Core Graphics objects are released by hand in
// @finally blocks. The entry point also runs inside guarded(): an autorelease
// pool (the calling thread is a Go thread with no pool of its own) and an
// @try that turns any other Objective-C exception into a CHOIX_VISION_FAILED
// result instead of letting it unwind through cgo.

#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ImageIO/ImageIO.h>
#import <Vision/Vision.h>
#include <stdlib.h>
#include <string.h>

#include "vision.h"

// Test hook (choix_vision_raise_after_request_for_test): when set, the entry
// point raises once its handler and request exist, after recording weak
// references to them.
static volatile int raiseAfterRequest = 0;
static __weak id weakHandler = nil;
static __weak id weakRequest = nil;

static ChoixVisionResult resultFailed(NSString *message) {
	ChoixVisionResult r = {0};
	r.status = CHOIX_VISION_FAILED;
	const char *s = message.UTF8String;
	r.message = strdup(s != NULL ? s : "unknown error");
	return r;
}

static ChoixVisionResult resultUnreadable(void) {
	ChoixVisionResult r = {0};
	r.status = CHOIX_VISION_UNREADABLE;
	return r;
}

// resultException is the CHOIX_VISION_FAILED result for a caught
// Objective-C exception.
static ChoixVisionResult resultException(id e) {
	if ([e isKindOfClass:[NSException class]]) {
		NSException *ex = e;
		return resultFailed([NSString stringWithFormat:@"Objective-C exception %@: %@", ex.name, ex.reason]);
	}
	return resultFailed([NSString stringWithFormat:@"Objective-C exception: %@", e]);
}

static ChoixVisionResult guarded(ChoixVisionResult (^body)(void)) {
	ChoixVisionResult r = {0};
	@autoreleasepool {
		@try {
			r = body();
		} @catch (id e) {
			r = resultException(e);
		}
	}
	return r;
}

// createSource opens path, or a copy of the len bytes at data, without
// caching decoded pixels. ImageIO opens a file read-only. The URL or data is
// released in @finally, so an exception leaves nothing behind.
static CGImageSourceRef createSource(const char *path, const void *data, size_t len) {
	CFURLRef url = NULL;
	CFDataRef bytes = NULL;
	@try {
		NSDictionary *opts = @{(__bridge NSString *)kCGImageSourceShouldCache : @NO};
		if (path != NULL) {
			url = CFURLCreateFromFileSystemRepresentation(NULL, (const UInt8 *)path, (CFIndex)strlen(path), false);
			if (url == NULL) {
				return NULL;
			}
			return CGImageSourceCreateWithURL(url, (__bridge CFDictionaryRef)opts);
		}
		bytes = CFDataCreate(NULL, data, (CFIndex)len);
		if (bytes == NULL) {
			return NULL;
		}
		return CGImageSourceCreateWithData(bytes, (__bridge CFDictionaryRef)opts);
	} @finally {
		if (url != NULL) {
			CFRelease(url);
		}
		if (bytes != NULL) {
			CFRelease(bytes);
		}
	}
}

// readable reports whether ImageIO recognised src as an image file.
static BOOL readable(CGImageSourceRef src) {
	return src != NULL && CGImageSourceGetType(src) != NULL && CGImageSourceGetCount(src) > 0;
}

// orientationOf returns image 0's EXIF orientation (1–8), 1 when absent.
static CGImagePropertyOrientation orientationOf(CGImageSourceRef src) {
	CFDictionaryRef props = CGImageSourceCopyPropertiesAtIndex(src, 0, NULL);
	if (props == NULL) {
		return kCGImagePropertyOrientationUp;
	}
	long o = 0;
	CFTypeRef v = CFDictionaryGetValue(props, kCGImagePropertyOrientation);
	if (v != NULL && CFGetTypeID(v) == CFNumberGetTypeID()) {
		CFNumberGetValue((CFNumberRef)v, kCFNumberLongType, &o);
	}
	CFRelease(props);
	if (o < 1 || o > 8) {
		return kCGImagePropertyOrientationUp;
	}
	return (CGImagePropertyOrientation)o;
}

ChoixVisionResult choix_vision_feature_print(const char *path, const void *data, size_t len,
	int revision, size_t want_len) {
	return guarded(^ChoixVisionResult {
		CGImageSourceRef src = NULL;
		CGImageRef img = NULL;
		// The ARC-owned objects are declared outside the @try and an
		// exception is caught in this frame, so their scope always ends
		// normally and ARC releases them. ARC releases nothing whose scope an
		// exception unwinds unless the file is built with
		// -fobjc-arc-exceptions, which cgo's flag allowlist refuses.
		NSDictionary *options = nil;
		VNImageRequestHandler *handler = nil;
		VNGenerateImageFeaturePrintRequest *req = nil;
		NSArray<VNRequest *> *requests = nil;
		VNFeaturePrintObservation *obs = nil;
		NSError *err = nil;
		@try {
			if (revision <= 0 || ![VNGenerateImageFeaturePrintRequest.supportedRevisions containsIndex:(NSUInteger)revision]) {
				return resultFailed([NSString stringWithFormat:@"feature print revision %d is not supported", revision]);
			}
			src = createSource(path, data, len);
			if (!readable(src)) {
				return resultUnreadable();
			}
			CGImagePropertyOrientation o = orientationOf(src);
			img = CGImageSourceCreateImageAtIndex(src, 0, NULL);
			if (img == NULL) {
				return resultUnreadable();
			}

			options = @{};
			handler = [[VNImageRequestHandler alloc] initWithCGImage:img orientation:o options:options];
			req = [[VNGenerateImageFeaturePrintRequest alloc] init];
			req.revision = (NSUInteger)revision;
			req.imageCropAndScaleOption = VNImageCropAndScaleOptionScaleFill;
			requests = @[ req ];
			if (![handler performRequests:requests error:&err]) {
				return resultFailed([NSString stringWithFormat:@"feature print: %@", err.localizedDescription ?: @"unknown error"]);
			}
			if (raiseAfterRequest) {
				weakHandler = handler;
				weakRequest = req;
				[NSException raise:@"ChoixTestException" format:@"raised after the request for a test"];
			}
			obs = req.results.firstObject;
			if (obs == nil) {
				return resultFailed(@"feature print: no observation");
			}
			if (obs.requestRevision != (NSUInteger)revision) {
				return resultFailed([NSString stringWithFormat:@"feature print ran at revision %lu, want %d",
					(unsigned long)obs.requestRevision, revision]);
			}
			if (obs.elementType != VNElementTypeFloat) {
				return resultFailed([NSString stringWithFormat:@"feature print element type %ld is not float", (long)obs.elementType]);
			}
			if (obs.elementCount != want_len || obs.data.length != want_len * sizeof(float)) {
				return resultFailed([NSString stringWithFormat:@"feature print has %lu elements (%lu bytes), want %zu",
					(unsigned long)obs.elementCount, (unsigned long)obs.data.length, want_len]);
			}
			ChoixVisionResult r = {0};
			r.values = malloc(want_len * sizeof(float));
			if (r.values == NULL) {
				return resultFailed(@"out of memory");
			}
			memcpy(r.values, obs.data.bytes, want_len * sizeof(float));
			r.len = want_len;
			r.revision = (int)obs.requestRevision;
			return r;
		} @catch (id e) {
			return resultException(e);
		} @finally {
			if (img != NULL) {
				CGImageRelease(img);
			}
			if (src != NULL) {
				CFRelease(src);
			}
		}
	});
}

ChoixVisionResult choix_vision_raise_for_test(const char *name) {
	return guarded(^ChoixVisionResult {
		NSString *exceptionName = (name != NULL ? [NSString stringWithUTF8String:name] : nil) ?: @"ChoixTestException";
		[NSException raise:exceptionName format:@"raised for a test"];
		return resultUnreadable();
	});
}

void choix_vision_raise_after_request_for_test(int on) {
	raiseAfterRequest = on;
	if (on) {
		weakHandler = nil;
		weakRequest = nil;
	}
}

int choix_vision_request_released_for_test(void) {
	return weakHandler == nil && weakRequest == nil;
}

//go:build darwin

// App-scoped security-scoped bookmarks on NSURL. Compiled with ARC. Every
// entry point runs inside guarded(): an autorelease pool (the calling thread
// is a Go thread with no pool of its own) and an @try that turns an
// Objective-C exception into a CHOIX_BOOKMARK_FAILED result instead of
// letting it unwind through cgo. Started security-scoped access is ended in
// @finally blocks, so an exception never leaves access running.

#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

#include "bookmark.h"

static const NSURLBookmarkResolutionOptions resolveOptions =
	NSURLBookmarkResolutionWithSecurityScope | NSURLBookmarkResolutionWithoutUI |
	NSURLBookmarkResolutionWithoutMounting;

static char *copyString(NSString *s) {
	const char *u = s.UTF8String;
	return strdup(u != NULL ? u : "");
}

static ChoixBookmarkResult resultFailed(NSString *message) {
	ChoixBookmarkResult r = {0};
	r.status = CHOIX_BOOKMARK_FAILED;
	r.message = copyString(message ?: @"unknown error");
	return r;
}

static ChoixBookmarkResult resultError(NSError *error, NSString *fallback) {
	if (error == nil) {
		return resultFailed(fallback);
	}
	ChoixBookmarkResult r = {0};
	r.status = CHOIX_BOOKMARK_NSERROR;
	r.domain = copyString(error.domain);
	r.code = (long)error.code;
	r.message = copyString(error.localizedDescription ?: fallback);
	return r;
}

// resultPath returns an OK result holding url's file-system path.
static ChoixBookmarkResult resultPath(NSURL *url, BOOL stale) {
	const char *p = url.fileSystemRepresentation;
	if (p == NULL) {
		return resultFailed(@"resolved bookmark has no file-system path");
	}
	ChoixBookmarkResult r = {0};
	r.path = strdup(p);
	if (r.path == NULL) {
		return resultFailed(@"out of memory");
	}
	r.stale = stale ? 1 : 0;
	return r;
}

// resultBytes returns an OK result holding a malloc'd copy of data.
static ChoixBookmarkResult resultBytes(NSData *data) {
	ChoixBookmarkResult r = {0};
	r.bytes = malloc(data.length > 0 ? data.length : 1);
	if (r.bytes == NULL) {
		return resultFailed(@"out of memory");
	}
	memcpy(r.bytes, data.bytes, data.length);
	r.len = data.length;
	return r;
}

static ChoixBookmarkResult guarded(ChoixBookmarkResult (^body)(void)) {
	ChoixBookmarkResult r = {0};
	@autoreleasepool {
		@try {
			r = body();
		} @catch (NSException *e) {
			r = resultFailed([NSString stringWithFormat:@"Objective-C exception %@: %@", e.name, e.reason]);
		} @catch (id e) {
			r = resultFailed([NSString stringWithFormat:@"Objective-C exception: %@", e]);
		}
	}
	return r;
}

// resolveURL resolves the len bytes at data. On failure it returns nil and
// sets *error.
static NSURL *resolveURL(const void *data, size_t len, BOOL *stale, NSError *__autoreleasing *error) {
	NSData *bytes = (data != NULL && len > 0) ? [NSData dataWithBytes:data length:len] : [NSData data];
	*stale = NO;
	return [NSURL URLByResolvingBookmarkData:bytes
	                                 options:resolveOptions
	                           relativeToURL:nil
	                     bookmarkDataIsStale:stale
	                                   error:error];
}

// createData makes a security-scoped bookmark of url, retrying with options
// 0 when that fails and unsandboxed is set. On failure it returns nil and
// sets *error to the last attempt's error.
static NSData *createData(NSURL *url, int unsandboxed, NSError *__autoreleasing *error) {
	NSData *data = [url bookmarkDataWithOptions:NSURLBookmarkCreationWithSecurityScope
	             includingResourceValuesForKeys:nil
	                              relativeToURL:nil
	                                      error:error];
	if (data == nil && unsandboxed) {
		data = [url bookmarkDataWithOptions:0 includingResourceValuesForKeys:nil relativeToURL:nil error:error];
	}
	return data;
}

ChoixBookmarkResult choix_bookmark_create(const char *path, int unsandboxed) {
	return guarded(^ChoixBookmarkResult {
		if (path == NULL || path[0] == '\0') {
			return resultFailed(@"empty path");
		}
		NSString *p = [[NSFileManager defaultManager] stringWithFileSystemRepresentation:path length:strlen(path)];
		NSURL *url = [NSURL fileURLWithPath:p isDirectory:YES];
		NSError *error = nil;
		NSData *data = createData(url, unsandboxed, &error);
		if (data == nil) {
			return resultError(error, @"cannot create bookmark");
		}
		return resultBytes(data);
	});
}

ChoixBookmarkResult choix_bookmark_resolve(const void *data, size_t len) {
	return guarded(^ChoixBookmarkResult {
		BOOL stale = NO;
		NSError *error = nil;
		NSURL *url = resolveURL(data, len, &stale, &error);
		if (url == nil) {
			return resultError(error, @"cannot resolve bookmark");
		}
		return resultPath(url, stale);
	});
}

ChoixBookmarkResult choix_bookmark_start(const void *data, size_t len) {
	return guarded(^ChoixBookmarkResult {
		BOOL stale = NO;
		NSError *error = nil;
		NSURL *url = resolveURL(data, len, &stale, &error);
		if (url == nil) {
			return resultError(error, @"cannot resolve bookmark");
		}
		BOOL started = NO;
		@try {
			started = [url startAccessingSecurityScopedResource];
			ChoixBookmarkResult r = resultPath(url, stale);
			if (r.status != CHOIX_BOOKMARK_OK) {
				return r;
			}
			if (started) {
				r.started = 1;
				r.handle = (void *)CFBridgingRetain(url);
				started = NO; // the caller owns the access now
			}
			return r;
		} @finally {
			if (started) {
				[url stopAccessingSecurityScopedResource];
			}
		}
	});
}

void choix_bookmark_stop(void *handle) {
	if (handle == NULL) {
		return;
	}
	@autoreleasepool {
		NSURL *url = CFBridgingRelease(handle);
		@try {
			[url stopAccessingSecurityScopedResource];
		} @catch (id ignored) {
			// Nothing to report to: the URL is released either way.
			(void)ignored;
		}
	}
}

ChoixBookmarkResult choix_bookmark_refresh(const void *data, size_t len, int unsandboxed) {
	return guarded(^ChoixBookmarkResult {
		BOOL stale = NO;
		NSError *error = nil;
		NSURL *url = resolveURL(data, len, &stale, &error);
		if (url == nil) {
			return resultError(error, @"cannot resolve bookmark");
		}
		BOOL started = NO;
		@try {
			started = [url startAccessingSecurityScopedResource];
			if (!started && !unsandboxed) {
				ChoixBookmarkResult r = resultFailed(@"security-scoped access refused");
				r.status = CHOIX_BOOKMARK_REFUSED;
				return r;
			}
			NSData *fresh = createData(url, unsandboxed, &error);
			if (fresh == nil) {
				return resultError(error, @"cannot create bookmark");
			}
			return resultBytes(fresh);
		} @finally {
			if (started) {
				[url stopAccessingSecurityScopedResource];
			}
		}
	});
}

ChoixBookmarkResult choix_bookmark_raise_for_test(const char *name) {
	return guarded(^ChoixBookmarkResult {
		NSString *exceptionName = (name != NULL ? [NSString stringWithUTF8String:name] : nil) ?: @"ChoixTestException";
		[NSException raise:exceptionName format:@"raised for a test"];
		return resultFailed(@"not reached");
	});
}

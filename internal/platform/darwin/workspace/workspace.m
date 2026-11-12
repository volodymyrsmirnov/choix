//go:build darwin

// NSWorkspace calls for Finder and the browser. Compiled with ARC. Every
// entry point runs inside guarded(): an autorelease pool (a Go thread has no
// pool of its own) and an @try that turns an Objective-C exception into a
// failure message instead of letting it unwind through cgo. The AppKit call
// itself runs on the main queue inside its own guarded(), so an exception
// there is caught on the main thread and still reported to the caller.

#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#include <dispatch/dispatch.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#include "workspace.h"

static char *failure(NSString *message) {
	const char *s = message.UTF8String;
	return strdup(s != NULL ? s : "unknown error");
}

static char *guarded(char *(^body)(void)) {
	char *r = NULL;
	@autoreleasepool {
		@try {
			r = body();
		} @catch (NSException *e) {
			r = failure([NSString stringWithFormat:@"Objective-C exception %@: %@", e.name, e.reason]);
		} @catch (id e) {
			r = failure([NSString stringWithFormat:@"Objective-C exception: %@", e]);
		}
	}
	return r;
}

// urlFrom returns the NSURL for raw, or nil when raw is NULL, not UTF-8 or
// not a URL.
static NSURL *urlFrom(const char *raw) {
	if (raw == NULL) {
		return nil;
	}
	NSString *s = [NSString stringWithUTF8String:raw];
	if (s == nil) {
		return nil;
	}
	return [NSURL URLWithString:s];
}

// perform makes the AppKit call. It runs on the main thread.
static char *perform(int op, NSURL *url) {
	NSWorkspace *ws = [NSWorkspace sharedWorkspace];
	switch (op) {
	case CHOIX_WORKSPACE_REVEAL:
		[ws activateFileViewerSelectingURLs:@[ url ]];
		return NULL;
	case CHOIX_WORKSPACE_OPEN:
		if (![ws openURL:url]) {
			return failure(@"NSWorkspace did not open the URL");
		}
		return NULL;
	default:
		return failure([NSString stringWithFormat:@"unknown operation %d", op]);
	}
}

char *choix_workspace_run(int op, const char *raw_url) {
	return guarded(^char * {
		NSURL *url = urlFrom(raw_url);
		if (url == nil) {
			return failure(@"not a valid URL");
		}
		__block char *result = NULL;
		void (^call)(void) = ^{
			result = guarded(^char * {
				return perform(op, url);
			});
		};
		if (pthread_main_np()) {
			// Already on the main thread: dispatching and waiting would
			// deadlock.
			call();
			return result;
		}
		// Wait, without a timeout, until the block has run: returning
		// earlier would let the caller release its access lease while the
		// call is still queued. The semaphore orders the block's write of
		// result before the read below.
		dispatch_semaphore_t done = dispatch_semaphore_create(0);
		dispatch_async(dispatch_get_main_queue(), ^{
			call();
			dispatch_semaphore_signal(done);
		});
		dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
		return result;
	});
}

char *choix_workspace_url_path_for_test(const char *raw_url, char **path) {
	*path = NULL;
	return guarded(^char * {
		NSURL *url = urlFrom(raw_url);
		if (url == nil) {
			return failure(@"not a valid URL");
		}
		// path, not fileSystemRepresentation: the latter decomposes
		// Unicode, which would hide whether the bytes survived the URL.
		const char *p = url.path.UTF8String;
		if (p == NULL) {
			return failure(@"URL has no path");
		}
		*path = strdup(p);
		return NULL;
	});
}

char *choix_workspace_raise_for_test(void) {
	return guarded(^char * {
		[NSException raise:@"ChoixTestException" format:@"raised for a test"];
		return NULL;
	});
}

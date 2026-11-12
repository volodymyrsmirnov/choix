//go:build darwin

// Volume state and Trash membership through Foundation. Compiled with ARC;
// no Core Foundation object is created by hand, so nothing needs a manual
// release. Every entry point runs inside guarded(): an autorelease pool (the
// calling thread is a Go thread with no pool of its own) and an @try that
// turns an Objective-C exception into a CHOIX_VOLUMES_FAILED result instead
// of letting it unwind through cgo.

#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

#include "volumes.h"

// copyString returns a malloc'd copy of s, or NULL for nil.
static char *copyString(NSString *s) {
	if (s == nil) {
		return NULL;
	}
	const char *u = s.UTF8String;
	return u != NULL ? strdup(u) : NULL;
}

static ChoixVolumesResult resultFailed(NSString *message) {
	ChoixVolumesResult r = {0};
	r.status = CHOIX_VOLUMES_FAILED;
	r.message = copyString(message);
	if (r.message == NULL) {
		r.message = strdup("unknown error");
	}
	return r;
}

// describeError renders err with its domain, code and underlying errors so
// the Go side can tell, say, a missing Trash (POSIX 2) from a denial.
static NSString *describeError(NSError *err) {
	if (err == nil) {
		return @"unknown error";
	}
	NSMutableString *s = [NSMutableString stringWithFormat:@"%@ (%@ %ld", err.localizedDescription, err.domain, (long)err.code];
	NSError *under = err.userInfo[NSUnderlyingErrorKey];
	while ([under isKindOfClass:[NSError class]]) {
		[s appendFormat:@"; underlying %@ %ld", under.domain, (long)under.code];
		under = under.userInfo[NSUnderlyingErrorKey];
	}
	[s appendString:@")"];
	return s;
}

static ChoixVolumesResult resultError(NSError *err) {
	return resultFailed(describeError(err));
}

static ChoixVolumesResult guarded(ChoixVolumesResult (^body)(void)) {
	ChoixVolumesResult r = {0};
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

// fileURL returns a file URL for the file-system path p.
static NSURL *fileURL(const char *p) {
	return [NSURL fileURLWithFileSystemRepresentation:p isDirectory:NO relativeToURL:nil];
}

ChoixVolumesResult choix_volumes_info(const char *path) {
	return guarded(^ChoixVolumesResult {
		NSURL *url = fileURL(path);
		NSError *err = nil;
		NSDictionary<NSURLResourceKey, id> *values = [url resourceValuesForKeys:@[
			NSURLVolumeUUIDStringKey,
			NSURLVolumeLocalizedNameKey,
			NSURLVolumeIsReadOnlyKey,
			NSURLVolumeIsRemovableKey,
		] error:&err];
		if (values == nil) {
			return resultError(err);
		}
		// Everything that could raise runs before the first strdup, so an
		// exception cannot leak a C string.
		ChoixVolumesResult r = {0};
		id uuid = values[NSURLVolumeUUIDStringKey];
		id name = values[NSURLVolumeLocalizedNameKey];
		id readOnly = values[NSURLVolumeIsReadOnlyKey];
		id removable = values[NSURLVolumeIsRemovableKey];
		r.read_only = [readOnly isKindOfClass:[NSNumber class]] && [readOnly boolValue] ? 1 : 0;
		r.removable = [removable isKindOfClass:[NSNumber class]] && [removable boolValue] ? 1 : 0;
		BOOL hasUUID = [uuid isKindOfClass:[NSString class]];
		BOOL hasName = [name isKindOfClass:[NSString class]];
		r.uuid = hasUUID ? copyString(uuid) : NULL;
		r.name = hasName ? copyString(name) : NULL;
		return r;
	});
}

ChoixVolumesResult choix_volumes_mounted(const char *uuid) {
	return guarded(^ChoixVolumesResult {
		NSString *want = [NSString stringWithUTF8String:uuid];
		ChoixVolumesResult r = {0};
		if (want == nil || want.length == 0) {
			return r;
		}
		NSArray<NSURL *> *mounted = [[NSFileManager defaultManager]
			mountedVolumeURLsIncludingResourceValuesForKeys:@[NSURLVolumeUUIDStringKey]
			                                        options:0];
		for (NSURL *vol in mounted) {
			id got = nil;
			if (![vol getResourceValue:&got forKey:NSURLVolumeUUIDStringKey error:NULL]) {
				continue;
			}
			if ([got isKindOfClass:[NSString class]] && [want isEqualToString:got]) {
				r.flag = 1;
				break;
			}
		}
		return r;
	});
}

ChoixVolumesResult choix_volumes_in_trash(const char *path) {
	return guarded(^ChoixVolumesResult {
		NSURLRelationship rel = NSURLRelationshipOther;
		NSError *err = nil;
		BOOL ok = [[NSFileManager defaultManager] getRelationship:&rel
		                                              ofDirectory:NSTrashDirectory
		                                                 inDomain:0
		                                              toItemAtURL:fileURL(path)
		                                                    error:&err];
		if (!ok) {
			return resultError(err);
		}
		ChoixVolumesResult r = {0};
		r.flag = rel == NSURLRelationshipContains ? 1 : 0;
		return r;
	});
}

ChoixVolumesResult choix_volumes_raise_for_test(const char *name) {
	return guarded(^ChoixVolumesResult {
		[NSException raise:[NSString stringWithUTF8String:name] format:@"raised for a test"];
		return (ChoixVolumesResult){0};
	});
}

//go:build darwin

// avmedia.m — AVFoundation metadata and keyframes for avmedia.go.
//
// Ownership: every completion block captures the request, the asset and,
// for frames, the generator strongly, so they live until the last block has
// run and been released, whether or not the Go caller is still waiting.
// Every block body runs inside @autoreleasepool (AVFoundation calls them on
// its own dispatch queues), as does every entry point Go calls.

#if !__has_feature(objc_arc)
#error "avmedia.m must be compiled with -fobjc-arc"
#endif

#import <AVFoundation/AVFoundation.h>
#import <CoreMedia/CoreMedia.h>
#import <Foundation/Foundation.h>
#import <ImageIO/ImageIO.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <math.h>
#include <stdatomic.h>

#include "_cgo_export.h"
#include "avmedia.h"

static atomic_int liveRequests;

int avm_live_requests(void) { return atomic_load(&liveRequests); }
// AVMRequest is the native side of one Properties or Frames call. asset and
// generator are read under @synchronized(self) so avm_cancel never misses a
// generation that is being started.
@interface AVMRequest : NSObject {
@public
	uintptr_t handle;
	atomic_bool cancelled;
	AVURLAsset *asset;
	AVAssetImageGenerator *generator;
	// Frames only: the requested times, which of them are still awaiting a
	// callback, and how many callbacks are outstanding.
	CMTime *times;
	int timeCount;
	NSMutableIndexSet *pending;
}
@end

@implementation AVMRequest
- (instancetype)init {
	self = [super init];
	if (self) {
		atomic_init(&cancelled, false);
		atomic_fetch_add(&liveRequests, 1);
	}
	return self;
}
- (void)dealloc {
	free(times);
	atomic_fetch_sub(&liveRequests, 1);
}
@end

// errorText describes err with its domain, code and underlying error.
static NSString *errorText(NSError *err) {
	if (err == nil) {
		return @"unknown error";
	}
	NSString *text = [NSString stringWithFormat:@"%@ (%@ %ld)", err.localizedDescription, err.domain, (long)err.code];
	NSError *under = err.userInfo[NSUnderlyingErrorKey];
	if (under != nil) {
		text = [text stringByAppendingFormat:@": %@ (%@ %ld)", under.localizedDescription, under.domain, (long)under.code];
	}
	return text;
}

// loadStatus folds the load status of every key into one callback status.
static int loadStatus(id<AVAsynchronousKeyValueLoading> obj, NSArray<NSString *> *keys, NSString **msg) {
	for (NSString *key in keys) {
		NSError *err = nil;
		AVKeyValueStatus st = [obj statusOfValueForKey:key error:&err];
		switch (st) {
		case AVKeyValueStatusLoaded:
			break;
		case AVKeyValueStatusCancelled:
			*msg = @"cancelled";
			return AVM_CANCELLED;
		case AVKeyValueStatusFailed:
			*msg = [NSString stringWithFormat:@"load %@: %@", key, errorText(err)];
			return AVM_FAILED;
		default:
			*msg = [NSString stringWithFormat:@"load %@: status %ld", key, (long)st];
			return AVM_FAILED;
		}
	}
	return AVM_OK;
}

static NSURL *fileURL(const char *path) {
	return [NSURL fileURLWithFileSystemRepresentation:path isDirectory:NO relativeToURL:nil];
}

static AVAssetTrack *firstVideoTrack(AVAsset *asset) {
	for (AVAssetTrack *track in asset.tracks) {
		if ([track.mediaType isEqualToString:AVMediaTypeVideo]) {
			return track;
		}
	}
	return nil;
}

// ---- Properties ----

// identifierString returns the first non-empty string value of an item
// with the identifier.
static NSString *identifierString(NSArray<AVMetadataItem *> *items, AVMetadataIdentifier ident) {
	for (AVMetadataItem *item in [AVMetadataItem metadataItemsFromArray:items filteredByIdentifier:ident]) {
		NSString *s = item.stringValue;
		if (s.length > 0) {
			return s;
		}
	}
	return nil;
}

// commonString returns the first non-empty string value of an item whose
// common key is key.
static NSString *commonString(NSArray<AVMetadataItem *> *items, AVMetadataKey key) {
	for (AVMetadataItem *item in items) {
		if ([item.commonKey isEqualToString:key]) {
			NSString *s = item.stringValue;
			if (s.length > 0) {
				return s;
			}
		}
	}
	return nil;
}

// keyString returns the first non-empty string value of an item whose key,
// in any key space, is key — written as in an identifier, after the key
// space and "/" (for example "%A9too" for ©too in "udta/%A9too" and
// "itsk/%A9too").
static NSString *keyString(NSArray<AVMetadataItem *> *items, NSString *key) {
	for (AVMetadataItem *item in items) {
		NSString *ident = item.identifier;
		NSRange slash = [ident rangeOfString:@"/"];
		if (slash.location == NSNotFound || ![[ident substringFromIndex:NSMaxRange(slash)] isEqualToString:key]) {
			continue;
		}
		NSString *s = item.stringValue;
		if (s.length > 0) {
			return s;
		}
	}
	return nil;
}

static void propertiesDone(AVMRequest *req, int status, NSString *msg, NSDictionary *props) {
	if (status == AVM_OK && atomic_load(&req->cancelled)) {
		status = AVM_CANCELLED;
		msg = @"cancelled";
	}
	NSData *json = nil;
	if (status == AVM_OK) {
		NSError *err = nil;
		json = [NSJSONSerialization dataWithJSONObject:props options:0 error:&err];
		if (json == nil) {
			status = AVM_FAILED;
			msg = [NSString stringWithFormat:@"encode properties: %@", errorText(err)];
		}
	}
	avmPropertiesDone(req->handle, status, (void *)json.bytes, json.length, (char *)msg.UTF8String);
}

static void propertiesLoaded(AVMRequest *req, AVURLAsset *asset, NSArray<NSString *> *keys) {
	NSString *msg = nil;
	int status = loadStatus(asset, keys, &msg);
	if (status != AVM_OK) {
		propertiesDone(req, status, msg, nil);
		return;
	}
	NSMutableDictionary *props = [NSMutableDictionary dictionary];
	CMTime duration = asset.duration;
	if (CMTIME_IS_NUMERIC(duration)) {
		Float64 seconds = CMTimeGetSeconds(duration);
		if (isfinite(seconds)) {
			props[@"duration"] = @(seconds);
		}
	}
	NSArray<AVMetadataItem *> *common = asset.commonMetadata;
	NSArray<AVMetadataItem *> *meta = asset.metadata;
	struct {
		NSString *name;
		AVMetadataKey common;
		AVMetadataIdentifier quicktime;
	} fields[] = {
		{@"make", AVMetadataCommonKeyMake, AVMetadataIdentifierQuickTimeMetadataMake},
		{@"model", AVMetadataCommonKeyModel, AVMetadataIdentifierQuickTimeMetadataModel},
		{@"software", AVMetadataCommonKeySoftware, AVMetadataIdentifierQuickTimeMetadataSoftware},
		{@"location", AVMetadataCommonKeyLocation, AVMetadataIdentifierQuickTimeMetadataLocationISO6709},
	};
	for (size_t i = 0; i < sizeof(fields) / sizeof(fields[0]); i++) {
		NSString *s = commonString(common, fields[i].common);
		if (s == nil) {
			s = identifierString(meta, fields[i].quicktime);
		}
		if (s != nil) {
			props[fields[i].name] = s;
		}
	}
	// ©enc, then ©too, by key in every key space: cameras write them as
	// QuickTime user data (udta) or as iTunes items (itsk).
	NSString *encoder = keyString(meta, @"%A9enc");
	if (encoder == nil) {
		encoder = keyString(meta, @"%A9too");
	}
	if (encoder != nil) {
		props[@"encoder"] = encoder;
	}

	AVAssetTrack *track = firstVideoTrack(asset);
	if (track == nil) {
		propertiesDone(req, AVM_OK, nil, props);
		return;
	}
	BOOL cancelled;
	@synchronized(req) {
		cancelled = atomic_load(&req->cancelled);
		// From here avm_cancel no longer cancels the asset's loading: the
		// track load must report back, and -cancelLoading is not documented
		// to reach it (see avm_cancel). It is short; the flag is checked
		// when it ends.
		req->asset = nil;
	}
	if (cancelled) {
		propertiesDone(req, AVM_CANCELLED, @"cancelled", nil);
		return;
	}
	NSArray<NSString *> *trackKeys = @[ @"naturalSize" ];
	[track loadValuesAsynchronouslyForKeys:trackKeys
	                     completionHandler:^{
		                     @autoreleasepool {
			                     (void)asset; // keep the asset alive with its track
			                     NSString *tmsg = nil;
			                     int tstatus = loadStatus(track, trackKeys, &tmsg);
			                     if (tstatus == AVM_OK) {
				                     CGSize size = track.naturalSize;
				                     props[@"width"] = @(size.width);
				                     props[@"height"] = @(size.height);
			                     }
			                     propertiesDone(req, tstatus, tmsg, props);
		                     }
	                     }];
}

avm_request avm_properties_start(const char *path, uintptr_t handle) {
	@autoreleasepool {
		AVMRequest *req = [[AVMRequest alloc] init];
		req->handle = handle;
		AVURLAsset *asset = [AVURLAsset URLAssetWithURL:fileURL(path) options:nil];
		@synchronized(req) {
			req->asset = asset;
		}
		NSArray<NSString *> *keys = @[ @"duration", @"tracks", @"commonMetadata", @"metadata" ];
		[asset loadValuesAsynchronouslyForKeys:keys
		                     completionHandler:^{
			                     @autoreleasepool {
				                     propertiesLoaded(req, asset, keys);
			                     }
		                     }];
		return (__bridge_retained void *)req;
	}
}

// ---- Frames ----

static NSData *encodeJPEG(CGImageRef image) {
	NSMutableData *buf = [NSMutableData data];
	CGImageDestinationRef dst = CGImageDestinationCreateWithData((__bridge CFMutableDataRef)buf,
	                                                             (__bridge CFStringRef)UTTypeJPEG.identifier, 1, NULL);
	if (dst == NULL) {
		return nil;
	}
	NSDictionary *opts = @{(__bridge NSString *)kCGImageDestinationLossyCompressionQuality : @0.85};
	CGImageDestinationAddImage(dst, image, (__bridge CFDictionaryRef)opts);
	BOOL ok = CGImageDestinationFinalize(dst);
	CFRelease(dst);
	return ok ? buf : nil;
}

static void framesLoadFailed(AVMRequest *req, int status, NSString *msg) {
	avmFramesLoaded(req->handle, status, 0, (char *)msg.UTF8String);
}

// frameDone reports one generator callback. It runs once per requested time,
// cancelled ones included.
static void frameDone(AVMRequest *req, CMTime requested, CGImageRef image, AVAssetImageGeneratorResult result,
                      NSError *error) {
	// Match the callback to the first pending index with that time; equal
	// fractions give equal times, and each gets one callback.
	NSUInteger index = NSNotFound;
	BOOL matched = NO;
	@synchronized(req) {
		for (NSUInteger i = req->pending.firstIndex; i != NSNotFound; i = [req->pending indexGreaterThanIndex:i]) {
			if (CMTimeCompare(req->times[i], requested) == 0) {
				index = i;
				matched = YES;
				break;
			}
		}
		if (!matched) {
			// A time we did not ask for: count it against the first pending
			// request so Go still sees exactly one callback per time.
			index = req->pending.firstIndex;
		}
		if (index != NSNotFound) {
			[req->pending removeIndex:index];
		}
		if (req->pending.count == 0) {
			// The last callback: drop the request's references so nothing
			// forms a cycle; this block still holds what it captured.
			req->generator = nil;
			req->asset = nil;
		}
	}
	if (index == NSNotFound) {
		// More callbacks than requested times; Go has already been told
		// about every one, so there is nobody to report this to.
		return;
	}
	if (!matched) {
		avmFrameDone(req->handle, (int)index, AVM_FAILED, NULL, 0, 0, 0, (char *)"unexpected frame time");
		return;
	}
	switch (result) {
	case AVAssetImageGeneratorSucceeded: {
		if (atomic_load(&req->cancelled)) {
			// The caller has gone; skip the encoding.
			avmFrameDone(req->handle, (int)index, AVM_CANCELLED, NULL, 0, 0, 0, (char *)"cancelled");
			return;
		}
		NSData *jpeg = encodeJPEG(image);
		if (jpeg == nil) {
			avmFrameDone(req->handle, (int)index, AVM_FAILED, NULL, 0, 0, 0, (char *)"encode JPEG failed");
			return;
		}
		avmFrameDone(req->handle, (int)index, AVM_OK, (void *)jpeg.bytes, jpeg.length, (int)CGImageGetWidth(image),
		             (int)CGImageGetHeight(image), NULL);
		return;
	}
	case AVAssetImageGeneratorCancelled:
		avmFrameDone(req->handle, (int)index, AVM_CANCELLED, NULL, 0, 0, 0, (char *)"cancelled");
		return;
	default: {
		NSString *msg = errorText(error);
		avmFrameDone(req->handle, (int)index, AVM_FAILED, NULL, 0, 0, 0, (char *)msg.UTF8String);
		return;
	}
	}
}

static void framesLoaded(AVMRequest *req, AVURLAsset *asset, NSArray<NSString *> *keys, NSData *fractions) {
	NSString *msg = nil;
	int status = loadStatus(asset, keys, &msg);
	if (status != AVM_OK) {
		framesLoadFailed(req, status, msg);
		return;
	}
	if (firstVideoTrack(asset) == nil) {
		framesLoadFailed(req, AVM_NO_VIDEO_TRACK, @"no video track");
		return;
	}
	CMTime duration = asset.duration;
	if (!CMTIME_IS_NUMERIC(duration)) {
		framesLoadFailed(req, AVM_FAILED, @"unknown duration");
		return;
	}
	int count = (int)(fractions.length / sizeof(double));
	const double *f = fractions.bytes;
	NSMutableArray<NSValue *> *values = [NSMutableArray arrayWithCapacity:(NSUInteger)count];
	CMTime *times = calloc((size_t)(count > 0 ? count : 1), sizeof(CMTime));
	if (times == NULL) {
		framesLoadFailed(req, AVM_FAILED, @"out of memory");
		return;
	}
	for (int i = 0; i < count; i++) {
		times[i] = CMTimeMultiplyByFloat64(duration, f[i]);
		[values addObject:[NSValue valueWithCMTime:times[i]]];
	}
	AVAssetImageGenerator *gen = [AVAssetImageGenerator assetImageGeneratorWithAsset:asset];
	gen.appliesPreferredTrackTransform = YES;

	@synchronized(req) {
		req->times = times;
		req->timeCount = count;
		req->pending = [NSMutableIndexSet indexSetWithIndexesInRange:NSMakeRange(0, (NSUInteger)count)];
		if (atomic_load(&req->cancelled)) {
			framesLoadFailed(req, AVM_CANCELLED, @"cancelled");
			return;
		}
		// Tell Go how many frame callbacks follow before the first can run.
		avmFramesLoaded(req->handle, AVM_OK, count, NULL);
		if (count == 0) {
			return;
		}
		req->generator = gen;
		[gen generateCGImagesAsynchronouslyForTimes:values
		                          completionHandler:^(CMTime requested, CGImageRef image, CMTime actual,
		                                              AVAssetImageGeneratorResult result, NSError *error) {
			                          @autoreleasepool {
				                          (void)gen; // the generator and asset live until
				                          (void)asset; // the last callback has run
				                          (void)actual;
				                          frameDone(req, requested, image, result, error);
			                          }
		                          }];
	}
}

avm_request avm_frames_start(const char *path, const double *fractions, int count, uintptr_t handle) {
	@autoreleasepool {
		AVMRequest *req = [[AVMRequest alloc] init];
		req->handle = handle;
		NSData *copied = [NSData dataWithBytes:fractions length:(NSUInteger)(count > 0 ? count : 0) * sizeof(double)];
		AVURLAsset *asset = [AVURLAsset URLAssetWithURL:fileURL(path) options:nil];
		@synchronized(req) {
			req->asset = asset;
		}
		NSArray<NSString *> *keys = @[ @"duration", @"tracks" ];
		[asset loadValuesAsynchronouslyForKeys:keys
		                     completionHandler:^{
			                     @autoreleasepool {
				                     framesLoaded(req, asset, keys, copied);
			                     }
		                     }];
		return (__bridge_retained void *)req;
	}
}

// ---- Cancel / release ----

void avm_cancel(avm_request r) {
	@autoreleasepool {
		AVMRequest *req = (__bridge AVMRequest *)r;
		atomic_store(&req->cancelled, true);
		AVURLAsset *asset;
		AVAssetImageGenerator *gen;
		@synchronized(req) {
			asset = req->asset;
			gen = req->generator;
		}
		// Outside the lock: either call may run the handlers synchronously.
		// Once generation has started, only the generator is cancelled:
		// -[AVAsset cancelLoading] during generation can make the generator
		// drop its pending requests without calling the handler for them
		// (seen on macOS 27 in about 1 of 30 tries), and then the slot would
		// never be released. Before that, the flag set above stops the load
		// handler from starting a generation, so cancelling the load is
		// enough.
		if (gen != nil) {
			[gen cancelAllCGImageGeneration];
		} else {
			[asset cancelLoading];
		}
	}
}

void avm_release(avm_request r) {
	@autoreleasepool {
		CFBridgingRelease(r);
	}
}

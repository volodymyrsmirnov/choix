//go:build darwin

// ImageIO metadata and thumbnails. Compiled with ARC; Core Foundation and
// Core Graphics objects are released by hand in @finally blocks so that a
// caught exception does not leak them (ARC itself may leak an object a frame
// held while an exception unwound it: cgo does not accept
// -fobjc-arc-exceptions, and the case is exceptional). Every entry point runs inside
// guarded(): an autorelease pool (the calling thread is a Go thread with no
// pool of its own) and an @try that turns an Objective-C exception into a
// CHOIX_IMAGEIO_FAILED result instead of letting it unwind through cgo.

#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ImageIO/ImageIO.h>
#include <math.h>
#include <stdlib.h>
#include <string.h>

#include "imageio.h"

static ChoixImageIOResult resultFailed(NSString *message) {
	ChoixImageIOResult r = {0};
	r.status = CHOIX_IMAGEIO_FAILED;
	const char *s = message.UTF8String;
	r.message = strdup(s != NULL ? s : "unknown error");
	return r;
}

static ChoixImageIOResult resultUnreadable(void) {
	ChoixImageIOResult r = {0};
	r.status = CHOIX_IMAGEIO_UNREADABLE;
	return r;
}

// resultBytes copies n bytes at p into a malloc'd buffer.
static ChoixImageIOResult resultBytes(const void *p, size_t n, int width, int height) {
	ChoixImageIOResult r = {0};
	r.bytes = malloc(n > 0 ? n : 1);
	if (r.bytes == NULL) {
		return resultFailed(@"out of memory");
	}
	if (n > 0) {
		memcpy(r.bytes, p, n);
	}
	r.len = n;
	r.width = width;
	r.height = height;
	return r;
}

static ChoixImageIOResult guarded(ChoixImageIOResult (^body)(void)) {
	ChoixImageIOResult r = {0};
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

// createSource opens path, or a copy of the len bytes at data, without
// caching decoded pixels. ImageIO opens a file read-only. The URL or data
// is released in @finally, so an exception leaves nothing behind.
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

// jsonSafe returns v with every value NSJSONSerialization accepts, or nil
// when v must be dropped (a non-finite number).
static id jsonSafe(id v) {
	if (v == nil) {
		return nil;
	}
	if ([v isKindOfClass:[NSString class]]) {
		return v;
	}
	if ([v isKindOfClass:[NSNumber class]]) {
		if (CFGetTypeID((__bridge CFTypeRef)v) == CFBooleanGetTypeID()) {
			return v;
		}
		return isfinite([(NSNumber *)v doubleValue]) ? v : nil;
	}
	if ([v isKindOfClass:[NSDictionary class]]) {
		NSDictionary *in = v;
		NSMutableDictionary *out = [NSMutableDictionary dictionaryWithCapacity:in.count];
		[in enumerateKeysAndObjectsUsingBlock:^(id key, id obj, BOOL *stop) {
			id safe = jsonSafe(obj);
			if (safe == nil) {
				return;
			}
			NSString *name = [key isKindOfClass:[NSString class]] ? key : [key description];
			out[name ?: @""] = safe;
		}];
		return out;
	}
	if ([v isKindOfClass:[NSArray class]]) {
		NSArray *in = v;
		NSMutableArray *out = [NSMutableArray arrayWithCapacity:in.count];
		for (id obj in in) {
			id safe = jsonSafe(obj);
			if (safe != nil) {
				[out addObject:safe];
			}
		}
		return out;
	}
	if ([v isKindOfClass:[NSData class]]) {
		NSData *in = v;
		const unsigned char *p = in.bytes;
		static const char hex[] = "0123456789abcdef";
		NSMutableString *out = [NSMutableString stringWithCapacity:in.length * 2];
		for (NSUInteger i = 0; i < in.length; i++) {
			[out appendFormat:@"%c%c", hex[p[i] >> 4], hex[p[i] & 0xF]];
		}
		return out;
	}
	return [v description] ?: @"";
}

// resultJSON serialises props after jsonSafe.
static ChoixImageIOResult resultJSON(NSDictionary *props) {
	id safe = jsonSafe(props);
	NSError *err = nil;
	NSData *json = [NSJSONSerialization dataWithJSONObject:safe options:0 error:&err];
	if (json == nil) {
		return resultFailed([NSString stringWithFormat:@"encode properties: %@", err.localizedDescription]);
	}
	return resultBytes(json.bytes, json.length, 0, 0);
}

ChoixImageIOResult choix_imageio_properties(const char *path, const void *data, size_t len) {
	return guarded(^ChoixImageIOResult {
		CGImageSourceRef src = NULL;
		CFDictionaryRef props = NULL;
		@try {
			src = createSource(path, data, len);
			if (!readable(src)) {
				return resultUnreadable();
			}
			props = CGImageSourceCopyPropertiesAtIndex(src, 0, NULL);
			if (props == NULL) {
				return resultUnreadable();
			}
			return resultJSON((__bridge NSDictionary *)props);
		} @finally {
			if (props != NULL) {
				CFRelease(props);
			}
			if (src != NULL) {
				CFRelease(src);
			}
		}
	});
}

// dictLong reads an integer property; 0 when absent or not a number.
static long dictLong(CFDictionaryRef dict, CFStringRef key) {
	if (dict == NULL) {
		return 0;
	}
	CFTypeRef v = CFDictionaryGetValue(dict, key);
	if (v == NULL || CFGetTypeID(v) != CFNumberGetTypeID()) {
		return 0;
	}
	long n = 0;
	if (!CFNumberGetValue((CFNumberRef)v, kCFNumberLongType, &n)) {
		return 0;
	}
	return n;
}

// maxPixelSize returns the kCGImageSourceThumbnailMaxPixelSize for a source
// of w×h stored pixels shown with orientation o. For FitLongSide it is limit.
// For FitWidth it is the long side that scales the oriented width to limit,
// or the source's own long side when the source is no wider than limit.
// ImageIO's own rounding (it may decode at a reduced DCT scale first) can
// still leave the bounded side a pixel over limit; boundedSize catches that.
static long maxPixelSize(long w, long h, int o, long limit, int fitWidth) {
	if (!fitWidth || w <= 0 || h <= 0) {
		return limit;
	}
	long width = (o >= 5) ? h : w;
	long longSide = MAX(w, h);
	if (limit >= width) {
		return longSide;
	}
	long m = (long)llround((double)limit * (double)longSide / (double)width);
	return MAX(1, MIN(m, longSide));
}

// boundedSize sets *outW × *outH to the size of a w×h oriented image once
// its width (fitWidth) or long side is at most limit, aspect kept.
static void boundedSize(size_t w, size_t h, long limit, int fitWidth, size_t *outW, size_t *outH) {
	*outW = w;
	*outH = h;
	size_t bound = fitWidth ? w : MAX(w, h);
	if (bound <= (size_t)limit) {
		return;
	}
	double scale = (double)limit / (double)bound;
	*outW = (size_t)MAX(1, llround((double)w * scale));
	*outH = (size_t)MAX(1, llround((double)h * scale));
}

// createRendered draws img with EXIF orientation o (1–8) applied onto an
// outW×outH canvas: the oriented size, possibly scaled down.
static CGImageRef createRendered(CGImageRef img, int o, size_t outW, size_t outH) {
	size_t w = CGImageGetWidth(img), h = CGImageGetHeight(img);
	BOOL swap = o >= 5;
	CGColorSpaceRef space = CGImageGetColorSpace(img);
	CGColorSpaceRef owned = NULL;
	if (space == NULL || CGColorSpaceGetModel(space) != kCGColorSpaceModelRGB) {
		owned = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
		space = owned;
	}
	CGContextRef ctx = CGBitmapContextCreate(NULL, outW, outH, 8, 0, space, (CGBitmapInfo)kCGImageAlphaNoneSkipLast);
	if (owned != NULL) {
		CGColorSpaceRelease(owned);
	}
	if (ctx == NULL) {
		return NULL;
	}
	CGContextSetInterpolationQuality(ctx, kCGInterpolationHigh);
	// Scale the oriented canvas to the output size, then map the stored
	// image, drawn at (0,0,w,h) in Core Graphics' y-up space, onto it.
	CGContextScaleCTM(ctx, (CGFloat)outW / (CGFloat)(swap ? h : w), (CGFloat)outH / (CGFloat)(swap ? w : h));
	CGFloat fw = (CGFloat)w, fh = (CGFloat)h;
	CGAffineTransform t = CGAffineTransformIdentity;
	switch (o) {
	case 2: t = CGAffineTransformMake(-1, 0, 0, 1, fw, 0); break;
	case 3: t = CGAffineTransformMake(-1, 0, 0, -1, fw, fh); break;
	case 4: t = CGAffineTransformMake(1, 0, 0, -1, 0, fh); break;
	case 5: t = CGAffineTransformMake(0, -1, -1, 0, fh, fw); break;
	case 6: t = CGAffineTransformMake(0, -1, 1, 0, 0, fw); break;
	case 7: t = CGAffineTransformMake(0, 1, 1, 0, 0, 0); break;
	case 8: t = CGAffineTransformMake(0, 1, -1, 0, fh, 0); break;
	default: break;
	}
	CGContextConcatCTM(ctx, t);
	CGContextDrawImage(ctx, CGRectMake(0, 0, fw, fh), img);
	CGImageRef out = CGBitmapContextCreateImage(ctx);
	CGContextRelease(ctx);
	return out;
}

ChoixImageIOResult choix_imageio_thumbnail(const char *path, const void *data, size_t len,
	int limit, int fit_width, int orientation) {
	return guarded(^ChoixImageIOResult {
		CGImageSourceRef src = NULL;
		CFDictionaryRef props = NULL;
		CGImageRef thumb = NULL;
		CGImageRef rendered = NULL;
		CFMutableDataRef out = NULL;
		CGImageDestinationRef dst = NULL;
		@try {
			if (limit <= 0) {
				return resultFailed([NSString stringWithFormat:@"invalid limit %d", limit]);
			}
			src = createSource(path, data, len);
			if (!readable(src)) {
				return resultUnreadable();
			}
			props = CGImageSourceCopyPropertiesAtIndex(src, 0, NULL);
			long w = dictLong(props, kCGImagePropertyPixelWidth);
			long h = dictLong(props, kCGImagePropertyPixelHeight);
			BOOL override = orientation >= 1 && orientation <= 8;
			int o = override ? orientation : (int)dictLong(props, kCGImagePropertyOrientation);
			if (o < 1 || o > 8) {
				o = 1;
			}

			NSDictionary *opts = @{
				(__bridge NSString *)kCGImageSourceCreateThumbnailFromImageAlways : @YES,
				(__bridge NSString *)kCGImageSourceCreateThumbnailWithTransform : @(!override),
				(__bridge NSString *)kCGImageSourceThumbnailMaxPixelSize : @(maxPixelSize(w, h, o, limit, fit_width)),
			};
			thumb = CGImageSourceCreateThumbnailAtIndex(src, 0, (__bridge CFDictionaryRef)opts);
			if (thumb == NULL) {
				return resultFailed(@"ImageIO made no thumbnail");
			}
			// ImageIO applied the file's own orientation; an override is
			// still to be applied.
			int pending = override ? o : 1;
			size_t tw = CGImageGetWidth(thumb), th = CGImageGetHeight(thumb);
			if (pending >= 5) {
				size_t t = tw;
				tw = th;
				th = t;
			}
			size_t outW, outH;
			boundedSize(tw, th, limit, fit_width, &outW, &outH);
			CGImageRef final = thumb;
			if (pending != 1 || outW != tw || outH != th) {
				rendered = createRendered(thumb, pending, outW, outH);
				if (rendered == NULL) {
					return resultFailed(@"cannot draw the thumbnail");
				}
				final = rendered;
			}

			out = CFDataCreateMutable(NULL, 0);
			if (out == NULL) {
				return resultFailed(@"out of memory");
			}
			dst = CGImageDestinationCreateWithData(out, CFSTR("public.jpeg"), 1, NULL);
			if (dst == NULL) {
				return resultFailed(@"cannot create a JPEG destination");
			}
			NSDictionary *jpegOpts = @{(__bridge NSString *)kCGImageDestinationLossyCompressionQuality : @0.85};
			CGImageDestinationAddImage(dst, final, (__bridge CFDictionaryRef)jpegOpts);
			if (!CGImageDestinationFinalize(dst)) {
				return resultFailed(@"cannot encode the JPEG");
			}
			size_t width = CGImageGetWidth(final), height = CGImageGetHeight(final);
			if (width > INT_MAX || height > INT_MAX) {
				return resultFailed(@"thumbnail too large");
			}
			return resultBytes(CFDataGetBytePtr(out), (size_t)CFDataGetLength(out), (int)width, (int)height);
		} @finally {
			if (dst != NULL) {
				CFRelease(dst);
			}
			if (out != NULL) {
				CFRelease(out);
			}
			if (rendered != NULL) {
				CGImageRelease(rendered);
			}
			if (thumb != NULL) {
				CGImageRelease(thumb);
			}
			if (props != NULL) {
				CFRelease(props);
			}
			if (src != NULL) {
				CFRelease(src);
			}
		}
	});
}

ChoixImageIOResult choix_imageio_raise_for_test(const char *name) {
	return guarded(^ChoixImageIOResult {
		NSString *exceptionName = (name != NULL ? [NSString stringWithUTF8String:name] : nil) ?: @"ChoixTestException";
		[NSException raise:exceptionName format:@"raised for a test"];
		return resultUnreadable();
	});
}

ChoixImageIOResult choix_imageio_json_for_test(void) {
	return guarded(^ChoixImageIOResult {
		const unsigned char bytes[] = {0x00, 0xAB, 0x7F};
		NSDictionary *props = @{
			@"Finite" : @1.5,
			@"NaN" : @(NAN),
			@"Inf" : @(INFINITY),
			@"NegInf" : @(-INFINITY),
			@"NotANumber" : [NSDecimalNumber notANumber],
			@"Bool" : @YES,
			@"Data" : [NSData dataWithBytes:bytes length:sizeof bytes],
			@"Date" : [NSDate dateWithTimeIntervalSince1970:0],
			@"List" : @[ @1, @(NAN), @2, @(INFINITY) ],
			@7 : @"number key",
			@"{Exif}" : @{@"ExposureTime" : @(NAN), @"FNumber" : @2.8},
		};
		return resultJSON(props);
	});
}

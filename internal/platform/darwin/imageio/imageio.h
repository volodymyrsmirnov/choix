#ifndef CHOIX_IMAGEIO_H
#define CHOIX_IMAGEIO_H

#include <stddef.h>

// Status of a ChoixImageIOResult.
enum {
	CHOIX_IMAGEIO_OK = 0,
	// ImageIO cannot open the source as an image.
	CHOIX_IMAGEIO_UNREADABLE = 1,
	// Any other failure, including a caught Objective-C exception; message
	// says what happened.
	CHOIX_IMAGEIO_FAILED = 2,
};

// ChoixImageIOResult is what every entry point returns. bytes (JSON for
// properties, JPEG for a thumbnail) and message are malloc'd and owned by the
// caller, who frees them with free(); both may be NULL.
typedef struct {
	int status;
	unsigned char *bytes;
	size_t len;
	int width, height;
	char *message;
} ChoixImageIOResult;

// The source is the file at path when path is non-NULL, otherwise the len
// bytes at data (copied before use; data may be NULL when len is 0). Neither
// writes a file.

// choix_imageio_properties returns image 0's property dictionary as JSON:
// non-finite numbers are dropped with their key, NSData becomes a lowercase
// hex string, any other non-JSON value its description.
ChoixImageIOResult choix_imageio_properties(const char *path, const void *data, size_t len);

// choix_imageio_thumbnail returns a JPEG (quality 0.85) with orientation
// applied. fit_width != 0 bounds the width by limit, otherwise the long side;
// never upscaled. orientation 1–8 replaces the source's orientation tag, 0
// keeps it.
ChoixImageIOResult choix_imageio_thumbnail(const char *path, const void *data, size_t len,
	int limit, int fit_width, int orientation);

// choix_imageio_raise_for_test raises an NSException named name inside the
// same guard as the entry points above.
ChoixImageIOResult choix_imageio_raise_for_test(const char *name);

// choix_imageio_json_for_test returns, through the same conversion as
// choix_imageio_properties, a fixed dictionary holding NaN, ±Inf, NSData,
// NSDate, a non-string key and nested containers.
ChoixImageIOResult choix_imageio_json_for_test(void);

#endif

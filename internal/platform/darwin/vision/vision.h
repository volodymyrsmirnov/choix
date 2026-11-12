#ifndef CHOIX_VISION_H
#define CHOIX_VISION_H

#include <stddef.h>

// Status of a ChoixVisionResult.
enum {
	CHOIX_VISION_OK = 0,
	// ImageIO cannot open or decode the source as an image.
	CHOIX_VISION_UNREADABLE = 1,
	// Any other failure, including a Vision error or a caught Objective-C
	// exception; message says what happened.
	CHOIX_VISION_FAILED = 2,
};

// ChoixVisionResult is what every entry point returns. values (len floats)
// and message are malloc'd and owned by the caller, who frees them with
// free(); both may be NULL. revision is the revision the request ran at.
typedef struct {
	int status;
	float *values;
	size_t len;
	int revision;
	char *message;
} ChoixVisionResult;

// choix_vision_feature_print returns the image feature print of image 0 of
// the file at path when path is non-NULL, otherwise of the len bytes at data
// (copied before use). The image's own orientation tag is applied. The
// request is pinned to revision and to "scale fill"; it fails when that
// revision is not supported, the element type is not float, or the element
// count is not want_len. It writes no file.
ChoixVisionResult choix_vision_feature_print(const char *path, const void *data, size_t len,
	int revision, size_t want_len);

// choix_vision_raise_for_test raises an NSException named name inside the
// same guard as the entry point above.
ChoixVisionResult choix_vision_raise_for_test(const char *name);

#endif

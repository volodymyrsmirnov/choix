#ifndef CHOIX_BOOKMARK_H
#define CHOIX_BOOKMARK_H

#include <stddef.h>

// Status of a ChoixBookmarkResult.
enum {
	CHOIX_BOOKMARK_OK = 0,
	// Foundation returned an NSError: domain, code and message say which.
	CHOIX_BOOKMARK_NSERROR = 1,
	// startAccessingSecurityScopedResource returned NO (Refresh only, in the
	// sandbox); message says so.
	CHOIX_BOOKMARK_REFUSED = 2,
	// Any other failure, including a caught Objective-C exception; message
	// says what happened.
	CHOIX_BOOKMARK_FAILED = 3,
};

// ChoixBookmarkResult is what every entry point returns. bytes, path, domain
// and message are malloc'd and owned by the caller, who frees them with
// free(); any may be NULL. handle, when non-NULL, is a retained NSURL whose
// security-scoped access is started; the caller ends it exactly once with
// choix_bookmark_stop.
typedef struct {
	int status;
	unsigned char *bytes;
	size_t len;
	char *path;
	int stale;
	int started;
	void *handle;
	char *domain;
	long code;
	char *message;
} ChoixBookmarkResult;

// Bookmark data passed in is the len bytes at data, copied before use; data
// may be NULL when len is 0. Resolution always uses
// NSURLBookmarkResolutionWithSecurityScope | WithoutUI | WithoutMounting.

// choix_bookmark_create returns a security-scoped bookmark of the directory
// at path in bytes. When that fails and unsandboxed is non-zero it
// retries with options 0.
ChoixBookmarkResult choix_bookmark_create(const char *path, int unsandboxed);

// choix_bookmark_resolve returns the bookmark's path and stale flag.
ChoixBookmarkResult choix_bookmark_resolve(const void *data, size_t len);

// choix_bookmark_start resolves the bookmark and starts security-scoped
// access. started reports whether the system granted it; when it did,
// handle holds the URL to pass to choix_bookmark_stop. On any failure no
// access is left started.
ChoixBookmarkResult choix_bookmark_start(const void *data, size_t len);

// choix_bookmark_stop ends the access started by choix_bookmark_start and
// releases handle. handle must not be used again.
void choix_bookmark_stop(void *handle);

// choix_bookmark_refresh resolves the bookmark, starts access, creates a new
// bookmark from the resolved URL (retrying with options 0 when unsandboxed
// is non-zero, as choix_bookmark_create does) and stops access before
// returning. A refused start is CHOIX_BOOKMARK_REFUSED unless unsandboxed is
// non-zero. bytes is set only on success.
ChoixBookmarkResult choix_bookmark_refresh(const void *data, size_t len, int unsandboxed);

// choix_bookmark_raise_for_test raises an NSException named name inside the
// same guard as the entry points above.
ChoixBookmarkResult choix_bookmark_raise_for_test(const char *name);

#endif

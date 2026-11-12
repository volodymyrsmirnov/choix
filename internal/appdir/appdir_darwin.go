//go:build darwin

package appdir

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation

#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

// choixPrivateFrameworksPath returns a malloc'd copy of the main bundle's
// privateFrameworksPath, or NULL when there is none or Foundation throws.
// The caller frees it.
static char *choixPrivateFrameworksPath(void) {
	@autoreleasepool {
		@try {
			NSString *p = [[NSBundle mainBundle] privateFrameworksPath];
			if (p == nil || p.length == 0) {
				return NULL;
			}
			const char *s = p.fileSystemRepresentation;
			return s == NULL ? NULL : strdup(s);
		} @catch (NSException *e) {
			return NULL;
		}
	}
}

// choixSearchPath returns a malloc'd copy of the first user-domain path for
// dir, or NULL when Foundation has none. The caller frees it.
static char *choixSearchPath(NSSearchPathDirectory dir) {
	@autoreleasepool {
		NSArray<NSString *> *paths = NSSearchPathForDirectoriesInDomains(dir, NSUserDomainMask, YES);
		NSString *p = paths.firstObject;
		if (p == nil || p.length == 0) {
			return NULL;
		}
		const char *s = p.fileSystemRepresentation;
		return s == NULL ? NULL : strdup(s);
	}
}

static char *choixApplicationSupportDir(void) {
	return choixSearchPath(NSApplicationSupportDirectory);
}

static char *choixLibraryDir(void) {
	return choixSearchPath(NSLibraryDirectory);
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// applicationSupportDir returns <NSApplicationSupportDirectory, user
// domain>: ~/Library/Application Support, or the container's equivalent
// when sandboxed. It creates nothing.
func applicationSupportDir() (string, error) {
	return takeCString(C.choixApplicationSupportDir(), "application support directory")
}

// libraryDir returns <NSLibraryDirectory, user domain>. It creates nothing.
func libraryDir() (string, error) {
	return takeCString(C.choixLibraryDir(), "library directory")
}

// BundleFrameworksDir returns the running app bundle's Contents/Frameworks
// directory — Foundation's [[NSBundle mainBundle] privateFrameworksPath] —
// or "" when the process is not running from an .app bundle (a bare binary,
// a test). It is used instead of os.Executable, which resolves a relative
// launch path against the sandbox container and then names a file that
// does not exist (spike S6).
func BundleFrameworksDir() string {
	p := C.choixPrivateFrameworksPath()
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return bundleFrameworksDir(C.GoString(p))
}

// sandboxContainerID returns APP_SANDBOX_CONTAINER_ID as libc's getenv sees
// it, or "" when it is unset. See Sandboxed for why os.Getenv is not used.
func sandboxContainerID() string {
	key := C.CString("APP_SANDBOX_CONTAINER_ID")
	defer C.free(unsafe.Pointer(key))
	v := C.getenv(key)
	if v == nil {
		return ""
	}
	return C.GoString(v)
}

// takeCString converts and frees a string returned by choixSearchPath.
func takeCString(p *C.char, what string) (string, error) {
	if p == nil {
		return "", errors.New("appdir: no user-domain " + what)
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p), nil
}

//go:build darwin

// AppKit reads for the window's own title bar. Compiled with ARC. Public API
// only: standardWindowButton:, a view's frame converted to window
// coordinates, and NSUserDefaults. Nothing here moves, hides or restyles a
// button. Every entry point runs inside guarded(): an autorelease pool (a Go
// thread has no pool of its own) and an @try that turns an Objective-C
// exception into a failure message instead of letting it unwind through
// cgo.

#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#include "titlebar.h"

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

char *choix_titlebar_buttons_right(void *window, double *right) {
	*right = 0;
	return guarded(^char * {
		if (window == NULL) {
			return failure(@"no window");
		}
		if (!pthread_main_np()) {
			return failure(@"window buttons are measured on the main thread only");
		}
		NSWindow *w = (__bridge NSWindow *)window;
		const NSWindowButton kinds[] = {NSWindowCloseButton, NSWindowMiniaturizeButton, NSWindowZoomButton};
		BOOL found = NO;
		CGFloat edge = 0;
		for (size_t i = 0; i < sizeof kinds / sizeof kinds[0]; i++) {
			NSButton *b = [w standardWindowButton:kinds[i]];
			if (b == nil || b.superview == nil) {
				continue;
			}
			// toView:nil: the window's base coordinate system, whose x is
			// measured from the window's left edge.
			NSRect r = [b convertRect:b.bounds toView:nil];
			if (!found || NSMaxX(r) > edge) {
				edge = NSMaxX(r);
			}
			found = YES;
		}
		if (!found) {
			return failure(@"the window has no standard window buttons");
		}
		*right = edge;
		return NULL;
	});
}

char *choix_titlebar_buttons_center_y(void *window, double *y) {
	*y = 0;
	return guarded(^char * {
		if (window == NULL) {
			return failure(@"no window");
		}
		if (!pthread_main_np()) {
			return failure(@"window buttons are measured on the main thread only");
		}
		NSWindow *w = (__bridge NSWindow *)window;
		const NSWindowButton kinds[] = {NSWindowCloseButton, NSWindowMiniaturizeButton, NSWindowZoomButton};
		BOOL found = NO;
		NSRect box = NSZeroRect;
		for (size_t i = 0; i < sizeof kinds / sizeof kinds[0]; i++) {
			NSButton *b = [w standardWindowButton:kinds[i]];
			if (b == nil || b.superview == nil) {
				continue;
			}
			// Window coordinates: y measured up from the bottom edge of the
			// window's frame.
			NSRect r = [b convertRect:b.bounds toView:nil];
			box = found ? NSUnionRect(box, r) : r;
			found = YES;
		}
		if (!found) {
			return failure(@"the window has no standard window buttons");
		}
		// From the top: the frame's height less the centre's height above
		// the bottom edge.
		*y = NSHeight(w.frame) - NSMidY(box);
		return NULL;
	});
}

char *choix_titlebar_double_click_preference(char **value) {
	*value = NULL;
	return guarded(^char * {
		// The same read as Wails v3.1.0's windowTitlebarDoubleClickPreference
		// (pkg/application/webview_window_darwin.go): the standard defaults'
		// search list resolves the key from the global domain, where System
		// Settings writes it. Reading it the same way keeps Choix's and
		// Wails' views of the setting identical.
		NSString *v = [[NSUserDefaults standardUserDefaults] stringForKey:@"AppleActionOnDoubleClick"];
		const char *s = v != nil ? v.UTF8String : "";
		*value = strdup(s != NULL ? s : "");
		return NULL;
	});
}

char *choix_titlebar_probe_for_test(double *right, double *center_y) {
	*right = 0;
	*center_y = 0;
	return guarded(^char * {
		if (!pthread_main_np()) {
			return failure(@"the probe runs on the main thread only");
		}
		[NSApplication sharedApplication];
		// Wails v3.1.0's window (createNativeWindow) with
		// MacTitleBarHiddenInset applied: transparent title bar, hidden
		// title, full-size content, an empty toolbar (its separator, which
		// Wails hides, does not move the buttons).
		NSWindowStyleMask mask = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
			NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable |
			NSWindowStyleMaskFullSizeContentView;
		NSWindow *w = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 1280, 800)
												  styleMask:mask
													backing:NSBackingStoreBuffered
													  defer:YES];
		w.releasedWhenClosed = NO;
		w.titlebarAppearsTransparent = YES;
		w.titleVisibility = NSWindowTitleHidden;
		NSToolbar *toolbar = [[NSToolbar alloc] initWithIdentifier:@"choix.titlebar.probe"];
		w.toolbar = toolbar;
		char *err = choix_titlebar_buttons_right((__bridge void *)w, right);
		if (err == NULL) {
			err = choix_titlebar_buttons_center_y((__bridge void *)w, center_y);
		}
		[w close];
		return err;
	});
}

char *choix_titlebar_raise_for_test(void) {
	return guarded(^char * {
		[NSException raise:@"ChoixTestException" format:@"raised for a test"];
		return NULL;
	});
}

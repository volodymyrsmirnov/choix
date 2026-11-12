//go:build darwin

// AppKit and WebKit calls on the main window. Compiled with ARC. Public API
// only: NSWindow's minSize, contentMinSize, frame, firstResponder and
// makeFirstResponder:, the content view's subviews, and WKWebView's
// underPageBackgroundColor (macOS 12). Nothing is swizzled and no private
// key is set. Every entry point runs inside guarded(): an autorelease pool
// (a Go thread has no pool of its own) and an @try that turns an
// Objective-C exception into a failure message instead of letting it
// unwind through cgo.

#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#import <WebKit/WebKit.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#include "windowchrome.h"

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

// checked refuses a nil window and any thread but the main one, before the
// pointer is touched.
static char *checked(void *window) {
	if (window == NULL) {
		return failure(@"no window");
	}
	if (!pthread_main_np()) {
		return failure(@"the window is changed on the main thread only");
	}
	return NULL;
}

// webViewIn returns the first WKWebView in view's hierarchy, breadth first
// (Wails puts it directly in the content view).
static WKWebView *webViewIn(NSView *view) {
	NSMutableArray<NSView *> *queue = [NSMutableArray arrayWithObject:view];
	while (queue.count > 0) {
		NSView *v = queue.firstObject;
		[queue removeObjectAtIndex:0];
		if ([v isKindOfClass:[WKWebView class]]) {
			return (WKWebView *)v;
		}
		[queue addObjectsFromArray:v.subviews];
	}
	return nil;
}

char *choix_window_keep_min_size(void *window, double width, double height, int *changed) {
	*changed = 0;
	char *bad = checked(window);
	if (bad != NULL) {
		return bad;
	}
	return guarded(^char * {
		NSWindow *w = (__bridge NSWindow *)window;
		if ((w.styleMask & NSWindowStyleMaskFullScreen) != 0) {
			return NULL; // full screen sizes the window itself
		}
		// Put back what Wails' windowSetMinSize(width, height) sets at
		// creation, the same way (the content minimum for a frame of that
		// size, then the frame minimum), when either is lower. AppKit
		// adds a toolbar's height to the frame minimum, so the result can
		// be taller than height; it is never lower.
		NSSize content = [w contentRectForFrameRect:NSMakeRect(0, 0, width, height)].size;
		NSSize min = w.minSize;
		NSSize cmin = w.contentMinSize;
		if (min.width < width || min.height < height || cmin.width < content.width || cmin.height < content.height) {
			[w setContentMinSize:content];
			[w setMinSize:NSMakeSize(width, height)];
			*changed = 1;
		}
		// A window left smaller than the minimum (resized while the
		// minimum was lower) grows to it, keeping its top-left corner. Not
		// while the user drags an edge (AppKit applies the minimum to the
		// next step itself) and not a zoomed window (the screen decides).
		NSSize least = w.minSize;
		NSRect f = w.frame;
		if ((f.size.width < least.width || f.size.height < least.height) && !w.inLiveResize && !w.isZoomed) {
			CGFloat top = NSMaxY(f);
			f.size.width = MAX(f.size.width, least.width);
			f.size.height = MAX(f.size.height, least.height);
			f.origin.y = top - f.size.height;
			[w setFrame:f display:YES];
			*changed = 1;
		}
		return NULL;
	});
}

char *choix_window_focus_page(void *window, int *focused) {
	*focused = 0;
	char *bad = checked(window);
	if (bad != NULL) {
		return bad;
	}
	return guarded(^char * {
		NSWindow *w = (__bridge NSWindow *)window;
		if (w.attachedSheet != nil) {
			return NULL; // a sheet has the keyboard
		}
		NSResponder *r = w.firstResponder;
		if (r != nil && r != w) {
			return NULL; // a view has the keyboard already (the page, or another)
		}
		WKWebView *web = webViewIn(w.contentView);
		if (web == nil) {
			return failure(@"the window has no web view");
		}
		if (![w makeFirstResponder:web]) {
			return failure(@"the web view did not take the keyboard");
		}
		*focused = 1;
		return NULL;
	});
}

char *choix_window_set_page_background(void *window, double red, double green, double blue) {
	char *bad = checked(window);
	if (bad != NULL) {
		return bad;
	}
	return guarded(^char * {
		NSWindow *w = (__bridge NSWindow *)window;
		WKWebView *web = webViewIn(w.contentView);
		if (web == nil) {
			return failure(@"the window has no web view");
		}
		web.underPageBackgroundColor = [NSColor colorWithSRGBRed:red green:green blue:blue alpha:1];
		return NULL;
	});
}

// Test support.

char *choix_window_new_for_test(double width, double height, void **window) {
	*window = NULL;
	if (!pthread_main_np()) {
		return failure(@"windows are made on the main thread only");
	}
	return guarded(^char * {
		[NSApplication sharedApplication];
		// Wails v3.1.0's window (createNativeWindow) with
		// MacTitleBarHiddenInset applied.
		NSWindowStyleMask mask = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
			NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable |
			NSWindowStyleMaskFullSizeContentView;
		NSScreen *screen = NSScreen.mainScreen;
		NSRect visible = screen != nil ? screen.visibleFrame : NSMakeRect(0, 0, 1440, 900);
		NSRect rect = NSMakeRect(NSMinX(visible) + 20, NSMaxY(visible) - height - 20, width, height);
		NSWindow *w = [[NSWindow alloc] initWithContentRect:rect
												  styleMask:mask
													backing:NSBackingStoreBuffered
													  defer:NO];
		w.releasedWhenClosed = NO;
		w.titlebarAppearsTransparent = YES;
		w.titleVisibility = NSWindowTitleHidden;
		w.toolbar = [[NSToolbar alloc] initWithIdentifier:@"choix.windowchrome.test"];
		NSView *content = w.contentView;
		WKWebView *web = [[WKWebView alloc] initWithFrame:content.bounds configuration:[WKWebViewConfiguration new]];
		web.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		[content addSubview:web];
		// Wails' file-drop overlay sits above the web view.
		NSView *overlay = [[NSView alloc] initWithFrame:content.bounds];
		overlay.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
		[content addSubview:overlay];
		// Another view that takes the keyboard (as a native text field would).
		NSTextField *field = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 40, 20)];
		field.identifier = @"choix.test.field";
		[content addSubview:field];
		*window = (__bridge_retained void *)w;
		return NULL;
	});
}

void choix_window_close_for_test(void *window) {
	@autoreleasepool {
		NSWindow *w = (__bridge_transfer NSWindow *)window;
		[w close];
	}
}

void choix_window_state_for_test(void *window, choix_window_test_state *state) {
	@autoreleasepool {
		NSWindow *w = (__bridge NSWindow *)window;
		memset(state, 0, sizeof *state);
		state->minWidth = w.minSize.width;
		state->minHeight = w.minSize.height;
		state->contentMinWidth = w.contentMinSize.width;
		state->contentMinHeight = w.contentMinSize.height;
		NSRect f = w.frame;
		state->width = f.size.width;
		state->height = f.size.height;
		state->left = NSMinX(f);
		state->top = NSMaxY(f);
		state->zoomed = w.isZoomed ? 1 : 0;
		WKWebView *web = webViewIn(w.contentView);
		state->webViewFirstResponder = (web != nil && w.firstResponder == web) ? 1 : 0;
		NSColor *c = [web.underPageBackgroundColor colorUsingColorSpace:NSColorSpace.sRGBColorSpace];
		if (c != nil) {
			CGFloat r, g, b, a;
			[c getRed:&r green:&g blue:&b alpha:&a];
			state->underPage[0] = r;
			state->underPage[1] = g;
			state->underPage[2] = b;
			state->underPage[3] = a;
		}
	}
}

void choix_window_set_wails_min_size_for_test(void *window, double width, double height) {
	@autoreleasepool {
		// Wails v3.1.0's windowSetMinSize (webview_window_darwin.go).
		NSWindow *w = (__bridge NSWindow *)window;
		NSSize content = [w contentRectForFrameRect:NSMakeRect(0, 0, width, height)].size;
		[w setContentMinSize:content];
		[w setMinSize:NSMakeSize(width, height)];
	}
}

void choix_window_zoom_for_test(void *window) {
	@autoreleasepool {
		[(__bridge NSWindow *)window zoom:nil];
	}
}

void choix_window_set_frame_size_for_test(void *window, double width, double height) {
	@autoreleasepool {
		NSWindow *w = (__bridge NSWindow *)window;
		NSRect f = w.frame;
		CGFloat top = NSMaxY(f);
		f.size = NSMakeSize(width, height);
		f.origin.y = top - height;
		[w setFrame:f display:NO];
	}
}

void choix_window_make_window_first_responder_for_test(void *window) {
	@autoreleasepool {
		NSWindow *w = (__bridge NSWindow *)window;
		[w makeFirstResponder:nil];
	}
}

void choix_window_make_other_view_first_responder_for_test(void *window) {
	@autoreleasepool {
		NSWindow *w = (__bridge NSWindow *)window;
		for (NSView *v in w.contentView.subviews) {
			if ([v.identifier isEqualToString:@"choix.test.field"]) {
				[w makeFirstResponder:v];
			}
		}
	}
}

char *choix_window_raise_for_test(void) {
	return guarded(^char * {
		[NSException raise:@"ChoixTestException" format:@"raised for a test"];
		return NULL;
	});
}

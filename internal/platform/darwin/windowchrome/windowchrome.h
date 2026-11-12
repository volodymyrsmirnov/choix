#ifndef CHOIX_WINDOWCHROME_H
#define CHOIX_WINDOWCHROME_H

// Every function returns NULL on success, otherwise a malloc'd message
// (including for a caught Objective-C exception) that the caller frees with
// free(). Every function taking a window must be called on the main thread;
// elsewhere it fails without touching the window.

// choix_window_keep_min_size makes the NSWindow's minSize (and the matching
// contentMinSize) at least width×height and grows a smaller window to it
// (top-left kept) unless it is zoomed or in a live resize; nothing in full
// screen. *changed is 1 when it changed anything.
char *choix_window_keep_min_size(void *window, double width, double height, int *changed);

// choix_window_focus_page makes the window's WKWebView its first responder
// when the first responder is the window itself or nil and no sheet is
// attached. *focused is 1 when it did.
char *choix_window_focus_page(void *window, int *focused);

// choix_window_set_page_background sets the window's WKWebView's
// underPageBackgroundColor to the sRGB colour (red, green, blue in 0…1).
char *choix_window_set_page_background(void *window, double red, double green, double blue);

// Test support: a window like Wails' main window (hidden-inset title bar,
// full-size content view holding a WKWebView and an overlay view above it,
// never shown), and what the tests read and do to it. Main thread only.
typedef struct {
	double minWidth, minHeight;
	double contentMinWidth, contentMinHeight;
	double width, height;
	double left, top;
	int zoomed;
	int webViewFirstResponder;
	double underPage[4];
} choix_window_test_state;

char *choix_window_new_for_test(double width, double height, void **window);
void choix_window_close_for_test(void *window);
void choix_window_state_for_test(void *window, choix_window_test_state *state);
void choix_window_set_wails_min_size_for_test(void *window, double width, double height);
void choix_window_zoom_for_test(void *window);
void choix_window_set_frame_size_for_test(void *window, double width, double height);
void choix_window_make_window_first_responder_for_test(void *window);
void choix_window_make_other_view_first_responder_for_test(void *window);
char *choix_window_raise_for_test(void);

#endif

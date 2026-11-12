#ifndef CHOIX_TITLEBAR_H
#define CHOIX_TITLEBAR_H

// Every function returns NULL on success, otherwise a malloc'd message
// (including for a caught Objective-C exception) that the caller frees with
// free().

// choix_titlebar_buttons_right stores in *right the right edge, in points
// from the window's left edge, of the right-most of the NSWindow's standard
// buttons (close, miniaturise, zoom). It must be called on the main thread;
// elsewhere it fails without touching window.
char *choix_titlebar_buttons_right(void *window, double *right);

// choix_titlebar_buttons_center_y stores in *y the common vertical centre
// of the NSWindow's standard buttons (the middle of the box around them),
// in points from the top edge of the window's frame. Main thread only, as
// choix_titlebar_buttons_right.
char *choix_titlebar_buttons_center_y(void *window, double *y);

// choix_titlebar_double_click_preference stores in *value (malloc'd) the
// user's AppleActionOnDoubleClick setting, "" when it is not set.
char *choix_titlebar_double_click_preference(char **value);

// choix_titlebar_probe_for_test makes a window like Wails'
// MacTitleBarHiddenInset one, measures it with choix_titlebar_buttons_right
// and choix_titlebar_buttons_center_y and closes it. Main thread only.
char *choix_titlebar_probe_for_test(double *right, double *center_y);

// choix_titlebar_raise_for_test raises an NSException named
// ChoixTestException inside the guard every entry point uses.
char *choix_titlebar_raise_for_test(void);

#endif

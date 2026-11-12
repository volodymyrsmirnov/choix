#ifndef CHOIX_WORKSPACE_H
#define CHOIX_WORKSPACE_H

// Operations of choix_workspace_run.
enum {
	// activateFileViewerSelectingURLs: with the one URL.
	CHOIX_WORKSPACE_REVEAL = 0,
	// openURL: with the URL.
	CHOIX_WORKSPACE_OPEN = 1,
};

// choix_workspace_run performs op on the URL in raw_url (absolute, already
// validated by the caller) through NSWorkspace. The AppKit call runs on the
// main queue and this function returns only after it has run: there is no
// timeout. It must not be called while the main queue is blocked on the
// caller. Returns NULL on success, otherwise a malloc'd message (including
// for a caught Objective-C exception) that the caller frees with free().
char *choix_workspace_run(int op, const char *raw_url);

// choix_workspace_url_path_for_test returns the path of raw_url as
// choix_workspace_run's URL conversion reads it, in *path (malloc'd), or a
// failure message as choix_workspace_run would. It touches no AppKit.
char *choix_workspace_url_path_for_test(const char *raw_url, char **path);

// choix_workspace_raise_for_test raises an NSException named
// ChoixTestException inside the guard every entry point uses.
char *choix_workspace_raise_for_test(void);

#endif

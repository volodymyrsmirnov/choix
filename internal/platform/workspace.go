package platform

// Workspace hands items to Finder and URLs to the browser through AppKit.
// Each method returns only after the AppKit call has actually run — there is
// no early return — so a caller's access lease always covers it.
type Workspace interface {
	// Reveal opens a Finder window with the item selected.
	Reveal(path string) error
	// OpenFolder opens a Finder window showing the folder's contents.
	OpenFolder(path string) error
	// OpenURL opens url in the default browser. Only https URLs are
	// accepted.
	OpenURL(url string) error
}

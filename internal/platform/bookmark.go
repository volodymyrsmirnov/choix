// Package platform declares the macOS services Choix needs — security-scoped
// bookmarks, volume facts, Finder and browser hand-off, and ImageIO /
// AVFoundation media decoding — as cgo-free interfaces and value types, plus
// the detached-call machinery that bounds uninterruptible native work. The
// cgo implementations live elsewhere; fakes live in platformtest.
package platform

import "errors"

// ErrGone reports that a bookmark's target no longer exists. It is returned
// only for the "no such file / volume" codes recorded by spike S1.
var ErrGone = errors.New("platform: bookmark target no longer exists")

// ErrUnusable reports any other bookmark resolve or access failure: the
// bookmark cannot be used, though its target may still exist.
var ErrUnusable = errors.New("platform: bookmark cannot be used")

// Bookmarks creates and uses app-scoped security-scoped bookmarks.
type Bookmarks interface {
	// Create makes a bookmark for path. The process must have access to
	// path now: a picker or drop grant, or an active StartAccess.
	Create(path string) ([]byte, error)
	// Resolve returns the bookmark's current path and whether the data is
	// stale and should be refreshed. It never mounts a volume and never
	// shows UI.
	Resolve(data []byte) (path string, stale bool, err error)
	// StartAccess resolves data and starts security-scoped access to it.
	// stop ends the access and is idempotent. In the sandbox a refused
	// start is ErrUnusable; outside it, a refused start is a lease whose
	// stop does nothing.
	StartAccess(data []byte) (path string, stop func(), err error)
	// Refresh resolves data, starts access, creates a new bookmark from that
	// same URL and stops access. On any error the caller keeps the old bytes.
	Refresh(data []byte) ([]byte, error)
}

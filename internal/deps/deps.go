// Package deps provides shared types for the small handful of external tools
// (ffmpeg, exiftool) the binary shells out to and for the model-install
// progress reporting consumed by the first-run wizard.
package deps

import "errors"

// ErrNotFound is returned when a tool or asset could not be located.
var ErrNotFound = errors.New("deps: not found")

// ProgressEvent is emitted by long-running install operations (model fetch,
// archive extraction) to drive UI progress reporting. Stage describes what
// is happening ("fetch", "verify", "extract", "done"); PercentDone is in
// [0, 1].
type ProgressEvent struct {
	Stage       string  `json:"stage"`
	PercentDone float64 `json:"percent_done"`
	Message     string  `json:"message,omitempty"`
}

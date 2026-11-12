// Package events defines the events the Go services send to the frontend:
// their names, their payloads, and a Coalescer that rate-limits the noisy
// ones. JSON tags are the frontend's field names (frontend/src/design/types.ts).
//
// The package does not import Wails: services emit through the Emitter
// interface, which cmd/choix adapts to the Wails event bus.
package events

import (
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
)

// Event names.
const (
	LibraryChanged = "library:changed"
	ScanProgress   = "scan:progress"
	ScanState      = "scan:state"
	QualityStatus  = "quality:status"
	FolderChanged  = "folder:changed"
	MenuAction     = "menu:action"
)

// Names returns every event name, in declaration order. The caller owns
// the returned slice.
func Names() []string {
	return []string{
		LibraryChanged, ScanProgress, ScanState,
		QualityStatus, FolderChanged, MenuAction,
	}
}

// Progress is a folder's scan progress (design type ScanProgress).
type Progress struct {
	Stage  string `json:"stage"` // discover | metadata | thumb | analyze | cluster
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	Failed int    `json:"failed"`
	State  string `json:"state"` // running | stopped
}

// Quality is a snapshot of the quality worker (design type QualityStatus,
// v1's quality.Status with the same state names). Up to two folder jobs run
// at once, so up to two rating passes: Passes lists each with its folder,
// and Done, Total and FolderID describe the latest one begun.
type Quality struct {
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
	Active  bool   `json:"active"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Rated   int    `json:"rated"`
	// FolderID is the folder of the pass Done and Total describe; empty
	// when no pass runs.
	FolderID string `json:"folder_id,omitempty"`
	// Passes are the rating passes in flight, oldest first; left out when
	// none runs.
	Passes []QualityPass `json:"passes,omitempty"`
}

// QualityPass is one folder's rating pass in flight: the photos it dealt
// with and the number pending when it began.
type QualityPass struct {
	FolderID string `json:"folder_id"`
	Done     int    `json:"done"`
	Total    int    `json:"total"`
}

// ScanProgressEvent is the payload of ScanProgress.
type ScanProgressEvent struct {
	FolderID string   `json:"folderId"`
	Progress Progress `json:"progress"`
	Phase    string   `json:"phase"` // starting | running | done | failed
}

// ScanStateEvent is the payload of ScanState.
type ScanStateEvent struct {
	FolderID string `json:"folderId"`
	State    string `json:"state"` // ready | scanning | queued | offline | error
	Error    string `json:"error,omitempty"`
}

// LibraryChangedEvent is the payload of LibraryChanged.
type LibraryChangedEvent struct {
	Reason   string `json:"reason"` // added | removed | gone | offline | online | moved | stats
	FolderID string `json:"folderId,omitempty"`
	Name     string `json:"name,omitempty"`
}

// FolderChangedEvent is the payload of FolderChanged.
type FolderChangedEvent struct {
	FolderID string `json:"folderId"`
	Reason   string `json:"reason"` // clusters | ratings
}

// MenuActionEvent is the payload of MenuAction.
type MenuActionEvent struct {
	ID string `json:"id"`
	// Data is the folder a context-menu click came from, if any.
	Data string `json:"data,omitempty"`
}

// ProgressFromPipeline converts a pipeline progress snapshot. The returned
// Progress is "stopped" when the phase is "failed" and "running" otherwise;
// the second value is the pipeline phase.
func ProgressFromPipeline(p pipeline.Progress) (Progress, string) {
	state := "running"
	if p.Phase == "failed" {
		state = "stopped"
	}
	return Progress{
		Stage:  p.Stage,
		Done:   p.Done,
		Total:  p.Total,
		Failed: p.Failed,
		State:  state,
	}, p.Phase
}

// Emitter sends one event to the frontend. Implementations must be safe for
// concurrent use.
type Emitter interface {
	Emit(name string, data any)
}

var _ Emitter = (*Recorder)(nil)

// Recorder is an Emitter for tests: it records every Emit. The zero value
// is ready to use and safe for concurrent use.
type Recorder struct {
	mu     sync.Mutex
	events map[string][]any
}

// Emit records data under name.
func (r *Recorder) Emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.events == nil {
		r.events = make(map[string][]any)
	}
	r.events[name] = append(r.events[name], data)
}

// Events returns a copy of the payloads recorded under name, oldest first.
func (r *Recorder) Events(name string) []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	src := r.events[name]
	out := make([]any, len(src))
	copy(out, src)
	return out
}

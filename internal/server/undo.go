package server

import "sync"

// undoEntry captures the previous and new state of a single pick action
// so it can be reversed.
type undoEntry struct {
	FileID           int64
	PrevState        string
	PrevExportedPath string
}

// undoLog is a small bounded LIFO of pick mutations. Single-user
// localhost only; no per-session split.
type undoLog struct {
	mu      sync.Mutex
	entries []undoEntry
	cap     int
}

func newUndoLog(cap int) *undoLog { return &undoLog{cap: cap} }

func (l *undoLog) Push(e undoEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
	if len(l.entries) > l.cap {
		l.entries = l.entries[len(l.entries)-l.cap:]
	}
}

// Pop returns the most recent entry and a boolean indicating success.
func (l *undoLog) Pop() (undoEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return undoEntry{}, false
	}
	last := l.entries[len(l.entries)-1]
	l.entries = l.entries[:len(l.entries)-1]
	return last, true
}

// Len returns the current depth (for testing).
func (l *undoLog) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

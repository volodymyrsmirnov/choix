package catalog

import "github.com/volodymyrsmirnov/choix/internal/picks"

// undoLimit is how many mutations of one folder can be undone ([call] C22).
const undoLimit = 100

// undoEntry is one undoable mutation: the file and the state it had before.
type undoEntry struct {
	fileID int64
	prev   picks.State
}

// undoStack is a folder's in-memory undo history, newest last, holding at
// most undoLimit entries (the oldest is dropped). It is not safe for
// concurrent use: the folder id's mutation lock guards it.
type undoStack struct {
	entries []undoEntry
}

// push records e, dropping the oldest entry when the stack is full.
func (u *undoStack) push(e undoEntry) {
	if len(u.entries) >= undoLimit {
		n := copy(u.entries, u.entries[len(u.entries)-undoLimit+1:])
		clear(u.entries[n:])
		u.entries = u.entries[:n]
	}
	u.entries = append(u.entries, e)
}

// pop removes and returns the newest entry; ok is false on an empty stack.
func (u *undoStack) pop() (e undoEntry, ok bool) {
	if len(u.entries) == 0 {
		return undoEntry{}, false
	}
	e = u.entries[len(u.entries)-1]
	u.entries = u.entries[:len(u.entries)-1]
	return e, true
}

// clear forgets every entry.
func (u *undoStack) clear() { u.entries = nil }

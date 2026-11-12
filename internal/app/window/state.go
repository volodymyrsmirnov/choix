package window

import (
	"encoding/json"
	"fmt"
)

// kvKey is the library.db kv key holding the window's frame ([call] C29).
const kvKey = "window"

// minVisible is how much of a saved frame, in points along each axis, must
// lie on one screen for the frame to be restored.
const minVisible = 100

// State is the window's frame: the top-left corner in Wails' global
// coordinates (points, Y down, origin at the primary screen's top-left
// corner — what WebviewWindow.Bounds reports and WindowXY places by) and
// the outer size in points.
type State struct{ X, Y, Width, Height int }

// stateJSON is State as stored in the kv table.
type stateJSON struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Rect is a screen's bounds, in the same coordinates as State.
type Rect struct{ X, Y, Width, Height int }

// KV is the key-value store the frame is saved in: library.db's kv table,
// through the runtime's gate (runtime.Runtime.KV).
type KV interface {
	KVGet(k string) (string, bool, error)
	KVSet(k, v string) error
}

// Load returns the saved frame, at least the minimum size; nil when none
// is saved. A value that cannot be read is an error, and nil.
func Load(kv KV) (*State, error) {
	raw, ok, err := kv.KVGet(kvKey)
	if err != nil {
		return nil, fmt.Errorf("window: read the saved frame: %w", err)
	}
	if !ok {
		return nil, nil
	}
	var j stateJSON
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return nil, fmt.Errorf("window: decode the saved frame: %w", err)
	}
	s := State(j).clamped()
	return &s, nil
}

// Save stores the frame.
func Save(kv KV, s State) error {
	raw, err := json.Marshal(stateJSON(s))
	if err != nil {
		return fmt.Errorf("window: encode the frame: %w", err)
	}
	if err := kv.KVSet(kvKey, string(raw)); err != nil {
		return fmt.Errorf("window: save the frame: %w", err)
	}
	return nil
}

// Restore returns the frame to open the window at: saved, grown to the
// minimum size, when it then overlaps one of screens by at least 100×100
// points; nil (open at the default size, centred) when it overlaps none,
// for example because the display it was on is gone.
func Restore(saved State, screens []Rect) *State {
	s := saved.clamped()
	for _, sc := range screens {
		w := min(s.X+s.Width, sc.X+sc.Width) - max(s.X, sc.X)
		h := min(s.Y+s.Height, sc.Y+sc.Height) - max(s.Y, sc.Y)
		if w >= minVisible && h >= minVisible {
			return &s
		}
	}
	return nil
}

// clamped returns s with each side at least the window's minimum.
func (s State) clamped() State {
	s.Width = max(s.Width, MinWidth)
	s.Height = max(s.Height, MinHeight)
	return s
}

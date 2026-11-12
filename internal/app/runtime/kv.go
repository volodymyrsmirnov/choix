package runtime

import "fmt"

// KV is library.db's key-value table for the native window's own state, its
// frame ([call] C29). Every call enters the runtime's gate, so once Shutdown
// has begun it answers unavailable without touching library.db — a save
// that fires while quitting never uses the database after it is closed —
// and Shutdown waits for a call in flight before closing it.
type KV struct{ r *Runtime }

// KV returns library.db's key-value table, behind the runtime's gate.
func (r *Runtime) KV() KV { return KV{r: r} }

// KVGet returns the value stored under k; ok is false when there is none.
func (s KV) KVGet(k string) (v string, ok bool, err error) {
	leave, err := s.r.gate.Enter()
	if err != nil {
		return "", false, fmt.Errorf("runtime: kv get %q: %w", k, err)
	}
	defer leave()
	return s.r.db.KVGet(k)
}

// KVSet stores v under k, replacing any previous value.
func (s KV) KVSet(k, v string) error {
	leave, err := s.r.gate.Enter()
	if err != nil {
		return fmt.Errorf("runtime: kv set %q: %w", k, err)
	}
	defer leave()
	return s.r.db.KVSet(k, v)
}

// Package platformtest provides in-memory fakes of the platform interfaces
// and builders for synthetic media fixtures, for tests that must not depend
// on cgo, the sandbox or real files.
package platformtest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// bookmarkPrefix starts every bookmark the fake hands out.
const bookmarkPrefix = "platformtest-bookmark:"

// target is the file a bookmark follows; it moves with Move and disappears
// with Delete.
type target struct {
	path string
	gone bool
}

// record is one bookmark's bytes and the conditions that apply to them.
type record struct {
	t        *target
	stale    bool
	unusable bool
	denied   bool
}

// Bookmarks is an in-memory platform.Bookmarks. Each Create returns new
// bytes that follow their path through Move. Delete, MakeUnusable,
// DenyAccess and MarkStale affect the bookmarks that exist for that path
// when they are called, so a bookmark created afterwards — the folder added
// again — is fresh. The zero value is ready to use; it is safe for
// concurrent use.
type Bookmarks struct {
	mu        sync.Mutex
	records   map[string]*record
	next      int
	leases    map[int]string
	nextLease int
	refreshed int
}

var _ platform.Bookmarks = (*Bookmarks)(nil)

// under reports whether path is p or lies inside it.
func under(path, p string) bool {
	return path == p || strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/")
}

// newRecordLocked stores a fresh bookmark for t and returns its bytes.
func (b *Bookmarks) newRecordLocked(t *target) []byte {
	if b.records == nil {
		b.records = map[string]*record{}
	}
	b.next++
	token := fmt.Sprintf("%s%d", bookmarkPrefix, b.next)
	b.records[token] = &record{t: t}
	return []byte(token)
}

// Create returns a new bookmark for path.
func (b *Bookmarks) Create(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("platformtest: create bookmark: empty path")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.newRecordLocked(&target{path: path}), nil
}

// resolveLocked finds the bookmark for data and checks it can be resolved.
func (b *Bookmarks) resolveLocked(data []byte) (*record, error) {
	r, ok := b.records[string(data)]
	switch {
	case !ok:
		return nil, fmt.Errorf("platformtest: resolve bookmark: unknown data: %w", platform.ErrUnusable)
	case r.t.gone:
		return nil, fmt.Errorf("platformtest: resolve %s: %w", r.t.path, platform.ErrGone)
	case r.unusable:
		return nil, fmt.Errorf("platformtest: resolve %s: %w", r.t.path, platform.ErrUnusable)
	}
	return r, nil
}

// Resolve returns the bookmark's current path and whether it was marked
// stale.
func (b *Bookmarks) Resolve(data []byte) (path string, stale bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, err := b.resolveLocked(data)
	if err != nil {
		return "", false, err
	}
	return r.t.path, r.stale, nil
}

// startLocked resolves data and starts a lease, modelling the sandbox: a
// denied start is ErrUnusable.
func (b *Bookmarks) startLocked(data []byte) (*record, func(), error) {
	r, err := b.resolveLocked(data)
	if err != nil {
		return nil, nil, err
	}
	if r.denied {
		return nil, nil, fmt.Errorf("platformtest: start access %s: %w", r.t.path, platform.ErrUnusable)
	}
	if b.leases == nil {
		b.leases = map[int]string{}
	}
	b.nextLease++
	id := b.nextLease
	b.leases[id] = r.t.path
	var once sync.Once
	stop := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.leases, id)
		})
	}
	return r, stop, nil
}

// StartAccess resolves data and records an active lease on its path until
// stop is called. stop is idempotent.
func (b *Bookmarks) StartAccess(data []byte) (path string, stop func(), err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, stop, err := b.startLocked(data)
	if err != nil {
		return "", nil, err
	}
	return r.t.path, stop, nil
}

// Refresh resolves data, starts access, creates new bytes for the same
// target and stops access. A successful refresh is counted in Refreshed.
func (b *Bookmarks) Refresh(data []byte) ([]byte, error) {
	b.mu.Lock()
	r, stop, err := b.startLocked(data)
	if err != nil {
		b.mu.Unlock()
		return nil, err
	}
	fresh := b.newRecordLocked(r.t)
	b.refreshed++
	b.mu.Unlock()
	stop()
	return fresh, nil
}

// eachLocked calls fn for every bookmark whose target is at or under path.
func (b *Bookmarks) eachLocked(path string, fn func(*record)) {
	for _, r := range b.records {
		if under(r.t.path, path) {
			fn(r)
		}
	}
}

// Move makes every bookmark whose target is oldPath, or inside it, follow it
// to newPath.
func (b *Bookmarks) Move(oldPath, newPath string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	moved := map[*target]bool{}
	b.eachLocked(oldPath, func(r *record) {
		if moved[r.t] {
			return
		}
		moved[r.t] = true
		r.t.path = newPath + strings.TrimPrefix(r.t.path, oldPath)
	})
}

// Delete makes the existing bookmarks of path, or of anything inside it,
// resolve to platform.ErrGone.
func (b *Bookmarks) Delete(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.eachLocked(path, func(r *record) { r.t.gone = true })
}

// MakeUnusable makes the existing bookmarks of path, or of anything inside
// it, fail to resolve or start with platform.ErrUnusable.
func (b *Bookmarks) MakeUnusable(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.eachLocked(path, func(r *record) { r.unusable = true })
}

// DenyAccess makes StartAccess (and Refresh) of the existing bookmarks of
// path, or of anything inside it, fail with platform.ErrUnusable; Resolve
// still succeeds.
func (b *Bookmarks) DenyAccess(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.eachLocked(path, func(r *record) { r.denied = true })
}

// MarkStale makes Resolve of the existing bookmarks of path, or of anything
// inside it, report stale. Bytes returned by Refresh are not stale.
func (b *Bookmarks) MarkStale(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.eachLocked(path, func(r *record) { r.stale = true })
}

// Active returns the paths of the access leases not yet stopped, one entry
// per lease, sorted.
func (b *Bookmarks) Active() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.leases))
	for _, p := range b.leases {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// Refreshed returns the number of successful Refresh calls.
func (b *Bookmarks) Refreshed() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.refreshed
}

// accessible reports whether an active lease covers path.
func (b *Bookmarks) accessible(path string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.leases {
		if under(path, p) {
			return true
		}
	}
	return false
}

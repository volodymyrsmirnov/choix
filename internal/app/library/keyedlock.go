package library

import (
	"context"
	"sync"
)

// keyedLock is a set of mutexes, one per key (a folder id), whose Lock waits
// while a context allows. Different keys never block each other. An entry
// exists only while someone holds or waits for its key. The zero value is
// ready to use.
type keyedLock struct {
	mu sync.Mutex
	m  map[string]*keyedEntry
}

// keyedEntry is one key's mutex: a token in ch means held. refs counts the
// holder and the waiters (keyedLock.mu).
type keyedEntry struct {
	ch   chan struct{}
	refs int
}

// Lock takes key's mutex, waiting until it is free or ctx is done. On
// success it returns the idempotent unlock func; when ctx is done first, or
// already, it returns ctx.Err() and holds nothing.
func (k *keyedLock) Lock(ctx context.Context, key string) (unlock func(), err error) {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*keyedEntry{}
	}
	e := k.m[key]
	if e == nil {
		e = &keyedEntry{ch: make(chan struct{}, 1)}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()

	if err := ctx.Err(); err != nil {
		k.drop(key, e)
		return nil, err
	}
	select {
	case e.ch <- struct{}{}:
	case <-ctx.Done():
		k.drop(key, e)
		return nil, ctx.Err()
	}
	// The mutex and a cancellation can be ready together; never start work
	// for a context that is already done.
	if err := ctx.Err(); err != nil {
		<-e.ch
		k.drop(key, e)
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-e.ch
			k.drop(key, e)
		})
	}, nil
}

// drop forgets one holder or waiter of e, removing the entry with the last.
func (k *keyedLock) drop(key string, e *keyedEntry) {
	k.mu.Lock()
	defer k.mu.Unlock()
	e.refs--
	if e.refs == 0 {
		delete(k.m, key)
	}
}

// count reports how many callers hold or wait for key (for tests).
func (k *keyedLock) count(key string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if e := k.m[key]; e != nil {
		return e.refs
	}
	return 0
}

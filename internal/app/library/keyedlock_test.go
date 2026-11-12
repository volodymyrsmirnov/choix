package library

import (
	"context"
	"errors"
	"testing"
	"time"
)

// waitForCount waits until key has n holders and waiters.
func waitForCount(t *testing.T, k *keyedLock, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for k.count(key) != n {
		if time.Now().After(deadline) {
			t.Fatalf("%s has %d holders and waiters, want %d", key, k.count(key), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestKeyedLock_SameKeySerialises(t *testing.T) {
	var k keyedLock
	unlock, err := k.Lock(context.Background(), "a")
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	got := make(chan func(), 1)
	go func() {
		u, err := k.Lock(context.Background(), "a")
		if err != nil {
			t.Errorf("second Lock: %v", err)
		}
		got <- u
	}()
	waitForCount(t, &k, "a", 2)
	select {
	case <-got:
		t.Fatal("second Lock succeeded while the first is held")
	default:
	}
	unlock()
	unlock() // idempotent: must not release the second holder
	u2 := <-got
	if n := k.count("a"); n != 1 {
		t.Fatalf("count = %d, want 1 (the second holder)", n)
	}
	u2()
	if n := k.count("a"); n != 0 || len(k.m) != 0 {
		t.Fatalf("count = %d, entries = %d after the last unlock, want 0", n, len(k.m))
	}
}

func TestKeyedLock_DifferentKeysDoNotBlock(t *testing.T) {
	var k keyedLock
	ua, err := k.Lock(context.Background(), "a")
	if err != nil {
		t.Fatalf("Lock a: %v", err)
	}
	defer ua()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ub, err := k.Lock(ctx, "b")
	if err != nil {
		t.Fatalf("Lock b while a is held: %v", err)
	}
	ub()
}

func TestKeyedLock_CancelledWaiter(t *testing.T) {
	var k keyedLock
	unlock, err := k.Lock(context.Background(), "a")
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer unlock()

	done, cancelDone := context.WithCancel(context.Background())
	cancelDone()
	if _, err := k.Lock(done, "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Lock with a cancelled context = %v, want context.Canceled", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		_, err := k.Lock(ctx, "a")
		res <- err
	}()
	waitForCount(t, &k, "a", 2)
	cancel()
	select {
	case err := <-res:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting Lock = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled waiter did not return")
	}
	if n := k.count("a"); n != 1 {
		t.Fatalf("count = %d, want 1 (the holder only)", n)
	}

	// A context that is already done never takes a free lock either.
	var free keyedLock
	if _, err := free.Lock(done, "x"); !errors.Is(err, context.Canceled) || free.count("x") != 0 {
		t.Fatalf("Lock(done) on a free key = %v (count %d), want context.Canceled and no entry", err, free.count("x"))
	}
}

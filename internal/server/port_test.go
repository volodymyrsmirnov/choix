package server

import (
	"net"
	"strconv"
	"testing"
)

func TestPickFreePortReturnsUsableTCPPort(t *testing.T) {
	port, err := PickFreePort()
	if err != nil {
		t.Fatalf("PickFreePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port %d out of range", port)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("listen on %d: %v", port, err)
	}
	_ = ln.Close()
}

func TestPickFreePortHonorsRequestedPort(t *testing.T) {
	port, err := PickFreePort()
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := ResolvePort(port)
	if err != nil {
		t.Fatalf("ResolvePort(%d): %v", port, err)
	}
	if got != port {
		t.Errorf("got %d want %d (preferred port should pass through)", got, port)
	}
}

func TestResolvePortZeroPicksFree(t *testing.T) {
	got, err := ResolvePort(0)
	if err != nil {
		t.Fatalf("ResolvePort(0): %v", err)
	}
	if got <= 0 {
		t.Errorf("got %d, want > 0", got)
	}
}

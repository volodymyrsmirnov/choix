package server

import (
	"fmt"
	"net"
)

// PickFreePort asks the kernel for an unused TCP port on 127.0.0.1.
// The returned port is briefly bound to verify availability, then released.
// There is an inherent race between the release and the caller's reuse;
// callers should treat the result as a hint and accept that a subsequent
// listen may fail.
func PickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("pick free port: %w", err)
	}
	defer func() { _ = ln.Close() }()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("pick free port: unexpected addr type %T", ln.Addr())
	}
	return addr.Port, nil
}

// ResolvePort returns preferred unchanged when non-zero (the caller
// requested a specific port), or a kernel-assigned free port when zero.
func ResolvePort(preferred int) (int, error) {
	if preferred != 0 {
		return preferred, nil
	}
	return PickFreePort()
}

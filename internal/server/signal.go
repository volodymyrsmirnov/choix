package server

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// RunUntilSignal serves srv until SIGINT or SIGTERM, then gracefully
// shuts down. Used by `choix serve` and the default `choix .` command.
func RunUntilSignal(ctx context.Context, srv *Server) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-runCtx.Done():
		}
	}()

	return srv.Run(runCtx)
}

package firstrun

import (
	"context"
	"fmt"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/deps"
)

// ModelInstaller is the subset of internal/ai/local used to materialize
// missing ONNX models.
type ModelInstaller interface {
	Install(ctx context.Context, kind local.ModelKind, progress chan<- deps.ProgressEvent) error
}

// ProgressFunc is invoked once per progress event.
type ProgressFunc func(deps.ProgressEvent)

// InstallModel runs the supplied ModelInstaller, forwarding progress events
// to cb. The callback is invoked from the same goroutine, in order.
func InstallModel(ctx context.Context, m ModelInstaller, kind local.ModelKind, cb ProgressFunc) error {
	ch := make(chan deps.ProgressEvent, 8)
	errCh := make(chan error, 1)
	go func() { errCh <- m.Install(ctx, kind, ch) }()

	for ev := range ch {
		cb(ev)
	}
	if err := <-errCh; err != nil {
		return fmt.Errorf("firstrun: install model %s: %w", kind, err)
	}
	return nil
}

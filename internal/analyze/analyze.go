// Package analyze is the pipeline's analyze stage: it makes the Vision
// feature print of a file's preview and stores it, normalised, for the
// grouper.
package analyze

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// PrintID identifies how stored feature prints were made. Rows stamped with
// another id are pending again.
const PrintID = "vnfp-r2/fill/preview/v1"

// ErrPrintsOff is what a Printer answers while visual grouping is off.
var ErrPrintsOff = errors.New("analyze: visual grouping is off")

// Printer makes the feature print of the image at path. A nil Printer, or
// one answering ErrPrintsOff, means prints are not computed.
type Printer interface {
	Print(ctx context.Context, path string) ([]float32, error)
}

// Analyzer analyzes one file at a time: it makes its print and moves it to
// scan_status 'analyzed'.
type Analyzer struct {
	store   *store.Store
	printer Printer

	// FailureGuard, when set, is consulted right before a failure would be
	// recorded. When it returns false (the folder is unreachable) nothing is
	// written — the row keeps its status — and the error wraps
	// pipeline.ErrFolderUnavailable. nil records every failure.
	FailureGuard func() bool
}

// New returns an Analyzer writing to s. p makes the prints; with a nil p
// (pass a nil interface, not a typed nil pointer) none is made.
func New(s *store.Store, p Printer) *Analyzer {
	return &Analyzer{store: s, printer: p}
}

// unavailable returns cause wrapped in pipeline.ErrFolderUnavailable when
// the guard reports the folder unreachable, and nil otherwise.
func (a *Analyzer) unavailable(cause error) error {
	if a.FailureGuard != nil && !a.FailureGuard() {
		return fmt.Errorf("%w: %w", pipeline.ErrFolderUnavailable, cause)
	}
	return nil
}

// fail records cause as the file's failure, unless the folder is
// unreachable (then nothing is written), and returns it.
func (a *Analyzer) fail(fileID int64, cause error) error {
	if uerr := a.unavailable(cause); uerr != nil {
		return uerr
	}
	if ferr := a.store.Files().UpdateStatus(fileID, "failed", cause.Error()); ferr != nil {
		return errors.Join(cause, fmt.Errorf("record failure: %w", ferr))
	}
	return cause
}

// Analyze makes the feature print of the file's preview at previewPath and
// moves the file to 'analyzed'. The outcomes:
//   - a print: stored, L2-normalised, stamped PrintID;
//   - no printer, or the printer answers ErrPrintsOff (checked before any
//     other error): nothing stored, the row unstamped, so the file is
//     pending when grouping is switched on;
//   - the printer failed, or returned a vector that is not FeaturePrintLen
//     finite values with a non-zero norm: a NULL print stamped PrintID, so
//     the file is not retried on every job;
//   - ctx ended, or the folder is unreachable: the row is left as it was
//     (an unreachable folder is pipeline.ErrFolderUnavailable);
//   - the preview cannot be opened while the folder is reachable: the file
//     is 'failed' with the error text.
func (a *Analyzer) Analyze(ctx context.Context, fileID int64, previewPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := readable(previewPath); err != nil {
		return a.fail(fileID, fmt.Errorf("read preview: %w", err))
	}
	if a.printer == nil {
		return a.analyzed(fileID)
	}
	vec, err := a.printer.Print(ctx, previewPath)
	switch {
	case errors.Is(err, ErrPrintsOff):
		return a.analyzed(fileID)
	case ctx.Err() != nil:
		return ctx.Err()
	case err == nil:
		vec, err = normalise(vec)
	}
	if err != nil {
		if uerr := a.unavailable(err); uerr != nil {
			return uerr
		}
		slog.Warn("analyze: no feature print", "file", fileID, "err", err)
		vec = nil
	}
	if err := a.store.AISignals().UpsertPrint(fileID, vec, PrintID); err != nil {
		return a.fail(fileID, fmt.Errorf("store print: %w", err))
	}
	return a.analyzed(fileID)
}

// analyzed moves the file to 'analyzed'.
func (a *Analyzer) analyzed(fileID int64) error {
	if err := a.store.Files().UpdateStatus(fileID, "analyzed", ""); err != nil {
		if uerr := a.unavailable(err); uerr != nil {
			return uerr
		}
		return fmt.Errorf("update status: %w", err)
	}
	return nil
}

// readable reports whether the file at path can be opened for reading.
func readable(path string) error {
	f, err := os.Open(path) //nolint:gosec // G304: the file's cached preview, opened read-only
	if err != nil {
		return err
	}
	return f.Close()
}

// normalise returns v scaled to unit length, or an error when v does not
// have platform.FeaturePrintLen finite values or its norm is zero.
func normalise(v []float32) ([]float32, error) {
	if len(v) != platform.FeaturePrintLen {
		return nil, fmt.Errorf("analyze: feature print has %d values, want %d", len(v), platform.FeaturePrintLen)
	}
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := math.Sqrt(sum)
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, fmt.Errorf("analyze: feature print has norm %v", norm)
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out, nil
}

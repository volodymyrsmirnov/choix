package meta

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Extractor orchestrates exiftool invocation, JSON parsing, and store updates
// for a single file. One Extractor is shared across the worker pool; it holds
// no per-file state.
//
// root is the absolute scan-root directory. Paths in the files table are
// stored relative to root (Plan-1 convention); Extractor joins them at the
// boundary, just before opening the file. Pass "" to bypass joining when the
// caller already supplies absolute paths to Process directly.
type Extractor struct {
	tool  *ExifTool
	store *store.Store
	root  string
}

// NewExtractor wires the exiftool runner, a store handle, and the scan root.
func NewExtractor(tool *ExifTool, s *store.Store, root string) *Extractor {
	return &Extractor{tool: tool, store: s, root: root}
}

// Process runs exiftool against absPath, parses the result, and writes every
// extracted field plus the gzipped raw JSON to the files row identified by
// fileID. absPath must be absolute (or resolvable from CWD); Process does not
// itself prepend root — that is ProcessByStatus's job.
//
// On any error, the row is moved to scan_status='failed' with the error text
// recorded in the row's `error` column, and the wrapped error is returned to
// the caller. Per-file failure does not cancel the rest of a scan; this method
// makes the failure durable and visible.
func (e *Extractor) Process(ctx context.Context, fileID int64, absPath string) error {
	if _, err := e.store.Files().GetByID(fileID); err != nil {
		return fmt.Errorf("load file row %d: %w", fileID, err)
	}

	rawJSON, err := e.tool.Run(ctx, absPath)
	if err != nil {
		return e.recordFailure(ctx, fileID, fmt.Errorf("exiftool: %w", err))
	}

	m, err := Parse(rawJSON)
	if err != nil {
		return e.recordFailure(ctx, fileID, fmt.Errorf("parse: %w", err))
	}

	gz, err := gzipBytes(rawJSON)
	if err != nil {
		return e.recordFailure(ctx, fileID, fmt.Errorf("gzip raw exif: %w", err))
	}

	captured, hasCaptured := Captured(m)
	upd := store.MetadataUpdate{
		DeviceKey:   DeviceKey(m),
		CapturedAt:  captured,
		HasCaptured: hasCaptured,
		Width:       m.Width,
		Height:      m.Height,
		ISO:         m.ISO,
		Aperture:    m.Aperture,
		Shutter:     m.Shutter,
		FocalLength: m.FocalLength,
		GPSLat:      m.GPSLatitude,
		GPSLon:      m.GPSLongitude,
		HasGPS:      m.HasGPS,
		RawExif:     gz,
		ScanStatus:  "metadata",
	}
	if err := e.store.Files().UpdateMetadataFull(ctx, fileID, upd); err != nil {
		return e.recordFailure(ctx, fileID, fmt.Errorf("update row: %w", err))
	}
	return nil
}

// recordFailure writes scan_status='failed' with the error text and returns a
// wrapped error. All metadata columns (device_key, captured_at, raw_exif,
// etc.) are left untouched so that a transient failure on a previously
// processed file does not overwrite valid EXIF with NULL/zeros.
// If the failure-recording write itself fails (e.g. the row was deleted
// concurrently), both errors are joined so neither is lost.
func (e *Extractor) recordFailure(ctx context.Context, fileID int64, cause error) error {
	if uerr := e.store.Files().MarkScanFailure(ctx, fileID, cause.Error()); uerr != nil {
		return errors.Join(cause, fmt.Errorf("record failure: %w", uerr))
	}
	return cause
}

// ProcessByStatus drains up to limit files currently in `status`, processes
// each one, and returns the count that succeeded. Per-file failures are
// recorded on the row (scan_status='failed') and counted against the total
// drained but do not stop the batch.
//
// Each file's stored path is joined with the Extractor's root to produce the
// absolute path passed to Process. When root is "", filepath.Join is a no-op
// and the stored path is used as-is — convenient for unit tests that store
// absolute paths directly.
//
// Errors returned from this method represent fatal batch problems (DB read
// failure, ctx cancellation between files); per-file processing errors are
// swallowed after being recorded.
func (e *Extractor) ProcessByStatus(ctx context.Context, status string, limit int) (int, error) {
	files, err := e.store.Files().PickByStatus(ctx, status, limit)
	if err != nil {
		return 0, fmt.Errorf("pick by status: %w", err)
	}

	success := 0
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return success, err
		}
		absPath := filepath.Join(e.root, f.Path)
		if perr := e.Process(ctx, f.ID, absPath); perr == nil {
			success++
		}
	}
	return success, nil
}

func gzipBytes(in []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(in); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

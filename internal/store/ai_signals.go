package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// printBytes is the size of a stored feature print: platform.FeaturePrintLen
// little-endian float32 values. A blob of any other size is no print.
const printBytes = platform.FeaturePrintLen * 4

// AISignals represents a row in the ai_signals table. The table predates
// the v1 scope cut and still has columns for retired per-file scores
// (sharpness, NIMA, faces, pHash, ...). The Vision feature print is the
// signal in use; the retired columns (clip_embedding among them) are never
// read or written here.
type AISignals struct {
	FileID int64
	// Print is the stored feature print, nil when there is none (or the
	// stored blob is not printBytes long). PrintID is how it was made; a
	// row stamped with an id but no print is a print Vision could not make.
	Print      []float32
	PrintID    string
	ComputedAt sql.NullInt64
}

// AISignalsRepo provides access to the ai_signals table.
type AISignalsRepo struct{ db *sql.DB }

// AISignals returns an AISignalsRepo backed by the store's DB connection.
func (s *Store) AISignals() *AISignalsRepo { return &AISignalsRepo{db: s.db} }

// GetByFileID returns the AI signal row for the given file. Returns ErrNotFound if missing.
func (r *AISignalsRepo) GetByFileID(fileID int64) (AISignals, error) {
	row := r.db.QueryRowContext(context.Background(),
		`SELECT file_id, vision_print, vision_print_id, computed_at FROM ai_signals WHERE file_id = ?`, fileID)
	var a AISignals
	var print []byte
	var printID sql.NullString
	err := row.Scan(&a.FileID, &print, &printID, &a.ComputedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AISignals{}, ErrNotFound
		}
		return AISignals{}, err
	}
	a.Print = decodePrint(print)
	a.PrintID = printID.String
	return a, nil
}

// UpsertPrint stores the feature print of a file, stamped with printID, and
// the time it was computed. A nil print stores NULL with the id: Vision
// could not make one, and the file is not pending for printID. A non-nil
// print must have platform.FeaturePrintLen values. The row's other columns
// are left alone.
func (r *AISignalsRepo) UpsertPrint(fileID int64, print []float32, printID string) error {
	if printID == "" {
		return errors.New("UpsertPrint: empty print id")
	}
	var blob []byte
	if print != nil {
		if len(print) != platform.FeaturePrintLen {
			return fmt.Errorf("UpsertPrint: print has %d values, want %d", len(print), platform.FeaturePrintLen)
		}
		blob = encodeFloats(print)
	}
	_, err := r.db.ExecContext(context.Background(),
		`INSERT INTO ai_signals (file_id, vision_print, vision_print_id, computed_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(file_id) DO UPDATE SET
		    vision_print    = excluded.vision_print,
		    vision_print_id = excluded.vision_print_id,
		    computed_at     = excluded.computed_at`,
		fileID, blob, printID, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("UpsertPrint: %w", err)
	}
	return nil
}

// AllPrints returns a map from file_id to feature print for every row that
// has a print stamped printID. Rows with no print, another id, or a blob
// that is not printBytes long are absent. Used by the grouper to avoid an
// N+1 query when reclustering thousands of files.
func (r *AISignalsRepo) AllPrints(printID string) (map[int64][]float32, error) {
	rows, err := r.db.QueryContext(context.Background(),
		`SELECT file_id, vision_print FROM ai_signals
		  WHERE vision_print IS NOT NULL AND vision_print_id = ?`, printID)
	if err != nil {
		return nil, fmt.Errorf("AllPrints: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64][]float32)
	for rows.Next() {
		var fid int64
		var blob []byte
		if err := rows.Scan(&fid, &blob); err != nil {
			return nil, fmt.Errorf("AllPrints: %w", err)
		}
		if p := decodePrint(blob); p != nil {
			out[fid] = p
		}
	}
	return out, rows.Err()
}

// decodePrint decodes a stored feature print; nil unless blob is exactly
// printBytes long.
func decodePrint(blob []byte) []float32 {
	if len(blob) != printBytes {
		return nil
	}
	return decodeFloats(blob)
}

// decodeFloats turns a little-endian f32 BLOB into []float32.
// Returns nil if blob is empty or has a length not divisible by 4.
func decodeFloats(blob []byte) []float32 {
	if len(blob) == 0 || len(blob)%4 != 0 {
		return nil
	}
	out := make([]float32, len(blob)/4)
	for i := range out {
		bits := binary.LittleEndian.Uint32(blob[i*4 : i*4+4])
		out[i] = math.Float32frombits(bits)
	}
	return out
}

// encodeFloats turns a []float32 into a little-endian f32 BLOB.
func encodeFloats(v []float32) []byte {
	out := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], math.Float32bits(f))
	}
	return out
}

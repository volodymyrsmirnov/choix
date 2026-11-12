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
// signal in use; the legacy columns are left as NULL by every writer here.
type AISignals struct {
	FileID        int64
	ClipEmbedding []byte    // raw BLOB, kept verbatim for round-tripping
	Embedding     []float32 // decoded from ClipEmbedding on read
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

// Upsert inserts or replaces the per-file AI signal row. When Embedding is
// non-nil and ClipEmbedding is nil, the embedding is encoded to little-endian
// f32 bytes and stored in clip_embedding.
func (r *AISignalsRepo) Upsert(a AISignals) error {
	clip := a.ClipEmbedding
	if clip == nil && len(a.Embedding) > 0 {
		clip = encodeEmbedding(a.Embedding)
	}
	_, err := r.db.ExecContext(context.Background(),
		`INSERT INTO ai_signals (file_id, clip_embedding, computed_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(file_id) DO UPDATE SET
		    clip_embedding = excluded.clip_embedding,
		    computed_at    = excluded.computed_at`,
		a.FileID, clip, a.ComputedAt)
	return err
}

// GetByFileID returns the AI signal row for the given file. Returns ErrNotFound if missing.
func (r *AISignalsRepo) GetByFileID(fileID int64) (AISignals, error) {
	row := r.db.QueryRowContext(context.Background(),
		`SELECT file_id, clip_embedding, vision_print, vision_print_id, computed_at FROM ai_signals WHERE file_id = ?`, fileID)
	var a AISignals
	var print []byte
	var printID sql.NullString
	err := row.Scan(&a.FileID, &a.ClipEmbedding, &print, &printID, &a.ComputedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AISignals{}, ErrNotFound
		}
		return AISignals{}, err
	}
	a.Embedding = decodeEmbedding(a.ClipEmbedding)
	a.Print = decodePrint(print)
	a.PrintID = printID.String
	return a, nil
}

// UpsertPrint stores the feature print of a file, stamped with printID, and
// the time it was computed. A nil print stores NULL with the id: Vision
// could not make one, and the file is not pending for printID. A non-nil
// print must have platform.FeaturePrintLen values. The row's other columns,
// clip_embedding included, are left alone.
func (r *AISignalsRepo) UpsertPrint(fileID int64, print []float32, printID string) error {
	if printID == "" {
		return errors.New("UpsertPrint: empty print id")
	}
	var blob []byte
	if print != nil {
		if len(print) != platform.FeaturePrintLen {
			return fmt.Errorf("UpsertPrint: print has %d values, want %d", len(print), platform.FeaturePrintLen)
		}
		blob = encodeEmbedding(print)
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
	return decodeEmbedding(blob)
}

// AllEmbeddings returns a map from file_id to decoded CLIP embedding for
// every ai_signals row that has one. Files without an embedding (typically
// because the CLIP model isn't installed) are absent from the map. Used by
// the grouper to avoid an N+1 query when reclustering thousands of files.
func (r *AISignalsRepo) AllEmbeddings() (map[int64][]float32, error) {
	rows, err := r.db.QueryContext(context.Background(),
		`SELECT file_id, clip_embedding FROM ai_signals WHERE clip_embedding IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[int64][]float32)
	for rows.Next() {
		var fid int64
		var blob []byte
		if err := rows.Scan(&fid, &blob); err != nil {
			return nil, err
		}
		if e := decodeEmbedding(blob); e != nil {
			out[fid] = e
		}
	}
	return out, rows.Err()
}

// decodeEmbedding turns a little-endian f32 BLOB into []float32.
// Returns nil if blob is empty or has a length not divisible by 4.
func decodeEmbedding(blob []byte) []float32 {
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

// encodeEmbedding turns a []float32 into a little-endian f32 BLOB.
func encodeEmbedding(emb []float32) []byte {
	out := make([]byte, len(emb)*4)
	for i, v := range emb {
		binary.LittleEndian.PutUint32(out[i*4:i*4+4], math.Float32bits(v))
	}
	return out
}

package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
)

// AISignals represents a row in the ai_signals table. The table predates
// the v1 scope cut and still has columns for retired per-file scores
// (sharpness, NIMA, faces, pHash, ...). Only the CLIP embedding survives;
// the legacy columns are left as NULL by every writer here.
type AISignals struct {
	FileID        int64
	ClipEmbedding []byte    // raw BLOB, kept verbatim for round-tripping
	Embedding     []float32 // decoded from ClipEmbedding on read
	ComputedAt    sql.NullInt64
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
		`SELECT file_id, clip_embedding, computed_at FROM ai_signals WHERE file_id = ?`, fileID)
	var a AISignals
	err := row.Scan(&a.FileID, &a.ClipEmbedding, &a.ComputedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AISignals{}, ErrNotFound
		}
		return AISignals{}, err
	}
	a.Embedding = decodeEmbedding(a.ClipEmbedding)
	return a, nil
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

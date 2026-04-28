package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
)

// AISignals represents a row in the ai_signals table.
type AISignals struct {
	FileID          int64
	Sharpness       sql.NullFloat64
	FaceCount       sql.NullInt64
	FacesEyesClosed sql.NullInt64
	ExposureClipPct sql.NullFloat64
	MeanLuma        sql.NullFloat64
	NIMAScore       sql.NullFloat64
	PHash           []byte
	ClipEmbedding   []byte    // raw BLOB (kept for round-tripping)
	Embedding       []float32 // decoded from ClipEmbedding on read; little-endian f32 array
	ComputedAt      sql.NullInt64
}

// AISignalsRepo provides access to the ai_signals table.
type AISignalsRepo struct{ db *sql.DB }

// AISignals returns an AISignalsRepo backed by the store's DB connection.
func (s *Store) AISignals() *AISignalsRepo { return &AISignalsRepo{db: s.db} }

const aiSignalsSelect = `SELECT file_id, sharpness, face_count, faces_eyes_closed,
	exposure_clip_pct, mean_luma, nima_score, phash, clip_embedding, computed_at
	FROM ai_signals`

// Upsert inserts or replaces the per-file AI signal row.
// When Embedding is non-nil and ClipEmbedding is nil, the embedding is encoded
// to little-endian f32 bytes and stored in clip_embedding.
func (r *AISignalsRepo) Upsert(a AISignals) error {
	clip := a.ClipEmbedding
	if clip == nil && len(a.Embedding) > 0 {
		clip = encodeEmbedding(a.Embedding)
	}
	_, err := r.db.ExecContext(context.Background(),
		`INSERT INTO ai_signals (file_id, sharpness, face_count, faces_eyes_closed,
		    exposure_clip_pct, mean_luma, nima_score, phash, clip_embedding, computed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(file_id) DO UPDATE SET
		    sharpness         = excluded.sharpness,
		    face_count        = excluded.face_count,
		    faces_eyes_closed = excluded.faces_eyes_closed,
		    exposure_clip_pct = excluded.exposure_clip_pct,
		    mean_luma         = excluded.mean_luma,
		    nima_score        = excluded.nima_score,
		    phash             = excluded.phash,
		    clip_embedding    = excluded.clip_embedding,
		    computed_at       = excluded.computed_at`,
		a.FileID, a.Sharpness, a.FaceCount, a.FacesEyesClosed,
		a.ExposureClipPct, a.MeanLuma, a.NIMAScore, a.PHash, clip, a.ComputedAt)
	return err
}

// GetByFileID returns the AI signal row for the given file. Returns ErrNotFound if missing.
func (r *AISignalsRepo) GetByFileID(fileID int64) (AISignals, error) {
	row := r.db.QueryRowContext(context.Background(),
		aiSignalsSelect+` WHERE file_id = ?`, fileID)
	return scanAISignals(row)
}

// GetByFile returns the AI signal row for the given file. Returns ErrNotFound if missing.
//
// Deprecated: use GetByFileID.
func (r *AISignalsRepo) GetByFile(fileID int64) (AISignals, error) {
	return r.GetByFileID(fileID)
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

// ListClipEmbeddings returns AISignals rows for files whose device_key matches
// and whose captured_at is in [bucketStart, bucketEnd). Used by the visual
// clusterer when grouping a single time bucket.
func (r *AISignalsRepo) ListClipEmbeddings(deviceKey string, bucketStart, bucketEnd int64) ([]AISignals, error) {
	rows, err := r.db.QueryContext(context.Background(),
		aiSignalsSelect+`
		 INNER JOIN files ON files.id = ai_signals.file_id
		 WHERE files.device_key   = ?
		   AND files.captured_at >= ?
		   AND files.captured_at <  ?
		   AND ai_signals.clip_embedding IS NOT NULL
		 ORDER BY files.captured_at, files.id`,
		deviceKey, bucketStart, bucketEnd)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []AISignals
	for rows.Next() {
		a, err := scanAISignals(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanAISignals(row rowScanner) (AISignals, error) {
	var a AISignals
	err := row.Scan(&a.FileID, &a.Sharpness, &a.FaceCount, &a.FacesEyesClosed,
		&a.ExposureClipPct, &a.MeanLuma, &a.NIMAScore, &a.PHash, &a.ClipEmbedding, &a.ComputedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AISignals{}, ErrNotFound
		}
		return AISignals{}, err
	}
	a.Embedding = decodeEmbedding(a.ClipEmbedding)
	return a, nil
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

package store

import (
	"context"
	"database/sql"
)

// FileMetadata holds EXIF-derived fields that can be set via UpdateMetadata.
// This is a convenience type for test helpers that need to set specific fields.
type FileMetadata struct {
	ISO         sql.NullInt64
	Aperture    sql.NullFloat64
	Shutter     sql.NullString
	FocalLength sql.NullFloat64
}

// UpdateMetadataFields updates selected metadata fields on the files row.
// Used by tests to inject EXIF data for focus-view testing.
func (r *FilesRepo) UpdateMetadataFields(id int64, m FileMetadata) error {
	_, err := r.db.ExecContext(context.Background(),
		`UPDATE files SET iso = ?, aperture = ?, shutter = ?, focal_length = ? WHERE id = ?`,
		m.ISO, m.Aperture, m.Shutter, m.FocalLength, id)
	return err
}

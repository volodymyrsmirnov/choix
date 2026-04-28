package meta

// Captured returns the best available capture timestamp for a metadata record
// as Unix seconds, plus an "ok" flag indicating whether any timestamp was
// found.
//
// Priority order (per spec):
//  1. DateTimeOriginal — the EXIF tag set by the camera at the moment of
//     capture; this is what we want.
//  2. CreateDate — set by the camera or by the file system; close enough.
//  3. ModifyDate — last-resort fallback; for unedited files this equals
//     capture time, but for edited files it lies. Better than nothing.
//
// If none of the three are populated, Captured returns (0, false). The caller
// is expected to bucket these files into a "no-timestamp" device bucket and
// flag them in the UI.
func Captured(m Metadata) (int64, bool) {
	if !m.DateTimeOriginal.IsZero() {
		return m.DateTimeOriginal.Unix(), true
	}
	if !m.CreateDate.IsZero() {
		return m.CreateDate.Unix(), true
	}
	if !m.ModifyDate.IsZero() {
		return m.ModifyDate.Unix(), true
	}
	return 0, false
}

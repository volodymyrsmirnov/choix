package rawpreview

import (
	"encoding/binary"
	"fmt"
)

// RAF header layout: a 16-byte magic, then, among other fields, the
// big-endian offset and length of the embedded JPEG.
const (
	rafMagic     = "FUJIFILMCCD-RAW "
	rafOffsetPos = 84
	rafLengthPos = 88
	rafHeaderLen = rafLengthPos + 4
)

// rafCandidates returns a RAF's single JPEG candidate from its header.
func rafCandidates(r *reader) ([]candidate, error) {
	h, err := r.read(0, rafHeaderLen)
	if err != nil {
		return nil, fmt.Errorf("header: %w", err)
	}
	if string(h[:len(rafMagic)]) != rafMagic {
		return nil, malformed("bad RAF magic")
	}
	return []candidate{{
		off: int64(binary.BigEndian.Uint32(h[rafOffsetPos:])),
		n:   int64(binary.BigEndian.Uint32(h[rafLengthPos:])),
	}}, nil
}

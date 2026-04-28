package meta

import "strings"

// DeviceKey returns the choix device-identity string for a parsed metadata
// record.
//
// Format (from spec):
//
//	"<Make> <Model>#<SerialNumber>"  if make and model are present
//	"<Make> <Model>#"                if make and model are present but no serial
//	"Unknown"                        if either make or model is missing
//
// Whitespace-only make/model is treated as missing.
func DeviceKey(m Metadata) string {
	make := strings.TrimSpace(m.Make)
	model := strings.TrimSpace(m.Model)
	if make == "" || model == "" {
		return "Unknown"
	}
	return make + " " + model + "#" + strings.TrimSpace(m.SerialNumber)
}

//go:build production && !server

package appdir

// dataOverride always returns "": the shipping build ignores
// CHOIX_DATA_DIR so the sandboxed app can only use its own container.
func dataOverride() (string, error) {
	return "", nil
}

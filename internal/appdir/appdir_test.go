package appdir

import (
	"strings"
	"testing"
)

// TestRoot_ApplicationSupport is the one test that resolves the real
// default location; Root creates <Application Support>/Choix and nothing
// else is written there.
func TestRoot_ApplicationSupport(t *testing.T) {
	t.Setenv("CHOIX_DATA_DIR", "")

	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if !strings.HasSuffix(got, "/Library/Application Support/Choix") {
		t.Errorf("Root = %q, want it to end with /Library/Application Support/Choix", got)
	}
}

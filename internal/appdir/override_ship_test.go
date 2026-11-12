//go:build production && !server

package appdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOverride_IgnoredInShippingBuild(t *testing.T) {
	over := filepath.Join(t.TempDir(), "new")
	t.Setenv("CHOIX_DATA_DIR", over)

	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got == over {
		t.Fatalf("Root = %q: the shipping build honoured CHOIX_DATA_DIR", got)
	}
	if !strings.HasSuffix(got, "/Library/Application Support/Choix") {
		t.Errorf("Root = %q, want it to end with /Library/Application Support/Choix", got)
	}
	if _, err := os.Stat(over); !os.IsNotExist(err) {
		t.Errorf("override directory %s was created (stat err %v)", over, err)
	}
}

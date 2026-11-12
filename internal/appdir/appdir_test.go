package appdir

import (
	"strings"
	"testing"
)

func TestBundleFrameworksDir_Layout(t *testing.T) {
	cases := map[string]string{
		"/x/Choix.app/Contents/Frameworks":            "/x/Choix.app/Contents/Frameworks",
		"/x/Choix.app/Contents/Frameworks/":           "/x/Choix.app/Contents/Frameworks",
		"/x/Choix.app/Contents/MacOS/../Frameworks":   "/x/Choix.app/Contents/Frameworks",
		"bin/Choix.app/Contents/Frameworks":           "",
		"":                                            "",
		"/var/folders/ab/T/go-build1/b001/Frameworks": "",
		"/x/Choix/Contents/Frameworks":                "",
		"/x/Choix.app/Frameworks":                     "",
		"/x/Choix.app/Contents/Resources":             "",
		"/x/.app/Contents/Frameworks":                 "",
	}
	for in, want := range cases {
		if got := bundleFrameworksDir(in); got != want {
			t.Errorf("bundleFrameworksDir(%q) = %q, want %q", in, got, want)
		}
	}
}

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

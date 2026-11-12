//go:build server

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestShellDialogs_AreTestSupport: the server build's picker and
// confirmation are the test-support dialogs the bound service feeds.
func TestShellDialogs_AreTestSupport(t *testing.T) {
	picker, confirm := shellDialogs()
	if picker != serverDialogs || confirm != serverDialogs {
		t.Fatalf("shellDialogs() = %T, %T; want the shared *testsupport.Dialogs", picker, confirm)
	}
	// Nothing set: the picker is cancelled and the removal declined.
	if paths, err := picker.PickFolders(); err != nil || paths != nil {
		t.Errorf("PickFolders = %v, %v; want nil, nil", paths, err)
	}
	if ok, err := confirm.ConfirmRemove("folder-1"); err != nil || ok {
		t.Errorf("ConfirmRemove = %v, %v; want false, nil", ok, err)
	}
}

func TestShellMenuState_None(t *testing.T) {
	if m := shellMenuState(); m != nil {
		t.Errorf("shellMenuState() = %T; want nil (no native menu in server builds)", m)
	}
}

func TestShellTitleBar_None(t *testing.T) {
	if b := shellTitleBar(); b != nil {
		t.Errorf("shellTitleBar() = %T; want nil (no window in server builds)", b)
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestShellAssets_ServesAssetsDir: with CHOIX_ASSETS_DIR set, the assets come
// from that directory, not from the embedded build.
func TestShellAssets_ServesAssetsDir(t *testing.T) {
	dir := t.TempDir()
	const marker = "<!doctype html><title>assets-dir-marker</title>"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envAssetsDir, dir)

	h := shellAssets()
	if rec := get(t, h, "/"); rec.Code != http.StatusOK || rec.Body.String() != marker {
		t.Fatalf("GET / = %d %q; want 200 with the directory's index.html", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "/app.js"); rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Errorf("GET /app.js = %d %q; want 200 with the directory's file", rec.Code, rec.Body.String())
	}
}

// TestShellAssets_EmbeddedWhenUnset: without CHOIX_ASSETS_DIR the embedded
// frontend is served, as in the desktop build.
func TestShellAssets_EmbeddedWhenUnset(t *testing.T) {
	t.Setenv(envAssetsDir, "")
	rec := get(t, shellAssets(), "/")
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("GET / = %d (%d bytes); want 200 with the embedded index.html", rec.Code, rec.Body.Len())
	}
}

// TestPinLoopback_DropsHostOverride: Wails lets WAILS_SERVER_HOST replace the
// configured bind host; the server build drops it so the test-support
// service is only ever reachable on 127.0.0.1.
func TestPinLoopback_DropsHostOverride(t *testing.T) {
	t.Setenv(envServerHost, "0.0.0.0")
	pinLoopback()
	if v, ok := os.LookupEnv(envServerHost); ok {
		t.Errorf("%s = %q after pinLoopback; want unset", envServerHost, v)
	}

	t.Setenv(envServerHost, "")
	pinLoopback() // set but empty: nothing to do, no panic
}

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestStaticHandlerCacheControl(t *testing.T) {
	fsys := fstest.MapFS{
		"main.89b5883b.js":  {Data: []byte("js")},
		"main.c68ede52.css": {Data: []byte("css")},
		"icon.0a1b2c3d.svg": {Data: []byte("<svg/>")},
		"icon.svg":          {Data: []byte("<svg/>")},
	}
	h := staticHandler(fsys)

	cases := []struct {
		path string
		want string
	}{
		{"/main.89b5883b.js", "public, max-age=31536000, immutable"},
		{"/main.c68ede52.css", "public, max-age=31536000, immutable"},
		{"/icon.0a1b2c3d.svg", "public, max-age=31536000, immutable"},
		{"/icon.svg", "public, max-age=3600"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", tc.path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != tc.want {
			t.Errorf("%s: Cache-Control %q, want %q", tc.path, got, tc.want)
		}
	}
}

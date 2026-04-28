package server

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/volodymyrsmirnov/choix/internal/firstrun"
)

// hashedAsset matches files whose name embeds a content hash, e.g.
// `main.89b5883b.js` or `main.c68ede52.css`. Such files are immutable —
// any change rolls a new filename — so we set Cache-Control accordingly.
var hashedAsset = regexp.MustCompile(`\.[0-9a-f]{8}\.(js|css|woff2?|map)$`)

// routes builds the full chi router from the Server's dependencies.
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()

	r.Use(s.idleResetMiddleware)
	r.Use(s.requestLogMiddleware)

	// IMPORTANT: Do not add a buffering middleware (chi/middleware.Compress
	// or similar) above the SSE route — it serializes flushes and breaks
	// live progress.

	// Static assets — embedded React bundle output.
	r.Handle("/static/*", http.StripPrefix("/static/", staticHandler(s.staticFS)))

	// JSON / SSE API.
	r.Get("/api/library", s.handleLibraryJSON)
	r.Get("/api/clusters/{clusterID}", s.handleClusterJSON)
	r.Get("/api/progress", s.handleProgressSSE)
	r.Get("/api/picks", s.handlePicksList)
	r.Post("/api/picks", s.handlePicksPost)
	r.Post("/api/recluster", s.handleReclusterPost)
	r.Get("/api/settings", s.handleSettingsGet)
	r.Post("/api/settings", s.handleSettingsPost)

	// First-run wizard — only registered when an installer was passed
	// in Config. Tests that construct Servers without one (the majority)
	// continue to see /setup return 404.
	if s.detector != nil {
		r.Method(http.MethodGet, "/setup", firstrun.NewSetupHandler(s.detector))
		r.Method(http.MethodPost, "/api/setup/install-model", firstrun.NewInstallModelHandler(s.cfg.Installer))
		r.Get("/api/setup/state", s.handleSetupState)
		r.Post("/api/setup/finalize", s.handleSetupFinalize)
	}

	// Media — path-keyed. The relative file path inside the scan root is
	// the URL identifier, with optional ?v=<content-hash> for cache
	// busting on content edits.
	r.Get("/thumb/*", s.handleThumb)
	r.Get("/full/*", s.handleFull)

	// SPA pages — every non-API path serves index.html so the React
	// router can pick it up. Keep this last.
	r.Get("/", s.serveIndex)
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		// Reserved namespaces fall through to 404.
		p := req.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/static/") ||
			strings.HasPrefix(p, "/thumb/") || strings.HasPrefix(p, "/full/") {
			http.NotFound(w, req)
			return
		}
		s.serveIndex(w, req)
	})

	return r
}

func staticHandler(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Files whose names contain a content hash (`main.<hash>.js`) are
		// immutable across releases — cache them aggressively. Anything
		// else (e.g. fonts the build chain doesn't hash) gets a short
		// cache lifetime so a release replaces it within an hour.
		if hashedAsset.MatchString(r.URL.Path) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		files.ServeHTTP(w, r)
	})
}

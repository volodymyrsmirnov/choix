package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	// URL is /thumb/<relative path>?tier=thumb|preview&v=<content-hash>.
	// Path-keyed URLs survive cluster rebuilds (which renumber clusters but
	// not files) and stay human-readable in DevTools. The optional `v`
	// query string is a content-hash cache buster — when the file's bytes
	// change, the API hands the React client a new hash, the URL changes,
	// and the browser refetches even though the path is the same.
	relPath := chi.URLParam(r, "*")
	if relPath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	tier := r.URL.Query().Get("tier")
	if tier == "" {
		tier = "thumb"
	}
	if tier != "thumb" && tier != "preview" {
		http.Error(w, "bad tier", http.StatusBadRequest)
		return
	}

	file, err := s.cfg.Store.Files().GetByPath(relPath)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id := file.ID
	rec, err := s.cfg.Store.Thumbs().Get(id, tier)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// rec.RelPath already includes the ".choix/thumbs/<bucket>/<file_id>-<tier>.jpg"
	// segment because thumb.Builder.upsertThumb stores the path relative to the
	// scan root. Joining ScanRoot + RelPath gives the absolute path, which is
	// then validated to prevent traversal via corrupt DB rows or symlinks.
	abs := filepath.Join(s.cfg.ScanRoot, rec.RelPath)
	safe, err := safeJoinUnderRoot(s.cfg.ScanRoot, abs)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f, err := os.Open(safe) //nolint:gosec // safe is validated by safeJoinUnderRoot
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Cheap ETag: file mtime + size. Stable per (id,tier).
	tag := fmt.Sprintf(`"t-%d-%d-%d"`, id, stat.ModTime().UnixNano(), stat.Size())
	if match := r.Header.Get("If-None-Match"); match != "" && match == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("ETag", tag)
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, filepath.Base(safe), stat.ModTime(), f)
}

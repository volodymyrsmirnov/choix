package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/volodymyrsmirnov/choix/internal/store"
	"github.com/volodymyrsmirnov/choix/internal/thumb"
)

// browserRenderableFormat reports whether the source format can be served
// as-is to a browser. HEIC and RAF cannot — Chrome and Firefox refuse to
// decode them, leaving the user staring at a broken image when they hit
// pixel-peep. Those formats get transcoded to a high-res JPEG on demand.
func browserRenderableFormat(format string) bool {
	switch format {
	case "jpeg", "png":
		return true
	}
	return false
}

func (s *Server) handleFull(w http.ResponseWriter, r *http.Request) {
	relPath := chi.URLParam(r, "*")
	if relPath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	rec, err := s.cfg.Store.Files().GetByPath(relPath)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Photos in formats browsers can't display get transcoded to a cached
	// JPEG (sized for retina pixel-peep). Videos and renderable photos go
	// straight to the original bytes.
	if rec.Kind == "photo" && !browserRenderableFormat(rec.Format) {
		jpegPath, jerr := s.ensureFullJPEG(r, &rec)
		if jerr == nil {
			s.serveFile(w, r, jpegPath, "image/jpeg")
			return
		}
		// Fall back to tier-2 preview (1600w) so the user sees *something*
		// rather than a broken icon if the transcode failed.
		if t, terr := s.cfg.Store.Thumbs().Get(rec.ID, thumb.TierPreview); terr == nil && t.RelPath != "" {
			abs := filepath.Join(s.cfg.ScanRoot, t.RelPath)
			if safe, safeErr := safeJoinUnderRoot(s.cfg.ScanRoot, abs); safeErr == nil {
				if _, statErr := os.Stat(safe); statErr == nil {
					s.serveFile(w, r, safe, "image/jpeg")
					return
				}
			}
		}
		http.Error(w, jerr.Error(), http.StatusInternalServerError)
		return
	}

	abs := filepath.Join(s.cfg.ScanRoot, rec.Path)
	clean, err := safeJoinUnderRoot(s.cfg.ScanRoot, abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	s.serveFile(w, r, clean, contentTypeFor(rec.Format))
}

// ensureFullJPEG returns the absolute path to a cached high-resolution JPEG
// transcode of the source file, building it on first request. The cache key
// is the deterministic CachePath layout under .choix/thumbs/<bucket>/<id>-full.jpg.
func (s *Server) ensureFullJPEG(r *http.Request, rec *store.File) (string, error) {
	dst := thumb.CachePath(s.cfg.ScanRoot, rec.ID, thumb.TierFullJPEG)
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 { //nolint:gosec // dst is built from rec.ID (DB PK) and ScanRoot, no user input
		return dst, nil
	}
	srcAbs := filepath.Join(s.cfg.ScanRoot, rec.Path)
	src, err := safeJoinUnderRoot(s.cfg.ScanRoot, srcAbs)
	if err != nil {
		return "", err
	}
	if _, _, err := thumb.SipsConvert(r.Context(), src, dst, thumb.WidthFullJPEG); err != nil {
		return "", err
	}
	return dst, nil
}

// safeJoinUnderRoot guards against path-traversal: the resolved absolute
// path must remain under the scan root.
//
// The function resolves both the scan root and the candidate path through
// symlinks before comparison. This is required on macOS, where /var is a
// symlink to /private/var — os.MkdirTemp and filepath.Abs return the
// unresolved /var/... prefix while EvalSymlinks yields /private/var/....
// Without resolving both sides the containment check produces false negatives.
//
// When the candidate path does not yet exist on disk (e.g. a cache file that
// will be created on first request), EvalSymlinks fails; the function falls
// back to a lexical check on the unresolved path, which is still sufficient
// for the non-symlink traversal cases (e.g. "../../etc/passwd" in rel_path).
func safeJoinUnderRoot(root, abs string) (string, error) {
	clean, err := filepath.Abs(filepath.Clean(abs))
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// Resolve the scan root through symlinks (best-effort).
	if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = resolved
	}

	containedUnder := func(path, root string) bool {
		return strings.HasPrefix(path+string(filepath.Separator), root+string(filepath.Separator))
	}

	// Attempt to resolve the candidate path through symlinks. When the path
	// exists on disk EvalSymlinks catches:
	//  (a) OS-level symlink prefixes (e.g. /var -> /private/var on macOS), and
	//  (b) a symlink inside .choix/thumbs that points outside the scan root.
	// When the path doesn't exist yet we fall back to the lexical check.
	if _, statErr := os.Stat(clean); statErr == nil {
		resolved, evalErr := filepath.EvalSymlinks(clean)
		if evalErr == nil {
			resolvedAbs, err := filepath.Abs(resolved)
			if err != nil {
				return "", err
			}
			if !containedUnder(resolvedAbs, rootAbs) {
				return "", errors.New("path escapes scan root via symlink")
			}
			return resolvedAbs, nil
		}
	}

	// Path doesn't exist yet or EvalSymlinks failed: fall back to lexical
	// containment on the unresolved path. Use the unresolved rootAbs here
	// because clean is also unresolved — they share the same unresolved prefix
	// so the prefix comparison is still valid.
	rootUnresolved, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if !containedUnder(clean, rootUnresolved) {
		return "", errors.New("path escapes scan root")
	}
	return clean, nil
}

// serveFile streams a file with the given content type via http.ServeContent
// so byte-range requests and caching headers work for big originals.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, path, contentType string) {
	f, err := os.OpenFile(path, os.O_RDONLY, 0) //nolint:gosec // path is validated upstream
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
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, filepath.Base(path), stat.ModTime(), f)
}

func contentTypeFor(format string) string {
	switch format {
	case "jpeg":
		return "image/jpeg"
	case "heic":
		return "image/heic"
	case "raf":
		return "application/octet-stream" // RAF has no IANA type
	case "mov":
		return "video/quicktime"
	case "png":
		return "image/png"
	default:
		return "application/octet-stream"
	}
}

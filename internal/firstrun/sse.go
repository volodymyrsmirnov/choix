// First-run wizard HTTP handlers. Designed to be mounted by the main
// server. The wizard now only covers tool + model installs; cloud-AI
// provider configuration was removed when the AI top-pick flow was
// retired.

package firstrun

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/deps"
)

//go:embed templates/setup.html
var templateFS embed.FS

// WriteProgressEvent emits a single SSE "progress" frame.
func WriteProgressEvent(w io.Writer, ev deps.ProgressEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: progress\ndata: %s\n\n", b)
	return err
}

// WriteDoneEvent emits a final "done" frame. err may be nil.
func WriteDoneEvent(w io.Writer, err error) error {
	payload := map[string]any{"ok": err == nil}
	if err != nil {
		payload["error"] = err.Error()
	}
	b, mErr := json.Marshal(payload)
	if mErr != nil {
		return mErr
	}
	_, wErr := fmt.Fprintf(w, "event: done\ndata: %s\n\n", b)
	return wErr
}

// StateDetector is used by SetupHandler to probe the environment.
type StateDetector interface {
	Detect(ctx context.Context) (SetupState, error)
}

// SetupHandler renders the first-run wizard when needed and redirects
// to the Library otherwise.
type SetupHandler struct {
	det StateDetector
	tpl *template.Template
}

// NewSetupHandler parses the embedded template once and returns a
// handler. Panics on template parse error — the templates are
// embedded, so this is a programmer error if it fails.
func NewSetupHandler(d StateDetector) *SetupHandler {
	tpl := template.Must(template.ParseFS(templateFS, "templates/setup.html"))
	return &SetupHandler{det: d, tpl: tpl}
}

func (h *SetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	st, err := h.det.Detect(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if st.IsReady() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tpl.Execute(w, st); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// InstallToolHandler serves POST /api/setup/install-tool. The
// response is text/event-stream with a "progress" event per chunk and
// a final "done" event.
type InstallToolHandler struct{ d Downloader }

// NewInstallToolHandler constructs a handler using the given Downloader.
func NewInstallToolHandler(d Downloader) *InstallToolHandler {
	return &InstallToolHandler{d: d}
}

func (h *InstallToolHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		_ = r.ParseForm() //nolint:gosec // G120: MaxBytesReader is set above
		name = r.Form.Get("name")
	}
	if name != "exiftool" && name != "ffmpeg" {
		http.Error(w, "unknown tool", http.StatusBadRequest)
		return
	}

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	cb := func(ev deps.ProgressEvent) {
		_ = WriteProgressEvent(w, ev)
		if flusher != nil {
			flusher.Flush()
		}
	}
	err := InstallTool(r.Context(), h.d, name, cb)
	_ = WriteDoneEvent(w, err)
	if flusher != nil {
		flusher.Flush()
	}
}

// InstallModelHandler serves POST /api/setup/install-model.
type InstallModelHandler struct{ m ModelInstaller }

// NewInstallModelHandler constructs a handler using the given ModelInstaller.
func NewInstallModelHandler(m ModelInstaller) *InstallModelHandler {
	return &InstallModelHandler{m: m}
}

func (h *InstallModelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	kindStr := r.URL.Query().Get("kind")
	if kindStr == "" {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		_ = r.ParseForm() //nolint:gosec // G120: MaxBytesReader is set above
		kindStr = r.Form.Get("kind")
	}
	kind, ok := local.ParseModelKind(kindStr)
	if !ok {
		http.Error(w, "unknown model kind", http.StatusBadRequest)
		return
	}

	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	cb := func(ev deps.ProgressEvent) {
		_ = WriteProgressEvent(w, ev)
		if flusher != nil {
			flusher.Flush()
		}
	}
	err := InstallModel(r.Context(), h.m, kind, cb)
	_ = WriteDoneEvent(w, err)
	if flusher != nil {
		flusher.Flush()
	}
}

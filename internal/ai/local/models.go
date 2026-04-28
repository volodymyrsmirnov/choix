package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ModelKind enumerates the local ONNX models choix uses. Only CLIP is in
// production use today; the AI top-pick flow that needed sharpness, face
// landmarks, and NIMA was retired and those models were removed from the
// registry.
type ModelKind int

const (
	ModelCLIP ModelKind = iota
)

func (k ModelKind) String() string {
	if k == ModelCLIP {
		return "clip"
	}
	return "unknown"
}

// ParseModelKind converts a string name to the corresponding ModelKind.
// Returns (0, false) for unrecognized names.
func ParseModelKind(s string) (ModelKind, bool) {
	if s == "clip" {
		return ModelCLIP, true
	}
	return 0, false
}

// Model describes one downloadable ONNX model.
type Model struct {
	Kind        ModelKind
	FileName    string
	URL         string
	SHA256      string
	InputShape  []int
	OutputShape []int
}

// KnownModels is the registry of pinned models. CLIP is pinned to a
// HuggingFace commit so the URL can't drift between releases.
var KnownModels = map[ModelKind]Model{
	ModelCLIP: {
		Kind:        ModelCLIP,
		FileName:    "clip_vit_b32.onnx",
		URL:         "https://huggingface.co/Qdrant/clip-ViT-B-32-vision/resolve/e0c24ed0fa57fa3e4f97f30de74c51d944036ace/model.onnx",
		SHA256:      "c68d3d9a200ddd2a8c8a5510b576d4c94d1ae383bf8b36dd8c084f94e1fb4d63",
		InputShape:  []int{1, 3, 224, 224},
		OutputShape: []int{1, 512},
	},
}

// Downloader is the minimal hash-pinned downloader contract.
type Downloader interface {
	Download(ctx context.Context, url, dest, sha256 string) error
}

// ModelStore manages on-disk model files under a single directory
// (typically ~/.choix/models/).
type ModelStore struct {
	dir        string
	downloader Downloader
}

// NewModelStore creates a ModelStore rooted at dir.
func NewModelStore(dir string) *ModelStore {
	return &ModelStore{dir: dir}
}

// WithDownloader attaches a Downloader and returns the receiver.
func (s *ModelStore) WithDownloader(d Downloader) *ModelStore {
	s.downloader = d
	return s
}

// Path returns the absolute path where the model file would live.
func (s *ModelStore) Path(kind ModelKind) string {
	return filepath.Join(s.dir, KnownModels[kind].FileName)
}

// Has returns true if the model file exists on disk and is non-empty.
func (s *ModelStore) Has(kind ModelKind) bool {
	st, err := os.Stat(s.Path(kind)) //nolint:gosec // G703: path is an internal model registry path
	return err == nil && st.Size() > 0
}

// Fetch downloads the model if missing. Verifies SHA256 after download.
func (s *ModelStore) Fetch(ctx context.Context, kind ModelKind) error {
	if s.Has(kind) {
		return nil
	}
	if s.downloader == nil {
		return errors.New("ModelStore: no downloader configured")
	}
	m, ok := KnownModels[kind]
	if !ok {
		return fmt.Errorf("unknown model kind %v", kind)
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil { //nolint:gosec // G301: 0750 is fine for model cache dir
		return fmt.Errorf("mkdir models dir: %w", err)
	}
	dest := s.Path(kind)
	if err := s.downloader.Download(ctx, m.URL, dest, m.SHA256); err != nil {
		return fmt.Errorf("download %s: %w", kind, err)
	}
	return nil
}

// VerifySHA256 reads the file at path and returns true when its hex SHA256 matches want.
func VerifySHA256(path, want string) (bool, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is an internal model cache path
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == want, nil
}

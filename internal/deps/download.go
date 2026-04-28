package deps

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// pinnedSHA is a sentinel SHA-256 placeholder. Every entry in DefaultSources
// that holds this value MUST be replaced with the real hash of the published
// static build before the first release. CI should refuse to ship a binary
// whose Sources still contain pinnedSHA.
const pinnedSHA = "PINNED-AT-RELEASE"

// ToolSource is one row of the pinned download table.
type ToolSource struct {
	// URL is the absolute https URL of the asset to download. When Format is
	// empty the URL must point directly at an executable binary. When Format is
	// "tar.gz" or "zip" the URL points at an archive; InnerPath names the
	// binary inside that archive.
	URL string
	// SHA256 is the lowercase hex-encoded SHA-256 of the bytes served at URL
	// (the archive bytes, not the extracted binary).
	SHA256 string
	// Format describes the archive container, if any. Accepted values:
	//   "" (empty) — URL is a raw executable binary, written directly.
	//   "tar.gz"   — URL is a gzip-compressed tar archive.
	//   "zip"      — URL is a ZIP archive.
	// Requires InnerPath when non-empty.
	Format string
	// InnerPath is the path inside the archive (relative, forward-slash
	// separated) of the binary to install. Ignored when Format is empty.
	InnerPath string
}

// DefaultSources is the production URL+hash table for choix's external tools.
//
// macOS arm64 only: v1 targets Apple Silicon per the design spec. Universal
// binary support (lipo of arm64 + x86_64) is scoped to plan 3 / distribution.
//
// IMPORTANT: every SHA256 below holds the sentinel "PINNED-AT-RELEASE" and
// MUST be replaced with the real hash of the published binary before the
// first tagged release. The release Makefile target should `grep -L
// PINNED-AT-RELEASE` and refuse to build if the sentinel is still present.
var DefaultSources = map[string]ToolSource{
	"exiftool": {
		// Upstream publishes macOS binaries on github.com/exiftool/exiftool
		// releases. Pin a specific tag (e.g. 13.00) when releasing.
		URL:       "https://github.com/exiftool/exiftool/releases/download/PINNED-AT-RELEASE/exiftool-macos.tar.gz",
		SHA256:    pinnedSHA,
		Format:    "tar.gz",
		InnerPath: "exiftool",
	},
	"ffmpeg": {
		// osxexperts.net publishes static macOS arm64 ffmpeg builds.
		// Mirror or pin a specific archive URL at release time.
		URL:       "https://www.osxexperts.net/ffmpeg-PINNED-AT-RELEASE-arm64.zip",
		SHA256:    pinnedSHA,
		Format:    "zip",
		InnerPath: "ffmpeg",
	},
}

// Downloader implements Fetcher by streaming pinned URLs into the support
// directory and verifying SHA-256 before installation. Construct with
// NewDownloader for production defaults; tests override Sources to point at
// httptest fixtures.
type Downloader struct {
	// SupportDir is where verified binaries land. Must already exist (or be
	// creatable). Typically ~/Library/Application Support/choix/bin.
	SupportDir string
	// Sources is the pinned URL+hash table indexed by tool name.
	Sources map[string]ToolSource
	// HTTPClient overrides the default http.Client. nil uses http.DefaultClient.
	HTTPClient *http.Client
}

// NewDownloader returns a Downloader configured with DefaultSources and the
// given support directory.
func NewDownloader(supportDir string) *Downloader {
	return &Downloader{
		SupportDir: supportDir,
		Sources:    DefaultSources,
	}
}

// Fetch downloads name into the support directory, verifies it against the
// pinned SHA-256, and returns the absolute path to the installed binary.
//
// For Format=="" (raw binary): streams the body while computing the SHA-256,
// verifies the hash, chmod 0755, atomically renames into place.
//
// For Format=="tar.gz" or "zip": downloads the archive to a temp file while
// computing its SHA-256, verifies the hash, then extracts the file at
// InnerPath from the archive into a second temp file, chmod 0755, atomically
// renames the extracted binary into place and removes the archive temp file.
//
// On any failure all temp files are removed.
func (d *Downloader) Fetch(ctx context.Context, name string) (string, error) {
	src, ok := d.Sources[name]
	if !ok {
		return "", fmt.Errorf("deps: no pinned source for %q", name)
	}
	if src.SHA256 == pinnedSHA {
		return "", fmt.Errorf("deps: %q has unpinned SHA256 %q; replace before release",
			name, pinnedSHA)
	}
	if err := os.MkdirAll(d.SupportDir, 0o750); err != nil {
		return "", fmt.Errorf("deps: mkdir support dir: %w", err)
	}
	dest := filepath.Join(d.SupportDir, name)
	tmp := dest + ".tmp"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return "", fmt.Errorf("deps: build request for %s: %w", name, err)
	}
	client := d.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("deps: GET %s: %w", src.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("deps: GET %s: status %s", src.URL, resp.Status)
	}

	// We deliberately use 0o755 here — the file is an executable tool
	// (ffmpeg, exiftool) that needs to be runnable by the user.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755) //nolint:gosec // executable bits required
	if err != nil {
		return "", fmt.Errorf("deps: open tmp: %w", err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hasher), resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("deps: download body: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("deps: close tmp: %w", err)
	}
	gotHex := hex.EncodeToString(hasher.Sum(nil))
	if gotHex != src.SHA256 {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("deps: %s hash mismatch: got %s want %s", name, gotHex, src.SHA256)
	}

	// Raw binary: rename directly into place.
	if src.Format == "" {
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("deps: rename into place: %w", err)
		}
		return dest, nil
	}

	// Archive: extract the named inner binary, then remove the archive tmp.
	binTmp := dest + ".bin.tmp"
	if err := extractBinary(src.Format, tmp, src.InnerPath, binTmp); err != nil {
		_ = os.Remove(tmp)
		_ = os.Remove(binTmp)
		return "", fmt.Errorf("deps: extract %s from archive: %w", src.InnerPath, err)
	}
	_ = os.Remove(tmp)
	if err := os.Rename(binTmp, dest); err != nil {
		_ = os.Remove(binTmp)
		return "", fmt.Errorf("deps: rename extracted binary into place: %w", err)
	}
	return dest, nil
}

// extractBinary reads the archive at archivePath, finds the entry named
// innerPath, and writes it (mode 0755) to destPath.
func extractBinary(format, archivePath, innerPath, destPath string) error {
	switch format {
	case "tar.gz":
		return extractTarGz(archivePath, innerPath, destPath)
	case "zip":
		return extractZip(archivePath, innerPath, destPath)
	default:
		return fmt.Errorf("unknown archive format %q", format)
	}
}

func extractTarGz(archivePath, innerPath, destPath string) error {
	af, err := os.Open(archivePath) //nolint:gosec // archivePath is a controlled temp file path
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = af.Close() }()

	gr, err := gzip.NewReader(af)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		if hdr.Name != innerPath {
			continue
		}
		return writeBinaryFile(tr, destPath)
	}
	return fmt.Errorf("entry %q not found in tar.gz archive", innerPath)
}

func extractZip(archivePath, innerPath, destPath string) error {
	zr, err := zip.OpenReader(archivePath) //nolint:gosec // archivePath is a controlled temp file path
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	for _, f := range zr.File {
		if f.Name != innerPath {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry: %w", err)
		}
		writeErr := writeBinaryFile(rc, destPath)
		_ = rc.Close()
		return writeErr
	}
	return fmt.Errorf("entry %q not found in zip archive", innerPath)
}

// writeBinaryFile copies r into destPath with mode 0755.
func writeBinaryFile(r io.Reader, destPath string) error {
	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755) //nolint:gosec // executable bits required
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return fmt.Errorf("write dest: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close dest: %w", err)
	}
	return nil
}

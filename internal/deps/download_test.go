package deps

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- helpers -----------------------------------------------------------------

// makeTarGz returns a .tar.gz archive containing a single file at innerPath
// with the given content.
func makeTarGz(t *testing.T, innerPath string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name: innerPath,
		Mode: 0o755,
		Size: int64(len(content)),
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// makeZip returns a .zip archive containing a single file at innerPath with
// the given content.
func makeZip(t *testing.T, innerPath string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(innerPath)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// --- raw binary (existing behaviour) -----------------------------------------

func TestDownloaderFetchesAndVerifiesHash(t *testing.T) {
	body := []byte("#!/bin/sh\necho fake-exiftool\n")
	wantHex := hexSum(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	supportDir := t.TempDir()
	d := &Downloader{
		SupportDir: supportDir,
		Sources: map[string]ToolSource{
			"exiftool": {URL: srv.URL, SHA256: wantHex},
		},
	}
	path, err := d.Fetch(context.Background(), "exiftool")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := filepath.Join(supportDir, "exiftool")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(got) != string(body) {
		t.Error("installed bytes differ from served bytes")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want executable", info.Mode().Perm())
	}
}

func TestDownloaderRejectsBadHash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	t.Cleanup(srv.Close)

	supportDir := t.TempDir()
	d := &Downloader{
		SupportDir: supportDir,
		Sources: map[string]ToolSource{
			"exiftool": {URL: srv.URL, SHA256: strings.Repeat("0", 64)},
		},
	}
	_, err := d.Fetch(context.Background(), "exiftool")
	if err == nil {
		t.Fatal("Fetch: expected hash-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "hash") {
		t.Errorf("err = %v, want it to mention hash", err)
	}
	// Temp file must be cleaned up on failure.
	entries, _ := os.ReadDir(supportDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover tmp file %q after failed download", e.Name())
		}
	}
}

func TestDownloaderUnknownTool(t *testing.T) {
	d := &Downloader{
		SupportDir: t.TempDir(),
		Sources:    map[string]ToolSource{},
	}
	_, err := d.Fetch(context.Background(), "ffmpeg")
	if err == nil {
		t.Fatal("Fetch: expected error for unknown tool, got nil")
	}
}

// --- tar.gz extraction -------------------------------------------------------

func TestDownloaderFetchesTarGz(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\necho fake-exiftool\n")
	archive := makeTarGz(t, "exiftool", binaryContent)
	archiveSum := hexSum(archive)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	supportDir := t.TempDir()
	d := &Downloader{
		SupportDir: supportDir,
		Sources: map[string]ToolSource{
			"exiftool": {
				URL:       srv.URL,
				SHA256:    archiveSum,
				Format:    "tar.gz",
				InnerPath: "exiftool",
			},
		},
	}
	path, err := d.Fetch(context.Background(), "exiftool")
	if err != nil {
		t.Fatalf("Fetch tar.gz: %v", err)
	}
	want := filepath.Join(supportDir, "exiftool")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if !bytes.Equal(got, binaryContent) {
		t.Error("installed bytes differ from original binary content")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want executable", info.Mode().Perm())
	}
	// Archive tmp must be cleaned up.
	entries, _ := os.ReadDir(supportDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover tmp file %q", e.Name())
		}
	}
}

func TestDownloaderTarGzBadHash(t *testing.T) {
	archive := makeTarGz(t, "exiftool", []byte("binary"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	supportDir := t.TempDir()
	d := &Downloader{
		SupportDir: supportDir,
		Sources: map[string]ToolSource{
			"exiftool": {
				URL:       srv.URL,
				SHA256:    strings.Repeat("0", 64),
				Format:    "tar.gz",
				InnerPath: "exiftool",
			},
		},
	}
	_, err := d.Fetch(context.Background(), "exiftool")
	if err == nil {
		t.Fatal("expected hash-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "hash") {
		t.Errorf("err = %v, want it to mention hash", err)
	}
	entries, _ := os.ReadDir(supportDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover tmp file %q", e.Name())
		}
	}
}

func TestDownloaderTarGzInnerPathNotFound(t *testing.T) {
	archive := makeTarGz(t, "othertool", []byte("binary"))
	archiveSum := hexSum(archive)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	supportDir := t.TempDir()
	d := &Downloader{
		SupportDir: supportDir,
		Sources: map[string]ToolSource{
			"exiftool": {
				URL:       srv.URL,
				SHA256:    archiveSum,
				Format:    "tar.gz",
				InnerPath: "exiftool",
			},
		},
	}
	_, err := d.Fetch(context.Background(), "exiftool")
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want it to mention not found", err)
	}
}

// --- zip extraction ----------------------------------------------------------

func TestDownloaderFetchesZip(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\necho fake-ffmpeg\n")
	archive := makeZip(t, "ffmpeg", binaryContent)
	archiveSum := hexSum(archive)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	supportDir := t.TempDir()
	d := &Downloader{
		SupportDir: supportDir,
		Sources: map[string]ToolSource{
			"ffmpeg": {
				URL:       srv.URL,
				SHA256:    archiveSum,
				Format:    "zip",
				InnerPath: "ffmpeg",
			},
		},
	}
	path, err := d.Fetch(context.Background(), "ffmpeg")
	if err != nil {
		t.Fatalf("Fetch zip: %v", err)
	}
	want := filepath.Join(supportDir, "ffmpeg")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if !bytes.Equal(got, binaryContent) {
		t.Error("installed bytes differ from original binary content")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want executable", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(supportDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover tmp file %q", e.Name())
		}
	}
}

func TestDownloaderZipBadHash(t *testing.T) {
	archive := makeZip(t, "ffmpeg", []byte("binary"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	supportDir := t.TempDir()
	d := &Downloader{
		SupportDir: supportDir,
		Sources: map[string]ToolSource{
			"ffmpeg": {
				URL:       srv.URL,
				SHA256:    strings.Repeat("0", 64),
				Format:    "zip",
				InnerPath: "ffmpeg",
			},
		},
	}
	_, err := d.Fetch(context.Background(), "ffmpeg")
	if err == nil {
		t.Fatal("expected hash-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "hash") {
		t.Errorf("err = %v, want it to mention hash", err)
	}
	entries, _ := os.ReadDir(supportDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover tmp file %q", e.Name())
		}
	}
}

func TestDownloaderZipInnerPathNotFound(t *testing.T) {
	archive := makeZip(t, "othertool", []byte("binary"))
	archiveSum := hexSum(archive)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	supportDir := t.TempDir()
	d := &Downloader{
		SupportDir: supportDir,
		Sources: map[string]ToolSource{
			"ffmpeg": {
				URL:       srv.URL,
				SHA256:    archiveSum,
				Format:    "zip",
				InnerPath: "ffmpeg",
			},
		},
	}
	_, err := d.Fetch(context.Background(), "ffmpeg")
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want it to mention not found", err)
	}
}

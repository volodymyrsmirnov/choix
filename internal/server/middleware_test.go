package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestRequestLogEmitsSlogLine(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	env := newTestServer(t)
	resp := env.get("/api/library")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	if !strings.Contains(buf.String(), "method=GET") {
		t.Errorf("log missing method: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "path=/api/library") {
		t.Errorf("log missing path: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "status=200") {
		t.Errorf("log missing status: %q", buf.String())
	}
}

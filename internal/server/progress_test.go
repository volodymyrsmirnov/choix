package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/pipeline"
)

func TestProgressSSEDeliversEvent(t *testing.T) {
	env := newTestServer(t)

	events := make(chan pipeline.Event, 4)
	env.server.progress.Attach(events)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", env.httptest.URL+"/api/progress", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/progress: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}

	// Push an event, then read until we see a data: line.
	want := pipeline.Event{Stage: "discover", Done: 3, Total: 10}
	events <- want

	rdr := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(1500 * time.Millisecond)
	var got pipeline.Event
	for time.Now().Before(deadline) {
		line, err := rdr.ReadString('\n')
		if err != nil && err != io.EOF {
			t.Fatalf("read: %v", err)
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if err := json.Unmarshal([]byte(payload), &got); err != nil {
			t.Fatalf("unmarshal data: %v", err)
		}
		break
	}
	if got != want {
		t.Errorf("got %+v want %+v", got, want)
	}
}

func TestProgressResponseIsUnbuffered(t *testing.T) {
	env := newTestServer(t)
	events := make(chan pipeline.Event, 1)
	env.server.progress.Attach(events)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", env.httptest.URL+"/api/progress", nil)
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Push an event and ensure we read its data: line in well under a second.
	events <- pipeline.Event{Stage: "thumb", Done: 1, Total: 1}
	rdr := bufio.NewReader(resp.Body)
	start := time.Now()
	for {
		line, err := rdr.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v (after %s)", err, time.Since(start))
		}
		if strings.HasPrefix(line, "data:") {
			break
		}
	}
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Errorf("first event took %s — buffering middleware suspected", elapsed)
	}
}

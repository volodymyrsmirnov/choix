package system

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
)

// recordedLog is one call of clientErrors.log.
type recordedLog struct {
	msg  string
	args map[string]any
}

// fakeClientErrors returns a limiter on a clock the test moves, and what it logged.
func fakeClientErrors() (*clientErrors, *time.Time, *[]recordedLog) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var mu sync.Mutex
	var logs []recordedLog
	c := &clientErrors{
		now: func() time.Time { return now },
		log: func(msg string, args ...any) {
			m := map[string]any{}
			for i := 0; i+1 < len(args); i += 2 {
				m[args[i].(string)] = args[i+1]
			}
			mu.Lock()
			logs = append(logs, recordedLog{msg, m})
			mu.Unlock()
		},
	}
	return c, &now, &logs
}

func TestClientErrors_LogsOneLine(t *testing.T) {
	c, _, logs := fakeClientErrors()
	c.report("render:focus", "Cannot read properties of undefined", "TypeError: x\n    at A\n\n    at B\n")
	if len(*logs) != 1 {
		t.Fatalf("logged %d reports, want 1", len(*logs))
	}
	got := (*logs)[0]
	if got.msg != "frontend error" {
		t.Errorf("msg = %q, want %q", got.msg, "frontend error")
	}
	if got.args["where"] != "render:focus" || got.args["message"] != "Cannot read properties of undefined" {
		t.Errorf("args = %v", got.args)
	}
	if got.args["stack"] != "TypeError: x | at A | at B" {
		t.Errorf("stack = %q, want the non-empty lines trimmed and joined", got.args["stack"])
	}
	if _, ok := got.args["dropped"]; ok {
		t.Errorf("dropped reported with nothing dropped: %v", got.args)
	}
}

func TestClientErrors_Bounded(t *testing.T) {
	c, _, logs := fakeClientErrors()
	var stack strings.Builder
	for i := range 100 {
		fmt.Fprintf(&stack, "    at frame%d %s\n", i, strings.Repeat("x", 500))
	}
	c.report("render\nfocus"+strings.Repeat("w", 200), strings.Repeat("m", 5000), stack.String())
	got := (*logs)[0].args
	where := got["where"].(string)
	if strings.Contains(where, "\n") || len(where) > clientWhereMax+len("…") {
		t.Errorf("where = %q: want one line of at most %d bytes", where, clientWhereMax)
	}
	if m := got["message"].(string); len(m) > clientMessageMax+len("…") || !strings.HasSuffix(m, "…") {
		t.Errorf("message is %d bytes, want it cut to %d and marked", len(m), clientMessageMax)
	}
	parts := strings.Split(got["stack"].(string), " | ")
	if len(parts) != clientStackLines+1 || parts[clientStackLines] != "…" {
		t.Fatalf("stack has %d parts, want %d lines and the cut mark", len(parts), clientStackLines)
	}
	for _, p := range parts {
		if len(p) > clientStackLineMax+len("…") {
			t.Fatalf("a stack line is %d bytes, want at most %d", len(p), clientStackLineMax)
		}
	}
}

func TestClientErrors_ClipKeepsRunes(t *testing.T) {
	s := strings.Repeat("é", 10) // 2 bytes each
	got := clip(s, 5)
	if got != "éé…" {
		t.Errorf("clip = %q, want two whole runes and the mark", got)
	}
}

func TestClientErrors_RateLimited(t *testing.T) {
	c, now, logs := fakeClientErrors()
	for range clientBurst + 25 {
		c.report("window:error", "loop", "")
	}
	if len(*logs) != clientBurst {
		t.Fatalf("a burst of %d reports logged %d lines, want %d", clientBurst+25, len(*logs), clientBurst)
	}
	// The next window logs again, saying how many were dropped.
	*now = now.Add(clientWindow)
	c.report("window:error", "after", "")
	if len(*logs) != clientBurst+1 {
		t.Fatalf("logged %d after the window, want %d", len(*logs), clientBurst+1)
	}
	if got := (*logs)[clientBurst].args["dropped"]; got != 25 {
		t.Errorf("dropped = %v, want 25", got)
	}
}

func TestReportClientError_LogsThroughSlog(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := NewService(Deps{Gate: &opgate.Gate{}})
	if err := s.ReportClientError("render:library", "Invalid time value", "RangeError: Invalid time value\n    at dayParts"); err != nil {
		t.Fatalf("ReportClientError = %v, want nil", err)
	}
	out := buf.String()
	for _, want := range []string{"level=ERROR", `msg="frontend error"`, "where=render:library", `message="Invalid time value"`, "at dayParts"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q lacks %q", out, want)
		}
	}
}

func TestReportClientError_NeverFailsAfterGateClosed(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	g := &opgate.Gate{}
	s := NewService(Deps{Gate: g})
	g.Close(0)
	if err := s.ReportClientError("window:error", "late", ""); err != nil {
		t.Fatalf("ReportClientError after quit began = %v, want nil (a report never fails its caller)", err)
	}
	if strings.Contains(buf.String(), "frontend error") {
		t.Errorf("a report after quit began was logged: %q", buf.String())
	}
}

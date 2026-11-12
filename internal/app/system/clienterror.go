package system

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Bounds of one frontend error report (ReportClientError). A report is
// cut to them before it is logged, so a page cannot write an unbounded line.
const (
	clientWhereMax     = 64
	clientMessageMax   = 1000
	clientStackLines   = 40
	clientStackLineMax = 300
)

// Rate limit of frontend error reports: at most clientBurst reports are
// logged per clientWindow; the rest are counted and the count is logged
// with the next report that gets through. A render loop on the page cannot
// flood the log.
const (
	clientBurst  = 10
	clientWindow = time.Minute
)

// clientErrors logs the frontend's error reports, rate-limited. Safe for
// concurrent use.
type clientErrors struct {
	now func() time.Time
	log func(msg string, args ...any)

	mu      sync.Mutex
	start   time.Time // the current window's start
	logged  int       // reports logged in the current window
	dropped int       // reports dropped since the last one logged
}

// newClientErrors returns a limiter on the real clock that logs with
// slog.Error.
func newClientErrors() *clientErrors {
	return &clientErrors{now: time.Now, log: slog.Error}
}

// report logs one report unless the window's budget is spent.
func (c *clientErrors) report(where, message, stack string) {
	c.mu.Lock()
	now := c.now()
	if c.start.IsZero() || now.Sub(c.start) >= clientWindow {
		c.start, c.logged = now, 0
	}
	if c.logged >= clientBurst {
		c.dropped++
		c.mu.Unlock()
		return
	}
	c.logged++
	dropped := c.dropped
	c.dropped = 0
	c.mu.Unlock()

	args := []any{
		"where", clip(oneLine(where), clientWhereMax),
		"message", clip(message, clientMessageMax),
		"stack", clipStack(stack),
	}
	if dropped > 0 {
		args = append(args, "dropped", dropped)
	}
	c.log("frontend error", args...)
}

// clip cuts s to at most n bytes, on a rune boundary, marking the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// oneLine replaces line breaks with spaces.
func oneLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
}

// clipStack keeps the stack's first clientStackLines non-empty lines, each
// cut to clientStackLineMax bytes and trimmed, joined with " | " so the
// report stays one log line.
func clipStack(stack string) string {
	var out []string
	for line := range strings.Lines(stack) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(out) == clientStackLines {
			out = append(out, "…")
			break
		}
		out = append(out, clip(line, clientStackLineMax))
	}
	return strings.Join(out, " | ")
}

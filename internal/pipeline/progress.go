package pipeline

// Progress is one snapshot of pipeline state. Emitted to a buffered channel
// during Run; consumers (CLI, SSE handler) render these. JSON tags use
// lowercase field names because Progress is serialized straight onto SSE
// events that the browser reads.
type Progress struct {
	Stage  string `json:"stage"` // "metadata" | "thumb" | "analyze" | "cluster" | "discover"
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	Failed int    `json:"failed"`
	Phase  string `json:"phase"` // "starting" | "running" | "done" | "failed"
}

// ProgressBufferSize is the default capacity of progress channels. Chosen
// to absorb short bursts without blocking the worker pool.
const ProgressBufferSize = 16

// Reporter emits Progress events to a channel without ever blocking the
// caller. If the channel is full it drops the oldest pending event and
// pushes the newest one. A nil channel makes Update a no-op.
type Reporter struct {
	ch chan Progress
}

// NewReporter wraps ch. Pass nil to disable reporting.
func NewReporter(ch chan Progress) *Reporter { return &Reporter{ch: ch} }

// Update sends p without blocking. If the buffer is full the oldest pending
// event is discarded. Safe for concurrent use.
func (r *Reporter) Update(p Progress) {
	if r == nil || r.ch == nil {
		return
	}
	for {
		select {
		case r.ch <- p:
			return
		default:
			// Drop oldest. The receive may race with another sender
			// draining a slot — if so we'll just retry.
			select {
			case <-r.ch:
			default:
			}
		}
	}
}

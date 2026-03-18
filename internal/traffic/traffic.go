package traffic

import (
	"sync/atomic"
)

// Stats holds cumulative traffic counters for the current process lifetime.
type Stats struct {
	BytesSent     uint64 `json:"bytes_sent"`
	BytesReceived uint64 `json:"bytes_received"`
	BytesTotal    uint64 `json:"bytes_total"`
}

// Recorder accumulates traffic stats in-memory for the current run.
type Recorder struct {
	sent uint64
	recv uint64
}

// NewRecorder creates an empty traffic recorder.
func NewRecorder() *Recorder {
	return &Recorder{}
}

// Add increments the cumulative counters.
func (r *Recorder) Add(sent, recv uint64) {
	if r == nil || (sent == 0 && recv == 0) {
		return
	}
	if sent > 0 {
		atomic.AddUint64(&r.sent, sent)
	}
	if recv > 0 {
		atomic.AddUint64(&r.recv, recv)
	}
}

// Snapshot returns the current cumulative counters.
func (r *Recorder) Snapshot() Stats {
	if r == nil {
		return Stats{}
	}
	sent := atomic.LoadUint64(&r.sent)
	recv := atomic.LoadUint64(&r.recv)
	return Stats{
		BytesSent:     sent,
		BytesReceived: recv,
		BytesTotal:    sent + recv,
	}
}

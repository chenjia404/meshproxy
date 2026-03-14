package protocol

import "time"

// CircuitState represents the lifecycle state of a circuit.
type CircuitState string

const (
	// CircuitNew means the circuit object is created but not yet negotiated.
	CircuitNew CircuitState = "NEW"
	// CircuitCreating means the circuit is in the process of being established.
	CircuitCreating CircuitState = "CREATING"
	// CircuitOpen means the circuit is successfully established and ready for use.
	CircuitOpen CircuitState = "OPEN"
	// CircuitDegraded means the circuit is still usable but has encountered issues.
	CircuitDegraded CircuitState = "DEGRADED"
	// CircuitClosed means the circuit is fully closed and cannot be used.
	CircuitClosed CircuitState = "CLOSED"
)

// PathHop describes a single hop in a planned circuit path.
type PathHop struct {
	PeerID  string `json:"peer_id"`
	IsRelay bool   `json:"is_relay"`
	IsExit  bool   `json:"is_exit"`
}

// PathPlan describes the end-to-end path used by a circuit.
// For direct exit, Hops 包含單一 exit hop。
// 對於 relay+exit，Hops 至少包含一個 relay hop 和一個 exit hop。
type PathPlan struct {
	Hops         []PathHop `json:"hops"`
	ExitHopIndex int       `json:"exit_hop_index"`
}

// CircuitInfo is a readonly view of a circuit for external consumers (e.g. API).
type CircuitInfo struct {
	ID                  string       `json:"id"`
	State               CircuitState `json:"state"`
	Plan                PathPlan     `json:"plan"`
	CreatedAt           time.Time    `json:"created_at"`
	UpdatedAt           time.Time    `json:"updated_at"`
	LastPingAt          time.Time    `json:"last_ping_at"`
	LastPongAt          time.Time    `json:"last_pong_at"`
	Alive               bool         `json:"alive"`
	ConsecutiveFailures int          `json:"consecutive_failures"`
	SmoothedRTTMillis   float64      `json:"smoothed_rtt_ms"`
	BytesSent           uint64       `json:"bytes_sent"`
	BytesReceived       uint64       `json:"bytes_received"`
}

package client

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
)

// StreamErrClosed is returned to the application when the circuit/stream is closed remotely.
var StreamErrClosed = errors.New("circuit stream closed: connection reset by remote")

// streamErrorConn wraps net.Conn and allows injecting a read error when circuit closes (clearer error to app).
type streamErrorConn struct {
	net.Conn
	mu      sync.Mutex
	readErr error
}

func (c *streamErrorConn) Read(b []byte) (n int, err error) {
	c.mu.Lock()
	e := c.readErr
	c.mu.Unlock()
	if e != nil {
		return 0, e
	}
	return c.Conn.Read(b)
}

// SetReadError sets the error returned by subsequent Read (e.g. when circuit is closed).
func (c *streamErrorConn) SetReadError(err error) {
	c.mu.Lock()
	c.readErr = err
	c.mu.Unlock()
}

// udpReplyConn implements net.Conn for UDP streams: Write sends data to a channel
// for the SOCKS5 UDP handler to forward to the client; Read returns EOF.
type udpReplyConn struct {
	ch chan []byte
}

func (c *udpReplyConn) Read(b []byte) (n int, err error)   { return 0, io.EOF }
func (c *udpReplyConn) Write(b []byte) (n int, err error)  { c.ch <- append([]byte(nil), b...); return len(b), nil }
func (c *udpReplyConn) Close() error                       { close(c.ch); return nil }
func (c *udpReplyConn) LocalAddr() net.Addr                { return nil }
func (c *udpReplyConn) RemoteAddr() net.Addr               { return nil }
func (c *udpReplyConn) SetDeadline(t time.Time) error      { return nil }
func (c *udpReplyConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *udpReplyConn) SetWriteDeadline(t time.Time) error { return nil }

// Stream represents a logical stream bound to a local TCP connection or UDP reply channel.
type Stream struct {
	ID         string
	CircuitID   string
	Conn       net.Conn
	TargetHost string // set after BeginTCP for API observability
	TargetPort int
}

// StreamInfo is a read-only snapshot of a stream for API (no Conn).
type StreamInfo struct {
	ID         string `json:"id"`
	CircuitID   string `json:"circuit_id"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
	State      string `json:"state"` // "active"
}

// StreamManager tracks active streams and maps incoming data back to local connections.
type StreamManager struct {
	mu      sync.RWMutex
	streams map[string]*Stream
}

// NewStreamManager creates a new StreamManager.
func NewStreamManager() *StreamManager {
	return &StreamManager{
		streams: make(map[string]*Stream),
	}
}

// NewStreamID allocates a new unique stream ID.
func (m *StreamManager) NewStreamID() string {
	return uuid.NewString()
}

// RegisterStream registers a new stream. Wraps conn so circuit close can report a clear error.
func (m *StreamManager) RegisterStream(circuitID string, conn net.Conn) *Stream {
	wrapped := &streamErrorConn{Conn: conn}
	s := &Stream{
		ID:        m.NewStreamID(),
		CircuitID: circuitID,
		Conn:      wrapped,
	}
	m.mu.Lock()
	m.streams[s.ID] = s
	m.mu.Unlock()
	return s
}

// RegisterStreamUDP registers a UDP stream. Data received from the circuit is sent to the
// returned channel; the caller should read from it and send UDP to the client. Close the
// stream with Remove(id) when done.
func (m *StreamManager) RegisterStreamUDP(circuitID string) (*Stream, <-chan []byte) {
	ch := make(chan []byte, 64)
	conn := &udpReplyConn{ch: ch}
	s := &Stream{
		ID:        m.NewStreamID(),
		CircuitID: circuitID,
		Conn:      conn,
	}
	m.mu.Lock()
	m.streams[s.ID] = s
	m.mu.Unlock()
	return s, ch
}

// Get returns the stream by ID.
func (m *StreamManager) Get(id string) (*Stream, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.streams[id]
	return s, ok
}

// Remove removes the stream from the manager and closes the connection (or UDP reply channel).
func (m *StreamManager) Remove(id string) {
	m.mu.Lock()
	s, ok := m.streams[id]
	if ok {
		delete(m.streams, id)
	}
	m.mu.Unlock()
	if ok && s.Conn != nil {
		_ = s.Conn.Close()
	}
}

// Unregister removes the stream from the manager without closing the connection (e.g. for retry with same conn).
func (m *StreamManager) Unregister(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.streams, id)
}

// SetStreamTarget sets the target host/port for a stream (call after BeginTCP for observability).
func (m *StreamManager) SetStreamTarget(id, host string, port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.streams[id]; ok {
		s.TargetHost = host
		s.TargetPort = port
	}
}

// ListStreams returns a snapshot of all streams for the API (no Conn exposed).
func (m *StreamManager) ListStreams() []StreamInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]StreamInfo, 0, len(m.streams))
	for _, s := range m.streams {
		out = append(out, StreamInfo{
			ID:         s.ID,
			CircuitID:  s.CircuitID,
			TargetHost: s.TargetHost,
			TargetPort: s.TargetPort,
			State:      "active",
		})
	}
	return out
}

// ListByCircuitID returns all streams for the given circuit (for notifying on circuit close).
func (m *StreamManager) ListByCircuitID(circuitID string) []*Stream {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Stream
	for _, s := range m.streams {
		if s.CircuitID == circuitID {
			out = append(out, s)
		}
	}
	return out
}

// NotifyCircuitClosed notifies all streams on the circuit with a clear error and closes their conns.
func (m *StreamManager) NotifyCircuitClosed(circuitID string, err error) {
	if err == nil {
		err = StreamErrClosed
	}
	m.mu.Lock()
	list := make([]*Stream, 0)
	for _, s := range m.streams {
		if s.CircuitID == circuitID {
			list = append(list, s)
		}
	}
	for _, s := range list {
		delete(m.streams, s.ID)
	}
	m.mu.Unlock()
	for _, s := range list {
		if w, ok := s.Conn.(*streamErrorConn); ok {
			w.SetReadError(err)
		}
		_ = s.Conn.Close()
	}
}


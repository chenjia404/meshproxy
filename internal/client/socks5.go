package client

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
)

// ErrorRecorder records errors for observability (e.g. API /api/v1/errors/recent). Optional.
type ErrorRecorder interface {
	Record(category, message string)
}

// Socks5Server handles incoming SOCKS5 connections and bridges them to circuits.
type Socks5Server struct {
	addr           string
	listener       net.Listener
	closeChan      chan struct{}
	circuitMgr     *CircuitManager
	pathSelector   *PathSelector
	streamManager  *StreamManager
	errorRecorder  ErrorRecorder
}

// NewSocks5Server creates a new SOCKS5 server instance. errRec may be nil.
func NewSocks5Server(addr string, cm *CircuitManager, selector *PathSelector, sm *StreamManager, errRec ErrorRecorder) *Socks5Server {
	return &Socks5Server{
		addr:          addr,
		closeChan:     make(chan struct{}),
		circuitMgr:    cm,
		pathSelector:  selector,
		streamManager: sm,
		errorRecorder: errRec,
	}
}

// Start begins listening for incoming TCP connections.
func (s *Socks5Server) Start() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = l
	log.Printf("[socks5] listening on %s", s.addr)

	go s.acceptLoop()
	return nil
}

// Addr returns the real listen address. Valid after Start.
func (s *Socks5Server) Addr() string {
	if s.listener == nil {
		return s.addr
	}
	return s.listener.Addr().String()
}

// Close shuts down the listener.
func (s *Socks5Server) Close() error {
	close(s.closeChan)
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Socks5Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closeChan:
				return
			default:
			}
			log.Printf("[socks5] accept error: %v", err)
			continue
		}

		go s.handleConn(conn)
	}
}

// poolReturnConn wraps a connection and returns the circuit to the pool when closed.
type poolReturnConn struct {
	net.Conn
	circuitID string
	mgr       *CircuitManager
	once      sync.Once
}

func (c *poolReturnConn) Close() error {
	var err error
	c.once.Do(func() {
		c.mgr.ReturnToPool(c.circuitID)
		err = c.Conn.Close()
	})
	return err
}

// handleConn implements the SOCKS5 state machine and uses the circuit pool when available.
func (s *Socks5Server) handleConn(conn net.Conn) {
	log.Printf("[socks5] new connection from %s to %s", conn.RemoteAddr().String(), conn.LocalAddr().String())

	host, port, err := s.handleHandshake(conn)
	if err != nil {
		log.Printf("[socks5] handshake failed: %v", err)
		return
	}

	log.Printf("[socks5] request to %s:%d", host, port)

	kind := PoolLowLatency

	// 4. Get circuit from pool or create on demand (with build retry / relay–exit failover)
	circuitID, err := s.circuitMgr.EnsureCircuitFromPool(kind)
	if err != nil {
		if s.errorRecorder != nil {
			s.errorRecorder.Record("circuit", "get/create circuit failed: "+err.Error())
		}
		log.Printf("[socks5] get/create circuit failed: %v", err)
		_ = sendSocks5Reply(conn, 0x01)
		return
	}
	log.Printf("[socks5] using circuit %s", circuitID)

	// Wrap conn so that when client closes we return the circuit to the pool
	wrapped := &poolReturnConn{Conn: conn, circuitID: circuitID, mgr: s.circuitMgr}

	// 5. Allocate stream
	stream := s.streamManager.RegisterStream(circuitID, wrapped)

	// 6. BEGIN_TCP with exit failover: retry with another circuit/exit on failure
	var lastBeginErr error
	if err := s.circuitMgr.BeginTCP(circuitID, stream.ID, host, int(port)); err != nil {
		lastBeginErr = err
		s.circuitMgr.MarkCircuitFailed(circuitID)
		s.streamManager.Unregister(stream.ID) // keep conn open for retry

		excludeExitIDs := []string{}
		if plan, ok := s.circuitMgr.GetPlan(circuitID); ok {
			excludeExitIDs = append(excludeExitIDs, plan.Hops[plan.ExitHopIndex].PeerID)
		}

		maxRetries := s.circuitMgr.BeginTCPRetries()
		for attempt := 0; attempt < maxRetries; attempt++ {
			circuitID, err = s.circuitMgr.CreateForPoolExcluding(kind, excludeExitIDs)
			if err != nil {
				lastBeginErr = err
				log.Printf("[socks5] create circuit (failover) failed: %v", err)
				break
			}
			wrapped = &poolReturnConn{Conn: conn, circuitID: circuitID, mgr: s.circuitMgr}
			stream = s.streamManager.RegisterStream(circuitID, wrapped)
			if err := s.circuitMgr.BeginTCP(circuitID, stream.ID, host, int(port)); err != nil {
				lastBeginErr = err
				log.Printf("[socks5] begin_tcp (failover) failed: %v", err)
				s.circuitMgr.MarkCircuitFailed(circuitID)
				s.streamManager.Unregister(stream.ID)
				if plan, ok := s.circuitMgr.GetPlan(circuitID); ok {
					excludeExitIDs = append(excludeExitIDs, plan.Hops[plan.ExitHopIndex].PeerID)
				}
				continue
			}
			lastBeginErr = nil
			break
		}
		if lastBeginErr != nil {
			if s.errorRecorder != nil {
				s.errorRecorder.Record("socks5", "begin_tcp failed after retries: "+lastBeginErr.Error())
			}
			log.Printf("[socks5] begin_tcp failed after retries: %v", lastBeginErr)
			_ = sendSocks5Reply(conn, 0x05)
			_ = conn.Close()
			return
		}
	}

	s.streamManager.SetStreamTarget(stream.ID, host, int(port))

	// 7. Respond OK to client
	if err := sendSocks5Reply(wrapped, 0x00); err != nil {
		log.Printf("[socks5] send reply failed: %v", err)
		s.circuitMgr.MarkCircuitFailed(circuitID)
		s.streamManager.Remove(stream.ID)
		_ = conn.Close()
		return
	}

	// 8. Start bidirectional relay (when wrapped closes, ReturnToPool is called)
	s.circuitMgr.StartDataPump(circuitID, stream.ID, wrapped)
}

// handleHandshake performs a minimal SOCKS5 NO-AUTH + CONNECT handshake and
// returns the requested host:port.
func (s *Socks5Server) handleHandshake(conn net.Conn) (string, uint16, error) {
	// 1. greeting
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", 0, err
	}
	if buf[0] != 0x05 {
		return "", 0, fmt.Errorf("unsupported version %d", buf[0])
	}
	nMethods := int(buf[1])
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", 0, err
	}

	// we only support NO AUTH (0x00)
	supportNoAuth := false
	for _, m := range methods {
		if m == 0x00 {
			supportNoAuth = true
			break
		}
	}
	if !supportNoAuth {
		// reply: no acceptable methods
		_, _ = conn.Write([]byte{0x05, 0xFF})
		return "", 0, fmt.Errorf("no acceptable auth method")
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return "", 0, err
	}

	// 2. request
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", 0, err
	}
	if header[0] != 0x05 {
		return "", 0, fmt.Errorf("invalid request version %d", header[0])
	}
	if header[1] != 0x01 { // CONNECT
		_ = sendSocks5Reply(conn, 0x07) // command not supported
		return "", 0, fmt.Errorf("unsupported cmd %d", header[1])
	}

	atyp := header[3]
	var host string
	switch atyp {
	case 0x01: // IPv4
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", 0, err
		}
		host = net.IP(addr).String()
	case 0x03: // domain name
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return "", 0, err
		}
		dlen := int(buf[0])
		domain := make([]byte, dlen)
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", 0, err
		}
		host = string(domain)
	default:
		_ = sendSocks5Reply(conn, 0x08) // address type not supported
		return "", 0, fmt.Errorf("unsupported atyp %d", atyp)
	}

	// port
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return "", 0, err
	}
	port := binary.BigEndian.Uint16(buf[:2])

	return host, port, nil
}

// sendSocks5Reply sends a minimal SOCKS5 reply with given status.
// We currently return a dummy bind addr of 0.0.0.0:0.
func sendSocks5Reply(conn net.Conn, rep byte) error {
	// VER REP RSV ATYP BND.ADDR BND.PORT
	resp := []byte{
		0x05,
		rep,
		0x00,
		0x01, // IPv4
		0x00, 0x00, 0x00, 0x00, // 0.0.0.0
		0x00, 0x00, // port 0
	}
	_, err := conn.Write(resp)
	return err
}


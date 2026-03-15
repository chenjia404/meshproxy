package client

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/p2p"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/safe"
	"github.com/chenjia404/meshproxy/internal/tunnel"
)

// ErrorRecorder records errors for observability (e.g. API /api/v1/errors/recent). Optional.
type ErrorRecorder interface {
	Record(category, message string)
}

// Socks5Server handles incoming SOCKS5 connections and bridges them to circuits.
type Socks5Server struct {
	addr              string
	listener          net.Listener
	closeChan         chan struct{}
	host              host.Host
	circuitMgr        *CircuitManager
	pathSelector      *PathSelector
	streamManager     *StreamManager
	errorRecorder     ErrorRecorder
	allowUDPAssociate bool
	tunnelToExit      bool
	exitUpstream      string
}

// NewSocks5Server creates a new SOCKS5 server instance. errRec may be nil.
func NewSocks5Server(addr string, h host.Host, cm *CircuitManager, selector *PathSelector, sm *StreamManager, errRec ErrorRecorder) *Socks5Server {
	return &Socks5Server{
		addr:              addr,
		closeChan:         make(chan struct{}),
		host:              h,
		circuitMgr:        cm,
		pathSelector:      selector,
		streamManager:     sm,
		errorRecorder:     errRec,
		allowUDPAssociate: false,
	}
}

// SetAllowUDPAssociate enables or disables SOCKS5 UDP ASSOCIATE. Call before Start().
func (s *Socks5Server) SetAllowUDPAssociate(enable bool) {
	s.allowUDPAssociate = enable
}

// SetTunnelToExit enables raw TCP tunneling to the selected exit's upstream SOCKS5 service.
func (s *Socks5Server) SetTunnelToExit(enable bool, upstream string) {
	s.tunnelToExit = enable
	s.exitUpstream = upstream
}

// Start begins listening for incoming TCP connections.
func (s *Socks5Server) Start() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = l
	log.Printf("[socks5] listening on %s", s.addr)

	safe.Go("client.socks5.acceptLoop", s.acceptLoop)
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

		safe.Go("client.socks5.handleConn", func() { s.handleConn(conn) })
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

func (s *Socks5Server) releaseCircuitAfterBeginError(circuitID string, err error) {
	if s.circuitMgr.CanReuseCircuitAfterBeginError(circuitID, err) {
		s.circuitMgr.ReturnToPool(circuitID)
		return
	}
	s.circuitMgr.MarkCircuitFailed(circuitID)
}

// handleConn implements the SOCKS5 state machine and uses the circuit pool when available.
func (s *Socks5Server) handleConn(conn net.Conn) {
	log.Printf("[socks5] new connection from %s to %s", conn.RemoteAddr().String(), conn.LocalAddr().String())

	if s.tunnelToExit {
		s.handleTunnelConn(conn)
		return
	}

	host, port, cmd, err := s.handleHandshake(conn)
	if err != nil {
		log.Printf("[socks5] handshake failed: %v", err)
		return
	}

	if cmd == 0x03 { // UDP ASSOCIATE
		if !s.allowUDPAssociate {
			_ = sendSocks5Reply(conn, 0x07)
			_ = conn.Close()
			return
		}
		s.handleUDPAssociate(conn)
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
		s.releaseCircuitAfterBeginError(circuitID, err)
		s.streamManager.Unregister(stream.ID) // keep conn open for retry

		excludeExitIDs := []string{}
		if plan, ok := s.circuitMgr.GetPlan(circuitID); ok {
			excludeExitIDs = append(excludeExitIDs, plan.Hops[plan.ExitHopIndex].PeerID)
		}

		maxRetries := s.circuitMgr.BeginTCPRetries()
		for attempt := 0; attempt < maxRetries; attempt++ {
			circuitID, err = s.circuitMgr.EnsureCircuitFromPoolExcluding(kind, excludeExitIDs)
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
				s.releaseCircuitAfterBeginError(circuitID, err)
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

func (s *Socks5Server) handleTunnelConn(conn net.Conn) {
	excludeExitIDs := []string{}
	maxRetries := s.circuitMgr.BeginTCPRetries()
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		plan, peerID, err := s.selectTunnelPath(excludeExitIDs)
		if err != nil {
			lastErr = err
			break
		}
		tunnelStream, err := s.openTunnelStream(peerID)
		if err != nil {
			lastErr = err
			excludeExitIDs = append(excludeExitIDs, peerID.String())
			if s.pathSelector != nil {
				s.pathSelector.ReportPathFailure(plan)
			}
			log.Printf("[socks5] open raw tunnel stream failed: %v", err)
			continue
		}
		tunnelID, err := newTunnelID()
		if err != nil {
			_ = tunnelStream.Close()
			lastErr = err
			excludeExitIDs = append(excludeExitIDs, plan.Hops[plan.ExitHopIndex].PeerID)
			continue
		}
		if err := s.writeTunnelRouteHeader(tunnelStream, tunnelID, plan); err != nil {
			_ = tunnelStream.Close()
			lastErr = err
			excludeExitIDs = append(excludeExitIDs, plan.Hops[plan.ExitHopIndex].PeerID)
			if s.pathSelector != nil {
				s.pathSelector.ReportPathFailure(plan)
			}
			continue
		}
		sess, err := tunnel.ClientHandshake(tunnelStream, tunnelID)
		if err != nil {
			_ = tunnelStream.Close()
			lastErr = err
			excludeExitIDs = append(excludeExitIDs, plan.Hops[plan.ExitHopIndex].PeerID)
			if s.pathSelector != nil {
				s.pathSelector.ReportPathFailure(plan)
			}
			log.Printf("[socks5] raw tunnel handshake failed: %v", err)
			continue
		}
		if s.pathSelector != nil {
			s.pathSelector.ReportPathSuccess(plan)
		}
		log.Printf("[socks5] raw tunnel established path=%s via %s", DebugString(plan), p2p.ProtocolRawTunnelE2E)
		s.pipeEncryptedTunnel(conn, tunnelStream, sess)
		return
	}

	if lastErr != nil && s.errorRecorder != nil {
		s.errorRecorder.Record("socks5_tunnel", "open raw tunnel failed: "+lastErr.Error())
	}
	if lastErr != nil {
		log.Printf("[socks5] open raw tunnel failed: %v", lastErr)
	}
	_ = conn.Close()
}

func (s *Socks5Server) selectTunnelPath(excludeExitIDs []string) (protocol.PathPlan, peer.ID, error) {
	plan, err := s.pathSelector.SelectRawTunnelPathExcluding(excludeExitIDs)
	if err != nil {
		return protocol.PathPlan{}, "", err
	}
	if len(plan.Hops) == 0 {
		return protocol.PathPlan{}, "", fmt.Errorf("empty raw tunnel path")
	}
	pid, err := peer.Decode(plan.Hops[0].PeerID)
	if err != nil {
		return protocol.PathPlan{}, "", err
	}
	return plan, pid, nil
}

func (s *Socks5Server) openTunnelStream(peerID peer.ID) (network.Stream, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.host.NewStream(ctx, peerID, p2p.ProtocolRawTunnelE2E)
}

func (s *Socks5Server) writeTunnelRouteHeader(stream network.Stream, tunnelID string, plan protocol.PathPlan) error {
	path := make([]string, 0, len(plan.Hops))
	for _, hop := range plan.Hops {
		path = append(path, hop.PeerID)
	}
	header := tunnel.RouteHeader{
		Version:    1,
		TunnelID:   tunnelID,
		Path:       path,
		HopIndex:   0,
		TargetExit: plan.Hops[plan.ExitHopIndex].PeerID,
	}
	return tunnel.WriteJSONFrame(stream, header)
}

func (s *Socks5Server) pipeEncryptedTunnel(conn net.Conn, stream network.Stream, sess *tunnel.Session) {
	var once sync.Once
	closeBoth := func() {
		_ = conn.Close()
		_ = stream.Close()
	}

	go func() {
		for {
			var frame tunnel.EncryptedFrame
			if err := tunnel.ReadJSONFrame(stream, &frame); err != nil {
				once.Do(closeBoth)
				return
			}
			switch frame.Type {
			case tunnel.FrameTypeData:
				plain, err := sess.Open(frame)
				if err != nil {
					once.Do(closeBoth)
					return
				}
				if len(plain) == 0 {
					continue
				}
				if _, err := conn.Write(plain); err != nil {
					once.Do(closeBoth)
					return
				}
			case tunnel.FrameTypeClose:
				once.Do(closeBoth)
				return
			}
		}
	}()

	buf := make([]byte, protocol.TCPDataChunkBytes)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			frame, frameErr := sess.Seal(tunnel.FrameTypeData, buf[:n])
			if frameErr != nil {
				once.Do(closeBoth)
				return
			}
			if frameErr = tunnel.WriteJSONFrame(stream, frame); frameErr != nil {
				once.Do(closeBoth)
				return
			}
		}
		if err != nil {
			if err == io.EOF {
				if frame, frameErr := sess.Seal(tunnel.FrameTypeClose, nil); frameErr == nil {
					_ = tunnel.WriteJSONFrame(stream, frame)
				}
			}
			once.Do(closeBoth)
			return
		}
	}
}

func newTunnelID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// handleHandshake performs a minimal SOCKS5 NO-AUTH handshake and parses the request.
// Returns (host, port, cmd, err). For UDP ASSOCIATE (cmd=0x03), host/port may be zero.
func (s *Socks5Server) handleHandshake(conn net.Conn) (string, uint16, byte, error) {
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", 0, 0, err
	}
	if buf[0] != 0x05 {
		return "", 0, 0, fmt.Errorf("unsupported version %d", buf[0])
	}
	nMethods := int(buf[1])
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", 0, 0, err
	}
	supportNoAuth := false
	for _, m := range methods {
		if m == 0x00 {
			supportNoAuth = true
			break
		}
	}
	if !supportNoAuth {
		_, _ = conn.Write([]byte{0x05, 0xFF})
		return "", 0, 0, fmt.Errorf("no acceptable auth method")
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return "", 0, 0, err
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", 0, 0, err
	}
	if header[0] != 0x05 {
		return "", 0, 0, fmt.Errorf("invalid request version %d", header[0])
	}
	cmd := header[1]
	if cmd != 0x01 && cmd != 0x03 {
		_ = sendSocks5Reply(conn, 0x07)
		return "", 0, 0, fmt.Errorf("unsupported cmd %d", cmd)
	}

	atyp := header[3]
	var host string
	switch atyp {
	case 0x01:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", 0, 0, err
		}
		host = net.IP(addr).String()
	case 0x03:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return "", 0, 0, err
		}
		dlen := int(buf[0])
		domain := make([]byte, dlen)
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", 0, 0, err
		}
		host = string(domain)
	case 0x04: // IPv6
		addr := make([]byte, 16)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", 0, 0, err
		}
		host = net.IP(addr).String()
	default:
		_ = sendSocks5Reply(conn, 0x08)
		return "", 0, 0, fmt.Errorf("unsupported atyp %d", atyp)
	}
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return "", 0, 0, err
	}
	port := binary.BigEndian.Uint16(buf[:2])
	return host, port, cmd, nil
}

// sendSocks5Reply sends a minimal SOCKS5 reply with given status.
// We currently return a dummy bind addr of 0.0.0.0:0.
func sendSocks5Reply(conn net.Conn, rep byte) error {
	resp := []byte{
		0x05,
		rep,
		0x00,
		0x01,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00,
	}
	_, err := conn.Write(resp)
	return err
}

// sendSocks5ReplyWithBind sends SOCKS5 reply with a specific bind address (for UDP ASSOCIATE).
func sendSocks5ReplyWithBind(conn net.Conn, rep byte, bindHost string, bindPort uint16) error {
	ip := net.ParseIP(bindHost)
	if ip == nil {
		ip = net.IPv4(0, 0, 0, 0)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		ip4 = net.IPv4(0, 0, 0, 0)
	}
	resp := []byte{
		0x05, rep, 0x00, 0x01,
		ip4[0], ip4[1], ip4[2], ip4[3],
		byte(bindPort >> 8), byte(bindPort),
	}
	_, err := conn.Write(resp)
	return err
}

// udpStreamEntry tracks one UDP stream in an ASSOCIATE session for reply routing.
type udpStreamEntry struct {
	circuitID  string
	streamID   string
	clientAddr net.Addr
	targetHost string
	targetPort int
	replyCh    <-chan []byte
}

// handleUDPAssociate handles SOCKS5 UDP ASSOCIATE: bind UDP, reply with bind addr, then relay UDP packets over circuits.
func (s *Socks5Server) handleUDPAssociate(tcpConn net.Conn) {
	defer tcpConn.Close()
	udpConn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		log.Printf("[socks5] udp associate listen: %v", err)
		_ = sendSocks5Reply(tcpConn, 0x01)
		return
	}
	defer udpConn.Close()
	udpAddr := udpConn.LocalAddr().(*net.UDPAddr)
	bindHost, _, _ := net.SplitHostPort(s.addr)
	if bindHost == "" {
		bindHost = "127.0.0.1"
	}
	if err := sendSocks5ReplyWithBind(tcpConn, 0x00, bindHost, uint16(udpAddr.Port)); err != nil {
		log.Printf("[socks5] udp associate reply: %v", err)
		return
	}
	log.Printf("[socks5] udp associate bound %s (client %s)", udpConn.LocalAddr(), tcpConn.RemoteAddr())

	streamsMu := sync.Mutex{}
	streamsByKey := make(map[string]*udpStreamEntry)
	var streamList []*udpStreamEntry

	// When TCP closes, tear down all UDP streams
	go func() {
		buf := make([]byte, 1)
		for {
			if _, err := tcpConn.Read(buf); err != nil {
				break
			}
		}
		streamsMu.Lock()
		for _, e := range streamList {
			s.streamManager.Remove(e.streamID)
			s.circuitMgr.ReturnToPool(e.circuitID)
		}
		streamList = nil
		streamsByKey = make(map[string]*udpStreamEntry)
		streamsMu.Unlock()
		_ = udpConn.Close()
	}()

	buf := make([]byte, 64*1024)
	for {
		n, clientAddr, err := udpConn.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < 10 {
			continue
		}
		dstHost, dstPort, payload, err := parseSocks5UDPRequest(buf[:n])
		if err != nil {
			continue
		}
		key := dstHost + ":" + strconv.Itoa(dstPort)
		streamsMu.Lock()
		entry := streamsByKey[key]
		if entry == nil {
			circuitID, err := s.circuitMgr.EnsureCircuitFromPool(PoolLowLatency)
			if err != nil {
				streamsMu.Unlock()
				if s.errorRecorder != nil {
					s.errorRecorder.Record("socks5_udp", "circuit failed: "+err.Error())
				}
				continue
			}
			stream, replyCh := s.streamManager.RegisterStreamUDP(circuitID)
			if err := s.circuitMgr.BeginUDP(circuitID, stream.ID, dstHost, dstPort); err != nil {
				s.streamManager.Remove(stream.ID)
				s.releaseCircuitAfterBeginError(circuitID, err)
				streamsMu.Unlock()
				continue
			}
			entry = &udpStreamEntry{
				circuitID:  circuitID,
				streamID:   stream.ID,
				clientAddr: clientAddr,
				targetHost: dstHost,
				targetPort: dstPort,
				replyCh:    replyCh,
			}
			streamsByKey[key] = entry
			streamList = append(streamList, entry)
			go func(e *udpStreamEntry) {
				for data := range e.replyCh {
					reply := buildSocks5UDPReply(e.targetHost, e.targetPort, data)
					_, _ = udpConn.WriteTo(reply, e.clientAddr)
				}
			}(entry)
			streamsMu.Unlock()
		} else {
			entry.clientAddr = clientAddr
			streamsMu.Unlock()
		}
		if err := s.circuitMgr.SendData(entry.circuitID, entry.streamID, payload); err != nil {
			if s.errorRecorder != nil {
				s.errorRecorder.Record("socks5_udp", "send_data: "+err.Error())
			}
		}
	}
}

// parseSocks5UDPRequest parses a SOCKS5 UDP request: RSV(2) FRAG(1) ATYP(1) DST.ADDR DST.PORT DATA.
// Returns (dstHost, dstPort, payload, error).
func parseSocks5UDPRequest(b []byte) (string, int, []byte, error) {
	if len(b) < 4 {
		return "", 0, nil, fmt.Errorf("packet too short")
	}
	atyp := b[3]
	var host string
	off := 4
	switch atyp {
	case 0x01:
		if len(b) < 10 {
			return "", 0, nil, fmt.Errorf("ipv4 too short")
		}
		host = net.IP(b[4:8]).String()
		off = 10
	case 0x03:
		if len(b) < 5 {
			return "", 0, nil, fmt.Errorf("domain length missing")
		}
		dlen := int(b[4])
		if len(b) < 7+dlen {
			return "", 0, nil, fmt.Errorf("domain too short")
		}
		host = string(b[5 : 5+dlen])
		off = 5 + dlen + 2
	case 0x04:
		if len(b) < 22 {
			return "", 0, nil, fmt.Errorf("ipv6 too short")
		}
		host = "[" + net.IP(b[4:20]).String() + "]"
		off = 22
	default:
		return "", 0, nil, fmt.Errorf("unsupported atyp %d", atyp)
	}
	if len(b) < off+2 {
		return "", 0, nil, fmt.Errorf("port missing")
	}
	port := int(binary.BigEndian.Uint16(b[off : off+2]))
	return host, port, b[off+2:], nil
}

// buildSocks5UDPReply builds a SOCKS5 UDP reply: RSV FRAG ATYP DST.ADDR DST.PORT DATA.
func buildSocks5UDPReply(dstHost string, dstPort int, payload []byte) []byte {
	ip := net.ParseIP(dstHost)
	var header []byte
	if ip != nil && ip.To4() != nil {
		header = make([]byte, 10)
		header[0], header[1], header[2], header[3] = 0, 0, 0, 0x01
		copy(header[4:8], ip.To4())
		binary.BigEndian.PutUint16(header[8:10], uint16(dstPort))
	} else {
		header = make([]byte, 7+len(dstHost))
		header[0], header[1], header[2], header[3] = 0, 0, 0, 0x03
		header[4] = byte(len(dstHost))
		copy(header[5:5+len(dstHost)], dstHost)
		binary.BigEndian.PutUint16(header[5+len(dstHost):7+len(dstHost)], uint16(dstPort))
	}
	return append(header, payload...)
}

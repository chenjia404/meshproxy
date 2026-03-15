package exit

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"

	"github.com/chenjia404/meshproxy/internal/p2p"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/safe"
	"github.com/chenjia404/meshproxy/internal/tunnel"
)

// Service implements the exit node: performs per-stream key exchange with the
// relay (which forwards the client's pub), then decrypts the last onion layer
// with UnwrapForwardFinal and handles OnionPayload (begin/data/end); responses
// are wrapped with the session's backward key.
// 若 Policy 非 nil，在處理 begin 時會按出口策略檢查（端口、域名、peer、私網等），拒絕時回傳 Connected{OK: false, Error: reason}。
const maxRecentRejects = 20

// RejectEntry 單條拒絕記錄，供 API status 使用。
type RejectEntry struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// udpSession 表示一個 UDP 會話：出口端用一個 UDPConn 與目標 host:port 收發報文。
type udpSession struct {
	conn       *net.UDPConn
	targetAddr *net.UDPAddr
	streamKey  string
	circuitID  string
	streamID   string
}

type Service struct {
	host           host.Host
	Policy         *PolicyChecker
	socks5Upstream string
	mu             sync.Mutex
	conns          map[string]net.Conn
	udpSessions    map[string]*udpSession
	sessions       map[string]*protocol.HopSession
	recentRejects  []RejectEntry
}

// NewService registers the circuit protocol handler on the given host and
// returns the created Service instance. policy 可為 nil，表示不做出口策略檢查。
func NewService(h host.Host, policy *PolicyChecker, socks5Upstream string) *Service {
	s := &Service{
		host:           h,
		Policy:         policy,
		socks5Upstream: socks5Upstream,
		conns:          make(map[string]net.Conn),
		udpSessions:    make(map[string]*udpSession),
		sessions:       make(map[string]*protocol.HopSession),
		recentRejects:  make([]RejectEntry, 0, maxRecentRejects),
	}
	h.SetStreamHandler(protocol.CircuitProtocolID, s.handleStream)
	h.SetStreamHandler(p2p.ProtocolSocks5, s.handleSocks5TunnelStream)
	return s
}

func (s *Service) isLocalSocks5Upstream(host string, port int) bool {
	if s.socks5Upstream == "" {
		return false
	}
	upstreamHost, upstreamPort, err := net.SplitHostPort(s.socks5Upstream)
	if err != nil {
		return false
	}
	portStr := strconv.Itoa(port)
	if upstreamPort != portStr {
		return false
	}
	return upstreamHost == host
}

func (s *Service) handleStream(str network.Stream) {
	safe.Go("exit.serveStream", func() { s.serveStream(str) })
}

func (s *Service) handleSocks5TunnelStream(str network.Stream) {
	safe.Go("exit.serveSocks5TunnelStream", func() { s.serveSocks5TunnelStream(str) })
}

// HandleRawTunnelE2EStream handles a multihop raw tunnel whose payload is
// end-to-end encrypted between client and exit. Exit decrypts and forwards the
// plaintext byte stream to its local dedicated SOCKS5 upstream.
func (s *Service) HandleRawTunnelE2EStream(str network.Stream) {
	safe.Go("exit.serveRawTunnelE2E", func() { s.serveRawTunnelE2E(str) })
}

// HandleRawTunnelE2EStreamWithHeader continues handling after the route header
// has already been consumed by an outer dispatcher.
func (s *Service) HandleRawTunnelE2EStreamWithHeader(str network.Stream, header tunnel.RouteHeader) {
	safe.Go("exit.serveRawTunnelE2EWithHeader", func() { s.serveRawTunnelE2EWithHeader(str, header) })
}

func (s *Service) serveSocks5TunnelStream(str network.Stream) {
	defer str.Close()
	if s.socks5Upstream == "" {
		return
	}
	upstream, err := net.Dial("tcp", s.socks5Upstream)
	if err != nil {
		return
	}
	defer upstream.Close()

	var once sync.Once
	closeBoth := func() {
		_ = upstream.Close()
		_ = str.Close()
	}

	safe.Go("exit.serveSocks5TunnelStream.copyUp", func() {
		_, _ = io.Copy(upstream, str)
		once.Do(closeBoth)
	})
	_, _ = io.Copy(str, upstream)
	once.Do(closeBoth)
}

func (s *Service) serveRawTunnelE2E(str network.Stream) {
	defer str.Close()
	if s.socks5Upstream == "" {
		return
	}

	var header tunnel.RouteHeader
	if err := tunnel.ReadJSONFrame(str, &header); err != nil {
		return
	}
	s.serveRawTunnelE2EWithHeader(str, header)
}

func (s *Service) serveRawTunnelE2EWithHeader(str network.Stream, header tunnel.RouteHeader) {
	if len(header.Path) == 0 || header.HopIndex < 0 || header.HopIndex >= len(header.Path) {
		return
	}
	selfID := s.host.ID().String()
	if header.Path[header.HopIndex] != selfID || header.Path[len(header.Path)-1] != selfID {
		return
	}
	if header.TargetExit != "" && header.TargetExit != selfID {
		return
	}

	sess, err := tunnel.ServerHandshake(str, header.TunnelID)
	if err != nil {
		return
	}

	upstream, err := net.Dial("tcp", s.socks5Upstream)
	if err != nil {
		return
	}
	defer upstream.Close()

	var writeMu sync.Mutex
	var once sync.Once
	closeBoth := func() {
		_ = upstream.Close()
		_ = str.Close()
	}

	safe.Go("exit.serveRawTunnelE2E.copyUpstream", func() {
		buf := make([]byte, protocol.TCPDataChunkBytes)
		for {
			n, err := upstream.Read(buf)
			if n > 0 {
				frame, frameErr := sess.Seal(tunnel.FrameTypeData, buf[:n])
				if frameErr != nil {
					once.Do(closeBoth)
					return
				}
				writeMu.Lock()
				frameErr = tunnel.WriteJSONFrame(str, frame)
				writeMu.Unlock()
				if frameErr != nil {
					once.Do(closeBoth)
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					frame, frameErr := sess.Seal(tunnel.FrameTypeClose, nil)
					if frameErr == nil {
						writeMu.Lock()
						_ = tunnel.WriteJSONFrame(str, frame)
						writeMu.Unlock()
					}
				}
				once.Do(closeBoth)
				return
			}
		}
	})

	for {
		var frame tunnel.EncryptedFrame
		if err := tunnel.ReadJSONFrame(str, &frame); err != nil {
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
			if _, err := upstream.Write(plain); err != nil {
				once.Do(closeBoth)
				return
			}
		case tunnel.FrameTypeClose:
			once.Do(closeBoth)
			return
		default:
			// ignore unsupported frame types for now
		}
	}
}

func (s *Service) serveStream(str network.Stream) {
	defer str.Close()
	streamKey := str.Conn().RemotePeer().String() + "/" + str.ID()
	var writeMu sync.Mutex

	frame, err := protocol.ReadFrame(str)
	if err != nil {
		return
	}
	if frame.Type != protocol.MsgTypeKeyExchangeInit {
		s.serveDirectStream(str, frame, &writeMu)
		return
	}
	s.serveEncryptedStream(str, streamKey, frame, &writeMu)
}

func (s *Service) serveEncryptedStream(str network.Stream, streamKey string, frame protocol.Frame, writeMu *sync.Mutex) {
	var initMsg protocol.KeyExchangeInit
	if json.Unmarshal(frame.PayloadJSON, &initMsg) != nil || len(initMsg.Payload) != protocol.X25519KeySize {
		return
	}
	clientPub := initMsg.Payload
	exitPriv, exitPub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		return
	}
	session, err := protocol.NewHopSessionFromKeyExchange(
		streamKey,
		protocol.HopRoleExit,
		exitPriv,
		clientPub,
		0,
	)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.sessions[streamKey] = session
	s.mu.Unlock()
	respFrame, _ := protocol.NewKeyExchangeRespFrame(frame.CircuitID, exitPub)
	writeMu.Lock()
	_ = protocol.WriteFrame(str, respFrame)
	writeMu.Unlock()

	for {
		frame, err := protocol.ReadFrame(str)
		if err != nil {
			return
		}
		switch frame.Type {
		case protocol.MsgTypeOnionData:
			var cell protocol.OnionCell
			if json.Unmarshal(frame.PayloadJSON, &cell) != nil {
				return
			}
			s.mu.Lock()
			sess := s.sessions[streamKey]
			s.mu.Unlock()
			if sess == nil {
				return
			}
			innerPlain, err := protocol.UnwrapForwardFinal(sess, frame.CircuitID, frame.StreamID, cell.Ciphertext)
			if err != nil {
				return
			}
			var payload protocol.OnionPayload
			if json.Unmarshal(innerPlain, &payload) != nil {
				return
			}
			switch payload.Kind {
			case "begin":
				if payload.Begin == nil {
					return
				}
				begin := payload.Begin
				addr := net.JoinHostPort(begin.TargetHost, strconv.Itoa(begin.TargetPort))
				// 出口策略檢查（順序見文檔）
				if rejectReason := s.checkBeginPolicy(str, begin); rejectReason != "" {
					s.recordReject(rejectReason)
					resp := protocol.Connected{OK: false, Error: string(rejectReason)}
					s.sendConnectedAndReturn(streamKey, frame.CircuitID, frame.StreamID, &resp, str, writeMu)
					return
				}
				remote, err := s.dialTCPTarget(begin.TargetHost, begin.TargetPort)
				resp := protocol.Connected{OK: err == nil}
				if err != nil {
					resp.Error = fmt.Sprintf("dial %s failed: %v", addr, err)
				}
				// 回包用 exit 的 backward key 封裝後寫回
				connectedPayload := protocol.OnionPayload{Kind: "connected", Connected: &resp}
				connectedJSON, _ := protocol.MarshalPaddedOnionPayload(connectedPayload)
				wrapped, err := s.wrapBackward(streamKey, frame.CircuitID, frame.StreamID, connectedJSON)
				if err != nil {
					return
				}
				outFrame := protocol.Frame{
					Type:        protocol.MsgTypeOnionData,
					CircuitID:   frame.CircuitID,
					StreamID:    frame.StreamID,
					PayloadJSON: wrapped,
				}
				writeMu.Lock()
				_ = protocol.WriteFrame(str, outFrame)
				writeMu.Unlock()
				// 若連線建立失敗，僅回傳 CONNECTED 錯誤，不啟動後續數據泵。
				if err != nil {
					return
				}
				s.storeConn(frame.StreamID, remote)
				safe.Go("exit.pumpRemoteToCircuit", func() { s.pumpRemoteToCircuit(frame.CircuitID, frame.StreamID, remote, str, writeMu, streamKey) })
			case "begin_udp":
				if payload.BeginUDP == nil {
					return
				}
				beginUDP := payload.BeginUDP
				addr := net.JoinHostPort(beginUDP.TargetHost, strconv.Itoa(beginUDP.TargetPort))
				if rejectReason := s.checkBeginUDPPolicy(str, beginUDP); rejectReason != "" {
					s.recordReject(rejectReason)
					resp := protocol.Connected{OK: false, Error: string(rejectReason)}
					s.sendConnectedAndReturn(streamKey, frame.CircuitID, frame.StreamID, &resp, str, writeMu)
					return
				}
				targetAddr, err := net.ResolveUDPAddr("udp", addr)
				if err != nil {
					resp := protocol.Connected{OK: false, Error: fmt.Sprintf("resolve udp %s: %v", addr, err)}
					s.sendConnectedAndReturn(streamKey, frame.CircuitID, frame.StreamID, &resp, str, writeMu)
					return
				}
				udpConn, err := net.ListenPacket("udp", ":0")
				if err != nil {
					resp := protocol.Connected{OK: false, Error: fmt.Sprintf("listen udp: %v", err)}
					s.sendConnectedAndReturn(streamKey, frame.CircuitID, frame.StreamID, &resp, str, writeMu)
					return
				}
				conn, ok := udpConn.(*net.UDPConn)
				if !ok {
					udpConn.Close()
					resp := protocol.Connected{OK: false, Error: "udp listen not *net.UDPConn"}
					s.sendConnectedAndReturn(streamKey, frame.CircuitID, frame.StreamID, &resp, str, writeMu)
					return
				}
				sess := &udpSession{
					conn:       conn,
					targetAddr: targetAddr,
					streamKey:  streamKey,
					circuitID:  frame.CircuitID,
					streamID:   frame.StreamID,
				}
				s.storeUDPSession(frame.StreamID, sess)
				resp := protocol.Connected{OK: true}
				connectedPayload := protocol.OnionPayload{Kind: "connected", Connected: &resp}
				connectedJSON, _ := protocol.MarshalPaddedOnionPayload(connectedPayload)
				wrapped, err := s.wrapBackward(streamKey, frame.CircuitID, frame.StreamID, connectedJSON)
				if err != nil {
					s.closeUDPSession(frame.StreamID)
					return
				}
				outFrame := protocol.Frame{
					Type:        protocol.MsgTypeOnionData,
					CircuitID:   frame.CircuitID,
					StreamID:    frame.StreamID,
					PayloadJSON: wrapped,
				}
				writeMu.Lock()
				_ = protocol.WriteFrame(str, outFrame)
				writeMu.Unlock()
				safe.Go("exit.pumpUDPToCircuit", func() { s.pumpUDPToCircuit(sess, str, writeMu) })
			case "data":
				if payload.Data == nil {
					continue
				}
				if conn := s.getConn(frame.StreamID); conn != nil {
					if _, err := conn.Write(payload.Data.Payload); err != nil {
						s.closeConn(frame.StreamID)
					}
				} else if udpSess := s.getUDPSession(frame.StreamID); udpSess != nil {
					if _, err := udpSess.conn.WriteTo(payload.Data.Payload, udpSess.targetAddr); err != nil {
						s.closeUDPSession(frame.StreamID)
					}
				}
			case protocol.OnionPayloadKindPing:
				if payload.Ping == nil {
					continue
				}
				log.Printf("[exit] heartbeat_ping_received circuit=%s ping=%s", frame.CircuitID, payload.Ping.PingID)
				pong := protocol.Pong{
					CircuitID:  frame.CircuitID,
					PingID:     payload.Ping.PingID,
					SentAtUnix: payload.Ping.SentAtUnix,
					EchoAtUnix: time.Now().UnixMilli(),
				}
				pongPayload := protocol.OnionPayload{
					Kind: protocol.OnionPayloadKindPong,
					Pong: &pong,
				}
				pongJSON, _ := protocol.MarshalPaddedOnionPayload(pongPayload)
				wrapped, err := s.wrapBackward(streamKey, frame.CircuitID, frame.StreamID, pongJSON)
				if err != nil {
					continue
				}
				pongFrame := protocol.Frame{
					Type:        protocol.MsgTypeOnionData,
					CircuitID:   frame.CircuitID,
					StreamID:    frame.StreamID,
					PayloadJSON: wrapped,
				}
				writeMu.Lock()
				_ = protocol.WriteFrame(str, pongFrame)
				writeMu.Unlock()
				log.Printf("[exit] heartbeat_pong_sent circuit=%s ping=%s", frame.CircuitID, payload.Ping.PingID)
			default:
				// ignore
			}
		case protocol.MsgTypeEnd:
			s.closeConn(frame.StreamID)
			s.closeUDPSession(frame.StreamID)
			return
		default:
			return
		}
	}
}

func (s *Service) serveDirectStream(str network.Stream, firstFrame protocol.Frame, writeMu *sync.Mutex) {
	frame := firstFrame
	for {
		if !s.handleDirectFrame(str, frame, writeMu) {
			return
		}
		nextFrame, err := protocol.ReadFrame(str)
		if err != nil {
			return
		}
		frame = nextFrame
	}
}

func (s *Service) handleDirectFrame(str network.Stream, frame protocol.Frame, writeMu *sync.Mutex) bool {
	switch frame.Type {
	case protocol.MsgTypeBeginTCP:
		var begin protocol.BeginTCP
		if err := json.Unmarshal(frame.PayloadJSON, &begin); err != nil {
			return false
		}
		addr := net.JoinHostPort(begin.TargetHost, strconv.Itoa(begin.TargetPort))
		if rejectReason := s.checkBeginPolicy(str, &begin); rejectReason != "" {
			s.recordReject(rejectReason)
			s.writeConnectedFrame(frame.CircuitID, frame.StreamID, protocol.Connected{OK: false, Error: string(rejectReason)}, str, writeMu)
			return true
		}
		remote, err := s.dialTCPTarget(begin.TargetHost, begin.TargetPort)
		resp := protocol.Connected{OK: err == nil}
		if err != nil {
			resp.Error = fmt.Sprintf("dial %s failed: %v", addr, err)
			s.writeConnectedFrame(frame.CircuitID, frame.StreamID, resp, str, writeMu)
			return true
		}
		s.storeConn(frame.StreamID, remote)
		s.writeConnectedFrame(frame.CircuitID, frame.StreamID, resp, str, writeMu)
		safe.Go("exit.pumpRemoteToCircuitPlain", func() { s.pumpRemoteToCircuitPlain(frame.CircuitID, frame.StreamID, remote, str, writeMu) })
		return true
	case protocol.MsgTypeBeginUDP:
		var beginUDP protocol.BeginUDP
		if err := json.Unmarshal(frame.PayloadJSON, &beginUDP); err != nil {
			return false
		}
		addr := net.JoinHostPort(beginUDP.TargetHost, strconv.Itoa(beginUDP.TargetPort))
		if rejectReason := s.checkBeginUDPPolicy(str, &beginUDP); rejectReason != "" {
			s.recordReject(rejectReason)
			s.writeConnectedFrame(frame.CircuitID, frame.StreamID, protocol.Connected{OK: false, Error: string(rejectReason)}, str, writeMu)
			return true
		}
		targetAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			s.writeConnectedFrame(frame.CircuitID, frame.StreamID, protocol.Connected{OK: false, Error: fmt.Sprintf("resolve udp %s: %v", addr, err)}, str, writeMu)
			return true
		}
		udpConn, err := net.ListenPacket("udp", ":0")
		if err != nil {
			s.writeConnectedFrame(frame.CircuitID, frame.StreamID, protocol.Connected{OK: false, Error: fmt.Sprintf("listen udp: %v", err)}, str, writeMu)
			return true
		}
		conn, ok := udpConn.(*net.UDPConn)
		if !ok {
			udpConn.Close()
			s.writeConnectedFrame(frame.CircuitID, frame.StreamID, protocol.Connected{OK: false, Error: "udp listen not *net.UDPConn"}, str, writeMu)
			return true
		}
		sess := &udpSession{
			conn:       conn,
			targetAddr: targetAddr,
			circuitID:  frame.CircuitID,
			streamID:   frame.StreamID,
		}
		s.storeUDPSession(frame.StreamID, sess)
		s.writeConnectedFrame(frame.CircuitID, frame.StreamID, protocol.Connected{OK: true}, str, writeMu)
		safe.Go("exit.pumpUDPToCircuitPlain", func() { s.pumpUDPToCircuitPlain(sess, str, writeMu) })
		return true
	case protocol.MsgTypeData:
		var cell protocol.DataCell
		if err := json.Unmarshal(frame.PayloadJSON, &cell); err != nil {
			return true
		}
		if conn := s.getConn(frame.StreamID); conn != nil {
			if _, err := conn.Write(cell.Payload); err != nil {
				s.closeConn(frame.StreamID)
			}
		} else if udpSess := s.getUDPSession(frame.StreamID); udpSess != nil {
			if _, err := udpSess.conn.WriteTo(cell.Payload, udpSess.targetAddr); err != nil {
				s.closeUDPSession(frame.StreamID)
			}
		}
		return true
	case protocol.MsgTypeEnd:
		s.closeConn(frame.StreamID)
		s.closeUDPSession(frame.StreamID)
		return true
	default:
		return false
	}
}

func (s *Service) wrapBackward(streamKey, circuitID, streamID string, plaintext []byte) ([]byte, error) {
	s.mu.Lock()
	sess := s.sessions[streamKey]
	s.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("no session")
	}
	nonce := protocol.BuildAEADNonce("bwd", sess.BackwardCounter)
	sess.BackwardCounter++
	aad := protocol.BuildAAD(circuitID, streamID, "bwd", 0)
	sealed, err := protocol.AEADSeal(sess.BackwardKey, nonce, plaintext, aad)
	if err != nil {
		return nil, err
	}
	return json.Marshal(protocol.OnionCell{Ciphertext: sealed})
}

func (s *Service) dialTCPTarget(targetHost string, targetPort int) (net.Conn, error) {
	targetAddr := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	if s.socks5Upstream == "" || s.isLocalSocks5Upstream(targetHost, targetPort) {
		return net.Dial("tcp", targetAddr)
	}
	return s.dialViaSocks5Upstream(targetHost, targetPort)
}

func (s *Service) dialViaSocks5Upstream(targetHost string, targetPort int) (net.Conn, error) {
	upstream, err := net.Dial("tcp", s.socks5Upstream)
	if err != nil {
		return nil, err
	}
	if err := upstream.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		_ = upstream.Close()
		return nil, err
	}
	if _, err := upstream.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		_ = upstream.Close()
		return nil, err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(upstream, reply); err != nil {
		_ = upstream.Close()
		return nil, err
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		_ = upstream.Close()
		return nil, fmt.Errorf("socks5 auth rejected: ver=%d method=%d", reply[0], reply[1])
	}

	req, err := buildSocks5ConnectRequest(targetHost, targetPort)
	if err != nil {
		_ = upstream.Close()
		return nil, err
	}
	if _, err := upstream.Write(req); err != nil {
		_ = upstream.Close()
		return nil, err
	}
	if err := readSocks5ConnectReply(upstream); err != nil {
		_ = upstream.Close()
		return nil, err
	}
	if err := upstream.SetDeadline(time.Time{}); err != nil {
		_ = upstream.Close()
		return nil, err
	}
	return upstream, nil
}

func buildSocks5ConnectRequest(targetHost string, targetPort int) ([]byte, error) {
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(targetHost); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(targetHost) > 255 {
			return nil, fmt.Errorf("target host too long")
		}
		req = append(req, 0x03, byte(len(targetHost)))
		req = append(req, targetHost...)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(targetPort))
	req = append(req, portBytes...)
	return req, nil
}

func readSocks5ConnectReply(conn net.Conn) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("invalid socks5 reply version %d", header[0])
	}
	if header[1] != 0x00 {
		return fmt.Errorf("socks5 connect rejected: %s", socks5ReplyMessage(header[1]))
	}
	var addrLen int
	switch header[3] {
	case 0x01:
		addrLen = 4
	case 0x03:
		size := make([]byte, 1)
		if _, err := io.ReadFull(conn, size); err != nil {
			return err
		}
		addrLen = int(size[0])
	case 0x04:
		addrLen = 16
	default:
		return fmt.Errorf("unsupported socks5 reply atyp %d", header[3])
	}
	rest := make([]byte, addrLen+2)
	_, err := io.ReadFull(conn, rest)
	return err
}

func socks5ReplyMessage(rep byte) string {
	switch rep {
	case 0x01:
		return "general failure"
	case 0x02:
		return "connection not allowed"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "ttl expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return fmt.Sprintf("reply code %d", rep)
	}
}

func (s *Service) pumpRemoteToCircuit(circuitID, streamID string, remote net.Conn, str network.Stream, writeMu *sync.Mutex, streamKey string) {
	if remote == nil {
		return
	}
	defer s.closeConn(streamID)
	defer remote.Close()
	buf := make([]byte, protocol.TCPDataChunkBytes)
	for {
		n, err := remote.Read(buf)
		if n > 0 {
			payload := protocol.OnionPayload{Kind: "data", Data: &protocol.DataCell{Payload: buf[:n]}}
			payloadJSON, _ := protocol.MarshalPaddedOnionPayload(payload)
			wrapped, err := s.wrapBackward(streamKey, circuitID, streamID, payloadJSON)
			if err != nil {
				return
			}
			frame := protocol.Frame{
				Type:        protocol.MsgTypeOnionData,
				CircuitID:   circuitID,
				StreamID:    streamID,
				PayloadJSON: wrapped,
			}
			writeMu.Lock()
			err = protocol.WriteFrame(str, frame)
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Service) pumpRemoteToCircuitPlain(circuitID, streamID string, remote net.Conn, str network.Stream, writeMu *sync.Mutex) {
	if remote == nil {
		return
	}
	defer s.closeConn(streamID)
	defer remote.Close()
	buf := make([]byte, protocol.TCPDataChunkBytes)
	for {
		n, err := remote.Read(buf)
		if n > 0 {
			frame, frameErr := protocol.NewDataFrame(circuitID, streamID, buf[:n])
			if frameErr != nil {
				return
			}
			writeMu.Lock()
			frameErr = protocol.WriteFrame(str, frame)
			writeMu.Unlock()
			if frameErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Service) storeConn(streamID string, conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns[streamID] = conn
}

func (s *Service) getConn(streamID string) net.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[streamID]
}

func (s *Service) closeConn(streamID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.conns[streamID]; ok {
		if c != nil {
			_ = c.Close()
		}
		delete(s.conns, streamID)
	}
}

func (s *Service) storeUDPSession(streamID string, sess *udpSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.udpSessions[streamID] = sess
}

func (s *Service) getUDPSession(streamID string) *udpSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.udpSessions[streamID]
}

func (s *Service) closeUDPSession(streamID string) {
	s.mu.Lock()
	sess, ok := s.udpSessions[streamID]
	delete(s.udpSessions, streamID)
	s.mu.Unlock()
	if ok && sess != nil && sess.conn != nil {
		_ = sess.conn.Close()
	}
}

// pumpUDPToCircuit 從 UDP 連接讀取回包並寫回 circuit。
func (s *Service) pumpUDPToCircuit(sess *udpSession, str network.Stream, writeMu *sync.Mutex) {
	defer s.closeUDPSession(sess.streamID)
	buf := make([]byte, 16*1024)
	for {
		n, _, err := sess.conn.ReadFromUDP(buf)
		if n > 0 {
			payload := protocol.OnionPayload{Kind: "data", Data: &protocol.DataCell{Payload: buf[:n]}}
			payloadJSON, _ := protocol.MarshalPaddedOnionPayload(payload)
			wrapped, err := s.wrapBackward(sess.streamKey, sess.circuitID, sess.streamID, payloadJSON)
			if err != nil {
				return
			}
			frame := protocol.Frame{
				Type:        protocol.MsgTypeOnionData,
				CircuitID:   sess.circuitID,
				StreamID:    sess.streamID,
				PayloadJSON: wrapped,
			}
			writeMu.Lock()
			err = protocol.WriteFrame(str, frame)
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Service) pumpUDPToCircuitPlain(sess *udpSession, str network.Stream, writeMu *sync.Mutex) {
	defer s.closeUDPSession(sess.streamID)
	buf := make([]byte, 16*1024)
	for {
		n, _, err := sess.conn.ReadFromUDP(buf)
		if n > 0 {
			frame, frameErr := protocol.NewDataFrame(sess.circuitID, sess.streamID, buf[:n])
			if frameErr != nil {
				return
			}
			writeMu.Lock()
			frameErr = protocol.WriteFrame(str, frame)
			writeMu.Unlock()
			if frameErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// checkBeginUDPPolicy 對 begin_udp 做出口策略檢查（UDP 協議）。
func (s *Service) checkBeginUDPPolicy(str network.Stream, begin *protocol.BeginUDP) ExitRejectReason {
	if s.Policy == nil {
		return ""
	}
	p := s.Policy
	if !p.IsEnabled() {
		return ExitRejectDisabled
	}
	if !p.AcceptNewStreams() || p.DrainMode() {
		return ExitRejectDraining
	}
	peerID := str.Conn().RemotePeer().String()
	if reason, ok := p.CheckPeerAllowed(peerID); !ok {
		return reason
	}
	if reason, ok := p.CheckProtocolAllowed(false); !ok {
		return reason
	}
	if reason, ok := p.CheckPortAllowed(begin.TargetPort); !ok {
		return reason
	}
	host := begin.TargetHost
	ip := net.ParseIP(host)
	if ip != nil {
		if reason, ok := p.CheckTargetIPAllowed(ip); !ok {
			return reason
		}
		return ""
	}
	if reason, ok := p.CheckDomainAllowed(host); !ok {
		return reason
	}
	if !p.GetPolicy().RemoteDNS {
		return ""
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return ExitRejectDomainDenied
	}
	for _, a := range addrs {
		if reason, ok := p.CheckTargetIPAllowed(a); !ok {
			return reason
		}
	}
	return ""
}

// checkBeginPolicy 按文檔順序執行出口策略檢查，拒絕時返回非空原因。
func (s *Service) checkBeginPolicy(str network.Stream, begin *protocol.BeginTCP) ExitRejectReason {
	if s.isLocalSocks5Upstream(begin.TargetHost, begin.TargetPort) {
		return ""
	}
	if s.Policy == nil {
		return ""
	}
	p := s.Policy
	if !p.IsEnabled() {
		return ExitRejectDisabled
	}
	if !p.AcceptNewStreams() || p.DrainMode() {
		return ExitRejectDraining
	}
	peerID := str.Conn().RemotePeer().String()
	if reason, ok := p.CheckPeerAllowed(peerID); !ok {
		return reason
	}
	if reason, ok := p.CheckProtocolAllowed(true); !ok {
		return reason
	}
	if reason, ok := p.CheckPortAllowed(begin.TargetPort); !ok {
		return reason
	}
	host := begin.TargetHost
	ip := net.ParseIP(host)
	if ip != nil {
		if reason, ok := p.CheckTargetIPAllowed(ip); !ok {
			return reason
		}
		return ""
	}
	// 域名
	if reason, ok := p.CheckDomainAllowed(host); !ok {
		return reason
	}
	if !p.GetPolicy().RemoteDNS {
		return ""
	}
	// remote_dns: 解析後對 IP 再做一次私網/回環/鏈路本地檢查
	addrs, err := net.LookupIP(host)
	if err != nil {
		return ExitRejectDomainDenied
	}
	for _, a := range addrs {
		if reason, ok := p.CheckTargetIPAllowed(a); !ok {
			return reason
		}
	}
	return ""
}

// sendConnectedAndReturn 寫回 Connected 響應後由調用方 return，不啟動數據泵。
func (s *Service) sendConnectedAndReturn(streamKey, circuitID, streamID string, resp *protocol.Connected, str network.Stream, writeMu *sync.Mutex) {
	connectedPayload := protocol.OnionPayload{Kind: "connected", Connected: resp}
	connectedJSON, _ := protocol.MarshalPaddedOnionPayload(connectedPayload)
	wrapped, err := s.wrapBackward(streamKey, circuitID, streamID, connectedJSON)
	if err != nil {
		return
	}
	outFrame := protocol.Frame{
		Type:        protocol.MsgTypeOnionData,
		CircuitID:   circuitID,
		StreamID:    streamID,
		PayloadJSON: wrapped,
	}
	writeMu.Lock()
	_ = protocol.WriteFrame(str, outFrame)
	writeMu.Unlock()
}

func (s *Service) writeConnectedFrame(circuitID, streamID string, resp protocol.Connected, str network.Stream, writeMu *sync.Mutex) {
	frame, err := protocol.NewConnectedFrame(circuitID, streamID, resp)
	if err != nil {
		return
	}
	writeMu.Lock()
	_ = protocol.WriteFrame(str, frame)
	writeMu.Unlock()
}

func (s *Service) recordReject(reason ExitRejectReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := RejectEntry{Reason: string(reason), At: time.Now()}
	s.recentRejects = append(s.recentRejects, e)
	if len(s.recentRejects) > maxRecentRejects {
		s.recentRejects = s.recentRejects[len(s.recentRejects)-maxRecentRejects:]
	}
}

// OpenConnCount 返回當前出口 TCP 連接數（供 API 狀態使用）。
func (s *Service) OpenConnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// GetRecentRejects 返回最近若干次策略拒絕記錄。
func (s *Service) GetRecentRejects() []RejectEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RejectEntry, len(s.recentRejects))
	copy(out, s.recentRejects)
	return out
}

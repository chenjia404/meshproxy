package exit

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"

	"github.com/chenjia404/meshproxy/internal/protocol"
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
	host          host.Host
	Policy        *PolicyChecker
	mu            sync.Mutex
	conns         map[string]net.Conn
	udpSessions   map[string]*udpSession
	sessions      map[string]*protocol.HopSession
	recentRejects []RejectEntry
}

// NewService registers the circuit protocol handler on the given host and
// returns the created Service instance. policy 可為 nil，表示不做出口策略檢查。
func NewService(h host.Host, policy *PolicyChecker) *Service {
	s := &Service{
		host:          h,
		Policy:        policy,
		conns:         make(map[string]net.Conn),
		udpSessions:   make(map[string]*udpSession),
		sessions:      make(map[string]*protocol.HopSession),
		recentRejects: make([]RejectEntry, 0, maxRecentRejects),
	}
	h.SetStreamHandler(protocol.CircuitProtocolID, s.handleStream)
	return s
}

func (s *Service) handleStream(str network.Stream) {
	go s.serveStream(str)
}

func (s *Service) serveStream(str network.Stream) {
	defer str.Close()
	streamKey := str.Conn().RemotePeer().String() + "/" + str.ID()
	var writeMu sync.Mutex

	// 第一筆：密鑰協商（relay 轉發的客戶端公鑰）
	frame, err := protocol.ReadFrame(str)
	if err != nil {
		return
	}
	if frame.Type != protocol.MsgTypeKeyExchangeInit {
		return
	}
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
					s.sendConnectedAndReturn(streamKey, frame.CircuitID, frame.StreamID, &resp, str, &writeMu)
					return
				}
				remote, err := net.Dial("tcp", addr)
				resp := protocol.Connected{OK: err == nil}
				if err != nil {
					resp.Error = fmt.Sprintf("dial %s failed: %v", addr, err)
				}
				// 回包用 exit 的 backward key 封裝後寫回
				connectedPayload := protocol.OnionPayload{Kind: "connected", Connected: &resp}
				connectedJSON, _ := json.Marshal(connectedPayload)
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
				go s.pumpRemoteToCircuit(frame.CircuitID, frame.StreamID, remote, str, &writeMu, streamKey)
			case "begin_udp":
				if payload.BeginUDP == nil {
					return
				}
				beginUDP := payload.BeginUDP
				addr := net.JoinHostPort(beginUDP.TargetHost, strconv.Itoa(beginUDP.TargetPort))
				if rejectReason := s.checkBeginUDPPolicy(str, beginUDP); rejectReason != "" {
					s.recordReject(rejectReason)
					resp := protocol.Connected{OK: false, Error: string(rejectReason)}
					s.sendConnectedAndReturn(streamKey, frame.CircuitID, frame.StreamID, &resp, str, &writeMu)
					return
				}
				targetAddr, err := net.ResolveUDPAddr("udp", addr)
				if err != nil {
					resp := protocol.Connected{OK: false, Error: fmt.Sprintf("resolve udp %s: %v", addr, err)}
					s.sendConnectedAndReturn(streamKey, frame.CircuitID, frame.StreamID, &resp, str, &writeMu)
					return
				}
				udpConn, err := net.ListenPacket("udp", ":0")
				if err != nil {
					resp := protocol.Connected{OK: false, Error: fmt.Sprintf("listen udp: %v", err)}
					s.sendConnectedAndReturn(streamKey, frame.CircuitID, frame.StreamID, &resp, str, &writeMu)
					return
				}
				conn, ok := udpConn.(*net.UDPConn)
				if !ok {
					udpConn.Close()
					resp := protocol.Connected{OK: false, Error: "udp listen not *net.UDPConn"}
					s.sendConnectedAndReturn(streamKey, frame.CircuitID, frame.StreamID, &resp, str, &writeMu)
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
				connectedJSON, _ := json.Marshal(connectedPayload)
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
				go s.pumpUDPToCircuit(sess, str, &writeMu)
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
				pongJSON, _ := json.Marshal(pongPayload)
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

func (s *Service) pumpRemoteToCircuit(circuitID, streamID string, remote net.Conn, str network.Stream, writeMu *sync.Mutex, streamKey string) {
	if remote == nil {
		return
	}
	defer s.closeConn(streamID)
	defer remote.Close()
	buf := make([]byte, 16*1024)
	for {
		n, err := remote.Read(buf)
		if n > 0 {
			payload := protocol.OnionPayload{Kind: "data", Data: &protocol.DataCell{Payload: buf[:n]}}
			payloadJSON, _ := json.Marshal(payload)
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
			payloadJSON, _ := json.Marshal(payload)
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
	connectedJSON, _ := json.Marshal(connectedPayload)
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

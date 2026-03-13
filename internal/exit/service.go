package exit

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"

	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"

	"meshproxy/internal/protocol"
)

// Service implements the exit node: performs per-stream key exchange with the
// relay (which forwards the client's pub), then decrypts the last onion layer
// with UnwrapForwardFinal and handles OnionPayload (begin/data/end); responses
// are wrapped with the session's backward key.
type Service struct {
	host     host.Host
	mu       sync.Mutex
	conns    map[string]net.Conn    // key: streamID
	sessions map[string]*protocol.HopSession // key: stream key (remotePeer+streamID)
}

// NewService registers the circuit protocol handler on the given host and
// returns the created Service instance.
func NewService(h host.Host) *Service {
	s := &Service{
		host:     h,
		conns:    make(map[string]net.Conn),
		sessions: make(map[string]*protocol.HopSession),
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
				addr := net.JoinHostPort(payload.Begin.TargetHost, strconv.Itoa(payload.Begin.TargetPort))
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
				if err != nil {
					return
				}
				s.storeConn(frame.StreamID, remote)
				go s.pumpRemoteToCircuit(frame.CircuitID, frame.StreamID, remote, str, &writeMu, streamKey)
			case "data":
				if payload.Data == nil {
					continue
				}
				if conn := s.getConn(frame.StreamID); conn != nil {
					if _, err := conn.Write(payload.Data.Payload); err != nil {
						s.closeConn(frame.StreamID)
					}
				}
			default:
				// ignore
			}
		case protocol.MsgTypeEnd:
			s.closeConn(frame.StreamID)
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
		_ = c.Close()
		delete(s.conns, streamID)
	}
}


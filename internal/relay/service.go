// Package relay 實現 relay 節點服務：接受客戶端連接，僅解一層洋蔥後轉發內層密文給下一跳（exit），
// 不查看 BEGIN_TCP 等業務明文。
package relay

import (
	"context"
	"encoding/json"
	"io"
	"log"
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

// Service 實現 relay 節點：與客戶端做每跳密鑰協商，收到 OnionData 時只解一層，將內層密文轉發給 next_hop。
type Service struct {
	host host.Host
	mu   sync.Mutex
	// streamID -> 與該客戶端 stream 對應的 HopSession（僅與發起方建立）
	sessionsByStream map[string]*protocol.HopSession
	// circuitID -> 到下一跳（exit）的 stream，用於轉發
	nextHopStreams map[string]network.Stream
	// exit 側 stream 的寫鎖（按 stream 分）
	writeMu map[string]*sync.Mutex
}

// NewService 在給定 host 上註冊 circuit 協議處理，返回 Service。
func NewService(h host.Host) *Service {
	s := &Service{
		host:             h,
		sessionsByStream: make(map[string]*protocol.HopSession),
		nextHopStreams:   make(map[string]network.Stream),
		writeMu:          make(map[string]*sync.Mutex),
	}
	h.SetStreamHandler(protocol.CircuitProtocolID, s.handleStream)
	return s
}

func (s *Service) handleStream(str network.Stream) {
	safe.Go("relay.serveStream", func() { s.serveStream(str) })
}

// HandleRawTunnelStream forwards a raw tunnel stream to the next hop and then
// only performs byte shuttling. Relay nodes do not inspect encrypted payloads.
func (s *Service) HandleRawTunnelStream(str network.Stream) {
	safe.Go("relay.serveRawTunnel", func() { s.serveRawTunnel(str) })
}

// HandleRawTunnelStreamWithHeader continues handling after the route header has
// already been consumed by an outer dispatcher.
func (s *Service) HandleRawTunnelStreamWithHeader(str network.Stream, header tunnel.RouteHeader) {
	safe.Go("relay.serveRawTunnelWithHeader", func() { s.serveRawTunnelWithHeader(str, header) })
}

// serveStream 與客戶端完成密鑰協商後，循環處理 OnionData：解一層，轉發內層給 next_hop。
func (s *Service) serveStream(str network.Stream) {
	defer str.Close()
	streamID := str.Conn().RemotePeer().String() + str.ID()

	// 1) 密鑰協商：讀客戶端公鑰，回自己的公鑰，建立 HopSession
	initFrame, err := protocol.ReadFrame(str)
	if err != nil || initFrame.Type != protocol.MsgTypeKeyExchangeInit {
		return
	}
	var initMsg protocol.KeyExchangeInit
	if err := json.Unmarshal(initFrame.PayloadJSON, &initMsg); err != nil || len(initMsg.Payload) != protocol.X25519KeySize {
		return
	}
	clientPub := initMsg.Payload
	relayPriv, relayPub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		return
	}
	session, err := protocol.NewHopSessionFromKeyExchange(
		initFrame.CircuitID+"-client",
		protocol.HopRoleRelay,
		relayPriv,
		clientPub,
		time.Now().Unix(),
	)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.sessionsByStream[streamID] = session
	s.mu.Unlock()
	log.Printf("[relay] hop_session_established stream=%s", streamID[:8])

	respFrame, err := protocol.NewKeyExchangeRespFrame(initFrame.CircuitID, relayPub)
	if err != nil {
		return
	}
	if err := protocol.WriteFrame(str, respFrame); err != nil {
		return
	}

	var writeMu sync.Mutex
	for {
		frame, err := protocol.ReadFrame(str)
		if err != nil {
			if err != io.EOF {
				log.Printf("[relay] forward_layer_unwrapped read_err stream=%s err=%v", streamID[:8], err)
			}
			return
		}
		switch frame.Type {
		case protocol.MsgTypeOnionData:
			var cell protocol.OnionCell
			if err := json.Unmarshal(frame.PayloadJSON, &cell); err != nil {
				continue
			}
			// 只解一層：得到 RelayLayerHeader + 內層密文
			header, innerCipher, err := protocol.UnwrapForward(session, frame.CircuitID, frame.StreamID, cell.Ciphertext)
			if err != nil {
				log.Printf("[relay] onion_decrypt_failed stream=%s err=%v", streamID[:8], err)
				continue
			}
			log.Printf("[relay] forward_layer_unwrapped next_hop=%s", header.NextPeerID[:8])
			// 轉發內層密文給 next_hop
			nextPeerID, err := peer.Decode(header.NextPeerID)
			if err != nil {
				continue
			}
			s.mu.Lock()
			nextStr, ok := s.nextHopStreams[frame.CircuitID]
			if !ok {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				nextStr, err = s.host.NewStream(ctx, nextPeerID, protocol.CircuitProtocolID)
				cancel()
				if err != nil {
					s.mu.Unlock()
					continue
				}
				s.nextHopStreams[frame.CircuitID] = nextStr
				s.writeMu[frame.CircuitID] = &writeMu
				safe.Go("relay.pumpNextHopToClient", func() { s.pumpNextHopToClient(frame.CircuitID, nextStr, str, session, &writeMu) })
			}
			nextStr = s.nextHopStreams[frame.CircuitID]
			s.mu.Unlock()
			writeMu.Lock()
			if header.InnerPayloadType == protocol.InnerPayloadTypeKeyExchange && len(innerCipher) == protocol.X25519KeySize {
				// extend：轉發客戶端公鑰給 exit，觸發 exit 的密鑰協商
				initFrame, _ := protocol.NewKeyExchangeInitFrame(frame.CircuitID, innerCipher)
				err = protocol.WriteFrame(nextStr, initFrame)
			} else {
				// 一般洋蔥數據：轉發內層密文給 exit
				innerCell := protocol.OnionCell{Ciphertext: innerCipher}
				innerPayload, _ := json.Marshal(innerCell)
				fwdFrame := protocol.Frame{
					Type:        protocol.MsgTypeOnionData,
					CircuitID:   frame.CircuitID,
					StreamID:    frame.StreamID,
					PayloadJSON: innerPayload,
				}
				err = protocol.WriteFrame(nextStr, fwdFrame)
			}
			writeMu.Unlock()
			if err != nil {
				log.Printf("[relay] relay_forward write_err=%v", err)
			}
		default:
			// 其他類型忽略或關閉
		}
	}
}

func (s *Service) serveRawTunnel(str network.Stream) {
	defer str.Close()

	var header tunnel.RouteHeader
	if err := tunnel.ReadJSONFrame(str, &header); err != nil {
		return
	}
	s.serveRawTunnelWithHeader(str, header)
}

func (s *Service) serveRawTunnelWithHeader(str network.Stream, header tunnel.RouteHeader) {
	if len(header.Path) == 0 || header.HopIndex < 0 || header.HopIndex >= len(header.Path) {
		return
	}
	if header.Path[header.HopIndex] != s.host.ID().String() {
		return
	}
	nextIndex := header.HopIndex + 1
	if nextIndex >= len(header.Path) {
		return
	}
	nextPeerID, err := peer.Decode(header.Path[nextIndex])
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	nextStr, err := s.host.NewStream(ctx, nextPeerID, p2p.ProtocolRawTunnelE2E)
	if err != nil {
		return
	}
	defer nextStr.Close()

	header.HopIndex = nextIndex
	if err := tunnel.WriteJSONFrame(nextStr, header); err != nil {
		return
	}

	var once sync.Once
	closeBoth := func() {
		_ = str.Close()
		_ = nextStr.Close()
	}
	safe.Go("relay.serveRawTunnel.copyUp", func() {
		_, _ = io.Copy(nextStr, str)
		once.Do(closeBoth)
	})
	_, _ = io.Copy(str, nextStr)
	once.Do(closeBoth)
}

func (s *Service) pumpNextHopToClient(circuitID string, nextStr network.Stream, clientStr network.Stream, session *protocol.HopSession, writeMu *sync.Mutex) {
	defer func() {
		s.mu.Lock()
		delete(s.nextHopStreams, circuitID)
		s.mu.Unlock()
		nextStr.Close()
	}()
	for {
		frame, err := protocol.ReadFrame(nextStr)
		if err != nil {
			return
		}
		// backward：用 relay 的 backward key 將從 exit 收到的 payload 再包一層，發給客戶端
		var plaintext []byte
		switch frame.Type {
		case protocol.MsgTypeKeyExchangeResp:
			var resp protocol.KeyExchangeResp
			if json.Unmarshal(frame.PayloadJSON, &resp) == nil && len(resp.Payload) == protocol.X25519KeySize {
				plaintext = resp.Payload
			}
		case protocol.MsgTypeOnionData, protocol.MsgTypeConnected:
			plaintext = frame.PayloadJSON
		default:
			continue
		}
		if len(plaintext) == 0 {
			continue
		}
		nonce := protocol.BuildAEADNonce("bwd", session.BackwardCounter)
		session.BackwardCounter++
		aad := protocol.BuildAAD(frame.CircuitID, frame.StreamID, "bwd", 0)
		wrapped, err := protocol.AEADSeal(session.BackwardKey, nonce, plaintext, aad)
		if err != nil {
			return
		}
		backCell := protocol.OnionCell{Ciphertext: wrapped}
		backPayload, _ := json.Marshal(backCell)
		outFrame := protocol.Frame{
			Type:        protocol.MsgTypeOnionData,
			CircuitID:   frame.CircuitID,
			StreamID:    frame.StreamID,
			PayloadJSON: backPayload,
		}
		writeMu.Lock()
		_ = protocol.WriteFrame(clientStr, outFrame)
		writeMu.Unlock()
	}
}

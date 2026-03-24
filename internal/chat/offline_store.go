package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/offlinestore"
	"github.com/chenjia404/meshproxy/internal/protocol"
)

const (
	offlineFetchInterval     = 90 * time.Second
	offlineFetchLimit        = 100
	offlineSubmitConcurrency = 4
)

func (s *Service) mergeOfflineStoreNodes(forRecipientPeerID string) []offlinestore.OfflineStoreNode {
	seen := make(map[string]struct{})
	var out []offlinestore.OfflineStoreNode
	for _, n := range s.offlineStoreNodes {
		pid := strings.TrimSpace(n.PeerID)
		if pid == "" {
			continue
		}
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		out = append(out, n)
	}
	_ = forRecipientPeerID // 預留：日後合併對端 profile 中的 store 列表
	return out
}

func (s *Service) tryOfflineStoreSubmit(msg Message, storedBlob, cachedCiphertext []byte) error {
	if s == nil || s.nodePriv == nil || s.host == nil {
		return errors.New("offline store: not configured")
	}
	switch msg.MsgType {
	case MessageTypeChatText, MessageTypeGroupInviteNote:
	default:
		return fmt.Errorf("offline store: unsupported msg_type %q", msg.MsgType)
	}
	nodes := s.mergeOfflineStoreNodes(msg.ReceiverPeerID)
	if len(nodes) == 0 {
		return errors.New("offline store: no store nodes")
	}
	env, err := s.buildOfflineEnvelope(msg, storedBlob, cachedCiphertext)
	if err != nil {
		return err
	}
	if err := signOfflineEnvelope(s.nodePriv, env, offlineDefaultTTLSec); err != nil {
		return err
	}
	return s.submitSignedOfflineEnvelope(env)
}

// submitSignedOfflineEnvelope 將已簽名的 OfflineMessageEnvelope 並發提交到配置的 store 節點。
func (s *Service) submitSignedOfflineEnvelope(env *OfflineMessageEnvelope) error {
	if s == nil || s.host == nil {
		return errors.New("offline store: nil service or host")
	}
	nodes := s.mergeOfflineStoreNodes(env.RecipientID)
	if len(nodes) == 0 {
		return errors.New("offline store: no store nodes")
	}
	rawMsg, err := json.Marshal(env)
	if err != nil {
		return err
	}
	req := &offlinestore.StoreMessageRequest{Version: 1, Message: rawMsg}
	client := offlinestore.NewLibp2pStoreClient(s.host)

	var okCount int
	var mu sync.Mutex
	var lastErr error
	var wg sync.WaitGroup
	sem := make(chan struct{}, offlineSubmitConcurrency)
	for _, node := range nodes {
		node := node
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pid, decErr := peer.Decode(strings.TrimSpace(node.PeerID))
			if decErr != nil {
				mu.Lock()
				lastErr = decErr
				mu.Unlock()
				return
			}
			cctx, ccancel := context.WithTimeout(s.ctx, offlinestore.DefaultRPCTimeout)
			defer ccancel()
			if connErr := offlinestore.ConnectStorePeer(cctx, s.host, s.routing, node, offlinestore.DefaultStoreAddrTTL); connErr != nil {
				mu.Lock()
				lastErr = connErr
				mu.Unlock()
				return
			}
			resp, stErr := client.StoreMessage(cctx, pid, req)
			if stErr != nil {
				mu.Lock()
				lastErr = stErr
				mu.Unlock()
				return
			}
			if resp != nil && resp.OK {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okCount == 0 {
		if lastErr != nil {
			return lastErr
		}
		return errors.New("offline store: all submissions failed")
	}
	return nil
}

func (s *Service) buildOfflineEnvelope(msg Message, storedBlob, cachedCiphertext []byte) (*OfflineMessageEnvelope, error) {
	ciphertext := cachedCiphertext
	if len(ciphertext) == 0 {
		ciphertext = storedBlob
	}
	if len(ciphertext) == 0 {
		return nil, errors.New("offline envelope: empty ciphertext")
	}
	_, err := s.store.GetSessionState(msg.ConversationID)
	if err != nil {
		return nil, err
	}
	nonce := protocol.BuildAEADNonce("fwd", msg.Counter)
	ttl := offlineDefaultTTLSec
	env := &OfflineMessageEnvelope{
		Version:        1,
		MsgID:          msg.MsgID,
		SenderID:       msg.SenderPeerID,
		RecipientID:    msg.ReceiverPeerID,
		ConversationID: msg.ConversationID,
		CreatedAt:      msg.CreatedAt.Unix(), // 秒，與 store-node 示例一致；拉取時還原為 SentAtUnix 毫秒
		TTLSec:         &ttl,
		Cipher: OfflineCipherPayload{
			Algorithm:      "chacha20-poly1305",
			RecipientKeyID: encodeRecipientKeyID(msg.Counter, msg.MsgType),
			Nonce:          base64.StdEncoding.EncodeToString(nonce),
			Ciphertext:     base64.StdEncoding.EncodeToString(ciphertext),
		},
		Signature: OfflineSignature{},
	}
	return env, nil
}

func offlineSentAtUnix(env *OfflineMessageEnvelope) int64 {
	if env == nil {
		return 0
	}
	// 兼容秒 / 毫秒
	if env.CreatedAt > 1_000_000_000_000 {
		return env.CreatedAt
	}
	return env.CreatedAt * 1000
}

func (s *Service) runOfflineStoreFetchLoop() {
	delay := time.Duration(5+rand.Intn(25)) * time.Second
	t := time.NewTimer(delay)
	defer t.Stop()
	tick := time.NewTicker(offlineFetchInterval)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.pollOfflineStoresOnce()
		case <-tick.C:
			s.pollOfflineStoresOnce()
		}
	}
}

func (s *Service) pollOfflineStoresOnce() {
	if s == nil || s.host == nil || s.nodePriv == nil {
		return
	}
	nodes := s.mergeOfflineStoreNodes("")
	if len(nodes) == 0 {
		return
	}
	for _, node := range nodes {
		s.fetchAndProcessFromStore(node)
	}
}

func (s *Service) fetchAndProcessFromStore(node offlinestore.OfflineStoreNode) {
	storePID, err := peer.Decode(strings.TrimSpace(node.PeerID))
	if err != nil {
		return
	}
	cctx, ccancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer ccancel()
	if err := offlinestore.ConnectStorePeer(cctx, s.host, s.routing, node, offlinestore.DefaultStoreAddrTTL); err != nil {
		return
	}
	lastAck, err := s.store.GetOfflineStoreLastAckSeq(node.PeerID)
	if err != nil {
		log.Printf("[chat] offline store cursor: %v", err)
		return
	}
	client := offlinestore.NewLibp2pStoreClient(s.host)
	resp, err := client.FetchMessages(s.ctx, storePID, s.localPeer, lastAck, offlineFetchLimit)
	if err != nil || resp == nil || !resp.OK {
		if err != nil {
			log.Printf("[chat] offline fetch peer=%s: %v", node.PeerID, err)
		}
		return
	}
	var maxOK uint64
	for _, raw := range resp.Items {
		if len(raw) == 0 {
			continue
		}
		seq, perr := s.processOneOfflineFetchItem(raw)
		if perr != nil {
			log.Printf("[chat] offline fetch item: %v", perr)
			break
		}
		if seq > maxOK {
			maxOK = seq
		}
	}
	if maxOK == 0 {
		return
	}
	ackReq, err := s.signOfflineAckReq(int64(maxOK))
	if err != nil {
		log.Printf("[chat] offline ack sign: %v", err)
		return
	}
	ackCtx, ackCancel := context.WithTimeout(s.ctx, offlinestore.DefaultRPCTimeout)
	defer ackCancel()
	ackResp, err := client.AckMessages(ackCtx, storePID, ackReq)
	if err != nil || ackResp == nil || !ackResp.OK {
		if err != nil {
			log.Printf("[chat] offline ack peer=%s: %v", node.PeerID, err)
		}
		return
	}
	if err := s.store.SetOfflineStoreLastAckSeq(node.PeerID, int64(maxOK)); err != nil {
		log.Printf("[chat] offline cursor save: %v", err)
	}
}

func (s *Service) signOfflineAckReq(ackSeq int64) (*offlinestore.AckMessagesRequest, error) {
	if s.nodePriv == nil {
		return nil, errors.New("no node key")
	}
	t := time.Now().Unix()
	pl, err := canonicalAckPayload(1, s.localPeer, offlineDeviceID, uint64(ackSeq), t)
	if err != nil {
		return nil, err
	}
	sig, err := s.nodePriv.Sign(pl)
	if err != nil {
		return nil, err
	}
	sigObj := map[string]string{
		"algorithm": "ed25519",
		"value":     base64.StdEncoding.EncodeToString(sig),
	}
	sigRaw, err := json.Marshal(sigObj)
	if err != nil {
		return nil, err
	}
	return &offlinestore.AckMessagesRequest{
		Version:     1,
		RecipientID: s.localPeer,
		DeviceID:    offlineDeviceID,
		AckSeq:      ackSeq,
		AckedAt:     t,
		Signature:   sigRaw,
	}, nil
}

func (s *Service) processOneOfflineFetchItem(raw []byte) (storeSeq uint64, err error) {
	var wire StoredMessageWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return 0, err
	}
	if wire.Message == nil {
		return 0, errors.New("nil offline message")
	}
	env := wire.Message
	if strings.TrimSpace(env.RecipientID) != s.localPeer {
		return 0, errors.New("recipient mismatch")
	}
	if err := verifyOfflineEnvelope(env.SenderID, env, offlineDefaultTTLSec); err != nil {
		return 0, err
	}
	if env.Cipher.Algorithm == OfflineFriendAlgoPlain {
		return s.processOfflineFriendPayload(env, wire.StoreSeq)
	}
	if _, err := s.store.GetSessionState(env.ConversationID); err != nil {
		return 0, err
	}
	counter, msgType, err := decodeRecipientKeyID(env.Cipher.RecipientKeyID)
	if err != nil {
		return 0, err
	}
	ctBytes, err := base64.StdEncoding.DecodeString(env.Cipher.Ciphertext)
	if err != nil {
		return 0, err
	}
	ct := ChatText{
		Type:             msgType,
		ConversationID: env.ConversationID,
		MsgID:            env.MsgID,
		FromPeerID:       env.SenderID,
		ToPeerID:         env.RecipientID,
		Ciphertext:       ctBytes,
		Counter:          counter,
		SentAtUnix:       offlineSentAtUnix(env),
	}
	if err := s.handleIncomingChatText(ct, TransportModeDirect, "", true); err != nil {
		return 0, err
	}
	return wire.StoreSeq, nil
}

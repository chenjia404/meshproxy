package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/offlinestore"
	"github.com/chenjia404/meshproxy/internal/protocol"
)

const (
	offlineFetchInterval = 90 * time.Second
	offlineFetchLimit    = 100
	// offlineFetchMaxBatchesPerPoll 单次轮询在同一 store 上最多连续拉取批次数；收到 HasMore 时继续下一页，避免积压需多轮 90s 才能清空。
	offlineFetchMaxBatchesPerPoll = 500
	offlineSubmitConcurrency      = 4

	// offlineStoreConnProtectTag 标记离线 store 对端，避免 ConnManager 因空闲裁撤底层连接（后续 NewStream 仍复用同一条连接）。
	offlineStoreConnProtectTag = "meshproxy-offline-store"
	// offlineStorePeerAddrTTL 写入 peerstore 的 store 地址 TTL，宜长于默认 1h，减少地址过期后的重复解析。
	offlineStorePeerAddrTTL = 24 * time.Hour
	// offlineStoreConnectTimeout 首次拨号 / 保活拨号超时。
	offlineStoreConnectTimeout = 30 * time.Second
	// offlineStoreKeepAliveInterval 周期性确保与已配置 store 仍连接并维持 Protect。
	offlineStoreKeepAliveInterval = 2 * time.Minute
)

type offlineFetchItemError struct {
	err       error
	permanent bool
}

func (e *offlineFetchItemError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *offlineFetchItemError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func newPermanentOfflineFetchItemError(err error) error {
	if err == nil {
		return nil
	}
	return &offlineFetchItemError{err: err, permanent: true}
}

func isPermanentOfflineFetchItemError(err error) bool {
	var target *offlineFetchItemError
	return errors.As(err, &target) && target.permanent
}

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

// connectOfflineStorePeerAndProtect 连接离线 store 并将 peer 标记为受保护，避免空闲时被 ConnManager 断开，便于后续 RPC 复用同一条传输连接。
func (s *Service) connectOfflineStorePeerAndProtect(node offlinestore.OfflineStoreNode) error {
	if s == nil || s.host == nil {
		return errors.New("offline store: nil service or host")
	}
	cctx, cancel := context.WithTimeout(s.ctx, offlineStoreConnectTimeout)
	defer cancel()
	if err := offlinestore.ConnectStorePeer(cctx, s.host, s.routing, node, offlineStorePeerAddrTTL); err != nil {
		return err
	}
	pid, err := peer.Decode(strings.TrimSpace(node.PeerID))
	if err != nil {
		return err
	}
	if cm := s.host.ConnManager(); cm != nil {
		cm.Protect(pid, offlineStoreConnProtectTag)
	}
	return nil
}

func (s *Service) tryOfflineStoreSubmit(msg Message, storedBlob, cachedCiphertext []byte, quiet bool) error {
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
	return s.submitSignedOfflineEnvelope(env, quiet)
}

// submitSignedOfflineEnvelope 將已簽名的 OfflineMessageEnvelope 並發提交到配置的 store 節點。
// quiet 為 true 時不記錄「submitting / submit ok」（例如中繼已成功後的冗餘寫入）。
func (s *Service) submitSignedOfflineEnvelope(env *OfflineMessageEnvelope, quiet bool) error {
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

	if !quiet {
		log.Printf("[chat] offline store: submitting msg=%s recipient=%s stores=%d", env.MsgID, env.RecipientID, len(nodes))
	}

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
			if connErr := s.connectOfflineStorePeerAndProtect(node); connErr != nil {
				mu.Lock()
				lastErr = connErr
				mu.Unlock()
				return
			}
			smCtx, smCancel := context.WithTimeout(s.ctx, offlinestore.DefaultRPCTimeout)
			resp, stErr := client.StoreMessage(smCtx, pid, req)
			smCancel()
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
	if !quiet {
		log.Printf("[chat] offline store: submit ok msg=%s accepted=%d/%d", env.MsgID, okCount, len(nodes))
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

// SyncOfflineStoresNow 立即拉取一次所有离线 store；用于登录/初始化完成后的首轮同步。
func (s *Service) SyncOfflineStoresNow() {
	s.pollOfflineStoresOnce()
}

// runOfflineStoreKeepConnectedLoop 启动后尽早连接各离线 store，并周期性重连 + Protect，复用底层连接、减少频繁建连。
func (s *Service) runOfflineStoreKeepConnectedLoop() {
	delay := time.Duration(2+rand.Intn(8)) * time.Second
	t := time.NewTimer(delay)
	defer t.Stop()
	tick := time.NewTicker(offlineStoreKeepAliveInterval)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.warmOfflineStoreConnectionsOnce()
		case <-tick.C:
			s.warmOfflineStoreConnectionsOnce()
		}
	}
}

func (s *Service) warmOfflineStoreConnectionsOnce() {
	if s == nil || s.host == nil {
		return
	}
	nodes := s.mergeOfflineStoreNodes("")
	for _, node := range nodes {
		if err := s.connectOfflineStorePeerAndProtect(node); err != nil {
			log.Printf("[chat] offline store keep-connected peer=%s: %v", node.PeerID, err)
		}
	}
}

func (s *Service) pollOfflineStoresOnce() {
	if s == nil || s.host == nil || s.nodePriv == nil {
		return
	}
	s.offlineStoreSyncMu.Lock()
	defer s.offlineStoreSyncMu.Unlock()
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
	if err := s.connectOfflineStorePeerAndProtect(node); err != nil {
		return
	}
	lastAck, err := s.store.GetOfflineStoreLastAckSeq(node.PeerID)
	if err != nil {
		log.Printf("[chat] offline store cursor: %v", err)
		return
	}
	client := offlinestore.NewLibp2pStoreClient(s.host)
	var batches int
	var finalSeq uint64
	var stoppedDueToCap bool
	lastAckI64 := lastAck
	for batch := 0; batch < offlineFetchMaxBatchesPerPoll; batch++ {
		resp, err := client.FetchMessages(s.ctx, storePID, s.localPeer, lastAckI64, offlineFetchLimit)
		if err != nil || resp == nil || !resp.OK {
			if err != nil {
				log.Printf("[chat] offline fetch peer=%s: %v", node.PeerID, err)
			}
			return
		}
		batches++
		var maxOK uint64
		var sawNonEmpty bool
		for _, raw := range resp.Items {
			if len(raw) == 0 {
				continue
			}
			sawNonEmpty = true
			seq, perr := s.processOneOfflineFetchItem(raw)
			if perr != nil {
				if isPermanentOfflineFetchItemError(perr) {
					log.Printf("[chat] offline fetch item store_seq=%d skipped permanently: %v", seq, perr)
					if seq > maxOK {
						maxOK = seq
					}
					continue
				}
				log.Printf("[chat] offline fetch item store_seq=%d blocked for retry: %v", seq, perr)
				break
			}
			if seq > maxOK {
				maxOK = seq
			}
		}
		if maxOK == 0 {
			if !sawNonEmpty {
				break
			}
			log.Printf("[chat] offline fetch peer=%s: non-empty batch but no store_seq (cannot advance cursor)", node.PeerID)
			return
		}
		ackReq, err := s.signOfflineAckReq(int64(maxOK))
		if err != nil {
			log.Printf("[chat] offline ack sign: %v", err)
			return
		}
		ackCtx, ackCancel := context.WithTimeout(s.ctx, offlinestore.DefaultRPCTimeout)
		ackResp, err := client.AckMessages(ackCtx, storePID, ackReq)
		ackCancel()
		if err != nil || ackResp == nil || !ackResp.OK {
			if err != nil {
				log.Printf("[chat] offline ack peer=%s: %v", node.PeerID, err)
			}
			return
		}
		if err := s.store.SetOfflineStoreLastAckSeq(node.PeerID, int64(maxOK)); err != nil {
			log.Printf("[chat] offline cursor save: %v", err)
			return
		}
		finalSeq = maxOK
		lastAckI64 = int64(maxOK)
		if !resp.HasMore {
			break
		}
		if batch == offlineFetchMaxBatchesPerPoll-1 {
			stoppedDueToCap = true
			break
		}
	}
	if finalSeq == 0 {
		return
	}
	if batches > 1 {
		log.Printf("[chat] offline store: fetched peer=%s batches=%d max_seq=%d", node.PeerID, batches, finalSeq)
	} else {
		log.Printf("[chat] offline store: fetched peer=%s items_ok max_seq=%d", node.PeerID, finalSeq)
	}
	if stoppedDueToCap {
		log.Printf("[chat] offline store: peer=%s backlog truncated at %d batches (next interval continues)", node.PeerID, offlineFetchMaxBatchesPerPoll)
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

// offlineStoreSeqJSONRx 在整条 JSON 损坏时尽力提取 store_seq，供跳过毒丸、推进 ACK 游标。
var offlineStoreSeqJSONRx = regexp.MustCompile(`"store_seq"\s*:\s*([0-9]+)`)

func peekOfflineStoreSeq(raw []byte) uint64 {
	var w struct {
		StoreSeq uint64 `json:"store_seq"`
	}
	if err := json.Unmarshal(raw, &w); err == nil {
		return w.StoreSeq
	}
	m := offlineStoreSeqJSONRx.FindSubmatch(raw)
	if len(m) != 2 {
		return 0
	}
	n, err := strconv.ParseUint(string(m[1]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func (s *Service) processOneOfflineFetchItem(raw []byte) (storeSeq uint64, err error) {
	var wire StoredMessageWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return peekOfflineStoreSeq(raw), newPermanentOfflineFetchItemError(err)
	}
	seq := wire.StoreSeq
	if wire.Message == nil {
		return seq, newPermanentOfflineFetchItemError(errors.New("nil offline message"))
	}
	env := wire.Message
	if strings.TrimSpace(env.RecipientID) != s.localPeer {
		return seq, newPermanentOfflineFetchItemError(errors.New("recipient mismatch"))
	}
	if err := verifyOfflineEnvelope(env.SenderID, env, offlineDefaultTTLSec); err != nil {
		return seq, newPermanentOfflineFetchItemError(err)
	}
	if err := validateOfflineEnvelopeTiming(env, &wire, offlineDefaultTTLSec, time.Now().UTC()); err != nil {
		return seq, newPermanentOfflineFetchItemError(err)
	}
	if env.Cipher.Algorithm == OfflineFriendAlgoECIES {
		if _, err := s.processOfflineFriendPayload(env, seq); err != nil {
			return seq, err
		}
		return seq, nil
	}
	counter, msgType, err := decodeRecipientKeyID(env.Cipher.RecipientKeyID)
	if err != nil {
		return seq, newPermanentOfflineFetchItemError(err)
	}
	ctBytes, err := base64.StdEncoding.DecodeString(env.Cipher.Ciphertext)
	if err != nil {
		return seq, newPermanentOfflineFetchItemError(err)
	}
	ct := ChatText{
		Type:           msgType,
		ConversationID: env.ConversationID,
		MsgID:          env.MsgID,
		FromPeerID:     env.SenderID,
		ToPeerID:       env.RecipientID,
		Ciphertext:     ctBytes,
		Counter:        counter,
		SentAtUnix:     offlineSentAtUnix(env),
	}
	if err := s.handleIncomingChatText(ct, TransportModeDirect, "", true); err != nil {
		return seq, err
	}
	return seq, nil
}

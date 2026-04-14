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
	"github.com/chenjia404/meshproxy/internal/safe"
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

	// OfflineGroupWireAlgoV1 表示离线 store 中承载的是完整群聊 wire 消息 JSON；
	// 外层 store envelope 负责 recipient 级签名，内层仍保留群消息自身签名。
	OfflineGroupWireAlgoV1 = "mesh-group-wire-v1"
)

type offlineFetchItemError struct {
	err       error
	permanent bool
}

type offlineConversationTask struct {
	run    func() (uint64, error)
	result chan offlineConversationTaskResult
}

type offlineConversationTaskResult struct {
	seq uint64
	err error
}

type offlineFetchPendingItem struct {
	idx      int
	storeSeq uint64
	resultCh chan offlineConversationTaskResult
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

func offlineFetchTaskKey(raw []byte) string {
	var preview struct {
		Message *struct {
			ConversationID string `json:"conversation_id"`
			SenderID       string `json:"sender_id"`
			MsgID          string `json:"msg_id"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &preview); err != nil || preview.Message == nil {
		return "__offline_default__"
	}
	if key := strings.TrimSpace(preview.Message.ConversationID); key != "" {
		return key
	}
	if key := strings.TrimSpace(preview.Message.SenderID); key != "" {
		return "__peer__:" + key
	}
	if key := strings.TrimSpace(preview.Message.MsgID); key != "" {
		return "__msg__:" + key
	}
	return "__offline_default__"
}

func (s *Service) offlineConversationTaskQueue(key string) chan offlineConversationTask {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "__offline_default__"
	}
	if q, ok := s.offlineConvWorkers.Load(key); ok {
		return q.(chan offlineConversationTask)
	}
	q := make(chan offlineConversationTask, 64)
	actual, loaded := s.offlineConvWorkers.LoadOrStore(key, q)
	if loaded {
		return actual.(chan offlineConversationTask)
	}
	safe.Go("chat.offlineConvWorker."+key, func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			case task, ok := <-q:
				if !ok {
					return
				}
				seq, err := task.run()
				task.result <- offlineConversationTaskResult{seq: seq, err: err}
				close(task.result)
			}
		}
	})
	return q
}

func (s *Service) runOfflineConversationTask(key string, run func() (uint64, error)) chan offlineConversationTaskResult {
	resultCh := make(chan offlineConversationTaskResult, 1)
	q := s.offlineConversationTaskQueue(key)
	task := offlineConversationTask{run: run, result: resultCh}
	select {
	case q <- task:
	case <-s.ctx.Done():
		resultCh <- offlineConversationTaskResult{err: s.ctx.Err()}
		close(resultCh)
	}
	return resultCh
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

// submitOfflineGroupWire 将单个群组协议载荷投递到指定接收者的离线 store。
// 这里存的是完整群组 wire JSON，而不是再次用私聊会话密钥加密后的载荷。
func (s *Service) submitOfflineGroupWire(recipientPeerID string, wire any, quiet bool) error {
	if s == nil || s.nodePriv == nil || s.host == nil {
		return errors.New("offline store: not configured")
	}
	env, err := s.buildOfflineGroupEnvelope(recipientPeerID, wire)
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

func (s *Service) buildOfflineGroupEnvelope(recipientPeerID string, wire any) (*OfflineMessageEnvelope, error) {
	if strings.TrimSpace(recipientPeerID) == "" {
		return nil, errors.New("offline group envelope: empty recipient")
	}
	nowUnixMillis := time.Now().UTC().UnixMilli()
	ttl := offlineDefaultTTLSec
	switch msg := wire.(type) {
	case GroupControlEnvelope:
		raw, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		return &OfflineMessageEnvelope{
			Version:        1,
			MsgID:          firstNonEmptyString(msg.EventID, fmt.Sprintf("group-control-%s-%d", msg.GroupID, msg.EventSeq)),
			SenderID:       msg.SignerPeerID,
			RecipientID:    recipientPeerID,
			ConversationID: msg.GroupID,
			CreatedAt:      firstNonZeroInt64(msg.CreatedAtUnix, nowUnixMillis),
			TTLSec:         &ttl,
			Cipher: OfflineCipherPayload{
				Algorithm:      OfflineGroupWireAlgoV1,
				RecipientKeyID: encodeRecipientKeyID(msg.EventSeq, MessageTypeGroupControl),
				Nonce:          "",
				Ciphertext:     base64.StdEncoding.EncodeToString(raw),
			},
			Signature: OfflineSignature{},
		}, nil
	case *GroupControlEnvelope:
		if msg == nil {
			return nil, errors.New("offline group envelope: nil group control")
		}
		return s.buildOfflineGroupEnvelope(recipientPeerID, *msg)
	case GroupJoinRequest:
		raw, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		return &OfflineMessageEnvelope{
			Version:        1,
			MsgID:          fmt.Sprintf("group-join-%s-%s-%d", msg.GroupID, msg.JoinerPeerID, msg.SentAtUnix),
			SenderID:       msg.JoinerPeerID,
			RecipientID:    recipientPeerID,
			ConversationID: msg.GroupID,
			CreatedAt:      firstNonZeroInt64(msg.SentAtUnix, nowUnixMillis),
			TTLSec:         &ttl,
			Cipher: OfflineCipherPayload{
				Algorithm:      OfflineGroupWireAlgoV1,
				RecipientKeyID: encodeRecipientKeyID(msg.AcceptedEpoch, MessageTypeGroupJoinRequest),
				Nonce:          "",
				Ciphertext:     base64.StdEncoding.EncodeToString(raw),
			},
			Signature: OfflineSignature{},
		}, nil
	case *GroupJoinRequest:
		if msg == nil {
			return nil, errors.New("offline group envelope: nil group join request")
		}
		return s.buildOfflineGroupEnvelope(recipientPeerID, *msg)
	case GroupLeaveRequest:
		raw, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		return &OfflineMessageEnvelope{
			Version:        1,
			MsgID:          fmt.Sprintf("group-leave-%s-%s-%d", msg.GroupID, msg.LeaverPeerID, msg.SentAtUnix),
			SenderID:       msg.LeaverPeerID,
			RecipientID:    recipientPeerID,
			ConversationID: msg.GroupID,
			CreatedAt:      firstNonZeroInt64(msg.SentAtUnix, nowUnixMillis),
			TTLSec:         &ttl,
			Cipher: OfflineCipherPayload{
				Algorithm:      OfflineGroupWireAlgoV1,
				RecipientKeyID: encodeRecipientKeyID(0, MessageTypeGroupLeaveRequest),
				Nonce:          "",
				Ciphertext:     base64.StdEncoding.EncodeToString(raw),
			},
			Signature: OfflineSignature{},
		}, nil
	case *GroupLeaveRequest:
		if msg == nil {
			return nil, errors.New("offline group envelope: nil group leave request")
		}
		return s.buildOfflineGroupEnvelope(recipientPeerID, *msg)
	case GroupChatText:
		raw, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		nonce := protocol.BuildGroupChatNonce(msg.GroupID, msg.SenderPeerID, msg.SenderSeq)
		return &OfflineMessageEnvelope{
			Version:        1,
			MsgID:          msg.MsgID,
			SenderID:       msg.SenderPeerID,
			RecipientID:    recipientPeerID,
			ConversationID: msg.GroupID,
			CreatedAt:      firstNonZeroInt64(msg.SentAtUnix, nowUnixMillis),
			TTLSec:         &ttl,
			Cipher: OfflineCipherPayload{
				Algorithm:      OfflineGroupWireAlgoV1,
				RecipientKeyID: encodeRecipientKeyID(msg.SenderSeq, MessageTypeGroupChatText),
				Nonce:          base64.StdEncoding.EncodeToString(nonce),
				Ciphertext:     base64.StdEncoding.EncodeToString(raw),
			},
			Signature: OfflineSignature{},
		}, nil
	case *GroupChatText:
		if msg == nil {
			return nil, errors.New("offline group envelope: nil group chat text")
		}
		return s.buildOfflineGroupEnvelope(recipientPeerID, *msg)
	case GroupChatFile:
		raw, err := json.Marshal(msg)
		if err != nil {
			return nil, err
		}
		nonce := protocol.BuildGroupChatNonce(msg.GroupID, msg.SenderPeerID, msg.SenderSeq)
		return &OfflineMessageEnvelope{
			Version:        1,
			MsgID:          msg.MsgID,
			SenderID:       msg.SenderPeerID,
			RecipientID:    recipientPeerID,
			ConversationID: msg.GroupID,
			CreatedAt:      firstNonZeroInt64(msg.SentAtUnix, nowUnixMillis),
			TTLSec:         &ttl,
			Cipher: OfflineCipherPayload{
				Algorithm:      OfflineGroupWireAlgoV1,
				RecipientKeyID: encodeRecipientKeyID(msg.SenderSeq, MessageTypeGroupChatFile),
				Nonce:          base64.StdEncoding.EncodeToString(nonce),
				Ciphertext:     base64.StdEncoding.EncodeToString(raw),
			},
			Signature: OfflineSignature{},
		}, nil
	case *GroupChatFile:
		if msg == nil {
			return nil, errors.New("offline group envelope: nil group chat file")
		}
		return s.buildOfflineGroupEnvelope(recipientPeerID, *msg)
	default:
		return nil, fmt.Errorf("offline group envelope: unsupported wire type %T", wire)
	}
}

func firstNonZeroInt64(vs ...int64) int64 {
	for _, v := range vs {
		if v != 0 {
			return v
		}
	}
	return 0
}

func firstNonEmptyString(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
	if s.isServerModeActive() {
		return
	}
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
	if s.isServerModeActive() {
		return
	}
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
	log.Printf("[chat] offline fetch start peer=%s after_seq=%d limit=%d", node.PeerID, lastAck, offlineFetchLimit)
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
		log.Printf("[chat] offline fetch batch peer=%s batch=%d after_seq=%d items=%d has_more=%v", node.PeerID, batch+1, lastAckI64, len(resp.Items), resp.HasMore)
		var maxOK uint64
		var sawNonEmpty bool
		nItems := len(resp.Items)
		pending := make([]offlineFetchPendingItem, 0, nItems)
		for i, raw := range resp.Items {
			if len(raw) == 0 {
				continue
			}
			sawNonEmpty = true
			seqPreview := peekOfflineStoreSeq(raw)
			log.Printf("[chat] offline fetch item begin peer=%s batch=%d item=%d/%d store_seq=%d", node.PeerID, batch+1, i+1, nItems, seqPreview)
			rawCopy := append([]byte(nil), raw...)
			resultCh := s.runOfflineConversationTask(offlineFetchTaskKey(rawCopy), func() (uint64, error) {
				return s.processOneOfflineFetchItem(rawCopy)
			})
			pending = append(pending, offlineFetchPendingItem{
				idx:      i,
				storeSeq: seqPreview,
				resultCh: resultCh,
			})
		}
		for _, item := range pending {
			result, ok := <-item.resultCh
			if !ok {
				result = offlineConversationTaskResult{seq: item.storeSeq, err: errors.New("offline conversation worker closed")}
			}
			seq, perr := result.seq, result.err
			if perr != nil {
				if isPermanentOfflineFetchItemError(perr) {
					log.Printf("[chat] offline fetch item store_seq=%d skipped permanently: %v", seq, perr)
					if seq > maxOK {
						maxOK = seq
					}
					continue
				}
				log.Printf("[chat] offline fetch item [%d/%d] store_seq=%d blocked for retry: %v", item.idx+1, nItems, seq, perr)
				break
			}
			log.Printf("[chat] offline fetch item done peer=%s batch=%d item=%d/%d store_seq=%d", node.PeerID, batch+1, item.idx+1, nItems, seq)
			if seq > maxOK {
				maxOK = seq
			}
		}
		if maxOK == 0 {
			if !sawNonEmpty {
				break
			}
			log.Printf("[chat] offline fetch peer=%s: non-empty batch (%d items) but no successful store_seq (cannot advance cursor; check logs above)", node.PeerID, nItems)
			return
		}
		ackReq, err := s.signOfflineAckReq(int64(maxOK))
		if err != nil {
			log.Printf("[chat] offline ack sign: %v", err)
			return
		}
		log.Printf("[chat] offline ack submit peer=%s batch=%d ack_seq=%d", node.PeerID, batch+1, maxOK)
		ackCtx, ackCancel := context.WithTimeout(s.ctx, offlinestore.DefaultRPCTimeout)
		ackResp, err := client.AckMessages(ackCtx, storePID, ackReq)
		ackCancel()
		if err != nil || ackResp == nil || !ackResp.OK {
			switch {
			case err != nil:
				log.Printf("[chat] offline ack peer=%s: %v", node.PeerID, err)
			case ackResp == nil:
				log.Printf("[chat] offline ack peer=%s: nil response", node.PeerID)
			default:
				log.Printf("[chat] offline ack peer=%s: ok=false code=%s msg=%s", node.PeerID, ackResp.ErrorCode, ackResp.ErrorMessage)
			}
			return
		}
		if err := s.store.SetOfflineStoreLastAckSeq(node.PeerID, int64(maxOK)); err != nil {
			log.Printf("[chat] offline cursor save: %v", err)
			return
		}
		log.Printf("[chat] offline ack applied peer=%s batch=%d ack_seq=%d deleted_until_seq=%d", node.PeerID, batch+1, maxOK, ackResp.DeletedUntilSeq)
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
	// meshchat-store V1：AckRequest.device_id 必須等於 recipient_id（見 store-node validateAckRequest），
	// 與 auth.CanonicalAck 簽名載荷一致；不可用固定字串如 mesh-proxy。
	deviceID := s.localPeer
	pl, err := canonicalAckPayload(1, s.localPeer, deviceID, uint64(ackSeq), t)
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
		DeviceID:    deviceID,
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
	log.Printf("[chat] offline fetch item parsed store_seq=%d msg=%s conv=%s from=%s to=%s algo=%s",
		seq, env.MsgID, env.ConversationID, env.SenderID, env.RecipientID, env.Cipher.Algorithm)
	if strings.TrimSpace(env.RecipientID) != s.localPeer {
		return seq, newPermanentOfflineFetchItemError(errors.New("recipient mismatch"))
	}
	if err := verifyOfflineEnvelope(env.SenderID, env, offlineDefaultTTLSec); err != nil {
		return seq, newPermanentOfflineFetchItemError(err)
	}
	log.Printf("[chat] offline fetch item verified store_seq=%d msg=%s", seq, env.MsgID)
	if err := validateOfflineEnvelopeTiming(env, &wire, offlineDefaultTTLSec, time.Now().UTC()); err != nil {
		return seq, newPermanentOfflineFetchItemError(err)
	}
	log.Printf("[chat] offline fetch item timing-ok store_seq=%d msg=%s", seq, env.MsgID)
	if env.Cipher.Algorithm == OfflineFriendAlgoECIES {
		log.Printf("[chat] offline fetch item friend-control begin store_seq=%d msg=%s", seq, env.MsgID)
		if _, err := s.processOfflineFriendPayload(env, seq); err != nil {
			return seq, fmt.Errorf("store_seq=%d msg=%s conv=%s from=%s algo=%s: process offline friend payload: %w",
				seq, env.MsgID, env.ConversationID, env.SenderID, env.Cipher.Algorithm, err)
		}
		log.Printf("[chat] offline fetch item friend-control done store_seq=%d msg=%s", seq, env.MsgID)
		return seq, nil
	}
	if env.Cipher.Algorithm == OfflineGroupWireAlgoV1 {
		log.Printf("[chat] offline fetch item group-wire begin store_seq=%d msg=%s", seq, env.MsgID)
		if err := s.processOfflineGroupPayload(env, seq); err != nil {
			return seq, fmt.Errorf("store_seq=%d msg=%s conv=%s from=%s algo=%s: process offline group payload: %w",
				seq, env.MsgID, env.ConversationID, env.SenderID, env.Cipher.Algorithm, err)
		}
		log.Printf("[chat] offline fetch item group-wire done store_seq=%d msg=%s", seq, env.MsgID)
		return seq, nil
	}
	counter, msgType, err := decodeRecipientKeyID(env.Cipher.RecipientKeyID)
	if err != nil {
		return seq, newPermanentOfflineFetchItemError(err)
	}
	log.Printf("[chat] offline fetch item chat begin store_seq=%d msg=%s type=%s counter=%d", seq, env.MsgID, msgType, counter)
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
	if err := s.handleIncomingChatText(ct, TransportModeOfflineStore, "", true, true); err != nil {
		return seq, fmt.Errorf("store_seq=%d msg=%s conv=%s from=%s to=%s type=%s counter=%d: handle incoming chat text: %w",
			seq, env.MsgID, env.ConversationID, env.SenderID, env.RecipientID, msgType, counter, err)
	}
	log.Printf("[chat] offline fetch item chat done store_seq=%d msg=%s type=%s counter=%d", seq, env.MsgID, msgType, counter)
	return seq, nil
}

func (s *Service) processOfflineGroupPayload(env *OfflineMessageEnvelope, storeSeq uint64) error {
	if env == nil {
		return newPermanentOfflineFetchItemError(errors.New("nil offline group envelope"))
	}
	if env.Cipher.Algorithm != OfflineGroupWireAlgoV1 {
		return newPermanentOfflineFetchItemError(fmt.Errorf("unsupported offline group algorithm %q", env.Cipher.Algorithm))
	}
	_, msgType, err := decodeRecipientKeyID(env.Cipher.RecipientKeyID)
	if err != nil {
		return newPermanentOfflineFetchItemError(err)
	}
	raw, err := base64.StdEncoding.DecodeString(env.Cipher.Ciphertext)
	if err != nil {
		return newPermanentOfflineFetchItemError(err)
	}
	switch msgType {
	case MessageTypeGroupControl, MessageTypeGroupJoinRequest, MessageTypeGroupLeaveRequest:
		var rawEnv map[string]any
		if err := json.Unmarshal(raw, &rawEnv); err != nil {
			return newPermanentOfflineFetchItemError(err)
		}
		groupID, senderPeerID, err := offlineGroupEnvelopeIdentity(rawEnv)
		if err != nil {
			return newPermanentOfflineFetchItemError(err)
		}
		if strings.TrimSpace(groupID) == "" {
			return newPermanentOfflineFetchItemError(errors.New("offline group control: empty group_id"))
		}
		if groupID != strings.TrimSpace(env.ConversationID) {
			return newPermanentOfflineFetchItemError(errors.New("offline group control: group_id mismatch"))
		}
		if senderPeerID != strings.TrimSpace(env.SenderID) {
			return newPermanentOfflineFetchItemError(errors.New("offline group control: sender mismatch"))
		}
		log.Printf("[group] offline store control store_seq=%d type=%s group=%s sender=%s",
			storeSeq, msgType, groupID, senderPeerID)
		return s.processGroupEnvelope(rawEnv)
	case MessageTypeGroupChatText:
		var msg GroupChatText
		if err := json.Unmarshal(raw, &msg); err != nil {
			return newPermanentOfflineFetchItemError(err)
		}
		if strings.TrimSpace(msg.GroupID) == "" {
			return newPermanentOfflineFetchItemError(errors.New("offline group text: empty group_id"))
		}
		if msg.GroupID != strings.TrimSpace(env.ConversationID) {
			return newPermanentOfflineFetchItemError(errors.New("offline group text: group_id mismatch"))
		}
		if msg.SenderPeerID != strings.TrimSpace(env.SenderID) {
			return newPermanentOfflineFetchItemError(errors.New("offline group text: sender mismatch"))
		}
		if err := s.verifyGroupChatText(msg); err != nil {
			return newPermanentOfflineFetchItemError(err)
		}
		log.Printf("[group] offline store text store_seq=%d group=%s msg=%s sender=%s seq=%d",
			storeSeq, msg.GroupID, msg.MsgID, msg.SenderPeerID, msg.SenderSeq)
		return s.handleIncomingGroupText(msg)
	case MessageTypeGroupChatFile:
		var msg GroupChatFile
		if err := json.Unmarshal(raw, &msg); err != nil {
			return newPermanentOfflineFetchItemError(err)
		}
		if strings.TrimSpace(msg.GroupID) == "" {
			return newPermanentOfflineFetchItemError(errors.New("offline group file: empty group_id"))
		}
		if msg.GroupID != strings.TrimSpace(env.ConversationID) {
			return newPermanentOfflineFetchItemError(errors.New("offline group file: group_id mismatch"))
		}
		if msg.SenderPeerID != strings.TrimSpace(env.SenderID) {
			return newPermanentOfflineFetchItemError(errors.New("offline group file: sender mismatch"))
		}
		if err := s.verifyGroupChatFile(msg); err != nil {
			return newPermanentOfflineFetchItemError(err)
		}
		log.Printf("[group] offline store file store_seq=%d group=%s msg=%s sender=%s seq=%d",
			storeSeq, msg.GroupID, msg.MsgID, msg.SenderPeerID, msg.SenderSeq)
		return s.handleIncomingGroupFile(msg)
	default:
		return newPermanentOfflineFetchItemError(fmt.Errorf("unsupported offline group msg_type %q", msgType))
	}
}

func offlineGroupEnvelopeIdentity(env map[string]any) (groupID string, senderPeerID string, err error) {
	typeName, _ := env["type"].(string)
	switch typeName {
	case MessageTypeGroupControl:
		var ctrl GroupControlEnvelope
		if err := remarshal(env, &ctrl); err != nil {
			return "", "", err
		}
		return ctrl.GroupID, ctrl.SignerPeerID, nil
	case MessageTypeGroupJoinRequest:
		var req GroupJoinRequest
		if err := remarshal(env, &req); err != nil {
			return "", "", err
		}
		return req.GroupID, req.JoinerPeerID, nil
	case MessageTypeGroupLeaveRequest:
		var req GroupLeaveRequest
		if err := remarshal(env, &req); err != nil {
			return "", "", err
		}
		return req.GroupID, req.LeaverPeerID, nil
	default:
		return "", "", fmt.Errorf("unsupported group envelope type %q", typeName)
	}
}

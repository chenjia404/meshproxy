package chat

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/binding"
	"github.com/chenjia404/meshproxy/internal/profile"
	"github.com/chenjia404/meshproxy/internal/safe"
)

const (
	defaultBindingTTLSeconds = 365 * 24 * 3600
	maxBindingTTLSeconds     = 3650 * 24 * 3600
)

// SetNodePrivateKey 注入節點 libp2p 身份私鑰（與 identity.Manager 一致），供綁定草稿簽名使用。
func (s *Service) SetNodePrivateKey(priv crypto.PrivKey) {
	s.nodePriv = priv
}

// GetLocalPeerProfile 回傳本機聊天 Profile（含 binding 欄位）。
func (s *Service) GetLocalPeerProfile() (Profile, error) {
	return s.GetProfile()
}

// CreateBindingDraft 產生待 ETH 簽名的草稿與 libp2p 端簽名。
func (s *Service) CreateBindingDraft(ethAddress string, chainID uint64, ttlSeconds int64) (binding.BindingDraft, error) {
	if s.nodePriv == nil {
		return binding.BindingDraft{}, errors.New("binding: node private key not configured")
	}
	ethAddress = binding.NormalizeEthAddress(ethAddress)
	if ethAddress == "" || !strings.HasPrefix(ethAddress, "0x") || len(ethAddress) != 42 {
		return binding.BindingDraft{}, errors.New("binding: invalid eth_address")
	}
	if chainID == 0 {
		return binding.BindingDraft{}, errors.New("binding: chain_id must be > 0")
	}
	ttl := ttlSeconds
	if ttl <= 0 {
		ttl = defaultBindingTTLSeconds
	}
	if ttl > maxBindingTTLSeconds {
		ttl = maxBindingTTLSeconds
	}
	curSeq, err := s.store.CurrentBindingSeq(s.localPeer)
	if err != nil {
		return binding.BindingDraft{}, err
	}
	nextSeq := curSeq + 1
	if nextSeq == 0 {
		return binding.BindingDraft{}, errors.New("binding: seq overflow")
	}
	var nonceBytes [16]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return binding.BindingDraft{}, err
	}
	nonce := hex.EncodeToString(nonceBytes[:])
	issued := time.Now().UTC().Unix()
	expire := issued + ttl
	pl := binding.BindingPayload{
		Version:    binding.Version1,
		Action:     binding.ActionBindEthAddr,
		PeerID:     s.localPeer,
		EthAddress: ethAddress,
		ChainID:    chainID,
		Domain:     binding.DomainMeshChat,
		Nonce:      nonce,
		Seq:        nextSeq,
		IssuedAt:   issued,
		ExpireAt:   expire,
	}
	if err := binding.ValidatePayloadFields(pl); err != nil {
		return binding.BindingDraft{}, err
	}
	canon := binding.CanonicalMessage(pl)
	sig, err := binding.SignWithPrivKey(s.nodePriv, []byte(canon))
	if err != nil {
		return binding.BindingDraft{}, err
	}
	return binding.BindingDraft{
		Payload:          pl,
		CanonicalMessage: canon,
		PeerSignature:    sig,
	}, nil
}

// FinalizeBindingRecord 在校驗雙簽後寫入本機 profile。
func (s *Service) FinalizeBindingRecord(payload binding.BindingPayload, peerSignature, ethSignature string) (Profile, error) {
	if s.nodePriv == nil {
		return Profile{}, errors.New("binding: node private key not configured")
	}
	payload.EthAddress = binding.NormalizeEthAddress(payload.EthAddress)
	if err := binding.ValidatePayloadFields(payload); err != nil {
		return Profile{}, err
	}
	if strings.TrimSpace(payload.PeerID) != s.localPeer {
		return Profile{}, errors.New("binding: payload.peer_id must match local peer")
	}
	if err := binding.PayloadMatchesCanonical(payload); err != nil {
		return Profile{}, err
	}
	canon := binding.CanonicalMessage(payload)
	if err := binding.VerifyPeerSignature(s.localPeer, []byte(canon), peerSignature); err != nil {
		return Profile{}, fmt.Errorf("binding: peer signature: %w", err)
	}
	curSeq, err := s.store.CurrentBindingSeq(s.localPeer)
	if err != nil {
		return Profile{}, err
	}
	if payload.Seq != curSeq+1 {
		return Profile{}, fmt.Errorf("binding: invalid seq: want %d got %d", curSeq+1, payload.Seq)
	}
	now := time.Now().UTC().Unix()
	if now > payload.ExpireAt {
		return Profile{}, errors.New("binding: payload expired")
	}
	if err := binding.ValidateEthSignaturePresent(ethSignature); err != nil {
		return Profile{}, err
	}
	var rec binding.BindingRecord
	rec.Payload = payload
	rec.Signatures.Peer.Algo = binding.AlgoLibP2P
	rec.Signatures.Peer.Value = strings.TrimSpace(peerSignature)
	rec.Signatures.Ethereum.Algo = binding.AlgoEIP191
	rec.Signatures.Ethereum.Value = strings.TrimSpace(ethSignature)

	p, err := s.store.SaveLocalBinding(s.localPeer, rec)
	if err != nil {
		return Profile{}, err
	}
	safe.Go("chat.broadcastBinding", func() {
		s.broadcastBindingToConversationPeers()
	})
	return p, nil
}

// ClearLocalBinding 清除本機綁定展示欄位（保留 seq）。
func (s *Service) ClearLocalBinding() (Profile, error) {
	return s.store.ClearLocalBinding(s.localPeer)
}

func (s *Service) broadcastBindingToConversationPeers() {
	convs, err := s.store.ListConversations()
	if err != nil {
		return
	}
	for _, c := range convs {
		s.maybeSyncBinding(c.PeerID)
	}
}

// maybeSyncBinding 向單一已建立會話的對端推送 BindingSync。
func (s *Service) maybeSyncBinding(peerID string) {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" || peerID == s.localPeer {
		return
	}
	if !s.peerSupportsChatRequest(peerID) {
		return
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return
	}
	if !s.peerHasActiveConnection(pid) {
		return
	}
	if _, err := s.store.GetConversationByPeer(peerID); err != nil {
		return
	}
	prof, err := s.GetProfile()
	if err != nil || prof.bindingRecord == nil {
		return
	}
	rec := *prof.bindingRecord
	wire := binding.BindingSync{
		Type:       binding.MessageTypeBindingSync,
		FromPeerID: s.localPeer,
		ToPeerID:   peerID,
		Binding:    &rec,
		SentAtUnix: time.Now().UnixMilli(),
	}
	safe.Go("chat.bindingSync."+peerID, func() {
		if err := s.sendEnvelopeConnectedOnly(peerID, wire); err != nil {
			if !isIgnorableChatRequestError(err) {
				log.Printf("[chat] binding sync failed peer=%s err=%v", peerID, err)
			}
		}
	})
}

// handleBindingSync 處理對端經 ProtocolChatRequest 送達的綁定同步。
func (s *Service) handleBindingSync(msg binding.BindingSync) error {
	if msg.Binding == nil {
		return nil
	}
	to := strings.TrimSpace(msg.ToPeerID)
	if to != "" && to != s.localPeer {
		return nil
	}
	from := strings.TrimSpace(msg.FromPeerID)
	if from == "" || from == s.localPeer {
		return nil
	}
	if _, err := s.store.GetConversationByPeer(from); err != nil {
		return nil
	}
	status, verr := s.validateBindingRecord(*msg.Binding, from)
	errMsg := ""
	if verr != nil {
		errMsg = verr.Error()
	}
	validatedAt := time.Now().UTC()
	if err := s.store.SavePeerBinding(from, *msg.Binding, status, errMsg, validatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	return nil
}

func (s *Service) validateBindingRecord(record binding.BindingRecord, expectedPeerID string) (string, error) {
	p := record.Payload
	p.EthAddress = binding.NormalizeEthAddress(p.EthAddress)
	if strings.TrimSpace(p.PeerID) != expectedPeerID {
		return profile.BindingStatusParseError, errors.New("payload.peer_id mismatch")
	}
	if err := binding.ValidatePayloadFields(p); err != nil {
		return profile.BindingStatusParseError, err
	}
	if strings.TrimSpace(record.Signatures.Peer.Algo) != binding.AlgoLibP2P {
		return profile.BindingStatusParseError, errors.New("invalid peer signature algo")
	}
	if strings.TrimSpace(record.Signatures.Ethereum.Algo) != binding.AlgoEIP191 {
		return profile.BindingStatusParseError, errors.New("invalid ethereum signature algo")
	}
	if err := binding.PayloadMatchesCanonical(p); err != nil {
		return profile.BindingStatusParseError, err
	}
	canon := binding.CanonicalMessage(p)
	if err := binding.VerifyPeerSignature(expectedPeerID, []byte(canon), record.Signatures.Peer.Value); err != nil {
		return profile.BindingStatusPeerSigInvalid, err
	}
	if err := binding.ValidateEthSignaturePresent(record.Signatures.Ethereum.Value); err != nil {
		return profile.BindingStatusEthSigInvalid, err
	}
	if time.Now().Unix() > p.ExpireAt {
		return profile.BindingStatusExpired, errors.New("binding expired")
	}
	return profile.BindingStatusValid, nil
}

// GetContactBindingDetails 回傳指定聯絡人已快取之 BindingRecord 與校驗狀態（須已存在與該 Peer 之單聊會話）。
func (s *Service) GetContactBindingDetails(peerID string) (ContactBindingDetails, error) {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return ContactBindingDetails{}, fmt.Errorf("empty peer_id")
	}
	if _, err := s.store.GetConversationByPeer(peerID); err != nil {
		return ContactBindingDetails{}, err
	}
	c, err := s.store.GetPeer(peerID)
	if err != nil {
		return ContactBindingDetails{}, err
	}
	return contactBindingDetailsFrom(c), nil
}

package publicchannel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ipfs/go-cid"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	corerouting "github.com/libp2p/go-libp2p/core/routing"
	"github.com/multiformats/go-multihash"

	"github.com/chenjia404/meshproxy/internal/chat"
	"github.com/chenjia404/meshproxy/internal/p2p"
	"github.com/chenjia404/meshproxy/internal/safe"
)

type Service struct {
	ctx       context.Context
	cancel    context.CancelFunc
	host      host.Host
	routing   corerouting.Routing
	pubsub    *pubsub.PubSub
	store     *Store
	localPeer string
	nodePriv  crypto.PrivKey
	ipfs      ipfsFilePinner

	subMu sync.Mutex
	subs  map[string]*channelSubscription

	provideMu               sync.Mutex
	provideStates           map[string]*provideState
	provideWakeCh           chan struct{}
	provideRetryBackoffs    []time.Duration
	bootstrapRetryBackoffs  []time.Duration
	provideSuccessInterval  time.Duration
	provideLoopInterval     time.Duration
	provideTimeout          time.Duration
	provideSuccessLogMinGap time.Duration
	nowFn                   func() time.Time
}

type serviceConfig struct {
	provideRetryBackoffs    []time.Duration
	bootstrapRetryBackoffs  []time.Duration
	provideSuccessInterval  time.Duration
	provideLoopInterval     time.Duration
	provideTimeout          time.Duration
	provideSuccessLogMinGap time.Duration
	nowFn                   func() time.Time
}

type channelSubscription struct {
	topic  *pubsub.Topic
	sub    *pubsub.Subscription
	cancel context.CancelFunc
}

// 公开频道 provider 公告状态只保存在内存中，用于异步重试与周期性重发。
type provideState struct {
	channelID        string
	lastProvideAt    int64
	lastProvideErrAt int64
	nextProvideAt    int64
	retryCount       int
	inFlight         bool
	nextProvideTime  time.Time
}

type contentRouter interface {
	Provide(ctx context.Context, c cid.Cid, announce bool) error
	FindProvidersAsync(ctx context.Context, c cid.Cid, count int) <-chan peer.AddrInfo
}

type ipfsFilePinner interface {
	PinAvatar(ctx context.Context, fileName string, data []byte) (cid string, err error)
	PinChatFile(ctx context.Context, fileName string, data []byte) (cid string, err error)
}

func defaultServiceConfig() serviceConfig {
	return serviceConfig{
		provideRetryBackoffs:    []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second, 5 * time.Minute},
		bootstrapRetryBackoffs:  []time.Duration{3 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second},
		provideSuccessInterval:  20 * time.Minute,
		provideLoopInterval:     time.Second,
		provideTimeout:          30 * time.Second,
		provideSuccessLogMinGap: time.Minute,
		nowFn:                   time.Now,
	}
}

func NewService(ctx context.Context, dbPath string, h host.Host, routing corerouting.Routing, ps *pubsub.PubSub) (*Service, error) {
	return newServiceWithConfig(ctx, dbPath, h, routing, ps, defaultServiceConfig())
}

func newServiceWithConfig(ctx context.Context, dbPath string, h host.Host, routing corerouting.Routing, ps *pubsub.PubSub, cfg serviceConfig) (*Service, error) {
	serviceCtx, cancel := context.WithCancel(ctx)
	store, err := NewStore(dbPath)
	if err != nil {
		cancel()
		return nil, err
	}
	if len(cfg.provideRetryBackoffs) == 0 {
		cfg.provideRetryBackoffs = defaultServiceConfig().provideRetryBackoffs
	}
	if len(cfg.bootstrapRetryBackoffs) == 0 {
		cfg.bootstrapRetryBackoffs = defaultServiceConfig().bootstrapRetryBackoffs
	}
	if cfg.provideSuccessInterval <= 0 {
		cfg.provideSuccessInterval = defaultServiceConfig().provideSuccessInterval
	}
	if cfg.provideLoopInterval <= 0 {
		cfg.provideLoopInterval = defaultServiceConfig().provideLoopInterval
	}
	if cfg.provideTimeout <= 0 {
		cfg.provideTimeout = defaultServiceConfig().provideTimeout
	}
	if cfg.provideSuccessLogMinGap < 0 {
		cfg.provideSuccessLogMinGap = 0
	}
	if cfg.nowFn == nil {
		cfg.nowFn = time.Now
	}
	s := &Service{
		ctx:                     serviceCtx,
		cancel:                  cancel,
		host:                    h,
		routing:                 routing,
		pubsub:                  ps,
		store:                   store,
		localPeer:               h.ID().String(),
		subs:                    make(map[string]*channelSubscription),
		provideStates:           make(map[string]*provideState),
		provideWakeCh:           make(chan struct{}, 1),
		provideRetryBackoffs:    append([]time.Duration(nil), cfg.provideRetryBackoffs...),
		bootstrapRetryBackoffs:  append([]time.Duration(nil), cfg.bootstrapRetryBackoffs...),
		provideSuccessInterval:  cfg.provideSuccessInterval,
		provideLoopInterval:     cfg.provideLoopInterval,
		provideTimeout:          cfg.provideTimeout,
		provideSuccessLogMinGap: cfg.provideSuccessLogMinGap,
		nowFn:                   cfg.nowFn,
	}
	h.SetStreamHandler(p2p.ProtocolPublicChannelRPC, s.handleRPCStream)
	s.runRetentionSweep(time.Now().UTC())
	safe.Go("publicchannel.retentionLoop", func() { s.runRetentionLoop() })
	safe.Go("publicchannel.reprovideLoop", func() { s.runProvideLoop() })
	safe.Go("publicchannel.reprovideRestore", func() { s.restoreOwnedChannelsForProvide() })
	return s, nil
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.subMu.Lock()
	for channelID, item := range s.subs {
		if item.cancel != nil {
			item.cancel()
		}
		if item.sub != nil {
			item.sub.Cancel()
		}
		if item.topic != nil {
			_ = item.topic.Close()
		}
		delete(s.subs, channelID)
	}
	s.subMu.Unlock()
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}

func (s *Service) SetNodePrivateKey(priv crypto.PrivKey) {
	s.nodePriv = priv
}

func (s *Service) SetIPFSFilePinner(p ipfsFilePinner) {
	s.ipfs = p
}

func (s *Service) now() time.Time {
	if s != nil && s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

func (s *Service) nowUnix() int64 {
	return s.now().Unix()
}

func (s *Service) buildPinnedAvatar(fileName, mimeType string, data []byte) (Avatar, error) {
	if len(data) == 0 {
		return Avatar{}, errors.New("avatar is empty")
	}
	if len(data) > chat.MaxProfileAvatarBytes {
		return Avatar{}, errors.New("avatar too large")
	}
	if s.ipfs == nil {
		return Avatar{}, errors.New("ipfs not available for public channel avatar upload")
	}
	fileName = chat.NormalizeAvatarFileName(fileName)
	if fileName == "" {
		return Avatar{}, errors.New("avatar file name is required")
	}
	mimeType = strings.TrimSpace(mimeType)
	c, err := s.ipfs.PinAvatar(s.ctx, fileName, data)
	if err != nil {
		return Avatar{}, err
	}
	if strings.TrimSpace(c) == "" {
		return Avatar{}, errors.New("ipfs pin avatar returned empty cid")
	}
	sum := sha256.Sum256(data)
	return Avatar{
		FileName: fileName,
		MIMEType: mimeType,
		Size:     int64(len(data)),
		SHA256:   hex.EncodeToString(sum[:]),
		BlobID:   c,
		URL:      "/ipfs/" + c + "/" + filepath.Base(fileName),
	}, nil
}

func (s *Service) GetChannelProfile(channelID string) (ChannelProfile, error) {
	return s.store.GetChannelProfile(channelID)
}

func (s *Service) GetChannelHead(channelID string) (ChannelHead, error) {
	return s.store.GetChannelHead(channelID)
}

func (s *Service) GetChannelSummary(channelID string) (ChannelSummary, error) {
	return s.store.GetChannelSummary(channelID)
}

func (s *Service) GetChannelMessage(channelID string, messageID int64) (ChannelMessage, error) {
	return s.store.GetChannelMessage(channelID, messageID)
}

func (s *Service) GetChannelMessages(channelID string, beforeMessageID int64, limit int) ([]ChannelMessage, error) {
	return s.store.GetChannelMessages(channelID, beforeMessageID, limit)
}

func (s *Service) GetChannelChanges(channelID string, afterSeq int64, limit int) (GetChangesResponse, error) {
	return s.store.GetChannelChanges(channelID, afterSeq, limit)
}

func (s *Service) ListChannelsByOwner(ownerPeerID string) ([]ChannelSummary, error) {
	return s.store.ListChannelsByOwner(strings.TrimSpace(ownerPeerID))
}

func (s *Service) ListSubscribedChannels() ([]ChannelSummary, error) {
	return s.store.ListSubscribedChannels(s.localPeer)
}

func (s *Service) ClearChannelUnreadCount(channelID string) (ChannelSummary, error) {
	now := time.Now().Unix()
	if err := s.store.ClearChannelUnreadCount(channelID, now); err != nil {
		return ChannelSummary{}, err
	}
	return s.store.GetChannelSummary(channelID)
}

func (s *Service) ListProviders(channelID string) ([]ChannelProvider, error) {
	return s.store.ListProviders(channelID)
}

func (s *Service) CreateChannel(input CreateChannelInput) (ChannelSummary, error) {
	if s.nodePriv == nil {
		return ChannelSummary{}, errors.New("public channel signer not configured")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return ChannelSummary{}, errors.New("channel name is required")
	}
	input.MessageRetentionMinutes = NormalizeRetentionMinutes(input.MessageRetentionMinutes)
	if err := ValidateRetentionMinutes(input.MessageRetentionMinutes); err != nil {
		return ChannelSummary{}, err
	}
	channelUUID, err := uuid.NewV7()
	if err != nil {
		return ChannelSummary{}, err
	}
	now := time.Now().Unix()
	profile := ChannelProfile{
		ChannelID:               channelUUID.String(),
		OwnerPeerID:             s.localPeer,
		OwnerVersion:            1,
		Name:                    name,
		Avatar:                  input.Avatar,
		Bio:                     strings.TrimSpace(input.Bio),
		MessageRetentionMinutes: input.MessageRetentionMinutes,
		ProfileVersion:          1,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := signProfile(s.nodePriv, &profile); err != nil {
		return ChannelSummary{}, err
	}
	head := ChannelHead{
		ChannelID:      profile.ChannelID,
		OwnerPeerID:    s.localPeer,
		OwnerVersion:   1,
		LastMessageID:  0,
		ProfileVersion: 1,
		LastSeq:        0,
		UpdatedAt:      now,
	}
	if err := signHead(s.nodePriv, &head); err != nil {
		return ChannelSummary{}, err
	}
	if err := s.store.CreateOwnedChannel(profile, head, now, s.localPeer); err != nil {
		return ChannelSummary{}, err
	}
	summary, err := s.store.GetChannelSummary(profile.ChannelID)
	if err != nil {
		return ChannelSummary{}, err
	}
	s.bootstrapOwnedChannelAsync(profile.ChannelID)
	return summary, nil
}

func (s *Service) CreateChannelWithAvatar(name, bio string, messageRetentionMinutes int, fileName, mimeType string, data []byte) (ChannelSummary, error) {
	avatar, err := s.buildPinnedAvatar(fileName, mimeType, data)
	if err != nil {
		return ChannelSummary{}, err
	}
	return s.CreateChannel(CreateChannelInput{
		Name:                    name,
		Bio:                     bio,
		Avatar:                  avatar,
		MessageRetentionMinutes: messageRetentionMinutes,
	})
}

func (s *Service) UpdateChannelProfile(channelID string, input UpdateChannelProfileInput) (ChannelSummary, error) {
	if s.nodePriv == nil {
		return ChannelSummary{}, errors.New("public channel signer not configured")
	}
	profile, err := s.store.GetChannelProfile(channelID)
	if err != nil {
		return ChannelSummary{}, err
	}
	head, err := s.store.GetChannelHead(channelID)
	if err != nil {
		return ChannelSummary{}, err
	}
	if profile.OwnerPeerID != s.localPeer {
		return ChannelSummary{}, errors.New("channel is not owned by local peer")
	}
	retentionMinutes := profile.MessageRetentionMinutes
	if input.MessageRetentionMinutes != nil {
		retentionMinutes = NormalizeRetentionMinutes(*input.MessageRetentionMinutes)
		if err := ValidateRetentionMinutes(retentionMinutes); err != nil {
			return ChannelSummary{}, err
		}
	}
	now := time.Now().Unix()
	profile.Name = strings.TrimSpace(input.Name)
	if profile.Name == "" {
		return ChannelSummary{}, errors.New("channel name is required")
	}
	profile.Bio = strings.TrimSpace(input.Bio)
	profile.Avatar = input.Avatar
	profile.MessageRetentionMinutes = retentionMinutes
	profile.ProfileVersion++
	profile.UpdatedAt = now
	if err := signProfile(s.nodePriv, &profile); err != nil {
		return ChannelSummary{}, err
	}
	head.OwnerVersion = profile.OwnerVersion
	head.ProfileVersion = profile.ProfileVersion
	head.LastSeq++
	head.UpdatedAt = now
	if err := signHead(s.nodePriv, &head); err != nil {
		return ChannelSummary{}, err
	}
	change := ChannelChange{
		ChannelID:      channelID,
		Seq:            head.LastSeq,
		ChangeType:     ChangeTypeProfile,
		ProfileVersion: ptrInt64(profile.ProfileVersion),
		CreatedAt:      now,
		ProviderPeerID: s.localPeer,
	}
	if err := s.store.CommitOwnedProfileChange(profile, head, change); err != nil {
		return ChannelSummary{}, err
	}
	s.runRetentionSweep(time.Unix(now, 0).UTC())
	s.scheduleOwnedProvide(channelID)
	s.publishChange(change)
	return s.store.GetChannelSummary(channelID)
}

func (s *Service) UpdateChannelProfileWithAvatar(channelID, name, bio string, messageRetentionMinutes int, fileName, mimeType string, data []byte) (ChannelSummary, error) {
	profile, err := s.store.GetChannelProfile(channelID)
	if err != nil {
		return ChannelSummary{}, err
	}
	avatar, err := s.buildPinnedAvatar(fileName, mimeType, data)
	if err != nil {
		return ChannelSummary{}, err
	}
	return s.UpdateChannelProfile(channelID, UpdateChannelProfileInput{
		Name:                    firstNonEmptyString(name, profile.Name),
		Bio:                     bio,
		Avatar:                  avatar,
		MessageRetentionMinutes: ptrInt(messageRetentionMinutes),
	})
}

func (s *Service) CreateChannelMessage(channelID string, input UpsertMessageInput) (ChannelMessage, error) {
	profile, err := s.store.GetChannelProfile(channelID)
	if err != nil {
		return ChannelMessage{}, err
	}
	head, err := s.store.GetChannelHead(channelID)
	if err != nil {
		return ChannelMessage{}, err
	}
	if profile.OwnerPeerID != s.localPeer {
		return ChannelMessage{}, errors.New("channel is not owned by local peer")
	}
	content := NormalizeMessageContent(input.Text, input.Files)
	if strings.TrimSpace(content.Text) == "" && len(content.Files) == 0 {
		return ChannelMessage{}, errors.New("message text or files are required")
	}
	now := time.Now().Unix()
	msg := ChannelMessage{
		ChannelID:     channelID,
		MessageID:     head.LastMessageID + 1,
		Version:       1,
		Seq:           head.LastSeq + 1,
		OwnerVersion:  profile.OwnerVersion,
		CreatorPeerID: s.localPeer,
		AuthorPeerID:  s.localPeer,
		CreatedAt:     now,
		UpdatedAt:     now,
		IsDeleted:     false,
		Content:       content,
	}
	msg.MessageType = DetermineMessageType(msg.Content, input.MessageType, false)
	if err := signMessage(s.nodePriv, &msg); err != nil {
		return ChannelMessage{}, err
	}
	head.LastMessageID = msg.MessageID
	head.LastSeq = msg.Seq
	head.ProfileVersion = profile.ProfileVersion
	head.UpdatedAt = now
	if err := signHead(s.nodePriv, &head); err != nil {
		return ChannelMessage{}, err
	}
	change := changeFromMessage(msg, s.localPeer)
	if err := s.store.CommitOwnedMessageChange(msg, head, change); err != nil {
		return ChannelMessage{}, err
	}
	s.scheduleOwnedProvide(channelID)
	s.publishChange(change)
	return msg, nil
}

func (s *Service) buildPinnedChatFile(fileName, mimeType string, data []byte) (File, error) {
	if s.ipfs == nil {
		return File{}, errors.New("ipfs not available for public channel file upload")
	}
	if len(data) == 0 {
		return File{}, errors.New("file is empty")
	}
	fileName = chat.NormalizeChatFileName(fileName)
	if fileName == "" {
		return File{}, errors.New("file_name is required")
	}
	mimeType = strings.TrimSpace(mimeType)
	fileCID, err := s.ipfs.PinChatFile(s.ctx, fileName, data)
	if err != nil {
		return File{}, err
	}
	if strings.TrimSpace(fileCID) == "" {
		return File{}, errors.New("ipfs pin returned empty cid")
	}
	sum := sha256.Sum256(data)
	return File{
		FileID:   fileCID,
		FileName: fileName,
		MIMEType: mimeType,
		Size:     int64(len(data)),
		SHA256:   hex.EncodeToString(sum[:]),
		BlobID:   fileCID,
		URL:      "/ipfs/" + fileCID + "/" + filepath.Base(fileName),
	}, nil
}

func (s *Service) CreateChannelFilesMessage(channelID, text string, uploads []UploadFileInput) (ChannelMessage, error) {
	if len(uploads) == 0 {
		return ChannelMessage{}, errors.New("files are required")
	}
	files := make([]File, 0, len(uploads))
	for _, item := range uploads {
		file, err := s.buildPinnedChatFile(item.FileName, item.MIMEType, item.Data)
		if err != nil {
			return ChannelMessage{}, err
		}
		files = append(files, file)
	}
	return s.CreateChannelMessage(channelID, UpsertMessageInput{
		MessageType: DetermineMessageType(MessageContent{Files: files}, "", false),
		Text:        text,
		Files:       files,
	})
}

func (s *Service) CreateChannelFileMessage(channelID, text, fileName, mimeType string, data []byte) (ChannelMessage, error) {
	return s.CreateChannelFilesMessage(channelID, text, []UploadFileInput{{
		FileName: fileName,
		MIMEType: mimeType,
		Data:     data,
	}})
}

func (s *Service) UpdateChannelMessage(ctx context.Context, channelID string, messageID int64, input UpsertMessageInput) (ChannelMessage, error) {
	if err := s.ensureMessageAvailable(ctx, channelID, messageID); err != nil {
		return ChannelMessage{}, err
	}
	profile, err := s.store.GetChannelProfile(channelID)
	if err != nil {
		return ChannelMessage{}, err
	}
	head, err := s.store.GetChannelHead(channelID)
	if err != nil {
		return ChannelMessage{}, err
	}
	current, err := s.store.GetChannelMessage(channelID, messageID)
	if err != nil {
		return ChannelMessage{}, err
	}
	if profile.OwnerPeerID != s.localPeer {
		return ChannelMessage{}, errors.New("channel is not owned by local peer")
	}
	if current.AuthorPeerID != s.localPeer {
		return ChannelMessage{}, errors.New("message author is not local owner")
	}
	if current.IsDeleted {
		return ChannelMessage{}, errors.New("message has been deleted")
	}
	content := NormalizeMessageContent(input.Text, input.Files)
	if strings.TrimSpace(content.Text) == "" && len(content.Files) == 0 {
		return ChannelMessage{}, errors.New("message text or files are required")
	}
	now := time.Now().Unix()
	current.Version++
	current.Seq = head.LastSeq + 1
	current.OwnerVersion = profile.OwnerVersion
	current.AuthorPeerID = s.localPeer
	current.UpdatedAt = now
	current.Content = content
	current.IsDeleted = false
	current.MessageType = DetermineMessageType(content, input.MessageType, false)
	if err := signMessage(s.nodePriv, &current); err != nil {
		return ChannelMessage{}, err
	}
	head.LastSeq = current.Seq
	head.ProfileVersion = profile.ProfileVersion
	head.UpdatedAt = now
	if err := signHead(s.nodePriv, &head); err != nil {
		return ChannelMessage{}, err
	}
	change := changeFromMessage(current, s.localPeer)
	if err := s.store.CommitOwnedMessageChange(current, head, change); err != nil {
		return ChannelMessage{}, err
	}
	s.scheduleOwnedProvide(channelID)
	s.publishChange(change)
	return current, nil
}

func (s *Service) DeleteChannelMessage(ctx context.Context, channelID string, messageID int64) (ChannelMessage, error) {
	if err := s.ensureMessageAvailable(ctx, channelID, messageID); err != nil {
		return ChannelMessage{}, err
	}
	profile, err := s.store.GetChannelProfile(channelID)
	if err != nil {
		return ChannelMessage{}, err
	}
	head, err := s.store.GetChannelHead(channelID)
	if err != nil {
		return ChannelMessage{}, err
	}
	current, err := s.store.GetChannelMessage(channelID, messageID)
	if err != nil {
		return ChannelMessage{}, err
	}
	if profile.OwnerPeerID != s.localPeer {
		return ChannelMessage{}, errors.New("channel is not owned by local peer")
	}
	if current.IsDeleted {
		return current, nil
	}
	now := time.Now().Unix()
	current.Version++
	current.Seq = head.LastSeq + 1
	current.OwnerVersion = profile.OwnerVersion
	current.AuthorPeerID = s.localPeer
	current.UpdatedAt = now
	current.IsDeleted = true
	current.Content = MessageContent{}
	current.MessageType = MessageTypeDeleted
	if err := signMessage(s.nodePriv, &current); err != nil {
		return ChannelMessage{}, err
	}
	head.LastSeq = current.Seq
	head.ProfileVersion = profile.ProfileVersion
	head.UpdatedAt = now
	if err := signHead(s.nodePriv, &head); err != nil {
		return ChannelMessage{}, err
	}
	change := changeFromMessage(current, s.localPeer)
	if err := s.store.CommitOwnedMessageChange(current, head, change); err != nil {
		return ChannelMessage{}, err
	}
	s.scheduleOwnedProvide(channelID)
	s.publishChange(change)
	return current, nil
}

func (s *Service) SubscribeChannel(ctx context.Context, channelID string, seedPeerIDs []string, lastSeenSeq int64) (SubscribeResult, error) {
	if err := ValidateChannelID(channelID); err != nil {
		return SubscribeResult{}, err
	}
	now := time.Now().Unix()
	if err := s.store.EnsureSubscribedChannel(channelID, lastSeenSeq, now); err != nil {
		return SubscribeResult{}, err
	}
	for _, peerID := range seedPeerIDs {
		_ = s.upsertProviderIfKnown(channelID, peerID, "seed", now)
	}
	if err := s.subscribeTopic(channelID); err != nil {
		return SubscribeResult{}, err
	}
	log.Printf("[publicchannel] subscribe join topic channel=%s seeds=%s", channelID, formatPeerIDsForLog(seedPeerIDs))
	// 订阅 topic 后主动连接 seed/provider，确保新节点尽快进入 pubsub mesh。
	s.proactivelyJoinTopicMesh(ctx, channelID, seedPeerIDs)
	remoteProfile, remoteHead, remoteMessages, providers, err := s.fetchInitialSnapshot(ctx, channelID, seedPeerIDs)
	if err != nil {
		log.Printf("[publicchannel] initial subscribe snapshot %s: %v", channelID, err)
		s.bootstrapSubscriptionAsync(channelID, append([]string(nil), seedPeerIDs...), lastSeenSeq)
		return s.localSubscribeResult(channelID)
	}
	if err := s.applyInitialSnapshot(ctx, channelID, remoteProfile, remoteHead, remoteMessages, seedPeerIDs, lastSeenSeq, now); err != nil {
		return SubscribeResult{}, err
	}
	filteredMessages, err := s.filterExpiredMessages(remoteProfile, remoteMessages)
	if err != nil {
		return SubscribeResult{}, err
	}
	return SubscribeResult{
		Profile:   remoteProfile,
		Head:      remoteHead,
		Messages:  filteredMessages,
		Providers: providers,
	}, nil
}

func (s *Service) SubscribeChannelAsync(channelID string, seedPeerIDs []string, lastSeenSeq int64) (SubscribeResult, error) {
	if err := ValidateChannelID(channelID); err != nil {
		return SubscribeResult{}, err
	}
	now := time.Now().Unix()
	if err := s.store.EnsureSubscribedChannel(channelID, lastSeenSeq, now); err != nil {
		return SubscribeResult{}, err
	}
	for _, peerID := range seedPeerIDs {
		_ = s.upsertProviderIfKnown(channelID, peerID, "seed", now)
	}
	if err := s.subscribeTopic(channelID); err != nil {
		return SubscribeResult{}, err
	}
	log.Printf("[publicchannel] subscribe join topic channel=%s seeds=%s", channelID, formatPeerIDsForLog(seedPeerIDs))
	// 异步订阅同样需要先尝试连上种子节点，否则只 Join topic 不足以进入 gossip mesh。
	joinCtx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	safe.Go("publicchannel.joinmesh."+channelID, func() {
		defer cancel()
		s.proactivelyJoinTopicMesh(joinCtx, channelID, seedPeerIDs)
	})
	s.bootstrapSubscriptionAsync(channelID, append([]string(nil), seedPeerIDs...), lastSeenSeq)
	return s.localSubscribeResult(channelID)
}

func (s *Service) applyInitialSnapshot(ctx context.Context, channelID string, remoteProfile ChannelProfile, remoteHead ChannelHead, remoteMessages []ChannelMessage, seedPeerIDs []string, lastSeenSeq, now int64) error {
	if err := s.store.ApplyProfile(remoteProfile); err != nil {
		return err
	}
	s.runRetentionSweep(time.Unix(now, 0).UTC())
	for _, peerID := range seedPeerIDs {
		_ = s.upsertProviderIfKnown(channelID, peerID, "seed", now)
	}
	if err := s.store.ApplyHead(remoteHead); err != nil {
		return err
	}
	appliedMessages, err := s.filterExpiredMessages(remoteProfile, remoteMessages)
	if err != nil {
		return err
	}
	for _, item := range remoteMessages {
		if err := s.applyVerifiedMessage(item); err != nil {
			return err
		}
	}
	if err := s.store.UpdateLoadedRange(channelID, appliedMessages, now); err != nil {
		return err
	}
	s.ensureProvided(channelID)
	resumeFrom := clampInt64(lastSeenSeq, 0, remoteHead.LastSeq)
	if resumeFrom <= 0 {
		if err := s.store.UpdateSyncState(channelID, remoteHead.LastSeq, remoteHead.LastSeq, true, now); err != nil {
			return err
		}
		return nil
	}
	if err := s.store.UpdateSyncState(channelID, resumeFrom, resumeFrom, true, now); err != nil {
		return err
	}
	if err := s.syncAfter(ctx, channelID, resumeFrom); err != nil {
		log.Printf("[publicchannel] post-subscribe resume sync %s from %d: %v", channelID, resumeFrom, err)
	}
	return nil
}

func (s *Service) bootstrapSubscriptionAsync(channelID string, seedPeerIDs []string, lastSeenSeq int64) {
	safe.Go("publicchannel.subscribe.bootstrap."+channelID, func() {
		maxAttempts := len(s.bootstrapRetryBackoffs) + 1
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if attempt > 1 {
				delay := s.bootstrapRetryBackoffs[attempt-2]
				timer := time.NewTimer(delay)
				select {
				case <-s.ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			fetchCtx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
			now := s.nowUnix()
			remoteProfile, remoteHead, remoteMessages, _, err := s.fetchInitialSnapshot(fetchCtx, channelID, seedPeerIDs)
			cancel()
			if err != nil {
				log.Printf("[publicchannel] async subscribe snapshot channel=%s attempt=%d/%d err=%v", channelID, attempt, maxAttempts, err)
				continue
			}
			applyCtx, applyCancel := context.WithTimeout(s.ctx, 20*time.Second)
			err = s.applyInitialSnapshot(applyCtx, channelID, remoteProfile, remoteHead, remoteMessages, seedPeerIDs, lastSeenSeq, now)
			applyCancel()
			if err != nil {
				log.Printf("[publicchannel] async subscribe apply channel=%s attempt=%d/%d err=%v", channelID, attempt, maxAttempts, err)
				continue
			}
			return
		}
	})
}

func (s *Service) localSubscribeResult(channelID string) (SubscribeResult, error) {
	summary, err := s.store.GetChannelSummary(channelID)
	if err != nil {
		return SubscribeResult{}, err
	}
	messages, err := s.store.GetChannelMessages(channelID, 0, DefaultPageLimit)
	if err == sql.ErrNoRows {
		messages = nil
		err = nil
	}
	if err != nil {
		return SubscribeResult{}, err
	}
	providers, err := s.store.ListProviders(channelID)
	if err == sql.ErrNoRows {
		providers = nil
		err = nil
	}
	if err != nil {
		return SubscribeResult{}, err
	}
	return SubscribeResult{
		Profile:   summary.Profile,
		Head:      summary.Head,
		Messages:  messages,
		Providers: providers,
	}, nil
}

func (s *Service) UnsubscribeChannel(channelID string) error {
	now := time.Now().Unix()
	state, err := s.store.GetChannelSyncState(channelID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		if err := s.store.UpdateSyncState(channelID, state.LastSeenSeq, state.LastSyncedSeq, false, now); err != nil {
			return err
		}
	}
	s.subMu.Lock()
	item := s.subs[channelID]
	delete(s.subs, channelID)
	s.subMu.Unlock()
	if item != nil {
		if item.cancel != nil {
			item.cancel()
		}
		if item.sub != nil {
			item.sub.Cancel()
		}
		if item.topic != nil {
			_ = item.topic.Close()
		}
	}
	return nil
}

func (s *Service) LoadMessagesFromProviders(ctx context.Context, channelID string, beforeMessageID int64, limit int) ([]ChannelMessage, error) {
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	localItems, err := s.store.GetChannelMessages(channelID, beforeMessageID, limit)
	if err != nil {
		return nil, err
	}
	if len(localItems) >= limit {
		return localItems, nil
	}
	peers := s.providerPeerIDs(ctx, channelID)
	if len(peers) == 0 {
		return localItems, nil
	}
	profile, err := s.ensureChannelProfileAvailable(ctx, channelID)
	if err != nil {
		return nil, err
	}
	body := struct {
		ChannelID       string `json:"channel_id"`
		BeforeMessageID int64  `json:"before_message_id,omitempty"`
		Limit           int    `json:"limit"`
	}{
		ChannelID:       channelID,
		BeforeMessageID: beforeMessageID,
		Limit:           limit,
	}
	var resp GetMessagesResponse
	var lastErr error
	bestItems := localItems
	for _, peerID := range peers {
		if err := s.rpcCall(ctx, peerID, "get_channel_messages", body, &resp); err != nil {
			s.recordProviderSyncFailure(channelID, peerID)
			lastErr = err
			continue
		}
		appliedItems := make([]ChannelMessage, 0, len(resp.Items))
		for _, item := range resp.Items {
			if err := s.applyVerifiedMessageWithProfile(profile, item); err != nil {
				return nil, err
			}
			expired, err := s.isMessageExpired(profile, item)
			if err != nil {
				return nil, err
			}
			if !expired {
				appliedItems = append(appliedItems, item)
			}
		}
		_ = s.store.UpdateLoadedRange(channelID, appliedItems, time.Now().Unix())
		items, err := s.store.GetChannelMessages(channelID, beforeMessageID, limit)
		if err != nil {
			return nil, err
		}
		if len(items) > len(bestItems) {
			bestItems = items
		}
		// 副本节点可能只缓存了部分历史；单个 provider 即便成功响应，也不能把空页或半页
		// 直接当成“没有更早消息”，需要继续尝试后续 provider 直到凑满当前页。
		if len(items) >= limit {
			s.recordProviderSyncSuccess(channelID, peerID)
			return items, nil
		}
		s.recordProviderSyncSuccess(channelID, peerID)
	}
	if len(bestItems) > 0 {
		return bestItems, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return localItems, nil
}

func (s *Service) SyncChannel(ctx context.Context, channelID string) error {
	state, err := s.store.GetChannelSyncState(channelID)
	if err != nil {
		return err
	}
	return s.syncAfter(ctx, channelID, state.LastSyncedSeq)
}

func (s *Service) ensureMessageAvailable(ctx context.Context, channelID string, messageID int64) error {
	_, err := s.store.GetChannelMessage(channelID, messageID)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	profile, err := s.ensureChannelProfileAvailable(ctx, channelID)
	if err != nil {
		return err
	}
	peers := s.providerPeerIDs(ctx, channelID)
	body := struct {
		ChannelID string `json:"channel_id"`
		MessageID int64  `json:"message_id"`
	}{ChannelID: channelID, MessageID: messageID}
	var msg ChannelMessage
	for _, peerID := range peers {
		if err := s.rpcCall(ctx, peerID, "get_channel_message", body, &msg); err != nil {
			s.recordProviderSyncFailure(channelID, peerID)
			continue
		}
		if err := s.applyVerifiedMessageWithProfile(profile, msg); err != nil {
			return err
		}
		s.recordProviderSyncSuccess(channelID, peerID)
		_, err := s.store.GetChannelMessage(channelID, messageID)
		return err
	}
	return sql.ErrNoRows
}

func (s *Service) fetchInitialSnapshot(ctx context.Context, channelID string, seedPeerIDs []string) (ChannelProfile, ChannelHead, []ChannelMessage, []ChannelProvider, error) {
	seen := make(map[string]struct{})
	peers := make([]string, 0, len(seedPeerIDs)+4)
	for _, peerID := range seedPeerIDs {
		peerID = strings.TrimSpace(peerID)
		if peerID == "" {
			continue
		}
		if _, ok := seen[peerID]; ok {
			continue
		}
		seen[peerID] = struct{}{}
		peers = append(peers, peerID)
	}
	for _, peerID := range s.providerPeerIDs(ctx, channelID) {
		if _, ok := seen[peerID]; ok {
			continue
		}
		seen[peerID] = struct{}{}
		peers = append(peers, peerID)
	}
	if len(peers) == 0 {
		return ChannelProfile{}, ChannelHead{}, nil, nil, errors.New("no providers available for channel")
	}
	var (
		profile ChannelProfile
		head    ChannelHead
		msgs    GetMessagesResponse
		lastErr error
	)
	bodyProfile := struct {
		ChannelID string `json:"channel_id"`
	}{ChannelID: channelID}
	bodyMessages := struct {
		ChannelID string `json:"channel_id"`
		Limit     int    `json:"limit"`
	}{ChannelID: channelID, Limit: DefaultPageLimit}
	for _, peerID := range peers {
		if err := s.rpcCall(ctx, peerID, "get_channel_profile", bodyProfile, &profile); err != nil {
			s.recordProviderSyncFailure(channelID, peerID)
			lastErr = err
			continue
		}
		if err := s.rpcCall(ctx, peerID, "get_channel_head", bodyProfile, &head); err != nil {
			s.recordProviderSyncFailure(channelID, peerID)
			lastErr = err
			continue
		}
		if err := s.rpcCall(ctx, peerID, "get_channel_messages", bodyMessages, &msgs); err != nil {
			s.recordProviderSyncFailure(channelID, peerID)
			lastErr = err
			continue
		}
		if err := verifyProfile(profile); err != nil {
			return ChannelProfile{}, ChannelHead{}, nil, nil, err
		}
		if err := verifyHead(head); err != nil {
			return ChannelProfile{}, ChannelHead{}, nil, nil, err
		}
		for _, item := range msgs.Items {
			if err := s.verifyMessageAgainstProfile(profile, item); err != nil {
				return ChannelProfile{}, ChannelHead{}, nil, nil, err
			}
		}
		s.recordProviderSyncSuccess(channelID, peerID)
		providers, _ := s.store.ListProviders(channelID)
		return profile, head, msgs.Items, providers, nil
	}
	if lastErr == nil {
		lastErr = errors.New("failed to fetch channel snapshot")
	}
	return ChannelProfile{}, ChannelHead{}, nil, nil, lastErr
}

func (s *Service) syncAfter(ctx context.Context, channelID string, afterSeq int64) error {
	peers := s.providerPeerIDs(ctx, channelID)
	if len(peers) == 0 {
		return nil
	}
	var (
		lastErr    error
		hadSuccess bool
	)
	for _, peerID := range peers {
		advanced, err := s.syncAfterWithPeer(ctx, peerID, channelID, afterSeq)
		if err != nil {
			s.recordProviderSyncFailure(channelID, peerID)
			lastErr = err
			continue
		}
		s.recordProviderSyncSuccess(channelID, peerID)
		hadSuccess = true
		// provider 成功响应并不代表已经补到新 change；遇到滞后副本时要继续尝试后续 provider。
		if advanced {
			return nil
		}
	}
	if hadSuccess {
		return nil
	}
	return lastErr
}

func (s *Service) syncAfterWithPeer(ctx context.Context, sourcePeerID, channelID string, afterSeq int64) (bool, error) {
	nextAfter := afterSeq
	currentLastSeq := afterSeq
	for {
		body := struct {
			ChannelID string `json:"channel_id"`
			AfterSeq  int64  `json:"after_seq"`
			Limit     int    `json:"limit"`
		}{ChannelID: channelID, AfterSeq: nextAfter, Limit: DefaultChangesLimit}
		var resp GetChangesResponse
		if err := s.rpcCall(ctx, sourcePeerID, "get_channel_changes", body, &resp); err != nil {
			return false, err
		}
		if err := s.applyChanges(ctx, sourcePeerID, channelID, resp); err != nil {
			return false, err
		}
		if resp.CurrentLastSeq > currentLastSeq {
			currentLastSeq = resp.CurrentLastSeq
		}
		if !resp.HasMore || resp.NextAfterSeq <= nextAfter {
			break
		}
		nextAfter = resp.NextAfterSeq
	}
	now := time.Now().Unix()
	// 只有整段 change 分页补齐后，才能推进 last_seen_seq / last_synced_seq，
	// 否则会把尚未拉到的中间页永久跳过去。
	if err := s.store.UpdateSyncState(channelID, currentLastSeq, currentLastSeq, true, now); err != nil {
		return false, err
	}
	return currentLastSeq > afterSeq, nil
}

func (s *Service) applyChanges(ctx context.Context, sourcePeerID, channelID string, resp GetChangesResponse) error {
	profile, err := s.store.GetChannelProfile(channelID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	unreadDelta := 0
	for _, change := range resp.Items {
		switch change.ChangeType {
		case ChangeTypeProfile:
			var remoteProfile ChannelProfile
			if err := s.rpcCall(ctx, sourcePeerID, "get_channel_profile", struct {
				ChannelID string `json:"channel_id"`
			}{ChannelID: channelID}, &remoteProfile); err != nil {
				return err
			}
			if err := verifyProfile(remoteProfile); err != nil {
				return err
			}
			if err := s.store.ApplyProfile(remoteProfile); err != nil {
				return err
			}
			s.runRetentionSweep(time.Now().UTC())
			profile = remoteProfile
		case ChangeTypeMessage:
			if change.MessageID == nil {
				continue
			}
			existingMissing := false
			if _, err := s.store.GetChannelMessage(channelID, *change.MessageID); err == sql.ErrNoRows {
				existingMissing = true
			} else if err != nil {
				return err
			}
			var remoteMsg ChannelMessage
			if err := s.rpcCall(ctx, sourcePeerID, "get_channel_message", struct {
				ChannelID string `json:"channel_id"`
				MessageID int64  `json:"message_id"`
			}{ChannelID: channelID, MessageID: *change.MessageID}, &remoteMsg); err != nil {
				return err
			}
			if profile.ChannelID == "" {
				profile, err = s.store.GetChannelProfile(channelID)
				if err != nil {
					return err
				}
			}
			if err := s.verifyMessageAgainstProfile(profile, remoteMsg); err != nil {
				return err
			}
			expired, err := s.isMessageExpired(profile, remoteMsg)
			if err != nil {
				return err
			}
			if expired {
				continue
			}
			if err := s.store.ApplyMessage(remoteMsg); err != nil {
				return err
			}
			if existingMissing && !remoteMsg.IsDeleted && profile.OwnerPeerID != s.localPeer {
				unreadDelta++
			}
		}
		if err := s.store.RecordChange(change); err != nil {
			return err
		}
	}
	if unreadDelta > 0 {
		if err := s.store.IncrementChannelUnreadCount(channelID, unreadDelta, time.Now().Unix()); err != nil {
			return err
		}
	}
	var head ChannelHead
	if err := s.rpcCall(ctx, sourcePeerID, "get_channel_head", struct {
		ChannelID string `json:"channel_id"`
	}{ChannelID: channelID}, &head); err == nil {
		if err := verifyHead(head); err == nil {
			_ = s.store.ApplyHead(head)
		}
	}
	return nil
}

func (s *Service) applyVerifiedMessage(message ChannelMessage) error {
	profile, err := s.store.GetChannelProfile(message.ChannelID)
	if err != nil {
		return err
	}
	if isPlaceholderChannelProfile(profile) {
		return errors.New("channel profile is not available")
	}
	return s.applyVerifiedMessageWithProfile(profile, message)
}

func (s *Service) applyVerifiedMessageWithProfile(profile ChannelProfile, message ChannelMessage) error {
	if err := s.verifyMessageAgainstProfile(profile, message); err != nil {
		return err
	}
	expired, err := s.isMessageExpired(profile, message)
	if err != nil {
		return err
	}
	if expired {
		return nil
	}
	return s.store.ApplyMessage(message)
}

func (s *Service) ensureChannelProfileAvailable(ctx context.Context, channelID string) (ChannelProfile, error) {
	profile, err := s.store.GetChannelProfile(channelID)
	switch {
	case err == nil && !isPlaceholderChannelProfile(profile):
		return profile, nil
	case err != nil && err != sql.ErrNoRows:
		return ChannelProfile{}, err
	}
	peers := s.providerPeerIDs(ctx, channelID)
	if len(peers) == 0 {
		if err == nil {
			return ChannelProfile{}, errors.New("channel profile is placeholder and no providers available")
		}
		return ChannelProfile{}, err
	}
	body := struct {
		ChannelID string `json:"channel_id"`
	}{ChannelID: channelID}
	var lastErr error
	for _, peerID := range peers {
		var remoteProfile ChannelProfile
		if rpcErr := s.rpcCall(ctx, peerID, "get_channel_profile", body, &remoteProfile); rpcErr != nil {
			s.recordProviderSyncFailure(channelID, peerID)
			lastErr = rpcErr
			continue
		}
		if err := verifyProfile(remoteProfile); err != nil {
			return ChannelProfile{}, err
		}
		var remoteHead ChannelHead
		if rpcErr := s.rpcCall(ctx, peerID, "get_channel_head", body, &remoteHead); rpcErr != nil {
			s.recordProviderSyncFailure(channelID, peerID)
			lastErr = rpcErr
			continue
		}
		if err := verifyHead(remoteHead); err != nil {
			return ChannelProfile{}, err
		}
		if remoteHead.OwnerPeerID != remoteProfile.OwnerPeerID {
			return ChannelProfile{}, errors.New("channel head owner mismatch")
		}
		if err := s.store.ApplyProfile(remoteProfile); err != nil {
			return ChannelProfile{}, err
		}
		if err := s.store.ApplyHead(remoteHead); err != nil {
			return ChannelProfile{}, err
		}
		s.recordProviderSyncSuccess(channelID, peerID)
		return remoteProfile, nil
	}
	if lastErr == nil {
		lastErr = errors.New("failed to fetch channel profile")
	}
	return ChannelProfile{}, lastErr
}

func isPlaceholderChannelProfile(profile ChannelProfile) bool {
	return strings.TrimSpace(profile.OwnerPeerID) == "" ||
		profile.OwnerVersion == 0 ||
		strings.TrimSpace(profile.Signature) == ""
}

func (s *Service) verifyMessageAgainstProfile(profile ChannelProfile, message ChannelMessage) error {
	if message.AuthorPeerID != profile.OwnerPeerID {
		return errors.New("author_peer_id must equal current owner_peer_id")
	}
	if message.OwnerVersion != profile.OwnerVersion {
		return errors.New("message owner_version mismatch")
	}
	return verifyMessage(profile.OwnerPeerID, message)
}

func (s *Service) runRetentionLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			s.runRetentionSweep(now.UTC())
		}
	}
}

func (s *Service) runRetentionSweep(now time.Time) {
	if err := s.store.CleanupExpiredMessages(now); err != nil {
		log.Printf("[publicchannel] retention cleanup failed: %v", err)
	}
}

func (s *Service) isMessageExpired(profile ChannelProfile, message ChannelMessage) (bool, error) {
	minutes := NormalizeRetentionMinutes(profile.MessageRetentionMinutes)
	if err := ValidateRetentionMinutes(minutes); err != nil {
		return false, err
	}
	if minutes == 0 {
		return false, nil
	}
	messageTime := message.CreatedAt
	if messageTime <= 0 {
		return false, nil
	}
	cutoff := time.Now().Unix() - int64(minutes)*60
	return messageTime <= cutoff, nil
}

func (s *Service) filterExpiredMessages(profile ChannelProfile, messages []ChannelMessage) ([]ChannelMessage, error) {
	filtered := make([]ChannelMessage, 0, len(messages))
	for _, item := range messages {
		expired, err := s.isMessageExpired(profile, item)
		if err != nil {
			return nil, err
		}
		if !expired {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *Service) ensureTopic(channelID string, subscribe bool) (*pubsub.Topic, error) {
	if s.pubsub == nil {
		return nil, errors.New("pubsub not configured")
	}
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if existing, ok := s.subs[channelID]; ok && existing.topic != nil {
		return existing.topic, nil
	}
	topic, err := s.pubsub.Join(TopicForChannel(channelID))
	if err != nil {
		return nil, err
	}
	if !subscribe {
		s.subs[channelID] = &channelSubscription{topic: topic}
		return topic, nil
	}
	return topic, nil
}

func (s *Service) subscribeTopic(channelID string) error {
	topic, err := s.ensureTopic(channelID, true)
	if err != nil {
		return err
	}
	s.subMu.Lock()
	if existing, ok := s.subs[channelID]; ok && existing.sub != nil {
		s.subMu.Unlock()
		return nil
	}
	sub, err := topic.Subscribe()
	if err != nil {
		s.subMu.Unlock()
		return err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.subs[channelID] = &channelSubscription{topic: topic, sub: sub, cancel: cancel}
	s.subMu.Unlock()
	safe.Go("publicchannel.topic."+channelID, func() { s.runSubscriptionLoop(ctx, channelID, sub) })
	return nil
}

func (s *Service) runSubscriptionLoop(ctx context.Context, channelID string, sub *pubsub.Subscription) {
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			return
		}
		if msg == nil || msg.ReceivedFrom == s.host.ID() {
			continue
		}
		var change ChannelChange
		if err := jsonUnmarshal(msg.Data, &change); err != nil {
			continue
		}
		change.ProviderPeerID = msg.ReceivedFrom.String()
		s.upsertProviderIfKnown(channelID, change.ProviderPeerID, "pubsub", time.Now().Unix())
		state, err := s.store.GetChannelSyncState(channelID)
		if err == nil && change.Seq <= state.LastSeenSeq {
			continue
		}
		syncCtx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
		_ = s.syncAfter(syncCtx, channelID, state.LastSyncedSeq)
		cancel()
	}
}

func (s *Service) publishChange(change ChannelChange) {
	if s.pubsub == nil {
		return
	}
	change.ProviderPeerID = s.localPeer
	raw, err := jsonMarshal(change)
	if err != nil {
		return
	}
	topic, err := s.ensureTopic(change.ChannelID, false)
	if err != nil {
		log.Printf("[publicchannel] join topic for publish %s: %v", change.ChannelID, err)
		return
	}
	if err := topic.Publish(s.ctx, raw); err != nil {
		log.Printf("[publicchannel] publish change %s: %v", change.ChannelID, err)
	}
}

func (s *Service) bootstrapOwnedChannelAsync(channelID string) {
	safe.Go("publicchannel.bootstrap."+channelID, func() {
		s.scheduleOwnedProvide(channelID)
		if _, err := s.ensureTopic(channelID, false); err != nil {
			log.Printf("[publicchannel] ensure topic %s: %v", channelID, err)
		}
	})
}

func (s *Service) restoreOwnedChannelsForProvide() {
	if _, ok := s.routing.(contentRouter); !ok || s.store == nil {
		return
	}
	channelIDs, err := s.store.ListOwnedChannelIDs(s.localPeer)
	if err != nil {
		log.Printf("[publicchannel] restore owned channels for provide: %v", err)
		return
	}
	for _, channelID := range channelIDs {
		s.scheduleOwnedProvide(channelID)
	}
}

func (s *Service) scheduleOwnedProvide(channelID string) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return
	}
	if _, ok := s.routing.(contentRouter); !ok {
		return
	}
	now := s.now()
	s.provideMu.Lock()
	state, ok := s.provideStates[channelID]
	if !ok {
		state = &provideState{channelID: channelID}
		s.provideStates[channelID] = state
	}
	if state.nextProvideTime.IsZero() || state.nextProvideTime.After(now) {
		state.nextProvideTime = now
		state.nextProvideAt = now.Unix()
	}
	s.provideMu.Unlock()
	s.wakeProvideLoop()
}

func (s *Service) wakeProvideLoop() {
	select {
	case s.provideWakeCh <- struct{}{}:
	default:
	}
}

func (s *Service) runProvideLoop() {
	ticker := time.NewTicker(s.provideLoopInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.startDueProvides()
		case <-s.provideWakeCh:
			s.startDueProvides()
		}
	}
}

func (s *Service) startDueProvides() {
	now := s.now()
	dueChannelIDs := make([]string, 0)
	s.provideMu.Lock()
	for channelID, state := range s.provideStates {
		if state.inFlight {
			continue
		}
		if state.nextProvideTime.IsZero() || state.nextProvideTime.After(now) {
			continue
		}
		state.inFlight = true
		dueChannelIDs = append(dueChannelIDs, channelID)
	}
	s.provideMu.Unlock()
	for _, channelID := range dueChannelIDs {
		channelID := channelID
		safe.Go("publicchannel.reprovide."+channelID, func() { s.runProvideAttempt(channelID) })
	}
}

func (s *Service) runProvideAttempt(channelID string) {
	err := s.provideChannel(channelID)
	now := s.now()
	s.provideMu.Lock()
	state, ok := s.provideStates[channelID]
	if !ok {
		state = &provideState{channelID: channelID}
		s.provideStates[channelID] = state
	}
	state.inFlight = false
	previousLastProvideAt := state.lastProvideAt
	previousRetryCount := state.retryCount
	if err != nil {
		state.lastProvideErrAt = now.Unix()
		state.retryCount++
		delay := s.provideRetryDelay(state.retryCount)
		nextTime := now.Add(delay)
		state.nextProvideTime = nextTime
		state.nextProvideAt = nextTime.Unix()
		retryCount := state.retryCount
		nextProvideAt := state.nextProvideAt
		s.provideMu.Unlock()
		log.Printf("[publicchannel] provide retry channel=%s retry_count=%d next_retry_at=%d err=%v", channelID, retryCount, nextProvideAt, err)
		return
	}
	state.lastProvideAt = now.Unix()
	state.retryCount = 0
	nextTime := now.Add(s.provideSuccessInterval)
	state.nextProvideTime = nextTime
	state.nextProvideAt = nextTime.Unix()
	state.lastProvideErrAt = 0
	lastProvideAt := state.lastProvideAt
	shouldLogSuccess := previousLastProvideAt == 0 || previousRetryCount > 0 || lastProvideAt-previousLastProvideAt >= int64(s.provideSuccessLogMinGap/time.Second)
	nextProvideAt := state.nextProvideAt
	s.provideMu.Unlock()
	if shouldLogSuccess {
		log.Printf("[publicchannel] provide ok channel=%s next_provide_at=%d", channelID, nextProvideAt)
	}
}

func (s *Service) provideRetryDelay(retryCount int) time.Duration {
	if len(s.provideRetryBackoffs) == 0 {
		return time.Minute
	}
	if retryCount <= 0 {
		return s.provideRetryBackoffs[0]
	}
	idx := retryCount - 1
	if idx >= len(s.provideRetryBackoffs) {
		idx = len(s.provideRetryBackoffs) - 1
	}
	return s.provideRetryBackoffs[idx]
}

func (s *Service) ensureProvided(channelID string) {
	if err := s.provideChannel(channelID); err != nil {
		log.Printf("[publicchannel] provide %s: %v", channelID, err)
	}
}

func (s *Service) provideChannel(channelID string) error {
	cr, ok := s.routing.(contentRouter)
	if !ok {
		return nil
	}
	key, err := providerCID(channelID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.provideTimeout)
	defer cancel()
	if err := cr.Provide(ctx, key, true); err != nil {
		return err
	}
	_ = s.upsertProviderIfKnown(channelID, s.localPeer, "dht", s.nowUnix())
	return nil
}

func (s *Service) providerPeerIDs(ctx context.Context, channelID string) []string {
	seen := map[string]struct{}{}
	var out []string
	topicPeers := s.topicPeerIDs(channelID)
	log.Printf("[publicchannel] pubsub topic peers channel=%s peers=%s", channelID, formatPeerIDsForLog(topicPeers))
	// 已经在当前 topic mesh 里的邻居通常是最容易连通的候选，优先尝试它们。
	for _, peerID := range topicPeers {
		if _, ok := seen[peerID]; ok {
			continue
		}
		seen[peerID] = struct{}{}
		out = append(out, peerID)
		_ = s.upsertProviderIfKnown(channelID, peerID, "pubsub_mesh", s.nowUnix())
	}
	providers, _ := s.store.ListProviders(channelID)
	for _, item := range providers {
		if item.PeerID == "" {
			continue
		}
		if _, ok := seen[item.PeerID]; ok {
			continue
		}
		seen[item.PeerID] = struct{}{}
		out = append(out, item.PeerID)
	}
	if cr, ok := s.routing.(contentRouter); ok {
		key, err := providerCID(channelID)
		if err == nil {
			// DHT provider 广告收敛偏慢，这里给稍长一点的窗口，减少“频道明明存在但还没找到”的概率。
			findCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			for info := range cr.FindProvidersAsync(findCtx, key, DefaultProviderFind) {
				if info.ID == "" {
					continue
				}
				s.host.Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)
				peerID := info.ID.String()
				if _, ok := seen[peerID]; ok {
					continue
				}
				seen[peerID] = struct{}{}
				out = append(out, peerID)
				_ = s.upsertProviderIfKnown(channelID, peerID, "dht", time.Now().Unix())
			}
		}
	}
	log.Printf("[publicchannel] sync provider candidates channel=%s peers=%s", channelID, formatPeerIDsForLog(out))
	return out
}

func (s *Service) topicPeerIDs(channelID string) []string {
	s.subMu.Lock()
	item := s.subs[channelID]
	s.subMu.Unlock()
	if item == nil || item.topic == nil {
		return nil
	}
	peers := item.topic.ListPeers()
	if len(peers) == 0 {
		return nil
	}
	out := make([]string, 0, len(peers))
	for _, pid := range peers {
		peerID := strings.TrimSpace(pid.String())
		if peerID == "" {
			continue
		}
		out = append(out, peerID)
	}
	return out
}

func (s *Service) proactivelyJoinTopicMesh(ctx context.Context, channelID string, seedPeerIDs []string) {
	for _, peerID := range s.mergeChannelPeerIDs(channelID, seedPeerIDs) {
		log.Printf("[publicchannel] join mesh connect peer channel=%s peer=%s", channelID, peerID)
		if err := s.connectPeer(ctx, peerID); err != nil {
			log.Printf("[publicchannel] join mesh channel=%s peer=%s err=%v", channelID, peerID, err)
		}
	}
}

func (s *Service) mergeChannelPeerIDs(channelID string, seedPeerIDs []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(seedPeerIDs)+4)
	appendPeer := func(peerID string) {
		peerID = strings.TrimSpace(peerID)
		if peerID == "" || peerID == s.localPeer {
			return
		}
		if _, ok := seen[peerID]; ok {
			return
		}
		seen[peerID] = struct{}{}
		out = append(out, peerID)
	}
	for _, peerID := range seedPeerIDs {
		appendPeer(peerID)
	}
	providers, _ := s.store.ListProviders(channelID)
	for _, item := range providers {
		appendPeer(item.PeerID)
	}
	return out
}

func (s *Service) connectPeer(ctx context.Context, peerID string) error {
	pid, err := peer.Decode(strings.TrimSpace(peerID))
	if err != nil {
		return err
	}
	if pid == s.host.ID() {
		return nil
	}
	if s.host.Network().Connectedness(pid) == network.Connected {
		return nil
	}
	if s.routing != nil && len(s.host.Peerstore().Addrs(pid)) == 0 {
		findCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		info, findErr := s.routing.FindPeer(findCtx, pid)
		cancel()
		if findErr == nil && len(info.Addrs) > 0 {
			s.host.Peerstore().AddAddrs(pid, info.Addrs, time.Hour)
		}
	}
	connectCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return s.host.Connect(connectCtx, peer.AddrInfo{ID: pid, Addrs: s.host.Peerstore().Addrs(pid)})
}

func (s *Service) upsertProviderIfKnown(channelID, peerID, source string, now int64) error {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return nil
	}
	if _, err := s.store.GetChannelProfile(channelID); err != nil && err != sql.ErrNoRows {
		return err
	}
	if err := s.store.UpsertProvider(channelID, peerID, source, now); err == sql.ErrNoRows {
		return nil
	} else {
		return err
	}
}

func providerCID(channelID string) (cid.Cid, error) {
	sum := sha256.Sum256([]byte("meshchat/public-channel/provider/" + channelID))
	hash, err := multihash.Encode(sum[:], multihash.SHA2_256)
	if err != nil {
		return cid.Undef, err
	}
	return cid.NewCidV1(cid.Raw, hash), nil
}

func changeFromMessage(message ChannelMessage, providerPeerID string) ChannelChange {
	return ChannelChange{
		ChannelID:      message.ChannelID,
		Seq:            message.Seq,
		ChangeType:     ChangeTypeMessage,
		MessageID:      ptrInt64(message.MessageID),
		Version:        ptrInt64(message.Version),
		IsDeleted:      ptrBool(message.IsDeleted),
		CreatedAt:      message.UpdatedAt,
		ProviderPeerID: providerPeerID,
	}
}

func ptrInt64(v int64) *int64 { return &v }

func ptrBool(v bool) *bool { return &v }

func ptrInt(v int) *int { return &v }

func clampInt64(v, minV, maxV int64) int64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func formatPeerIDsForLog(peerIDs []string) string {
	if len(peerIDs) == 0 {
		return "[]"
	}
	items := make([]string, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		peerID = strings.TrimSpace(peerID)
		if peerID == "" {
			continue
		}
		items = append(items, peerID)
	}
	if len(items) == 0 {
		return "[]"
	}
	return "[" + strings.Join(items, ",") + "]"
}

func (s *Service) recordProviderSyncSuccess(channelID, peerID string) {
	if s == nil || s.store == nil {
		return
	}
	if err := s.store.RecordProviderSyncSuccess(channelID, peerID, s.nowUnix()); err != nil && err != sql.ErrNoRows {
		log.Printf("[publicchannel] record provider success channel=%s peer=%s err=%v", channelID, peerID, err)
	}
}

func (s *Service) recordProviderSyncFailure(channelID, peerID string) {
	if s == nil || s.store == nil {
		return
	}
	if err := s.store.RecordProviderSyncFailure(channelID, peerID, s.nowUnix()); err != nil && err != sql.ErrNoRows {
		log.Printf("[publicchannel] record provider failure channel=%s peer=%s err=%v", channelID, peerID, err)
	}
}

func firstNonEmptyString(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

package publicchannel

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	peer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/offlinestore"
	"github.com/chenjia404/meshproxy/internal/publicchannel/storenode"
	"github.com/chenjia404/meshproxy/internal/safe"
)

// storePushKind 推送阶段：先推空资料+头，再按需逐条推消息（每次 RPC 仅含最新一条业务消息）。
type storePushKind int

const (
	storePushInitial storePushKind = iota // profile + head，无 messages
	storePushProfileOnly                  // 资料/头更新，无 messages
	storePushWithMessage                  // profile + head + messages 仅含一条
)

type storePushJob struct {
	kind      storePushKind
	messageID int64
}

func (s *Service) SetStoreNodes(nodes []offlinestore.OfflineStoreNode) {
	if s == nil {
		return
	}
	if len(nodes) == 0 {
		s.storeNodes = nil
		return
	}
	s.storeNodes = append([]offlinestore.OfflineStoreNode(nil), nodes...)
}

func (s *Service) scheduleStorePush(channelID string, job storePushJob) {
	if s == nil || len(s.storeNodes) == 0 || s.nodePriv == nil || s.host == nil {
		return
	}
	cid := s.resolveCanonicalChannelID(channelID)
	if cid == "" {
		return
	}
	safe.Go("publicchannel.storePush."+cid, func() {
		muIface, _ := s.storePushMuByChan.LoadOrStore(cid, &sync.Mutex{})
		mu := muIface.(*sync.Mutex)
		mu.Lock()
		defer mu.Unlock()
		s.runStorePush(cid, job)
	})
}

// scheduleStorePushBackfill 用于迁移后：先推初始空快照，再按 message_id 升序逐条推送。
func (s *Service) scheduleStorePushBackfill(channelID string) {
	if s == nil || len(s.storeNodes) == 0 || s.nodePriv == nil {
		return
	}
	cid := s.resolveCanonicalChannelID(channelID)
	if cid == "" {
		return
	}
	safe.Go("publicchannel.storePushBackfill."+cid, func() {
		muIface, _ := s.storePushMuByChan.LoadOrStore(cid, &sync.Mutex{})
		mu := muIface.(*sync.Mutex)
		mu.Lock()
		defer mu.Unlock()

		s.runStorePush(cid, storePushJob{kind: storePushInitial})

		head, err := s.store.GetChannelHead(cid)
		if err != nil {
			log.Printf("[publicchannel] store backfill head %s: %v", cid, err)
			return
		}
		for mid := int64(1); mid <= head.LastMessageID; mid++ {
			msg, err := s.store.GetChannelMessage(cid, mid)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				log.Printf("[publicchannel] store backfill message %s #%d: %v", cid, mid, err)
				return
			}
			s.runStorePushWithMessages(cid, []ChannelMessage{msg})
		}
	})
}

func (s *Service) runStorePush(canonicalID string, job storePushJob) {
	var msgs []ChannelMessage
	switch job.kind {
	case storePushInitial, storePushProfileOnly:
		msgs = nil
	case storePushWithMessage:
		m, err := s.store.GetChannelMessage(canonicalID, job.messageID)
		if err != nil {
			log.Printf("[publicchannel] store push load message %s #%d: %v", canonicalID, job.messageID, err)
			return
		}
		msgs = []ChannelMessage{m}
	default:
		return
	}
	s.runStorePushWithMessages(canonicalID, msgs)
}

func (s *Service) runStorePushWithMessages(canonicalID string, msgs []ChannelMessage) {
	if len(s.storeNodes) == 0 || s.nodePriv == nil || s.host == nil {
		return
	}
	summary, err := s.store.GetChannelSummary(canonicalID)
	if err != nil {
		log.Printf("[publicchannel] store push summary %s: %v", canonicalID, err)
		return
	}
	if summary.Profile.OwnerPeerID != s.localPeer {
		return
	}
	req, err := s.buildStorePushRequest(summary.Profile, summary.Head, msgs)
	if err != nil {
		log.Printf("[publicchannel] store push build %s: %v", canonicalID, err)
		return
	}
	client := storenode.NewClient(s.host)
	for _, node := range s.storeNodes {
		pid, err := peer.Decode(strings.TrimSpace(node.PeerID))
		if err != nil {
			log.Printf("[publicchannel] store push bad store peer_id: %v", err)
			continue
		}
		pushCtx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
		if err := offlinestore.ConnectStorePeer(pushCtx, s.host, s.routing, node, offlinestore.DefaultStoreAddrTTL); err != nil {
			log.Printf("[publicchannel] store push connect peer=%s: %v", node.PeerID, err)
			cancel()
			continue
		}
		_, err = client.Push(pushCtx, pid, req)
		cancel()
		if err != nil {
			log.Printf("[publicchannel] store push rpc peer=%s channel=%s: %v", node.PeerID, canonicalID, err)
			continue
		}
		log.Printf("[publicchannel] store push ok peer=%s channel=%s msgs=%d", node.PeerID, canonicalID, len(msgs))
	}
}

func (s *Service) buildStorePushRequest(profile ChannelProfile, head ChannelHead, messages []ChannelMessage) (*storenode.PushRequest, error) {
	storeCID, err := meshToStoreChannelID(profile.ChannelID)
	if err != nil {
		return nil, err
	}
	wp := profileToStoreNode(storeCID, profile)
	wh := headToStoreNode(storeCID, head)
	if err := storenode.SignProfile(s.nodePriv, &wp); err != nil {
		return nil, err
	}
	if err := storenode.SignHead(s.nodePriv, &wh); err != nil {
		return nil, err
	}
	var wmsgs []*storenode.ChannelMessage
	for _, m := range messages {
		wm := messageToStoreNode(storeCID, m)
		if err := storenode.SignMessage(s.nodePriv, &wm); err != nil {
			return nil, err
		}
		wmsgs = append(wmsgs, &wm)
	}
	return &storenode.PushRequest{Profile: &wp, Head: &wh, Messages: wmsgs}, nil
}

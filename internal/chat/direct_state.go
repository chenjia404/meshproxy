package chat

import (
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"
)

func (s *Service) repairHistoricalDirectState() error {
	if s == nil || s.store == nil {
		return nil
	}

	requests, err := s.store.ListRequests(s.localPeer)
	if err != nil {
		return err
	}
	if len(requests) == 0 {
		return nil
	}

	conversations, err := s.store.ListConversations()
	if err != nil {
		return err
	}

	acceptedRequests := make([]Request, 0, len(requests))
	inboundPubByPeer := make(map[string]string)
	for _, req := range requests {
		peerID := s.peerIDForRequest(req)
		if peerID == "" {
			continue
		}
		if req.State == RequestStateAccepted {
			acceptedRequests = append(acceptedRequests, req)
		}
		if req.ToPeerID == s.localPeer {
			if _, ok := inboundPubByPeer[peerID]; !ok {
				if pub := strings.TrimSpace(req.RemoteChatKexPub); pub != "" {
					inboundPubByPeer[peerID] = pub
				}
			}
		}
	}
	if len(acceptedRequests) == 0 {
		return nil
	}

	_, priv, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		return err
	}

	conversationsByPeer := make(map[string]Conversation, len(conversations))
	for _, conv := range conversations {
		if conv.PeerID == "" {
			continue
		}
		conversationsByPeer[conv.PeerID] = conv
	}

	now := time.Now().UTC()
	scanned := 0
	requestsFixed := 0
	conversationsFixed := 0
	sessionStatesFixed := 0
	skipped := 0

	for _, req := range acceptedRequests {
		peerID := s.peerIDForRequest(req)
		if peerID == "" {
			continue
		}
		scanned++

		existingConv, hasConv := conversationsByPeer[peerID]
		targetConvID := deriveStableConversationID(s.localPeer, peerID)
		if hasConv && existingConv.ConversationID != targetConvID {
			if err := s.store.MigrateConversationID(existingConv.ConversationID, targetConvID); err != nil {
				return err
			}
			repairedConv, err := s.store.GetConversation(targetConvID)
			if err != nil {
				return err
			}
			existingConv = repairedConv
			conversationsByPeer[peerID] = repairedConv
			hasConv = true
		}

		if strings.TrimSpace(req.ConversationID) != targetConvID {
			if err := s.store.UpdateRequestState(req.RequestID, RequestStateAccepted, targetConvID); err != nil {
				return err
			}
			req.ConversationID = targetConvID
			requestsFixed++
		}

		sess, err := s.store.GetSessionState(targetConvID)
		hadSession := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !hadSession {
			remotePub := s.remoteChatKexPubForRepair(req, peerID, inboundPubByPeer)
			if remotePub == "" {
				skipped++
				log.Printf("[chat] startup repair skipped request=%s peer=%s conversation=%s: missing remote chat kex pub", req.RequestID, peerID, targetConvID)
				continue
			}
			sess, err = deriveSessionState(targetConvID, s.localPeer, peerID, priv, remotePub)
			if err != nil {
				skipped++
				log.Printf("[chat] startup repair skipped request=%s peer=%s conversation=%s: derive session failed: %v", req.RequestID, peerID, targetConvID, err)
				continue
			}
		}

		needsConversationRepair := !hasConv || existingConv.State != ConversationStateActive || !hadSession
		if !needsConversationRepair {
			continue
		}

		retentionMinutes := req.RetentionMinutes
		if hasConv && existingConv.RetentionMinutes > retentionMinutes {
			retentionMinutes = existingConv.RetentionMinutes
		}
		transportMode := strings.TrimSpace(req.LastTransportMode)
		if transportMode == "" {
			transportMode = TransportModeDirect
		}
		if hasConv && strings.TrimSpace(existingConv.LastTransportMode) != "" {
			transportMode = existingConv.LastTransportMode
		}
		createdAt := req.CreatedAt
		if hasConv && !existingConv.CreatedAt.IsZero() {
			createdAt = existingConv.CreatedAt
		}
		if createdAt.IsZero() {
			createdAt = now
		}

		sess.ConversationID = targetConvID
		sess.PeerID = peerID
		repairedConv, err := s.store.CreateConversation(Conversation{
			ConversationID:    targetConvID,
			PeerID:            peerID,
			State:             ConversationStateActive,
			LastTransportMode: transportMode,
			RetentionMinutes:  retentionMinutes,
			CreatedAt:         createdAt,
			UpdatedAt:         now,
		}, sess)
		if err != nil {
			return err
		}
		conversationsByPeer[peerID] = repairedConv
		if !hadSession {
			sessionStatesFixed++
		}
		if !hasConv || existingConv.State != ConversationStateActive {
			conversationsFixed++
		}
	}

	if requestsFixed > 0 || conversationsFixed > 0 || sessionStatesFixed > 0 || skipped > 0 {
		log.Printf(
			"[chat] startup direct data repair scanned=%d requests_fixed=%d conversations_fixed=%d session_states_fixed=%d skipped=%d",
			scanned,
			requestsFixed,
			conversationsFixed,
			sessionStatesFixed,
			skipped,
		)
	}
	return nil
}

func (s *Service) peerIDForRequest(req Request) string {
	switch {
	case req.FromPeerID == s.localPeer:
		return strings.TrimSpace(req.ToPeerID)
	case req.ToPeerID == s.localPeer:
		return strings.TrimSpace(req.FromPeerID)
	default:
		return ""
	}
}

func (s *Service) remoteChatKexPubForRepair(req Request, peerID string, inboundPubByPeer map[string]string) string {
	if req.ToPeerID == s.localPeer {
		return strings.TrimSpace(req.RemoteChatKexPub)
	}
	return strings.TrimSpace(inboundPubByPeer[peerID])
}

func (s *Service) findAcceptedRequestForPeer(peerID, preferredConversationID string) (Request, string, error) {
	requests, err := s.store.ListRequests(s.localPeer)
	if err != nil {
		return Request{}, "", err
	}

	inboundPub := ""
	for _, req := range requests {
		if s.peerIDForRequest(req) != peerID {
			continue
		}
		if req.ToPeerID == s.localPeer {
			if pub := strings.TrimSpace(req.RemoteChatKexPub); pub != "" {
				inboundPub = pub
				break
			}
		}
	}

	var selected *Request
	for _, req := range requests {
		if s.peerIDForRequest(req) != peerID || req.State != RequestStateAccepted {
			continue
		}
		reqCopy := req
		if preferredConversationID != "" && strings.TrimSpace(req.ConversationID) == preferredConversationID {
			selected = &reqCopy
			break
		}
		if selected == nil {
			selected = &reqCopy
		}
	}
	if selected == nil {
		return Request{}, "", sql.ErrNoRows
	}

	remotePub := s.remoteChatKexPubForRepair(*selected, peerID, map[string]string{peerID: inboundPub})
	return *selected, remotePub, nil
}

func (s *Service) repairIncomingDirectState(conversationID, fromPeerID, transportMode string) (Conversation, sessionState, bool, error) {
	if s == nil || s.store == nil || conversationID == "" || fromPeerID == "" {
		return Conversation{}, sessionState{}, false, sql.ErrNoRows
	}

	req, remotePub, err := s.findAcceptedRequestForPeer(fromPeerID, conversationID)
	if err != nil {
		return Conversation{}, sessionState{}, false, err
	}

	existingConv, err := s.store.GetConversationByPeer(fromPeerID)
	hasConv := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, sessionState{}, false, err
	}

	targetConvID := conversationID
	if hasConv {
		targetConvID = existingConv.ConversationID
	}

	sess, err := s.store.GetSessionState(targetConvID)
	hadSession := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, sessionState{}, false, err
	}
	if !hadSession {
		if remotePub == "" {
			return Conversation{}, sessionState{}, false, sql.ErrNoRows
		}
		_, priv, err := s.store.GetProfile(s.localPeer)
		if err != nil {
			return Conversation{}, sessionState{}, false, err
		}
		sess, err = deriveSessionState(targetConvID, s.localPeer, fromPeerID, priv, remotePub)
		if err != nil {
			return Conversation{}, sessionState{}, false, err
		}
	}

	now := time.Now().UTC()
	retentionMinutes := req.RetentionMinutes
	if hasConv && existingConv.RetentionMinutes > retentionMinutes {
		retentionMinutes = existingConv.RetentionMinutes
	}
	lastTransportMode := transportMode
	if hasConv && strings.TrimSpace(existingConv.LastTransportMode) != "" {
		lastTransportMode = existingConv.LastTransportMode
	}
	createdAt := req.CreatedAt
	if hasConv && !existingConv.CreatedAt.IsZero() {
		createdAt = existingConv.CreatedAt
	}
	if createdAt.IsZero() {
		createdAt = now
	}

	sess.ConversationID = targetConvID
	sess.PeerID = fromPeerID
	conv, err := s.store.CreateConversation(Conversation{
		ConversationID:    targetConvID,
		PeerID:            fromPeerID,
		State:             ConversationStateActive,
		LastTransportMode: lastTransportMode,
		RetentionMinutes:  retentionMinutes,
		CreatedAt:         createdAt,
		UpdatedAt:         now,
	}, sess)
	if err != nil {
		return Conversation{}, sessionState{}, false, err
	}

	if err := s.store.UpdateRequestState(req.RequestID, RequestStateAccepted, targetConvID); err != nil {
		return Conversation{}, sessionState{}, false, err
	}
	if remotePub != "" {
		_ = s.store.UpdateRequestRemoteChatKexPub(req.RequestID, remotePub)
	}

	if !hasConv || existingConv.State != ConversationStateActive || !hadSession {
		log.Printf("[chat] repaired incoming direct state peer=%s conversation=%s session_rebuilt=%t", fromPeerID, targetConvID, !hadSession)
	}
	return conv, sess, true, nil
}

func (s *Service) loadIncomingDirectState(conversationID, fromPeerID, transportMode string) (Conversation, sessionState, bool, error) {
	conv, err := s.store.GetConversation(conversationID)
	if err == nil {
		sess, err := s.store.GetSessionState(conversationID)
		if err == nil {
			return conv, sess, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Conversation{}, sessionState{}, false, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, sessionState{}, false, err
	}

	return s.repairIncomingDirectState(conversationID, fromPeerID, transportMode)
}

func (s *Service) handleIncomingRetentionUpdate(update RetentionUpdate, transportMode string) error {
	conv, _, _, err := s.loadIncomingDirectState(update.ConversationID, update.FromPeerID, transportMode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if s.maybeSendSessionRejectForConversation(update.FromPeerID) {
				return nil
			}
		}
		return err
	}

	conv, err = s.store.UpdateConversationRetention(conv.ConversationID, update.RetentionMinutes)
	if err != nil {
		return err
	}
	if err := s.store.UpdateConversationRetentionSync(conv.ConversationID, "synced", time.Now()); err != nil {
		return err
	}
	return nil
}

// maybeSendSessionRejectForConversation tries to detect "no friend relationship"
// for a peer and sends a SessionReject back when there is no accepted request.
func (s *Service) maybeSendSessionRejectForConversation(fromPeerID string) bool {
	if s == nil || s.store == nil || fromPeerID == "" {
		return false
	}

	req, err := s.store.LatestRequestBetweenPeers(fromPeerID, s.localPeer)
	requestID := ""
	if err == nil {
		requestID = req.RequestID
	}
	if requestID == "" {
		return false
	}

	req, err = s.store.GetRequest(requestID)
	if err == nil {
		if req.ToPeerID == s.localPeer && req.FromPeerID == fromPeerID && req.State == RequestStateAccepted {
			return false
		}
	}

	if hasAccepted, _ := s.store.HasAcceptedRequest(fromPeerID, s.localPeer); hasAccepted {
		return false
	}

	_ = s.sendEnvelope(fromPeerID, SessionReject{
		Type:       MessageTypeSessionReject,
		RequestID:  requestID,
		FromPeerID: s.localPeer,
		ToPeerID:   fromPeerID,
		SentAtUnix: time.Now().UTC().UnixMilli(),
	})
	return true
}

// repairOrphanPendingSessionAccepts fixes a historical bug where RemoteChatKexPub was written
// before deriveSessionState/CreateConversation; failures left requests stuck as pending with
// no active conversation, so contacts and messages never appeared.
func (s *Service) repairOrphanPendingSessionAccepts() error {
	if s == nil || s.store == nil {
		return nil
	}
	requests, err := s.store.ListRequests(s.localPeer)
	if err != nil {
		return err
	}
	_, priv, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		return err
	}
	fixed := 0
	for _, req := range requests {
		if req.State != RequestStatePending {
			continue
		}
		if strings.TrimSpace(req.RemoteChatKexPub) == "" {
			continue
		}
		peerID := s.peerIDForRequest(req)
		if peerID == "" {
			continue
		}
		targetConvID := deriveStableConversationID(s.localPeer, peerID)
		if conv, err := s.store.GetConversationByPeer(peerID); err == nil {
			if conv.State == ConversationStateActive {
				if _, err := s.store.GetSessionState(conv.ConversationID); err == nil {
					if err := s.store.UpdateRequestState(req.RequestID, RequestStateAccepted, conv.ConversationID); err != nil {
						log.Printf("[chat] orphan accept repair: update request state failed request=%s err=%v", req.RequestID, err)
					} else {
						fixed++
					}
					continue
				}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			continue
		}
		sess, err := deriveSessionState(targetConvID, s.localPeer, peerID, priv, req.RemoteChatKexPub)
		if err != nil {
			log.Printf("[chat] orphan accept repair skipped request=%s peer=%s: %v", req.RequestID, peerID, err)
			continue
		}
		retention := req.RetentionMinutes
		now := time.Now().UTC()
		if _, err := s.store.CreateConversation(Conversation{
			ConversationID:    targetConvID,
			PeerID:            peerID,
			State:             ConversationStateActive,
			LastTransportMode: TransportModeDirect,
			RetentionMinutes:  retention,
			CreatedAt:         now,
			UpdatedAt:         now,
		}, sess); err != nil {
			log.Printf("[chat] orphan accept repair: create conversation failed request=%s peer=%s err=%v", req.RequestID, peerID, err)
			continue
		}
		// requests.nickname/bio 對「我發出的」請求存的是本地 profile，不能寫入對方 peer 列。
		// 僅在對方發起（收件人為本地）時，nickname/bio 才代表遠端。
		if req.ToPeerID == s.localPeer {
			_ = s.store.UpsertPeer(peerID, req.Nickname, req.Bio)
		}
		if err := s.store.UpdateRequestState(req.RequestID, RequestStateAccepted, targetConvID); err != nil {
			log.Printf("[chat] orphan accept repair: update request state failed request=%s err=%v", req.RequestID, err)
			continue
		}
		_ = s.store.DeleteFriendRequestJob(req.RequestID)
		fixed++
		s.publishChatEvent(newFriendRequestEvent(
			RequestStateAccepted,
			req.RequestID,
			peerID,
			s.localPeer,
			targetConvID,
		))
	}
	if fixed > 0 {
		log.Printf("[chat] repair orphan pending session accepts fixed=%d", fixed)
	}
	return nil
}

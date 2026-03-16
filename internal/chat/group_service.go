package chat

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	peer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/p2p"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/safe"
	"github.com/chenjia404/meshproxy/internal/tunnel"
)

const (
	groupRetryTickInterval = 10 * time.Second
	groupRetryBatchSize    = 32
	groupRetryBaseDelay    = 15 * time.Second
	groupRetryMaxDelay     = 5 * time.Minute
	groupMaxFutureSkew     = 10 * time.Minute
	groupRequestMaxAge     = 15 * time.Minute
)

func (s *Service) ListGroups() ([]Group, error) {
	return s.store.ListGroups()
}

func (s *Service) GetGroupDetails(groupID string) (GroupDetails, error) {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return GroupDetails{}, err
	}
	members, err := s.store.ListGroupMembers(groupID)
	if err != nil {
		return GroupDetails{}, err
	}
	events, err := s.store.ListRecentGroupEvents(groupID, 12)
	if err != nil {
		return GroupDetails{}, err
	}
	return GroupDetails{Group: group, Members: members, RecentEvents: events}, nil
}

func (s *Service) CreateGroup(title string, memberPeerIDs []string) (Group, error) {
	title, err := normalizeGroupTitle(title)
	if err != nil {
		return Group{}, err
	}
	initialInvitees, err := s.normalizeInitialInvitees(memberPeerIDs)
	if err != nil {
		return Group{}, err
	}
	now := time.Now().UTC()
	groupID := uuid.NewString()
	initialMembers := []string{s.localPeer}
	groupMembers := []GroupMember{{
		GroupID:     groupID,
		PeerID:      s.localPeer,
		Role:        GroupRoleController,
		State:       GroupMemberStateActive,
		JoinedEpoch: 1,
		UpdatedAt:   now,
	}}
	payload := GroupCreatePayload{
		Title:            title,
		ControllerPeerID: s.localPeer,
		InitialMembers:   initialMembers,
		Epoch:            1,
	}
	initialEpochKeys, err := generateGroupEpochKeys(initialMembers)
	if err != nil {
		return Group{}, err
	}
	payload.EpochKeys = initialEpochKeys
	event, err := newGroupEvent(groupID, 1, GroupEventCreate, s.localPeer, s.localPeer, now, payload)
	if err != nil {
		return Group{}, err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return Group{}, err
	}
	group := Group{
		GroupID:          groupID,
		Title:            title,
		ControllerPeerID: s.localPeer,
		CurrentEpoch:     1,
		State:            GroupStateActive,
		LastEventSeq:     1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	epoch := GroupEpoch{
		GroupID:            groupID,
		Epoch:              1,
		WrappedKeyForLocal: initialEpochKeys[s.localPeer],
		CreatedAt:          now,
	}
	created, err := s.store.CreateGroup(group, epoch, groupMembers, event)
	if err != nil {
		return Group{}, err
	}
	s.broadcastGroupEvent(created.GroupID, event, peersFromMembers(groupMembers, s.localPeer)...)
	for _, peerID := range initialInvitees {
		created, err = s.InviteGroupMember(created.GroupID, peerID, GroupRoleMember, "")
		if err != nil {
			return Group{}, err
		}
	}
	return created, nil
}

func (s *Service) InviteGroupMember(groupID, peerID, role, inviteText string) (Group, error) {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return Group{}, err
	}
	if group.State != GroupStateActive {
		return Group{}, errors.New("group is archived")
	}
	if group.ControllerPeerID != s.localPeer {
		return Group{}, errors.New("only controller can invite members")
	}
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return Group{}, errors.New("peer_id is required")
	}
	if peerID == s.localPeer {
		return Group{}, errors.New("controller is already in the group")
	}
	if _, err := s.store.GetConversationByPeer(peerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, errors.New("group invites require an existing direct conversation")
		}
		return Group{}, err
	}
	role, err = normalizeInviteRole(role)
	if err != nil {
		return Group{}, err
	}
	existing, err := s.store.GetGroupMember(groupID, peerID)
	if err == nil && (existing.State == GroupMemberStateActive || existing.State == GroupMemberStateInvited) {
		return Group{}, errors.New("peer is already an invited or active member")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Group{}, err
	}
	now := time.Now().UTC()
	members, err := s.store.ListGroupMembers(groupID)
	if err != nil {
		return Group{}, err
	}
	currentEpochKey, err := s.store.GetGroupEpoch(groupID, group.CurrentEpoch)
	if err != nil {
		return Group{}, err
	}
	payload := GroupInvitePayload{
		InviteePeerID:    peerID,
		Role:             role,
		InviteText:       strings.TrimSpace(inviteText),
		Epoch:            group.CurrentEpoch,
		Title:            group.Title,
		ControllerPeerID: group.ControllerPeerID,
		KnownMembers:     append(activePeerIDs(members), peerID),
		WrappedGroupKey:  currentEpochKey.WrappedKeyForLocal,
	}
	event, err := newGroupEvent(groupID, group.LastEventSeq+1, GroupEventInvite, s.localPeer, s.localPeer, now, payload)
	if err != nil {
		return Group{}, err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return Group{}, err
	}
	member := GroupMember{
		GroupID:   groupID,
		PeerID:    peerID,
		Role:      role,
		State:     GroupMemberStateInvited,
		InvitedBy: s.localPeer,
		UpdatedAt: now,
	}
	_ = s.store.UpsertPeer(peerID, "")
	updated, err := s.store.UpsertGroupMember(groupID, member, event)
	if err != nil {
		return Group{}, err
	}
	recipients := peersFromMembers(members, s.localPeer)
	s.broadcastGroupEvent(groupID, event, recipients...)
	if err := s.sendDirectGroupInviteNotice(peerID, updated, payload, groupEnvelopeFromEvent(event)); err != nil {
		log.Printf("[group] send direct invite notice group=%s peer=%s failed: %v", groupID, peerID, err)
	}
	return updated, nil
}

func (s *Service) AcceptGroupInvite(groupID string) (Group, error) {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return Group{}, err
	}
	if group.State != GroupStateActive {
		return Group{}, errors.New("group is archived")
	}
	member, err := s.store.GetGroupMember(groupID, s.localPeer)
	if err != nil {
		return Group{}, err
	}
	if member.State != GroupMemberStateInvited {
		return Group{}, errors.New("local peer is not in invited state")
	}
	now := time.Now().UTC()
	if group.ControllerPeerID != s.localPeer {
		req := GroupJoinRequest{
			Type:          MessageTypeGroupJoinRequest,
			GroupID:       groupID,
			JoinerPeerID:  s.localPeer,
			AcceptedEpoch: group.CurrentEpoch,
			SentAtUnix:    now.UnixMilli(),
		}
		if err := s.signGroupJoinRequest(&req); err != nil {
			return Group{}, err
		}
		if err := s.sendEnvelope(group.ControllerPeerID, req); err != nil {
			return Group{}, err
		}
		return s.syncAcceptedInviteState(groupID, group.ControllerPeerID)
	}
	payload := GroupJoinPayload{
		JoinerPeerID:  s.localPeer,
		AcceptedEpoch: group.CurrentEpoch,
	}
	event, err := newGroupEvent(groupID, group.LastEventSeq+1, GroupEventJoin, s.localPeer, s.localPeer, now, payload)
	if err != nil {
		return Group{}, err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return Group{}, err
	}
	updated, err := s.store.ActivateGroupMember(groupID, s.localPeer, group.CurrentEpoch, event)
	if err != nil {
		return Group{}, err
	}
	members, _ := s.store.ListGroupMembers(groupID)
	s.broadcastGroupEvent(groupID, event, peersFromMembers(members, s.localPeer)...)
	return updated, nil
}

func (s *Service) LeaveGroup(groupID, reason string) (Group, error) {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return Group{}, err
	}
	if group.State != GroupStateActive {
		return Group{}, errors.New("group is archived")
	}
	member, err := s.store.GetGroupMember(groupID, s.localPeer)
	if err != nil {
		return Group{}, err
	}
	if member.State != GroupMemberStateActive {
		return Group{}, errors.New("local peer is not an active member")
	}
	if member.Role == GroupRoleController {
		members, err := s.store.ListGroupMembers(groupID)
		if err != nil {
			return Group{}, err
		}
		for _, candidate := range members {
			if candidate.PeerID != s.localPeer && candidate.State == GroupMemberStateActive {
				return Group{}, errors.New("controller must transfer control before leaving")
			}
		}
		return Group{}, errors.New("controller leave for archived groups is not implemented yet")
	}
	now := time.Now().UTC()
	if group.ControllerPeerID != s.localPeer {
		req := GroupLeaveRequest{
			Type:         MessageTypeGroupLeaveRequest,
			GroupID:      groupID,
			LeaverPeerID: s.localPeer,
			Reason:       strings.TrimSpace(reason),
			SentAtUnix:   now.UnixMilli(),
		}
		if err := s.signGroupLeaveRequest(&req); err != nil {
			return Group{}, err
		}
		if err := s.sendEnvelope(group.ControllerPeerID, req); err != nil {
			return Group{}, err
		}
		return s.store.GetGroup(groupID)
	}
	payload := GroupLeavePayload{
		LeaverPeerID: s.localPeer,
		Reason:       strings.TrimSpace(reason),
		NewEpoch:     group.CurrentEpoch + 1,
	}
	remainingMembers, err := s.store.ListGroupMembers(groupID)
	if err != nil {
		return Group{}, err
	}
	payload.EpochKeys, err = generateGroupEpochKeys(activePeerIDsExcluding(remainingMembers, s.localPeer))
	if err != nil {
		return Group{}, err
	}
	event, err := newGroupEvent(groupID, group.LastEventSeq+1, GroupEventLeave, s.localPeer, s.localPeer, now, payload)
	if err != nil {
		return Group{}, err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return Group{}, err
	}
	nextEpoch := GroupEpoch{
		GroupID:            groupID,
		Epoch:              group.CurrentEpoch + 1,
		WrappedKeyForLocal: payload.EpochKeys[s.localPeer],
		CreatedAt:          now,
	}
	updated, err := s.store.MarkGroupMemberLeft(groupID, s.localPeer, group.CurrentEpoch, nextEpoch, event)
	if err != nil {
		return Group{}, err
	}
	members, _ := s.store.ListGroupMembers(groupID)
	recipients := peersFromMembers(members, s.localPeer)
	s.broadcastGroupEvent(groupID, event, recipients...)
	return updated, nil
}

func (s *Service) RemoveGroupMember(groupID, peerID, reason string) (Group, error) {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return Group{}, err
	}
	if group.State != GroupStateActive {
		return Group{}, errors.New("group is archived")
	}
	if group.ControllerPeerID != s.localPeer {
		return Group{}, errors.New("only controller can remove members")
	}
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return Group{}, errors.New("peer_id is required")
	}
	if peerID == s.localPeer {
		return Group{}, errors.New("controller cannot remove itself")
	}
	member, err := s.store.GetGroupMember(groupID, peerID)
	if err != nil {
		return Group{}, err
	}
	if member.State != GroupMemberStateActive && member.State != GroupMemberStateInvited {
		return Group{}, errors.New("peer is not an invited or active member")
	}
	now := time.Now().UTC()
	payload := GroupRemovePayload{
		TargetPeerID: peerID,
		Reason:       strings.TrimSpace(reason),
		NewEpoch:     group.CurrentEpoch + 1,
	}
	members, err := s.store.ListGroupMembers(groupID)
	if err != nil {
		return Group{}, err
	}
	payload.EpochKeys, err = generateGroupEpochKeys(activePeerIDsExcluding(members, peerID))
	if err != nil {
		return Group{}, err
	}
	event, err := newGroupEvent(groupID, group.LastEventSeq+1, GroupEventRemove, s.localPeer, s.localPeer, now, payload)
	if err != nil {
		return Group{}, err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return Group{}, err
	}
	nextEpoch := GroupEpoch{
		GroupID:            groupID,
		Epoch:              group.CurrentEpoch + 1,
		WrappedKeyForLocal: payload.EpochKeys[s.localPeer],
		CreatedAt:          now,
	}
	updated, err := s.store.MarkGroupMemberRemoved(groupID, peerID, group.CurrentEpoch, nextEpoch, event)
	if err != nil {
		return Group{}, err
	}
	recipients := peersFromMembers(members, s.localPeer)
	recipients = appendIfMissing(recipients, peerID, s.localPeer)
	s.broadcastGroupEvent(groupID, event, recipients...)
	return updated, nil
}

func (s *Service) UpdateGroupTitle(groupID, title string) (Group, error) {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return Group{}, err
	}
	if group.State != GroupStateActive {
		return Group{}, errors.New("group is archived")
	}
	if group.ControllerPeerID != s.localPeer {
		return Group{}, errors.New("only controller can update the group title")
	}
	title, err = normalizeGroupTitle(title)
	if err != nil {
		return Group{}, err
	}
	now := time.Now().UTC()
	payload := GroupTitleUpdatePayload{Title: title}
	event, err := newGroupEvent(groupID, group.LastEventSeq+1, GroupEventTitleUpdate, s.localPeer, s.localPeer, now, payload)
	if err != nil {
		return Group{}, err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return Group{}, err
	}
	updated, err := s.store.UpdateGroupTitle(groupID, title, event)
	if err != nil {
		return Group{}, err
	}
	members, _ := s.store.ListGroupMembers(groupID)
	s.broadcastGroupEvent(groupID, event, peersFromMembers(members, s.localPeer)...)
	return updated, nil
}

func (s *Service) UpdateGroupRetention(groupID string, minutes int) (Group, error) {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return Group{}, err
	}
	if group.State != GroupStateActive {
		return Group{}, errors.New("group is archived")
	}
	member, err := s.store.GetGroupMember(groupID, s.localPeer)
	if err != nil {
		return Group{}, err
	}
	if member.State != GroupMemberStateActive {
		return Group{}, errors.New("local peer is not an active group member")
	}
	if member.Role != GroupRoleController && member.Role != GroupRoleAdmin {
		return Group{}, errors.New("only controller or admin can update group retention")
	}
	if minutes != 0 && (minutes < MinRetentionMinutes || minutes > MaxRetentionMinutes) {
		return Group{}, fmt.Errorf("retention_minutes must be 0 or between %d and %d", MinRetentionMinutes, MaxRetentionMinutes)
	}
	now := time.Now().UTC()
	payload := GroupRetentionUpdatePayload{RetentionMinutes: minutes}
	event, err := newGroupEvent(groupID, group.LastEventSeq+1, GroupEventRetentionUpdate, s.localPeer, s.localPeer, now, payload)
	if err != nil {
		return Group{}, err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return Group{}, err
	}
	updated, err := s.store.UpdateGroupRetention(groupID, minutes, event)
	if err != nil {
		return Group{}, err
	}
	members, _ := s.store.ListGroupMembers(groupID)
	s.broadcastGroupEvent(groupID, event, peersFromMembers(members, s.localPeer)...)
	return updated, nil
}

func (s *Service) DissolveGroup(groupID, reason string) (Group, error) {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return Group{}, err
	}
	if group.State != GroupStateActive {
		return Group{}, errors.New("group is already archived")
	}
	if group.ControllerPeerID != s.localPeer {
		return Group{}, errors.New("only controller can dissolve the group")
	}
	now := time.Now().UTC()
	payload := GroupDissolvePayload{Reason: strings.TrimSpace(reason)}
	event, err := newGroupEvent(groupID, group.LastEventSeq+1, GroupEventDissolve, s.localPeer, s.localPeer, now, payload)
	if err != nil {
		return Group{}, err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return Group{}, err
	}
	members, err := s.store.ListGroupMembers(groupID)
	if err != nil {
		return Group{}, err
	}
	updated, err := s.store.DissolveGroup(groupID, event)
	if err != nil {
		return Group{}, err
	}
	s.broadcastGroupEvent(groupID, event, peersFromMembers(members, s.localPeer)...)
	return updated, nil
}

func (s *Service) TransferGroupController(groupID, peerID string) (Group, error) {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return Group{}, err
	}
	if group.State != GroupStateActive {
		return Group{}, errors.New("group is archived")
	}
	if group.ControllerPeerID != s.localPeer {
		return Group{}, errors.New("only controller can transfer control")
	}
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return Group{}, errors.New("peer_id is required")
	}
	if peerID == s.localPeer {
		return Group{}, errors.New("target peer is already the controller")
	}
	member, err := s.store.GetGroupMember(groupID, peerID)
	if err != nil {
		return Group{}, err
	}
	if member.State != GroupMemberStateActive {
		return Group{}, errors.New("target peer must be an active member")
	}
	now := time.Now().UTC()
	payload := GroupControllerTransferPayload{
		FromPeerID: s.localPeer,
		ToPeerID:   peerID,
		NewEpoch:   group.CurrentEpoch + 1,
	}
	members, err := s.store.ListGroupMembers(groupID)
	if err != nil {
		return Group{}, err
	}
	payload.EpochKeys, err = generateGroupEpochKeys(activePeerIDs(members))
	if err != nil {
		return Group{}, err
	}
	event, err := newGroupEvent(groupID, group.LastEventSeq+1, GroupEventControllerTransfer, s.localPeer, s.localPeer, now, payload)
	if err != nil {
		return Group{}, err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return Group{}, err
	}
	nextEpoch := GroupEpoch{
		GroupID:            groupID,
		Epoch:              group.CurrentEpoch + 1,
		WrappedKeyForLocal: payload.EpochKeys[s.localPeer],
		CreatedAt:          now,
	}
	updated, err := s.store.TransferGroupController(groupID, s.localPeer, peerID, nextEpoch, event)
	if err != nil {
		return Group{}, err
	}
	s.broadcastGroupEvent(groupID, event, peersFromMembers(members, s.localPeer)...)
	return updated, nil
}

func (s *Service) ListGroupMessages(groupID string) ([]GroupMessage, error) {
	if _, err := s.store.GetGroupMember(groupID, s.localPeer); err != nil {
		return nil, err
	}
	return s.store.ListGroupMessages(groupID)
}

func (s *Service) RevokeGroupMessage(groupID, msgID string) error {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return err
	}
	if group.State != GroupStateActive {
		return errors.New("group is archived")
	}
	member, err := s.store.GetGroupMember(groupID, s.localPeer)
	if err != nil {
		return err
	}
	if member.State != GroupMemberStateActive {
		return errors.New("local peer is not an active group member")
	}
	msg, err := s.store.GetGroupMessage(msgID)
	if err != nil {
		return err
	}
	if msg.GroupID != groupID {
		return errors.New("message does not belong to group")
	}
	if err := s.ensureLocalGroupMessageRevokeAllowed(groupID, member, msg); err != nil {
		return err
	}
	now := time.Now().UTC()
	payload := GroupMessageRevokePayload{
		MsgID:        msg.MsgID,
		SenderPeerID: msg.SenderPeerID,
	}
	event, err := newGroupEvent(groupID, group.LastEventSeq+1, GroupEventMessageRevoke, s.localPeer, s.localPeer, now, payload)
	if err != nil {
		return err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return err
	}
	if err := s.store.RevokeGroupMessage(groupID, msg.MsgID, msg.SenderPeerID, s.localPeer, event); err != nil {
		return err
	}
	members, _ := s.store.ListGroupMembers(groupID)
	s.broadcastGroupEvent(groupID, event, peersFromMembers(members, s.localPeer)...)
	return nil
}

func (s *Service) SendGroupText(groupID, text string) (GroupMessage, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return GroupMessage{}, errors.New("group message text is empty")
	}
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return GroupMessage{}, err
	}
	if group.State != GroupStateActive {
		return GroupMessage{}, errors.New("group is archived")
	}
	member, err := s.store.GetGroupMember(groupID, s.localPeer)
	if err != nil {
		return GroupMessage{}, err
	}
	if member.State != GroupMemberStateActive {
		return GroupMessage{}, errors.New("local peer is not an active group member")
	}
	groupEpoch, err := s.store.GetGroupEpoch(groupID, group.CurrentEpoch)
	if err != nil {
		return GroupMessage{}, err
	}
	senderSeq, err := s.store.NextGroupSenderSeq(groupID, s.localPeer)
	if err != nil {
		return GroupMessage{}, err
	}
	nonce := protocol.BuildAEADNonce("fwd", senderSeq)
	aad := []byte(groupID + "\x00group_chat_text")
	ciphertext, err := protocol.AEADSeal(groupEpoch.WrappedKeyForLocal, nonce, []byte(text), aad)
	if err != nil {
		return GroupMessage{}, err
	}
	now := time.Now().UTC()
	msg := GroupMessage{
		MsgID:        uuid.NewString(),
		GroupID:      groupID,
		Epoch:        group.CurrentEpoch,
		SenderPeerID: s.localPeer,
		SenderSeq:    senderSeq,
		MsgType:      MessageTypeGroupChatText,
		Plaintext:    text,
		State:        GroupMessageStateLocalOnly,
		CreatedAt:    now,
	}
	members, err := s.store.ListGroupMembers(groupID)
	if err != nil {
		return GroupMessage{}, err
	}
	recipients := activePeerIDsExcluding(members, s.localPeer)
	deliveries := make([]GroupMessageDelivery, 0, len(recipients))
	for _, peerID := range recipients {
		deliveries = append(deliveries, GroupMessageDelivery{
			MsgID:         msg.MsgID,
			PeerID:        peerID,
			TransportMode: TransportModeDirect,
			State:         GroupDeliveryStatePending,
			UpdatedAt:     now,
		})
	}
	wire := GroupChatText{
		Type:         MessageTypeGroupChatText,
		GroupID:      groupID,
		Epoch:        group.CurrentEpoch,
		MsgID:        msg.MsgID,
		SenderPeerID: s.localPeer,
		SenderSeq:    senderSeq,
		Ciphertext:   ciphertext,
		SentAtUnix:   now.UnixMilli(),
	}
	if err := s.signGroupChatText(&wire); err != nil {
		return GroupMessage{}, err
	}
	msg.Signature = append([]byte(nil), wire.Signature...)
	msg, err = s.store.AddGroupMessage(msg, ciphertext, deliveries)
	if err != nil {
		return GroupMessage{}, err
	}
	sentCount := 0
	failedCount := 0
	for _, peerID := range recipients {
		if err := s.sendEnvelope(peerID, wire); err != nil {
			failedCount++
			_ = s.scheduleGroupDeliveryRetry(msg.MsgID, peerID, 1)
			log.Printf("[group] send text group=%s peer=%s failed: %v", groupID, peerID, err)
			continue
		}
		sentCount++
		_ = s.store.MarkGroupDeliveryState(msg.MsgID, peerID, GroupDeliveryStateSentToTransport, time.Time{})
	}
	switch {
	case len(recipients) == 0:
		msg.State = GroupMessageStateLocalOnly
	case failedCount == 0 && sentCount == len(recipients):
		msg.State = GroupMessageStateSentToTransport
	case sentCount > 0:
		msg.State = GroupMessageStatePartiallySent
	default:
		msg.State = GroupMessageStateFailedPartial
	}
	return s.store.GetGroupMessage(msg.MsgID)
}

func (s *Service) SendGroupFile(groupID, fileName, mimeType string, data []byte) (GroupMessage, error) {
	if len(data) == 0 {
		return GroupMessage{}, errors.New("file is empty")
	}
	if len(data) > MaxChatFileBytes {
		return GroupMessage{}, fmt.Errorf("file too large: max %d bytes", MaxChatFileBytes)
	}
	fileName = NormalizeChatFileName(fileName)
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return GroupMessage{}, err
	}
	if group.State != GroupStateActive {
		return GroupMessage{}, errors.New("group is archived")
	}
	member, err := s.store.GetGroupMember(groupID, s.localPeer)
	if err != nil {
		return GroupMessage{}, err
	}
	if member.State != GroupMemberStateActive {
		return GroupMessage{}, errors.New("local peer is not an active group member")
	}
	groupEpoch, err := s.store.GetGroupEpoch(groupID, group.CurrentEpoch)
	if err != nil {
		return GroupMessage{}, err
	}
	senderSeq, err := s.store.NextGroupSenderSeq(groupID, s.localPeer)
	if err != nil {
		return GroupMessage{}, err
	}
	nonce := protocol.BuildAEADNonce("fwd", senderSeq)
	aad := []byte(groupID + "\x00group_chat_file")
	ciphertext, err := protocol.AEADSeal(groupEpoch.WrappedKeyForLocal, nonce, data, aad)
	if err != nil {
		return GroupMessage{}, err
	}
	now := time.Now().UTC()
	msg := GroupMessage{
		MsgID:        uuid.NewString(),
		GroupID:      groupID,
		Epoch:        group.CurrentEpoch,
		SenderPeerID: s.localPeer,
		SenderSeq:    senderSeq,
		MsgType:      MessageTypeGroupChatFile,
		FileName:     fileName,
		MIMEType:     mimeType,
		FileSize:     int64(len(data)),
		State:        GroupMessageStateLocalOnly,
		CreatedAt:    now,
	}
	members, err := s.store.ListGroupMembers(groupID)
	if err != nil {
		return GroupMessage{}, err
	}
	recipients := activePeerIDsExcluding(members, s.localPeer)
	deliveries := make([]GroupMessageDelivery, 0, len(recipients))
	for _, peerID := range recipients {
		deliveries = append(deliveries, GroupMessageDelivery{
			MsgID:         msg.MsgID,
			PeerID:        peerID,
			TransportMode: TransportModeDirect,
			State:         GroupDeliveryStatePending,
			UpdatedAt:     now,
		})
	}
	wire := GroupChatFile{
		Type:         MessageTypeGroupChatFile,
		GroupID:      groupID,
		Epoch:        group.CurrentEpoch,
		MsgID:        msg.MsgID,
		SenderPeerID: s.localPeer,
		SenderSeq:    senderSeq,
		FileName:     fileName,
		MIMEType:     mimeType,
		FileSize:     int64(len(data)),
		Ciphertext:   ciphertext,
		SentAtUnix:   now.UnixMilli(),
	}
	if err := s.signGroupChatFile(&wire); err != nil {
		return GroupMessage{}, err
	}
	msg.Signature = append([]byte(nil), wire.Signature...)
	msg, err = s.store.AddGroupMessage(msg, data, deliveries)
	if err != nil {
		return GroupMessage{}, err
	}
	sentCount := 0
	failedCount := 0
	for _, peerID := range recipients {
		if err := s.sendEnvelope(peerID, wire); err != nil {
			failedCount++
			_ = s.scheduleGroupDeliveryRetry(msg.MsgID, peerID, 1)
			log.Printf("[group] send file group=%s peer=%s failed: %v", groupID, peerID, err)
			continue
		}
		sentCount++
		_ = s.store.MarkGroupDeliveryState(msg.MsgID, peerID, GroupDeliveryStateSentToTransport, time.Time{})
	}
	switch {
	case len(recipients) == 0:
		msg.State = GroupMessageStateLocalOnly
	case failedCount == 0 && sentCount == len(recipients):
		msg.State = GroupMessageStateSentToTransport
	case sentCount > 0:
		msg.State = GroupMessageStatePartiallySent
	default:
		msg.State = GroupMessageStateFailedPartial
	}
	return s.store.GetGroupMessage(msg.MsgID)
}

func (s *Service) GetGroupMessageFile(groupID, msgID string) (GroupMessage, []byte, error) {
	if _, err := s.store.GetGroupMember(groupID, s.localPeer); err != nil {
		return GroupMessage{}, nil, err
	}
	msg, err := s.store.GetGroupMessage(msgID)
	if err != nil {
		return GroupMessage{}, nil, err
	}
	if msg.GroupID != groupID {
		return GroupMessage{}, nil, errors.New("message does not belong to group")
	}
	if msg.MsgType != MessageTypeGroupChatFile {
		return GroupMessage{}, nil, errors.New("message is not a file")
	}
	blob, err := s.store.GetGroupMessageBlob(msgID)
	if err != nil {
		return GroupMessage{}, nil, err
	}
	return msg, blob, nil
}

func (s *Service) SyncGroup(groupID, fromPeerID string) error {
	group, err := s.store.GetGroup(groupID)
	if err != nil {
		return err
	}
	member, err := s.store.GetGroupMember(groupID, s.localPeer)
	if err != nil {
		return err
	}
	if member.State != GroupMemberStateActive && member.State != GroupMemberStateInvited {
		return errors.New("local peer is not an active or invited group member")
	}
	remoteMember, err := s.store.GetGroupMember(groupID, fromPeerID)
	if err != nil {
		return err
	}
	if remoteMember.State != GroupMemberStateActive {
		return errors.New("sync source is not an active group member")
	}
	cursors, err := s.store.ListGroupSenderCursors(groupID)
	if err != nil {
		return err
	}
	req := GroupSyncRequest{
		Type:          MessageTypeGroupSyncRequest,
		GroupID:       groupID,
		LastEventSeq:  group.LastEventSeq,
		SenderCursors: cursors,
	}
	resp, err := s.requestGroupSync(fromPeerID, req)
	if err != nil {
		return err
	}
	for _, event := range resp.Events {
		if err := s.verifyGroupControlEnvelope(event); err != nil {
			return err
		}
		if err := s.applyGroupControlEnvelope(event); err != nil {
			return err
		}
	}
	for _, msg := range resp.Messages {
		if err := s.verifyGroupChatText(msg); err != nil {
			return err
		}
		if err := s.handleIncomingGroupText(msg); err != nil {
			return err
		}
	}
	for _, msg := range resp.FileMessages {
		if err := s.verifyGroupChatFile(msg); err != nil {
			return err
		}
		if err := s.handleIncomingGroupFile(msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) syncAcceptedInviteState(groupID, controllerPeerID string) (Group, error) {
	delays := []time.Duration{150 * time.Millisecond, 400 * time.Millisecond, time.Second}
	var lastErr error
	for _, delay := range delays {
		time.Sleep(delay)
		if err := s.SyncGroup(groupID, controllerPeerID); err != nil {
			lastErr = err
		}
		member, err := s.store.GetGroupMember(groupID, s.localPeer)
		if err == nil && member.State == GroupMemberStateActive {
			return s.store.GetGroup(groupID)
		}
	}
	if lastErr != nil {
		safe.Go("chat.followupGroupJoinSync", func() {
			time.Sleep(2 * time.Second)
			if err := s.SyncGroup(groupID, controllerPeerID); err != nil {
				log.Printf("[group] follow-up sync after accept failed group=%s controller=%s: %v", groupID, controllerPeerID, err)
			}
		})
	}
	return s.store.GetGroup(groupID)
}

func (s *Service) runGroupRetryLoop() {
	ticker := time.NewTicker(groupRetryTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.processGroupRetries(time.Now().UTC()); err != nil {
				log.Printf("[group] retry loop failed: %v", err)
			}
		}
	}
}

func (s *Service) processGroupRetries(now time.Time) error {
	items, err := s.store.ListGroupDeliveriesForRetry(now, groupRetryBatchSize)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.SenderPeerID != s.localPeer {
			continue
		}
		if err := s.retryGroupDelivery(item); err != nil {
			log.Printf("[group] retry delivery group=%s msg=%s peer=%s failed: %v", item.GroupID, item.MsgID, item.PeerID, err)
		}
	}
	eventItems, err := s.store.ListGroupEventDeliveriesForRetry(now, groupRetryBatchSize)
	if err != nil {
		return err
	}
	for _, item := range eventItems {
		if err := s.retryGroupEventDelivery(item); err != nil {
			log.Printf("[group] retry event delivery group=%s event=%s peer=%s failed: %v", item.GroupID, item.EventID, item.PeerID, err)
		}
	}
	return nil
}

func (s *Service) retryGroupDelivery(item groupRetryDelivery) error {
	wire, err := s.buildGroupRetryEnvelope(item)
	if err != nil {
		return err
	}
	if err := s.sendEnvelope(item.PeerID, wire); err != nil {
		return s.scheduleGroupDeliveryRetry(item.MsgID, item.PeerID, item.RetryCount+1)
	}
	return s.store.MarkGroupDeliveryState(item.MsgID, item.PeerID, GroupDeliveryStateSentToTransport, time.Time{})
}

func (s *Service) buildGroupRetryEnvelope(item groupRetryDelivery) (any, error) {
	switch item.MsgType {
	case MessageTypeGroupChatText:
		return GroupChatText{
			Type:         MessageTypeGroupChatText,
			GroupID:      item.GroupID,
			Epoch:        item.Epoch,
			MsgID:        item.MsgID,
			SenderPeerID: item.SenderPeerID,
			SenderSeq:    item.SenderSeq,
			Ciphertext:   item.CiphertextBlob,
			SentAtUnix:   item.SentAtUnix,
			Signature:    item.Signature,
		}, nil
	case MessageTypeGroupChatFile:
		groupEpoch, err := s.store.GetGroupEpoch(item.GroupID, item.Epoch)
		if err != nil {
			return nil, err
		}
		nonce := protocol.BuildAEADNonce("fwd", item.SenderSeq)
		aad := []byte(item.GroupID + "\x00group_chat_file")
		ciphertext, err := protocol.AEADSeal(groupEpoch.WrappedKeyForLocal, nonce, item.CiphertextBlob, aad)
		if err != nil {
			return nil, err
		}
		return GroupChatFile{
			Type:         MessageTypeGroupChatFile,
			GroupID:      item.GroupID,
			Epoch:        item.Epoch,
			MsgID:        item.MsgID,
			SenderPeerID: item.SenderPeerID,
			SenderSeq:    item.SenderSeq,
			FileName:     item.FileName,
			MIMEType:     item.MIMEType,
			FileSize:     item.FileSize,
			Ciphertext:   ciphertext,
			SentAtUnix:   item.SentAtUnix,
			Signature:    item.Signature,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported group retry msg_type %q", item.MsgType)
	}
}

func (s *Service) scheduleGroupDeliveryRetry(msgID, peerID string, retryCount int) error {
	if retryCount < 1 {
		retryCount = 1
	}
	return s.store.QueueGroupDeliveryRetry(msgID, peerID, retryCount, time.Now().UTC().Add(groupRetryDelay(retryCount)))
}

func (s *Service) retryGroupEventDelivery(item groupRetryEventDelivery) error {
	env := GroupControlEnvelope{
		Type:          MessageTypeGroupControl,
		EventType:     item.EventType,
		GroupID:       item.GroupID,
		EventID:       item.EventID,
		EventSeq:      item.EventSeq,
		ActorPeerID:   item.ActorPeerID,
		SignerPeerID:  item.SignerPeerID,
		CreatedAtUnix: item.CreatedAtUnix,
		Payload:       json.RawMessage(item.PayloadJSON),
		Signature:     item.Signature,
	}
	if err := s.sendEnvelope(item.PeerID, env); err != nil {
		return s.scheduleGroupEventDeliveryRetry(item.EventID, item.PeerID, item.RetryCount+1)
	}
	return s.store.MarkGroupEventDeliverySent(item.EventID, item.PeerID)
}

func (s *Service) scheduleGroupEventDeliveryRetry(eventID, peerID string, retryCount int) error {
	if retryCount < 1 {
		retryCount = 1
	}
	return s.store.QueueGroupEventDeliveryRetry(eventID, peerID, retryCount, time.Now().UTC().Add(groupRetryDelay(retryCount)))
}

func groupRetryDelay(retryCount int) time.Duration {
	delay := groupRetryBaseDelay
	for i := 1; i < retryCount; i++ {
		delay *= 2
		if delay >= groupRetryMaxDelay {
			return groupRetryMaxDelay
		}
	}
	if delay > groupRetryMaxDelay {
		return groupRetryMaxDelay
	}
	return delay
}

func normalizeGroupTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", errors.New("group title is empty")
	}
	if len(title) > MaxGroupTitleLen {
		return "", errors.New("group title is too long")
	}
	return title, nil
}

func normalizeInviteRole(role string) (string, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return GroupRoleMember, nil
	}
	switch role {
	case GroupRoleAdmin, GroupRoleMember:
		return role, nil
	default:
		return "", errors.New("invalid group role")
	}
}

func newGroupEvent(groupID string, seq uint64, eventType, actorPeerID, signerPeerID string, createdAt time.Time, payload any) (GroupEvent, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return GroupEvent{}, err
	}
	return GroupEvent{
		EventID:      uuid.NewString(),
		GroupID:      groupID,
		EventSeq:     seq,
		EventType:    eventType,
		ActorPeerID:  actorPeerID,
		SignerPeerID: signerPeerID,
		PayloadJSON:  string(payloadJSON),
		Signature:    []byte{},
		CreatedAt:    createdAt,
	}, nil
}

func (s *Service) signGroupEvent(event *GroupEvent) error {
	if event.SignerPeerID != s.localPeer {
		return errors.New("group event signer must be local peer")
	}
	payload, err := marshalGroupControlEnvelopeForSigning(groupEnvelopeFromEvent(*event))
	if err != nil {
		return err
	}
	sig, err := s.signGroupPayload(payload)
	if err != nil {
		return err
	}
	event.Signature = sig
	return nil
}

func (s *Service) verifyGroupControlEnvelope(env GroupControlEnvelope) error {
	if err := validateGroupFutureTimestamp(env.CreatedAtUnix, "group control"); err != nil {
		return err
	}
	payload, err := marshalGroupControlEnvelopeForSigning(env)
	if err != nil {
		return err
	}
	if err := s.verifyGroupPayload(env.SignerPeerID, payload, env.Signature); err != nil {
		return err
	}
	return s.verifyGroupControlAuthority(env)
}

func (s *Service) verifyGroupControlAuthority(env GroupControlEnvelope) error {
	switch env.EventType {
	case GroupEventCreate:
		var payload GroupCreatePayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return err
		}
		if env.SignerPeerID != payload.ControllerPeerID || env.ActorPeerID != payload.ControllerPeerID {
			return errors.New("invalid group_create signer or actor")
		}
		return nil
	case GroupEventInvite, GroupEventRemove, GroupEventTitleUpdate, GroupEventRetentionUpdate, GroupEventMessageRevoke, GroupEventDissolve, GroupEventControllerTransfer, GroupEventJoin, GroupEventLeave:
		group, err := s.store.GetGroup(env.GroupID)
		if err != nil {
			if env.EventType == GroupEventInvite && errors.Is(err, sql.ErrNoRows) {
				var payload GroupInvitePayload
				if err := json.Unmarshal(env.Payload, &payload); err != nil {
					return err
				}
				if env.SignerPeerID != payload.ControllerPeerID || env.ActorPeerID != env.SignerPeerID {
					return errors.New("invalid group_invite signer or actor")
				}
				return nil
			}
			return err
		}
		if group.ControllerPeerID != env.SignerPeerID {
			if env.EventType != GroupEventRetentionUpdate && env.EventType != GroupEventMessageRevoke {
				return errors.New("group control signer is not current controller")
			}
		}
		if env.EventType == GroupEventRetentionUpdate {
			member, memberErr := s.store.GetGroupMember(env.GroupID, env.SignerPeerID)
			if memberErr != nil {
				return memberErr
			}
			if member.State != GroupMemberStateActive {
				return errors.New("group retention signer is not an active group member")
			}
			if member.Role != GroupRoleController && member.Role != GroupRoleAdmin {
				return errors.New("group retention signer is not controller or admin")
			}
		}
		if env.EventType == GroupEventMessageRevoke {
			if env.ActorPeerID != env.SignerPeerID {
				return errors.New("group message revoke actor must match signer")
			}
			var payload GroupMessageRevokePayload
			if err := json.Unmarshal(env.Payload, &payload); err != nil {
				return err
			}
			if payload.MsgID == "" || payload.SenderPeerID == "" {
				return errors.New("group message revoke payload is incomplete")
			}
			signerMember, memberErr := s.store.GetGroupMember(env.GroupID, env.SignerPeerID)
			if memberErr != nil {
				return memberErr
			}
			if signerMember.State != GroupMemberStateActive {
				return errors.New("group message revoke signer is not an active member")
			}
			if signerMember.PeerID == payload.SenderPeerID {
				return nil
			}
			targetMember, targetErr := s.store.GetGroupMember(env.GroupID, payload.SenderPeerID)
			if targetErr != nil {
				return targetErr
			}
			switch signerMember.Role {
			case GroupRoleController:
				return nil
			case GroupRoleAdmin:
				if targetMember.Role == GroupRoleController {
					return errors.New("admin cannot revoke controller messages")
				}
				return nil
			default:
				return errors.New("member cannot revoke other people's messages")
			}
		}
		switch env.EventType {
		case GroupEventInvite, GroupEventRemove, GroupEventTitleUpdate, GroupEventRetentionUpdate, GroupEventDissolve, GroupEventControllerTransfer:
			if env.ActorPeerID != env.SignerPeerID {
				return errors.New("group control actor must match signer")
			}
		}
		return nil
	default:
		return nil
	}
}

func (s *Service) signGroupJoinRequest(req *GroupJoinRequest) error {
	payload, err := marshalGroupJoinRequestForSigning(*req)
	if err != nil {
		return err
	}
	sig, err := s.signGroupPayload(payload)
	if err != nil {
		return err
	}
	req.Signature = sig
	return nil
}

func (s *Service) verifyGroupJoinRequest(req GroupJoinRequest) error {
	if err := validateGroupRecentTimestamp(req.SentAtUnix, "group join request", groupRequestMaxAge); err != nil {
		return err
	}
	payload, err := marshalGroupJoinRequestForSigning(req)
	if err != nil {
		return err
	}
	return s.verifyGroupPayload(req.JoinerPeerID, payload, req.Signature)
}

func (s *Service) signGroupLeaveRequest(req *GroupLeaveRequest) error {
	payload, err := marshalGroupLeaveRequestForSigning(*req)
	if err != nil {
		return err
	}
	sig, err := s.signGroupPayload(payload)
	if err != nil {
		return err
	}
	req.Signature = sig
	return nil
}

func (s *Service) verifyGroupLeaveRequest(req GroupLeaveRequest) error {
	if err := validateGroupRecentTimestamp(req.SentAtUnix, "group leave request", groupRequestMaxAge); err != nil {
		return err
	}
	payload, err := marshalGroupLeaveRequestForSigning(req)
	if err != nil {
		return err
	}
	return s.verifyGroupPayload(req.LeaverPeerID, payload, req.Signature)
}

func (s *Service) signGroupChatText(msg *GroupChatText) error {
	payload, err := marshalGroupChatTextForSigning(*msg)
	if err != nil {
		return err
	}
	sig, err := s.signGroupPayload(payload)
	if err != nil {
		return err
	}
	msg.Signature = sig
	return nil
}

func (s *Service) verifyGroupChatText(msg GroupChatText) error {
	if err := validateGroupFutureTimestamp(msg.SentAtUnix, "group chat text"); err != nil {
		return err
	}
	payload, err := marshalGroupChatTextForSigning(msg)
	if err != nil {
		return err
	}
	return s.verifyGroupPayload(msg.SenderPeerID, payload, msg.Signature)
}

func (s *Service) signGroupChatFile(msg *GroupChatFile) error {
	payload, err := marshalGroupChatFileForSigning(*msg)
	if err != nil {
		return err
	}
	sig, err := s.signGroupPayload(payload)
	if err != nil {
		return err
	}
	msg.Signature = sig
	return nil
}

func (s *Service) verifyGroupChatFile(msg GroupChatFile) error {
	if err := validateGroupFutureTimestamp(msg.SentAtUnix, "group chat file"); err != nil {
		return err
	}
	payload, err := marshalGroupChatFileForSigning(msg)
	if err != nil {
		return err
	}
	return s.verifyGroupPayload(msg.SenderPeerID, payload, msg.Signature)
}

func (s *Service) signGroupDeliveryAck(ack *GroupDeliveryAck) error {
	payload, err := marshalGroupDeliveryAckForSigning(*ack)
	if err != nil {
		return err
	}
	sig, err := s.signGroupPayload(payload)
	if err != nil {
		return err
	}
	ack.Signature = sig
	return nil
}

func (s *Service) verifyGroupDeliveryAck(ack GroupDeliveryAck) error {
	if err := validateGroupFutureTimestamp(ack.AckedAtUnix, "group delivery ack"); err != nil {
		return err
	}
	payload, err := marshalGroupDeliveryAckForSigning(ack)
	if err != nil {
		return err
	}
	return s.verifyGroupPayload(ack.FromPeerID, payload, ack.Signature)
}

func (s *Service) signGroupPayload(payload []byte) ([]byte, error) {
	priv := s.host.Peerstore().PrivKey(s.host.ID())
	if priv == nil {
		return nil, errors.New("local signing key is unavailable")
	}
	return priv.Sign(payload)
}

func (s *Service) verifyGroupPayload(peerID string, payload, signature []byte) error {
	if len(signature) == 0 {
		return errors.New("missing group message signature")
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return err
	}
	pub := s.host.Peerstore().PubKey(pid)
	if pub == nil {
		pub, err = pid.ExtractPublicKey()
		if err != nil {
			return fmt.Errorf("resolve peer public key: %w", err)
		}
	}
	ok, err := pub.Verify(payload, signature)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("group message signature verification failed")
	}
	return nil
}

func validateGroupFutureTimestamp(unixMillis int64, label string) error {
	if unixMillis <= 0 {
		return fmt.Errorf("%s timestamp is missing", label)
	}
	if time.UnixMilli(unixMillis).UTC().After(time.Now().UTC().Add(groupMaxFutureSkew)) {
		return fmt.Errorf("%s timestamp is too far in the future", label)
	}
	return nil
}

func validateGroupRecentTimestamp(unixMillis int64, label string, maxAge time.Duration) error {
	if err := validateGroupFutureTimestamp(unixMillis, label); err != nil {
		return err
	}
	if maxAge <= 0 {
		return nil
	}
	if time.Since(time.UnixMilli(unixMillis).UTC()) > maxAge {
		return fmt.Errorf("%s timestamp is too old", label)
	}
	return nil
}

func shouldApplyGroupEvent(lastEventSeq, incomingEventSeq uint64) (bool, error) {
	switch {
	case incomingEventSeq <= lastEventSeq:
		return false, nil
	case incomingEventSeq != lastEventSeq+1:
		return false, fmt.Errorf("group event seq gap: local=%d incoming=%d", lastEventSeq, incomingEventSeq)
	default:
		return true, nil
	}
}

func marshalGroupJoinRequestForSigning(req GroupJoinRequest) ([]byte, error) {
	return json.Marshal(struct {
		Type          string `json:"type"`
		GroupID       string `json:"group_id"`
		JoinerPeerID  string `json:"joiner_peer_id"`
		AcceptedEpoch uint64 `json:"accepted_epoch"`
		SentAtUnix    int64  `json:"sent_at_unix"`
	}{
		Type:          req.Type,
		GroupID:       req.GroupID,
		JoinerPeerID:  req.JoinerPeerID,
		AcceptedEpoch: req.AcceptedEpoch,
		SentAtUnix:    req.SentAtUnix,
	})
}

func marshalGroupLeaveRequestForSigning(req GroupLeaveRequest) ([]byte, error) {
	return json.Marshal(struct {
		Type         string `json:"type"`
		GroupID      string `json:"group_id"`
		LeaverPeerID string `json:"leaver_peer_id"`
		Reason       string `json:"reason"`
		SentAtUnix   int64  `json:"sent_at_unix"`
	}{
		Type:         req.Type,
		GroupID:      req.GroupID,
		LeaverPeerID: req.LeaverPeerID,
		Reason:       req.Reason,
		SentAtUnix:   req.SentAtUnix,
	})
}

func marshalGroupControlEnvelopeForSigning(env GroupControlEnvelope) ([]byte, error) {
	return json.Marshal(struct {
		Type          string          `json:"type"`
		EventType     string          `json:"event_type"`
		GroupID       string          `json:"group_id"`
		EventID       string          `json:"event_id"`
		EventSeq      uint64          `json:"event_seq"`
		ActorPeerID   string          `json:"actor_peer_id"`
		SignerPeerID  string          `json:"signer_peer_id"`
		CreatedAtUnix int64           `json:"created_at_unix"`
		Payload       json.RawMessage `json:"payload"`
	}{
		Type:          env.Type,
		EventType:     env.EventType,
		GroupID:       env.GroupID,
		EventID:       env.EventID,
		EventSeq:      env.EventSeq,
		ActorPeerID:   env.ActorPeerID,
		SignerPeerID:  env.SignerPeerID,
		CreatedAtUnix: env.CreatedAtUnix,
		Payload:       env.Payload,
	})
}

func marshalGroupChatTextForSigning(msg GroupChatText) ([]byte, error) {
	return json.Marshal(struct {
		Type         string `json:"type"`
		GroupID      string `json:"group_id"`
		Epoch        uint64 `json:"epoch"`
		MsgID        string `json:"msg_id"`
		SenderPeerID string `json:"sender_peer_id"`
		SenderSeq    uint64 `json:"sender_seq"`
		Ciphertext   []byte `json:"ciphertext"`
		SentAtUnix   int64  `json:"sent_at_unix"`
	}{
		Type:         msg.Type,
		GroupID:      msg.GroupID,
		Epoch:        msg.Epoch,
		MsgID:        msg.MsgID,
		SenderPeerID: msg.SenderPeerID,
		SenderSeq:    msg.SenderSeq,
		Ciphertext:   msg.Ciphertext,
		SentAtUnix:   msg.SentAtUnix,
	})
}

func marshalGroupChatFileForSigning(msg GroupChatFile) ([]byte, error) {
	return json.Marshal(struct {
		Type         string `json:"type"`
		GroupID      string `json:"group_id"`
		Epoch        uint64 `json:"epoch"`
		MsgID        string `json:"msg_id"`
		SenderPeerID string `json:"sender_peer_id"`
		SenderSeq    uint64 `json:"sender_seq"`
		FileName     string `json:"file_name"`
		MIMEType     string `json:"mime_type"`
		FileSize     int64  `json:"file_size"`
		Ciphertext   []byte `json:"ciphertext"`
		SentAtUnix   int64  `json:"sent_at_unix"`
	}{
		Type:         msg.Type,
		GroupID:      msg.GroupID,
		Epoch:        msg.Epoch,
		MsgID:        msg.MsgID,
		SenderPeerID: msg.SenderPeerID,
		SenderSeq:    msg.SenderSeq,
		FileName:     msg.FileName,
		MIMEType:     msg.MIMEType,
		FileSize:     msg.FileSize,
		Ciphertext:   msg.Ciphertext,
		SentAtUnix:   msg.SentAtUnix,
	})
}

func marshalGroupDeliveryAckForSigning(ack GroupDeliveryAck) ([]byte, error) {
	return json.Marshal(struct {
		Type        string `json:"type"`
		GroupID     string `json:"group_id"`
		MsgID       string `json:"msg_id"`
		FromPeerID  string `json:"from_peer_id"`
		ToPeerID    string `json:"to_peer_id"`
		AckedAtUnix int64  `json:"acked_at_unix"`
	}{
		Type:        ack.Type,
		GroupID:     ack.GroupID,
		MsgID:       ack.MsgID,
		FromPeerID:  ack.FromPeerID,
		ToPeerID:    ack.ToPeerID,
		AckedAtUnix: ack.AckedAtUnix,
	})
}

func (s *Service) processGroupEnvelope(env map[string]any) error {
	switch env["type"] {
	case MessageTypeGroupControl:
		var ctrl GroupControlEnvelope
		if err := remarshal(env, &ctrl); err != nil {
			return err
		}
		if err := s.verifyGroupControlEnvelope(ctrl); err != nil {
			return err
		}
		return s.applyGroupControlEnvelope(ctrl)
	case MessageTypeGroupJoinRequest:
		var req GroupJoinRequest
		if err := remarshal(env, &req); err != nil {
			return err
		}
		if err := s.verifyGroupJoinRequest(req); err != nil {
			return err
		}
		return s.handleRemoteGroupJoinRequest(req)
	case MessageTypeGroupLeaveRequest:
		var req GroupLeaveRequest
		if err := remarshal(env, &req); err != nil {
			return err
		}
		if err := s.verifyGroupLeaveRequest(req); err != nil {
			return err
		}
		return s.handleRemoteGroupLeaveRequest(req)
	case MessageTypeGroupChatText:
		var msg GroupChatText
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		if err := s.verifyGroupChatText(msg); err != nil {
			return err
		}
		return s.handleIncomingGroupText(msg)
	case MessageTypeGroupChatFile:
		var msg GroupChatFile
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		if err := s.verifyGroupChatFile(msg); err != nil {
			return err
		}
		return s.handleIncomingGroupFile(msg)
	case MessageTypeGroupDeliveryAck:
		var ack GroupDeliveryAck
		if err := remarshal(env, &ack); err != nil {
			return err
		}
		if err := s.verifyGroupDeliveryAck(ack); err != nil {
			return err
		}
		return s.handleIncomingGroupAck(ack)
	default:
		return nil
	}
}

func (s *Service) applyGroupControlEnvelope(env GroupControlEnvelope) error {
	if env.EventType != GroupEventCreate && env.EventType != GroupEventDissolve {
		group, err := s.store.GetGroup(env.GroupID)
		if err == nil && group.State == GroupStateArchived {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	switch env.EventType {
	case GroupEventCreate:
		return s.applyRemoteGroupCreate(env)
	case GroupEventInvite:
		return s.applyRemoteGroupInvite(env)
	case GroupEventJoin:
		return s.applyRemoteGroupJoin(env)
	case GroupEventLeave:
		return s.applyRemoteGroupLeave(env)
	case GroupEventRemove:
		return s.applyRemoteGroupRemove(env)
	case GroupEventTitleUpdate:
		return s.applyRemoteGroupTitleUpdate(env)
	case GroupEventRetentionUpdate:
		return s.applyRemoteGroupRetentionUpdate(env)
	case GroupEventMessageRevoke:
		return s.applyRemoteGroupMessageRevoke(env)
	case GroupEventDissolve:
		return s.applyRemoteGroupDissolve(env)
	case GroupEventControllerTransfer:
		return s.applyRemoteGroupControllerTransfer(env)
	default:
		return nil
	}
}

func (s *Service) handleRemoteGroupJoinRequest(req GroupJoinRequest) error {
	group, err := s.store.GetGroup(req.GroupID)
	if err != nil {
		return err
	}
	if group.State != GroupStateActive {
		return nil
	}
	if group.ControllerPeerID != s.localPeer {
		return nil
	}
	member, err := s.store.GetGroupMember(req.GroupID, req.JoinerPeerID)
	if err != nil {
		return err
	}
	if member.State != GroupMemberStateInvited {
		return nil
	}
	now := time.Now().UTC()
	payload := GroupJoinPayload{
		JoinerPeerID:  req.JoinerPeerID,
		AcceptedEpoch: req.AcceptedEpoch,
	}
	event, err := newGroupEvent(req.GroupID, group.LastEventSeq+1, GroupEventJoin, req.JoinerPeerID, s.localPeer, now, payload)
	if err != nil {
		return err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return err
	}
	updated, err := s.store.ActivateGroupMember(req.GroupID, req.JoinerPeerID, group.CurrentEpoch, event)
	if err != nil {
		return err
	}
	members, _ := s.store.ListGroupMembers(req.GroupID)
	recipients := peersFromMembers(members, s.localPeer)
	s.broadcastGroupEvent(req.GroupID, event, recipients...)
	_ = updated
	return nil
}

func (s *Service) handleRemoteGroupLeaveRequest(req GroupLeaveRequest) error {
	group, err := s.store.GetGroup(req.GroupID)
	if err != nil {
		return err
	}
	if group.State != GroupStateActive {
		return nil
	}
	if group.ControllerPeerID != s.localPeer {
		return nil
	}
	member, err := s.store.GetGroupMember(req.GroupID, req.LeaverPeerID)
	if err != nil {
		return err
	}
	if member.State != GroupMemberStateActive {
		return nil
	}
	now := time.Now().UTC()
	payload := GroupLeavePayload{
		LeaverPeerID: req.LeaverPeerID,
		Reason:       req.Reason,
	}
	members, err := s.store.ListGroupMembers(req.GroupID)
	if err != nil {
		return err
	}
	payload.NewEpoch = group.CurrentEpoch + 1
	payload.EpochKeys, err = generateGroupEpochKeys(activePeerIDsExcluding(members, req.LeaverPeerID))
	if err != nil {
		return err
	}
	event, err := newGroupEvent(req.GroupID, group.LastEventSeq+1, GroupEventLeave, req.LeaverPeerID, s.localPeer, now, payload)
	if err != nil {
		return err
	}
	if err := s.signGroupEvent(&event); err != nil {
		return err
	}
	nextEpoch := GroupEpoch{
		GroupID:            req.GroupID,
		Epoch:              group.CurrentEpoch + 1,
		WrappedKeyForLocal: payload.EpochKeys[s.localPeer],
		CreatedAt:          now,
	}
	if _, err := s.store.MarkGroupMemberLeft(req.GroupID, req.LeaverPeerID, group.CurrentEpoch, nextEpoch, event); err != nil {
		return err
	}
	recipients := peersFromMembers(members, s.localPeer)
	recipients = appendIfMissing(recipients, req.LeaverPeerID, s.localPeer)
	s.broadcastGroupEvent(req.GroupID, event, recipients...)
	return nil
}

func (s *Service) applyRemoteGroupCreate(env GroupControlEnvelope) error {
	if group, err := s.store.GetGroup(env.GroupID); err == nil {
		shouldApply, err := shouldApplyGroupEvent(group.LastEventSeq, env.EventSeq)
		if err != nil || !shouldApply {
			return err
		}
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if env.EventSeq != 1 {
		return fmt.Errorf("group create event_seq must be 1, got %d", env.EventSeq)
	}
	var payload GroupCreatePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	now := time.UnixMilli(env.CreatedAtUnix).UTC()
	members := make([]GroupMember, 0, len(payload.InitialMembers))
	for _, peerID := range payload.InitialMembers {
		role := GroupRoleMember
		state := GroupMemberStateInvited
		joinedEpoch := uint64(0)
		if peerID == payload.ControllerPeerID {
			role = GroupRoleController
			state = GroupMemberStateActive
			joinedEpoch = payload.Epoch
		}
		members = append(members, GroupMember{
			GroupID:     env.GroupID,
			PeerID:      peerID,
			Role:        role,
			State:       state,
			InvitedBy:   payload.ControllerPeerID,
			JoinedEpoch: joinedEpoch,
			UpdatedAt:   now,
		})
		_ = s.store.UpsertPeer(peerID, "")
	}
	group := Group{
		GroupID:          env.GroupID,
		Title:            payload.Title,
		ControllerPeerID: payload.ControllerPeerID,
		CurrentEpoch:     payload.Epoch,
		State:            GroupStateActive,
		LastEventSeq:     env.EventSeq,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	event := groupEventFromEnvelope(env)
	epoch := GroupEpoch{
		GroupID:            env.GroupID,
		Epoch:              payload.Epoch,
		WrappedKeyForLocal: payload.EpochKeys[s.localPeer],
		CreatedAt:          now,
	}
	_, err := s.store.CreateGroup(group, epoch, members, event)
	return ignoreDuplicateCreate(err)
}

func (s *Service) applyRemoteGroupInvite(env GroupControlEnvelope) error {
	return s.applyRemoteGroupInviteEnvelope(env, true)
}

func (s *Service) applyRemoteGroupInviteEnvelope(env GroupControlEnvelope, appendNotice bool) error {
	group, err := s.ensureGroupForInviteEnvelope(env)
	if err != nil {
		return err
	}
	shouldApply, err := shouldApplyGroupEvent(group.LastEventSeq, env.EventSeq)
	if err != nil || !shouldApply {
		return err
	}
	var payload GroupInvitePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	now := time.UnixMilli(env.CreatedAtUnix).UTC()
	member := GroupMember{
		GroupID:   env.GroupID,
		PeerID:    payload.InviteePeerID,
		Role:      payload.Role,
		State:     GroupMemberStateInvited,
		InvitedBy: env.ActorPeerID,
		UpdatedAt: now,
	}
	if err := s.store.UpsertGroupEpoch(GroupEpoch{
		GroupID:            env.GroupID,
		Epoch:              payload.Epoch,
		WrappedKeyForLocal: payload.WrappedGroupKey,
		CreatedAt:          now,
	}); err != nil {
		return err
	}
	updated, err := s.store.UpsertGroupMember(env.GroupID, member, groupEventFromEnvelope(env))
	if err != nil {
		return err
	}
	if appendNotice && payload.InviteePeerID == s.localPeer {
		s.appendGroupInviteNotice(updated, payload, env)
	}
	return nil
}

func (s *Service) applyRemoteGroupJoin(env GroupControlEnvelope) error {
	group, err := s.store.GetGroup(env.GroupID)
	if err != nil {
		return err
	}
	shouldApply, err := shouldApplyGroupEvent(group.LastEventSeq, env.EventSeq)
	if err != nil || !shouldApply {
		return err
	}
	var payload GroupJoinPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	_, err = s.store.ActivateGroupMember(env.GroupID, payload.JoinerPeerID, group.CurrentEpoch, groupEventFromEnvelope(env))
	return err
}

func (s *Service) applyRemoteGroupLeave(env GroupControlEnvelope) error {
	group, err := s.store.GetGroup(env.GroupID)
	if err != nil {
		return err
	}
	shouldApply, err := shouldApplyGroupEvent(group.LastEventSeq, env.EventSeq)
	if err != nil || !shouldApply {
		return err
	}
	var payload GroupLeavePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	nextEpoch := GroupEpoch{
		GroupID:            env.GroupID,
		Epoch:              payload.NewEpoch,
		WrappedKeyForLocal: payload.EpochKeys[s.localPeer],
		CreatedAt:          time.UnixMilli(env.CreatedAtUnix).UTC(),
	}
	_, err = s.store.MarkGroupMemberLeft(env.GroupID, payload.LeaverPeerID, group.CurrentEpoch, nextEpoch, groupEventFromEnvelope(env))
	return err
}

func (s *Service) applyRemoteGroupRemove(env GroupControlEnvelope) error {
	group, err := s.store.GetGroup(env.GroupID)
	if err != nil {
		return err
	}
	shouldApply, err := shouldApplyGroupEvent(group.LastEventSeq, env.EventSeq)
	if err != nil || !shouldApply {
		return err
	}
	var payload GroupRemovePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	nextEpoch := GroupEpoch{
		GroupID:            env.GroupID,
		Epoch:              payload.NewEpoch,
		WrappedKeyForLocal: payload.EpochKeys[s.localPeer],
		CreatedAt:          time.UnixMilli(env.CreatedAtUnix).UTC(),
	}
	_, err = s.store.MarkGroupMemberRemoved(env.GroupID, payload.TargetPeerID, group.CurrentEpoch, nextEpoch, groupEventFromEnvelope(env))
	return err
}

func (s *Service) applyRemoteGroupTitleUpdate(env GroupControlEnvelope) error {
	group, err := s.store.GetGroup(env.GroupID)
	if err != nil {
		return err
	}
	shouldApply, err := shouldApplyGroupEvent(group.LastEventSeq, env.EventSeq)
	if err != nil || !shouldApply {
		return err
	}
	var payload GroupTitleUpdatePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	_, err = s.store.UpdateGroupTitle(env.GroupID, payload.Title, groupEventFromEnvelope(env))
	return err
}

func (s *Service) applyRemoteGroupRetentionUpdate(env GroupControlEnvelope) error {
	group, err := s.store.GetGroup(env.GroupID)
	if err != nil {
		return err
	}
	shouldApply, err := shouldApplyGroupEvent(group.LastEventSeq, env.EventSeq)
	if err != nil || !shouldApply {
		return err
	}
	var payload GroupRetentionUpdatePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	_, err = s.store.UpdateGroupRetention(env.GroupID, payload.RetentionMinutes, groupEventFromEnvelope(env))
	return err
}

func (s *Service) applyRemoteGroupMessageRevoke(env GroupControlEnvelope) error {
	group, err := s.store.GetGroup(env.GroupID)
	if err != nil {
		return err
	}
	shouldApply, err := shouldApplyGroupEvent(group.LastEventSeq, env.EventSeq)
	if err != nil || !shouldApply {
		return err
	}
	var payload GroupMessageRevokePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	if payload.MsgID == "" || payload.SenderPeerID == "" {
		return errors.New("group message revoke payload is incomplete")
	}
	if msg, err := s.store.GetGroupMessage(payload.MsgID); err == nil && msg.SenderPeerID != payload.SenderPeerID {
		return errors.New("group message revoke sender does not match target message")
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return s.store.RevokeGroupMessage(env.GroupID, payload.MsgID, payload.SenderPeerID, env.ActorPeerID, groupEventFromEnvelope(env))
}

func (s *Service) applyRemoteGroupDissolve(env GroupControlEnvelope) error {
	group, err := s.store.GetGroup(env.GroupID)
	if err != nil {
		return err
	}
	shouldApply, err := shouldApplyGroupEvent(group.LastEventSeq, env.EventSeq)
	if err != nil || !shouldApply {
		return err
	}
	_, err = s.store.DissolveGroup(env.GroupID, groupEventFromEnvelope(env))
	return err
}

func (s *Service) applyRemoteGroupControllerTransfer(env GroupControlEnvelope) error {
	group, err := s.store.GetGroup(env.GroupID)
	if err != nil {
		return err
	}
	shouldApply, err := shouldApplyGroupEvent(group.LastEventSeq, env.EventSeq)
	if err != nil || !shouldApply {
		return err
	}
	var payload GroupControllerTransferPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return err
	}
	nextEpoch := GroupEpoch{
		GroupID:            env.GroupID,
		Epoch:              payload.NewEpoch,
		WrappedKeyForLocal: payload.EpochKeys[s.localPeer],
		CreatedAt:          time.UnixMilli(env.CreatedAtUnix).UTC(),
	}
	_, err = s.store.TransferGroupController(env.GroupID, payload.FromPeerID, payload.ToPeerID, nextEpoch, groupEventFromEnvelope(env))
	return err
}

func (s *Service) ensureGroupForInviteEnvelope(env GroupControlEnvelope) (Group, error) {
	group, err := s.store.GetGroup(env.GroupID)
	if err == nil {
		return group, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Group{}, err
	}
	var payload GroupInvitePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return Group{}, err
	}
	now := time.UnixMilli(env.CreatedAtUnix).UTC()
	initialMembers := append([]string{payload.ControllerPeerID}, payload.KnownMembers...)
	initialMembers = uniquePeers(initialMembers)
	members := make([]GroupMember, 0, len(initialMembers))
	for _, peerID := range initialMembers {
		role := GroupRoleMember
		state := GroupMemberStateActive
		if peerID == payload.ControllerPeerID {
			role = GroupRoleController
		}
		if peerID == payload.InviteePeerID {
			state = GroupMemberStateInvited
		}
		members = append(members, GroupMember{
			GroupID:   env.GroupID,
			PeerID:    peerID,
			Role:      role,
			State:     state,
			InvitedBy: payload.ControllerPeerID,
			UpdatedAt: now,
		})
		_ = s.store.UpsertPeer(peerID, "")
	}
	group = Group{
		GroupID:          env.GroupID,
		Title:            payload.Title,
		ControllerPeerID: payload.ControllerPeerID,
		CurrentEpoch:     payload.Epoch,
		State:            GroupStateActive,
		LastEventSeq:     env.EventSeq - 1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	createEvent, err := newGroupEvent(env.GroupID, 0, GroupEventCreate, payload.ControllerPeerID, payload.ControllerPeerID, now, GroupCreatePayload{
		Title:            payload.Title,
		ControllerPeerID: payload.ControllerPeerID,
		InitialMembers:   initialMembers,
		Epoch:            payload.Epoch,
		EpochKeys:        map[string][]byte{s.localPeer: payload.WrappedGroupKey},
	})
	if err != nil {
		return Group{}, err
	}
	epoch := GroupEpoch{
		GroupID:            env.GroupID,
		Epoch:              payload.Epoch,
		WrappedKeyForLocal: payload.WrappedGroupKey,
		CreatedAt:          now,
	}
	if _, err := s.store.CreateGroup(group, epoch, members, createEvent); err != nil {
		if err := ignoreDuplicateCreate(err); err != nil {
			return Group{}, err
		}
	}
	return s.store.GetGroup(env.GroupID)
}

func (s *Service) broadcastGroupEvent(groupID string, event GroupEvent, peerIDs ...string) {
	env := groupEnvelopeFromEvent(event)
	for _, peerID := range uniquePeers(peerIDs) {
		if peerID == "" || peerID == s.localPeer {
			continue
		}
		if err := s.sendEnvelope(peerID, env); err != nil {
			_ = s.scheduleGroupEventDeliveryRetry(event.EventID, peerID, 1)
			log.Printf("[group] broadcast event=%s group=%s peer=%s failed: %v", event.EventType, groupID, peerID, err)
			continue
		}
		_ = s.store.MarkGroupEventDeliverySent(event.EventID, peerID)
	}
}

func groupEnvelopeFromEvent(event GroupEvent) GroupControlEnvelope {
	return GroupControlEnvelope{
		Type:          MessageTypeGroupControl,
		EventType:     event.EventType,
		GroupID:       event.GroupID,
		EventID:       event.EventID,
		EventSeq:      event.EventSeq,
		ActorPeerID:   event.ActorPeerID,
		SignerPeerID:  event.SignerPeerID,
		CreatedAtUnix: event.CreatedAt.UnixMilli(),
		Payload:       json.RawMessage(event.PayloadJSON),
		Signature:     event.Signature,
	}
}

func groupEventFromEnvelope(env GroupControlEnvelope) GroupEvent {
	return GroupEvent{
		EventID:      env.EventID,
		GroupID:      env.GroupID,
		EventSeq:     env.EventSeq,
		EventType:    env.EventType,
		ActorPeerID:  env.ActorPeerID,
		SignerPeerID: env.SignerPeerID,
		PayloadJSON:  string(env.Payload),
		Signature:    env.Signature,
		CreatedAt:    time.UnixMilli(env.CreatedAtUnix).UTC(),
	}
}

func peersFromMembers(members []GroupMember, exclude string) []string {
	out := make([]string, 0, len(members))
	for _, member := range members {
		if member.PeerID == exclude {
			continue
		}
		if member.State == GroupMemberStateActive || member.State == GroupMemberStateInvited {
			out = append(out, member.PeerID)
		}
	}
	return uniquePeers(out)
}

func activePeerIDs(members []GroupMember) []string {
	out := make([]string, 0, len(members))
	for _, member := range members {
		if member.State == GroupMemberStateActive {
			out = append(out, member.PeerID)
		}
	}
	return uniquePeers(out)
}

func appendIfMissing(peers []string, peerID string, exclude string) []string {
	if peerID == "" || peerID == exclude {
		return peers
	}
	for _, existing := range peers {
		if existing == peerID {
			return peers
		}
	}
	return append(peers, peerID)
}

func uniquePeers(peers []string) []string {
	seen := make(map[string]struct{}, len(peers))
	out := make([]string, 0, len(peers))
	for _, peerID := range peers {
		peerID = strings.TrimSpace(peerID)
		if peerID == "" {
			continue
		}
		if _, ok := seen[peerID]; ok {
			continue
		}
		seen[peerID] = struct{}{}
		out = append(out, peerID)
	}
	return out
}

func (s *Service) appendGroupInviteNotice(group Group, payload GroupInvitePayload, env GroupControlEnvelope) {
	conv, err := s.store.GetConversationByPeer(payload.ControllerPeerID)
	if err != nil {
		return
	}
	noticePayload, err := json.Marshal(buildGroupInviteNoticePayload(group, payload, env, GroupMemberStateInvited))
	if err != nil {
		return
	}
	_, _ = s.store.AddMessage(Message{
		MsgID:          "group-invite-" + env.EventID,
		ConversationID: conv.ConversationID,
		SenderPeerID:   payload.ControllerPeerID,
		ReceiverPeerID: s.localPeer,
		Direction:      "inbound",
		MsgType:        MessageTypeGroupInviteNote,
		Plaintext:      string(noticePayload),
		TransportMode:  TransportModeDirect,
		State:          MessageStateReceived,
		Counter:        0,
		CreatedAt:      time.UnixMilli(env.CreatedAtUnix).UTC(),
	}, nil)
}

func (s *Service) sendDirectGroupInviteNotice(peerID string, group Group, payload GroupInvitePayload, env GroupControlEnvelope) error {
	conv, err := s.store.GetConversationByPeer(peerID)
	if err != nil {
		return err
	}
	sess, err := s.store.GetSessionState(conv.ConversationID)
	if err != nil {
		return err
	}
	noticePayload, err := json.Marshal(buildGroupInviteNoticePayload(group, payload, env, ""))
	if err != nil {
		return err
	}
	counter := sess.SendCounter
	nonce := protocol.BuildAEADNonce("fwd", counter)
	aad := []byte(conv.ConversationID + "\x00" + MessageTypeGroupInviteNotice)
	ciphertext, err := protocol.AEADSeal(sess.SendKey, nonce, noticePayload, aad)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	msgID := "group-invite-" + env.EventID
	localMsg := Message{
		MsgID:          msgID,
		ConversationID: conv.ConversationID,
		SenderPeerID:   s.localPeer,
		ReceiverPeerID: peerID,
		Direction:      "outbound",
		MsgType:        MessageTypeGroupInviteNotice,
		Plaintext:      string(noticePayload),
		TransportMode:  TransportModeDirect,
		State:          MessageStateLocalOnly,
		Counter:        counter,
		CreatedAt:      now,
	}
	if _, err := s.store.AddMessage(localMsg, ciphertext); err != nil {
		return err
	}
	if err := s.store.UpdateSendCounter(conv.ConversationID, counter+1); err != nil {
		return err
	}
	if err := s.store.UpsertOutboxJob(msgID, peerID, MessageStateQueuedForRetry, 0, time.Now().UTC(), time.Time{}); err != nil {
		return err
	}
	if err := s.sendStoredDirectMessage(localMsg, ciphertext, nil); err != nil {
		localMsg.State = MessageStateQueuedForRetry
		if _, updateErr := s.store.AddMessage(localMsg, ciphertext); updateErr != nil {
			return updateErr
		}
		return s.scheduleOutboxRetry(localMsg.MsgID, peerID, 1)
	}
	localMsg.State = MessageStateSentToTransport
	if _, err := s.store.AddMessage(localMsg, ciphertext); err != nil {
		return err
	}
	return s.markOutboxSentToTransport(localMsg.MsgID, peerID, 0, time.Now().UTC())
}

func buildGroupInviteNoticePayload(group Group, payload GroupInvitePayload, env GroupControlEnvelope, localMemberState string) GroupInviteNoticePayload {
	return GroupInviteNoticePayload{
		GroupID:          group.GroupID,
		Title:            group.Title,
		ControllerPeerID: payload.ControllerPeerID,
		InviteePeerID:    payload.InviteePeerID,
		InviteText:       payload.InviteText,
		EventID:          env.EventID,
		EventSeq:         env.EventSeq,
		CurrentEpoch:     group.CurrentEpoch,
		LocalMemberState: localMemberState,
		InviteEnvelope:   env,
	}
}

func (s *Service) normalizeInitialInvitees(memberPeerIDs []string) ([]string, error) {
	seen := map[string]struct{}{s.localPeer: {}}
	out := make([]string, 0, len(memberPeerIDs))
	for _, peerID := range memberPeerIDs {
		peerID = strings.TrimSpace(peerID)
		if peerID == "" {
			continue
		}
		if _, ok := seen[peerID]; ok {
			continue
		}
		if _, err := s.store.GetConversationByPeer(peerID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("group invites require an existing direct conversation: %s", peerID)
			}
			return nil, err
		}
		seen[peerID] = struct{}{}
		out = append(out, peerID)
	}
	return out, nil
}

func ignoreDuplicateCreate(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return nil
	}
	return err
}

func (s *Service) handleIncomingGroupText(msg GroupChatText) error {
	group, err := s.store.GetGroup(msg.GroupID)
	if err != nil {
		return err
	}
	revoked, err := s.store.IsGroupMessageRevoked(msg.GroupID, msg.MsgID)
	if err != nil {
		return err
	}
	if revoked {
		return s.sendGroupDeliveryAck(msg.GroupID, msg.MsgID, msg.SenderPeerID)
	}
	member, err := s.store.GetGroupMember(msg.GroupID, msg.SenderPeerID)
	if err != nil {
		return err
	}
	if member.State != GroupMemberStateActive {
		return nil
	}
	groupEpoch, err := s.store.GetGroupEpoch(msg.GroupID, msg.Epoch)
	if err != nil {
		return err
	}
	nonce := protocol.BuildAEADNonce("fwd", msg.SenderSeq)
	aad := []byte(msg.GroupID + "\x00group_chat_text")
	plain, err := protocol.AEADOpen(groupEpoch.WrappedKeyForLocal, nonce, msg.Ciphertext, aad)
	if err != nil {
		return err
	}
	incoming := GroupMessage{
		MsgID:        msg.MsgID,
		GroupID:      msg.GroupID,
		Epoch:        msg.Epoch,
		SenderPeerID: msg.SenderPeerID,
		SenderSeq:    msg.SenderSeq,
		MsgType:      MessageTypeGroupChatText,
		Plaintext:    string(plain),
		Signature:    append([]byte(nil), msg.Signature...),
		State:        GroupMessageStateReceived,
		CreatedAt:    time.UnixMilli(msg.SentAtUnix).UTC(),
	}
	if _, err := s.store.AddGroupMessage(incoming, msg.Ciphertext, nil); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return s.sendGroupDeliveryAck(msg.GroupID, msg.MsgID, msg.SenderPeerID)
		}
		return err
	}
	_ = s.sendGroupDeliveryAck(msg.GroupID, msg.MsgID, msg.SenderPeerID)
	_ = group
	return nil
}

func (s *Service) handleIncomingGroupFile(msg GroupChatFile) error {
	group, err := s.store.GetGroup(msg.GroupID)
	if err != nil {
		return err
	}
	revoked, err := s.store.IsGroupMessageRevoked(msg.GroupID, msg.MsgID)
	if err != nil {
		return err
	}
	if revoked {
		return s.sendGroupDeliveryAck(msg.GroupID, msg.MsgID, msg.SenderPeerID)
	}
	member, err := s.store.GetGroupMember(msg.GroupID, msg.SenderPeerID)
	if err != nil {
		return err
	}
	if member.State != GroupMemberStateActive {
		return nil
	}
	groupEpoch, err := s.store.GetGroupEpoch(msg.GroupID, msg.Epoch)
	if err != nil {
		return err
	}
	nonce := protocol.BuildAEADNonce("fwd", msg.SenderSeq)
	aad := []byte(msg.GroupID + "\x00group_chat_file")
	plain, err := protocol.AEADOpen(groupEpoch.WrappedKeyForLocal, nonce, msg.Ciphertext, aad)
	if err != nil {
		return err
	}
	incoming := GroupMessage{
		MsgID:        msg.MsgID,
		GroupID:      msg.GroupID,
		Epoch:        msg.Epoch,
		SenderPeerID: msg.SenderPeerID,
		SenderSeq:    msg.SenderSeq,
		MsgType:      MessageTypeGroupChatFile,
		FileName:     NormalizeChatFileName(msg.FileName),
		MIMEType:     msg.MIMEType,
		FileSize:     int64(len(plain)),
		Signature:    append([]byte(nil), msg.Signature...),
		State:        GroupMessageStateReceived,
		CreatedAt:    time.UnixMilli(msg.SentAtUnix).UTC(),
	}
	if _, err := s.store.AddGroupMessage(incoming, plain, nil); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return s.sendGroupDeliveryAck(msg.GroupID, msg.MsgID, msg.SenderPeerID)
		}
		return err
	}
	_ = s.sendGroupDeliveryAck(msg.GroupID, msg.MsgID, msg.SenderPeerID)
	_ = group
	return nil
}

func (s *Service) sendGroupDeliveryAck(groupID, msgID, toPeerID string) error {
	ack := GroupDeliveryAck{
		Type:        MessageTypeGroupDeliveryAck,
		GroupID:     groupID,
		MsgID:       msgID,
		FromPeerID:  s.localPeer,
		ToPeerID:    toPeerID,
		AckedAtUnix: time.Now().UnixMilli(),
	}
	if err := s.signGroupDeliveryAck(&ack); err != nil {
		return err
	}
	if err := s.sendEnvelope(toPeerID, ack); err != nil {
		log.Printf("[group] send ack group=%s peer=%s failed: %v", groupID, toPeerID, err)
		return err
	}
	return nil
}

func (s *Service) handleIncomingGroupAck(ack GroupDeliveryAck) error {
	if ack.ToPeerID != s.localPeer {
		return errors.New("group ack is not addressed to local peer")
	}
	msg, err := s.store.GetGroupMessage(ack.MsgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if msg.GroupID != ack.GroupID {
		return errors.New("group ack group_id does not match message")
	}
	if msg.SenderPeerID != s.localPeer {
		return errors.New("group ack targets a message not sent by local peer")
	}
	return s.store.MarkGroupDeliveryState(ack.MsgID, ack.FromPeerID, GroupDeliveryStateDeliveredRemote, time.UnixMilli(ack.AckedAtUnix).UTC())
}

func (s *Service) ensureLocalGroupMessageRevokeAllowed(groupID string, actor GroupMember, msg GroupMessage) error {
	if actor.PeerID == msg.SenderPeerID {
		return nil
	}
	switch actor.Role {
	case GroupRoleController:
		return nil
	case GroupRoleAdmin:
		target, err := s.store.GetGroupMember(groupID, msg.SenderPeerID)
		if err != nil {
			return err
		}
		if target.Role == GroupRoleController {
			return errors.New("admin cannot revoke controller messages")
		}
		return nil
	default:
		return errors.New("you can only revoke your own messages")
	}
}

func (s *Service) handleGroupSyncRequest(req GroupSyncRequest, fromPeerID string) (GroupSyncResponse, error) {
	member, err := s.store.GetGroupMember(req.GroupID, fromPeerID)
	if err != nil {
		return GroupSyncResponse{}, err
	}
	if member.State != GroupMemberStateActive {
		return GroupSyncResponse{}, errors.New("requesting peer is not an active group member")
	}
	events, err := s.store.ListGroupEventsAfter(req.GroupID, req.LastEventSeq)
	if err != nil {
		return GroupSyncResponse{}, err
	}
	msgs, err := s.store.ListGroupMessagesForSync(req.GroupID, req.SenderCursors)
	if err != nil {
		return GroupSyncResponse{}, err
	}
	fileItems, err := s.store.ListGroupFileMessagesForSync(req.GroupID, req.SenderCursors)
	if err != nil {
		return GroupSyncResponse{}, err
	}
	fileMsgs := make([]GroupChatFile, 0, len(fileItems))
	for _, item := range fileItems {
		groupEpoch, err := s.store.GetGroupEpoch(item.GroupID, item.Epoch)
		if err != nil {
			return GroupSyncResponse{}, err
		}
		nonce := protocol.BuildAEADNonce("fwd", item.SenderSeq)
		aad := []byte(item.GroupID + "\x00group_chat_file")
		ciphertext, err := protocol.AEADSeal(groupEpoch.WrappedKeyForLocal, nonce, item.PlainBlob, aad)
		if err != nil {
			return GroupSyncResponse{}, err
		}
		fileMsgs = append(fileMsgs, GroupChatFile{
			Type:         MessageTypeGroupChatFile,
			GroupID:      item.GroupID,
			Epoch:        item.Epoch,
			MsgID:        item.MsgID,
			SenderPeerID: item.SenderPeerID,
			SenderSeq:    item.SenderSeq,
			FileName:     item.FileName,
			MIMEType:     item.MIMEType,
			FileSize:     item.FileSize,
			Ciphertext:   ciphertext,
			SentAtUnix:   item.SentAtUnix,
			Signature:    item.Signature,
		})
	}
	return GroupSyncResponse{
		Type:         MessageTypeGroupSyncResponse,
		GroupID:      req.GroupID,
		Events:       events,
		Messages:     msgs,
		FileMessages: fileMsgs,
	}, nil
}

func (s *Service) requestGroupSync(peerID string, req GroupSyncRequest) (GroupSyncResponse, error) {
	if err := s.ensurePeerConnected(peerID); err != nil {
		return GroupSyncResponse{}, err
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return GroupSyncResponse{}, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	str, err := s.host.NewStream(ctx, pid, p2p.ProtocolGroupSync)
	if err != nil {
		return GroupSyncResponse{}, err
	}
	defer str.Close()
	if err := tunnel.WriteJSONFrame(str, req); err != nil {
		return GroupSyncResponse{}, err
	}
	var resp GroupSyncResponse
	if err := tunnel.ReadJSONFrame(str, &resp); err != nil {
		return GroupSyncResponse{}, err
	}
	return resp, nil
}

func generateGroupEpochKeys(peerIDs []string) (map[string][]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(peerIDs))
	for _, peerID := range uniquePeers(peerIDs) {
		buf := make([]byte, len(key))
		copy(buf, key)
		out[peerID] = buf
	}
	return out, nil
}

func activePeerIDsExcluding(members []GroupMember, excluded ...string) []string {
	excludedSet := make(map[string]struct{}, len(excluded))
	for _, peerID := range excluded {
		excludedSet[peerID] = struct{}{}
	}
	out := make([]string, 0, len(members))
	for _, member := range members {
		if member.State != GroupMemberStateActive {
			continue
		}
		if _, ok := excludedSet[member.PeerID]; ok {
			continue
		}
		out = append(out, member.PeerID)
	}
	return uniquePeers(out)
}

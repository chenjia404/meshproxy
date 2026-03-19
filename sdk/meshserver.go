package sdk

import (
	"context"
	"errors"

	"github.com/chenjia404/meshproxy/internal/meshserver"
	"github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"
)

type MeshServerVisibility = sessionv1.Visibility
type MeshServerMessageType = sessionv1.MessageType
type MeshServerServerSummary = sessionv1.SpaceSummary
type MeshServerMyPermissions = meshserver.MyPermissions
type MeshServerMyServersResp = meshserver.MyServersResp
type MeshServerListServersResp = sessionv1.ListSpacesResp
type MeshServerChannelSummary = sessionv1.ChannelSummary
type MeshServerListChannelsResp = sessionv1.ListChannelsResp
type MeshServerListServerMembersResp = sessionv1.ListSpaceMembersResp
type MeshServerCreateGroupResp = sessionv1.CreateGroupResp
type MeshServerCreateChannelResp = sessionv1.CreateChannelResp
type MeshServerSubscribeChannelResp = sessionv1.SubscribeChannelResp
type MeshServerUnsubscribeChannelResp = sessionv1.UnsubscribeChannelResp
type MeshServerJoinServerResp = sessionv1.JoinSpaceResp
type MeshServerInviteServerMemberResp = sessionv1.InviteSpaceMemberResp
type MeshServerKickServerMemberResp = sessionv1.KickSpaceMemberResp
type MeshServerBanServerMemberResp = sessionv1.BanSpaceMemberResp
type MeshServerSendMessageAck = sessionv1.SendMessageAck
type MeshServerSyncChannelResp = sessionv1.SyncChannelResp
type MeshServerConnectionInfo = meshserver.ConnectionInfo

const (
	MeshServerVisibilityUnspecified = sessionv1.Visibility_VISIBILITY_UNSPECIFIED
	MeshServerVisibilityPublic      = sessionv1.Visibility_PUBLIC
	MeshServerVisibilityPrivate     = sessionv1.Visibility_PRIVATE

	MeshServerMessageTypeUnspecified = sessionv1.MessageType_MESSAGE_TYPE_UNSPECIFIED
	MeshServerMessageTypeText        = sessionv1.MessageType_TEXT
	MeshServerMessageTypeImage       = sessionv1.MessageType_IMAGE
	MeshServerMessageTypeFile        = sessionv1.MessageType_FILE
	MeshServerMessageTypeSystem      = sessionv1.MessageType_SYSTEM
)

type MeshServerService struct {
	inner *Node
}

func (n *Node) MeshServer() *MeshServerService {
	if n == nil {
		return nil
	}
	return &MeshServerService{inner: n}
}

func (s *MeshServerService) ListServers(ctx context.Context) (*MeshServerListServersResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerListServers(ctx)
}

// ListSpaces is a Space-terminology alias of ListServers.
func (s *MeshServerService) ListSpaces(ctx context.Context) (*MeshServerListServersResp, error) {
	return s.ListServers(ctx)
}

func (s *MeshServerService) Connections() []MeshServerConnectionInfo {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil
	}
	return s.inner.inner.MeshServerConnections()
}

func (s *MeshServerService) Connect(ctx context.Context, name, peerID, clientAgent, protocolID string) (MeshServerConnectionInfo, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return MeshServerConnectionInfo{}, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerConnect(ctx, name, peerID, clientAgent, protocolID)
}

func (s *MeshServerService) Disconnect(name string) error {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerDisconnect(name)
}

func (s *MeshServerService) ListServersForConnection(ctx context.Context, connection string) (*MeshServerListServersResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerListServersForConnection(ctx, connection)
}

// ListSpacesForConnection is a Space-terminology alias of ListServersForConnection.
func (s *MeshServerService) ListSpacesForConnection(ctx context.Context, connection string) (*MeshServerListServersResp, error) {
	return s.ListServersForConnection(ctx, connection)
}

func (s *MeshServerService) JoinServer(ctx context.Context, serverID string) (*MeshServerJoinServerResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerJoinServer(ctx, serverID)
}

// JoinSpace is a Space-terminology alias of JoinServer.
func (s *MeshServerService) JoinSpace(ctx context.Context, spaceID string) (*MeshServerJoinServerResp, error) {
	return s.JoinServer(ctx, spaceID)
}

func (s *MeshServerService) JoinServerForConnection(ctx context.Context, connection, serverID string) (*MeshServerJoinServerResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerJoinServerForConnection(ctx, connection, serverID)
}

// JoinSpaceForConnection is a Space-terminology alias of JoinServerForConnection.
func (s *MeshServerService) JoinSpaceForConnection(ctx context.Context, connection, spaceID string) (*MeshServerJoinServerResp, error) {
	return s.JoinServerForConnection(ctx, connection, spaceID)
}

func (s *MeshServerService) InviteServerMember(ctx context.Context, serverID, targetUserID string) (*MeshServerInviteServerMemberResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerInviteServerMember(ctx, serverID, targetUserID)
}

// InviteSpaceMember is a Space-terminology alias of InviteServerMember.
func (s *MeshServerService) InviteSpaceMember(ctx context.Context, spaceID, targetUserID string) (*MeshServerInviteServerMemberResp, error) {
	return s.InviteServerMember(ctx, spaceID, targetUserID)
}

func (s *MeshServerService) InviteServerMemberForConnection(ctx context.Context, connection, serverID, targetUserID string) (*MeshServerInviteServerMemberResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerInviteServerMemberForConnection(ctx, connection, serverID, targetUserID)
}

func (s *MeshServerService) KickServerMember(ctx context.Context, serverID, targetUserID string) (*MeshServerKickServerMemberResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerKickServerMember(ctx, serverID, targetUserID)
}

// KickSpaceMember is a Space-terminology alias of KickServerMember.
func (s *MeshServerService) KickSpaceMember(ctx context.Context, spaceID, targetUserID string) (*MeshServerKickServerMemberResp, error) {
	return s.KickServerMember(ctx, spaceID, targetUserID)
}

func (s *MeshServerService) KickServerMemberForConnection(ctx context.Context, connection, serverID, targetUserID string) (*MeshServerKickServerMemberResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerKickServerMemberForConnection(ctx, connection, serverID, targetUserID)
}

func (s *MeshServerService) BanServerMember(ctx context.Context, serverID, targetUserID string) (*MeshServerBanServerMemberResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerBanServerMember(ctx, serverID, targetUserID)
}

// BanSpaceMember is a Space-terminology alias of BanServerMember.
func (s *MeshServerService) BanSpaceMember(ctx context.Context, spaceID, targetUserID string) (*MeshServerBanServerMemberResp, error) {
	return s.BanServerMember(ctx, spaceID, targetUserID)
}

func (s *MeshServerService) BanServerMemberForConnection(ctx context.Context, connection, serverID, targetUserID string) (*MeshServerBanServerMemberResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerBanServerMemberForConnection(ctx, connection, serverID, targetUserID)
}

func (s *MeshServerService) ListServerMembers(ctx context.Context, serverID string, afterMemberID uint64, limit uint32) (*MeshServerListServerMembersResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerListServerMembers(ctx, serverID, afterMemberID, limit)
}

// ListSpaceMembers is a Space-terminology alias of ListServerMembers.
func (s *MeshServerService) ListSpaceMembers(ctx context.Context, spaceID string, afterMemberID uint64, limit uint32) (*MeshServerListServerMembersResp, error) {
	return s.ListServerMembers(ctx, spaceID, afterMemberID, limit)
}

func (s *MeshServerService) ListServerMembersForConnection(ctx context.Context, connection, serverID string, afterMemberID uint64, limit uint32) (*MeshServerListServerMembersResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerListServerMembersForConnection(ctx, connection, serverID, afterMemberID, limit)
}

func (s *MeshServerService) ListChannels(ctx context.Context, serverID string) (*MeshServerListChannelsResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerListChannels(ctx, serverID)
}

// ListSpaceChannels is a Space-terminology alias of ListChannels.
func (s *MeshServerService) ListSpaceChannels(ctx context.Context, spaceID string) (*MeshServerListChannelsResp, error) {
	return s.ListChannels(ctx, spaceID)
}

func (s *MeshServerService) ListChannelsForConnection(ctx context.Context, connection, serverID string) (*MeshServerListChannelsResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerListChannelsForConnection(ctx, connection, serverID)
}

func (s *MeshServerService) CreateGroup(ctx context.Context, serverID, name, description string, visibility MeshServerVisibility, slowModeSeconds uint32) (*MeshServerCreateGroupResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerCreateGroup(ctx, serverID, name, description, visibility, slowModeSeconds)
}

// CreateSpaceGroup is a Space-terminology alias of CreateGroup.
func (s *MeshServerService) CreateSpaceGroup(ctx context.Context, spaceID, name, description string, visibility MeshServerVisibility, slowModeSeconds uint32) (*MeshServerCreateGroupResp, error) {
	return s.CreateGroup(ctx, spaceID, name, description, visibility, slowModeSeconds)
}

func (s *MeshServerService) CreateGroupForConnection(ctx context.Context, connection, serverID, name, description string, visibility MeshServerVisibility, slowModeSeconds uint32) (*MeshServerCreateGroupResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerCreateGroupForConnection(ctx, connection, serverID, name, description, visibility, slowModeSeconds)
}

func (s *MeshServerService) CreateChannel(ctx context.Context, serverID, name, description string, visibility MeshServerVisibility, slowModeSeconds uint32) (*MeshServerCreateChannelResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerCreateChannel(ctx, serverID, name, description, visibility, slowModeSeconds)
}

// CreateSpaceChannel is a Space-terminology alias of CreateChannel.
func (s *MeshServerService) CreateSpaceChannel(ctx context.Context, spaceID, name, description string, visibility MeshServerVisibility, slowModeSeconds uint32) (*MeshServerCreateChannelResp, error) {
	return s.CreateChannel(ctx, spaceID, name, description, visibility, slowModeSeconds)
}

// GetMyPermissions returns my permissions on a specific Space.
func (s *MeshServerService) GetMyPermissions(ctx context.Context, spaceID string) (*MeshServerMyPermissions, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerGetMyPermissionsForConnection(ctx, "", spaceID)
}

// GetMyPermissionsForConnection is the connection-specific variant.
func (s *MeshServerService) GetMyPermissionsForConnection(ctx context.Context, connection, spaceID string) (*MeshServerMyPermissions, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerGetMyPermissionsForConnection(ctx, connection, spaceID)
}

// ListMySpaces returns all Spaces where I am already a member.
func (s *MeshServerService) ListMySpaces(ctx context.Context) (*MeshServerMyServersResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerListMyServersForConnection(ctx, "")
}

// ListMySpacesForConnection is the connection-specific variant.
func (s *MeshServerService) ListMySpacesForConnection(ctx context.Context, connection string) (*MeshServerMyServersResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerListMyServersForConnection(ctx, connection)
}

func (s *MeshServerService) CreateChannelForConnection(ctx context.Context, connection, serverID, name, description string, visibility MeshServerVisibility, slowModeSeconds uint32) (*MeshServerCreateChannelResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerCreateChannelForConnection(ctx, connection, serverID, name, description, visibility, slowModeSeconds)
}

func (s *MeshServerService) SubscribeChannel(ctx context.Context, channelID string, lastSeenSeq uint64) (*MeshServerSubscribeChannelResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerSubscribeChannel(ctx, channelID, lastSeenSeq)
}

func (s *MeshServerService) SubscribeChannelForConnection(ctx context.Context, connection, channelID string, lastSeenSeq uint64) (*MeshServerSubscribeChannelResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerSubscribeChannelForConnection(ctx, connection, channelID, lastSeenSeq)
}

func (s *MeshServerService) UnsubscribeChannel(ctx context.Context, channelID string) (*MeshServerUnsubscribeChannelResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerUnsubscribeChannel(ctx, channelID)
}

func (s *MeshServerService) UnsubscribeChannelForConnection(ctx context.Context, connection, channelID string) (*MeshServerUnsubscribeChannelResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerUnsubscribeChannelForConnection(ctx, connection, channelID)
}

func (s *MeshServerService) SendText(ctx context.Context, channelID, clientMsgID, text string) (*MeshServerSendMessageAck, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerSendText(ctx, channelID, clientMsgID, text)
}

func (s *MeshServerService) SendTextForConnection(ctx context.Context, connection, channelID, clientMsgID, text string) (*MeshServerSendMessageAck, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerSendTextForConnection(ctx, connection, channelID, clientMsgID, text)
}

func (s *MeshServerService) SyncChannel(ctx context.Context, channelID string, afterSeq uint64, limit uint32) (*MeshServerSyncChannelResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerSyncChannel(ctx, channelID, afterSeq, limit)
}

func (s *MeshServerService) SyncChannelForConnection(ctx context.Context, connection, channelID string, afterSeq uint64, limit uint32) (*MeshServerSyncChannelResp, error) {
	if s == nil || s.inner == nil || s.inner.inner == nil {
		return nil, errors.New("meshserver client not available")
	}
	return s.inner.inner.MeshServerSyncChannelForConnection(ctx, connection, channelID, afterSeq, limit)
}

package sessionv1

import proto "github.com/golang/protobuf/proto"

const _ = proto.ProtoPackageIsVersion4

type MsgType int32

const (
	MsgType_MSG_TYPE_UNSPECIFIED                  MsgType = 0
	MsgType_HELLO                                 MsgType = 1
	MsgType_AUTH_CHALLENGE                        MsgType = 2
	MsgType_AUTH_PROVE                            MsgType = 3
	MsgType_AUTH_RESULT                           MsgType = 4
	MsgType_PING                                  MsgType = 10
	MsgType_PONG                                  MsgType = 11
	MsgType_ERROR                                 MsgType = 12
	MsgType_LIST_SPACES_REQ                       MsgType = 20
	MsgType_LIST_SPACES_RESP                      MsgType = 21
	MsgType_LIST_CHANNELS_REQ                     MsgType = 22
	MsgType_LIST_CHANNELS_RESP                    MsgType = 23
	MsgType_SUBSCRIBE_CHANNEL_REQ                 MsgType = 24
	MsgType_SUBSCRIBE_CHANNEL_RESP                MsgType = 25
	MsgType_UNSUBSCRIBE_CHANNEL_REQ               MsgType = 26
	MsgType_UNSUBSCRIBE_CHANNEL_RESP              MsgType = 27
	MsgType_CREATE_GROUP_REQ                      MsgType = 28
	MsgType_CREATE_GROUP_RESP                     MsgType = 29
	MsgType_SEND_MESSAGE_REQ                      MsgType = 30
	MsgType_SEND_MESSAGE_ACK                      MsgType = 31
	MsgType_MESSAGE_EVENT                         MsgType = 32
	MsgType_CHANNEL_DELIVER_ACK                   MsgType = 33
	MsgType_CHANNEL_READ_UPDATE                   MsgType = 34
	MsgType_SYNC_CHANNEL_REQ                      MsgType = 35
	MsgType_SYNC_CHANNEL_RESP                     MsgType = 36
	MsgType_CREATE_CHANNEL_REQ                    MsgType = 37
	MsgType_CREATE_CHANNEL_RESP                   MsgType = 38
	MsgType_JOIN_SPACE_REQ                        MsgType = 43
	MsgType_JOIN_SPACE_RESP                       MsgType = 44
	MsgType_INVITE_SPACE_MEMBER_REQ               MsgType = 45
	MsgType_INVITE_SPACE_MEMBER_RESP              MsgType = 46
	MsgType_KICK_SPACE_MEMBER_REQ                 MsgType = 47
	MsgType_KICK_SPACE_MEMBER_RESP                MsgType = 48
	MsgType_BAN_SPACE_MEMBER_REQ                  MsgType = 49
	MsgType_BAN_SPACE_MEMBER_RESP                 MsgType = 50
	MsgType_LIST_SPACE_MEMBERS_REQ                MsgType = 51
	MsgType_LIST_SPACE_MEMBERS_RESP               MsgType = 52
	MsgType_UNBAN_SPACE_MEMBER_REQ                MsgType = 53
	MsgType_UNBAN_SPACE_MEMBER_RESP               MsgType = 54
	MsgType_CREATE_SPACE_REQ                      MsgType = 57
	MsgType_CREATE_SPACE_RESP                     MsgType = 58
	MsgType_GET_CREATE_SPACE_PERMISSIONS_REQ      MsgType = 59
	MsgType_GET_CREATE_SPACE_PERMISSIONS_RESP     MsgType = 60
	MsgType_GET_CREATE_GROUP_PERMISSIONS_REQ      MsgType = 61
	MsgType_GET_CREATE_GROUP_PERMISSIONS_RESP     MsgType = 62
	MsgType_GET_MEDIA_REQ                         MsgType = 63
	MsgType_GET_MEDIA_RESP                        MsgType = 64
	MsgType_ADMIN_SET_GROUP_AUTO_DELETE_REQ       MsgType = 65
	MsgType_ADMIN_SET_GROUP_AUTO_DELETE_RESP      MsgType = 66
	MsgType_OPEN_DIRECT_CONVERSATION_REQ          MsgType = 67
	MsgType_OPEN_DIRECT_CONVERSATION_RESP         MsgType = 68
	MsgType_LIST_DIRECT_CONVERSATIONS_REQ         MsgType = 69
	MsgType_LIST_DIRECT_CONVERSATIONS_RESP        MsgType = 70
	MsgType_SEND_DIRECT_MESSAGE_REQ               MsgType = 71
	MsgType_SEND_DIRECT_MESSAGE_ACK               MsgType = 72
	MsgType_DIRECT_MESSAGE_EVENT                  MsgType = 73
	MsgType_ACK_DIRECT_MESSAGE_REQ                MsgType = 74
	MsgType_ACK_DIRECT_MESSAGE_RESP               MsgType = 75
	MsgType_DIRECT_PEER_ACK_EVENT                 MsgType = 76
	MsgType_SYNC_DIRECT_MESSAGES_REQ              MsgType = 77
	MsgType_SYNC_DIRECT_MESSAGES_RESP             MsgType = 78
	MsgType_ADMIN_SET_SPACE_MEMBER_ROLE_REQ       MsgType = 39
	MsgType_ADMIN_SET_SPACE_MEMBER_ROLE_RESP      MsgType = 40
	MsgType_ADMIN_SET_SPACE_CHANNEL_CREATION_REQ  MsgType = 41
	MsgType_ADMIN_SET_SPACE_CHANNEL_CREATION_RESP MsgType = 42
)

var MsgType_name = map[int32]string{
	0:  "MSG_TYPE_UNSPECIFIED",
	1:  "HELLO",
	2:  "AUTH_CHALLENGE",
	3:  "AUTH_PROVE",
	4:  "AUTH_RESULT",
	10: "PING",
	11: "PONG",
	12: "ERROR",
	20: "LIST_SPACES_REQ",
	21: "LIST_SPACES_RESP",
	22: "LIST_CHANNELS_REQ",
	23: "LIST_CHANNELS_RESP",
	24: "SUBSCRIBE_CHANNEL_REQ",
	25: "SUBSCRIBE_CHANNEL_RESP",
	26: "UNSUBSCRIBE_CHANNEL_REQ",
	27: "UNSUBSCRIBE_CHANNEL_RESP",
	28: "CREATE_GROUP_REQ",
	29: "CREATE_GROUP_RESP",
	30: "SEND_MESSAGE_REQ",
	31: "SEND_MESSAGE_ACK",
	32: "MESSAGE_EVENT",
	33: "CHANNEL_DELIVER_ACK",
	34: "CHANNEL_READ_UPDATE",
	35: "SYNC_CHANNEL_REQ",
	36: "SYNC_CHANNEL_RESP",
	37: "CREATE_CHANNEL_REQ",
	38: "CREATE_CHANNEL_RESP",
	39: "ADMIN_SET_SPACE_MEMBER_ROLE_REQ",
	40: "ADMIN_SET_SPACE_MEMBER_ROLE_RESP",
	41: "ADMIN_SET_SPACE_CHANNEL_CREATION_REQ",
	42: "ADMIN_SET_SPACE_CHANNEL_CREATION_RESP",
	43: "JOIN_SPACE_REQ",
	44: "JOIN_SPACE_RESP",
	45: "INVITE_SPACE_MEMBER_REQ",
	46: "INVITE_SPACE_MEMBER_RESP",
	47: "KICK_SPACE_MEMBER_REQ",
	48: "KICK_SPACE_MEMBER_RESP",
	49: "BAN_SPACE_MEMBER_REQ",
	50: "BAN_SPACE_MEMBER_RESP",
	51: "LIST_SPACE_MEMBERS_REQ",
	52: "LIST_SPACE_MEMBERS_RESP",
	53: "UNBAN_SPACE_MEMBER_REQ",
	54: "UNBAN_SPACE_MEMBER_RESP",
	57: "CREATE_SPACE_REQ",
	58: "CREATE_SPACE_RESP",
	59: "GET_CREATE_SPACE_PERMISSIONS_REQ",
	60: "GET_CREATE_SPACE_PERMISSIONS_RESP",
	61: "GET_CREATE_GROUP_PERMISSIONS_REQ",
	62: "GET_CREATE_GROUP_PERMISSIONS_RESP",
	63: "GET_MEDIA_REQ",
	64: "GET_MEDIA_RESP",
	65: "ADMIN_SET_GROUP_AUTO_DELETE_REQ",
	66: "ADMIN_SET_GROUP_AUTO_DELETE_RESP",
	67: "OPEN_DIRECT_CONVERSATION_REQ",
	68: "OPEN_DIRECT_CONVERSATION_RESP",
	69: "LIST_DIRECT_CONVERSATIONS_REQ",
	70: "LIST_DIRECT_CONVERSATIONS_RESP",
	71: "SEND_DIRECT_MESSAGE_REQ",
	72: "SEND_DIRECT_MESSAGE_ACK",
	73: "DIRECT_MESSAGE_EVENT",
	74: "ACK_DIRECT_MESSAGE_REQ",
	75: "ACK_DIRECT_MESSAGE_RESP",
	76: "DIRECT_PEER_ACK_EVENT",
	77: "SYNC_DIRECT_MESSAGES_REQ",
	78: "SYNC_DIRECT_MESSAGES_RESP",
}

func (x MsgType) String() string { return proto.EnumName(MsgType_name, int32(x)) }

type Visibility int32

const (
	Visibility_VISIBILITY_UNSPECIFIED Visibility = 0
	Visibility_PUBLIC                 Visibility = 1
	Visibility_PRIVATE                Visibility = 2
)

var Visibility_name = map[int32]string{
	0: "VISIBILITY_UNSPECIFIED",
	1: "PUBLIC",
	2: "PRIVATE",
}

func (x Visibility) String() string { return proto.EnumName(Visibility_name, int32(x)) }

type ChannelType int32

const (
	ChannelType_CHANNEL_TYPE_UNSPECIFIED ChannelType = 0
	ChannelType_GROUP                    ChannelType = 1
	ChannelType_BROADCAST                ChannelType = 2
)

var ChannelType_name = map[int32]string{
	0: "CHANNEL_TYPE_UNSPECIFIED",
	1: "GROUP",
	2: "BROADCAST",
}

func (x ChannelType) String() string { return proto.EnumName(ChannelType_name, int32(x)) }

type MessageType int32

const (
	MessageType_MESSAGE_TYPE_UNSPECIFIED MessageType = 0
	MessageType_TEXT                     MessageType = 1
	MessageType_IMAGE                    MessageType = 2
	MessageType_FILE                     MessageType = 3
	MessageType_SYSTEM                   MessageType = 4
)

var MessageType_name = map[int32]string{
	0: "MESSAGE_TYPE_UNSPECIFIED",
	1: "TEXT",
	2: "IMAGE",
	3: "FILE",
	4: "SYSTEM",
}

func (x MessageType) String() string { return proto.EnumName(MessageType_name, int32(x)) }

type MemberRole int32

const (
	MemberRole_MEMBER_ROLE_UNSPECIFIED MemberRole = 0
	MemberRole_OWNER                   MemberRole = 1
	MemberRole_ADMIN                   MemberRole = 2
	MemberRole_MEMBER                  MemberRole = 3
	MemberRole_SUBSCRIBER              MemberRole = 4
)

var MemberRole_name = map[int32]string{
	0: "MEMBER_ROLE_UNSPECIFIED",
	1: "OWNER",
	2: "ADMIN",
	3: "MEMBER",
	4: "SUBSCRIBER",
}

func (x MemberRole) String() string { return proto.EnumName(MemberRole_name, int32(x)) }

type Envelope struct {
	Version     uint32  `protobuf:"varint,1,opt,name=version,proto3" json:"version,omitempty"`
	MsgType     MsgType `protobuf:"varint,2,opt,name=msg_type,json=msgType,proto3,enum=meshserver.session.v1.MsgType" json:"msg_type,omitempty"`
	RequestId   string  `protobuf:"bytes,3,opt,name=request_id,json=requestId,proto3" json:"request_id,omitempty"`
	TimestampMs uint64  `protobuf:"varint,4,opt,name=timestamp_ms,json=timestampMs,proto3" json:"timestamp_ms,omitempty"`
	Body        []byte  `protobuf:"bytes,5,opt,name=body,proto3" json:"body,omitempty"`
}

func (m *Envelope) Reset()         { *m = Envelope{} }
func (m *Envelope) String() string { return proto.CompactTextString(m) }
func (*Envelope) ProtoMessage()    {}

type Hello struct {
	ClientPeerId    string `protobuf:"bytes,1,opt,name=client_peer_id,json=clientPeerId,proto3" json:"client_peer_id,omitempty"`
	ClientAgent     string `protobuf:"bytes,2,opt,name=client_agent,json=clientAgent,proto3" json:"client_agent,omitempty"`
	ProtocolVersion string `protobuf:"bytes,3,opt,name=protocol_version,json=protocolVersion,proto3" json:"protocol_version,omitempty"`
}

func (m *Hello) Reset()         { *m = Hello{} }
func (m *Hello) String() string { return proto.CompactTextString(m) }
func (*Hello) ProtoMessage()    {}

type AuthChallenge struct {
	NodePeerId  string `protobuf:"bytes,1,opt,name=node_peer_id,json=nodePeerId,proto3" json:"node_peer_id,omitempty"`
	Nonce       []byte `protobuf:"bytes,2,opt,name=nonce,proto3" json:"nonce,omitempty"`
	IssuedAtMs  uint64 `protobuf:"varint,3,opt,name=issued_at_ms,json=issuedAtMs,proto3" json:"issued_at_ms,omitempty"`
	ExpiresAtMs uint64 `protobuf:"varint,4,opt,name=expires_at_ms,json=expiresAtMs,proto3" json:"expires_at_ms,omitempty"`
	SessionHint string `protobuf:"bytes,5,opt,name=session_hint,json=sessionHint,proto3" json:"session_hint,omitempty"`
}

func (m *AuthChallenge) Reset()         { *m = AuthChallenge{} }
func (m *AuthChallenge) String() string { return proto.CompactTextString(m) }
func (*AuthChallenge) ProtoMessage()    {}

type AuthProve struct {
	ClientPeerId string `protobuf:"bytes,1,opt,name=client_peer_id,json=clientPeerId,proto3" json:"client_peer_id,omitempty"`
	Nonce        []byte `protobuf:"bytes,2,opt,name=nonce,proto3" json:"nonce,omitempty"`
	IssuedAtMs   uint64 `protobuf:"varint,3,opt,name=issued_at_ms,json=issuedAtMs,proto3" json:"issued_at_ms,omitempty"`
	ExpiresAtMs  uint64 `protobuf:"varint,4,opt,name=expires_at_ms,json=expiresAtMs,proto3" json:"expires_at_ms,omitempty"`
	Signature    []byte `protobuf:"bytes,5,opt,name=signature,proto3" json:"signature,omitempty"`
	PublicKey    []byte `protobuf:"bytes,6,opt,name=public_key,json=publicKey,proto3" json:"public_key,omitempty"`
}

func (m *AuthProve) Reset()         { *m = AuthProve{} }
func (m *AuthProve) String() string { return proto.CompactTextString(m) }
func (*AuthProve) ProtoMessage()    {}

type AuthResult struct {
	Ok          bool            `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SessionId   string          `protobuf:"bytes,2,opt,name=session_id,json=sessionId,proto3" json:"session_id,omitempty"`
	UserId      string          `protobuf:"bytes,3,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	DisplayName string          `protobuf:"bytes,4,opt,name=display_name,json=displayName,proto3" json:"display_name,omitempty"`
	Message     string          `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
	Spaces      []*SpaceSummary `protobuf:"bytes,6,rep,name=spaces,proto3" json:"spaces,omitempty"`
}

func (m *AuthResult) Reset()         { *m = AuthResult{} }
func (m *AuthResult) String() string { return proto.CompactTextString(m) }
func (*AuthResult) ProtoMessage()    {}

type ErrorMsg struct {
	Code    uint32 `protobuf:"varint,1,opt,name=code,proto3" json:"code,omitempty"`
	Message string `protobuf:"bytes,2,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *ErrorMsg) Reset()         { *m = ErrorMsg{} }
func (m *ErrorMsg) String() string { return proto.CompactTextString(m) }
func (*ErrorMsg) ProtoMessage()    {}

type Ping struct {
	Nonce uint64 `protobuf:"varint,1,opt,name=nonce,proto3" json:"nonce,omitempty"`
}

func (m *Ping) Reset()         { *m = Ping{} }
func (m *Ping) String() string { return proto.CompactTextString(m) }
func (*Ping) ProtoMessage()    {}

type Pong struct {
	Nonce uint64 `protobuf:"varint,1,opt,name=nonce,proto3" json:"nonce,omitempty"`
}

func (m *Pong) Reset()         { *m = Pong{} }
func (m *Pong) String() string { return proto.CompactTextString(m) }
func (*Pong) ProtoMessage()    {}

type ChannelSummary struct {
	ChannelId       uint32      `protobuf:"varint,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	SpaceId         uint32      `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	Type            ChannelType `protobuf:"varint,3,opt,name=type,proto3,enum=meshserver.session.v1.ChannelType" json:"type,omitempty"`
	Name            string      `protobuf:"bytes,4,opt,name=name,proto3" json:"name,omitempty"`
	Description     string      `protobuf:"bytes,5,opt,name=description,proto3" json:"description,omitempty"`
	Visibility      Visibility  `protobuf:"varint,6,opt,name=visibility,proto3,enum=meshserver.session.v1.Visibility" json:"visibility,omitempty"`
	SlowModeSeconds uint32      `protobuf:"varint,7,opt,name=slow_mode_seconds,json=slowModeSeconds,proto3" json:"slow_mode_seconds,omitempty"`
	AutoDeleteAfterSeconds uint32      `protobuf:"varint,8,opt,name=auto_delete_after_seconds,json=autoDeleteAfterSeconds,proto3" json:"auto_delete_after_seconds,omitempty"`
	LastSeq                uint64      `protobuf:"varint,9,opt,name=last_seq,json=lastSeq,proto3" json:"last_seq,omitempty"`
	MemberCount            uint32      `protobuf:"varint,10,opt,name=member_count,json=memberCount,proto3" json:"member_count,omitempty"`
	CanView                bool        `protobuf:"varint,11,opt,name=can_view,json=canView,proto3" json:"can_view,omitempty"`
	CanSendMessage         bool        `protobuf:"varint,12,opt,name=can_send_message,json=canSendMessage,proto3" json:"can_send_message,omitempty"`
	CanSendImage           bool        `protobuf:"varint,13,opt,name=can_send_image,json=canSendImage,proto3" json:"can_send_image,omitempty"`
	CanSendFile            bool        `protobuf:"varint,14,opt,name=can_send_file,json=canSendFile,proto3" json:"can_send_file,omitempty"`
}

func (m *ChannelSummary) Reset()         { *m = ChannelSummary{} }
func (m *ChannelSummary) String() string { return proto.CompactTextString(m) }
func (*ChannelSummary) ProtoMessage()    {}

type ListChannelsReq struct {
	SpaceId uint32 `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
}

func (m *ListChannelsReq) Reset()         { *m = ListChannelsReq{} }
func (m *ListChannelsReq) String() string { return proto.CompactTextString(m) }
func (*ListChannelsReq) ProtoMessage()    {}

type ListChannelsResp struct {
	SpaceId  uint32            `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	Channels []*ChannelSummary `protobuf:"bytes,2,rep,name=channels,proto3" json:"channels,omitempty"`
}

func (m *ListChannelsResp) Reset()         { *m = ListChannelsResp{} }
func (m *ListChannelsResp) String() string { return proto.CompactTextString(m) }
func (*ListChannelsResp) ProtoMessage()    {}

type CreateSpaceReq struct {
	Name                 string     `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Description          string     `protobuf:"bytes,2,opt,name=description,proto3" json:"description,omitempty"`
	Visibility           Visibility `protobuf:"varint,3,opt,name=visibility,proto3,enum=meshserver.session.v1.Visibility" json:"visibility,omitempty"`
	AllowChannelCreation bool       `protobuf:"varint,4,opt,name=allow_channel_creation,json=allowChannelCreation,proto3" json:"allow_channel_creation,omitempty"`
}

func (m *CreateSpaceReq) Reset()         { *m = CreateSpaceReq{} }
func (m *CreateSpaceReq) String() string { return proto.CompactTextString(m) }
func (*CreateSpaceReq) ProtoMessage()    {}

type CreateSpaceResp struct {
	Ok      bool          `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId uint32        `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	Space   *SpaceSummary `protobuf:"bytes,3,opt,name=space,proto3" json:"space,omitempty"`
	Message string        `protobuf:"bytes,4,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *CreateSpaceResp) Reset()         { *m = CreateSpaceResp{} }
func (m *CreateSpaceResp) String() string { return proto.CompactTextString(m) }
func (*CreateSpaceResp) ProtoMessage()    {}

type SubscribeChannelReq struct {
	ChannelId   uint32 `protobuf:"varint,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	LastSeenSeq uint64 `protobuf:"varint,2,opt,name=last_seen_seq,json=lastSeenSeq,proto3" json:"last_seen_seq,omitempty"`
}

func (m *SubscribeChannelReq) Reset()         { *m = SubscribeChannelReq{} }
func (m *SubscribeChannelReq) String() string { return proto.CompactTextString(m) }
func (*SubscribeChannelReq) ProtoMessage()    {}

type SubscribeChannelResp struct {
	Ok             bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ChannelId      uint32 `protobuf:"varint,2,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	CurrentLastSeq uint64 `protobuf:"varint,3,opt,name=current_last_seq,json=currentLastSeq,proto3" json:"current_last_seq,omitempty"`
	Message        string `protobuf:"bytes,4,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *SubscribeChannelResp) Reset()         { *m = SubscribeChannelResp{} }
func (m *SubscribeChannelResp) String() string { return proto.CompactTextString(m) }
func (*SubscribeChannelResp) ProtoMessage()    {}

type UnsubscribeChannelReq struct {
	ChannelId uint32 `protobuf:"varint,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
}

func (m *UnsubscribeChannelReq) Reset()         { *m = UnsubscribeChannelReq{} }
func (m *UnsubscribeChannelReq) String() string { return proto.CompactTextString(m) }
func (*UnsubscribeChannelReq) ProtoMessage()    {}

type UnsubscribeChannelResp struct {
	Ok        bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ChannelId uint32 `protobuf:"varint,2,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
}

func (m *UnsubscribeChannelResp) Reset()         { *m = UnsubscribeChannelResp{} }
func (m *UnsubscribeChannelResp) String() string { return proto.CompactTextString(m) }
func (*UnsubscribeChannelResp) ProtoMessage()    {}

type CreateGroupReq struct {
	SpaceId         uint32     `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	Name            string     `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Description     string     `protobuf:"bytes,3,opt,name=description,proto3" json:"description,omitempty"`
	Visibility      Visibility `protobuf:"varint,4,opt,name=visibility,proto3,enum=meshserver.session.v1.Visibility" json:"visibility,omitempty"`
	SlowModeSeconds uint32     `protobuf:"varint,5,opt,name=slow_mode_seconds,json=slowModeSeconds,proto3" json:"slow_mode_seconds,omitempty"`
}

func (m *CreateGroupReq) Reset()         { *m = CreateGroupReq{} }
func (m *CreateGroupReq) String() string { return proto.CompactTextString(m) }
func (*CreateGroupReq) ProtoMessage()    {}

type CreateGroupResp struct {
	Ok        bool            `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId   uint32          `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	ChannelId uint32          `protobuf:"varint,3,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	Channel   *ChannelSummary `protobuf:"bytes,4,opt,name=channel,proto3" json:"channel,omitempty"`
	Message   string          `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *CreateGroupResp) Reset()         { *m = CreateGroupResp{} }
func (m *CreateGroupResp) String() string { return proto.CompactTextString(m) }
func (*CreateGroupResp) ProtoMessage()    {}

type CreateChannelReq struct {
	SpaceId         uint32     `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	Name            string     `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Description     string     `protobuf:"bytes,3,opt,name=description,proto3" json:"description,omitempty"`
	Visibility      Visibility `protobuf:"varint,4,opt,name=visibility,proto3,enum=meshserver.session.v1.Visibility" json:"visibility,omitempty"`
	SlowModeSeconds uint32     `protobuf:"varint,5,opt,name=slow_mode_seconds,json=slowModeSeconds,proto3" json:"slow_mode_seconds,omitempty"`
}

func (m *CreateChannelReq) Reset()         { *m = CreateChannelReq{} }
func (m *CreateChannelReq) String() string { return proto.CompactTextString(m) }
func (*CreateChannelReq) ProtoMessage()    {}

type CreateChannelResp struct {
	Ok        bool            `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId   uint32          `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	ChannelId uint32          `protobuf:"varint,3,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	Channel   *ChannelSummary `protobuf:"bytes,4,opt,name=channel,proto3" json:"channel,omitempty"`
	Message   string          `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *CreateChannelResp) Reset()         { *m = CreateChannelResp{} }
func (m *CreateChannelResp) String() string { return proto.CompactTextString(m) }
func (*CreateChannelResp) ProtoMessage()    {}

type SpaceSummary struct {
	SpaceId              uint32     `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	Name                 string     `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	AvatarUrl            string     `protobuf:"bytes,3,opt,name=avatar_url,json=avatarUrl,proto3" json:"avatar_url,omitempty"`
	Description          string     `protobuf:"bytes,4,opt,name=description,proto3" json:"description,omitempty"`
	Visibility           Visibility `protobuf:"varint,5,opt,name=visibility,proto3,enum=meshserver.session.v1.Visibility" json:"visibility,omitempty"`
	MemberCount          uint32     `protobuf:"varint,6,opt,name=member_count,json=memberCount,proto3" json:"member_count,omitempty"`
	AllowChannelCreation bool       `protobuf:"varint,7,opt,name=allow_channel_creation,json=allowChannelCreation,proto3" json:"allow_channel_creation,omitempty"`
}

func (m *SpaceSummary) Reset()         { *m = SpaceSummary{} }
func (m *SpaceSummary) String() string { return proto.CompactTextString(m) }
func (*SpaceSummary) ProtoMessage()    {}

type ListSpacesReq struct{}

func (m *ListSpacesReq) Reset()         { *m = ListSpacesReq{} }
func (m *ListSpacesReq) String() string { return proto.CompactTextString(m) }
func (*ListSpacesReq) ProtoMessage()    {}

type ListSpacesResp struct {
	Spaces []*SpaceSummary `protobuf:"bytes,1,rep,name=spaces,proto3" json:"spaces,omitempty"`
}

func (m *ListSpacesResp) Reset()         { *m = ListSpacesResp{} }
func (m *ListSpacesResp) String() string { return proto.CompactTextString(m) }
func (*ListSpacesResp) ProtoMessage()    {}

type SpaceMemberSummary struct {
	MemberId     uint64     `protobuf:"varint,1,opt,name=member_id,json=memberId,proto3" json:"member_id,omitempty"`
	UserId       string     `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	DisplayName  string     `protobuf:"bytes,3,opt,name=display_name,json=displayName,proto3" json:"display_name,omitempty"`
	AvatarUrl    string     `protobuf:"bytes,4,opt,name=avatar_url,json=avatarUrl,proto3" json:"avatar_url,omitempty"`
	Role         MemberRole `protobuf:"varint,5,opt,name=role,proto3,enum=meshserver.session.v1.MemberRole" json:"role,omitempty"`
	Nickname     string     `protobuf:"bytes,6,opt,name=nickname,proto3" json:"nickname,omitempty"`
	IsMuted      bool       `protobuf:"varint,7,opt,name=is_muted,json=isMuted,proto3" json:"is_muted,omitempty"`
	IsBanned     bool       `protobuf:"varint,8,opt,name=is_banned,json=isBanned,proto3" json:"is_banned,omitempty"`
	JoinedAtMs   uint64     `protobuf:"varint,9,opt,name=joined_at_ms,json=joinedAtMs,proto3" json:"joined_at_ms,omitempty"`
	LastSeenAtMs uint64     `protobuf:"varint,10,opt,name=last_seen_at_ms,json=lastSeenAtMs,proto3" json:"last_seen_at_ms,omitempty"`
}

func (m *SpaceMemberSummary) Reset()         { *m = SpaceMemberSummary{} }
func (m *SpaceMemberSummary) String() string { return proto.CompactTextString(m) }
func (*SpaceMemberSummary) ProtoMessage()    {}

type ListSpaceMembersReq struct {
	SpaceId       uint32 `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	AfterMemberId uint64 `protobuf:"varint,2,opt,name=after_member_id,json=afterMemberId,proto3" json:"after_member_id,omitempty"`
	Limit         uint32 `protobuf:"varint,3,opt,name=limit,proto3" json:"limit,omitempty"`
}

func (m *ListSpaceMembersReq) Reset()         { *m = ListSpaceMembersReq{} }
func (m *ListSpaceMembersReq) String() string { return proto.CompactTextString(m) }
func (*ListSpaceMembersReq) ProtoMessage()    {}

type ListSpaceMembersResp struct {
	SpaceId           uint32                `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	Members           []*SpaceMemberSummary `protobuf:"bytes,2,rep,name=members,proto3" json:"members,omitempty"`
	NextAfterMemberId uint64                `protobuf:"varint,3,opt,name=next_after_member_id,json=nextAfterMemberId,proto3" json:"next_after_member_id,omitempty"`
	HasMore           bool                  `protobuf:"varint,4,opt,name=has_more,json=hasMore,proto3" json:"has_more,omitempty"`
}

func (m *ListSpaceMembersResp) Reset()         { *m = ListSpaceMembersResp{} }
func (m *ListSpaceMembersResp) String() string { return proto.CompactTextString(m) }
func (*ListSpaceMembersResp) ProtoMessage()    {}

type UnbanSpaceMemberReq struct {
	SpaceId      uint32 `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	TargetUserId string `protobuf:"bytes,2,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
}

func (m *UnbanSpaceMemberReq) Reset()         { *m = UnbanSpaceMemberReq{} }
func (m *UnbanSpaceMemberReq) String() string { return proto.CompactTextString(m) }
func (*UnbanSpaceMemberReq) ProtoMessage()    {}

type UnbanSpaceMemberResp struct {
	Ok           bool          `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId      uint32        `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	TargetUserId string        `protobuf:"bytes,3,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Space        *SpaceSummary `protobuf:"bytes,4,opt,name=space,proto3" json:"space,omitempty"`
	Message      string        `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *UnbanSpaceMemberResp) Reset()         { *m = UnbanSpaceMemberResp{} }
func (m *UnbanSpaceMemberResp) String() string { return proto.CompactTextString(m) }
func (*UnbanSpaceMemberResp) ProtoMessage()    {}

type GetCreateSpacePermissionsReq struct {
}

func (m *GetCreateSpacePermissionsReq) Reset()         { *m = GetCreateSpacePermissionsReq{} }
func (m *GetCreateSpacePermissionsReq) String() string { return proto.CompactTextString(m) }
func (*GetCreateSpacePermissionsReq) ProtoMessage()    {}

type GetCreateSpacePermissionsResp struct {
	Ok             bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	CanCreateSpace bool   `protobuf:"varint,2,opt,name=can_create_space,json=canCreateSpace,proto3" json:"can_create_space,omitempty"`
	Message        string `protobuf:"bytes,3,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *GetCreateSpacePermissionsResp) Reset()         { *m = GetCreateSpacePermissionsResp{} }
func (m *GetCreateSpacePermissionsResp) String() string { return proto.CompactTextString(m) }
func (*GetCreateSpacePermissionsResp) ProtoMessage()    {}

type GetCreateGroupPermissionsReq struct {
	SpaceId uint32 `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
}

func (m *GetCreateGroupPermissionsReq) Reset()         { *m = GetCreateGroupPermissionsReq{} }
func (m *GetCreateGroupPermissionsReq) String() string { return proto.CompactTextString(m) }
func (*GetCreateGroupPermissionsReq) ProtoMessage()    {}

type GetCreateGroupPermissionsResp struct {
	Ok             bool          `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId        uint32        `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	Space          *SpaceSummary `protobuf:"bytes,3,opt,name=space,proto3" json:"space,omitempty"`
	Role           MemberRole    `protobuf:"varint,4,opt,name=role,proto3,enum=meshserver.session.v1.MemberRole" json:"role,omitempty"`
	CanCreateGroup bool          `protobuf:"varint,5,opt,name=can_create_group,json=canCreateGroup,proto3" json:"can_create_group,omitempty"`
	Message        string        `protobuf:"bytes,6,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *GetCreateGroupPermissionsResp) Reset()         { *m = GetCreateGroupPermissionsResp{} }
func (m *GetCreateGroupPermissionsResp) String() string { return proto.CompactTextString(m) }
func (*GetCreateGroupPermissionsResp) ProtoMessage()    {}

type AdminSetSpaceMemberRoleReq struct {
	SpaceId      uint32     `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	TargetUserId string     `protobuf:"bytes,2,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Role         MemberRole `protobuf:"varint,3,opt,name=role,proto3,enum=meshserver.session.v1.MemberRole" json:"role,omitempty"`
}

func (m *AdminSetSpaceMemberRoleReq) Reset()         { *m = AdminSetSpaceMemberRoleReq{} }
func (m *AdminSetSpaceMemberRoleReq) String() string { return proto.CompactTextString(m) }
func (*AdminSetSpaceMemberRoleReq) ProtoMessage()    {}

type AdminSetSpaceMemberRoleResp struct {
	Ok           bool       `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId      uint32     `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	TargetUserId string     `protobuf:"bytes,3,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Role         MemberRole `protobuf:"varint,4,opt,name=role,proto3,enum=meshserver.session.v1.MemberRole" json:"role,omitempty"`
	Message      string     `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *AdminSetSpaceMemberRoleResp) Reset()         { *m = AdminSetSpaceMemberRoleResp{} }
func (m *AdminSetSpaceMemberRoleResp) String() string { return proto.CompactTextString(m) }
func (*AdminSetSpaceMemberRoleResp) ProtoMessage()    {}

type AdminSetSpaceChannelCreationReq struct {
	SpaceId              uint32 `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	AllowChannelCreation bool   `protobuf:"varint,2,opt,name=allow_channel_creation,json=allowChannelCreation,proto3" json:"allow_channel_creation,omitempty"`
}

func (m *AdminSetSpaceChannelCreationReq) Reset()         { *m = AdminSetSpaceChannelCreationReq{} }
func (m *AdminSetSpaceChannelCreationReq) String() string { return proto.CompactTextString(m) }
func (*AdminSetSpaceChannelCreationReq) ProtoMessage()    {}

type AdminSetSpaceChannelCreationResp struct {
	Ok                   bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId              uint32 `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	AllowChannelCreation bool   `protobuf:"varint,3,opt,name=allow_channel_creation,json=allowChannelCreation,proto3" json:"allow_channel_creation,omitempty"`
	Message              string `protobuf:"bytes,4,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *AdminSetSpaceChannelCreationResp) Reset()         { *m = AdminSetSpaceChannelCreationResp{} }
func (m *AdminSetSpaceChannelCreationResp) String() string { return proto.CompactTextString(m) }
func (*AdminSetSpaceChannelCreationResp) ProtoMessage()    {}

type JoinSpaceReq struct {
	SpaceId uint32 `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
}

func (m *JoinSpaceReq) Reset()         { *m = JoinSpaceReq{} }
func (m *JoinSpaceReq) String() string { return proto.CompactTextString(m) }
func (*JoinSpaceReq) ProtoMessage()    {}

type JoinSpaceResp struct {
	Ok      bool          `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId uint32        `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	Space   *SpaceSummary `protobuf:"bytes,3,opt,name=space,proto3" json:"space,omitempty"`
	Message string        `protobuf:"bytes,4,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *JoinSpaceResp) Reset()         { *m = JoinSpaceResp{} }
func (m *JoinSpaceResp) String() string { return proto.CompactTextString(m) }
func (*JoinSpaceResp) ProtoMessage()    {}

type InviteSpaceMemberReq struct {
	SpaceId      uint32 `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	TargetUserId string `protobuf:"bytes,2,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
}

func (m *InviteSpaceMemberReq) Reset()         { *m = InviteSpaceMemberReq{} }
func (m *InviteSpaceMemberReq) String() string { return proto.CompactTextString(m) }
func (*InviteSpaceMemberReq) ProtoMessage()    {}

type InviteSpaceMemberResp struct {
	Ok           bool          `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId      uint32        `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	TargetUserId string        `protobuf:"bytes,3,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Space        *SpaceSummary `protobuf:"bytes,4,opt,name=space,proto3" json:"space,omitempty"`
	Message      string        `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *InviteSpaceMemberResp) Reset()         { *m = InviteSpaceMemberResp{} }
func (m *InviteSpaceMemberResp) String() string { return proto.CompactTextString(m) }
func (*InviteSpaceMemberResp) ProtoMessage()    {}

type KickSpaceMemberReq struct {
	SpaceId      uint32 `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	TargetUserId string `protobuf:"bytes,2,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
}

func (m *KickSpaceMemberReq) Reset()         { *m = KickSpaceMemberReq{} }
func (m *KickSpaceMemberReq) String() string { return proto.CompactTextString(m) }
func (*KickSpaceMemberReq) ProtoMessage()    {}

type KickSpaceMemberResp struct {
	Ok           bool          `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId      uint32        `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	TargetUserId string        `protobuf:"bytes,3,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Space        *SpaceSummary `protobuf:"bytes,4,opt,name=space,proto3" json:"space,omitempty"`
	Message      string        `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *KickSpaceMemberResp) Reset()         { *m = KickSpaceMemberResp{} }
func (m *KickSpaceMemberResp) String() string { return proto.CompactTextString(m) }
func (*KickSpaceMemberResp) ProtoMessage()    {}

type BanSpaceMemberReq struct {
	SpaceId      uint32 `protobuf:"varint,1,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	TargetUserId string `protobuf:"bytes,2,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
}

func (m *BanSpaceMemberReq) Reset()         { *m = BanSpaceMemberReq{} }
func (m *BanSpaceMemberReq) String() string { return proto.CompactTextString(m) }
func (*BanSpaceMemberReq) ProtoMessage()    {}

type BanSpaceMemberResp struct {
	Ok           bool          `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SpaceId      uint32        `protobuf:"varint,2,opt,name=space_id,json=spaceId,proto3" json:"space_id,omitempty"`
	TargetUserId string        `protobuf:"bytes,3,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Space        *SpaceSummary `protobuf:"bytes,4,opt,name=space,proto3" json:"space,omitempty"`
	Message      string        `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *BanSpaceMemberResp) Reset()         { *m = BanSpaceMemberResp{} }
func (m *BanSpaceMemberResp) String() string { return proto.CompactTextString(m) }
func (*BanSpaceMemberResp) ProtoMessage()    {}

type MediaImage struct {
	MediaId      string `protobuf:"bytes,1,opt,name=media_id,json=mediaId,proto3" json:"media_id,omitempty"`
	BlobId       string `protobuf:"bytes,2,opt,name=blob_id,json=blobId,proto3" json:"blob_id,omitempty"`
	Sha256       string `protobuf:"bytes,3,opt,name=sha256,proto3" json:"sha256,omitempty"`
	Url          string `protobuf:"bytes,4,opt,name=url,proto3" json:"url,omitempty"`
	Width        uint32 `protobuf:"varint,5,opt,name=width,proto3" json:"width,omitempty"`
	Height       uint32 `protobuf:"varint,6,opt,name=height,proto3" json:"height,omitempty"`
	MimeType     string `protobuf:"bytes,7,opt,name=mime_type,json=mimeType,proto3" json:"mime_type,omitempty"`
	Size         uint64 `protobuf:"varint,8,opt,name=size,proto3" json:"size,omitempty"`
	InlineData   []byte `protobuf:"bytes,9,opt,name=inline_data,json=inlineData,proto3" json:"inline_data,omitempty"`
	OriginalName string `protobuf:"bytes,10,opt,name=original_name,json=originalName,proto3" json:"original_name,omitempty"`
}

func (m *MediaImage) Reset()         { *m = MediaImage{} }
func (m *MediaImage) String() string { return proto.CompactTextString(m) }
func (*MediaImage) ProtoMessage()    {}

type MediaFile struct {
	MediaId    string `protobuf:"bytes,1,opt,name=media_id,json=mediaId,proto3" json:"media_id,omitempty"`
	BlobId     string `protobuf:"bytes,2,opt,name=blob_id,json=blobId,proto3" json:"blob_id,omitempty"`
	Sha256     string `protobuf:"bytes,3,opt,name=sha256,proto3" json:"sha256,omitempty"`
	FileName   string `protobuf:"bytes,4,opt,name=file_name,json=fileName,proto3" json:"file_name,omitempty"`
	Url        string `protobuf:"bytes,5,opt,name=url,proto3" json:"url,omitempty"`
	MimeType   string `protobuf:"bytes,6,opt,name=mime_type,json=mimeType,proto3" json:"mime_type,omitempty"`
	Size       uint64 `protobuf:"varint,7,opt,name=size,proto3" json:"size,omitempty"`
	InlineData []byte `protobuf:"bytes,8,opt,name=inline_data,json=inlineData,proto3" json:"inline_data,omitempty"`
	FileCid    string `protobuf:"bytes,9,opt,name=file_cid,json=fileCid,proto3" json:"file_cid,omitempty"`
}

func (m *MediaFile) Reset()         { *m = MediaFile{} }
func (m *MediaFile) String() string { return proto.CompactTextString(m) }
func (*MediaFile) ProtoMessage()    {}

type MessageContent struct {
	Images []*MediaImage `protobuf:"bytes,1,rep,name=images,proto3" json:"images,omitempty"`
	Files  []*MediaFile  `protobuf:"bytes,2,rep,name=files,proto3" json:"files,omitempty"`
	Text   string        `protobuf:"bytes,3,opt,name=text,proto3" json:"text,omitempty"`
}

func (m *MessageContent) Reset()         { *m = MessageContent{} }
func (m *MessageContent) String() string { return proto.CompactTextString(m) }
func (*MessageContent) ProtoMessage()    {}

type SendMessageReq struct {
	ChannelId   uint32          `protobuf:"varint,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	ClientMsgId string          `protobuf:"bytes,2,opt,name=client_msg_id,json=clientMsgId,proto3" json:"client_msg_id,omitempty"`
	MessageType MessageType     `protobuf:"varint,3,opt,name=message_type,json=messageType,proto3,enum=meshserver.session.v1.MessageType" json:"message_type,omitempty"`
	Content     *MessageContent `protobuf:"bytes,4,opt,name=content,proto3" json:"content,omitempty"`
}

func (m *SendMessageReq) Reset()         { *m = SendMessageReq{} }
func (m *SendMessageReq) String() string { return proto.CompactTextString(m) }
func (*SendMessageReq) ProtoMessage()    {}

type SendMessageAck struct {
	Ok           bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ChannelId    uint32 `protobuf:"varint,2,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	ClientMsgId  string `protobuf:"bytes,3,opt,name=client_msg_id,json=clientMsgId,proto3" json:"client_msg_id,omitempty"`
	MessageId    string `protobuf:"bytes,4,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	Seq          uint64 `protobuf:"varint,5,opt,name=seq,proto3" json:"seq,omitempty"`
	ServerTimeMs uint64 `protobuf:"varint,6,opt,name=server_time_ms,json=serverTimeMs,proto3" json:"server_time_ms,omitempty"`
	Message      string `protobuf:"bytes,7,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *SendMessageAck) Reset()         { *m = SendMessageAck{} }
func (m *SendMessageAck) String() string { return proto.CompactTextString(m) }
func (*SendMessageAck) ProtoMessage()    {}

type MessageEvent struct {
	ChannelId    uint32          `protobuf:"varint,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	MessageId    string          `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	Seq          uint64          `protobuf:"varint,3,opt,name=seq,proto3" json:"seq,omitempty"`
	SenderUserId string          `protobuf:"bytes,4,opt,name=sender_user_id,json=senderUserId,proto3" json:"sender_user_id,omitempty"`
	MessageType  MessageType     `protobuf:"varint,5,opt,name=message_type,json=messageType,proto3,enum=meshserver.session.v1.MessageType" json:"message_type,omitempty"`
	Content      *MessageContent `protobuf:"bytes,6,opt,name=content,proto3" json:"content,omitempty"`
	CreatedAtMs  uint64          `protobuf:"varint,7,opt,name=created_at_ms,json=createdAtMs,proto3" json:"created_at_ms,omitempty"`
}

func (m *MessageEvent) Reset()         { *m = MessageEvent{} }
func (m *MessageEvent) String() string { return proto.CompactTextString(m) }
func (*MessageEvent) ProtoMessage()    {}

type ChannelDeliverAck struct {
	ChannelId uint32 `protobuf:"varint,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	AckedSeq  uint64 `protobuf:"varint,2,opt,name=acked_seq,json=ackedSeq,proto3" json:"acked_seq,omitempty"`
}

func (m *ChannelDeliverAck) Reset()         { *m = ChannelDeliverAck{} }
func (m *ChannelDeliverAck) String() string { return proto.CompactTextString(m) }
func (*ChannelDeliverAck) ProtoMessage()    {}

type ChannelReadUpdate struct {
	ChannelId   uint32 `protobuf:"varint,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	LastReadSeq uint64 `protobuf:"varint,2,opt,name=last_read_seq,json=lastReadSeq,proto3" json:"last_read_seq,omitempty"`
}

func (m *ChannelReadUpdate) Reset()         { *m = ChannelReadUpdate{} }
func (m *ChannelReadUpdate) String() string { return proto.CompactTextString(m) }
func (*ChannelReadUpdate) ProtoMessage()    {}

type SyncChannelReq struct {
	ChannelId uint32 `protobuf:"varint,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	AfterSeq  uint64 `protobuf:"varint,2,opt,name=after_seq,json=afterSeq,proto3" json:"after_seq,omitempty"`
	Limit     uint32 `protobuf:"varint,3,opt,name=limit,proto3" json:"limit,omitempty"`
}

func (m *SyncChannelReq) Reset()         { *m = SyncChannelReq{} }
func (m *SyncChannelReq) String() string { return proto.CompactTextString(m) }
func (*SyncChannelReq) ProtoMessage()    {}

type SyncChannelResp struct {
	ChannelId    uint32          `protobuf:"varint,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	Messages     []*MessageEvent `protobuf:"bytes,2,rep,name=messages,proto3" json:"messages,omitempty"`
	NextAfterSeq uint64          `protobuf:"varint,3,opt,name=next_after_seq,json=nextAfterSeq,proto3" json:"next_after_seq,omitempty"`
	HasMore      bool            `protobuf:"varint,4,opt,name=has_more,json=hasMore,proto3" json:"has_more,omitempty"`
}

func (m *SyncChannelResp) Reset()         { *m = SyncChannelResp{} }
func (m *SyncChannelResp) String() string { return proto.CompactTextString(m) }
func (*SyncChannelResp) ProtoMessage()    {}

type GetMediaReq struct {
	MediaId string `protobuf:"bytes,1,opt,name=media_id,json=mediaId,proto3" json:"media_id,omitempty"`
}

func (m *GetMediaReq) Reset()         { *m = GetMediaReq{} }
func (m *GetMediaReq) String() string { return proto.CompactTextString(m) }
func (*GetMediaReq) ProtoMessage()    {}

type GetMediaResp struct {
	Ok      bool       `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	MediaId string     `protobuf:"bytes,2,opt,name=media_id,json=mediaId,proto3" json:"media_id,omitempty"`
	File    *MediaFile `protobuf:"bytes,3,opt,name=file,proto3" json:"file,omitempty"`
	Message string     `protobuf:"bytes,4,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *GetMediaResp) Reset()         { *m = GetMediaResp{} }
func (m *GetMediaResp) String() string { return proto.CompactTextString(m) }
func (*GetMediaResp) ProtoMessage()    {}

type AdminSetGroupAutoDeleteReq struct {
	ChannelId              uint32 `protobuf:"varint,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	AutoDeleteAfterSeconds uint32 `protobuf:"varint,2,opt,name=auto_delete_after_seconds,json=autoDeleteAfterSeconds,proto3" json:"auto_delete_after_seconds,omitempty"`
}

func (m *AdminSetGroupAutoDeleteReq) Reset()         { *m = AdminSetGroupAutoDeleteReq{} }
func (m *AdminSetGroupAutoDeleteReq) String() string { return proto.CompactTextString(m) }
func (*AdminSetGroupAutoDeleteReq) ProtoMessage()    {}

type AdminSetGroupAutoDeleteResp struct {
	Ok                     bool          `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ChannelId              uint32        `protobuf:"varint,2,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	AutoDeleteAfterSeconds uint32        `protobuf:"varint,3,opt,name=auto_delete_after_seconds,json=autoDeleteAfterSeconds,proto3" json:"auto_delete_after_seconds,omitempty"`
	Channel                *ChannelSummary `protobuf:"bytes,4,opt,name=channel,proto3" json:"channel,omitempty"`
	Message                string        `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *AdminSetGroupAutoDeleteResp) Reset()         { *m = AdminSetGroupAutoDeleteResp{} }
func (m *AdminSetGroupAutoDeleteResp) String() string { return proto.CompactTextString(m) }
func (*AdminSetGroupAutoDeleteResp) ProtoMessage()    {}

type OpenDirectConversationReq struct {
	PeerUserId string `protobuf:"bytes,1,opt,name=peer_user_id,json=peerUserId,proto3" json:"peer_user_id,omitempty"`
}

func (m *OpenDirectConversationReq) Reset()         { *m = OpenDirectConversationReq{} }
func (m *OpenDirectConversationReq) String() string { return proto.CompactTextString(m) }
func (*OpenDirectConversationReq) ProtoMessage()    {}

type OpenDirectConversationResp struct {
	Ok             bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ConversationId string `protobuf:"bytes,2,opt,name=conversation_id,json=conversationId,proto3" json:"conversation_id,omitempty"`
	PeerUserId     string `protobuf:"bytes,3,opt,name=peer_user_id,json=peerUserId,proto3" json:"peer_user_id,omitempty"`
	LastSeq        uint64 `protobuf:"varint,4,opt,name=last_seq,json=lastSeq,proto3" json:"last_seq,omitempty"`
	Message        string `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *OpenDirectConversationResp) Reset()         { *m = OpenDirectConversationResp{} }
func (m *OpenDirectConversationResp) String() string { return proto.CompactTextString(m) }
func (*OpenDirectConversationResp) ProtoMessage()    {}

type DirectConversationSummary struct {
	ConversationId    string `protobuf:"bytes,1,opt,name=conversation_id,json=conversationId,proto3" json:"conversation_id,omitempty"`
	PeerUserId        string `protobuf:"bytes,2,opt,name=peer_user_id,json=peerUserId,proto3" json:"peer_user_id,omitempty"`
	PeerDisplayName   string `protobuf:"bytes,3,opt,name=peer_display_name,json=peerDisplayName,proto3" json:"peer_display_name,omitempty"`
	LastSeq           uint64 `protobuf:"varint,4,opt,name=last_seq,json=lastSeq,proto3" json:"last_seq,omitempty"`
	LastMessageAtMs   uint64 `protobuf:"varint,5,opt,name=last_message_at_ms,json=lastMessageAtMs,proto3" json:"last_message_at_ms,omitempty"`
}

func (m *DirectConversationSummary) Reset()         { *m = DirectConversationSummary{} }
func (m *DirectConversationSummary) String() string { return proto.CompactTextString(m) }
func (*DirectConversationSummary) ProtoMessage()    {}

type ListDirectConversationsReq struct{}

func (m *ListDirectConversationsReq) Reset()         { *m = ListDirectConversationsReq{} }
func (m *ListDirectConversationsReq) String() string { return proto.CompactTextString(m) }
func (*ListDirectConversationsReq) ProtoMessage()    {}

type ListDirectConversationsResp struct {
	Conversations []*DirectConversationSummary `protobuf:"bytes,1,rep,name=conversations,proto3" json:"conversations,omitempty"`
}

func (m *ListDirectConversationsResp) Reset()         { *m = ListDirectConversationsResp{} }
func (m *ListDirectConversationsResp) String() string { return proto.CompactTextString(m) }
func (*ListDirectConversationsResp) ProtoMessage()    {}

type SendDirectMessageReq struct {
	ConversationId string      `protobuf:"bytes,1,opt,name=conversation_id,json=conversationId,proto3" json:"conversation_id,omitempty"`
	ToUserId       string      `protobuf:"bytes,2,opt,name=to_user_id,json=toUserId,proto3" json:"to_user_id,omitempty"`
	ClientMsgId    string      `protobuf:"bytes,3,opt,name=client_msg_id,json=clientMsgId,proto3" json:"client_msg_id,omitempty"`
	MessageType    MessageType `protobuf:"varint,4,opt,name=message_type,json=messageType,proto3,enum=meshserver.session.v1.MessageType" json:"message_type,omitempty"`
	Text           string      `protobuf:"bytes,5,opt,name=text,proto3" json:"text,omitempty"`
}

func (m *SendDirectMessageReq) Reset()         { *m = SendDirectMessageReq{} }
func (m *SendDirectMessageReq) String() string { return proto.CompactTextString(m) }
func (*SendDirectMessageReq) ProtoMessage()    {}

type SendDirectMessageAck struct {
	Ok           bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ConversationId string `protobuf:"bytes,2,opt,name=conversation_id,json=conversationId,proto3" json:"conversation_id,omitempty"`
	ClientMsgId    string `protobuf:"bytes,3,opt,name=client_msg_id,json=clientMsgId,proto3" json:"client_msg_id,omitempty"`
	MessageId      string `protobuf:"bytes,4,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	Seq            uint64 `protobuf:"varint,5,opt,name=seq,proto3" json:"seq,omitempty"`
	ServerTimeMs   uint64 `protobuf:"varint,6,opt,name=server_time_ms,json=serverTimeMs,proto3" json:"server_time_ms,omitempty"`
	Message        string `protobuf:"bytes,7,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *SendDirectMessageAck) Reset()         { *m = SendDirectMessageAck{} }
func (m *SendDirectMessageAck) String() string { return proto.CompactTextString(m) }
func (*SendDirectMessageAck) ProtoMessage()    {}

type DirectMessageEvent struct {
	ConversationId string      `protobuf:"bytes,1,opt,name=conversation_id,json=conversationId,proto3" json:"conversation_id,omitempty"`
	MessageId      string      `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	Seq            uint64      `protobuf:"varint,3,opt,name=seq,proto3" json:"seq,omitempty"`
	FromUserId     string      `protobuf:"bytes,4,opt,name=from_user_id,json=fromUserId,proto3" json:"from_user_id,omitempty"`
	ToUserId       string      `protobuf:"bytes,5,opt,name=to_user_id,json=toUserId,proto3" json:"to_user_id,omitempty"`
	MessageType    MessageType `protobuf:"varint,6,opt,name=message_type,json=messageType,proto3,enum=meshserver.session.v1.MessageType" json:"message_type,omitempty"`
	Text           string      `protobuf:"bytes,7,opt,name=text,proto3" json:"text,omitempty"`
	CreatedAtMs    uint64      `protobuf:"varint,8,opt,name=created_at_ms,json=createdAtMs,proto3" json:"created_at_ms,omitempty"`
}

func (m *DirectMessageEvent) Reset()         { *m = DirectMessageEvent{} }
func (m *DirectMessageEvent) String() string { return proto.CompactTextString(m) }
func (*DirectMessageEvent) ProtoMessage()    {}

type AckDirectMessageReq struct {
	MessageId string `protobuf:"bytes,1,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
}

func (m *AckDirectMessageReq) Reset()         { *m = AckDirectMessageReq{} }
func (m *AckDirectMessageReq) String() string { return proto.CompactTextString(m) }
func (*AckDirectMessageReq) ProtoMessage()    {}

type AckDirectMessageResp struct {
	Ok        bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	MessageId string `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	Message   string `protobuf:"bytes,3,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *AckDirectMessageResp) Reset()         { *m = AckDirectMessageResp{} }
func (m *AckDirectMessageResp) String() string { return proto.CompactTextString(m) }
func (*AckDirectMessageResp) ProtoMessage()    {}

type DirectPeerAckEvent struct {
	ConversationId string `protobuf:"bytes,1,opt,name=conversation_id,json=conversationId,proto3" json:"conversation_id,omitempty"`
	MessageId      string `protobuf:"bytes,2,opt,name=message_id,json=messageId,proto3" json:"message_id,omitempty"`
	AckedAtMs      uint64 `protobuf:"varint,3,opt,name=acked_at_ms,json=ackedAtMs,proto3" json:"acked_at_ms,omitempty"`
	PeerUserId     string `protobuf:"bytes,4,opt,name=peer_user_id,json=peerUserId,proto3" json:"peer_user_id,omitempty"`
}

func (m *DirectPeerAckEvent) Reset()         { *m = DirectPeerAckEvent{} }
func (m *DirectPeerAckEvent) String() string { return proto.CompactTextString(m) }
func (*DirectPeerAckEvent) ProtoMessage()    {}

type SyncDirectMessagesReq struct {
	ConversationId string `protobuf:"bytes,1,opt,name=conversation_id,json=conversationId,proto3" json:"conversation_id,omitempty"`
	AfterSeq       uint64 `protobuf:"varint,2,opt,name=after_seq,json=afterSeq,proto3" json:"after_seq,omitempty"`
	Limit          uint32 `protobuf:"varint,3,opt,name=limit,proto3" json:"limit,omitempty"`
}

func (m *SyncDirectMessagesReq) Reset()         { *m = SyncDirectMessagesReq{} }
func (m *SyncDirectMessagesReq) String() string { return proto.CompactTextString(m) }
func (*SyncDirectMessagesReq) ProtoMessage()    {}

type SyncDirectMessagesResp struct {
	ConversationId string                `protobuf:"bytes,1,opt,name=conversation_id,json=conversationId,proto3" json:"conversation_id,omitempty"`
	Messages       []*DirectMessageEvent `protobuf:"bytes,2,rep,name=messages,proto3" json:"messages,omitempty"`
	NextAfterSeq   uint64                `protobuf:"varint,3,opt,name=next_after_seq,json=nextAfterSeq,proto3" json:"next_after_seq,omitempty"`
	HasMore        bool                  `protobuf:"varint,4,opt,name=has_more,json=hasMore,proto3" json:"has_more,omitempty"`
}

func (m *SyncDirectMessagesResp) Reset()         { *m = SyncDirectMessagesResp{} }
func (m *SyncDirectMessagesResp) String() string { return proto.CompactTextString(m) }
func (*SyncDirectMessagesResp) ProtoMessage()    {}

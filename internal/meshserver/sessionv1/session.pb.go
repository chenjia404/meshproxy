package sessionv1

import proto "github.com/golang/protobuf/proto"

const _ = proto.ProtoPackageIsVersion4

type MsgType int32

const (
	MsgType_MSG_TYPE_UNSPECIFIED            MsgType = 0
	MsgType_HELLO                           MsgType = 1
	MsgType_AUTH_CHALLENGE                  MsgType = 2
	MsgType_AUTH_PROVE                      MsgType = 3
	MsgType_AUTH_RESULT                     MsgType = 4
	MsgType_PING                            MsgType = 10
	MsgType_PONG                            MsgType = 11
	MsgType_ERROR                           MsgType = 12
	MsgType_LIST_SERVERS_REQ                MsgType = 20
	MsgType_LIST_SERVERS_RESP               MsgType = 21
	MsgType_LIST_CHANNELS_REQ               MsgType = 22
	MsgType_LIST_CHANNELS_RESP              MsgType = 23
	MsgType_SUBSCRIBE_CHANNEL_REQ           MsgType = 24
	MsgType_SUBSCRIBE_CHANNEL_RESP          MsgType = 25
	MsgType_UNSUBSCRIBE_CHANNEL_REQ         MsgType = 26
	MsgType_UNSUBSCRIBE_CHANNEL_RESP        MsgType = 27
	MsgType_CREATE_GROUP_REQ                MsgType = 28
	MsgType_CREATE_GROUP_RESP               MsgType = 29
	MsgType_SEND_MESSAGE_REQ                MsgType = 30
	MsgType_SEND_MESSAGE_ACK                MsgType = 31
	MsgType_MESSAGE_EVENT                   MsgType = 32
	MsgType_CHANNEL_DELIVER_ACK             MsgType = 33
	MsgType_CHANNEL_READ_UPDATE             MsgType = 34
	MsgType_SYNC_CHANNEL_REQ                MsgType = 35
	MsgType_SYNC_CHANNEL_RESP               MsgType = 36
	MsgType_CREATE_CHANNEL_REQ              MsgType = 37
	MsgType_CREATE_CHANNEL_RESP             MsgType = 38
	MsgType_ADMIN_SET_MEMBER_ROLE_REQ       MsgType = 39
	MsgType_ADMIN_SET_MEMBER_ROLE_RESP      MsgType = 40
	MsgType_ADMIN_SET_CHANNEL_CREATION_REQ  MsgType = 41
	MsgType_ADMIN_SET_CHANNEL_CREATION_RESP MsgType = 42
	MsgType_JOIN_SERVER_REQ                 MsgType = 43
	MsgType_JOIN_SERVER_RESP                MsgType = 44
	MsgType_INVITE_SERVER_MEMBER_REQ        MsgType = 45
	MsgType_INVITE_SERVER_MEMBER_RESP       MsgType = 46
	MsgType_KICK_SERVER_MEMBER_REQ          MsgType = 47
	MsgType_KICK_SERVER_MEMBER_RESP         MsgType = 48
	MsgType_BAN_SERVER_MEMBER_REQ           MsgType = 49
	MsgType_BAN_SERVER_MEMBER_RESP          MsgType = 50
	MsgType_LIST_SERVER_MEMBERS_REQ         MsgType = 51
	MsgType_LIST_SERVER_MEMBERS_RESP        MsgType = 52
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
	20: "LIST_SERVERS_REQ",
	21: "LIST_SERVERS_RESP",
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
	39: "ADMIN_SET_MEMBER_ROLE_REQ",
	40: "ADMIN_SET_MEMBER_ROLE_RESP",
	41: "ADMIN_SET_CHANNEL_CREATION_REQ",
	42: "ADMIN_SET_CHANNEL_CREATION_RESP",
	43: "JOIN_SERVER_REQ",
	44: "JOIN_SERVER_RESP",
	45: "INVITE_SERVER_MEMBER_REQ",
	46: "INVITE_SERVER_MEMBER_RESP",
	47: "KICK_SERVER_MEMBER_REQ",
	48: "KICK_SERVER_MEMBER_RESP",
	49: "BAN_SERVER_MEMBER_REQ",
	50: "BAN_SERVER_MEMBER_RESP",
	51: "LIST_SERVER_MEMBERS_REQ",
	52: "LIST_SERVER_MEMBERS_RESP",
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
	ServerPeerId string `protobuf:"bytes,1,opt,name=server_peer_id,json=serverPeerId,proto3" json:"server_peer_id,omitempty"`
	Nonce        []byte `protobuf:"bytes,2,opt,name=nonce,proto3" json:"nonce,omitempty"`
	IssuedAtMs   uint64 `protobuf:"varint,3,opt,name=issued_at_ms,json=issuedAtMs,proto3" json:"issued_at_ms,omitempty"`
	ExpiresAtMs  uint64 `protobuf:"varint,4,opt,name=expires_at_ms,json=expiresAtMs,proto3" json:"expires_at_ms,omitempty"`
	SessionHint  string `protobuf:"bytes,5,opt,name=session_hint,json=sessionHint,proto3" json:"session_hint,omitempty"`
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
	Ok          bool             `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	SessionId   string           `protobuf:"bytes,2,opt,name=session_id,json=sessionId,proto3" json:"session_id,omitempty"`
	UserId      string           `protobuf:"bytes,3,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	DisplayName string           `protobuf:"bytes,4,opt,name=display_name,json=displayName,proto3" json:"display_name,omitempty"`
	Message     string           `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
	Servers     []*ServerSummary `protobuf:"bytes,6,rep,name=servers,proto3" json:"servers,omitempty"`
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

type ServerSummary struct {
	ServerId             string     `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	Name                 string     `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	AvatarUrl            string     `protobuf:"bytes,3,opt,name=avatar_url,json=avatarUrl,proto3" json:"avatar_url,omitempty"`
	Description          string     `protobuf:"bytes,4,opt,name=description,proto3" json:"description,omitempty"`
	Visibility           Visibility `protobuf:"varint,5,opt,name=visibility,proto3,enum=meshserver.session.v1.Visibility" json:"visibility,omitempty"`
	MemberCount          uint32     `protobuf:"varint,6,opt,name=member_count,json=memberCount,proto3" json:"member_count,omitempty"`
	AllowChannelCreation bool       `protobuf:"varint,7,opt,name=allow_channel_creation,json=allowChannelCreation,proto3" json:"allow_channel_creation,omitempty"`
}

func (m *ServerSummary) Reset()         { *m = ServerSummary{} }
func (m *ServerSummary) String() string { return proto.CompactTextString(m) }
func (*ServerSummary) ProtoMessage()    {}

type ListServersReq struct{}

func (m *ListServersReq) Reset()         { *m = ListServersReq{} }
func (m *ListServersReq) String() string { return proto.CompactTextString(m) }
func (*ListServersReq) ProtoMessage()    {}

type ListServersResp struct {
	Servers []*ServerSummary `protobuf:"bytes,1,rep,name=servers,proto3" json:"servers,omitempty"`
}

func (m *ListServersResp) Reset()         { *m = ListServersResp{} }
func (m *ListServersResp) String() string { return proto.CompactTextString(m) }
func (*ListServersResp) ProtoMessage()    {}

type ChannelSummary struct {
	ChannelId       string      `protobuf:"bytes,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	ServerId        string      `protobuf:"bytes,2,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	Type            ChannelType `protobuf:"varint,3,opt,name=type,proto3,enum=meshserver.session.v1.ChannelType" json:"type,omitempty"`
	Name            string      `protobuf:"bytes,4,opt,name=name,proto3" json:"name,omitempty"`
	Description     string      `protobuf:"bytes,5,opt,name=description,proto3" json:"description,omitempty"`
	Visibility      Visibility  `protobuf:"varint,6,opt,name=visibility,proto3,enum=meshserver.session.v1.Visibility" json:"visibility,omitempty"`
	SlowModeSeconds uint32      `protobuf:"varint,7,opt,name=slow_mode_seconds,json=slowModeSeconds,proto3" json:"slow_mode_seconds,omitempty"`
	LastSeq         uint64      `protobuf:"varint,8,opt,name=last_seq,json=lastSeq,proto3" json:"last_seq,omitempty"`
	CanView         bool        `protobuf:"varint,9,opt,name=can_view,json=canView,proto3" json:"can_view,omitempty"`
	CanSendMessage  bool        `protobuf:"varint,10,opt,name=can_send_message,json=canSendMessage,proto3" json:"can_send_message,omitempty"`
	CanSendImage    bool        `protobuf:"varint,11,opt,name=can_send_image,json=canSendImage,proto3" json:"can_send_image,omitempty"`
	CanSendFile     bool        `protobuf:"varint,12,opt,name=can_send_file,json=canSendFile,proto3" json:"can_send_file,omitempty"`
}

func (m *ChannelSummary) Reset()         { *m = ChannelSummary{} }
func (m *ChannelSummary) String() string { return proto.CompactTextString(m) }
func (*ChannelSummary) ProtoMessage()    {}

type ListChannelsReq struct {
	ServerId string `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
}

func (m *ListChannelsReq) Reset()         { *m = ListChannelsReq{} }
func (m *ListChannelsReq) String() string { return proto.CompactTextString(m) }
func (*ListChannelsReq) ProtoMessage()    {}

type ListChannelsResp struct {
	ServerId string            `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	Channels []*ChannelSummary `protobuf:"bytes,2,rep,name=channels,proto3" json:"channels,omitempty"`
}

func (m *ListChannelsResp) Reset()         { *m = ListChannelsResp{} }
func (m *ListChannelsResp) String() string { return proto.CompactTextString(m) }
func (*ListChannelsResp) ProtoMessage()    {}

type SubscribeChannelReq struct {
	ChannelId   string `protobuf:"bytes,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	LastSeenSeq uint64 `protobuf:"varint,2,opt,name=last_seen_seq,json=lastSeenSeq,proto3" json:"last_seen_seq,omitempty"`
}

func (m *SubscribeChannelReq) Reset()         { *m = SubscribeChannelReq{} }
func (m *SubscribeChannelReq) String() string { return proto.CompactTextString(m) }
func (*SubscribeChannelReq) ProtoMessage()    {}

type SubscribeChannelResp struct {
	Ok             bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ChannelId      string `protobuf:"bytes,2,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	CurrentLastSeq uint64 `protobuf:"varint,3,opt,name=current_last_seq,json=currentLastSeq,proto3" json:"current_last_seq,omitempty"`
	Message        string `protobuf:"bytes,4,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *SubscribeChannelResp) Reset()         { *m = SubscribeChannelResp{} }
func (m *SubscribeChannelResp) String() string { return proto.CompactTextString(m) }
func (*SubscribeChannelResp) ProtoMessage()    {}

type UnsubscribeChannelReq struct {
	ChannelId string `protobuf:"bytes,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
}

func (m *UnsubscribeChannelReq) Reset()         { *m = UnsubscribeChannelReq{} }
func (m *UnsubscribeChannelReq) String() string { return proto.CompactTextString(m) }
func (*UnsubscribeChannelReq) ProtoMessage()    {}

type UnsubscribeChannelResp struct {
	Ok        bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ChannelId string `protobuf:"bytes,2,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
}

func (m *UnsubscribeChannelResp) Reset()         { *m = UnsubscribeChannelResp{} }
func (m *UnsubscribeChannelResp) String() string { return proto.CompactTextString(m) }
func (*UnsubscribeChannelResp) ProtoMessage()    {}

type CreateGroupReq struct {
	ServerId        string     `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
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
	ServerId  string          `protobuf:"bytes,2,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	ChannelId string          `protobuf:"bytes,3,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	Channel   *ChannelSummary `protobuf:"bytes,4,opt,name=channel,proto3" json:"channel,omitempty"`
	Message   string          `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *CreateGroupResp) Reset()         { *m = CreateGroupResp{} }
func (m *CreateGroupResp) String() string { return proto.CompactTextString(m) }
func (*CreateGroupResp) ProtoMessage()    {}

type CreateChannelReq struct {
	ServerId        string     `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
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
	ServerId  string          `protobuf:"bytes,2,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	ChannelId string          `protobuf:"bytes,3,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	Channel   *ChannelSummary `protobuf:"bytes,4,opt,name=channel,proto3" json:"channel,omitempty"`
	Message   string          `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *CreateChannelResp) Reset()         { *m = CreateChannelResp{} }
func (m *CreateChannelResp) String() string { return proto.CompactTextString(m) }
func (*CreateChannelResp) ProtoMessage()    {}

type AdminSetMemberRoleReq struct {
	ServerId     string     `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	TargetUserId string     `protobuf:"bytes,2,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Role         MemberRole `protobuf:"varint,3,opt,name=role,proto3,enum=meshserver.session.v1.MemberRole" json:"role,omitempty"`
}

func (m *AdminSetMemberRoleReq) Reset()         { *m = AdminSetMemberRoleReq{} }
func (m *AdminSetMemberRoleReq) String() string { return proto.CompactTextString(m) }
func (*AdminSetMemberRoleReq) ProtoMessage()    {}

type AdminSetMemberRoleResp struct {
	Ok           bool       `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ServerId     string     `protobuf:"bytes,2,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	TargetUserId string     `protobuf:"bytes,3,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Role         MemberRole `protobuf:"varint,4,opt,name=role,proto3,enum=meshserver.session.v1.MemberRole" json:"role,omitempty"`
	Message      string     `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *AdminSetMemberRoleResp) Reset()         { *m = AdminSetMemberRoleResp{} }
func (m *AdminSetMemberRoleResp) String() string { return proto.CompactTextString(m) }
func (*AdminSetMemberRoleResp) ProtoMessage()    {}

type AdminSetChannelCreationReq struct {
	ServerId             string `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	AllowChannelCreation bool   `protobuf:"varint,2,opt,name=allow_channel_creation,json=allowChannelCreation,proto3" json:"allow_channel_creation,omitempty"`
}

func (m *AdminSetChannelCreationReq) Reset()         { *m = AdminSetChannelCreationReq{} }
func (m *AdminSetChannelCreationReq) String() string { return proto.CompactTextString(m) }
func (*AdminSetChannelCreationReq) ProtoMessage()    {}

type AdminSetChannelCreationResp struct {
	Ok                   bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ServerId             string `protobuf:"bytes,2,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	AllowChannelCreation bool   `protobuf:"varint,3,opt,name=allow_channel_creation,json=allowChannelCreation,proto3" json:"allow_channel_creation,omitempty"`
	Message              string `protobuf:"bytes,4,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *AdminSetChannelCreationResp) Reset()         { *m = AdminSetChannelCreationResp{} }
func (m *AdminSetChannelCreationResp) String() string { return proto.CompactTextString(m) }
func (*AdminSetChannelCreationResp) ProtoMessage()    {}

type JoinServerReq struct {
	ServerId string `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
}

func (m *JoinServerReq) Reset()         { *m = JoinServerReq{} }
func (m *JoinServerReq) String() string { return proto.CompactTextString(m) }
func (*JoinServerReq) ProtoMessage()    {}

type JoinServerResp struct {
	Ok       bool           `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ServerId string         `protobuf:"bytes,2,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	Server   *ServerSummary `protobuf:"bytes,3,opt,name=server,proto3" json:"server,omitempty"`
	Message  string         `protobuf:"bytes,4,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *JoinServerResp) Reset()         { *m = JoinServerResp{} }
func (m *JoinServerResp) String() string { return proto.CompactTextString(m) }
func (*JoinServerResp) ProtoMessage()    {}

type InviteServerMemberReq struct {
	ServerId     string `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	TargetUserId string `protobuf:"bytes,2,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
}

func (m *InviteServerMemberReq) Reset()         { *m = InviteServerMemberReq{} }
func (m *InviteServerMemberReq) String() string { return proto.CompactTextString(m) }
func (*InviteServerMemberReq) ProtoMessage()    {}

type InviteServerMemberResp struct {
	Ok           bool           `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ServerId     string         `protobuf:"bytes,2,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	TargetUserId string         `protobuf:"bytes,3,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Server       *ServerSummary `protobuf:"bytes,4,opt,name=server,proto3" json:"server,omitempty"`
	Message      string         `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *InviteServerMemberResp) Reset()         { *m = InviteServerMemberResp{} }
func (m *InviteServerMemberResp) String() string { return proto.CompactTextString(m) }
func (*InviteServerMemberResp) ProtoMessage()    {}

type KickServerMemberReq struct {
	ServerId     string `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	TargetUserId string `protobuf:"bytes,2,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
}

func (m *KickServerMemberReq) Reset()         { *m = KickServerMemberReq{} }
func (m *KickServerMemberReq) String() string { return proto.CompactTextString(m) }
func (*KickServerMemberReq) ProtoMessage()    {}

type KickServerMemberResp struct {
	Ok           bool           `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ServerId     string         `protobuf:"bytes,2,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	TargetUserId string         `protobuf:"bytes,3,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Server       *ServerSummary `protobuf:"bytes,4,opt,name=server,proto3" json:"server,omitempty"`
	Message      string         `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *KickServerMemberResp) Reset()         { *m = KickServerMemberResp{} }
func (m *KickServerMemberResp) String() string { return proto.CompactTextString(m) }
func (*KickServerMemberResp) ProtoMessage()    {}

type BanServerMemberReq struct {
	ServerId     string `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	TargetUserId string `protobuf:"bytes,2,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
}

func (m *BanServerMemberReq) Reset()         { *m = BanServerMemberReq{} }
func (m *BanServerMemberReq) String() string { return proto.CompactTextString(m) }
func (*BanServerMemberReq) ProtoMessage()    {}

type BanServerMemberResp struct {
	Ok           bool           `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ServerId     string         `protobuf:"bytes,2,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	TargetUserId string         `protobuf:"bytes,3,opt,name=target_user_id,json=targetUserId,proto3" json:"target_user_id,omitempty"`
	Server       *ServerSummary `protobuf:"bytes,4,opt,name=server,proto3" json:"server,omitempty"`
	Message      string         `protobuf:"bytes,5,opt,name=message,proto3" json:"message,omitempty"`
}

func (m *BanServerMemberResp) Reset()         { *m = BanServerMemberResp{} }
func (m *BanServerMemberResp) String() string { return proto.CompactTextString(m) }
func (*BanServerMemberResp) ProtoMessage()    {}

type ServerMemberSummary struct {
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

func (m *ServerMemberSummary) Reset()         { *m = ServerMemberSummary{} }
func (m *ServerMemberSummary) String() string { return proto.CompactTextString(m) }
func (*ServerMemberSummary) ProtoMessage()    {}

type ListServerMembersReq struct {
	ServerId      string `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	AfterMemberId uint64 `protobuf:"varint,2,opt,name=after_member_id,json=afterMemberId,proto3" json:"after_member_id,omitempty"`
	Limit         uint32 `protobuf:"varint,3,opt,name=limit,proto3" json:"limit,omitempty"`
}

func (m *ListServerMembersReq) Reset()         { *m = ListServerMembersReq{} }
func (m *ListServerMembersReq) String() string { return proto.CompactTextString(m) }
func (*ListServerMembersReq) ProtoMessage()    {}

type ListServerMembersResp struct {
	ServerId          string                 `protobuf:"bytes,1,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	Members           []*ServerMemberSummary `protobuf:"bytes,2,rep,name=members,proto3" json:"members,omitempty"`
	NextAfterMemberId uint64                 `protobuf:"varint,3,opt,name=next_after_member_id,json=nextAfterMemberId,proto3" json:"next_after_member_id,omitempty"`
	HasMore           bool                   `protobuf:"varint,4,opt,name=has_more,json=hasMore,proto3" json:"has_more,omitempty"`
}

func (m *ListServerMembersResp) Reset()         { *m = ListServerMembersResp{} }
func (m *ListServerMembersResp) String() string { return proto.CompactTextString(m) }
func (*ListServerMembersResp) ProtoMessage()    {}

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
	ChannelId   string          `protobuf:"bytes,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	ClientMsgId string          `protobuf:"bytes,2,opt,name=client_msg_id,json=clientMsgId,proto3" json:"client_msg_id,omitempty"`
	MessageType MessageType     `protobuf:"varint,3,opt,name=message_type,json=messageType,proto3,enum=meshserver.session.v1.MessageType" json:"message_type,omitempty"`
	Content     *MessageContent `protobuf:"bytes,4,opt,name=content,proto3" json:"content,omitempty"`
}

func (m *SendMessageReq) Reset()         { *m = SendMessageReq{} }
func (m *SendMessageReq) String() string { return proto.CompactTextString(m) }
func (*SendMessageReq) ProtoMessage()    {}

type SendMessageAck struct {
	Ok           bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	ChannelId    string `protobuf:"bytes,2,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
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
	ChannelId    string          `protobuf:"bytes,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
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
	ChannelId string `protobuf:"bytes,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	AckedSeq  uint64 `protobuf:"varint,2,opt,name=acked_seq,json=ackedSeq,proto3" json:"acked_seq,omitempty"`
}

func (m *ChannelDeliverAck) Reset()         { *m = ChannelDeliverAck{} }
func (m *ChannelDeliverAck) String() string { return proto.CompactTextString(m) }
func (*ChannelDeliverAck) ProtoMessage()    {}

type ChannelReadUpdate struct {
	ChannelId   string `protobuf:"bytes,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	LastReadSeq uint64 `protobuf:"varint,2,opt,name=last_read_seq,json=lastReadSeq,proto3" json:"last_read_seq,omitempty"`
}

func (m *ChannelReadUpdate) Reset()         { *m = ChannelReadUpdate{} }
func (m *ChannelReadUpdate) String() string { return proto.CompactTextString(m) }
func (*ChannelReadUpdate) ProtoMessage()    {}

type SyncChannelReq struct {
	ChannelId string `protobuf:"bytes,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	AfterSeq  uint64 `protobuf:"varint,2,opt,name=after_seq,json=afterSeq,proto3" json:"after_seq,omitempty"`
	Limit     uint32 `protobuf:"varint,3,opt,name=limit,proto3" json:"limit,omitempty"`
}

func (m *SyncChannelReq) Reset()         { *m = SyncChannelReq{} }
func (m *SyncChannelReq) String() string { return proto.CompactTextString(m) }
func (*SyncChannelReq) ProtoMessage()    {}

type SyncChannelResp struct {
	ChannelId    string          `protobuf:"bytes,1,opt,name=channel_id,json=channelId,proto3" json:"channel_id,omitempty"`
	Messages     []*MessageEvent `protobuf:"bytes,2,rep,name=messages,proto3" json:"messages,omitempty"`
	NextAfterSeq uint64          `protobuf:"varint,3,opt,name=next_after_seq,json=nextAfterSeq,proto3" json:"next_after_seq,omitempty"`
	HasMore      bool            `protobuf:"varint,4,opt,name=has_more,json=hasMore,proto3" json:"has_more,omitempty"`
}

func (m *SyncChannelResp) Reset()         { *m = SyncChannelResp{} }
func (m *SyncChannelResp) String() string { return proto.CompactTextString(m) }
func (*SyncChannelResp) ProtoMessage()    {}
